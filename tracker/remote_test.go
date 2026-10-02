package tracker

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func newRemoteTestModel(t *testing.T) (*Model, *Remote) {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	drainPlayer(broker)
	return m, m.Remote()
}

// remoteStep checks that a change made through Remote is one step of the
// undo history, which brings the song back as it was.
func remoteStep(t *testing.T, m *Model, change func() (string, error)) string {
	t.Helper()
	before := m.d.Song.Copy()
	undos := len(m.undoStack)
	text, err := change()
	if err != nil {
		t.Fatalf("the change failed: %v", err)
	}
	if len(m.undoStack) != undos+1 {
		t.Fatalf("the change made %d steps of the undo history, not 1", len(m.undoStack)-undos)
	}
	after := m.d.Song.Copy()
	if reflect.DeepEqual(before, after) {
		t.Fatalf("the change did not change the song")
	}
	m.History().Undo().Do()
	if !reflect.DeepEqual(before, m.d.Song) {
		t.Fatalf("undo did not bring the song back")
	}
	m.History().Redo().Do()
	if !reflect.DeepEqual(after, m.d.Song) {
		t.Fatalf("redo did not make the change again")
	}
	return text
}

// remoteRefused checks that a change is refused, and leaves the song and the
// undo history as they were.
func remoteRefused(t *testing.T, m *Model, want string, change func() (string, error)) {
	t.Helper()
	before := m.d.Song.Copy()
	undos := len(m.undoStack)
	_, err := change()
	if err == nil {
		t.Fatalf("the change was not refused")
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the error %q does not tell %q", err, want)
	}
	if len(m.undoStack) != undos || !reflect.DeepEqual(before, m.d.Song) {
		t.Fatalf("the refused change changed the song or the undo history")
	}
}

func findType(t *testing.T, units []sointu.Unit, unitType string) *sointu.Unit {
	t.Helper()
	for i := range units {
		if units[i].Type == unitType {
			return &units[i]
		}
	}
	t.Fatalf("no %s unit", unitType)
	return nil
}

func TestRemoteDescribe(t *testing.T) {
	_, r := newRemoteTestModel(t)
	song := r.Song()
	for _, want := range []string{"100 BPM", `0 "Instr": 1 voices, 6 units of 63, MIDI channel 1 (auto), to outaux`, `1 "Global"`, "bus: reads aux1"} {
		if !strings.Contains(song, want) {
			t.Errorf("the song does not tell %q:\n%s", want, song)
		}
	}
	instr, err := r.Instrument("Instr", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#1 envelope [0>1]", "attack=64(92.88 ms)[0..128]", "#3 mulp [2>1]", "type=0(sine)", "delaytime1=48"} {
		if !strings.Contains(instr, want) {
			t.Errorf("the instrument does not tell %q:\n%s", want, instr)
		}
	}
	if _, err := r.Instrument("7", false); err == nil {
		t.Error("an instrument that is not there was described")
	}
	types, err := RemoteUnitTypes("")
	if err != nil || !strings.Contains(types, "filter [1>1 (2>2)]") || !strings.Contains(types, "reverb [2>2, go/wasm]") {
		t.Errorf("the unit types do not tell the stack and go/wasm:\n%s", types)
	}
	filter, err := RemoteUnitTypes("filter")
	if err != nil || !strings.Contains(filter, "48=988,") {
		t.Errorf("the displayed values of the filter frequency are not listed:\n%s", filter)
	}
	if envelope, _ := RemoteUnitTypes("envelope"); !strings.Contains(envelope, "64=92.88 ms, 72=262.70 ms") {
		t.Errorf("the displayed values of the envelope times are not listed with their units:\n%s", envelope)
	}
	for _, name := range sointu.UnitNames {
		if unitDescriptions[name] == "" {
			t.Errorf("the unit type %s has no description", name)
		}
	}
	presets := r.Presets("mastering")
	if !strings.Contains(presets, "Global mastering 2 buses") {
		t.Errorf("the presets do not list Global mastering 2 buses:\n%s", presets)
	}
}

