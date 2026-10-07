package tracker

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// Params returns the Param view of the Model, containing methods to manipulate
// the parameters.
func (m *Model) Params() *ParamModel { return (*ParamModel)(m) }

type ParamModel Model

// Wires returns the wires of the unit editor, telling which parameters are
// connected to which: those of the root units, and under the unfolded module
// units, those between the units of their modules.
func (m *ParamModel) Wires(yield func(wire Wire) bool) {
	c := (*Model)(m).rows()
	cursor := m.Cursor()
	for _, l := range c.lists {
		d := (*Model)(m).derivedOf(l.module)
		if d == nil {
			continue
		}
		for _, wire := range d.wires {
			// the wires are between units; the unit editor draws rows
			if wire.FromSet {
				if wire.From >= len(l.rows) {
					continue
				}
				wire.From = l.rows[wire.From]
			}
			if wire.ToSet {
				if wire.To.Y >= len(l.rows) {
					continue
				}
				wire.To.Y = l.rows[wire.To.Y]
			}
			wire.Highlight = (wire.FromSet && cursor.Y == wire.From) || (wire.ToSet && cursor == wire.To)
			if !yield(wire) {
				return
			}
		}
	}
}

// chooseSendSource
type chooseSendSource struct {
	ID int
	*Model
}

func (m *ParamModel) IsChoosingSendTarget() bool {
	return m.d.SendSource > 0
}

func (m *ParamModel) ChooseSendSource(id int) Action {
	return MakeAction(chooseSendSource{ID: id, Model: (*Model)(m)})
}
func (s chooseSendSource) Do() {
	defer (*Model)(s.Model).change("ChooseSendSource", NoChange, MinorChange)()
	if s.Model.d.SendSource == s.ID {
		s.Model.d.SendSource = 0 // unselect
		return
	}
	s.Model.d.SendSource = s.ID
}

// chooseSendTarget
type chooseSendTarget struct {
	ID   int
	Port int
	*Model
}

func (m *ParamModel) ChooseSendTarget(id int, port int) Action {
	return MakeAction(chooseSendTarget{ID: id, Port: port, Model: (*Model)(m)})
}
func (s chooseSendTarget) Do() {
	defer (*Model)(s.Model).change("ChooseSendTarget", SongChange, MinorChange)()
	sourceID := (*Model)(s.Model).d.SendSource
	s.d.SendSource = 0
	if sourceID <= 0 || s.ID <= 0 || s.Port < 0 || s.Port > 7 {
		return
	}
	source := s.d.Song.FindUnit(sourceID)
	if source == nil || source.Disabled {
		return
	}
	source.Parameters["target"] = s.ID
	source.Parameters["port"] = s.Port
}

// paramsColumns
type paramsColumns Model

func (m *ParamModel) Columns() List              { return List{(*paramsColumns)(m)} }
func (pt *paramsColumns) Selected() int          { return pt.d.ParamIndex }
func (pt *paramsColumns) Selected2() int         { return pt.d.ParamIndex }
func (pt *paramsColumns) SetSelected(index int)  { pt.d.ParamIndex = index }
func (pt *paramsColumns) SetSelected2(index int) {}
func (pt *paramsColumns) Count() int             { return (*ParamModel)(pt).Width() }

// Model and Params methods

func (pt *ParamModel) Table() Table { return Table{pt} }

// The table is in rows of the unit editor: see rows.go.
func (pt *ParamModel) Cursor() Point {
	return Point{pt.d.ParamIndex, (*Model)(pt).rowOfUnit(pt.d.UnitIndex)}
}
func (pt *ParamModel) Cursor2() Point {
	return Point{pt.d.ParamIndex, (*Model)(pt).rowOfUnit(pt.d.UnitIndex2)}
}
func (pt *ParamModel) SetCursor(p Point) {
	pt.d.ParamIndex = max(min(p.X, pt.Width()-1), 0)
	(*Model)(pt).selectRow(p.Y)
}
func (pt *ParamModel) SetCursor2(p Point) {
	pt.d.ParamIndex = max(min(p.X, pt.Width()-1), 0)
	pt.d.UnitIndex2 = (*Model)(pt).unitNearRow(p.Y)
}
func (pt *ParamModel) Width() int {
	if _, ok := (*Model)(pt).root(); !ok {
		return 0
	}
	// TODO: we hack the +1 so that we always have one extra cell to draw the
	// comments. Refactor the gioui side so that we can specify the width and
	// height regardless of the underlying table size
	return (*Model)(pt).rows().paramsWidth + 1
}
func (pt *ParamModel) RowWidth(y int) int {
	c := (*Model)(pt).rows()
	if y < 0 || y >= len(c.rows) {
		return 0
	}
	return len(c.rows[y].params)
}
func (pt *ParamModel) Height() int { return (*Model)(pt).numRows() }
func (pt *ParamModel) MoveCursor(dx, dy int) (ok bool) {
	p := pt.Cursor()
	p.X += dx
	p.Y += dy
	pt.SetCursor(p)
	return p == pt.Cursor()
}

// extendCursor moves the cursor by dx parameters and dy units among the
// units being edited, past the inner units of the module units among them.
func (pt *ParamModel) extendCursor(dx, dy int) {
	pt.d.ParamIndex = max(min(pt.d.ParamIndex+dx, pt.Width()-1), 0)
	pt.d.UnitIndex = clamp(pt.d.UnitIndex+dy, 0, max(len((*Model)(pt).units())-1, 0))
}
func (pt *ParamModel) Item(p Point) Parameter {
	c := (*Model)(pt).rows()
	if p.Y < 0 || p.Y >= len(c.rows) || p.X < 0 || p.X >= len(c.rows[p.Y].params) {
		return Parameter{}
	}
	return c.rows[p.Y].params[p.X]
}

// selected returns the parameter at p if its unit is one of the units being
// edited: a selection from a module unit to the unit after it leaves the
// inner units of the module unit alone.
func (pt *ParamModel) selected(p Point) Parameter {
	if !(*Model)(pt).rowInScope(p.Y) {
		return Parameter{}
	}
	return pt.Item(p)
}
func (pt *ParamModel) clear(p Point) {
	q := pt.selected(p)
	q.Reset()
}
func (pt *ParamModel) set(p Point, value int) {
	q := pt.selected(p)
	q.SetValue(value)
}
func (pt *ParamModel) add(rect Rect, delta int, largeStep bool) (ok bool) {
	for y := rect.TopLeft.Y; y <= rect.BottomRight.Y; y++ {
		for x := rect.TopLeft.X; x <= rect.BottomRight.X; x++ {
			p := Point{x, y}
			q := pt.selected(p)
			if !q.Add(delta, largeStep) {
				return false
			}
			// a setting of the tracker alone is no change of the song
			ok = ok || !q.trackerSetting()
		}
	}
	return ok
}

