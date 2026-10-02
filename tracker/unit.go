package tracker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

// Unit returns the Unit view of the model, containing methods to manipulate the
// units.
func (m *Model) Unit() *UnitModel { return (*UnitModel)(m) }

type UnitModel Model

// Add returns an Action to add a new unit. If the before parameter is true,
// then the new unit is added before the currently selected unit; otherwise,
// after.
func (m *UnitModel) Add(before bool) Action {
	return MakeAction(addUnit{Before: before, Model: (*Model)(m)})
}

type addUnit struct {
	Before bool
	*Model
}

func (a addUnit) Do() {
	m := (*Model)(a.Model)
	defer m.change("AddUnitAction", PatchChange, MajorChange)()
	switch {
	case m.editingModule() && m.unitsPtr() == nil: // no modules, add one
		m.addModule()
	case !m.editingModule() && len(m.d.Song.Patch) == 0: // no instruments, add one
		instr := sointu.Instrument{NumVoices: 1}
		instr.Units = make([]sointu.Unit, 0, 1)
		m.d.Song.Patch = append(m.d.Song.Patch, instr)
		m.d.UnitIndex = 0
	case len(m.units()) > 0 && !a.Before:
		m.d.UnitIndex++
	}
	if !m.editingModule() {
		m.d.InstrIndex = max(min(m.d.InstrIndex, len(m.d.Song.Patch)-1), 0)
	}
	list := m.unitsPtr()
	if list == nil {
		m.changeCancel = true
		return
	}
	newUnits := make([]sointu.Unit, len(*list)+1)
	m.d.UnitIndex = clamp(m.d.UnitIndex, 0, len(newUnits)-1)
	m.d.UnitIndex2 = m.d.UnitIndex
	copy(newUnits, (*list)[:m.d.UnitIndex])
	copy(newUnits[m.d.UnitIndex+1:], (*list)[m.d.UnitIndex:])
	m.assignUnitIDs(newUnits[m.d.UnitIndex : m.d.UnitIndex+1])
	*list = newUnits
	m.d.ParamIndex = 0
}

// Delete returns an Action to delete the currently selected unit(s).
func (m *UnitModel) Delete() Action { return MakeAction((*deleteUnit)(m)) }

type deleteUnit UnitModel

func (m *deleteUnit) Enabled() bool {
	// an instrument keeps at least one unit; a module can be emptied, but
	// only on the Modules tab: an unfolded module unit keeps an inner unit
	// for the cursor to be on
	list, module, depth := (*Model)(m).scope()
	if list == nil {
		return false
	}
	return len(*list) > 1 || len(*list) == 1 && module >= 0 && depth == 0
}
func (m *deleteUnit) Do() {
	defer (*Model)(m).change("DeleteUnitAction", PatchChange, MajorChange)()
	(*UnitModel)(m).List().DeleteElements(true)
}

// Clear returns an Action to clear the currently selected unit(s) i.e. they are
// set as empty units, but are kept in the unit list.
func (m *UnitModel) Clear() Action { return MakeAction((*clearUnit)(m)) }

type clearUnit UnitModel

func (m *clearUnit) Enabled() bool {
	return len((*Model)(m).units()) > 0
}
func (m *clearUnit) Do() {
	defer (*Model)(m).change("DeleteUnitAction", PatchChange, MajorChange)()
	r := (*Model)(m).unitRange()
	units := (*Model)(m).units()
	for i := r.Start; i < r.End && i < len(units); i++ {
		units[i] = sointu.Unit{}
		units[i].ID = (*Model)(m).maxID() + 1
	}
}

// Searching returns a Bool telling whether the user is currently searching for
// a unit (should the search resultsbe displayed).
func (m *UnitModel) Searching() Bool { return MakeBool((*unitSearching)(m)) }

type unitSearching UnitModel

func (m *unitSearching) Value() bool { return m.d.UnitSearching }
func (m *unitSearching) SetValue(val bool) {
	m.d.UnitSearching = val
	u := (*Model)(m).selectedUnit()
	if u == nil {
		m.d.UnitSearchString = ""
		return
	}
	m.d.UnitSearchString = u.Type
	(*UnitModel)(m).updateDerivedUnitSearch()
}