func TestRemoteEditUnits(t *testing.T) {
	m, r := newRemoteTestModel(t)
	units := m.d.Song.Patch[0].Units
	env, osc := units[0].ID, units[1].ID
	text := remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{
			{Unit: env, Params: map[string]any{"attack": 10.0, "release": 70.0}},
			{Unit: osc, Params: map[string]any{"type": "trisaw", "transpose": 52.0}, Comment: ptr("bass")},
		})
	})
	u := m.d.Song.Patch[0].Units
	if u[0].Parameters["attack"] != 10 || u[0].Parameters["release"] != 70 || u[1].Parameters["type"] != sointu.Trisaw || u[1].Parameters["transpose"] != 52 || u[1].Comment != "bass" {
		t.Errorf("the units were not changed: %v %v", u[0], u[1])
	}
	if !strings.Contains(text, "comment") && !strings.Contains(text, "// bass") {
		t.Errorf("the result does not show the units:\n%s", text)
	}
	if m.d.UnitIndex != 1 || m.d.InstrIndex != 0 {
		t.Errorf("the cursor is not on the unit changed last: unit %d", m.d.UnitIndex)
	}
	remoteRefused(t, m, "0 to 128", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: env, Params: map[string]any{"attack": 20.0}}, {Unit: osc, Params: map[string]any{"gain": 200.0}}})
	})
	remoteRefused(t, m, "has no parameter", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: env, Params: map[string]any{"cutoff": 20.0}}})
	})
	remoteRefused(t, m, "no unit has the ID", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: 999, Params: map[string]any{"attack": 20.0}}})
	})
	remoteRefused(t, m, "displayed as", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: osc, Params: map[string]any{"type": "supersaw"}}})
	})
	remoteRefused(t, m, "whole number", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: osc, Params: map[string]any{"gain": 1.5}}})
	})
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: osc, Disabled: ptr(true)}})
	})
	if !m.d.Song.Patch[0].Units[1].Disabled {
		t.Error("the unit was not disabled")
	}
}

func TestRemoteAddDeleteMoveUnits(t *testing.T) {
	m, r := newRemoteTestModel(t)
	osc := m.d.Song.Patch[0].Units[1].ID
	text := remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "", osc, 0, []RemoteNewUnit{
			{Type: "filter", Params: map[string]any{"frequency": 40.0}},
			{Type: "oscillator", Params: map[string]any{"lfo": 1.0}},
			{Type: "send", Params: map[string]any{"target": "new:0", "port": "frequency", "amount": 80.0, "sendpop": 1.0}},
			{Type: "eq", Bands: []RemoteEQBand{{Type: "lowcut", Frequency: 30}, {Type: "bell", Frequency: 250, Gain: -4, Q: 1.4}}},
		})
	})
	units := m.d.Song.Patch[0].Units
	if len(units) != 10 || units[2].Type != "filter" || units[3].Type != "oscillator" || units[4].Type != "send" || units[5].Type != "eq" {
		t.Fatalf("the units were not added after the oscillator: %v", units)
	}
	if units[4].Parameters["target"] != units[2].ID || units[4].Parameters["port"] != 0 {
		t.Errorf("the send does not go to the frequency of the filter: %v", units[4].Parameters)
	}
	if len(units[5].Bands) != 2 || units[5].Bands[1].Gain != -4 {
		t.Errorf("the eq has not got its bands: %v", units[5].Bands)
	}
	if !strings.Contains(text, "-> frequency of #") || !strings.Contains(text, "{bell 250 Hz -4 dB q 1.4}") {
		t.Errorf("the result does not show the send and the bands:\n%s", text)
	}
	filter := units[2].ID
	remoteRefused(t, m, "no unit type", func() (string, error) {
		return r.AddUnits("0", "", 0, 0, []RemoteNewUnit{{Type: "supersaw"}})
	})
	remoteRefused(t, m, "ports", func() (string, error) {
		return r.AddUnits("0", "", 0, 0, []RemoteNewUnit{{Type: "send", Params: map[string]any{"target": float64(filter), "port": "cutoff"}}})
	})
	remoteRefused(t, m, "more than 63 units", func() (string, error) {
		many := make([]RemoteNewUnit, 60)
		for i := range many {
			many[i] = RemoteNewUnit{Type: "gain"}
		}
		return r.AddUnits("0", "", 0, 0, many)
	})
	remoteStep(t, m, func() (string, error) { return r.MoveUnits([]int{filter}, "", "", 0, osc) })
	if m.d.Song.Patch[0].Units[1].ID != filter || m.d.Song.Patch[0].Units[2].ID != osc {
		t.Errorf("the filter was not moved before the oscillator")
	}
	remoteStep(t, m, func() (string, error) { return r.MoveUnits([]int{filter}, "Global", "", 0, 0) })
	if g := m.d.Song.Patch[1].Units; g[len(g)-1].ID != filter {
		t.Errorf("the filter was not moved to the end of Global")
	}
	remoteStep(t, m, func() (string, error) { return r.DeleteUnits([]int{filter}) })
	if _, err := r.findUnit(filter); err == nil {
		t.Error("the filter was not deleted")
	}
	remoteRefused(t, m, "last unit", func() (string, error) {
		ids := []int{}
		for _, u := range m.d.Song.Patch[1].Units {
			ids = append(ids, u.ID)
		}
		return r.DeleteUnits(ids)
	})
}