type paramsTable struct {
	Params [][]int `yaml:",flow"`
}

func (pt *ParamModel) marshal(rect Rect) (data []byte, ok bool) {
	width := rect.BottomRight.X - rect.TopLeft.X + 1
	height := rect.BottomRight.Y - rect.TopLeft.Y + 1
	var table = paramsTable{Params: make([][]int, 0, width)}
	for x := 0; x < width; x++ {
		table.Params = append(table.Params, make([]int, 0, rect.BottomRight.Y-rect.TopLeft.Y+1))
		for y := 0; y < height; y++ {
			p := pt.Item(Point{x + rect.TopLeft.X, y + rect.TopLeft.Y})
			table.Params[x] = append(table.Params[x], p.Value())
		}
	}
	ret, err := yaml.Marshal(table)
	if err != nil {
		return nil, false
	}
	return ret, true
}
func (pt *ParamModel) unmarshal(data []byte) (paramsTable, bool) {
	var table paramsTable
	yaml.Unmarshal(data, &table)
	if len(table.Params) == 0 {
		return paramsTable{}, false
	}
	for i := 0; i < len(table.Params); i++ {
		if len(table.Params[i]) > 0 {
			return table, true
		}
	}
	return paramsTable{}, false
}

func (pt *ParamModel) unmarshalAtCursor(data []byte) (ret bool) {
	table, ok := pt.unmarshal(data)
	if !ok {
		return false
	}
	for i := 0; i < len(table.Params); i++ {
		for j, q := range table.Params[i] {
			x := i + pt.Cursor().X
			y := j + pt.Cursor().Y
			p := pt.selected(Point{x, y})
			ret = p.SetValue(q) || ret
		}
	}
	return ret
}
func (pt *ParamModel) unmarshalRange(rect Rect, data []byte) (ret bool) {
	table, ok := pt.unmarshal(data)
	if !ok {
		return false
	}
	if len(table.Params) == 0 || len(table.Params[0]) == 0 {
		return false
	}
	width := rect.BottomRight.X - rect.TopLeft.X + 1
	height := rect.BottomRight.Y - rect.TopLeft.Y + 1
	if len(table.Params) < width {
		return false
	}
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if len(table.Params[0]) < height {
				return false
			}
			p := pt.selected(Point{x + rect.TopLeft.X, y + rect.TopLeft.Y})
			ret = p.SetValue(table.Params[x][y]) || ret
		}
	}
	return ret
}
func (pt *ParamModel) change(kind string, severity ChangeSeverity) func() {
	return (*Model)(pt).change(kind, PatchChange, severity)
}
func (pt *ParamModel) cancel() {
	pt.changeCancel = true
}

type (
	// Parameter represents a parameter of a unit. To support polymorphism
	// without causing allocations, it has a vtable that defines the methods for
	// the specific parameter type, to which all the method calls are delegated.
	Parameter struct {
		m      *Model
		unit   *sointu.Unit
		up     *sointu.UnitParameter
		index  int
		vtable parameterVtable
		port   int
		// arg is set for a parameter of a module unit that sets a
		// parameter of its module with something bound to it: see
		// moduleArg
		arg *moduleArg
		// through is set for a bound parameter of an inner unit: it has the
		// value that the module unit above it gives it, see innerBinding
		through *innerBinding
	}

	parameterVtable interface {
		Value(*Parameter) int
		SetValue(*Parameter, int) bool
		Range(*Parameter) RangeInclusive
		Type(*Parameter) ParameterType
		Name(*Parameter) string
		Hint(*Parameter) ParameterHint
		Reset(*Parameter)
		RoundToGrid(*Parameter, int, bool) int
	}

	// optional interfaces of the vtables

	// parameterStepper replaces how Add steps the parameter: it returns the
	// value delta steps from the current one.
	parameterStepper interface {
		Step(p *Parameter, delta int, large bool) int
	}
	// parameterScaler gives the knob of the parameter a scale that is not
	// linear in its value.
	parameterScaler interface {
		Position(p *Parameter, value int) float32
		Dragged(p *Parameter, start int, amount float32) int
	}
	// parameterLabeler names the value of the parameter on its knob.
	parameterLabeler interface {
		Label(p *Parameter) string
	}
	// parameterChooser gives the choices of a ChoiceParameter, when they
	// are not the values of its range.
	parameterChooser interface {
		Choices(p *Parameter) IntValue
	}

	// different parameter vtables to handle different types of parameters.
	// Casting struct{} to interface does not cause allocations.
	namedParameter      struct{}
	delayTimeParameter  struct{}
	delayLinesParameter struct{}
	delayFreeParameter  struct{}
	gmDlsEntryParameter struct{}
	reverbParameter     struct{}
	// bufferParameter is a parameter referring to a buffer, audio,
	// spectrum or bus; writer means the unit writes a spectrum or starts a
	// chain of mc units on it, so that it cannot be none.
	bufferParameter      struct{ spectrum, bus, writer bool }
	spawnTargetParameter struct{}
	// moduleParameter is the module of a module unit, and
	// moduleArgParameter a parameter of the module (Parameter.index, from
	// 1), which the unit sets
	moduleParameter            struct{}
	moduleArgParameter         struct{}
	spawnRateParameter         struct{ namedParameter }
	bufferFrameParameter       struct{ namedParameter }
	convolutionLengthParameter struct{ namedParameter }

	ParamYieldFunc func(param Parameter) bool

	ParameterType int

	ParameterHint struct {
		Label string
		Valid bool
	}
)

const (
	NoParameter ParameterType = iota
	IntegerParameter
	BoolParameter
	IDParameter
	ChoiceParameter // an Int with named values, e.g. for a dropdown; see Parameter.Int
)

// Parameter methods