// SearchTerm returns a String which is the search term user has typed when
// searching for units.
func (m *UnitModel) SearchTerm() String { return MakeString((*unitSearchTerm)(m)) }

type unitSearchTerm UnitModel

func (v *unitSearchTerm) Value() string {
	// return current unit type string if not searching
	if !v.d.UnitSearching {
		if u := (*Model)(v).selectedUnit(); u != nil {
			return u.Type
		}
		return ""
	} else {
		return v.d.UnitSearchString
	}
}
func (v *unitSearchTerm) SetValue(value string) bool {
	v.d.UnitSearchString = value
	v.d.UnitSearching = true
	(*UnitModel)(v).updateDerivedUnitSearch()
	return true
}

func (v *UnitModel) updateDerivedUnitSearch() {
	// update search results based on current search string
	v.derived.searchResults = v.derived.searchResults[:0]
	for _, name := range sointu.UnitNames {
		if strings.HasPrefix(name, v.SearchTerm().Value()) {
			v.derived.searchResults = append(v.derived.searchResults, name)
		}
	}
}

// SearchResult returns the unit search result at a given index.
func (l *UnitModel) SearchResult(index int) (name string, ok bool) {
	if index < 0 || index >= len(l.derived.searchResults) {
		return "", false
	}
	return l.derived.searchResults[index], true
}

// SearchResults returns a List of all the unit names matching the given search
// term.
func (m *UnitModel) SearchResults() List { return List{(*unitSearchResults)(m)} }

type unitSearchResults UnitModel

func (l *unitSearchResults) Selected() int          { return l.d.UnitSearchIndex }
func (l *unitSearchResults) Selected2() int         { return l.d.UnitSearchIndex }
func (l *unitSearchResults) SetSelected(value int)  { l.d.UnitSearchIndex = value }
func (l *unitSearchResults) SetSelected2(value int) {}
func (l *unitSearchResults) Count() (count int)     { return len(l.derived.searchResults) }

// Comment returns a String representing the comment string of the current unit.
func (m *UnitModel) Comment() String { return MakeString((*unitComment)(m)) }

type unitComment UnitModel

func (v *unitComment) Value() string {
	if u := (*Model)(v).selectedUnit(); u != nil {
		return u.Comment
	}
	return ""
}
func (v *unitComment) SetValue(value string) bool {
	u := (*Model)(v).selectedUnit()
	if u == nil {
		return false
	}
	defer (*Model)(v).change("UnitComment", PatchChange, MinorChange)()
	u.Comment = value
	return true
}

// Disabled returns a Bool controlling whether the currently selected unit(s)
// are disabled.
func (m *UnitModel) Disabled() Bool { return MakeBool((*unitDisabled)(m)) }

type unitDisabled UnitModel

func (m *unitDisabled) Value() bool {
	u := (*Model)(m).selectedUnit()
	return u != nil && u.Disabled
}
func (m *unitDisabled) SetValue(val bool) {
	units := (*Model)(m).units()
	if units == nil {
		return
	}
	r := (*Model)(m).unitRange()
	defer (*Model)(m).change("UnitDisabledSet", PatchChange, MajorChange)()
	for i := r.Start; i < r.End && i < len(units); i++ {
		units[i].Disabled = val
	}
}
func (m *unitDisabled) Enabled() bool {
	return len((*Model)(m).units()) > 0
}

// Item returns information about the unit on the given row of the unit
// editor: a root unit, or an inner unit of a module unit.
func (v *UnitModel) Item(row int) UnitListItem {
	m := (*Model)(v)
	u, r, ok := m.rowUnit(row)
	if !ok {
		return UnitListItem{}
	}
	c := m.rows()
	ret := UnitListItem{
		Title:      m.unitTitle(u),
		Type:       u.Type,
		Comment:    u.Comment,
		Disabled:   u.Disabled,
		Inner:      r.depth > 0,
		Depth:      r.depth,
		First:      r.depth > 0 && r.index == 0,
		Last:       r.depth > 0 && (row+1 >= len(c.rows) || c.rows[row+1].depth < r.depth),
		Selectable: r.list == c.scope,
		Outer:      r.outer,
		Stack:      r.signals.StackAfter(),
		Signals:    r.signals,
	}
	if scope := c.lists[c.scope]; scope.parent >= 0 && r.depth > 0 {
		ret.Edited = r.module == scope.module // also under other module units using the module
	}
	if mod, ok := v.d.Song.Modules.Find(u.Parameters["module"]); ok && u.Type == "module" {
		ret.Module, ret.Unfolded = true, u.Unfolded
		if row+1 < len(c.rows) && c.rows[row+1].parent == row {
			// unfolded, its inner units show what it does with the signals:
			// its inputs pass on to them, and they leave its outputs
			ret.Signals = Rail{PassThrough: r.signals.PassThrough + max(v.d.Song.Modules[mod].Inputs, 0)}
		}
	}
	return ret
}