func TestRemoteInstruments(t *testing.T) {
	m, r := newRemoteTestModel(t)
	remoteStep(t, m, func() (string, error) { return r.AddInstrument("Bus", "Global mastering 2 buses", 1, "", "Global") })
	if len(m.d.Song.Patch) != 3 || m.d.Song.Patch[2].Name != "Bus" || len(m.d.Song.Patch[2].Units) < 10 {
		t.Fatalf("the preset was not added as instrument 2 Bus: %v", m.d.Song.Patch[2].Name)
	}
	if len(m.d.Song.Modules) == 0 {
		t.Error("the modules of the preset were not added")
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditInstrument("Instr", ptr("Lead"), ptr("a lead"), ptr(3), ptr(true), nil, "")
	})
	if i := m.d.Song.Patch[0]; i.Name != "Lead" || i.Comment != "a lead" || i.NumVoices != 3 || !i.Mute {
		t.Errorf("the instrument was not changed: %v", i)
	}
	remoteStep(t, m, func() (string, error) { return r.EditInstrument("1", nil, nil, nil, nil, ptr(true), "") })
	for i, instr := range m.d.Song.Patch {
		if instr.Mute != (i != 1) {
			t.Errorf("solo of instrument 1: instrument %d muted %v", i, instr.Mute)
		}
	}
	remoteStep(t, m, func() (string, error) { return r.EditInstrument("0", nil, nil, nil, nil, nil, "Reverb unit Hall") })
	if findType(t, m.d.Song.Patch[0].Units, "reverb") == nil || m.d.Song.Patch[0].NumVoices != 3 {
		t.Error("the preset was not loaded into the instrument, keeping its voices")
	}
	remoteRefused(t, m, "no instrument preset", func() (string, error) { return r.AddInstrument("", "Nope", 1, "", "") })
	remoteRefused(t, m, "voices", func() (string, error) { return r.EditInstrument("0", nil, nil, ptr(1000), nil, nil, "") })
	remoteRefused(t, m, "no instrument", func() (string, error) { return r.DeleteInstrument("9") })
	remoteStep(t, m, func() (string, error) { return r.DeleteInstrument("Bus") })
	if len(m.d.Song.Patch) != 2 {
		t.Error("the instrument was not deleted")
	}
	remoteStep(t, m, func() (string, error) { return r.SetBPM(174) })
	if m.d.Song.BPM != 174 {
		t.Error("the tempo was not set")
	}
	remoteRefused(t, m, "BPM", func() (string, error) { return r.SetBPM(0) })
}