func (p *Parameter) Value() int {
	if p.vtable == nil {
		return 0
	}
	if p.through != nil {
		p.through.refresh(p)
	}
	return p.vtable.Value(p)
}
func (p *Parameter) Port() (int, bool) {
	if p.port <= 0 {
		return 0, false
	}
	return p.port - 1, true
}
func (p *Parameter) SetValue(value int) bool {
	if p.vtable == nil {
		return false
	}
	r := p.Range()
	value = r.Clamp(value)
	if value == p.Value() || value < r.Min || value > r.Max {
		return false
	}
	if p.through != nil {
		// the value was set in the stand-in unit: the module unit that the
		// unit is an inner unit of gets it
		defer p.m.change("InnerBoundParameter", PatchChange, MinorChange)()
		defer p.through.store(p)
	}
	if p.arg != nil {
		// the value was set in the stand-in unit: the module unit gets it
		defer p.m.change("ModuleArgParameter"+p.arg.key(), PatchChange, MinorChange)()
		defer p.arg.store(p)
	}
	if _, ok := p.Bound(); ok && p.through == nil {
		// the value of a bound parameter is the default of the parameter
		// of the module
		unit, name, _ := p.bindTarget()
		defer p.m.change("BoundParameter", PatchChange, MinorChange)()
		defer p.m.syncBoundDefault(unit, name)
	}
	return p.vtable.SetValue(p, value)
}

// bindTarget returns the unit and the name that the parameter is bound to a
// parameter of a module by, in Unit.Bind: a parameter of the unit, a delay
// time, or for a parameter of a module unit, the module unit and p1 to p8.
func (p *Parameter) bindTarget() (unit *sointu.Unit, name string, ok bool) {
	switch {
	case p.unit == nil || p.vtable == nil:
		return nil, "", false
	case p.through != nil:
		return p.through.unit, p.through.name, true
	case p.arg != nil:
		return p.arg.call, p.arg.key(), true
	case p.up != nil:
		return p.unit, p.up.Name, true
	}
	if _, ok := p.vtable.(*delayTimeParameter); ok {
		return p.unit, sointu.DelayTimeName(p.index), true
	}
	return nil, "", false
}
func (p *Parameter) Add(delta int, snapToGrid bool) bool {
	if p.vtable == nil {
		return false
	}
	if p.through != nil && p.through.binding.Scaled {
		return p.through.step(p, delta, snapToGrid)
	}
	if s, ok := p.vtable.(parameterStepper); ok {
		return p.SetValue(s.Step(p, delta, snapToGrid))
	}
	newVal := p.Value() + delta
	if snapToGrid && p.vtable != nil {
		newVal = p.vtable.RoundToGrid(p, newVal, delta > 0)
	}
	return p.SetValue(newVal)
}

// trackerSetting tells if the parameter is a setting of the tracker, not
// stored in the song: changing it is not undone, and does not make the song
// changed.
func (p *Parameter) trackerSetting() bool {
	_, ok := p.vtable.(*delayFreeParameter)
	return ok
}

// Label returns the text shown on the knob of the parameter: its value, or
// what the value is called.
func (p *Parameter) Label() string {
	if l, ok := p.vtable.(parameterLabeler); ok {
		return l.Label(p)
	}
	return strconv.Itoa(p.Value())
}

// Position returns where a value lies on the scale of the knob of the
// parameter, 0 being the minimum and 1 the maximum.
func (p *Parameter) Position(value int) float32 {
	if s, ok := p.vtable.(parameterScaler); ok {
		return s.Position(p, value)
	}
	r := p.Range()
	return float32(value-r.Min) / float32(r.Max-r.Min)
}

// Dragged returns the value that dragging the knob of the parameter leads to,
// from the value start by amount of its scale: 1 is from the minimum to the
// maximum. The value is not limited to the range.
func (p *Parameter) Dragged(start int, amount float32) int {
	if s, ok := p.vtable.(parameterScaler); ok {
		return s.Dragged(p, start, amount)
	}
	r := p.Range()
	return int(float32(start) + amount*float32(r.Max-r.Min))
}

func (p *Parameter) Range() RangeInclusive {
	if p.vtable == nil {
		return RangeInclusive{}
	}
	r := p.vtable.Range(p)
	if _, plain := p.vtable.(*namedParameter); plain && p.arg != nil {
		// the range that the module gives its parameter, within it
		mp := p.arg.param(p.m)
		r.Min, r.Max = max(r.Min, mp.MinValue), min(r.Max, max(mp.MaxValue, mp.MinValue))
		r.Max = max(r.Max, r.Min)
	}
	return r
}
func (p *Parameter) Neutral() int {
	if p.vtable == nil {
		return 0
	}
	if a, ok := p.vtable.(*moduleArgParameter); ok {
		return a.param(p).Neutral
	}
	if p.arg != nil {
		if up, ok := sointu.BindableParam(p.unit.Type, p.arg.name); ok {
			return up.Neutral
		}
	}
	if p.up != nil {
		return p.up.Neutral
	}
	return 0
}
func (p *Parameter) Type() ParameterType {
	if p.vtable == nil {
		return NoParameter
	}
	return p.vtable.Type(p)
}
func (p *Parameter) Name() string {
	if p.vtable == nil {
		return ""
	}
	if p.arg != nil {
		return p.arg.param(p.m).Name // of the parameter of the module
	}
	return p.vtable.Name(p)
}
func (p *Parameter) Hint() ParameterHint {
	if p.vtable == nil {
		return ParameterHint{}
	}
	hint := p.vtable.Hint(p)
	if name, ok := p.Bound(); ok {
		// the module units set it; the value is the default of the module
		hint.Label = "← " + name + ": " + hint.Label
	}
	return hint
}
func (p *Parameter) Reset() {
	if p.vtable == nil {
		return
	}
	if p.through != nil {
		p.through.reset(p)
		return
	}
	if p.arg != nil {
		// back to the default of the parameter of the module
		defer p.m.change("ResetModuleArgParameter", PatchChange, MinorChange)()
		p.arg.call.Parameters[p.arg.key()] = p.arg.param(p.m).Default
		return
	}
	if _, ok := p.Bound(); ok {
		unit, name, _ := p.bindTarget()
		defer p.m.change("BoundParameter", PatchChange, MinorChange)()
		defer p.m.syncBoundDefault(unit, name)
	}
	p.vtable.Reset(p)
}

// Int returns the parameter as an Int, with the value names of choice
// parameters, e.g. for showing the choices in a menu.
func (p Parameter) Int() Int {
	if c, ok := p.vtable.(parameterChooser); ok {
		return MakeInt(c.Choices(&p))
	}
	return MakeInt(parameterInt{p})
}

// ChoiceLabel returns the text shown on the button of a choice parameter,
// which opens the menu of its choices, and the hint to show as its tip, if
// the two are not the same.
func (p *Parameter) ChoiceLabel() (label, tip string) {
	if l, ok := p.vtable.(parameterLabeler); ok {
		return l.Label(p), p.Hint().Label
	}
	return p.Hint().Label, ""
}

