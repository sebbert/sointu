package tracker

import "github.com/vsariola/sointu"

// The rows of the unit editor. Normally they are the units being edited. A
// module unit can be unfolded (UnitModel.Unfold); it is then followed by its
// inner units:
// the units that it stands for, which can be seen but not changed. For a
// module unit of an instrument, they are the units that the synth runs for
// it, with the values that the module unit gives the bound parameters and
// with the units of the modules that its module uses. For a module unit of a
// module, they are the units of its module as they are.
//
// modelData.UnitIndex and UnitIndex2 stay indices of the units being edited;
// UnitModel.List, UnitModel.Item and ParamModel work in rows. The cursor is
// never on an inner unit: moving onto one moves past them.

type (
	// innerUnits are the units that a module unit stands for, and what is
	// derived from them.
	innerUnits struct {
		units       []sointu.Unit
		source      []sointu.ExpandedUnit // where each unit came from; zero for the units of a module as they are
		params      [][]Parameter
		uses        []sointu.StackUse
		before      []int // the signals of the module on the stack before each unit, less its inputs
		width       int   // the most signals of the module on the stack
		paramsWidth int   // the most parameters of a unit, with room for previews
	}

	// innerCache holds the inner units of the module units being edited, by
	// the ID of the module unit, as long as the song the player has, the
	// tab, the instrument and the module stay the same.
	innerCache struct {
		expansion *sointu.Expansion
		editing   bool
		index     int
		byCall    map[int]*innerUnits
	}
)

// Unfold returns a Bool telling whether the selected module unit is
// unfolded, like a section that can be collapsed: the unit editor then shows
// the units that it stands for under it. Setting it folds or unfolds all the
// selected module units.
func (m *UnitModel) Unfold() Bool { return MakeBool((*unitUnfold)(m)) }

type unitUnfold UnitModel

func (m *unitUnfold) Enabled() bool {
	_, _, ok := (*Model)(m).selectedModuleUnit()
	return ok
}
func (m *unitUnfold) Value() bool {
	u, _, ok := (*Model)(m).selectedModuleUnit()
	return ok && u.Unfolded
}
func (m *unitUnfold) SetValue(val bool) {
	units := (*Model)(m).units()
	r := (*Model)(m).unitRange()
	for i := r.Start; i < r.End; i++ {
		(*Model)(m).setUnfolded(&units[i], val)
	}
}

// ToggleUnfold returns an Action to fold or unfold the module unit on the
// given row of the unit editor.
func (m *UnitModel) ToggleUnfold(row int) Action {
	return MakeAction(toggleUnfold{row: row, UnitModel: m})
}

type toggleUnfold struct {
	row int
	*UnitModel
}

func (m toggleUnfold) Do() {
	model := (*Model)(m.UnitModel)
	units := model.units()
	if index, e, _, ok := model.rowAt(m.row); ok && e == nil && index < len(units) {
		model.setUnfolded(&units[index], !units[index].Unfolded)
	}
}

// setUnfolded folds or unfolds a module unit. It is kept in the unit
// (sointu.Unit.Unfolded), so it is saved with the song and undone, but the
// player is not told: nothing it plays changes.
func (m *Model) setUnfolded(u *sointu.Unit, val bool) {
	if u.Type != "module" || u.Unfolded == val {
		return
	}
	defer m.change("Unfold", NoChange, MinorChange)()
	u.Unfolded = val
	m.d.UnitSearching = false
}

// HasModuleUnits reports whether there are module units among the units
// being edited.
func (m *UnitModel) HasModuleUnits() bool {
	for _, u := range (*Model)(m).units() {
		if u.Type == "module" {
			return true
		}
	}
	return false
}

// unfold reports whether any of the units being edited is unfolded:
// otherwise the rows of the unit editor are the units being edited.
func (m *Model) unfold() bool {
	for _, u := range m.units() {
		if u.Unfolded {
			return true
		}
	}
	return false
}

