package tracker

import (
	"fmt"
	"math"
	"strings"

	"github.com/vsariola/sointu"
)

// AutomationModel is the automation of the song: the lanes of the slots
// that a plugin shows its host as parameters, and what they are bound to.
type AutomationModel Model

func (m *Model) Automation() *AutomationModel { return (*AutomationModel)(m) }

// Lanes returns the lanes of the song. The caller must not change them.
func (m *AutomationModel) Lanes() sointu.Automation { return m.d.Song.Automation }

// Live returns the value that the host last gave the slot, in steps of a
// parameter.
func (m *AutomationModel) Live(slot int) float32 {
	if slot < 0 || slot >= len(m.playerStatus.Automation) {
		return 0
	}
	return m.playerStatus.Automation[slot]
}

// HostValues returns the values of the automation slots that were saved
// with the song by a plugin host.
func (m *AutomationModel) HostValues() (values [sointu.AutomationSlots]float32) {
	copy(values[:], m.d.HostAutomation)
	return values
}

// target returns what a lane can be bound to for the parameter under the
// cursor: a parameter of a unit of an instrument that a send can modulate.
func (m *AutomationModel) target() (unit int, param string, ok bool) {
	point := (*Model)(m).Params().Table().Cursor()
	item := (*Model)(m).Params().Item(point)
	return m.targetOf(&item)
}

func (m *AutomationModel) targetOf(item *Parameter) (unit int, param string, ok bool) {
	switch item.vtable.(type) {
	case nil, *delayTimeParameter:
		return 0, "", false
	}
	u, name, ok := item.bindTarget() // for a parameter of a module unit, the module unit and p1 to p8
	if !ok || u.ID == 0 || !sointu.CanAutomate(u.Type, name) {
		return 0, "", false
	}
	if _, _, err := m.d.Song.Patch.FindUnit(u.ID); err != nil {
		return 0, "", false // a unit of a module: lanes only reach the units of the instruments
	}
	return u.ID, name, true
}

// SlotOf returns the slot of the lane bound to the parameter.
func (m *AutomationModel) SlotOf(item *Parameter) (slot int, ok bool) {
	if len(m.d.Song.Automation) == 0 {
		return 0, false
	}
	unit, param, ok := m.targetOf(item)
	if !ok {
		return 0, false
	}
	i, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return 0, false
	}
	return m.d.Song.Automation[i].Slot, true
}

// Automated returns a Bool telling whether the parameter under the cursor
// has a lane. Setting it binds the first free slot to the parameter, so
// that the host can automate it; clearing it removes the lane and its
// points.
func (m *AutomationModel) Automated() Bool { return MakeBool((*automated)(m)) }

type automated AutomationModel

func (m *automated) Enabled() bool {
	_, _, ok := (*AutomationModel)(m).target()
	return ok
}

func (m *automated) Value() bool {
	unit, param, ok := (*AutomationModel)(m).target()
	if !ok {
		return false
	}
	_, ok = m.d.Song.Automation.FindParam(unit, param)
	return ok
}

func (m *automated) SetValue(val bool) {
	unit, param, ok := (*AutomationModel)(m).target()
	if !ok {
		return
	}
	if val {
		if err := (*AutomationModel)(m).Bind(unit, param, -1); err != nil {
			(*Model)(m).Alerts().Add(err.Error(), Warning)
		}
		return
	}
	(*AutomationModel)(m).Unbind(unit, param)
}

// Bind gives the parameter of the unit with the ID a lane: that of the
// slot, which is then bound to nothing else, or with a negative slot the
// first free one, or a lane without a binding that has no points.
func (m *AutomationModel) Bind(unit int, param string, slot int) error {
	a := m.d.Song.Automation
	if _, ok := a.FindParam(unit, param); ok {
		return fmt.Errorf("the parameter has a lane already")
	}
	i, j, err := m.d.Song.Patch.FindUnit(unit)
	if err != nil {
		return fmt.Errorf("automation reaches the units of the instruments only: %w", err)
	}
	if !sointu.CanAutomate(m.d.Song.Patch[i].Units[j].Type, param) {
		return fmt.Errorf("%v of a %v unit cannot be modulated, so not automated", param, m.d.Song.Patch[i].Units[j].Type)
	}
	if slot >= sointu.AutomationSlots {
		return fmt.Errorf("there are %d automation slots", sointu.AutomationSlots)
	}
	if slot < 0 {
		var ok bool
		if slot, ok = a.FreeSlot(); !ok {
			return fmt.Errorf("all %d automation slots are in use", sointu.AutomationSlots)
		}
	}
	defer (*Model)(m).change("AutomationBind", PatchChange, MajorChange)()
	a = a.Copy()
	if k, ok := a.Find(slot); ok {
		a[k].Unit, a[k].Param = unit, param
	} else {
		a = append(a, sointu.Lane{Slot: slot, Unit: unit, Param: param})
	}
	m.d.Song.Automation = a
	(*Model)(m).Alerts().Add(fmt.Sprintf("Automation slot %d offsets %s", slot+1, m.SlotName(slot)), Info)
	return nil
}