func TestRemoteModules(t *testing.T) {
	m, r := newRemoteTestModel(t)
	remoteStep(t, m, func() (string, error) { return r.AddModule("", "Reverb", 0) })
	if len(m.d.Song.Modules) != 1 || m.d.Song.Modules[0].Name != "Reverb" {
		t.Fatalf("the module preset was not added: %v", m.d.Song.Modules)
	}
	out := m.d.Song.Patch[1].Units[len(m.d.Song.Patch[1].Units)-1].ID
	text := remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "", 0, out, []RemoteNewUnit{{Type: "module", Params: map[string]any{"module": "Reverb", "decay": 100.0, "p1": 70.0}}})
	})
	call := findType(t, m.d.Song.Patch[1].Units, "module")
	if call.Parameters["p2"] != 100 || call.Parameters["p1"] != 70 {
		t.Errorf("the module unit has not got its parameters: %v", call.Parameters)
	}
	if !strings.Contains(text, "p2:decay=100") {
		t.Errorf("the module unit does not show its parameters by name:\n%s", text)
	}
	remoteRefused(t, m, "needs the parameter module", func() (string, error) {
		return r.AddUnits("0", "", 0, 0, []RemoteNewUnit{{Type: "module"}})
	})
	remoteStep(t, m, func() (string, error) { return r.AddModule("Saws", "", 0) })
	saws := len(m.d.Song.Modules) - 1
	remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "Saws", 0, 0, []RemoteNewUnit{
			{Type: "oscillator", Params: map[string]any{"type": "trisaw"}, Bind: map[string]*RemoteBinding{"detune": {Param: 1}}},
			{Type: "oscillator", Params: map[string]any{"type": "trisaw", "detune": 58.0}},
			{Type: "addp"},
		})
	})
	mod := m.d.Song.Modules[saws]
	if len(mod.Units) != 3 || len(mod.Params) != 1 || mod.Units[0].Bind["detune"].Param != 1 {
		t.Fatalf("the module has not got its units and the binding: %+v", mod)
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditModule("Saws", nil, ptr("two saws"), nil, []RemoteModuleParam{{Name: ptr("spread"), Default: ptr(80)}}, nil)
	})
	mod = m.d.Song.Modules[saws]
	if mod.Params[0].Name != "spread" || mod.Params[0].Default != 80 || mod.Units[0].Parameters["detune"] != 80 || mod.Comment != "two saws" {
		t.Errorf("the module was not changed: %+v", mod)
	}
	described, err := r.Module("Saws", false)
	if err != nil || !strings.Contains(described, "bound: detune=p1") || !strings.Contains(described, `p1 "spread": default 80`) {
		t.Errorf("the module does not show its parameters and binding:\n%s", described)
	}
	scaled := mod.Units[1].ID
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: scaled, Bind: map[string]*RemoteBinding{"detune": {Param: 1, Min: ptr(64), Max: ptr(40)}}}})
	})
	if b := m.d.Song.Modules[saws].Units[1].Bind["detune"]; !b.Scaled || b.Min != 64 || b.Max != 40 {
		t.Errorf("the scaled binding was not made: %+v", b)
	}
	remoteRefused(t, m, "only the units of a module", func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: m.d.Song.Patch[0].Units[0].ID, Bind: map[string]*RemoteBinding{"attack": {Param: 1}}}})
	})
	remoteStep(t, m, func() (string, error) { return r.EditModule("Saws", nil, nil, nil, nil, []int{1}) })
	if len(m.d.Song.Modules[saws].Params) != 0 || len(m.d.Song.Modules[saws].Units[0].Bind) != 0 {
		t.Error("the parameter of the module was not deleted with its bindings")
	}
	remoteStep(t, m, func() (string, error) { return r.DeleteModule("Reverb") })
	if call := findType(t, m.d.Song.Patch[1].Units, "module"); call.Parameters["module"] != 0 {
		t.Error("the module unit still uses the deleted module")
	}
}

func TestRemoteHistory(t *testing.T) {
	m, r := newRemoteTestModel(t)
	before := m.d.Song.Copy()
	if _, err := r.SetBPM(120); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetBPM(130); err != nil {
		t.Fatal(err)
	}
	if m.d.Song.BPM != 130 {
		t.Fatal("the tempo was not set")
	}
	if _, err := r.Undo(2); err != nil || !reflect.DeepEqual(before, m.d.Song) {
		t.Fatalf("two steps were not undone: %v", err)
	}
	if _, err := r.Redo(1); err != nil || m.d.Song.BPM != 120 {
		t.Fatalf("one step was not redone: %v", err)
	}
	if _, err := r.Redo(5); err != nil || m.d.Song.BPM != 130 {
		t.Fatalf("the last step was not redone: %v", err)
	}
	if _, err := r.Redo(1); err == nil {
		t.Error("there was something to redo")
	}
}

