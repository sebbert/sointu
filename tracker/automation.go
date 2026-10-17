package tracker

import (
	"fmt"
	"math"
	"slices"
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
	i, _, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return 0, false
	}
	return m.d.Song.Automation[i].Slot, true
}

// AmountOf returns the amount of the target of a lane that the parameter
// is, if it is one: see sointu.LaneTarget.
func (m *AutomationModel) AmountOf(item *Parameter) (amount float64, ok bool) {
	if len(m.d.Song.Automation) == 0 {
		return 0, false
	}
	unit, param, ok := m.targetOf(item)
	if !ok {
		return 0, false
	}
	i, j, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return 0, false
	}
	return m.d.Song.Automation[i].Targets[j].Amount, true
}

// SetAmountOf sets the amount of the target of a lane that the parameter
// is, -1 to 1 in 64ths, as turning a knob does: a minor change.
func (m *AutomationModel) SetAmountOf(item *Parameter, amount float64) bool {
	unit, param, ok := m.targetOf(item)
	if !ok {
		return false
	}
	i, j, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return false
	}
	amount = math.Round(min(max(amount, -1), 1)*64) / 64
	if m.d.Song.Automation[i].Targets[j].Amount == amount {
		return false
	}
	defer (*Model)(m).change("AutomationAmount", PatchChange, MinorChange)()
	a := m.d.Song.Automation.Copy()
	a[i].Targets[j].Amount = amount
	m.d.Song.Automation = a
	return true
}

// NewLane returns an Action that gives the parameter a lane of its own, in
// the first free slot: enabled for a parameter that can be automated and is
// not a target of a lane.
func (m *AutomationModel) NewLane(item *Parameter) Action {
	unit, param, ok := m.targetOf(item)
	return MakeFuncAction(func() {
		if err := m.Bind(unit, param, -1, 1); err != nil {
			(*Model)(m).Alerts().Add(err.Error(), Warning)
		}
	}, func() bool {
		_, _, bound := m.d.Song.Automation.FindParam(unit, param)
		return ok && !bound
	})
}

// JoinLane returns an Action that makes the parameter one more target of
// the lane of the slot, with the amount it has if it is a target of
// another lane, else 1: enabled for a parameter that can be automated and
// is not a target of that lane.
func (m *AutomationModel) JoinLane(item *Parameter, slot int) Action {
	unit, param, ok := m.targetOf(item)
	return MakeFuncAction(func() {
		amount := 1.0
		if i, j, bound := m.d.Song.Automation.FindParam(unit, param); bound {
			amount = m.d.Song.Automation[i].Targets[j].Amount
		}
		if err := m.Bind(unit, param, slot, amount); err != nil {
			(*Model)(m).Alerts().Add(err.Error(), Warning)
		}
	}, func() bool {
		i, _, bound := m.d.Song.Automation.FindParam(unit, param)
		_, exists := m.d.Song.Automation.Find(slot)
		return ok && exists && (!bound || m.d.Song.Automation[i].Slot != slot)
	})
}

// RemoveLane returns an Action that removes the parameter from its lane,
// and the lane with its points if it was the last target: enabled for a
// parameter that is a target of a lane.
func (m *AutomationModel) RemoveLane(item *Parameter) Action {
	unit, param, ok := m.targetOf(item)
	return MakeFuncAction(func() { m.Unbind(unit, param) }, func() bool {
		_, _, bound := m.d.Song.Automation.FindParam(unit, param)
		return ok && bound
	})
}

// Automated returns a Bool telling whether the parameter under the cursor
// is a target of a lane. Setting it gives the parameter a lane of its own,
// in the first free slot, so that the host can automate it; clearing it
// removes it from its lane, and the lane with its points if it was the last
// target.
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
	_, _, ok = m.d.Song.Automation.FindParam(unit, param)
	return ok
}

func (m *automated) SetValue(val bool) {
	unit, param, ok := (*AutomationModel)(m).target()
	if !ok {
		return
	}
	if val {
		if err := (*AutomationModel)(m).Bind(unit, param, -1, 1); err != nil {
			(*Model)(m).Alerts().Add(err.Error(), Warning)
		}
		return
	}
	(*AutomationModel)(m).Unbind(unit, param)
}

