package tracker

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

// newModuleTestModel returns a model with one instrument: an LFO sent to
// nothing yet, an envelope, an oscillator, a filter and an out.
func newModuleTestModel(t *testing.T) (*Model, *Broker) {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	unit := func(id int, typ string, params sointu.ParamMap) sointu.Unit {
		u := sointu.MakeUnit(typ)
		u.ID = id
		for k, v := range params {
			u.Parameters[k] = v
		}
		return u
	}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{{Name: "lead", NumVoices: 1, Units: []sointu.Unit{
			unit(1, "oscillator", sointu.ParamMap{"lfo": 1}),
			unit(2, "send", sointu.ParamMap{"sendpop": 1}),
			unit(3, "envelope", nil),
			unit(4, "oscillator", sointu.ParamMap{"detune": 70}),
			unit(5, "mulp", nil),
			unit(6, "filter", sointu.ParamMap{"frequency": 40}),
			unit(7, "out", sointu.ParamMap{"stereo": 0}),
		}}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 1}}
	}()
	drainPlayer(broker)
	return m, broker
}

// playerPatch returns the patch that the model last sent to the player.
func playerPatch(t *testing.T, broker *Broker) sointu.Patch {
	t.Helper()
	var ret sointu.Patch
	for {
		select {
		case msg := <-broker.ToPlayer:
			switch p := msg.(type) {
			case sointu.Patch:
				ret = p
			case sointu.Song:
				ret = p.Patch
			}
		default:
			if ret == nil {
				t.Fatal("the model sent no patch to the player")
			}
			return ret
		}
	}
}

func unitTypes(units []sointu.Unit) string {
	var types []string
	for _, u := range units {
		types = append(types, u.Type)
	}
	return strings.Join(types, " ")
}

// makeTestModule makes a module of the envelope, oscillator, mulp and filter
// of the test model.
func makeTestModule(t *testing.T, m *Model) {
	t.Helper()
	units := m.Unit().List()
	units.SetSelected(2)
	units.SetSelected2(5)
	m.Unit().MakeModule().Do()
	if got := unitTypes(m.d.Song.Patch[0].Units); got != "oscillator send module out" {
		t.Fatalf("after making a module, the instrument is: %v", got)
	}
	if len(m.d.Song.Modules) != 1 || unitTypes(m.d.Song.Modules[0].Units) != "envelope oscillator mulp filter" {
		t.Fatalf("the modules are %+v", m.d.Song.Modules)
	}
}

func TestMakeModule(t *testing.T) {
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	if m.d.Song.Modules[0].Inputs != 0 || m.d.UnitIndex != 2 || m.Unit().Item(2).Title != "Module" {
		t.Errorf("inputs %v, selected unit %v, shown as %q", m.d.Song.Modules[0].Inputs, m.d.UnitIndex, m.Unit().Item(2).Title)
	}
	if n, err := m.d.Song.Modules.Outputs(0); n != 1 || err != nil {
		t.Errorf("outputs: %v, %v", n, err)
	}
	if err := m.Unit().RailError(); err.Err != nil {
		t.Errorf("rail error: %v", err.Err)
	}
	// the player gets the units of the module in place of the module unit
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter out" {
		t.Errorf("the player got: %v", got)
	}
	if n, limit := m.Unit().ExpandedUnits(); n != 7 || limit != vm.MAX_UNITS {
		t.Errorf("expanded units: %v of %v", n, limit)
	}
	// units taking signals from before them become inputs of the module
	m.History().Undo().Do()
	if len(m.d.Song.Modules) != 0 || len(m.d.Song.Patch[0].Units) != 7 {
		t.Fatalf("undo left %v modules and %v units", len(m.d.Song.Modules), len(m.d.Song.Patch[0].Units))
	}
	units := m.Unit().List()
	units.SetSelected(4)
	units.SetSelected2(5)
	m.Unit().MakeModule().Do()
	if in := m.d.Song.Modules[0].Inputs; in != 2 {
		t.Errorf("a module of mulp and filter has %v inputs, want 2", in)
	}
	if err := m.Unit().RailError(); err.Err != nil {
		t.Errorf("rail error: %v", err.Err)
	}
}