// UnitListItem is a unit in the unit list. Title is its type, or for a
// module unit, the name of its module. Inner is true for an inner unit: a
// unit of the module of the module unit above it, and Depth tells how many
// module units it is inside.
//
// Module is true for a module unit with a module, which can be unfolded, and
// Unfolded if it is: its signals then pass on to its inner units, which
// show what it does with them. First and Last are true for the first inner
// unit of a module unit and for the last row under one.
//
// Selectable is true for the units being edited, the ones in the list of
// units that the cursor is in: a selection is among them. Edited is true
// for the inner units showing the module that the cursor is in, also under
// other module units: they all change with it.
type UnitListItem struct {
	Type, Title, Comment string
	Disabled             bool
	Inner, First, Last   bool
	Depth                int
	Module, Unfolded     bool
	Selectable, Edited   bool
	Signals              Rail
	Stack                int // the number of signals on the stack after the unit
	Outer                int // for an inner unit, the signals passing by the outermost module unit it is inside
}

// Type returns the type of the currently selected unit.
func (m *UnitModel) Type() string {
	if u := (*Model)(m).selectedUnit(); u != nil {
		return u.Type
	}
	return ""
}

// SetType sets the type of the currently selected unit.
func (m *UnitModel) SetType(t string) {
	list := (*Model)(m).unitsPtr()
	if list == nil {
		return
	}
	if m.d.UnitIndex < 0 {
		m.d.UnitIndex = 0
	}
	for len(*list) <= m.d.UnitIndex {
		*list = append(*list, sointu.Unit{})
	}
	unit := sointu.MakeUnit(t)
	oldUnit := (*list)[m.d.UnitIndex]
	if oldUnit.Type == unit.Type {
		return
	}
	if t == "module" {
		unit = (*Model)(m).newModuleUnit()
	}
	defer (*unitList)(m).Change("SetSelectedType", MajorChange)()
	// a new spectral unit reads the spectrum written last before it; units
	// writing a spectrum get a new one when the change is done
	for j, name := range sointu.SpectrumBufferParams(unit.Type) {
		if j > 0 || !sointu.WritesSpectrum(unit.Type) {
			unit.Parameters[name] = (*Model)(m).defaultSpectrumBuffer(*list, m.d.UnitIndex)
		}
	}
	// a new mc unit uses the bus of the mc unit before it; mcspread gets a
	// new one when the change is done
	for _, name := range sointu.BusParams(unit.Type) {
		if !sointu.WritesBus(unit.Type) {
			unit.Parameters[name] = (*Model)(m).defaultBus(*list, m.d.UnitIndex)
		}
	}
	(*list)[m.d.UnitIndex] = unit
	(*list)[m.d.UnitIndex].ID = oldUnit.ID // keep the ID of the replaced unit
}

// List returns a List of the rows of the unit editor: the units of the
// selected instrument, or of the selected module on the Modules tab, and
// with Unfold, the inner units of the module units among them. Its elements
// are the units being edited: see rows.go.
func (m *UnitModel) List() List { return List{(*unitRows)(m)} }

type unitList UnitModel

func (v *unitList) Selected() int          { return v.d.UnitIndex }
func (v *unitList) Selected2() int         { return v.d.UnitIndex2 }
func (v *unitList) SetSelected2(value int) { v.d.UnitIndex2 = value }
func (m *unitList) SetSelected(value int) {
	m.d.UnitIndex = value
	m.d.ParamIndex = 0
	m.d.UnitSearching = false
	m.d.UnitSearchString = ""
}
func (v *unitList) Count() int { return len((*Model)(v).units()) }

