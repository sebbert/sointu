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

	// Lane is the automation of one slot: the parameters that it offsets,
	// and its points. The value of the lane, in steps of a parameter, is
	// added to each target's parameter times the amount of the target, on
	// top of the value that the unit has. Song.Expand turns the targets
	// into the automation table of their instruments (Instrument.Automation),
	// which the synths apply before the units run. Without targets the lane
	// is bound to nothing, and only automation units with the slot play it.
	//
	// The value is that of the points, linear between them, that of the
	// first point before it and that of the last point after it; without
	// points, 0.
	//
	// Name is what a plugin host shows for the slot; without one, the
	// tracker names it by what it is bound to.
	//
	// Unipolar tells how a plugin host moves the slot: a unipolar slot
	// goes from 0 to AutomationRange steps over the range of the host (its
	// default, at the bottom, is 0), a bipolar one from -AutomationRange to
	// AutomationRange (0 in the middle); see HostSteps. A unipolar slot
	// suits a parameter whose unit is at the bottom of its range, so that
	// the host moves it all the way up.
	//
	// Songs saved before lanes had targets have the one parameter of a lane
	// in Unit and Param of the lane: they are read as its target.
	Lane struct {
		Slot     int
		Name     string          `yaml:",omitempty" json:",omitempty"`
		Unipolar bool            `yaml:",omitempty" json:",omitempty"`
		Targets  []LaneTarget    `yaml:",omitempty" json:",omitempty"`
		Points   AutomationCurve `yaml:",flow,omitempty" json:",omitempty"`
	}

	// LaneTarget is a parameter that a lane offsets: the parameter Param of
	// the unit with the ID Unit, a parameter that a send can modulate (for a
	// module unit, p1 to p8). Amount is the part of the value of the lane
	// that it adds, from -1 to 1, in steps of 1/64 as the amount of a send;
	// it is 1 when left out of a file.
	LaneTarget struct {
		Unit   int
		Param  string
		Amount float64
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

// laneTargetFile is a LaneTarget as files have it: the amount only when it
// is not 1.
type laneTargetFile struct {
	Unit   int      `yaml:"unit" json:"Unit"`
	Param  string   `yaml:"param" json:"Param"`
	Amount *float64 `yaml:"amount,omitempty" json:"Amount,omitempty"`
}

func (t LaneTarget) file() laneTargetFile {
	f := laneTargetFile{Unit: t.Unit, Param: t.Param}
	if t.Amount != 1 {
		a := t.Amount
		f.Amount = &a
	}
	return f
}

func (f laneTargetFile) target() LaneTarget {
	t := LaneTarget{Unit: f.Unit, Param: f.Param, Amount: 1}
	if f.Amount != nil {
		t.Amount = *f.Amount
	}
	return t
}

func (t LaneTarget) MarshalYAML() (any, error) {
	var n yaml.Node
	if err := n.Encode(t.file()); err != nil {
		return nil, err
	}
	n.Style = yaml.FlowStyle
	return &n, nil
}

func (t *LaneTarget) UnmarshalYAML(node *yaml.Node) error {
	var f laneTargetFile
	if err := node.Decode(&f); err != nil {
		return err
	}
	*t = f.target()
	return nil
}

func (t LaneTarget) MarshalJSON() ([]byte, error) { return json.Marshal(t.file()) }

func (t *LaneTarget) UnmarshalJSON(data []byte) error {
	var f laneTargetFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*t = f.target()
	return nil
}

// laneFile is a Lane as files have it, also as it was saved before lanes
// had targets: with its one parameter in Unit and Param.
type laneFile struct {
	Slot     int
	Name     string          `yaml:",omitempty" json:",omitempty"`
	Unipolar bool            `yaml:",omitempty" json:",omitempty"`
	Unit     int             `yaml:",omitempty" json:",omitempty"`
	Param    string          `yaml:",omitempty" json:",omitempty"`
	Targets  []LaneTarget    `yaml:",omitempty" json:",omitempty"`
	Points   AutomationCurve `yaml:",flow,omitempty" json:",omitempty"`
}

func (f laneFile) lane() Lane {
	l := Lane{Slot: f.Slot, Name: f.Name, Unipolar: f.Unipolar, Targets: f.Targets, Points: f.Points}
	if f.Unit != 0 {
		l.Targets = append([]LaneTarget{{Unit: f.Unit, Param: f.Param, Amount: 1}}, l.Targets...)
	}
	return l
}

func (l *Lane) UnmarshalYAML(node *yaml.Node) error {
	var f laneFile
	if err := node.Decode(&f); err != nil {
		return err
	}
	*l = f.lane()
	return nil
}

func (l *Lane) UnmarshalJSON(data []byte) error {
	var f laneFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*l = f.lane()
	return nil
}

// HostSteps returns the value of a slot in steps for a value that a plugin
// host gives it, which hosts have from -AutomationRange to AutomationRange
// with 0 in the middle: the same for a bipolar slot, and for a unipolar one
// from 0 at the bottom to AutomationRange at the top. HostValue is the
// inverse.
func HostSteps(host float64, unipolar bool) float64 {
	if unipolar {
		return (host + AutomationRange) / 2
	}
	return host
}

// HostValue returns the value that a plugin host has for a slot at the given
// steps: see HostSteps.
func HostValue(steps float64, unipolar bool) float64 {
	if unipolar {
		return steps*2 - AutomationRange
	}
	return steps
}

// AmountValue returns the amount of the target as the amount of a send: 64
// plus 64 times the amount, from 0 to 128.
func (t LaneTarget) AmountValue() int {
	return 64 + int(math.Round(64*min(max(t.Amount, -1), 1)))
}

// Copy makes a deep copy of the automation.
func (a Automation) Copy() Automation {
	if a == nil {
		return nil
	}
	ret := make(Automation, len(a))
	for i, l := range a {
		ret[i] = l
		ret[i].Targets = slices.Clone(l.Targets)
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

// FindParam returns the index of the lane with a target that is the
// parameter of the unit with the ID, and the index of that target.
func (a Automation) FindParam(unit int, param string) (lane, target int, ok bool) {
	if unit == 0 {
		return 0, 0, false
	}
	for i, l := range a {
		for j, t := range l.Targets {
			if t.Unit == unit && t.Param == param {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// RemoveTarget returns the automation without target j of lane i, and
// without the lane, with its points, if that was its last target. It
// changes a: copy it first.
func (a Automation) RemoveTarget(i, j int) Automation {
	a[i].Targets = append(a[i].Targets[:j:j], a[i].Targets[j+1:]...)
	if len(a[i].Targets) == 0 {
		a[i].Targets = nil
		a = append(a[:i:i], a[i+1:]...)
	}
	if len(a) == 0 {
		return nil
	}
	return a
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

// laneMarker is the parameter that marks the automation units that
// insertAutomation puts before the targets of the lanes, for
// extractAutomation to find them after the expansion.
const laneMarker = "lane"

// insertAutomation returns the song with an automation unit and a send
// before every unit that a lane has a target in: the send adds the value of
// the lane to the parameter, times the amount of the target. The expansion
// of the modules then turns sends to module units into sends to the
// parameters bound to them, as for any send, and extractAutomation takes
// the pairs out again, as the automation tables of the instruments. The
// instruments without targets share their units with s.
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
		for _, t := range l.Targets {
			i, j, err := ret.Patch.FindUnit(t.Unit)
			if err != nil {
				exp.Problems = append(exp.Problems, fmt.Errorf("automation: slot %d offsets unit %d, which is not an enabled unit of an instrument", l.Slot+1, t.Unit))
				continue
			}
			target := ret.Patch[i].Units[j]
			if !CanAutomate(target.Type, t.Param) {
				exp.Problems = append(exp.Problems, fmt.Errorf("automation: slot %d offsets %v of a %v unit, which cannot be modulated", l.Slot+1, t.Param, target.Type))
				continue
			}
			if !copied {
				ret.Patch = append(Patch{}, s.Patch...)
				copied = true
			}
			units := make([]Unit, 0, len(ret.Patch[i].Units)+2)
			units = append(units, ret.Patch[i].Units[:j]...)
			units = append(units,
				Unit{Type: "automation", Parameters: ParamMap{"slot": l.Slot, laneMarker: 1}},
				Unit{Type: "send", Parameters: ParamMap{"stereo": 0, "amount": t.AmountValue(), "voice": 0, "target": t.Unit, "port": PortOf(target.Type, t.Param), "sendpop": 1}},
			)
			ret.Patch[i].Units = append(units, ret.Patch[i].Units[j:]...)
		}
	}
	return ret
}

// extractAutomation takes the automation units that insertAutomation put
// in the song out of it again, with the sends (or the pop) that their send
// became, and gives each instrument the targets of those sends as its
// automation table, with the points of the lanes of their slots, from the
// automation a. The units of the expanded song that are in spans
// (Expansion.Spans and EQs) move up.
func (s *Song) extractAutomation(a Automation, exp *Expansion) {
	args := map[int][]int{}
	for _, l := range a {
		args[l.Slot] = s.automationArgs(l.Points)
	}
	copied := false
	for i := range s.Patch {
		units := s.Patch[i].Units
		if !slices.ContainsFunc(units, func(u Unit) bool { return u.Type == "automation" && u.Parameters[laneMarker] == 1 }) {
			continue
		}
		if !copied {
			s.Patch = append(Patch{}, s.Patch...)
			copied = true
		}
		instr := &s.Patch[i]
		instr.Automation = slices.Clone(instr.Automation)
		out := make([]Unit, 0, len(units))
		counts := make([]int, len(units)) // how many units each unit is now
		for j := 0; j < len(units); j++ {
			u := units[j]
			if u.Type != "automation" || u.Parameters[laneMarker] != 1 {
				out = append(out, u)
				counts[j] = 1
				continue
			}
			slot := u.Parameters["slot"]
			// the sends that the send became, up to the one that pops; a
			// send to a module unit without bound parameters became a pop
			for j+1 < len(units) {
				next := units[j+1]
				if next.Type == "pop" {
					j++
					break
				}
				if next.Type != "send" {
					break
				}
				j++
				instr.Automation = append(instr.Automation, AutomationTarget{
					Slot:   slot,
					Unit:   next.Parameters["target"],
					Port:   next.Parameters["port"],
					Amount: next.Parameters["amount"],
					Points: args[slot],
				})
				if next.Parameters["sendpop"] == 1 {
					break
				}
			}
		}
		instr.Units = out
		exp.moveSpans(i, counts)
	}
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
		// the automation units that the user put in the patch: those that
		// insertAutomation put there are gone
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