func TestModuleEditingAndBinding(t *testing.T) {
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	if !m.Module().Editing() || m.Unit().List().Count() != 4 {
		t.Fatalf("on the Modules tab: editing %v, %v units", m.Module().Editing(), m.Unit().List().Count())
	}
	// bind the frequency of the filter to a new parameter
	m.Module().AddParam().Do()
	m.Module().ParamName(1).SetValue("cutoff")
	bindParam(t, m, 3, "frequency", 1)
	mod := &m.d.Song.Modules[0]
	if mod.Units[3].Bind["frequency"].Param != 1 || mod.Params[0].Default != 40 {
		t.Fatalf("bindings %v, default %v", mod.Units[3].Bind, mod.Params[0].Default)
	}
	p, _ := m.Module().Param(1)
	if p.Name != "cutoff" || p.MaxValue != 128 || p.DisplayFunc == nil {
		t.Errorf("the parameter of the module: %+v", p)
	}
	// the module unit has the parameter, with the default; the player gets it
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	arg := m.Params().Item(Point{1, 2})
	if arg.Name() != "cutoff" || arg.Value() != 40 || arg.Range().Max != 128 {
		t.Fatalf("the parameter of the module unit: %v = %v", arg.Name(), arg.Value())
	}
	arg.SetValue(90)
	if got := playerPatch(t, broker)[0].Units[5].Parameters["frequency"]; got != 90 {
		t.Errorf("the player got the frequency %v, want 90", got)
	}
	// a send to the module unit modulates the filter
	m.Params().ChooseSendSource(2).Do()
	port, ok := arg.Port()
	if !ok || port != 0 {
		t.Fatalf("the port of the parameter is %v, %v", port, ok)
	}
	m.Params().ChooseSendTarget(arg.UnitID(), port).Do()
	patch := playerPatch(t, broker)
	if send, filter := patch[0].Units[1], patch[0].Units[5]; send.Parameters["target"] != filter.ID || filter.ID == 0 || send.Parameters["port"] != 0 {
		t.Errorf("the send goes to %v port %v, the filter is %v", send.Parameters["target"], send.Parameters["port"], filter.ID)
	}
	for _, a := range m.Alerts().Iterate {
		if a.Name == "Modules" {
			t.Errorf("alert: %v", a.Message)
		}
	}
	// editing a bound parameter in the module changes the default
	m.Unit().OpenModule().Do()
	bound := paramNamed(t, m, 3, "frequency")
	if name, ok := bound.Bound(); !ok || name != "cutoff" || !strings.HasPrefix(bound.Hint().Label, "← cutoff") {
		t.Errorf("the bound parameter: %v %v %q", name, ok, bound.Hint().Label)
	}
	bound.SetValue(55)
	if m.d.Song.Modules[0].Params[0].Default != 55 {
		t.Errorf("the default is %v after editing the bound parameter", m.d.Song.Modules[0].Params[0].Default)
	}
	// deleting the parameter removes the binding, and the send loses its target
	m.Module().DeleteParam(1).Do()
	if len(m.d.Song.Modules[0].Units[3].Bind) != 0 || m.d.Song.Patch[0].Units[1].Parameters["target"] != 0 {
		t.Errorf("after deleting the parameter: bindings %v, send target %v", m.d.Song.Modules[0].Units[3].Bind, m.d.Song.Patch[0].Units[1].Parameters["target"])
	}
}

func paramNamed(t *testing.T, m *Model, unit int, name string) Parameter {
	t.Helper()
	for x := 0; x < m.Params().RowWidth(unit); x++ {
		if p := m.Params().Item(Point{x, unit}); p.Name() == name {
			return p
		}
	}
	t.Fatalf("unit %v has no parameter %v", unit, name)
	return Parameter{}
}

// bindParam binds the named parameter of a unit of the module being edited
// to parameter k of the module.
func bindParam(t *testing.T, m *Model, unit int, name string, k int) {
	t.Helper()
	for x := m.Params().RowWidth(unit) - 1; x >= 0; x-- { // the last one: a delay time, not the port of the delay times
		if p := m.Params().Item(Point{x, unit}); p.Name() == name {
			m.Params().SetCursor(Point{x, unit})
			if !m.Module().ParamBound(k).Enabled() {
				t.Fatalf("cannot bind %v", name)
			}
			m.Module().ParamBound(k).SetValue(true)
			return
		}
	}
	t.Fatalf("unit %v has no parameter %v", unit, name)
}