// innerUnitsOf returns the inner units of a unit being edited, or nil if it has
// none or they are not shown.
func (m *Model) innerUnitsOf(u *sointu.Unit) *innerUnits {
	if !u.Unfolded || u.Type != "module" || u.Disabled || u.ID == 0 {
		return nil
	}
	c := &m.innerCache
	index := m.d.InstrIndex
	if m.editingModule() {
		index = m.d.ModuleIndex
	}
	if c.expansion != m.expansion || c.editing != m.editingModule() || c.index != index || c.byCall == nil {
		*c = innerCache{expansion: m.expansion, editing: m.editingModule(), index: index, byCall: map[int]*innerUnits{}}
	}
	if e, ok := c.byCall[u.ID]; ok {
		return e
	}
	e := m.makeInnerUnits(u)
	c.byCall[u.ID] = e
	return e
}

func (m *Model) makeInnerUnits(call *sointu.Unit) *innerUnits {
	index, ok := m.d.Song.Modules.Find(call.Parameters["module"])
	if !ok {
		return nil
	}
	e := &innerUnits{}
	if !m.editingModule() {
		if m.expansion == nil || m.d.InstrIndex < 0 || m.d.InstrIndex >= len(m.expanded) {
			return nil
		}
		// the units of the synth that came from the module unit
		for _, u := range m.expanded[m.d.InstrIndex].Units {
			if x, ok := m.expansion.Units[u.ID]; ok && x.Call == call.ID && x.Instrument == m.d.InstrIndex {
				e.units = append(e.units, u.Copy())
				e.source = append(e.source, x)
			}
		}
	} else {
		for _, u := range m.d.Song.Modules[index].Units {
			e.units = append(e.units, u.Copy())
			e.source = append(e.source, sointu.ExpandedUnit{})
		}
	}
	if len(e.units) == 0 {
		return nil
	}
	e.params = make([][]Parameter, len(e.units))
	e.uses = make([]sointu.StackUse, len(e.units))
	e.before = make([]int, len(e.units))
	depth := max(m.d.Song.Modules[index].Inputs, 0)
	e.width = depth
	previews := false
	for i := range e.units {
		u := &e.units[i]
		e.params[i] = m.deriveParams(u, nil)
		for j := range e.params[i] {
			e.params[i][j].inner, e.params[i][j].innerIndex = e, i
		}
		e.paramsWidth = max(e.paramsWidth, len(e.params[i]))
		_, _, ok := unitBuffer(u)
		previews = previews || ok
		e.uses[i] = m.d.Song.Modules.StackUse(u)
		depth = max(depth-len(e.uses[i].Inputs), 0)
		e.before[i] = depth
		e.width = max(e.width, depth+max(len(e.uses[i].Inputs), e.uses[i].NumOutputs))
		depth += e.uses[i].NumOutputs
	}
	if previews {
		e.paramsWidth += UnitPreviewCells
	}
	return e
}

// bound returns the name of the parameter of a module that a parameter of
// inner unit i is bound to.
func (e *innerUnits) bound(m *Model, i int, param string) (string, bool) {
	if i < 0 || i >= len(e.units) || e.source[i].Body == 0 {
		return "", false // the units of a module as they are show no bindings: they are not those of the module being edited
	}
	module, ok := m.d.Song.Modules.Find(e.source[i].Module)
	if !ok {
		return "", false
	}
	for j := range m.d.Song.Modules[module].Units {
		u := &m.d.Song.Modules[module].Units[j]
		if u.ID != e.source[i].Body {
			continue
		}
		k, ok := u.Bind[param]
		if !ok {
			return "", false
		}
		if mp, ok := m.d.Song.Modules.Param(module, k); ok {
			return mp.Name, true
		}
		return sointu.ModuleParamName(k), true
	}
	return "", false
}

// numRows returns the number of rows of the unit editor.
func (m *Model) numRows() int {
	units := m.units()
	n := len(units)
	if !m.unfold() {
		return n
	}
	for i := range units {
		if e := m.innerUnitsOf(&units[i]); e != nil {
			n += len(e.units)
		}
	}
	return n
}

// rowOfUnit returns the row of the unit with the given index among the
// units being edited.
func (m *Model) rowOfUnit(index int) int {
	if !m.unfold() {
		return index
	}
	units := m.units()
	row := index
	for i := 0; i < index && i < len(units); i++ {
		if e := m.innerUnitsOf(&units[i]); e != nil {
			row += len(e.units)
		}
	}
	return row
}

