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
	if m.editingModule() {
		if m.unitsPtr() == nil { // no modules, add one
			m.addModule()
		}
		if len(m.units()) > 0 && !a.Before {
			m.d.UnitIndex++
		}
	} else if len(m.d.Song.Patch) == 0 { // no instruments, add one
		instr := sointu.Instrument{NumVoices: 1}
		instr.Units = make([]sointu.Unit, 0, 1)
		m.d.Song.Patch = append(m.d.Song.Patch, instr)
		m.d.UnitIndex = 0
	} else {
		if !a.Before {
			m.d.UnitIndex++
		}
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
	// an instrument keeps at least one unit; a module can be emptied
	n := len((*Model)(m).units())
	return n > 1 || n == 1 && (*Model)(m).editingModule()
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
// editor: a unit being edited, or an inner unit of a module unit.
func (v *UnitModel) Item(row int) UnitListItem {
	units := (*Model)(v).units()
	index, e, i, ok := (*Model)(v).rowAt(row)
	if !ok || index >= len(units) {
		return UnitListItem{}
	}
	signals := Rail{}
	if d := (*Model)(v).derivedUnits(); d != nil && index < len(d.rails) {
		signals = d.rails[index]
	}
	if e != nil {
		// its signals are on top of those passing the module unit
		u := &e.units[i]
		return UnitListItem{
			Title:    (*Model)(v).unitTitle(u),
			Type:     u.Type,
			Comment:  u.Comment,
			Disabled: u.Disabled,
			Inner:    true,
			Stack:    signals.PassThrough + e.before[i] + e.uses[i].NumOutputs,
			First:    i == 0,
			Last:     i == len(e.units)-1,
			Signals:  Rail{PassThrough: signals.PassThrough + e.before[i], StackUse: e.uses[i], Send: !u.Disabled && u.Type == "send"},
		}
	}
	unit := units[index]
	mod, isModule := v.d.Song.Modules.Find(unit.Parameters["module"])
	isModule = isModule && unit.Type == "module"
	stack := signals.StackAfter()
	if isModule && (*Model)(v).innerUnitsOf(&units[index]) != nil {
		// unfolded, its inner units show what it does with the signals:
		// its inputs pass on to them, and they leave its outputs
		signals = Rail{PassThrough: signals.PassThrough + max(v.d.Song.Modules[mod].Inputs, 0)}
	}
	return UnitListItem{
		Stack:    stack,
		Module:   isModule,
		Unfolded: isModule && v.unfolded[unit.ID],
		Title:    (*Model)(v).unitTitle(&unit),
		Type:     unit.Type,
		Comment:  unit.Comment,
		Disabled: unit.Disabled,
		Signals:  signals,
	}
}

// UnitListItem is a unit in the unit list. Title is its type, or for a
// module unit, the name of its module. Inner is true for a unit that the
// module unit above it stands for, which cannot be changed.
//
// Module is true for a module unit with a module, which can be unfolded, and
// Unfolded if it is: its signals then pass on to its inner units, which
// show what it does with them. First and Last are true for the first and the last of
// the inner units of a module unit.
type UnitListItem struct {
	Type, Title, Comment string
	Disabled             bool
	Inner, First, Last   bool
	Module, Unfolded     bool
	Signals              Rail
	Stack                int // the number of signals on the stack after the unit
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
// with Unfold, the inner units of the module units among them, which cannot be
// selected. It implements the ListData & MutableListData interfaces.
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
	if !m.editingModule() {
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
	m.assignUnitIDs(pastedUnits.Units)
	sel := v.Selected()
	var ok bool
	*list, ok = Insert(*list, sel, pastedUnits.Units...)
	if !ok {
		return Range{}, errors.New("UnitListView.unmarshal: insert failed")
	}
	return Range{sel, sel + len(pastedUnits.Units)}, nil
}

// RailError returns the first error of the signal rails. Its UnitIndex is -1
// unless the unit is one of the units being edited.
func (s *UnitModel) RailError() RailError {
	ret := s.derived.railError
	if m := (*Model)(s); ret.Err != nil && (m.editingModule() != (ret.Module > 0) ||
		m.editingModule() && ret.Module-1 != m.d.ModuleIndex || !m.editingModule() && ret.InstrIndex != m.d.InstrIndex) {
		ret.UnitIndex = -1
	} else if ret.UnitIndex >= 0 {
		ret.UnitIndex = (*Model)(s).rowOfUnit(ret.UnitIndex) // the unit editor shows rows
	}
	return ret
}

func (s *UnitModel) RailWidth() int {
	m := (*Model)(s)
	d := m.derivedUnits()
	if d == nil {
		return 0
	}
	width := d.railWidth
	if m.unfold() {
		units := m.units()
		for i := range units {
			if e := m.innerUnitsOf(&units[i]); e != nil && i < len(d.rails) {
				width = max(width, d.rails[i].PassThrough+e.width)
			}
		}
	}
	return width
}

// rowUnit returns the unit on a row of the unit editor: a unit being
// edited, or an inner unit. played is the ID that the unit has in the synth, or
// 0 if it is not in the synth.
func (m *Model) rowUnit(row int) (u *sointu.Unit, played int, ok bool) {
	units := m.units()
	index, e, i, ok := m.rowAt(row)
	if !ok || index >= len(units) {
		return nil, 0, false
	}
	if e != nil {
		if e.source[i].Body != 0 { // a unit of the synth
			played = e.units[i].ID
		}
		return &e.units[i], played, true
	}
	return &units[index], m.playedUnitID(index), true
}

func (e *RailError) Error() string { return e.Err.Error() }

func (s *Rail) StackAfter() int { return s.PassThrough + s.StackUse.NumOutputs }

// Spectrum returns the spectrum as the unit on row i of the unit editor, a
// spectral unit, last left it, as BufferModel.SpectrumOf.
func (m *UnitModel) Spectrum(i int) ([]float32, int) {
	_, id, _ := (*Model)(m).rowUnit(i)
	if id == 0 {
		return nil, 0
	}
	return (*Model)(m).spectrumOf(SpectrumSource{Unit: id})
}

// Bus returns the ID of the bus of unit i of the selected instrument, if it
// is an mc unit, whose preview shows the levels of its bus.
func (m *UnitModel) Bus(i int) (id int, ok bool) {
	u, _, ok := (*Model)(m).rowUnit(i)
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
	u, _, ok := (*Model)(m).rowUnit(i)
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