func TestInlineAndUniqueModule(t *testing.T) {
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Module().AddParam().Do()
	bindParam(t, m, 1, "detune", 1)
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	arg := m.Params().Item(Point{1, 2})
	arg.SetValue(99)

	m.Unit().UniqueModule().Do()
	if len(m.d.Song.Modules) != 2 || m.d.Song.Patch[0].Units[2].Parameters["module"] != m.d.Song.Modules[1].ID || m.d.Song.Modules[1].Name == m.d.Song.Modules[0].Name {
		t.Fatalf("after making the module unique: %v modules", len(m.d.Song.Modules))
	}
	seen := map[int]bool{}
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if seen[u.ID] || u.ID == 0 {
				t.Errorf("the ID %v is not unique", u.ID)
			}
			seen[u.ID] = true
		}
	}
	m.Unit().List().SetSelected(2)
	m.Unit().InlineModule().Do()
	units := m.d.Song.Patch[0].Units
	if got := unitTypes(units); got != "oscillator send envelope oscillator mulp filter out" {
		t.Fatalf("after inlining: %v", got)
	}
	if units[3].Parameters["detune"] != 99 || units[3].Bind != nil {
		t.Errorf("the inlined oscillator has detune %v and the bindings %v", units[3].Parameters["detune"], units[3].Bind)
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != unitTypes(units) {
		t.Errorf("the player got: %v", got)
	}
}

func TestModulesInInstrumentFiles(t *testing.T) {
	m, _ := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Module().List().SetSelected(0)
	m.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	m.Module().Name().SetValue("voice")
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	var file bytes.Buffer
	if !m.Instrument().Write(nopWriteCloser{&file}) {
		t.Fatal("writing the instrument failed")
	}
	if !strings.Contains(file.String(), "modules:") {
		t.Fatalf("the instrument file has no modules:\n%s", file.String())
	}
	read := func(m *Model) {
		t.Helper()
		if !m.Instrument().Read(io.NopCloser(bytes.NewReader(file.Bytes()))) {
			t.Fatal("reading the instrument failed")
		}
	}
	// a song without the module gets it
	other, broker := newModuleTestModel(t)
	read(other)
	if len(other.d.Song.Modules) != 1 || other.d.Song.Modules[0].Name != "voice" {
		t.Fatalf("after loading the instrument, the modules are %+v", other.d.Song.Modules)
	}
	if call := other.d.Song.Patch[0].Units[2]; call.Type != "module" || call.Parameters["module"] != other.d.Song.Modules[0].ID {
		t.Errorf("the module unit is %+v", call)
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter out" {
		t.Errorf("the player got: %v", got)
	}
	// loading it again uses the same module
	read(other)
	if len(other.d.Song.Modules) != 1 {
		t.Errorf("loading the instrument again added a module: %v modules", len(other.d.Song.Modules))
	}
	// a different module with the same name is added with another name
	other.d.Song.Modules[0].Units[1].Parameters["detune"] = 1
	read(other)
	if len(other.d.Song.Modules) != 2 || other.d.Song.Modules[1].Name != "voice 2" {
		t.Fatalf("after loading with a changed module, the modules are %+v", other.d.Song.Modules)
	}
	if id := other.d.Song.Patch[0].Units[2].Parameters["module"]; id != other.d.Song.Modules[1].ID {
		t.Errorf("the module unit uses module %v, want %v", id, other.d.Song.Modules[1].ID)
	}
	// units copied with their module
	other.Unit().List().SetSelected(2)
	other.Unit().List().SetSelected2(2)
	data, ok := other.Unit().List().CopyElements()
	if !ok || !strings.Contains(string(data), "modules:") {
		t.Fatalf("copied units: %s", data)
	}
	third, _ := newModuleTestModel(t)
	if !third.Unit().List().PasteElements(data) || len(third.d.Song.Modules) != 1 || third.d.Song.Modules[0].Name != "voice 2" {
		t.Errorf("after pasting: %+v", third.d.Song.Modules)
	}
}