func TestRemoteRenderSource(t *testing.T) {
	m, r := newRemoteTestModel(t)
	before := m.d.Song.Copy()
	undos := len(m.undoStack)
	osc := m.d.Song.Patch[0].Units[1].ID
	src, err := r.RenderSource("0", true, &RemoteWhatIf{Edits: []RemoteUnitEdit{{Unit: osc, Params: map[string]any{"type": "pulse"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.undoStack) != undos || !reflect.DeepEqual(before, m.d.Song) {
		t.Fatal("the render changed the song or the undo history")
	}
	if src.Song.Patch[0].Units[1].Parameters["type"] != sointu.Pulse {
		t.Error("the copy has not got the change of the render")
	}
	if g := findType(t, src.Song.Patch[1].Units, "out"); g.Parameters["gain"] != 0 {
		t.Error("the dry render did not silence the other instruments")
	}
	if _, err := r.RenderSource("0", true, &RemoteWhatIf{Edits: []RemoteUnitEdit{{Unit: osc, Params: map[string]any{"type": 99.0}}}}); err == nil {
		t.Error("a change out of range was rendered")
	}
	if len(m.undoStack) != undos || !reflect.DeepEqual(before, m.d.Song) {
		t.Fatal("the refused render changed the song or the undo history")
	}
	// an instrument that only writes to an aux channel is heard dry
	patch := sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{{Type: "loadval", Parameters: sointu.ParamMap{"value": 96}}, {Type: "aux", Parameters: sointu.ParamMap{"channel": 8, "gain": 128}}}},
		{NumVoices: 1, Units: []sointu.Unit{{Type: "in", Parameters: sointu.ParamMap{"channel": 8, "stereo": 1}}, {Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}}}},
	}
	notes := dryRouting(patch, 0)
	if patch[0].Units[1].Parameters["channel"] != 0 || len(notes) != 1 || patch[1].Units[1].Parameters["gain"] != 0 {
		t.Errorf("the aux unit was not sent to the main output: %v %v", patch, notes)
	}
}

func TestRemotePanicLeavesTheSong(t *testing.T) {
	m, r := newRemoteTestModel(t)
	before := m.d.Song.Copy()
	undos := len(m.undoStack)
	_, err := r.edit(PatchChange, func() (remoteFocus, string, error) {
		m.d.Song.Patch[0].Name = "changed"
		var units []sointu.Unit
		_ = units[3] // panics
		return remoteFocus{}, "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "not made") {
		t.Fatalf("the panic was not an error: %v", err)
	}
	if len(m.undoStack) != undos || !reflect.DeepEqual(before, m.d.Song) || m.changeLevel != 0 {
		t.Fatal("the panic left the song changed")
	}
	if _, err := r.SetBPM(140); err != nil || len(m.undoStack) != undos+1 {
		t.Fatalf("changes do not work after the panic: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestRemoteSelection(t *testing.T) {
	m, r := newRemoteTestModel(t)
	m.d.InstrIndex, m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 1, 2, 0
	sel := r.Selection(true)
	osc, mulp := m.d.Song.Patch[0].Units[1].ID, m.d.Song.Patch[0].Units[2].ID
	for _, want := range []string{"tab Editor", `instrument 0 "Instr"`, "unit #" + itoa(osc) + " oscillator", "(units #" + itoa(osc) + " #" + itoa(mulp) + " selected)", "parameter ", "not playing", "score cursor: track 0"} {
		if !strings.Contains(sel, want) {
			t.Errorf("the selection does not tell %q:\n%s", want, sel)
		}
	}
	if song := r.Song(); !strings.Contains(song, "selection: tab Editor") {
		t.Errorf("get_song does not tell the selection:\n%s", song)
	}
	m.d.InstrumentTab = InstrumentBuffersTab
	if sel := r.Selection(false); strings.Contains(sel, "unit #") || !strings.Contains(sel, "tab Buffers") {
		t.Errorf("the Buffers tab without buffers: %s", sel)
	}
	if _, err := r.AddModule("", "Reverb", 0); err != nil {
		t.Fatal(err)
	}
	// a change moves the cursor, so the selection is what it was about
	if sel := r.Selection(false); !strings.Contains(sel, `tab Modules | instrument 0 "Instr" | module "Reverb" | unit #`) {
		t.Errorf("the selection after adding a module: %s", sel)
	}
	m.d.InstrumentTab, m.d.BufferIndex = InstrumentBuffersTab, 0
	if sel := r.Selection(false); !strings.Contains(sel, `buffer `) || !strings.Contains(sel, `"Bus 1"`) {
		t.Errorf("the selected buffer is not told: %s", sel)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestRemoteInstrumentOrder(t *testing.T) {
	m, r := newRemoteTestModel(t)
	text := remoteStep(t, m, func() (string, error) { return r.AddInstrument("Saw", "", 1, "", "") })
	if m.d.Song.Patch[1].Name != "Saw" || m.d.Song.Patch[2].Name != "Global" {
		t.Fatalf("the instrument was not added before the bus:\n%s", text)
	}
	if !strings.Contains(text, "MIDI channels changed") {
		t.Errorf("the result does not tell that the bus moved to another MIDI channel:\n%s", text)
	}
	text = remoteStep(t, m, func() (string, error) { return r.MoveInstrument("Instr", "", "Global") })
	if m.d.Song.Patch[2].Name != "Instr" || m.d.Song.Patch[1].Name != "Global" {
		t.Fatalf("the instrument was not moved after the bus:\n%s", text)
	}
	if song := r.Song(); !strings.Contains(song, `WARNING: instrument 2 "Instr" writes aux1 after instrument 1 "Global" reads it`) {
		t.Errorf("the song does not warn of the instrument after its bus:\n%s", song)
	}
	remoteStep(t, m, func() (string, error) { return r.MoveInstrument("Instr", "0", "") })
	if m.d.Song.Patch[0].Name != "Instr" || strings.Contains(r.Song(), "WARNING") {
		t.Errorf("the instrument was not moved back first:\n%s", r.Song())
	}
	remoteRefused(t, m, "before or after", func() (string, error) { return r.MoveInstrument("Instr", "", "") })
}

func TestRemoteChanges(t *testing.T) {
	m, r := newRemoteTestModel(t)
	text, _ := r.Call(func() (string, error) { return "", nil })
	if !strings.Contains(text, "first call") {
		t.Errorf("the first call does not say so: %q", text)
	}
	text, _ = r.Call(func() (string, error) { return "", nil })
	if text != "nothing changed since your previous call" {
		t.Errorf("an unchanged song: %q", text)
	}
	// the user changes the song in the tracker
	instr := &m.d.Song.Patch[0]
	osc := findType(t, instr.Units, "oscillator")
	osc.Parameters["transpose"] = 76
	instr.Units = append(instr.Units, sointu.Unit{Type: "distort", ID: 999, Parameters: map[string]int{"drive": 80}})
	m.d.Song.BPM = 140
	m.d.Song.Patch[1].Name = "Master"
	text, _ = r.Call(func() (string, error) { return "the answer", nil })
	for _, want := range []string{"NOTE: the song changed",
		fmt.Sprintf(`instrument 0 "Instr": unit #%d oscillator: transpose=76(1 oct) (was 64(0 oct))`, osc.ID),
		`instrument 0 "Instr": added unit #999 distort`, "tempo: 140 BPM", `instrument 1 "Master": renamed from "Global"`, "the answer"} {
		if !strings.Contains(text, want) {
			t.Errorf("the changes do not tell %q:\n%s", want, text)
		}
	}
	text, _ = r.Call(func() (string, error) { return "the answer", nil })
	if text != "the answer" {
		t.Errorf("the changes are told again: %q", text)
	}
}
