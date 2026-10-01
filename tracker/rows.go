package tracker

import (
	"slices"

	"github.com/vsariola/sointu"
)

// The rows of the unit editor. They start from the root units: the units of
// the selected instrument, or on the Modules tab, of the selected module. A
// module unit can be unfolded (UnitModel.Unfold); it is then followed by its
// inner units: the units of its module, as they are in the module. They can
// be changed there like on the Modules tab, which changes the module, and so
// every module unit using it. A parameter bound to a parameter of the module
// shows the value that the module unit above gives it, and changing it
// changes that value of the module unit (innerBinding). A module unit among
// the inner units can be unfolded in turn.
//
// The cursor can be on any row. The units being edited (Model.units) are the
// list of units that the cursor is in: the root units, or the units of the
// module of the module unit that the cursor is inside. modelData.UnitPath
// tells which: the IDs of the module units that the cursor is inside, from
// the outermost. modelData.UnitIndex and UnitIndex2 are indices of the units
// being edited, so a selection never leaves one list of units, and the
// actions on the selected units work on the module that the cursor is in.
// UnitModel.List, UnitModel.Item and ParamModel work in rows.

type (
	// unitRow is a row of the unit editor.
	unitRow struct {
		module  int  // the index of the module that the unit is a unit of, or -1 for a unit of an instrument
		index   int  // the index of the unit among the units of the module or the instrument
		list    int  // the index of its list in rowCache.lists
		parent  int  // the row of the module unit that it is an inner unit of, or -1
		depth   int  // the number of module units that it is inside
		call    int  // the ID of the outermost of them, or 0
		signals Rail // with the signals passing by the module units that it is inside
		outer   int  // the signals passing by the outermost of them
		params  []Parameter
	}

	// unitRowList is a list of units shown in the unit editor: the root
	// units, or the inner units of one module unit.
	unitRowList struct {
		module int   // as unitRow.module
		parent int   // the row of the module unit, or -1
		pass   int   // the signals passing by the units of the list
		rows   []int // the row of each unit
	}

	// rowCache holds the rows of the unit editor, until the song, what is
	// unfolded or what the unit editor shows changes.
	rowCache struct {
		valid       bool
		tab         InstrumentTab
		instr, mod  int
		path        []int
		rows        []unitRow
		lists       []unitRowList
		scope       int // the index in lists of the units being edited
		paramsWidth int // the most parameters of a row, with room for previews
		railWidth   int // the most signals on a row
	}
)

// root returns the root units of the unit editor: the index of the selected
// module on the Modules tab, or -1 for the units of the selected instrument.
// ok is false if there is no such module or instrument.
func (m *Model) root() (module int, ok bool) {
	if m.editingModule() {
		return m.d.ModuleIndex, m.d.ModuleIndex >= 0 && m.d.ModuleIndex < len(m.d.Song.Modules)
	}
	return -1, m.d.InstrIndex >= 0 && m.d.InstrIndex < len(m.d.Song.Patch)
}

// unitsPtrOf returns the units of the module with the given index, or with
// -1, of the selected instrument.
func (m *Model) unitsPtrOf(module int) *[]sointu.Unit {
	if module >= 0 {
		if module < len(m.d.Song.Modules) {
			return &m.d.Song.Modules[module].Units
		}
		return nil
	}
	if i := m.d.InstrIndex; i >= 0 && i < len(m.d.Song.Patch) {
		return &m.d.Song.Patch[i].Units
	}
	return nil
}

// derivedOf returns the derived data of the units that unitsPtrOf returns.
func (m *Model) derivedOf(module int) *derivedInstrument {
	if module >= 0 {
		if module < len(m.derived.modules) && module < len(m.d.Song.Modules) {
			return &m.derived.modules[module]
		}
		return nil
	}
	if i := m.d.InstrIndex; i >= 0 && i < len(m.derived.patch) && i < len(m.d.Song.Patch) {
		return &m.derived.patch[i]
	}
	return nil
}