func TestModuleBuses(t *testing.T) {
	m, _ := newModuleTestModel(t)
	m.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	m.Module().Add().Do()
	m.Unit().Add(false).Do()
	m.Unit().SetType("mcspread")
	bus := m.d.Song.Modules[0].Units[0].Parameters["bus"]
	if buf, ok := m.d.Song.Buffers.Find(bus); bus == 0 || !ok || !buf.Bus {
		t.Fatalf("the mcspread of a module has the bus %v", bus)
	}
	m.Unit().Add(false).Do()
	m.Unit().SetType("mcsum")
	if got := m.d.Song.Modules[0].Units[1].Parameters["bus"]; got != bus {
		t.Errorf("the mcsum uses bus %v, want %v", got, bus)
	}
	m.Module().Delete().Do()
	if len(m.d.Song.Modules) != 0 || len(m.d.Song.Buffers) != 0 {
		t.Errorf("after deleting the module: %v modules, buffers %v", len(m.d.Song.Modules), m.d.Song.Buffers)
	}
}

func TestModuleCannotUseItself(t *testing.T) {
	m, _ := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Unit().Add(false).Do()
	m.Unit().SetType("module")
	u := m.d.Song.Modules[0].Units[m.d.UnitIndex]
	if u.Type != "module" || u.Parameters["module"] != 0 {
		t.Fatalf("the new module unit is %+v", u)
	}
	if p := m.Params().Item(Point{0, m.d.UnitIndex}); (&p).SetValue(1) || m.d.Song.Modules[0].Units[m.d.UnitIndex].Parameters["module"] != 0 {
		t.Errorf("a module unit of a module could use the module itself")
	}
}