func (v *unitList) Move(r Range, delta int) (ok bool) {
	m := (*Model)(v)
	units := m.units()
	if units == nil {
		return false
	}
	for i, j := range r.Swaps(delta) {
		units[i], units[j] = units[j], units[i]
	}
	return true
}

func (v *unitList) Delete(r Range) (ok bool) {
	m := (*Model)(v)
	list := m.unitsPtr()
	if list == nil {
		return false
	}
	if _, _, depth := m.scope(); depth > 0 && r.Len() >= len(*list) {
		return false // an unfolded module unit keeps an inner unit: see deleteUnit
	}
	*list = append((*list)[:r.Start], (*list)[r.End:]...)
	return true
}

func (v *unitList) Change(n string, severity ChangeSeverity) func() {
	return (*Model)(v).change("UnitListView."+n, PatchChange, severity)
}

func (v *unitList) Cancel() {
	(*Model)(v).changeCancel = true
}

func (v *unitList) Marshal(r Range) ([]byte, error) {
	m := (*Model)(v)
	if m.unitsPtr() == nil {
		return nil, errors.New("UnitListView.marshal: no instruments")
	}
	units := m.units()[r.Start:r.End]
	if _, ok := m.scopeModule(); !ok {
		units = withoutBindings(units)
	}
	ret, err := yaml.Marshal(unitClipboard{Units: units, Modules: m.modulesUsedBy(units)})
	if err != nil {
		return nil, fmt.Errorf("UnitListView.marshal: %v", err)
	}
	return ret, nil
}

func (v *unitList) Unmarshal(data []byte) (r Range, err error) {
	m := (*Model)(v)
	list := m.unitsPtr()
	if list == nil {
		return Range{}, errors.New("UnitListView.unmarshal: no instruments")
	}
	var pastedUnits unitClipboard
	if err := yaml.Unmarshal(data, &pastedUnits); err != nil {
		return Range{}, fmt.Errorf("UnitListView.unmarshal: %v", err)
	}
	if len(pastedUnits.Units) == 0 {
		return Range{}, errors.New("UnitListView.unmarshal: no units")
	}
	m.importModules(pastedUnits.Modules, pastedUnits.Units)
	list = m.unitsPtr() // importing modules may have moved them
	for i := range pastedUnits.Units {
		if u := &pastedUnits.Units[i]; u.Type == "module" && m.wouldUseItself(u.Parameters["module"]) {
			u.Parameters["module"] = 0
			m.Alerts().Add("A module cannot use itself", Warning)
		}
	}
	m.assignUnitIDs(pastedUnits.Units)
	sel := v.Selected()
	var ok bool
	*list, ok = Insert(*list, sel, pastedUnits.Units...)
	if !ok {
		return Range{}, errors.New("UnitListView.unmarshal: insert failed")
	}
	return Range{sel, sel + len(pastedUnits.Units)}, nil
}

// RailError returns the first error of the signal rails. Its UnitIndex is
// the row of the unit in the unit editor, or -1 if it is not shown there.
func (s *UnitModel) RailError() RailError {
	ret := s.derived.railError
	if ret.Err == nil || ret.UnitIndex < 0 {
		return ret
	}
	module := ret.Module - 1
	if module < 0 && ret.InstrIndex != s.d.InstrIndex {
		ret.UnitIndex = -1
		return ret
	}
	c := (*Model)(s).rows()
	for _, l := range c.lists {
		if l.module == module && ret.UnitIndex < len(l.rows) {
			ret.UnitIndex = l.rows[ret.UnitIndex]
			return ret
		}
	}
	ret.UnitIndex = -1
	return ret
}

func (s *UnitModel) RailWidth() int { return (*Model)(s).rows().railWidth }

// playedUnit returns the unit that the synth runs for the unit on a row of
// the unit editor, or nil if it runs none. A unit of a module is in the
// synth once for every module unit using the module, with other IDs: for an
// inner unit, it is the copy made for the module unit above it, and for a
// unit of the module on the Modules tab, the first copy in the selected
// instrument, or else the first one in the song. If a module unit uses a
// module more than once, through other modules, it is the first copy.
func (m *Model) playedUnit(row int) *sointu.Unit {
	u, r, ok := m.rowUnit(row)
	if !ok || u.ID == 0 || u.Disabled {
		return nil
	}
	if r.module < 0 {
		return u
	}
	id, instr := m.playedCopy(row)
	if id == 0 || instr >= len(m.expanded) {
		return nil
	}
	units := m.expanded[instr].Units
	for i := range units {
		if units[i].ID == id {
			return &units[i]
		}
	}
	return nil
}