// innerModule returns the index of the module whose units are shown under
// the unit: if it is an unfolded module unit with a module that has units.
// shown are the modules that the unit is shown inside: a module using itself
// is not unfolded again.
func (m *Model) innerModule(u *sointu.Unit, shown []int) (module int, ok bool) {
	if !u.Unfolded || u.Type != "module" || u.Disabled || u.ID == 0 {
		return 0, false
	}
	module, ok = m.d.Song.Modules.Find(u.Parameters["module"])
	if !ok || len(m.d.Song.Modules[module].Units) == 0 || slices.Contains(shown, module) {
		return 0, false
	}
	return module, true
}

// scope returns the units being edited: the root units, or with the cursor
// inside module units (modelData.UnitPath), the units of the module of the
// innermost one. module is the index of the module that they are the units
// of, or -1 for the units of an instrument. depth tells how many module
// units the cursor is inside: less than UnitPath has, if one of them no
// longer shows its inner units.
func (m *Model) scope() (list *[]sointu.Unit, module, depth int) {
	module, ok := m.root()
	if !ok {
		return nil, module, 0
	}
	list = m.unitsPtrOf(module)
	if len(m.d.UnitPath) == 0 {
		return list, module, 0
	}
	var buf [8]int
	shown := append(buf[:0], module)
	for _, id := range m.d.UnitPath {
		i := slices.IndexFunc(*list, func(u sointu.Unit) bool { return u.ID == id })
		if i < 0 {
			break
		}
		inner, ok := m.innerModule(&(*list)[i], shown)
		if !ok {
			break
		}
		module, list = inner, &m.d.Song.Modules[inner].Units
		shown = append(shown, inner)
		depth++
	}
	return list, module, depth
}

// scopeModule returns the index of the module whose units are being edited:
// the selected module on the Modules tab, or the module of the module unit
// that the cursor is inside. ok is false if the units of an instrument are
// being edited.
func (m *Model) scopeModule() (module int, ok bool) {
	list, module, _ := m.scope()
	return module, list != nil && module >= 0
}

// fixScope keeps the cursor valid after a change. If a module unit that the
// cursor was inside no longer shows its inner units, e.g. because it was
// folded, the cursor goes to that module unit.
func (m *Model) fixScope() {
	list, _, depth := m.scope()
	if depth < len(m.d.UnitPath) {
		if list != nil {
			id := m.d.UnitPath[depth]
			if i := slices.IndexFunc(*list, func(u sointu.Unit) bool { return u.ID == id }); i >= 0 {
				m.d.UnitIndex, m.d.UnitIndex2 = i, i
			}
		}
		m.d.UnitPath = slices.Clone(m.d.UnitPath[:depth])
	}
	n := 0
	if list != nil {
		n = len(*list)
	}
	m.d.UnitIndex = clamp(m.d.UnitIndex, 0, n-1)
	m.d.UnitIndex2 = clamp(m.d.UnitIndex2, 0, n-1)
}

// leaveModuleUnits puts the cursor back among the root units, e.g. when the
// unit editor is about to show other units.
func (m *Model) leaveModuleUnits() { m.d.UnitPath = nil }

// rows returns the rows of the unit editor.
func (m *Model) rows() *rowCache {
	c := &m.rowCache
	if c.valid && c.tab == m.d.InstrumentTab && c.instr == m.d.InstrIndex && c.mod == m.d.ModuleIndex && slices.Equal(c.path, m.d.UnitPath) {
		return c
	}
	*c = rowCache{valid: true, tab: m.d.InstrumentTab, instr: m.d.InstrIndex, mod: m.d.ModuleIndex,
		path: append(c.path[:0], m.d.UnitPath...), rows: c.rows[:0], lists: c.lists[:0]}
	root, ok := m.root()
	if !ok {
		return c
	}
	_, _, depth := m.scope()
	m.addRows(c, root, -1, 0, 0, m.d.UnitPath[:depth], true, []int{root}, nil)
	return c
}

