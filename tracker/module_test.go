package tracker

import (
	"bytes"
	"io"
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
	if mod.Units[3].Bind["frequency"] != 1 || mod.Params[0].Default != 40 {
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
	for x := 0; x < m.Params().RowWidth(unit); x++ {
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
