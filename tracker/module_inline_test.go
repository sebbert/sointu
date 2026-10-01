package tracker

import (
	"strings"
	"testing"
)

// newInlineTestModel returns the test model with its module used by two
// module units, the first one unfolded: the rows are oscillator, send,
// module, envelope, oscillator, mulp, filter, module, out.
func newInlineTestModel(t *testing.T) (*Model, *Broker) {
	t.Helper()
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	units := m.Unit().List()
	units.SetSelected(2)
	units.SetSelected2(2)
	data, ok := units.CopyElements()
	if !ok || !units.PasteElements(data) {
		t.Fatal("the module unit could not be copied")
	}
	units.SetSelected(2)
	units.SetSelected2(2)
	m.Unit().Unfold().SetValue(true)
	if got := rowTitles(m); got != "oscillator send Module envelope oscillator mulp filter Module out" {
		t.Fatalf("the rows are: %v", got)
	}
	drainPlayer(broker)
	return m, broker
}

func rowTitles(m *Model) string {
	var titles []string
	for i := range m.Unit().List().Count() {
		titles = append(titles, m.Unit().Item(i).Title)
	}
	return strings.Join(titles, " ")
}

// selectRow puts the cursor of the unit list on a row.
func selectRow(m *Model, row int) {
	m.Unit().List().SetSelected(row)
	m.Unit().List().SetSelected2(m.Unit().List().Selected())
}

func TestInnerParameters(t *testing.T) {
	m, broker := newInlineTestModel(t)
	selectRow(m, 6)
	if name, uses, ok := m.Unit().InModule(); !ok || name != "Module" || uses != 2 || m.d.UnitIndex != 3 {
		t.Fatalf("on the filter of the module: %q, %v uses, %v, unit %v", name, uses, ok, m.d.UnitIndex)
	}
	if item := m.Unit().Item(6); !item.Inner || !item.Selectable || !item.Edited || m.Unit().Item(7).Selectable || m.Unit().Item(2).Edited {
		t.Errorf("the row of the filter: %+v", item)
	}
	// a parameter that is not bound: the module changes, and both module
	// units with it
	frequency := paramNamed(t, m, 6, "frequency")
	if !frequency.SetValue(77) || m.d.Song.Modules[0].Units[3].Parameters["frequency"] != 77 {
		t.Fatalf("the frequency of the module is %v", m.d.Song.Modules[0].Units[3].Parameters["frequency"])
	}
	patch := playerPatch(t, broker)
	if got := unitTypes(patch[0].Units); got != "oscillator send envelope oscillator mulp filter envelope oscillator mulp filter out" {
		t.Fatalf("the player got: %v", got)
	}
	if a, b := patch[0].Units[5].Parameters["frequency"], patch[0].Units[9].Parameters["frequency"]; a != 77 || b != 77 {
		t.Errorf("the player got the frequencies %v and %v", a, b)
	}
	m.History().Undo().Do()
	if got := m.d.Song.Modules[0].Units[3].Parameters["frequency"]; got != 40 || m.d.UnitIndex != 3 || len(m.d.UnitPath) != 1 {
		t.Errorf("after undo: frequency %v, unit %v inside %v", got, m.d.UnitIndex, m.d.UnitPath)
	}
	m.History().Redo().Do()
	if got := m.d.Song.Modules[0].Units[3].Parameters["frequency"]; got != 77 {
		t.Errorf("after redo: frequency %v", got)
	}
	// by keyboard, with the cursor on it
	for x := 0; x < m.Params().RowWidth(6); x++ {
		if p := m.Params().Item(Point{x, 6}); p.Name() == "resonance" {
			m.Params().SetCursor(Point{x, 6})
			m.Params().SetCursor2(Point{x, 6})
		}
	}
	before := m.d.Song.Modules[0].Units[3].Parameters["resonance"]
	m.Params().Table().Add(1, false)
	if got := m.d.Song.Modules[0].Units[3].Parameters["resonance"]; got != before+1 {
		t.Errorf("after adding one the resonance is %v, was %v", got, before)
	}
}