// addRows adds the rows of the units of a module, or with -1, of the selected
// instrument, and of the inner units of the unfolded module units among
// them. parent is the row of the module unit that they are the inner units
// of, pass the signals passing by them and outer those passing by the
// outermost module unit that they are inside. path are the IDs of the module
// units that the cursor is inside from here on, if along is true: if the
// cursor is in these units or their inner units. shown are the modules being
// shown, and calls the module units that the units are inside, from the
// outermost.
func (m *Model) addRows(c *rowCache, module, parent, pass, outer int, path []int, along bool, shown []int, calls []*sointu.Unit) {
	ptr, d := m.unitsPtrOf(module), m.derivedOf(module)
	if ptr == nil || d == nil {
		return
	}
	units := *ptr
	root, _ := m.root()
	list := len(c.lists)
	c.lists = append(c.lists, unitRowList{module: module, parent: parent, pass: pass, rows: make([]int, len(units))})
	if along && len(path) == 0 {
		c.scope = list
	}
	depth, call := len(calls), 0
	if depth > 0 {
		call = calls[0].ID
	}
	c.paramsWidth = max(c.paramsWidth, d.paramsWidth)
	c.railWidth = max(c.railWidth, pass+d.railWidth)
	for i := range units {
		u := &units[i]
		row := len(c.rows)
		c.lists[list].rows[i] = row
		r := unitRow{module: module, index: i, list: list, parent: parent, depth: depth, call: call, outer: outer}
		if i < len(d.rails) {
			r.signals = d.rails[i]
			r.signals.PassThrough += pass
		}
		if i < len(d.params) {
			r.params = d.params[i]
			if depth > 0 {
				r.params = m.innerParams(r.params, u, module, root, calls)
			}
		}
		c.rows = append(c.rows, r)
		inner, ok := m.innerModule(u, shown)
		if !ok {
			continue
		}
		below := r.signals.PassThrough // the signals passing by the module unit pass by its inner units
		o := outer
		if depth == 0 {
			o = below
		}
		enter := along && len(path) > 0 && path[0] == u.ID
		var rest []int
		if enter {
			rest = path[1:]
		}
		m.addRows(c, inner, row, below, o, rest, enter, append(shown, inner), append(calls[:depth:depth], u))
	}
}

// numRows returns the number of rows of the unit editor.
func (m *Model) numRows() int { return len(m.rows().rows) }

// rowOfUnit returns the row of the unit with the given index among the
// units being edited.
func (m *Model) rowOfUnit(index int) int {
	c := m.rows()
	if c.scope >= len(c.lists) {
		return 0
	}
	rows := c.lists[c.scope].rows
	if len(rows) == 0 {
		return max(c.lists[c.scope].parent, 0)
	}
	return rows[clamp(index, 0, len(rows)-1)]
}

// rowUnit returns the unit on a row of the unit editor.
func (m *Model) rowUnit(row int) (*sointu.Unit, *unitRow, bool) {
	c := m.rows()
	if row < 0 || row >= len(c.rows) {
		return nil, nil, false
	}
	r := &c.rows[row]
	if ptr := m.unitsPtrOf(r.module); ptr != nil && r.index < len(*ptr) {
		return &(*ptr)[r.index], r, true
	}
	return nil, nil, false
}

// selectRow puts the cursor on a row. On a row of other units than those
// being edited, e.g. on an inner unit of a module unit, the units being
// edited become those of the row, and the selection only the row.
func (m *Model) selectRow(row int) {
	c := m.rows()
	if len(c.rows) == 0 {
		return
	}
	r := c.rows[clamp(row, 0, len(c.rows)-1)]
	if r.list != c.scope {
		var path []int
		for p := r.parent; p >= 0; p = c.rows[p].parent {
			if u, _, ok := m.rowUnit(p); ok {
				path = append(path, u.ID)
			}
		}
		slices.Reverse(path)
		m.d.UnitPath = path
		m.d.UnitIndex2 = r.index
	}
	m.d.UnitIndex = r.index
}

// unitNearRow returns the index of the unit, among the units being edited,
// that a selection from the cursor to the row ends with: the unit on the
// row, the one that the row is inside, or the first or last one.
func (m *Model) unitNearRow(row int) int {
	c := m.rows()
	if len(c.rows) == 0 || c.scope >= len(c.lists) {
		return 0
	}
	row = clamp(row, 0, len(c.rows)-1)
	for r := row; r >= 0; r = c.rows[r].parent {
		if c.rows[r].list == c.scope {
			return c.rows[r].index
		}
	}
	if rows := c.lists[c.scope].rows; len(rows) > 0 && row > rows[0] {
		return len(rows) - 1
	}
	return 0
}