func TestUnfold(t *testing.T) {
	m, _ := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Module().AddParam().Do()
	m.Module().ParamName(1).SetValue("detune")
	bindParam(t, m, 1, "detune", 1)
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	arg := m.Params().Item(Point{1, 2})
	arg.SetValue(99)
	units, params := m.Unit().List(), m.Params()
	if units.Count() != 4 || params.Height() != 4 || m.Unit().Item(3).Inner {
		t.Fatalf("folded: %v rows", units.Count())
	}
	// unfolded, the units that the synth runs for the module unit follow it
	m.Unit().Unfold().SetValue(true)
	if units.Count() != 8 || params.Height() != 8 {
		t.Fatalf("unfolded: %v rows", units.Count())
	}
	var types []string
	for i := range units.Count() {
		item := m.Unit().Item(i)
		if item.Inner != (i >= 3 && i <= 6) {
			t.Errorf("row %v: inner %v", i, item.Inner)
		}
		types = append(types, item.Title)
	}
	if got := strings.Join(types, " "); got != "oscillator send Module envelope oscillator mulp filter out" {
		t.Errorf("rows: %v", got)
	}
	// unfolded, the module unit passes its signals on to its inner units,
	// which leave its output
	if head := m.Unit().Item(2); head.Signals.StackUse.NumOutputs != 0 || head.Signals.PassThrough != 0 || head.Stack != 1 || !head.Unfolded {
		t.Errorf("the unfolded module unit: %+v", head)
	}
	if last := m.Unit().Item(6); last.Stack != 1 || !last.Last {
		t.Errorf("the last inner unit: %+v", last)
	}
	// the signals of the inner units: envelope 1, oscillator 2, mulp 1
	after := func(row int) int {
		signals := m.Unit().Item(row).Signals
		return signals.StackAfter()
	}
	if a, b, c := after(3), after(4), after(5); a != 1 || b != 2 || c != 1 {
		t.Errorf("signals after the inner units: %v %v %v", a, b, c)
	}
	// with the value of the module unit, bound
	var detune Parameter
	for x := 0; x < params.RowWidth(4); x++ {
		if p := params.Item(Point{x, 4}); p.Name() == "detune" {
			detune = p
		}
	}
	if name, ok := detune.Bound(); detune.Value() != 99 || !ok || name != "detune" {
		t.Errorf("the detune of the inner unit is %v, bound to %q", detune.Value(), name)
	}
	// the cursor goes through the inner units
	if units.Selected() != 2 {
		t.Fatalf("the module unit is on row %v", units.Selected())
	}
	units.SetSelected(units.Selected() + 1)
	if m.d.UnitIndex != 0 || units.Selected() != 3 || len(m.d.UnitPath) != 1 {
		t.Errorf("down from the module unit: unit %v, row %v, inside %v", m.d.UnitIndex, units.Selected(), m.d.UnitPath)
	}
	units.SetSelected(7)
	if m.d.UnitIndex != 3 || units.Selected() != 7 || len(m.d.UnitPath) != 0 {
		t.Errorf("on the out: unit %v, row %v, inside %v", m.d.UnitIndex, units.Selected(), m.d.UnitPath)
	}
	units.SetSelected(units.Selected() - 1)
	if m.d.UnitIndex != 3 || units.Selected() != 6 || len(m.d.UnitPath) != 1 {
		t.Errorf("up from the out: unit %v, row %v, inside %v", m.d.UnitIndex, units.Selected(), m.d.UnitPath)
	}
	params.SetCursor(Point{0, 2})
	params.MoveCursor(0, 1)
	if c := params.Cursor(); c.Y != 3 || m.d.UnitIndex != 0 || len(m.d.UnitPath) != 1 {
		t.Errorf("the cursor moved down to row %v, unit %v", c.Y, m.d.UnitIndex)
	}
	// moving, copying and deleting work on the units
	units.SetSelected(2)
	units.SetSelected2(2)
	if !units.MoveElements(1) || unitTypes(m.d.Song.Patch[0].Units) != "oscillator send out module" || m.d.UnitIndex != 3 {
		t.Errorf("after moving the module unit down: %v, unit %v", unitTypes(m.d.Song.Patch[0].Units), m.d.UnitIndex)
	}
	if !units.MoveElements(-1) || unitTypes(m.d.Song.Patch[0].Units) != "oscillator send module out" || m.d.UnitIndex != 2 {
		t.Errorf("after moving it back up: %v, unit %v", unitTypes(m.d.Song.Patch[0].Units), m.d.UnitIndex)
	}
	units.SetSelected(units.Count() - 1) // the out, below the inner units
	units.SetSelected2(units.Selected())
	if !units.MoveElements(-1) || unitTypes(m.d.Song.Patch[0].Units) != "oscillator send out module" {
		t.Errorf("after moving the out up: %v", unitTypes(m.d.Song.Patch[0].Units))
	}
	m.History().Undo().Do()
	units.SetSelected(2)
	units.SetSelected2(2)
	data, ok := units.CopyElements()
	if !ok || !units.PasteElements(data) || unitTypes(m.d.Song.Patch[0].Units) != "oscillator send module module out" {
		t.Fatalf("after copying and pasting the module unit: %v", unitTypes(m.d.Song.Patch[0].Units))
	}
	if units.Count() != 13 { // the copy is unfolded like the original
		t.Errorf("with two module units: %v rows", units.Count())
	}
	m.Unit().ToggleUnfold(units.Selected()).Do()
	if units.Count() != 9 || m.Unit().Item(units.Selected()).Unfolded {
		t.Errorf("with one folded: %v rows", units.Count())
	}
	if !units.DeleteElements(false) || unitTypes(m.d.Song.Patch[0].Units) != "oscillator send module out" || units.Count() != 8 {
		t.Errorf("after deleting one: %v, %v rows", unitTypes(m.d.Song.Patch[0].Units), units.Count())
	}
	// the inner units follow the module unit
	units.SetSelected(2)
	arg = params.Item(Point{1, 2})
	arg.SetValue(12)
	for x := 0; x < params.RowWidth(4); x++ {
		if p := params.Item(Point{x, 4}); p.Name() == "detune" && p.Value() != 12 {
			t.Errorf("after changing the module unit, the detune of the inner unit is %v", p.Value())
		}
	}
	m.Unit().Unfold().SetValue(false)
	if units.Count() != 4 {
		t.Errorf("folded again: %v rows", units.Count())
	}
}