// Unbind removes the lane of the parameter, with its points.
func (m *AutomationModel) Unbind(unit int, param string) bool {
	i, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return false
	}
	defer (*Model)(m).change("AutomationUnbind", PatchChange, MajorChange)()
	a := m.d.Song.Automation.Copy()
	m.d.Song.Automation = append(a[:i], a[i+1:]...)
	if len(m.d.Song.Automation) == 0 {
		m.d.Song.Automation = nil
	}
	return true
}

// SetPoints sets the points of the lane of the slot, which is made if it
// has none.
func (m *AutomationModel) SetPoints(slot int, points sointu.AutomationCurve) error {
	if slot < 0 || slot >= sointu.AutomationSlots {
		return fmt.Errorf("there are %d automation slots", sointu.AutomationSlots)
	}
	for i, p := range points {
		if math.IsNaN(p.Row) || math.IsNaN(p.Value) || p.Row < 0 || math.Abs(p.Value) > sointu.AutomationRange {
			return fmt.Errorf("point %d: a point is a row from 0 and an offset of at most ±%d steps", i+1, sointu.AutomationRange)
		}
		if i > 0 && p.Row < points[i-1].Row {
			return fmt.Errorf("point %d: the points are in the order of their rows", i+1)
		}
	}
	defer (*Model)(m).change("AutomationPoints", PatchChange, MajorChange)()
	a := m.d.Song.Automation.Copy()
	k, ok := a.Find(slot)
	if !ok {
		k, a = len(a), append(a, sointu.Lane{Slot: slot})
	}
	a[k].Points = append(sointu.AutomationCurve{}, points...)
	if len(points) == 0 {
		a[k].Points = nil
		if a[k].Unit == 0 {
			a = append(a[:k], a[k+1:]...) // nothing is left of it
		}
	}
	m.d.Song.Automation = a
	return nil
}

// MaxAutomationName is the longest name of a slot, in bytes.
const MaxAutomationName = 64

// Name returns a String with the name of the lane of the parameter under
// the cursor, which a plugin host shows for its slot: empty for the name by
// what the slot is bound to (DefaultSlotName).
func (m *AutomationModel) Name() String { return MakeString((*automationName)(m)) }

type automationName AutomationModel

func (v *automationName) lane() (int, bool) {
	unit, param, ok := (*AutomationModel)(v).target()
	if !ok {
		return 0, false
	}
	return v.d.Song.Automation.FindParam(unit, param)
}

func (v *automationName) Value() string {
	if i, ok := v.lane(); ok {
		return v.d.Song.Automation[i].Name
	}
	return ""
}

func (v *automationName) SetValue(value string) bool {
	i, ok := v.lane()
	if !ok {
		return false
	}
	return (*AutomationModel)(v).Rename(v.d.Song.Automation[i].Slot, value) == nil
}

// Rename names the slot, which must have a lane; "" gives it back the name
// by what it is bound to.
func (m *AutomationModel) Rename(slot int, name string) error {
	i, ok := m.d.Song.Automation.Find(slot)
	if !ok {
		return fmt.Errorf("automation slot %d has no lane", slot+1)
	}
	name = strings.Join(strings.Fields(name), " ") // one line
	if len(name) > MaxAutomationName {
		return fmt.Errorf("the name of a slot is at most %d bytes", MaxAutomationName)
	}
	if m.d.Song.Automation[i].Name == name {
		return nil
	}
	defer (*Model)(m).change("AutomationName", PatchChange, MinorChange)()
	a := m.d.Song.Automation.Copy()
	a[i].Name = name
	m.d.Song.Automation = a
	return nil
}

