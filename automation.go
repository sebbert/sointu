package sointu

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"
)

// AutomationSlots is the number of automation slots: the parameters that a
// plugin shows its host. AutomationRange is how far a slot can offset a
// parameter either way, in steps of the parameter. AutomationScale is the
// number of values of a point in a step, as the synths have them.
const (
	AutomationSlots = 64
	AutomationRange = 128
	AutomationScale = 128
)

type (
	// Automation is the automation of a song: the lanes of the slots that
	// are in use, at most one for each slot.
	Automation []Lane

	// Lane is the automation of one slot: what the slot is bound to, and
	// its points. The value of the lane is added to the parameter Param of
	// the unit with the ID Unit, in steps of the parameter, on top of the
	// value that the unit has: Song.Expand puts an automation unit and a send
	// before the unit. With Unit 0 the lane is bound to nothing, and only
	// automation units with the slot play it.
	//
	// The value is that of the points, linear between them, that of the
	// first point before it and that of the last point after it; without
	// points, 0.
	Lane struct {
		Slot   int
		Unit   int             `yaml:",omitempty" json:",omitempty"`
		Param  string          `yaml:",omitempty" json:",omitempty"`
		Points AutomationCurve `yaml:",flow,omitempty" json:",omitempty"`
	}

	// AutomationCurve is the points of a lane, in the order of their rows.
	// Two points at the same row are a step.
	AutomationCurve []AutomationPoint

	// AutomationPoint is a point of a lane: a row of the song, counted from
	// its start and with a fraction, and the offset there, in steps of the
	// parameter. It is saved as [row, value].
	AutomationPoint struct{ Row, Value float64 }
)

func (p AutomationPoint) MarshalYAML() (any, error) {
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range [2]float64{p.Row, p.Value} {
		var c yaml.Node
		if err := c.Encode(v); err != nil {
			return nil, err
		}
		n.Content = append(n.Content, &c)
	}
	return n, nil
}

func (p *AutomationPoint) UnmarshalYAML(node *yaml.Node) error {
	var v [2]float64
	if err := node.Decode(&v); err != nil {
		return fmt.Errorf("an automation point is [row, value]: %w", err)
	}
	p.Row, p.Value = v[0], v[1]
	return nil
}

func (p AutomationPoint) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]float64{p.Row, p.Value})
}

func (p *AutomationPoint) UnmarshalJSON(data []byte) error {
	var v [2]float64
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("an automation point is [row, value]: %w", err)
	}
	p.Row, p.Value = v[0], v[1]
	return nil
}

// Copy makes a deep copy of the automation.
func (a Automation) Copy() Automation {
	if a == nil {
		return nil
	}
	ret := make(Automation, len(a))
	for i, l := range a {
		ret[i] = l
		ret[i].Points = slices.Clone(l.Points)
	}
	return ret
}

// Find returns the index of the lane of the slot.
func (a Automation) Find(slot int) (int, bool) {
	for i, l := range a {
		if l.Slot == slot {
			return i, true
		}
	}
	return 0, false
}

// FindParam returns the index of the lane bound to the parameter of the
// unit with the ID.
func (a Automation) FindParam(unit int, param string) (int, bool) {
	if unit == 0 {
		return 0, false
	}
	for i, l := range a {
		if l.Unit == unit && l.Param == param {
			return i, true
		}
	}
	return 0, false
}

// FreeSlot returns the first slot without a lane.
func (a Automation) FreeSlot() (int, bool) {
	for slot := 0; slot < AutomationSlots; slot++ {
		if _, ok := a.Find(slot); !ok {
			return slot, true
		}
	}
	return 0, false
}

// Active returns the lanes that do something when the song plays: those
// with a point that is not 0. The others add exactly nothing.
func (a Automation) Active() Automation {
	var ret Automation
	for _, l := range a {
		if slices.ContainsFunc(l.Points, func(p AutomationPoint) bool { return math.Round(p.Value*AutomationScale) != 0 }) {
			ret = append(ret, l)
		}
	}
	return ret
}

// Value returns the value of the curve at the row.
func (c AutomationCurve) Value(row float64) float64 {
	if len(c) == 0 {
		return 0
	}
	i := sort.Search(len(c), func(i int) bool { return c[i].Row > row })
	if i == 0 {
		return c[0].Value
	}
	if i == len(c) {
		return c[len(c)-1].Value
	}
	a, b := c[i-1], c[i]
	return a.Value + (b.Value-a.Value)*(row-a.Row)/(b.Row-a.Row)
}

// CanAutomate reports whether a lane can be bound to the named parameter of
// a unit of the type: the parameters that a send can modulate, as the lane
// reaches it through a send. For a module unit those are the parameters of
// its module.
func CanAutomate(unitType, param string) bool {
	for _, p := range UnitTypes[unitType].Params {
		if p.Name == param {
			return p.CanModulate
		}
	}
	return false
}