// TestModuleUnitParameterKinds checks that a parameter of a module unit is
// like the parameter bound to it: a choice of buses, a delay time on its grid.
func TestModuleUnitParameterKinds(t *testing.T) {
	m, broker := newModuleTestModel(t)
	m.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	m.Module().Add().Do()
	m.Unit().Add(false).Do()
	m.Unit().SetType("mcspread")
	m.Unit().Add(false).Do()
	m.Unit().SetType("mcsum")
	m.Unit().Add(false).Do()
	m.Unit().SetType("delay")
	mod := &m.d.Song.Modules[0]
	mod.Units[2].Parameters["notetracking"] = 2
	func() {
		defer m.change("Test", PatchChange, MajorChange)()
		mod.Units[2].VarArgs = []int{24}
		m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: 50, Name: "Other bus", Channels: sointu.MCChannels, Bus: true})
	}()
	m.Module().AddParam().Do()
	m.Module().AddParam().Do()
	bindParam(t, m, 0, "bus", 1)
	bindParam(t, m, 2, "delaytime", 2)
	mod = &m.d.Song.Modules[0]
	if mod.Units[0].Bind["bus"].Param != 1 || mod.Units[2].Bind["delaytime1"].Param != 2 || mod.Params[1].Default != 24 {
		t.Fatalf("bindings %v %v, defaults %+v", mod.Units[0].Bind, mod.Units[2].Bind, mod.Params)
	}
	// a module unit in the instrument
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	m.Unit().Add(false).Do()
	m.Unit().SetType("module")
	row := m.d.UnitIndex
	bus, time := m.Params().Item(Point{1, row}), m.Params().Item(Point{2, row})
	// the bus: a choice of the buses of the song, stored by ID
	if bus.Type() != ChoiceParameter || bus.Name() != "p1" || bus.Range().Max != 2 {
		t.Fatalf("the bus parameter: type %v, name %q, range %v", bus.Type(), bus.Name(), bus.Range())
	}
	choices := bus.Int()
	if choices.StringOf(2) != "Other bus" || !choices.SetValue(2) {
		t.Fatalf("the choices of the bus: %q", choices.StringOf(2))
	}
	call := &m.d.Song.Patch[0].Units[row]
	if call.Parameters["p1"] != 50 {
		t.Errorf("the module unit has the bus %v, want 50", call.Parameters["p1"])
	}
	if got := playerPatch(t, broker)[0].Units[row].Parameters["bus"]; got != 50 {
		t.Errorf("the player got the bus %v, want 50", got)
	}
	// the delay time: a choice of note lengths, as it follows the tempo
	if time.Type() != ChoiceParameter || time.Value() != 24 || time.Label() == "24" {
		t.Fatalf("the delay time parameter: type %v, value %v, label %q", time.Type(), time.Value(), time.Label())
	}
	lengths := time.Int()
	if !lengths.SetValue(lengths.Value() - 1) { // a longer note
		t.Fatal("the delay time could not be set")
	}
	call = &m.d.Song.Patch[0].Units[row]
	if v := call.Parameters["p2"]; v <= 24 {
		t.Errorf("the module unit has the delay time %v, want more than 24", v)
	}
	if got := playerPatch(t, broker)[0].Units[row+2].VarArgs; len(got) != 1 || got[0] != call.Parameters["p2"] {
		t.Errorf("the player got the delay times %v, want %v", got, call.Parameters["p2"])
	}
	if m.d.Song.Modules[0].Units[2].VarArgs[0] != 24 {
		t.Errorf("the delay time of the module changed to %v", m.d.Song.Modules[0].Units[2].VarArgs[0])
	}
	// resetting it gives the default of the module
	time = m.Params().Item(Point{2, row})
	time.Reset()
	if v := m.d.Song.Patch[0].Units[row].Parameters["p2"]; v != 24 {
		t.Errorf("after a reset the delay time is %v, want 24", v)
	}
}

func TestUnfoldIsSaved(t *testing.T) {
	m, _ := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().List().SetSelected(2)
	m.Unit().Unfold().SetValue(true)
	if !m.d.Song.Patch[0].Units[2].Unfolded || m.Unit().List().Count() != 8 {
		t.Fatalf("unfolded: %v, %v rows", m.d.Song.Patch[0].Units[2].Unfolded, m.Unit().List().Count())
	}
	var file bytes.Buffer
	m.Song().Write(nopWriteCloser{&file})
	if !strings.Contains(file.String(), "unfolded: true") {
		t.Errorf("the song file does not tell that the module unit is unfolded")
	}
	other, _ := newModuleTestModel(t)
	other.Song().Read(io.NopCloser(bytes.NewReader(file.Bytes())))
	if !other.d.Song.Patch[0].Units[2].Unfolded || other.Unit().List().Count() != 8 {
		t.Errorf("after loading: %v rows", other.Unit().List().Count())
	}
	m.History().Undo().Do()
	if m.d.Song.Patch[0].Units[2].Unfolded || m.Unit().List().Count() != 4 {
		t.Errorf("after undo: %v rows", m.Unit().List().Count())
	}
}

