package tracker

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/vsariola/sointu"
)

// The automation as the remote tools show and change it. Slots are numbered
// from 1, as the tracker and the hosts show them (A1 to A64).

// describeAutomation lists the lanes of the song, a line each.
func (r *Remote) describeAutomation() string {
	m := (*Model)(r)
	lanes := m.d.Song.Automation
	if len(lanes) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "automation (slot A1 to A%d: a parameter of the plugin in the host, offsetting a parameter in steps):\n", sointu.AutomationSlots)
	for _, l := range lanes {
		fmt.Fprintf(&b, "  A%d ", l.Slot+1)
		if l.Name != "" {
			fmt.Fprintf(&b, "%q ", l.Name)
		}
		if len(l.Targets) == 0 {
			b.WriteString("bound to nothing (for automation units with that slot)")
		} else {
			b.WriteString("offsets ")
			b.WriteString(r.describeTargets(l.Targets))
		}
		if n := len(l.Points); n == 0 {
			b.WriteString(", no points")
		} else {
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, p := range l.Points {
				lo, hi = min(lo, p.Value), max(hi, p.Value)
			}
			fmt.Fprintf(&b, ", %d points, rows %.4g to %.4g, %+.4g to %+.4g steps", n, l.Points[0].Row, l.Points[n-1].Row, lo, hi)
		}
		if live := m.Automation().Live(l.Slot); live != 0 {
			fmt.Fprintf(&b, ", the host has it at %+.4g now", live)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// describeTargets lists the targets of a lane: the unit and the parameter,
// the amount when it is not 1, and what the tracker names them.
func (r *Remote) describeTargets(targets []sointu.LaneTarget) string {
	m := (*Model)(r)
	var parts []string
	for _, t := range targets {
		s := fmt.Sprintf("#%d %s", t.Unit, t.Param)
		if t.Amount != 1 {
			s += fmt.Sprintf(" times %.4g", t.Amount)
		}
		if name := m.Automation().targetName(t); name != "" {
			s += " (" + name + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// Automation returns the points of the lane of a slot, numbered from 1.
func (r *Remote) Automation(slot int) (string, error) {
	m := (*Model)(r)
	i, ok := m.d.Song.Automation.Find(slot - 1)
	if !ok {
		return "", fmt.Errorf("slot A%d has no lane; get_song lists the lanes", slot)
	}
	l := m.d.Song.Automation[i]
	var b strings.Builder
	b.WriteString(strings.TrimSuffix(r.describeAutomation(), "\n"))
	fmt.Fprintf(&b, "\nA%d, points as [row, offset in steps], linear between them (%d rows per beat, %d rows in the song):\n", slot, m.d.Song.RowsPerBeat, m.d.Song.Score.LengthInRows())
	for _, p := range l.Points {
		fmt.Fprintf(&b, " [%.6g, %.6g]", p.Row, p.Value)
	}
	if len(l.Points) == 0 {
		b.WriteString(" none")
	}
	return b.String(), nil
}

// Automate makes a parameter of a unit of an instrument a target of the
// lane of an automation slot: of a new lane in the first free slot for slot
// 0; a slot that has a lane gets it as one more target. amount, if given,
// is the part of the lane that it gets, -1 to 1; else 1, or what it has.
// With remove it takes the parameter out of its lane, and the lane with its
// points if it was the last target. With name, the slot gets the name,
// which the host shows, also when the parameter is a target already; ""
// gives it back the name by what it is bound to.
func (r *Remote) Automate(unit int, param string, slot int, remove bool, name *string, amount *float64) (string, error) {
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		m := (*Model)(r)
		loc, err := r.findUnit(unit)
		if err != nil {
			return remoteFocus{}, "", err
		}
		focus := remoteFocus{unit: unit, instr: loc.instr, module: loc.module}
		if loc.instr < 0 {
			return focus, "", errors.New("automation reaches the units of the instruments only: automate a parameter of the module unit instead")
		}
		if remove {
			i, _, bound := m.d.Song.Automation.FindParam(unit, param)
			if !bound {
				return focus, "", fmt.Errorf("%s of unit %d is not automated", param, unit)
			}
			s := m.d.Song.Automation[i].Slot
			m.Automation().Unbind(unit, param)
			if _, ok := m.d.Song.Automation.Find(s); ok {
				return focus, fmt.Sprintf("removed %s of unit %d from automation slot A%d, which keeps its other targets", param, unit, s+1), nil
			}
			return focus, fmt.Sprintf("removed the automation of %s of unit %d: slot A%d is free", param, unit, s+1), nil
		}
		i, j, bound := m.d.Song.Automation.FindParam(unit, param)
		if bound && amount == nil && (slot == 0 || slot-1 == m.d.Song.Automation[i].Slot) {
			if name == nil {
				return focus, "", fmt.Errorf("%s of unit %d is a target of slot A%d already", param, unit, m.Automation().slotOfLane(i)+1)
			}
			// the lane is there: naming it
			s := m.d.Song.Automation[i].Slot
			if err := m.Automation().Rename(s, *name); err != nil {
				return focus, "", err
			}
			return focus, fmt.Sprintf("automation slot A%d is named %q in the host", s+1, m.Automation().HostNames()[s]), nil
		}
		a := 1.0
		if amount != nil {
			a = *amount
		} else if bound {
			a = m.d.Song.Automation[i].Targets[j].Amount
		}
		if bound && slot == 0 {
			slot = m.d.Song.Automation[i].Slot + 1 // a new amount
		}
		if err := m.Automation().Bind(unit, param, slot-1, a); err != nil {
			return focus, "", err
		}
		i, _, _ = m.d.Song.Automation.FindParam(unit, param)
		s := m.d.Song.Automation[i].Slot
		if name != nil {
			if err := m.Automation().Rename(s, *name); err != nil {
				return focus, "", err
			}
		}
		l := m.d.Song.Automation[i]
		return focus, fmt.Sprintf("automation slot A%d, named %q in the host, offsets %s", s+1, m.Automation().HostNames()[s], r.describeTargets(l.Targets)), nil
	})
}

// SetAutomation sets the points of the lane of a slot, numbered from 1:
// rows of the song and offsets in steps of the parameter.
func (r *Remote) SetAutomation(slot int, points [][2]float64) (string, error) {
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		m := (*Model)(r)
		curve := make(sointu.AutomationCurve, len(points))
		for i, p := range points {
			curve[i] = sointu.AutomationPoint{Row: p[0], Value: p[1]}
		}
		focus := remoteFocus{instr: -1, module: -1}
		if err := m.Automation().SetPoints(slot-1, curve); err != nil {
			return focus, "", err
		}
		return focus, fmt.Sprintf("set the %d points of automation slot A%d\n%s", len(points), slot, strings.TrimSuffix(r.describeAutomation(), "\n")), nil
	})
}

// automationChanges lists what changed in the automation.
func automationChanges(old, now sointu.Automation) (lines []string) {
	for _, l := range now {
		i, ok := old.Find(l.Slot)
		switch {
		case !ok:
			lines = append(lines, fmt.Sprintf("automation: slot A%d added (%s, %d points)", l.Slot+1, targetsShort(l.Targets), len(l.Points)))
		case old[i].Name != l.Name:
			lines = append(lines, fmt.Sprintf("automation: slot A%d is named %q (was %q)", l.Slot+1, l.Name, old[i].Name))
		case !reflect.DeepEqual(old[i].Targets, l.Targets):
			lines = append(lines, fmt.Sprintf("automation: slot A%d offsets %s (was %s)", l.Slot+1, targetsShort(l.Targets), targetsShort(old[i].Targets)))
		case !reflect.DeepEqual(old[i].Points, l.Points):
			lines = append(lines, fmt.Sprintf("automation: slot A%d has %d points (had %d): e.g. recorded from the host", l.Slot+1, len(l.Points), len(old[i].Points)))
		}
	}
	for _, l := range old {
		if _, ok := now.Find(l.Slot); !ok {
			lines = append(lines, fmt.Sprintf("automation: slot A%d removed", l.Slot+1))
		}
	}
	return lines
}

// targetsShort lists the targets of a lane as unit and parameter, with the
// amount when it is not 1.
func targetsShort(targets []sointu.LaneTarget) string {
	if len(targets) == 0 {
		return "nothing"
	}
	var parts []string
	for _, t := range targets {
		s := fmt.Sprintf("#%d %s", t.Unit, t.Param)
		if t.Amount != 1 {
			s += fmt.Sprintf(" times %.4g", t.Amount)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