// automationArgs returns the points of a curve as an automation unit has
// them in its VarArgs: for each point, its time in samples from the start
// of the song and its value in 1/AutomationScale steps.
func (s *Song) automationArgs(c AutomationCurve) []int {
	spr := float64(s.SamplesPerRow())
	limit := float64(AutomationRange * AutomationScale)
	args := make([]int, 0, 2*len(c))
	for _, p := range c {
		t := math.Round(max(p.Row, 0) * spr)
		v := math.Round(min(max(p.Value*AutomationScale, -limit), limit))
		args = append(args, int(min(t, math.MaxInt32)), int(v))
	}
	return args
}

// insertAutomation returns the song with an automation unit and a send
// before every unit that a lane is bound to: the send adds the value of the
// lane to the parameter. The instruments without such units share their
// units with s.
func (s *Song) insertAutomation(exp *Expansion) Song {
	ret := *s
	copied := false
	seen := map[int]bool{}
	for _, l := range s.Automation {
		if l.Slot < 0 || l.Slot >= AutomationSlots || seen[l.Slot] {
			exp.Problems = append(exp.Problems, fmt.Errorf("automation: slot %d is out of range or has two lanes", l.Slot+1))
			continue
		}
		seen[l.Slot] = true
		if l.Unit == 0 {
			continue
		}
		i, j, err := ret.Patch.FindUnit(l.Unit)
		if err != nil {
			exp.Problems = append(exp.Problems, fmt.Errorf("automation: slot %d is bound to unit %d, which is not an enabled unit of an instrument", l.Slot+1, l.Unit))
			continue
		}
		target := ret.Patch[i].Units[j]
		if !CanAutomate(target.Type, l.Param) {
			exp.Problems = append(exp.Problems, fmt.Errorf("automation: slot %d is bound to %v of a %v unit, which cannot be modulated", l.Slot+1, l.Param, target.Type))
			continue
		}
		if !copied {
			ret.Patch = append(Patch{}, s.Patch...)
			copied = true
		}
		units := make([]Unit, 0, len(ret.Patch[i].Units)+2)
		units = append(units, ret.Patch[i].Units[:j]...)
		units = append(units,
			Unit{Type: "automation", Parameters: ParamMap{"slot": l.Slot}},
			Unit{Type: "send", Parameters: ParamMap{"stereo": 0, "amount": 128, "voice": 0, "target": l.Unit, "port": PortOf(target.Type, l.Param), "sendpop": 1}},
		)
		ret.Patch[i].Units = append(units, ret.Patch[i].Units[j:]...)
	}
	return ret
}

// fillAutomation gives the automation units of the song the points of the
// lanes of their slots, from the automation a, and removes the automation
// from the song. Units of instruments that are changed are copied first.
func (s *Song) fillAutomation(a Automation) {
	s.Automation = nil
	if len(a) == 0 {
		return
	}
	copied := false
	for i, instr := range s.Patch {
		unitsCopied := false
		for j, u := range instr.Units {
			if u.Type != "automation" {
				continue
			}
			k, ok := a.Find(u.Parameters["slot"])
			if !ok || len(a[k].Points) == 0 {
				continue
			}
			if !copied {
				s.Patch = append(Patch{}, s.Patch...)
				copied = true
			}
			if !unitsCopied {
				s.Patch[i].Units = append([]Unit{}, instr.Units...)
				unitsCopied = true
			}
			s.Patch[i].Units[j].VarArgs = s.automationArgs(a[k].Points)
		}
	}
}

// ReduceAutomation returns the fewest of the points, in their order, whose
// curve stays within tolerance of every one of them (Ramer, Douglas and
// Peucker), measured along the value. The first and the last point are
// kept.
func ReduceAutomation(points AutomationCurve, tolerance float64) AutomationCurve {
	if len(points) < 3 {
		return slices.Clone(points)
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, len(points) - 1}}
	for len(stack) > 0 {
		sp := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		a, b := points[sp.a], points[sp.b]
		worst, at := tolerance, -1
		for i := sp.a + 1; i < sp.b; i++ {
			v := a.Value
			if b.Row > a.Row {
				v += (b.Value - a.Value) * (points[i].Row - a.Row) / (b.Row - a.Row)
			}
			if d := math.Abs(points[i].Value - v); d > worst {
				worst, at = d, i
			}
		}
		if at >= 0 {
			keep[at] = true
			stack = append(stack, span{sp.a, at}, span{at, sp.b})
		}
	}
	var ret AutomationCurve
	for i, p := range points {
		if keep[i] {
			ret = append(ret, p)
		}
	}
	return ret
}