// Bind makes the parameter of the unit with the ID a target of the lane of
// the slot, with the amount (-1 to 1, see sointu.LaneTarget): with a
// negative slot, of a new lane in the first free slot, or a lane without
// targets that has no points. A slot that has a lane gets the parameter as
// one more target, so that it offsets several parameters at once. A
// parameter that is a target of the lane already gets the amount; one that
// is a target of another lane moves to this one.
func (m *AutomationModel) Bind(unit int, param string, slot int, amount float64) error {
	a := m.d.Song.Automation
	i, j, err := m.d.Song.Patch.FindUnit(unit)
	if err != nil {
		return fmt.Errorf("automation reaches the units of the instruments only: %w", err)
	}
	if !sointu.CanAutomate(m.d.Song.Patch[i].Units[j].Type, param) {
		return fmt.Errorf("%v of a %v unit cannot be modulated, so not automated", param, m.d.Song.Patch[i].Units[j].Type)
	}
	if math.IsNaN(amount) || amount < -1 || amount > 1 {
		return fmt.Errorf("the amount of a target is -1 to 1")
	}
	if slot >= sointu.AutomationSlots {
		return fmt.Errorf("there are %d automation slots", sointu.AutomationSlots)
	}
	li, ti, bound := a.FindParam(unit, param)
	if slot < 0 {
		if bound {
			return fmt.Errorf("the parameter has a lane already")
		}
		var ok bool
		if slot, ok = a.FreeSlot(); !ok {
			return fmt.Errorf("all %d automation slots are in use", sointu.AutomationSlots)
		}
	}
	if bound && a[li].Slot == slot && a[li].Targets[ti].Amount == amount {
		return nil
	}
	defer (*Model)(m).change("AutomationBind", PatchChange, MajorChange)()
	a = a.Copy()
	if bound && a[li].Slot == slot {
		a[li].Targets[ti].Amount = amount
		m.d.Song.Automation = a
		return nil
	}
	if bound { // from another lane
		a = a.RemoveTarget(li, ti)
	}
	t := sointu.LaneTarget{Unit: unit, Param: param, Amount: amount}
	if k, ok := a.Find(slot); ok {
		a[k].Targets = append(a[k].Targets, t)
	} else {
		a = append(a, sointu.Lane{Slot: slot, Targets: []sointu.LaneTarget{t}})
	}
	m.d.Song.Automation = a
	(*Model)(m).Alerts().Add(fmt.Sprintf("Automation slot %d offsets %s", slot+1, m.SlotName(slot)), Info)
	return nil
}

// Unbind removes the parameter from its lane, and the lane with its points
// if it was its last target.
func (m *AutomationModel) Unbind(unit int, param string) bool {
	i, j, ok := m.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return false
	}
	defer (*Model)(m).change("AutomationUnbind", PatchChange, MajorChange)()
	m.d.Song.Automation = m.d.Song.Automation.Copy().RemoveTarget(i, j)
	return true
}

// slotOfLane returns the slot of lane i.
func (m *AutomationModel) slotOfLane(i int) int { return m.d.Song.Automation[i].Slot }

// SlotNumber returns an Int with the number of the slot (from 1) of the
// lane that the parameter under the cursor is a target of. Setting it moves
// the parameter to the lane of that slot, as one more target of it; a lane
// that the parameter was the only target of moves along with its points
// and name, if the slot has no lane.
func (m *AutomationModel) SlotNumber() Int { return MakeInt((*slotNumber)(m)) }

type slotNumber AutomationModel

func (v *slotNumber) Value() int {
	if slot, ok := (*AutomationModel)(v).Slot(); ok {
		return slot + 1
	}
	return 0
}

func (v *slotNumber) Range() RangeInclusive { return RangeInclusive{1, sointu.AutomationSlots} }

func (v *slotNumber) Enabled() bool {
	_, ok := (*AutomationModel)(v).Slot()
	return ok
}

func (v *slotNumber) SetValue(value int) bool {
	unit, param, ok := (*AutomationModel)(v).target()
	if !ok {
		return false
	}
	a := v.d.Song.Automation
	i, j, ok := a.FindParam(unit, param)
	slot := value - 1
	if !ok || slot < 0 || slot >= sointu.AutomationSlots || a[i].Slot == slot {
		return false
	}
	if _, taken := a.Find(slot); !taken && len(a[i].Targets) == 1 {
		defer (*Model)(v).change("AutomationSlot", PatchChange, MajorChange)()
		a = a.Copy()
		a[i].Slot = slot
		v.d.Song.Automation = a
		return true
	}
	return (*AutomationModel)(v).Bind(unit, param, slot, a[i].Targets[j].Amount) == nil
}