func TestInnerBoundParameters(t *testing.T) {
	m, broker := newInlineTestModel(t)
	m.Unit().OpenModule().Do()
	m.Module().AddParam().Do()
	m.Module().ParamName(1).SetValue("tune")
	bindParam(t, m, 1, "detune", 1) // the oscillator, at 70
	m.Module().AddParam().Do()
	bindParam(t, m, 3, "frequency", 2) // the filter, at 40: from 30 to 94
	m.Module().BindingAt(2, false).SetValue(30)
	m.Module().BindingAt(2, true).SetValue(94)
	if d := m.d.Song.Modules[0].Params[1].Default; d != 20 {
		t.Fatalf("the default of the scaled parameter is %v, want 20", d)
	}
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	if got := rowTitles(m); got != "oscillator send Module envelope oscillator mulp filter Module out" {
		t.Fatalf("the rows are: %v", got)
	}
	drainPlayer(broker)
	first, second := &m.d.Song.Patch[0].Units[2], &m.d.Song.Patch[0].Units[3]
	// the inner row shows what the module unit gives, and sets it
	detune := paramNamed(t, m, 4, "detune")
	if name, ok := detune.Bound(); !ok || name != "tune" || detune.Value() != 70 || !strings.HasPrefix(detune.Hint().Label, "← tune") {
		t.Fatalf("the bound detune: %v, bound %v to %q, hint %q", detune.Value(), ok, name, detune.Hint().Label)
	}
	if !detune.SetValue(5) || first.Parameters["p1"] != 5 || second.Parameters["p1"] == 5 {
		t.Fatalf("the module units have %v and %v", first.Parameters["p1"], second.Parameters["p1"])
	}
	mod := &m.d.Song.Modules[0]
	if mod.Params[0].Default != 70 || mod.Units[1].Parameters["detune"] != 70 {
		t.Errorf("the module changed: default %v, detune %v", mod.Params[0].Default, mod.Units[1].Parameters["detune"])
	}
	patch := playerPatch(t, broker)
	if a, b := patch[0].Units[3].Parameters["detune"], patch[0].Units[7].Parameters["detune"]; a != 5 || b != 70 {
		t.Errorf("the player got the detunes %v and %v, want 5 and 70", a, b)
	}
	if got := paramNamed(t, m, 4, "detune"); got.Value() != 5 {
		t.Errorf("the inner row shows %v", got.Value())
	}
	if arg := m.Params().Item(Point{1, 2}); arg.Value() != 5 {
		t.Errorf("the module unit shows %v", arg.Value())
	}
	m.History().Undo().Do()
	if got := paramNamed(t, m, 4, "detune"); got.Value() != 70 {
		t.Errorf("after undo the inner row shows %v", got.Value())
	}
	// resetting it gives the default of the module
	m.History().Redo().Do()
	detune = paramNamed(t, m, 4, "detune")
	detune.Reset()
	if got := m.d.Song.Patch[0].Units[2].Parameters["p1"]; got != 70 {
		t.Errorf("after a reset the module unit has %v", got)
	}

	// through a scaled binding: the nearest value there is
	frequency := paramNamed(t, m, 6, "frequency")
	if frequency.Value() != 40 {
		t.Fatalf("the frequency is %v", frequency.Value())
	}
	if !frequency.SetValue(62) || m.d.Song.Patch[0].Units[2].Parameters["p2"] != 64 {
		t.Errorf("the module unit has %v, want 64", m.d.Song.Patch[0].Units[2].Parameters["p2"])
	}
	if b := m.d.Song.Modules[0].Units[3].Bind["frequency"]; !b.Scaled || b.Min != 30 || b.Max != 94 || m.d.Song.Modules[0].Params[1].Default != 20 {
		t.Errorf("the module changed: %+v, default %v", b, m.d.Song.Modules[0].Params[1].Default)
	}
	if got := playerPatch(t, broker)[0].Units[5].Parameters["frequency"]; got != 62 {
		t.Errorf("the player got the frequency %v, want 62", got)
	}
	// a step is a step of the parameter of the module
	frequency = paramNamed(t, m, 6, "frequency")
	if !frequency.Add(1, false) || m.d.Song.Patch[0].Units[2].Parameters["p2"] != 65 {
		t.Errorf("after a step the module unit has %v, want 65", m.d.Song.Patch[0].Units[2].Parameters["p2"])
	}
	frequency = paramNamed(t, m, 6, "frequency")
	if !frequency.SetValue(128) || m.d.Song.Patch[0].Units[2].Parameters["p2"] != 128 {
		t.Errorf("at the top the module unit has %v", m.d.Song.Patch[0].Units[2].Parameters["p2"])
	}
	if frequency = paramNamed(t, m, 6, "frequency"); frequency.Value() != 94 {
		t.Errorf("at the top the frequency is %v, want 94", frequency.Value())
	}
}