type parameterInt struct{ p Parameter }

func (v parameterInt) Value() int              { return v.p.Value() }
func (v parameterInt) SetValue(value int) bool { return v.p.SetValue(value) }
func (v parameterInt) Range() RangeInclusive   { return v.p.Range() }
func (v parameterInt) StringOf(value int) string {
	if s, ok := v.p.vtable.(interface{ StringOf(*Parameter, int) string }); ok {
		return s.StringOf(&v.p, value)
	}
	return strconv.Itoa(value)
}

func (p *Parameter) UnitID() int {
	if p.unit == nil {
		return 0
	}
	if p.arg != nil {
		return p.arg.call.ID // sends go to the module unit
	}
	return p.unit.ID
}

// namedParameter vtable

func (n *namedParameter) Value(p *Parameter) int { return p.unit.Parameters[p.up.Name] }
func (n *namedParameter) SetValue(p *Parameter, value int) bool {
	defer p.m.change("Parameter"+p.Name(), PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = value
	return true
}
func (n *namedParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: p.up.MinValue, Max: p.up.MaxValue}
}
func (n *namedParameter) Type(p *Parameter) ParameterType {
	if p.up == nil || !p.up.CanSet {
		return NoParameter
	}
	if p.unit.Type == "send" && p.up.Name == "target" {
		return IDParameter
	}
	if p.up.MinValue >= -1 && p.up.MaxValue <= 1 {
		return BoolParameter
	}
	return IntegerParameter
}
func (n *namedParameter) Name(p *Parameter) string {
	if p.up.Name == "notetracking" {
		return "tracking" // notetracking does not fit in the UI
	}
	return p.up.Name
}
func (n *namedParameter) Hint(p *Parameter) ParameterHint {
	val := p.Value()
	label := strconv.Itoa(val)
	if p.up.DisplayFunc != nil {
		valueInUnits, units := p.up.DisplayFunc(val)
		label = fmt.Sprintf("%s %s", valueInUnits, units)
	}
	return ParameterHint{label, true}
}
func (n *namedParameter) RoundToGrid(p *Parameter, val int, up bool) int {
	if p.up.Name == "transpose" {
		return roundToGrid(val-64, 12, up) + 64
	}
	return roundToGrid(val, 8, up)
}
func (n *namedParameter) Reset(p *Parameter) {
	defer p.m.change("Reset"+p.Name(), PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = p.up.Default
}

// GmDlsEntry is a single sample entry from the gm.dls file
type GmDlsEntry struct {
	Start              int    // sample start offset in words
	LoopStart          int    // loop start offset in words
	LoopLength         int    // loop length in words
	SuggestedTranspose int    // suggested transpose in semitones, so that all samples play at same pitch
	Name               string // sample Name
}

// gmDlsEntryMap is a reverse map, to find the index of the GmDlsEntry in the
var gmDlsEntryMap = make(map[vm.SampleOffset]int)

func init() {
	for i, e := range GmDlsEntries {
		key := vm.SampleOffset{Start: uint32(e.Start), LoopStart: uint16(e.LoopStart), LoopLength: uint16(e.LoopLength)}
		gmDlsEntryMap[key] = i
	}
}

// gmDlsEntryParameter vtable

func (g *gmDlsEntryParameter) Value(p *Parameter) int {
	key := vm.SampleOffset{
		Start:      uint32(p.unit.Parameters["samplestart"]),
		LoopStart:  uint16(p.unit.Parameters["loopstart"]),
		LoopLength: uint16(p.unit.Parameters["looplength"]),
	}
	if v, ok := gmDlsEntryMap[key]; ok {
		return v + 1
	}
	return 0
}
func (g *gmDlsEntryParameter) SetValue(p *Parameter, v int) bool {
	if v < 1 || v > len(GmDlsEntries) {
		return false
	}
	defer p.m.change("GmDlsEntryParameter", PatchChange, MinorChange)()
	e := GmDlsEntries[v-1]
	p.unit.Parameters["samplestart"] = e.Start
	p.unit.Parameters["loopstart"] = e.LoopStart
	p.unit.Parameters["looplength"] = e.LoopLength
	p.unit.Parameters["transpose"] = 64 + e.SuggestedTranspose
	return true
}
func (g *gmDlsEntryParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 0, Max: len(GmDlsEntries)}
}
func (g *gmDlsEntryParameter) Type(p *Parameter) ParameterType {
	return IntegerParameter
}
func (g *gmDlsEntryParameter) Name(p *Parameter) string {
	return "sample"
}
func (g *gmDlsEntryParameter) Hint(p *Parameter) ParameterHint {
	label := "custom"
	if v := g.Value(p); v > 0 {
		label = GmDlsEntries[v-1].Name
	}
	return ParameterHint{label, true}
}
func (g *gmDlsEntryParameter) RoundToGrid(p *Parameter, val int, up bool) int {
	return roundToGrid(val, 16, up)
}
func (g *gmDlsEntryParameter) Reset(p *Parameter) {}

// delayTimeParameter vtable

var delayNoteTrackGrid, delayBpmTrackGrid []int

func init() {
	for st := -30; st <= 30; st++ {
		gridVal := int(math.Exp2(float64(st)/12)*10787 + 0.5)
		delayNoteTrackGrid = append(delayNoteTrackGrid, gridVal)
	}
	for i := 0; i < 16; i++ {
		delayBpmTrackGrid = append(delayBpmTrackGrid, 1<<i)
		delayBpmTrackGrid = append(delayBpmTrackGrid, 3<<i)
		delayBpmTrackGrid = append(delayBpmTrackGrid, 9<<i)
	}
	slices.Sort(delayBpmTrackGrid)
}

func (d *delayTimeParameter) Name(p *Parameter) string { return "delaytime" }
func (d *delayTimeParameter) Value(p *Parameter) int {
	if p.index < 0 || p.index >= len(p.unit.VarArgs) {
		return 1
	}
	return p.unit.VarArgs[p.index]
}
func (d *delayTimeParameter) SetValue(p *Parameter, v int) bool {
	defer p.m.change("DelayTimeParameter", PatchChange, MinorChange)()
	p.unit.VarArgs[p.index] = v
	return true
}
func (d *delayTimeParameter) Range(p *Parameter) RangeInclusive {
	if p.unit.Parameters["notetracking"] == delayBPM {
		return RangeInclusive{Min: 1, Max: delayBPMMax}
	}
	return RangeInclusive{Min: 1, Max: delaySamplesMax}
}
func (d *delayTimeParameter) RoundToGrid(p *Parameter, val int, up bool) int {
	switch p.unit.Parameters["notetracking"] {
	default:
		return roundToGrid(val, 16, up)
	case 1:
		return roundToSliceGrid(val, delayNoteTrackGrid, up)
	case 2:
		return roundToSliceGrid(val, delayBpmTrackGrid, up)
	}
}
func (d *delayTimeParameter) Reset(p *Parameter) {}