// rowInScope tells if the unit on the row is one of the units being edited.
func (m *Model) rowInScope(row int) bool {
	c := m.rows()
	return row >= 0 && row < len(c.rows) && c.rows[row].list == c.scope
}

// unitRange returns the range of the selected units, as indices of the
// units being edited.
func (m *Model) unitRange() Range {
	n := len(m.units())
	a := max(min(m.d.UnitIndex, m.d.UnitIndex2, n-1), 0)
	b := min(max(m.d.UnitIndex, m.d.UnitIndex2)+1, n)
	return Range{a, max(b, a)}
}

// Unfold returns a Bool telling whether the selected module unit is
// unfolded, like a section that can be collapsed: the unit editor then shows
// the units of its module under it. Setting it folds or unfolds all the
// selected module units. With the cursor on an inner unit that is no module
// unit, it folds the module unit that the cursor is inside.
func (m *UnitModel) Unfold() Bool { return MakeBool((*unitUnfold)(m)) }

type unitUnfold UnitModel

// enclosing returns the module unit that the cursor is inside, if the
// selected unit is no module unit to fold or unfold itself.
func (m *unitUnfold) enclosing() *sointu.Unit {
	if _, _, ok := (*Model)(m).selectedModuleUnit(); ok {
		return nil
	}
	c := (*Model)(m).rows()
	if c.scope >= len(c.lists) || c.lists[c.scope].parent < 0 {
		return nil
	}
	u, _, _ := (*Model)(m).rowUnit(c.lists[c.scope].parent)
	return u
}
func (m *unitUnfold) Enabled() bool {
	_, _, ok := (*Model)(m).selectedModuleUnit()
	return ok || m.enclosing() != nil
}
func (m *unitUnfold) Value() bool {
	u, _, ok := (*Model)(m).selectedModuleUnit()
	return ok && u.Unfolded || m.enclosing() != nil
}
func (m *unitUnfold) SetValue(val bool) {
	if u := m.enclosing(); u != nil {
		(*Model)(m).setUnfolded(u, val)
		return
	}
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
	if u, _, ok := (*Model)(m.UnitModel).rowUnit(m.row); ok {
		(*Model)(m.UnitModel).setUnfolded(u, !u.Unfolded)
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

// HasModuleUnits reports whether there are module units among the root
// units of the unit editor.
func (m *UnitModel) HasModuleUnits() bool {
	root, ok := (*Model)(m).root()
	if !ok {
		return false
	}
	for _, u := range *(*Model)(m).unitsPtrOf(root) {
		if u.Type == "module" {
			return true
		}
	}
	return false
}

// InModule returns the name of the module whose units the cursor is in, as
// inner units of a module unit, and the number of module units using it:
// changing the units changes them all. ok is false if the cursor is not
// inside a module unit.
func (m *UnitModel) InModule() (name string, uses int, ok bool) {
	_, module, depth := (*Model)(m).scope()
	if depth == 0 || module < 0 {
		return "", 0, false
	}
	mod := &m.d.Song.Modules[module]
	return moduleTitle(mod), (*Model)(m).moduleUses(mod.ID), true
}

// unitRows is the rows of the unit editor as a list. Moving, deleting,
// copying and pasting work on the units being edited: see elements.
type unitRows UnitModel

func (v *unitRows) units() *unitList { return (*unitList)(v) }
func (v *unitRows) elements() List   { return List{v.units()} }
func (v *unitRows) Count() int       { return (*Model)(v).numRows() }
func (v *unitRows) Selected() int    { return (*Model)(v).rowOfUnit(v.d.UnitIndex) }
func (v *unitRows) Selected2() int   { return (*Model)(v).rowOfUnit(v.d.UnitIndex2) }
func (v *unitRows) SetSelected(row int) {
	(*Model)(v).selectRow(row)
	v.d.ParamIndex = 0
	v.d.UnitSearching = false
	v.d.UnitSearchString = ""
}
func (v *unitRows) SetSelected2(row int) { v.d.UnitIndex2 = (*Model)(v).unitNearRow(row) }

// extendSelection moves the cursor by delta units among the units being
// edited, past the inner units of the module units among them.
func (v *unitRows) extendSelection(delta int) {
	v.units().SetSelected(clamp(v.d.UnitIndex+delta, 0, max(v.units().Count()-1, 0)))
}
