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
		if l.Unit == 0 {
			b.WriteString("bound to nothing (for automation units with that slot)")
		} else {
			fmt.Fprintf(&b, "offsets #%d %s (%s)", l.Unit, l.Param, m.Automation().SlotName(l.Slot))
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

// Automate binds an automation slot to a parameter of a unit of an
// instrument, the first free one for slot 0, or with remove takes the lane
// of the parameter away, with its points.
func (r *Remote) Automate(unit int, param string, slot int, remove bool) (string, error) {
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
			if !m.Automation().Unbind(unit, param) {
				return focus, "", fmt.Errorf("%s of unit %d has no lane", param, unit)
			}
			return focus, fmt.Sprintf("removed the automation of %s of unit %d", param, unit), nil
		}
		if err := m.Automation().Bind(unit, param, slot-1); err != nil {
			return focus, "", err
		}
		i, _ := m.d.Song.Automation.FindParam(unit, param)
		return focus, fmt.Sprintf("automation slot A%d offsets %s of unit %d", m.d.Song.Automation[i].Slot+1, param, unit), nil
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
			lines = append(lines, fmt.Sprintf("automation: slot A%d added (#%d %s, %d points)", l.Slot+1, l.Unit, l.Param, len(l.Points)))
		case old[i].Unit != l.Unit || old[i].Param != l.Param:
			lines = append(lines, fmt.Sprintf("automation: slot A%d offsets #%d %s (was #%d %s)", l.Slot+1, l.Unit, l.Param, old[i].Unit, old[i].Param))
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