func TestInnerUnitsStructure(t *testing.T) {
	m, broker := newInlineTestModel(t)
	units := m.Unit().List()
	call := m.d.Song.Patch[0].Units[2].ID
	types := func() string { return unitTypes(m.d.Song.Modules[0].Units) }
	instr := func() string { return unitTypes(m.d.Song.Patch[0].Units) }
	selectRow(m, 4) // the oscillator
	// add a unit after it, in the module
	m.Unit().Add(false).Do()
	m.Unit().SetType("gain")
	if types() != "envelope oscillator gain mulp filter" || instr() != "oscillator send module module out" || units.Selected() != 5 {
		t.Fatalf("after adding a unit: %v in %v, row %v", types(), instr(), units.Selected())
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator gain mulp filter envelope oscillator gain mulp filter out" {
		t.Errorf("the player got: %v", got)
	}
	// comment, disable
	m.Unit().Comment().SetValue("quieter")
	m.Unit().Disabled().SetValue(true)
	if u := m.d.Song.Modules[0].Units[2]; u.Comment != "quieter" || !u.Disabled || m.Unit().Item(5).Comment != "quieter" || !m.Unit().Item(5).Disabled {
		t.Errorf("the gain: %+v", u)
	}
	m.Unit().Disabled().SetValue(false)
	// move it up, within the module
	if !units.MoveElements(-1) || types() != "envelope gain oscillator mulp filter" || units.Selected() != 4 {
		t.Errorf("after moving it up: %v, row %v", types(), units.Selected())
	}
	units.MoveElements(-1)
	if units.MoveElements(-1) || types() != "gain envelope oscillator mulp filter" || instr() != "oscillator send module module out" {
		t.Errorf("after moving it to the top and further: %v in %v", types(), instr())
	}
	// copy and paste, within the module
	data, ok := units.CopyElements()
	if !ok || !units.PasteElements(data) || types() != "gain gain envelope oscillator mulp filter" {
		t.Errorf("after pasting: %v", types())
	}
	// delete: the unit of the module, never the module unit
	m.Unit().Delete().Do()
	units.DeleteElements(false)
	if types() != "envelope oscillator mulp filter" || instr() != "oscillator send module module out" {
		t.Fatalf("after deleting: %v in %v", types(), instr())
	}
	if len(m.d.UnitPath) != 1 || m.d.UnitPath[0] != call {
		t.Errorf("the cursor is inside %v", m.d.UnitPath)
	}
	// a selection stays among the inner units
	selectRow(m, 4)
	units.SetSelected2(0)
	if units.Selected2() != 3 {
		t.Errorf("selecting up to the first row ends on row %v", units.Selected2())
	}
	units.SetSelected2(8)
	if units.Selected2() != 6 {
		t.Errorf("selecting down to the last row ends on row %v", units.Selected2())
	}
	// the last inner unit cannot be deleted: the cursor would be on the
	// module unit, and the next delete would delete that
	selectRow(m, 3)
	units.SetSelected2(6)
	if units.DeleteElements(false) || types() != "envelope oscillator mulp filter" {
		t.Errorf("all the inner units could be deleted: %v", types())
	}
	units.SetSelected2(5)
	if !units.DeleteElements(false) || types() != "filter" || instr() != "oscillator send module module out" {
		t.Fatalf("after deleting all but one: %v in %v", types(), instr())
	}
	if m.Unit().Delete().Enabled() {
		t.Errorf("the last inner unit can be deleted")
	}
	m.Unit().Delete().Do()
	if types() != "filter" || instr() != "oscillator send module module out" {
		t.Errorf("after deleting the last one: %v in %v", types(), instr())
	}
	m.History().Undo().Do()
	// making a module of inner units: a module in the module
	selectRow(m, 4)
	units.SetSelected2(5)
	if types() != "envelope oscillator mulp filter" || units.Selected2() != 5 {
		t.Fatalf("before making a module: %v", types())
	}
	m.Unit().MakeModule().Do()
	if types() != "envelope module filter" || len(m.d.Song.Modules) != 2 || instr() != "oscillator send module module out" {
		t.Fatalf("after making a module: %v, %v modules", types(), len(m.d.Song.Modules))
	}
	// which can be unfolded in turn, and replaced by its units again
	m.Unit().Unfold().SetValue(true)
	if got := rowTitles(m); got != "oscillator send Module envelope Module 2 oscillator mulp filter Module out" {
		t.Fatalf("the rows are: %v", got)
	}
	if item := m.Unit().Item(5); item.Depth != 2 || m.Unit().Item(4).Depth != 1 || !m.Unit().Item(6).Last || m.Unit().Item(7).Depth != 1 {
		t.Errorf("the row of the inner oscillator: %+v", item)
	}
	selectRow(m, 5)
	if name, _, ok := m.Unit().InModule(); !ok || name != "Module 2" || len(m.d.UnitPath) != 2 {
		t.Errorf("on the inner oscillator: in %q, inside %v", name, m.d.UnitPath)
	}
	// a module cannot use itself: not the one that the cursor is in
	if m.Unit().SetType("module"); m.d.Song.Modules[1].Units[0].Parameters["module"] != 0 {
		t.Errorf("the new module unit of module 2 uses module %v", m.d.Song.Modules[1].Units[0].Parameters["module"])
	}
	m.History().Undo().Do()
	// folding the module unit that the cursor is inside puts it on that
	m.Unit().Unfold().SetValue(false)
	if len(m.d.UnitPath) != 1 || m.d.UnitIndex != 1 || units.Selected() != 4 {
		t.Errorf("after folding: unit %v inside %v, row %v", m.d.UnitIndex, m.d.UnitPath, units.Selected())
	}
	m.Unit().InlineModule().Do()
	if types() != "envelope oscillator mulp filter" {
		t.Errorf("after replacing it: %v", types())
	}
	selectRow(m, 5)
	m.Unit().ToggleUnfold(2).Do()
	if len(m.d.UnitPath) != 0 || m.d.UnitIndex != 2 || units.Count() != 5 {
		t.Errorf("after folding the module unit: unit %v inside %v, %v rows", m.d.UnitIndex, m.d.UnitPath, units.Count())
	}
	// extending a selection from a module unit goes past its inner units
	m.Unit().ToggleUnfold(2).Do()
	selectRow(m, 2)
	if !units.ExtendSelection(1) || m.d.UnitIndex != 3 || m.d.UnitIndex2 != 2 || units.Selected() != 7 {
		t.Errorf("after extending the selection: units %v to %v, row %v", m.d.UnitIndex2, m.d.UnitIndex, units.Selected())
	}
}