func TestModulePresets(t *testing.T) {
	dir := t.TempDir()
	m, _ := newModuleTestModel(t)
	m.modulePresetPath = dir
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Module().Name().SetValue("my voice")
	// a module using it, saved with it
	m.Module().Add().Do()
	m.Module().Name().SetValue("outer")
	m.Unit().Add(false).Do()
	m.Unit().SetType("module")
	if got := m.d.Song.Modules[1].Units[0].Parameters["module"]; got != m.d.Song.Modules[0].ID {
		t.Fatalf("the module unit of outer uses module %v", got)
	}
	m.Module().SavePreset().Do()
	presets := m.Module().Presets()
	if r := presets.Range(); r.Max != 0 || presets.StringOf(0) != "outer" {
		t.Fatalf("the presets: %v, %q", r, presets.StringOf(0))
	}
	// another song gets both modules, and the saved one is selected
	other, _ := newModuleTestModel(t)
	other.modulePresetPath = dir
	other.loadModulePresets()
	other.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	if !other.Module().Presets().SetValue(0) {
		t.Fatal("the preset could not be loaded")
	}
	mods := other.d.Song.Modules
	if len(mods) != 2 || mods[0].Name != "my voice" || mods[1].Name != "outer" || other.d.ModuleIndex != 1 {
		t.Fatalf("after loading the preset: %+v, selected %v", mods, other.d.ModuleIndex)
	}
	if mods[1].Units[0].Parameters["module"] != mods[0].ID {
		t.Errorf("the module unit of outer uses module %v, want %v", mods[1].Units[0].Parameters["module"], mods[0].ID)
	}
	// loading it again adds nothing
	other.Module().Presets().SetValue(0)
	if len(other.d.Song.Modules) != 2 {
		t.Errorf("loading the preset again: %v modules", len(other.d.Song.Modules))
	}
	// saving over a preset asks first
	file := filepath.Join(dir, "outer.yml")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m.Module().Comment().SetValue("changed")
	m.Module().SavePreset().Do()
	if now, _ := os.ReadFile(file); m.Dialog() != OverwriteModulePresetDialog || m.Module().AskedPreset() != "outer" || !bytes.Equal(now, before) {
		t.Fatalf("saving over the preset: dialog %v about %q, file changed: %v", m.Dialog(), m.Module().AskedPreset(), !bytes.Equal(now, before))
	}
	m.CancelDialog().Do()
	if now, _ := os.ReadFile(file); m.Dialog() != NoDialog || !bytes.Equal(now, before) {
		t.Errorf("after cancelling: dialog %v, file changed: %v", m.Dialog(), !bytes.Equal(now, before))
	}
	m.Module().SavePreset().Do()
	m.Module().OverwritePreset().Do()
	if now, _ := os.ReadFile(file); m.Dialog() != NoDialog || !strings.Contains(string(now), "comment: changed") {
		t.Errorf("after saving over it: dialog %v, file:\n%s", m.Dialog(), now)
	}
	// a module with another name is saved without asking
	m.Module().Name().SetValue("second")
	m.Module().SavePreset().Do()
	presets = m.Module().Presets()
	if r := presets.Range(); m.Dialog() != NoDialog || r.Max != 1 || presets.StringOf(1) != "second" {
		t.Fatalf("after saving a second preset: dialog %v, presets %v", m.Dialog(), r)
	}
	// deleting a preset asks first
	deletion := m.Module().DeletePresets()
	if deletion.StringOf(0) != "Delete outer" || m.Module().ConfirmDeletePreset().Enabled() {
		t.Errorf("the deletions: %q", deletion.StringOf(0))
	}
	deletion.SetValue(0)
	if _, err := os.Stat(file); m.Dialog() != DeleteModulePresetDialog || m.Module().AskedPreset() != "outer" || err != nil {
		t.Fatalf("deleting the preset: dialog %v about %q, file: %v", m.Dialog(), m.Module().AskedPreset(), err)
	}
	m.CancelDialog().Do()
	if _, err := os.Stat(file); err != nil || m.Module().Presets().Range().Max != 1 {
		t.Errorf("after cancelling the file is gone: %v", err)
	}
	m.Module().DeletePresets().SetValue(0)
	m.Module().ConfirmDeletePreset().Do()
	presets = m.Module().Presets()
	if _, err := os.Stat(file); !os.IsNotExist(err) || m.Dialog() != NoDialog || presets.Range().Max != 0 || presets.StringOf(0) != "second" {
		t.Errorf("after deleting: file %v, dialog %v, presets %v", err, m.Dialog(), presets.Range())
	}
}