// Amount returns an Int with the amount of the parameter under the cursor
// in the lane that it is a target of, in 64ths: from -64 to 64.
func (m *AutomationModel) Amount() Int { return MakeInt((*laneAmount)(m)) }

type laneAmount AutomationModel

func (v *laneAmount) Value() int {
	unit, param, ok := (*AutomationModel)(v).target()
	if !ok {
		return 0
	}
	i, j, ok := v.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return 0
	}
	return int(math.Round(v.d.Song.Automation[i].Targets[j].Amount * 64))
}

func (v *laneAmount) Range() RangeInclusive { return RangeInclusive{-64, 64} }

func (v *laneAmount) Enabled() bool {
	_, ok := (*AutomationModel)(v).Slot()
	return ok
}

func (v *laneAmount) StringOf(value int) string {
	return fmt.Sprintf("%.2f", float64(value)/64)
}

func (v *laneAmount) SetValue(value int) bool {
	unit, param, ok := (*AutomationModel)(v).target()
	if !ok {
		return false
	}
	i, _, ok := v.d.Song.Automation.FindParam(unit, param)
	if !ok {
		return false
	}
	return (*AutomationModel)(v).Bind(unit, param, v.d.Song.Automation[i].Slot, float64(value)/64) == nil
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
		if len(a[k].Targets) == 0 {
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
	i, _, ok := v.d.Song.Automation.FindParam(unit, param)
	return i, ok
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
// and the parameter of its first target, and how many more it has, or ""
// for a slot that is bound to nothing.
func (m *AutomationModel) DefaultSlotName(slot int) string {
	k, ok := m.d.Song.Automation.Find(slot)
	if !ok || len(m.d.Song.Automation[k].Targets) == 0 {
		return ""
	}
	l := m.d.Song.Automation[k]
	name := m.targetName(l.Targets[0])
	if name == "" {
		return ""
	}
	if n := len(l.Targets) - 1; n > 0 {
		name += fmt.Sprintf(" +%d", n)
	}
	return name
}

// targetName returns the instrument, the unit and the parameter of a
// target, e.g. "Bass: filter frequency", or "" if the unit is gone.
func (m *AutomationModel) targetName(t sointu.LaneTarget) string {
	i, j, err := m.d.Song.Patch.FindUnit(t.Unit)
	if err != nil {
		return ""
	}
	u := &m.d.Song.Patch[i].Units[j]
	what, param := u.Type, t.Param
	if u.Type == "module" {
		if mi, ok := m.d.Song.Modules.Find(u.Parameters["module"]); ok {
			mod := &m.d.Song.Modules[mi]
			what = mod.Name
			var k int
			if _, err := fmt.Sscanf(t.Param, "p%d", &k); err == nil && k >= 1 && k <= len(mod.Params) && mod.Params[k-1].Name != "" {
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

// fixAutomation removes the targets of the lanes whose units are gone, or
// that are parameters that their units do not have, and the lanes that had
// targets and have none left, and those of slots that do not exist or have
// a lane already.
func (m *Model) fixAutomation() {
	a := m.d.Song.Automation
	if len(a) == 0 {
		return
	}
	valid := func(t sointu.LaneTarget) bool {
		// disabled units keep their targets
		for _, instr := range m.d.Song.Patch {
			for _, u := range instr.Units {
				if u.ID == t.Unit {
					return sointu.CanAutomate(u.Type, t.Param)
				}
			}
		}
		return false
	}
	seen := map[int]bool{}
	var out sointu.Automation
	changed := false
	for _, l := range a {
		if l.Slot < 0 || l.Slot >= sointu.AutomationSlots || seen[l.Slot] {
			changed = true
			continue
		}
		seen[l.Slot] = true
		if !slices.ContainsFunc(l.Targets, func(t sointu.LaneTarget) bool { return !valid(t) }) {
			out = append(out, l)
			continue
		}
		changed = true
		var targets []sointu.LaneTarget
		for _, t := range l.Targets {
			if valid(t) {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			l.Targets = targets
			out = append(out, l)
		}
	}
	if changed {
		m.d.Song.Automation = out
	}
}