// rowAt returns what is on a row: the index of a unit being edited, and if
// the row is an inner unit of that unit, its inner units and the index among them
// (otherwise e is nil). ok is false if there is no such row.
func (m *Model) rowAt(row int) (unit int, e *innerUnits, inner int, ok bool) {
	units := m.units()
	if !m.unfold() {
		return row, nil, 0, row >= 0 && row < len(units)
	}
	if row < 0 {
		return 0, nil, 0, false
	}
	for i := range units {
		if row == 0 {
			return i, nil, 0, true
		}
		row--
		if e := m.innerUnitsOf(&units[i]); e != nil {
			if row < len(e.units) {
				return i, e, row, true
			}
			row -= len(e.units)
		}
	}
	return 0, nil, 0, false
}

// unitOfRow returns the index of the unit that selecting a row selects: the
// unit on the row, or for an inner unit, the module unit it belongs to. With
// down, an inner unit selects the unit after its module unit instead, if there
// is one: moving down from a module unit goes past its inner units.
func (m *Model) unitOfRow(row int, down bool) int {
	unit, e, _, ok := m.rowAt(row)
	if !ok {
		if row < 0 {
			return 0
		}
		return max(len(m.units())-1, 0)
	}
	if e != nil && down && unit+1 < len(m.units()) {
		return unit + 1
	}
	return unit
}

// unitRange returns the range of the selected units, as indices of the
// units being edited.
func (m *Model) unitRange() Range {
	n := len(m.units())
	a := max(min(m.d.UnitIndex, m.d.UnitIndex2, n-1), 0)
	b := min(max(m.d.UnitIndex, m.d.UnitIndex2)+1, n)
	return Range{a, max(b, a)}
}

// unitRows is the units being edited as a list of the rows of the unit
// editor.
type unitRows UnitModel

func (v *unitRows) units() *unitList { return (*unitList)(v) }
func (v *unitRows) Count() int       { return (*Model)(v).numRows() }
func (v *unitRows) Selected() int    { return (*Model)(v).rowOfUnit(v.d.UnitIndex) }
func (v *unitRows) Selected2() int   { return (*Model)(v).rowOfUnit(v.d.UnitIndex2) }

// Selecting the row after a module unit, an inner unit, is taken as moving
// down from it; selecting any other inner unit, e.g. by clicking it, selects
// its module unit.
func (v *unitRows) SetSelected(row int) {
	v.units().SetSelected((*Model)(v).unitOfRow(row, row == v.Selected()+1))
}
func (v *unitRows) SetSelected2(row int) {
	v.units().SetSelected2((*Model)(v).unitOfRow(row, row == v.Selected2()+1))
}

// unitRangeOf returns the units on the rows of r, which starts and ends
// with rows of units.
func (v *unitRows) unitRangeOf(r Range) Range {
	m := (*Model)(v)
	if !m.unfold() {
		return r
	}
	a, _, _, ok := m.rowAt(r.Start)
	b, _, _, ok2 := m.rowAt(r.End - 1)
	if !ok || !ok2 {
		return Range{}
	}
	return Range{a, b + 1}
}
func (v *unitRows) Move(r Range, delta int) (ok bool) {
	u := v.unitRangeOf(r)
	delta = min(max(delta, -1), 1) // a row at a time is a unit at a time
	if u.Len() <= 0 || u.Start+delta < 0 || u.End+delta > v.units().Count() {
		return false
	}
	return v.units().Move(u, delta)
}
func (v *unitRows) Delete(r Range) (ok bool) {
	u := v.unitRangeOf(r)
	return u.Len() > 0 && v.units().Delete(u)
}
func (v *unitRows) Change(n string, severity ChangeSeverity) func() {
	return v.units().Change(n, severity)
}
func (v *unitRows) Cancel() { v.units().Cancel() }
func (v *unitRows) Marshal(r Range) ([]byte, error) {
	return v.units().Marshal(v.unitRangeOf(r))
}
func (v *unitRows) Unmarshal(data []byte) (r Range, err error) {
	u, err := v.units().Unmarshal(data)
	if err != nil {
		return Range{}, err
	}
	m := (*Model)(v)
	return Range{m.rowOfUnit(u.Start), m.rowOfUnit(u.End-1) + 1}, nil
}