// TestInnerUnitsOfInnerUnits checks a parameter bound through two modules.
func TestInnerUnitsOfInnerUnits(t *testing.T) {
	m, broker := newInlineTestModel(t)
	// a module of the oscillator, in the module
	selectRow(m, 4)
	m.Unit().MakeModule().Do()
	m.Unit().Unfold().SetValue(true)
	if got := rowTitles(m); got != "oscillator send Module envelope Module 2 oscillator mulp filter Module out" {
		t.Fatalf("the rows are: %v", got)
	}
	// its detune is a parameter of module 2, and that one a parameter of
	// the module
	m.Unit().OpenModule().Do()
	if m.d.ModuleIndex != 1 {
		t.Fatalf("module %v is shown", m.d.ModuleIndex)
	}
	m.Module().AddParam().Do()
	bindParam(t, m, 0, "detune", 1)
	m.Module().List().SetSelected(0)
	m.Module().AddParam().Do()
	m.Module().ParamName(1).SetValue("tune")
	m.Params().SetCursor(Point{1, 1})
	if !m.Module().ParamBound(1).Enabled() {
		t.Fatal("the parameter of the module unit cannot be bound")
	}
	m.Module().ParamBound(1).SetValue(true)
	// on the Modules tab, the inner unit of the module unit shows the
	// default, and changes it, like the module unit does
	if got := rowTitles(m); got != "envelope Module 2 oscillator mulp filter" {
		t.Fatalf("the rows of the module are: %v", got)
	}
	detune := paramNamed(t, m, 2, "detune")
	if name, ok := detune.Bound(); !ok || detune.Value() != 70 || name != "p1" {
		t.Fatalf("the detune in the module: %v, bound %v to %q", detune.Value(), ok, name)
	}
	if !detune.SetValue(60) || m.d.Song.Modules[0].Params[0].Default != 60 || m.d.Song.Modules[0].Units[1].Parameters["p1"] != 60 {
		t.Errorf("the default is %v, the module unit has %v", m.d.Song.Modules[0].Params[0].Default, m.d.Song.Modules[0].Units[1].Parameters["p1"])
	}
	if d := m.d.Song.Modules[1].Params[0].Default; d != 70 {
		t.Errorf("the default of module 2 changed to %v", d)
	}
	// in the instrument, it shows what the module unit of the instrument
	// gives, and changes that
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	drainPlayer(broker)
	arg := m.Params().Item(Point{1, 2})
	if arg.Name() != "tune" || !arg.SetValue(33) {
		t.Fatalf("the parameter of the module unit: %q", arg.Name())
	}
	for _, row := range []int{4, 5} { // the module unit in the module, and the oscillator in module 2
		name := "detune"
		if row == 4 {
			name = "p1"
		}
		if p := paramNamed(t, m, row, name); p.Value() != 33 {
			t.Errorf("row %v shows %v, want 33", row, p.Value())
		}
	}
	detune = paramNamed(t, m, 5, "detune")
	if !detune.SetValue(44) || m.d.Song.Patch[0].Units[2].Parameters["p1"] != 44 {
		t.Errorf("the module unit of the instrument has %v, want 44", m.d.Song.Patch[0].Units[2].Parameters["p1"])
	}
	inner := paramNamed(t, m, 4, "p1")
	if !inner.SetValue(55) || m.d.Song.Patch[0].Units[2].Parameters["p1"] != 55 {
		t.Errorf("the module unit of the instrument has %v, want 55", m.d.Song.Patch[0].Units[2].Parameters["p1"])
	}
	if a, b := m.d.Song.Modules[0].Params[0].Default, m.d.Song.Modules[0].Units[1].Parameters["p1"]; a != 60 || b != 60 {
		t.Errorf("the module changed: default %v, module unit %v", a, b)
	}
	patch := playerPatch(t, broker)
	if got := unitTypes(patch[0].Units); got != "oscillator send envelope oscillator mulp filter envelope oscillator mulp filter out" {
		t.Fatalf("the player got: %v", got)
	}
	if a, b := patch[0].Units[3].Parameters["detune"], patch[0].Units[7].Parameters["detune"]; a != 55 || b != 60 {
		t.Errorf("the player got the detunes %v and %v, want 55 and 60", a, b)
	}
	// the previews of an inner unit are those of the unit played for it
	selectRow(m, 5)
	if u := m.playedUnit(5); u == nil || u.ID != patch[0].Units[3].ID || u.Parameters["detune"] != 55 {
		t.Errorf("the unit played for the inner oscillator: %+v, want %v", u, patch[0].Units[3].ID)
	}
}