// SlotName returns the name of the slot, as a host shows it: the name of
// its lane, or without one DefaultSlotName.
func (m *AutomationModel) SlotName(slot int) string {
	if k, ok := m.d.Song.Automation.Find(slot); ok && m.d.Song.Automation[k].Name != "" {
		return m.d.Song.Automation[k].Name
	}
	return m.DefaultSlotName(slot)
}

// DefaultSlotName returns what the slot offsets: the instrument, the unit
// and the parameter, or "" for a slot that is bound to nothing.
func (m *AutomationModel) DefaultSlotName(slot int) string {
	k, ok := m.d.Song.Automation.Find(slot)
	if !ok || m.d.Song.Automation[k].Unit == 0 {
		return ""
	}
	l := m.d.Song.Automation[k]
	i, j, err := m.d.Song.Patch.FindUnit(l.Unit)
	if err != nil {
		return ""
	}
	u := &m.d.Song.Patch[i].Units[j]
	what, param := u.Type, l.Param
	if u.Type == "module" {
		if mi, ok := m.d.Song.Modules.Find(u.Parameters["module"]); ok {
			mod := &m.d.Song.Modules[mi]
			what = mod.Name
			var k int
			if _, err := fmt.Sscanf(l.Param, "p%d", &k); err == nil && k >= 1 && k <= len(mod.Params) && mod.Params[k-1].Name != "" {
				param = mod.Params[k-1].Name
			}
		}
	}
	if u.Comment != "" {
		what = u.Comment
	}
	return fmt.Sprintf("%s: %s %s", m.d.Song.Patch[i].Name, what, param)
}

// Slot returns the slot of the lane of the parameter under the cursor.
func (m *AutomationModel) Slot() (slot int, ok bool) {
	i, ok := (*automationName)(m).lane()
	if !ok {
		return 0, false
	}
	return m.d.Song.Automation[i].Slot, true
}

// SlotNames returns the SlotName of every slot.
func (m *AutomationModel) SlotNames() (names [sointu.AutomationSlots]string) {
	for _, l := range m.d.Song.Automation {
		if l.Slot >= 0 && l.Slot < len(names) {
			names[l.Slot] = m.SlotName(l.Slot)
		}
	}
	return names
}

// HostNames returns the name of every slot as a plugin host shows it: the
// name that the lane was given, or else the slot and what it is bound to,
// e.g. "A3 Bass: filter frequency"; "" for a slot without a lane or bound to
// nothing and not named.
func (m *AutomationModel) HostNames() (names [sointu.AutomationSlots]string) {
	for _, l := range m.d.Song.Automation {
		if l.Slot < 0 || l.Slot >= len(names) {
			continue
		}
		if l.Name != "" {
			names[l.Slot] = l.Name
		} else if d := m.DefaultSlotName(l.Slot); d != "" {
			names[l.Slot] = fmt.Sprintf("A%d %s", l.Slot+1, d)
		}
	}
	return names
}

// fixAutomation removes the lanes that are bound to units that are gone,
// or to parameters that they do not have, and those of slots that do not
// exist or have a lane already.
func (m *Model) fixAutomation() {
	a := m.d.Song.Automation
	if len(a) == 0 {
		return
	}
	seen := map[int]bool{}
	valid := func(l sointu.Lane) bool {
		if l.Slot < 0 || l.Slot >= sointu.AutomationSlots || seen[l.Slot] {
			return false
		}
		seen[l.Slot] = true
		if l.Unit == 0 {
			return true
		}
		// disabled units keep their lanes
		for _, instr := range m.d.Song.Patch {
			for _, u := range instr.Units {
				if u.ID == l.Unit {
					return sointu.CanAutomate(u.Type, l.Param)
				}
			}
		}
		return false
	}
	var out sointu.Automation
	for i, l := range a {
		if valid(l) {
			if out != nil {
				out = append(out, l)
			}
			continue
		}
		if out == nil {
			out = append(sointu.Automation{}, a[:i]...)
		}
	}
	if out != nil || len(a) == 0 {
		m.d.Song.Automation = out
		if len(out) == 0 {
			m.d.Song.Automation = nil
		}
	}
}