func TestScaledBinding(t *testing.T) {
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Module().AddParam().Do()
	m.Module().ParamName(1).SetValue("bright")
	bindParam(t, m, 3, "frequency", 1) // the filter, at 40
	bindParam(t, m, 3, "resonance", 1)
	at0, at128 := m.Module().BindingAt(1, false), m.Module().BindingAt(1, true)
	if at0.Value() != 0 || at128.Value() != 128 {
		t.Fatalf("a binding that is not scaled is from %v to %v", at0.Value(), at128.Value())
	}
	// the resonance from 100 down to 20: the default, 40, gives it 75
	at0.SetValue(100)
	at128.SetValue(20)
	mod := &m.d.Song.Modules[0]
	if b := mod.Units[3].Bind["resonance"]; !b.Scaled || b.Min != 100 || b.Max != 20 {
		t.Fatalf("the binding: %+v", b)
	}
	if mod.Params[0].Default != 40 || mod.Units[3].Parameters["frequency"] != 40 || mod.Units[3].Parameters["resonance"] != 75 {
		t.Errorf("default %v, frequency %v, resonance %v", mod.Params[0].Default, mod.Units[3].Parameters["frequency"], mod.Units[3].Parameters["resonance"])
	}
	// the frequency, the first binding, from 30 to 94: the default becomes
	// the value that keeps it at 40, and the module unit follows
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	arg := m.Params().Item(Point{1, 2})
	arg.SetValue(62)
	m.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	for x := 0; x < m.Params().RowWidth(3); x++ {
		if p := m.Params().Item(Point{x, 3}); p.Name() == "frequency" {
			m.Params().SetCursor(Point{x, 3})
		}
	}
	m.Module().BindingAt(1, false).SetValue(30)
	m.Module().BindingAt(1, true).SetValue(94)
	mod = &m.d.Song.Modules[0]
	if b := mod.Units[3].Bind["frequency"]; !b.Scaled || b.Min != 30 || b.Max != 94 {
		t.Fatalf("the binding of the frequency: %+v", b)
	}
	if mod.Params[0].Default != 20 || mod.Units[3].Parameters["frequency"] != 40 {
		t.Errorf("default %v, frequency %v", mod.Params[0].Default, mod.Units[3].Parameters["frequency"])
	}
	if got := m.d.Song.Patch[0].Units[2].Parameters["p1"]; got != 64 { // 30 + 64/2 = 62
		t.Errorf("the module unit has %v, want 64", got)
	}
	filter := playerPatch(t, broker)[0].Units[5]
	if filter.Parameters["frequency"] != 62 || filter.Parameters["resonance"] != 60 {
		t.Errorf("the player got frequency %v and resonance %v, want 62 and 60", filter.Parameters["frequency"], filter.Parameters["resonance"])
	}
	// the module unit: a knob from 0 to 128, labelled with the frequency
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	arg = m.Params().Item(Point{1, 2})
	if r := arg.Range(); r.Min != 0 || r.Max != 128 || arg.Value() != 64 || arg.Name() != "bright" {
		t.Errorf("the parameter of the module unit: %v in %v", arg.Value(), r)
	}
	plain := m.Params().Item(Point{1, 2})
	if label := plain.Label(); label == "64" || label == "" {
		t.Errorf("the knob is labelled %q", label)
	}
	// editing the bound frequency in the module sets the default
	m.Unit().List().SetSelected(2)
	m.Unit().OpenModule().Do()
	bound := paramNamed(t, m, 3, "frequency")
	bound.SetValue(94)
	if d := m.d.Song.Modules[0].Params[0].Default; d != 128 || m.d.Song.Modules[0].Units[3].Parameters["resonance"] != 20 {
		t.Errorf("after setting the frequency to 94: default %v, resonance %v", d, m.d.Song.Modules[0].Units[3].Parameters["resonance"])
	}
}