// playedID returns the ID that the unit on a row of the unit editor has in
// the patch that the synth plays: its own, or for a unit of a module, that
// of the copy that playedUnit returns. It is 0 if the unit is not played.
func (m *Model) playedID(row int) int {
	u, r, ok := m.rowUnit(row)
	if !ok || u.ID == 0 || u.Disabled {
		return 0
	}
	if r.module < 0 {
		return u.ID
	}
	id, _ := m.playedCopy(row)
	return id
}

// playedCopy returns the ID of the copy of a unit of a module that is
// played for the row, and its instrument, or 0: see playedUnit.
func (m *Model) playedCopy(row int) (id, instr int) {
	u, r, ok := m.rowUnit(row)
	if !ok || m.expansion == nil || r.module < 0 {
		return 0, 0
	}
	module := m.d.Song.Modules[r.module].ID
	best, bestInstr, bestHere := 0, 0, false
	for id, e := range m.expansion.Units {
		if e.Body != u.ID || e.Module != module {
			continue
		}
		here := e.Instrument == m.d.InstrIndex
		if r.call != 0 && (!here || e.Call != r.call) {
			continue
		}
		if best == 0 || here && !bestHere || here == bestHere && id < best {
			best, bestInstr, bestHere = id, e.Instrument, here
		}
	}
	return best, bestInstr
}

// selectedAsPlayed returns the selected unit as it is played: for an inner
// unit of a module unit, the bound parameters have the values that the
// module unit gives them.
func (m *Model) selectedAsPlayed() *sointu.Unit {
	if _, _, depth := m.scope(); depth > 0 {
		if u := m.playedUnit(m.rowOfUnit(m.d.UnitIndex)); u != nil {
			return u
		}
	}
	return m.selectedUnit()
}

func (e *RailError) Error() string { return e.Err.Error() }

func (s *Rail) StackAfter() int { return s.PassThrough + s.StackUse.NumOutputs }

// Spectrum returns the spectrum as the unit on row i of the unit editor, a
// spectral unit, last left it, as BufferModel.SpectrumOf.
func (m *UnitModel) Spectrum(i int) ([]float32, int) {
	u := (*Model)(m).playedUnit(i)
	if u == nil {
		return nil, 0
	}
	return (*Model)(m).spectrumOf(SpectrumSource{Unit: u.ID})
}

// previewUnit returns the unit whose buffer or bus the preview of row i
// shows: the unit that the synth runs for the row, as each module unit has
// buffers of its own and gives the bound ones, or else the unit on the row.
func (m *Model) previewUnit(i int) (*sointu.Unit, bool) {
	if u := m.playedUnit(i); u != nil {
		return u, true
	}
	u, _, ok := m.rowUnit(i)
	return u, ok
}

// Bus returns the ID of the bus of unit i of the selected instrument, if it
// is an mc unit, whose preview shows the levels of its bus.
func (m *UnitModel) Bus(i int) (id int, ok bool) {
	u, ok := (*Model)(m).previewUnit(i)
	if !ok {
		return 0, false
	}
	if sointu.BusParams(u.Type) == nil {
		return 0, false
	}
	return u.Parameters["bus"], true
}

// Buffer returns the ID of the buffer that unit i of the selected instrument
// plays, writes or holds its spectrum in, and whether it is a spectrum.
func (m *UnitModel) Buffer(i int) (id int, spectrum, ok bool) {
	u, ok := (*Model)(m).previewUnit(i)
	if !ok {
		return 0, false, false
	}
	return unitBuffer(u)
}

// UnitPreviewCells is how many parameter cells wide the preview of a unit's
// buffer is: the rack is that much wider, so that scrolled to the right, the
// previews at its right edge cover no parameters.
const UnitPreviewCells = 2

func unitBuffer(u *sointu.Unit) (id int, spectrum, ok bool) {
	for _, p := range sointu.UnitTypes[u.Type].Params {
		if p.Name == "buffer" {
			return u.Parameters["buffer"], sointu.SpectrumBufferParams(u.Type) != nil, true
		}
	}
	return 0, false, false
}