// delayLinesParameter vtable

func (d *delayLinesParameter) Value(p *Parameter) int {
	val := len(p.unit.VarArgs)
	if p.unit.Parameters["stereo"] == 1 {
		val /= 2
	}
	return val
}
func (d *delayLinesParameter) SetValue(p *Parameter, v int) bool {
	defer p.m.change("DelayLinesParameter", PatchChange, MinorChange)()
	targetLines := v
	if p.unit.Parameters["stereo"] == 1 {
		targetLines *= 2
	}
	for len(p.unit.VarArgs) < targetLines {
		p.unit.VarArgs = append(p.unit.VarArgs, 1)
	}
	p.unit.VarArgs = p.unit.VarArgs[:targetLines]
	return true
}
func (d *delayLinesParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 1, Max: 32}
}
func (d *delayLinesParameter) Type(p *Parameter) ParameterType                { return IntegerParameter }
func (d *delayLinesParameter) Name(p *Parameter) string                       { return "delaylines" }
func (r *delayLinesParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (d *delayLinesParameter) Hint(p *Parameter) ParameterHint {
	return ParameterHint{strconv.Itoa(d.Value(p)), true}
}
func (d *delayLinesParameter) LargeStep(p *Parameter) int {
	return 4
}
func (d *delayLinesParameter) Reset(p *Parameter) {}

// reverbParameter vtable

type delayPreset struct {
	name    string
	stereo  int
	varArgs []int
}

var reverbs = []delayPreset{
	{"stereo", 1, []int{1116, 1188, 1276, 1356, 1422, 1492, 1556, 1618,
		1140, 1212, 1300, 1380, 1446, 1516, 1580, 1642,
	}},
	{"left", 0, []int{1116, 1188, 1276, 1356, 1422, 1492, 1556, 1618}},
	{"right", 0, []int{1140, 1212, 1300, 1380, 1446, 1516, 1580, 1642}},
}

func (r *reverbParameter) Value(p *Parameter) int {
	i := slices.IndexFunc(reverbs, func(d delayPreset) bool {
		return d.stereo == p.unit.Parameters["stereo"] && p.unit.Parameters["notetracking"] == 0 && slices.Equal(d.varArgs, p.unit.VarArgs)
	})
	return i + 1
}
func (r *reverbParameter) SetValue(p *Parameter, v int) bool {
	if v < 1 || v > len(reverbs) {
		return false
	}
	defer p.m.change("ReverbParameter", PatchChange, MinorChange)()
	entry := reverbs[v-1]
	p.unit.Parameters["stereo"] = entry.stereo
	p.unit.Parameters["notetracking"] = 0
	p.unit.VarArgs = make([]int, len(entry.varArgs))
	copy(p.unit.VarArgs, entry.varArgs)
	p.m.setDelayFree(p.unit, delayOffGrid(p.unit)) // the times of the presets are not whole milliseconds
	return true
}
func (r *reverbParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 0, Max: len(reverbs)}
}
func (r *reverbParameter) Type(p *Parameter) ParameterType                { return IntegerParameter }
func (r *reverbParameter) Name(p *Parameter) string                       { return "reverb" }
func (r *reverbParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (r *reverbParameter) Reset(p *Parameter)                             {}
func (r *reverbParameter) Hint(p *Parameter) ParameterHint {
	i := r.Value(p)
	label := "custom"
	if i > 0 {
		label = reverbs[i-1].name
	}
	return ParameterHint{label, true}
}

func roundToGrid(value, grid int, up bool) int {
	if up {
		return value + mod(-value, grid)
	}
	return value - mod(value, grid)
}

func mod(a, b int) int {
	m := a % b
	if a < 0 && b < 0 {
		m -= b
	}
	if a < 0 && b > 0 {
		m += b
	}
	return m
}

func roundToSliceGrid(value int, grid []int, up bool) int {
	if up {
		for _, v := range grid {
			if value < v {
				return v
			}
		}
	} else {
		for i := len(grid) - 1; i >= 0; i-- {
			if value > grid[i] {
				return grid[i]
			}
		}
	}
	return value
}

// bufferParameter vtable: the buffer used by a unit, audio buffers for
// bufread and bufwrite, spectrum buffers for the spectral units and buses for
// the mc units. Its values
// are 0 for no buffer and i+1 for the i-th of those buffers of the song; the
// unit stores the buffer's ID.

var (
	audioBufferParameter    = &bufferParameter{}
	spectrumBufferParameter = &bufferParameter{spectrum: true}
	spectrumWriterParameter = &bufferParameter{spectrum: true, writer: true}
	busParameter            = &bufferParameter{bus: true}
	busWriterParameter      = &bufferParameter{bus: true, writer: true}
)

func (b *bufferParameter) buffers(p *Parameter) []sointu.Buffer {
	var ret []sointu.Buffer
	for _, buf := range p.m.d.Song.Buffers {
		if buf.Spectrum == b.spectrum && buf.Bus == b.bus {
			ret = append(ret, buf)
		}
	}
	return ret
}
func (b *bufferParameter) Value(p *Parameter) int {
	id := p.unit.Parameters[p.up.Name]
	for i, buf := range b.buffers(p) {
		if buf.ID == id {
			return i + 1
		}
	}
	return 0
}
func (b *bufferParameter) SetValue(p *Parameter, v int) bool {
	bufs, id := b.buffers(p), 0
	if v > 0 && v <= len(bufs) {
		id = bufs[v-1].ID
	}
	if id == 0 && b.writer {
		return false
	}
	defer p.m.change("BufferParameter", PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = id
	return true
}
func (b *bufferParameter) Range(p *Parameter) RangeInclusive {
	r := RangeInclusive{Min: 0, Max: len(b.buffers(p))}
	if b.writer {
		r.Min = min(1, r.Max)
	}
	return r
}
func (b *bufferParameter) Type(p *Parameter) ParameterType { return ChoiceParameter }
func (b *bufferParameter) Name(p *Parameter) string        { return p.up.Name }
func (b *bufferParameter) StringOf(p *Parameter, v int) string {
	if bufs := b.buffers(p); v > 0 && v <= len(bufs) {
		if bufs[v-1].Name != "" {
			return bufs[v-1].Name
		}
		return fmt.Sprintf("Buffer %d", bufs[v-1].ID)
	}
	if id := p.unit.Parameters[p.up.Name]; id != 0 && v == b.Value(p) {
		return "missing"
	}
	return "none"
}
func (b *bufferParameter) Hint(p *Parameter) ParameterHint {
	v := b.Value(p)
	return ParameterHint{b.StringOf(p, v), v > 0}
}
func (b *bufferParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (b *bufferParameter) Reset(p *Parameter) {
	if b.writer {
		return
	}
	defer p.m.change("ResetBufferParameter", PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = 0
}

// spawnTargetParameter vtable: the instrument whose voices a spawn unit
// triggers. Its values are 0 for none and i+1 for the i-th instrument, as
// stored in the unit.

func (b *spawnTargetParameter) Value(p *Parameter) int { return p.unit.Parameters["instrument"] }
func (b *spawnTargetParameter) SetValue(p *Parameter, v int) bool {
	defer p.m.change("SpawnTargetParameter", PatchChange, MinorChange)()
	p.unit.Parameters["instrument"] = v
	return true
}
func (b *spawnTargetParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 0, Max: max(len(p.m.d.Song.Patch), b.Value(p))}
}
func (b *spawnTargetParameter) Type(p *Parameter) ParameterType { return ChoiceParameter }
func (b *spawnTargetParameter) Name(p *Parameter) string        { return "instrument" }
func (b *spawnTargetParameter) StringOf(p *Parameter, v int) string {
	if v <= 0 {
		return "none"
	}
	if v > len(p.m.d.Song.Patch) {
		return "missing"
	}
	if name := p.m.d.Song.Patch[v-1].Name; name != "" {
		return fmt.Sprintf("%d: %s", v, name)
	}
	return strconv.Itoa(v)
}
func (b *spawnTargetParameter) Hint(p *Parameter) ParameterHint {
	v := b.Value(p)
	return ParameterHint{b.StringOf(p, v), v > 0 && v <= len(p.m.d.Song.Patch)}
}
func (b *spawnTargetParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (b *spawnTargetParameter) Reset(p *Parameter) {
	defer p.m.change("ResetSpawnTargetParameter", PatchChange, MinorChange)()
	p.unit.Parameters["instrument"] = 0
}

// spawnRateParameter vtable: the rate of a spawn unit, shown in spawns per
// beat in sync mode, and as the note length between the spawns where it is
// one.

// spawnNoteLength returns the note length between the spawns of a spawn unit
// in sync mode, if it is one: the rate doubles every 8 steps, so every 8th
// value is a straight note, a quarter note at 64. Dotted notes and triplets
// fall between the values.
func spawnNoteLength(rate int) (string, bool) {
	if rate%8 != 0 {
		return "", false
	}
	if k := rate/8 - 6; k >= 0 { // the whole note is at 48
		return fmt.Sprintf("1/%d", 1<<k), true
	} else {
		return fmt.Sprintf("%d/1", 1<<-k), true
	}
}

func (b *spawnRateParameter) Label(p *Parameter) string {
	if p.unit.Parameters["mode"] == sointu.SpawnModeSync {
		if name, ok := spawnNoteLength(p.Value()); ok {
			return name
		}
	}
	return strconv.Itoa(p.Value())
}

func (b *spawnRateParameter) Hint(p *Parameter) ParameterHint {
	if p.unit.Parameters["mode"] != sointu.SpawnModeSync {
		return b.namedParameter.Hint(p)
	}
	perBeat := sointu.SpawnsPerBeat(float64(p.Value()) / 128)
	var text string
	if perBeat >= 1 {
		text = fmt.Sprintf("%s per beat", strconv.FormatFloat(perBeat, 'g', 3, 64))
	} else {
		text = fmt.Sprintf("every %s beats", strconv.FormatFloat(1/perBeat, 'g', 3, 64))
	}
	if name, ok := spawnNoteLength(p.Value()); ok {
		text = name + ": " + text
	}
	return ParameterHint{text, true}
}

// bufferFrameParameter vtable: a position in frames in the buffer played by a
// bufread unit, limited to the length of the buffer and shown in seconds.

func (b *bufferFrameParameter) Range(p *Parameter) RangeInclusive {
	frames := p.m.bufferFrames(p.unit.Parameters["buffer"])
	v := p.unit.Parameters[p.up.Name]
	r := RangeInclusive{Min: 0, Max: max(frames, v, 1)}
	if p.unit.Type == "bufread" && (p.up.Name == "start" || p.up.Name == "loopstart") { // negative counts back from the end
		r.Min = min(-frames, v)
	}
	return r
}
func (b *bufferFrameParameter) Hint(p *Parameter) ParameterHint {
	v := b.Value(p)
	if v < 0 {
		return ParameterHint{fmt.Sprintf("%s from end (%d)", formatDuration(-v), v), true}
	}
	return ParameterHint{fmt.Sprintf("%s (%d)", formatDuration(v), v), true}
}
func (b *bufferFrameParameter) RoundToGrid(p *Parameter, val int, up bool) int {
	return roundToGrid(val, 441, up) // 10 ms
}

// Bound returns the name of the parameter of the module that the parameter
// is bound to, if it is a parameter of a unit of a module and bound.
func (p *Parameter) Bound() (name string, ok bool) {
	unit, key, ok := p.bindTarget()
	if !ok {
		return "", false
	}
	b, ok := unit.Bind[key]
	if !ok {
		return "", false
	}
	module, found := 0, false
	if p.through != nil {
		module, found = p.through.module, true
	} else {
		module, found = p.m.moduleOf(unit)
	}
	if found {
		if mp, ok := p.m.d.Song.Modules.Param(module, b.Param); ok {
			return mp.Name, true
		}
	}
	return sointu.ModuleParamName(b.Param), true
}

// innerBinding is what gives a bound parameter of an inner unit of a module
// unit the value that the module unit gives it, instead of the default of
// the module that it has in the module. The Parameter is that of a stand-in
// unit: a copy of the unit, holding that value. Its vtable works on the
// stand-in, and after a change the module unit gets the value, through the
// binding, like when its own parameter is changed. The module is left as it
// is.
type innerBinding struct {
	unit    *sointu.Unit   // the unit of the module
	standIn *sointu.Unit   // the copy of it
	name    string         // what of it is bound: see sointu.Unit.BoundValue
	binding sointu.Binding // to which parameter of the module
	module  int            // the index of the module
	// calls are the module units that the unit is inside, from the
	// outermost: the last one uses the module. root is the index of the
	// module that the first one is a unit of, or -1 if it is a unit of an
	// instrument.
	calls []*sointu.Unit
	root  int
}

// argValue returns the value that parameter k (from 1) of the module of the
// last of the module units has: what the module unit sets it to, or the
// default of the module, or if the module unit is an inner unit and binds it
// to a parameter of its own module, what the module unit before it gives
// that one. Song.Expand gives the same.
func (m *Model) argValue(calls []*sointu.Unit, k int) int {
	call, key := calls[len(calls)-1], sointu.ModuleParamName(k)
	if b, ok := call.Bind[key]; ok && len(calls) > 1 {
		return b.Map(m.argValue(calls[:len(calls)-1], b.Param))
	}
	if v, ok := call.Parameters[key]; ok {
		return v
	}
	if i, ok := m.d.Song.Modules.Find(call.Parameters["module"]); ok {
		if mp, ok := m.d.Song.Modules.Param(i, k); ok {
			return mp.Default
		}
	}
	return 0
}

// setArgValue sets the value that argValue returns, like changing the
// parameter of the last of the module units on its own row: if it is bound
// in turn, the module unit before it gets the value, or on the Modules tab,
// the default of the module being edited there.
func (m *Model) setArgValue(root int, calls []*sointu.Unit, k, value int) {
	call, key := calls[len(calls)-1], sointu.ModuleParamName(k)
	if b, ok := call.Bind[key]; ok {
		if len(calls) > 1 {
			m.setArgValue(root, calls[:len(calls)-1], b.Param, b.Unmap(value))
			return
		}
		if root >= 0 && root < len(m.d.Song.Modules) {
			m.setModuleDefault(root, b.Param, b.Unmap(value))
			return
		}
	}
	call.Parameters[key] = value
}

// value returns the value that the bound parameter gets.
func (t *innerBinding) value(m *Model) int {
	v := t.binding.Map(m.argValue(t.calls, t.binding.Param))
	if p, ok := sointu.BindableParam(t.unit.Type, t.name); ok && t.unit.Type != "module" {
		v = min(max(v, p.MinValue), max(p.MaxValue, p.MinValue))
	}
	return v
}

// refresh gives the stand-in the value that the bound parameter gets now.
func (t *innerBinding) refresh(p *Parameter) {
	v := t.value(p.m)
	t.standIn.SetBoundValue(t.name, v)
	if p.arg != nil { // a parameter of a module unit, on a stand-in of its own
		p.unit.SetBoundValue(p.arg.name, v)
	}
}

// store gives the module unit the value of the stand-in.
func (t *innerBinding) store(p *Parameter) {
	p.m.setArgValue(t.root, t.calls, t.binding.Param, t.binding.Unmap(t.standIn.BoundValue(t.name)))
	t.refresh(p)
}

// step changes the parameter of the module by delta instead of the bound
// parameter: with a scaled binding, a step of the bound parameter may be
// less than a step of the parameter of the module.
func (t *innerBinding) step(p *Parameter, delta int, large bool) bool {
	old := min(max(p.m.argValue(t.calls, t.binding.Param), 0), 128)
	value := old + delta
	if large {
		value = roundToGrid(value, 8, delta > 0)
	}
	value = min(max(value, 0), 128)
	if value == old {
		return false
	}
	defer p.m.change("InnerBoundParameter", PatchChange, MinorChange)()
	p.m.setArgValue(t.root, t.calls, t.binding.Param, value)
	t.refresh(p)
	return true
}

// reset gives the parameter of the module its default again.
func (t *innerBinding) reset(p *Parameter) {
	mp, ok := p.m.d.Song.Modules.Param(t.module, t.binding.Param)
	if !ok {
		return
	}
	defer p.m.change("ResetInnerBoundParameter", PatchChange, MinorChange)()
	p.m.setArgValue(t.root, t.calls, t.binding.Param, mp.Default)
	t.refresh(p)
}

// innerParams returns the parameters of an inner unit of a module unit: the
// parameters src of the unit of the module, but for the bound ones, which
// have the values that the module units give them.
func (m *Model) innerParams(src []Parameter, u *sointu.Unit, module, root int, calls []*sointu.Unit) []Parameter {
	var ret []Parameter
	for x := range src {
		unit, name, ok := src[x].bindTarget()
		if !ok || unit != u {
			continue
		}
		b, ok := u.Bind[name]
		if !ok {
			continue
		}
		if up, ok := sointu.BindableParam(u.Type, name); !ok || !up.CanSet {
			continue // only a port
		}
		standIn := new(sointu.Unit)
		*standIn = u.Copy()
		standIn.Bind = nil
		t := &innerBinding{unit: u, standIn: standIn, name: name, binding: b, module: module, calls: calls, root: root}
		standIn.SetBoundValue(name, t.value(m))
		for _, p := range m.deriveParams(standIn, nil) {
			if _, n, ok := p.bindTarget(); ok && n == name {
				if ret == nil {
					ret = slices.Clone(src)
				}
				p.through, p.port = t, src[x].port
				ret[x] = p
				break
			}
		}
	}
	if ret == nil {
		return src
	}
	return ret
}

// moduleParameter vtable: the module that a module unit stands for. Its
// values are 0 for none and i+1 for the i-th module of the song; the unit
// stores the ID of the module.

func (b *moduleParameter) Value(p *Parameter) int {
	if i, ok := p.m.d.Song.Modules.Find(p.unit.Parameters["module"]); ok {
		return i + 1
	}
	return 0
}
func (b *moduleParameter) SetValue(p *Parameter, v int) bool {
	mods := p.m.d.Song.Modules
	if v < 0 || v > len(mods) {
		return false
	}
	// a module cannot use itself: not the module that the unit is a unit of
	if module, ok := p.m.moduleOf(p.unit); v > 0 && ok && p.m.usesModule(mods[v-1].ID, module) {
		p.m.Alerts().Add("A module cannot use itself", Warning)
		return false
	}
	defer p.m.change("ModuleParameter", PatchChange, MajorChange)()
	if v == 0 {
		p.unit.Parameters["module"] = 0
		return true
	}
	// the parameters of the new module start from its defaults
	u := mods.MakeModuleUnit(v - 1)
	for k := 1; k <= sointu.MaxModuleParams; k++ {
		delete(p.unit.Parameters, sointu.ModuleParamName(k))
		delete(p.unit.Bind, sointu.ModuleParamName(k))
	}
	for name, value := range u.Parameters {
		p.unit.Parameters[name] = value
	}
	return true
}
func (b *moduleParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 0, Max: len(p.m.d.Song.Modules)}
}
func (b *moduleParameter) Type(p *Parameter) ParameterType { return ChoiceParameter }
func (b *moduleParameter) Name(p *Parameter) string        { return "module" }
func (b *moduleParameter) StringOf(p *Parameter, v int) string {
	if mods := p.m.d.Song.Modules; v > 0 && v <= len(mods) {
		return moduleTitle(&mods[v-1])
	}
	if p.unit.Parameters["module"] != 0 && v == b.Value(p) {
		return "missing"
	}
	return "none"
}
func (b *moduleParameter) Hint(p *Parameter) ParameterHint {
	v := b.Value(p)
	return ParameterHint{b.StringOf(p, v), v > 0}
}
func (b *moduleParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (b *moduleParameter) Reset(p *Parameter)                             {}

// moduleArgParameter vtable: a parameter of the module of a module unit.
// Its name, range and display are those of the parameter of the module; a
// unit that does not set it has the default of the module.

func (b *moduleArgParameter) param(p *Parameter) sointu.UnitParameter {
	if i, ok := p.m.d.Song.Modules.Find(p.unit.Parameters["module"]); ok {
		if ret, ok := p.m.d.Song.Modules.Param(i, p.index); ok {
			return ret
		}
	}
	return sointu.UnitParameter{Name: sointu.ModuleParamName(p.index)}
}
func (b *moduleArgParameter) Value(p *Parameter) int {
	mp := b.param(p)
	if v, ok := p.unit.Parameters[p.up.Name]; ok {
		return min(max(v, mp.MinValue), max(mp.MaxValue, mp.MinValue))
	}
	return mp.Default
}
func (b *moduleArgParameter) SetValue(p *Parameter, v int) bool {
	defer p.m.change("ModuleArgParameter"+p.up.Name, PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = v
	return true
}
func (b *moduleArgParameter) Range(p *Parameter) RangeInclusive {
	mp := b.param(p)
	return RangeInclusive{Min: mp.MinValue, Max: max(mp.MaxValue, mp.MinValue)}
}
func (b *moduleArgParameter) Type(p *Parameter) ParameterType {
	mp := b.param(p)
	if !mp.CanSet {
		return NoParameter // only a port
	}
	if mp.MinValue >= -1 && mp.MaxValue <= 1 {
		return BoolParameter
	}
	return IntegerParameter
}
func (b *moduleArgParameter) Name(p *Parameter) string { return b.param(p).Name }
func (b *moduleArgParameter) Hint(p *Parameter) ParameterHint {
	mp, val := b.param(p), b.Value(p)
	if mp.DisplayFunc != nil {
		valueInUnits, units := mp.DisplayFunc(val)
		return ParameterHint{fmt.Sprintf("%s %s", valueInUnits, units), true}
	}
	return ParameterHint{strconv.Itoa(val), true}
}

// Label is the text on the knob: with a scaled binding, the value that the
// first bound parameter gets, as that parameter shows it.
func (b *moduleArgParameter) Label(p *Parameter) string {
	if i, ok := p.m.d.Song.Modules.Find(p.unit.Parameters["module"]); ok {
		if source, name, ok := p.m.d.Song.Modules.ParamSourceUnit(i, p.index); ok && source.Bind[name].Scaled {
			if mp := b.param(p); mp.DisplayFunc != nil {
				value, _ := mp.DisplayFunc(b.Value(p))
				return value
			}
		}
	}
	return strconv.Itoa(b.Value(p))
}

func (b *moduleArgParameter) RoundToGrid(p *Parameter, val int, up bool) int {
	return roundToGrid(val, 8, up)
}
func (b *moduleArgParameter) Reset(p *Parameter) {
	defer p.m.change("ResetModuleArgParameter", PatchChange, MinorChange)()
	p.unit.Parameters[p.up.Name] = b.param(p).Default
}

// moduleArg is what makes a parameter of a module unit look and work like
// the parameter that the module binds to it, e.g. a menu of the buffers for
// a buffer, or note lengths for a delay time following the tempo. The
// Parameter is that of a stand-in unit: a copy of the unit of the module with
// the bound parameter, holding the value of the module unit. Its vtable
// works on the stand-in, and after a change the module unit gets the value.
type moduleArg struct {
	call   *sointu.Unit // the module unit
	module int          // the index of its module
	k      int          // the parameter of the module, from 1
	name   string       // what is bound to it in the stand-in: see sointu.Unit.BoundValue
}

func (a *moduleArg) key() string { return sointu.ModuleParamName(a.k) }

func (a *moduleArg) param(m *Model) sointu.UnitParameter {
	if a.module < len(m.d.Song.Modules) {
		if p, ok := m.d.Song.Modules.Param(a.module, a.k); ok {
			return p
		}
	}
	return sointu.UnitParameter{Name: a.key()}
}

// store gives the module unit the value of the stand-in unit.
func (a *moduleArg) store(p *Parameter) {
	a.call.Parameters[a.key()] = p.unit.BoundValue(a.name)
}

// moduleArg returns parameter k (from 1) of a module unit as a Parameter:
// like the first parameter bound to it, on a stand-in unit, or if nothing is
// bound to it or it is not shown, a plain one. up is p<k> of the module
// unit type.
func (m *Model) moduleArg(call *sointu.Unit, module, k int, up *sointu.UnitParameter) Parameter {
	plain := Parameter{m: m, unit: call, up: up, index: k, vtable: &moduleArgParameter{}, port: k}
	source, name, ok := m.d.Song.Modules.ParamSourceUnit(module, k)
	if !ok || source.Bind[name].Scaled || source.Type == "module" {
		// with a scaled binding the values are 0 to 128, not those of the
		// bound parameter: a plain knob, labelled with what they give
		return plain
	}
	standIn := new(sointu.Unit)
	*standIn = source.Copy()
	standIn.Bind = nil
	arg := &moduleArg{call: call, module: module, k: k, name: name}
	value, ok := call.Parameters[arg.key()]
	if !ok {
		value = arg.param(m).Default
	}
	standIn.SetBoundValue(name, value)
	for _, p := range m.deriveParams(standIn, nil) {
		if _, n, ok := p.bindTarget(); ok && n == name {
			p.arg, p.port = arg, k
			return p
		}
	}
	return plain
}
