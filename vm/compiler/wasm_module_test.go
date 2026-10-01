package compiler_test

import (
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

func moduleTestUnit(typ string, id int, params sointu.ParamMap, bind map[string]int) sointu.Unit {
	u := sointu.MakeUnit(typ)
	u.ID = id
	for name, k := range bind {
		if u.Bind == nil {
			u.Bind = map[string]sointu.Binding{}
		}
		u.Bind[name] = sointu.Binding{Param: k}
	}
	for k, v := range params {
		u.Parameters[k] = v
	}
	return u
}

// moduleTestSongs returns a song with modules and the same song written
// without them. The first instrument, with two voices, plays a module of an
// envelope, another module (two oscillators, with bound detune and color)
// and a filter, whose cutoff, a parameter of the module with a scaled
// binding, an LFO modulates through a send to the module unit. With reverb, a second instrument runs
// each channel of the first one's sound through a module of mc units, which
// has its own bus: the second module unit gets a clone of it.
func moduleTestSongs(reverb bool) (withModules, without sointu.Song) {
	unit := moduleTestUnit
	osc := func(detune, color int, bind map[string]int) sointu.Unit {
		return unit("oscillator", 0, sointu.ParamMap{"transpose": 64, "detune": detune, "color": color, "gain": 128, "type": sointu.Trisaw}, bind)
	}
	envelope := unit("envelope", 0, sointu.ParamMap{"attack": 40, "decay": 70, "sustain": 60, "release": 70, "gain": 128}, nil)
	filter := func(id, frequency int, bind map[string]int) sointu.Unit {
		return unit("filter", id, sointu.ParamMap{"frequency": frequency, "resonance": 50, "lowpass": 1}, bind)
	}
	mulp, addp := unit("mulp", 0, nil, nil), unit("addp", 0, nil, nil)
	mc := func(bus int) []sointu.Unit {
		return []sointu.Unit{
			unit("mcspread", 0, sointu.ParamMap{"bus": bus, "stereo": 0, "gain": 80}, nil),
			unit("mcloop", 0, sointu.ParamMap{"bus": bus, "feedback": 110}, nil),
			unit("mcdelay", 0, sointu.ParamMap{"bus": bus, "size": 300, "decay": 60}, nil),
			unit("mcmix", 0, sointu.ParamMap{"bus": bus, "type": sointu.MCMixHouseholder}, nil),
			unit("mcloopend", 0, sointu.ParamMap{"bus": bus}, nil),
			unit("mcsum", 0, sointu.ParamMap{"bus": bus, "stereo": 0}, nil),
		}
	}
	modules := sointu.Modules{
		{ID: 1, Name: "saws", Params: []sointu.ModuleParam{{Name: "detune", Default: 64}, {Name: "color", Default: 64}}, Units: []sointu.Unit{
			osc(64, 64, map[string]int{"detune": 1, "color": 2}), osc(50, 100, nil), addp,
		}},
		{ID: 2, Name: "voice", Params: []sointu.ModuleParam{{Name: "cutoff", Default: 64}, {Name: "detune", Default: 64}}, Units: []sointu.Unit{
			envelope,
			moduleTestUnit("module", 0, sointu.ParamMap{"module": 1, "p1": 64, "p2": 32}, map[string]int{"p1": 2}),
			mulp,
			filter(0, 64, map[string]int{"frequency": 1}),
		}},
		{ID: 3, Name: "verb", Inputs: 1, Units: mc(1)},
	}
	lfo := unit("oscillator", 0, sointu.ParamMap{"transpose": 70, "detune": 64, "color": 128, "gain": 128, "type": sointu.Sine, "lfo": 1}, nil)
	send := func(target, amount int) sointu.Unit {
		return unit("send", 0, sointu.ParamMap{"amount": amount, "target": target, "port": 0, "sendpop": 1}, nil)
	}
	pan := unit("pan", 0, sointu.ParamMap{"panning": 40}, nil)
	sink := unit("out", 0, sointu.ParamMap{"stereo": 1, "gain": 128}, nil)
	if reverb {
		sink = unit("outaux", 0, sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 128}, nil)
	}
	song := func(patch sointu.Patch, buffers sointu.Buffers, modules sointu.Modules) sointu.Song {
		return sointu.Song{BPM: 120, RowsPerBeat: 4, Buffers: buffers, Modules: modules, Patch: patch,
			Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 2, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 67, 1, 0, 1, 55, 1, 1, 72, 1, 0, 1, 1, 1, 1}}}}},
		}
	}
	// the cutoff of the module is scaled onto 20 to 110: 40 gives the
	// filter 20 + 90·40/128 = 48, and the send of 32/64 becomes 23/64
	modules[1].Units[3].Bind["frequency"] = sointu.Binding{Param: 1, Scaled: true, Min: 20, Max: 110}
	a := []sointu.Unit{lfo, send(5, 96), moduleTestUnit("module", 5, sointu.ParamMap{"module": 2, "p1": 40, "p2": 70}, nil), pan, sink}
	b := []sointu.Unit{lfo, send(9, 87), envelope, osc(70, 32, nil), osc(50, 100, nil), addp, mulp, filter(9, 48, nil), pan, sink}
	patchA, patchB := sointu.Patch{{Name: "lead", NumVoices: 2, Units: a}}, sointu.Patch{{Name: "lead", NumVoices: 2, Units: b}}
	var buffersA, buffersB sointu.Buffers
	if reverb {
		in := unit("in", 0, sointu.ParamMap{"stereo": 1, "channel": 2}, nil)
		xch := unit("xch", 0, nil, nil)
		out := unit("out", 0, sointu.ParamMap{"stereo": 1, "gain": 128}, nil)
		verb := moduleTestUnit("module", 0, sointu.ParamMap{"module": 3}, nil)
		patchA = append(patchA, sointu.Instrument{Name: "reverb", NumVoices: 1, Units: []sointu.Unit{in, verb, xch, verb, out}})
		units := append(append(append([]sointu.Unit{in}, mc(1)...), xch), mc(2)...)
		patchB = append(patchB, sointu.Instrument{Name: "reverb", NumVoices: 1, Units: append(units, out)})
		buffersA = sointu.Buffers{{ID: 1, Name: "Bus 1", Channels: sointu.MCChannels, Bus: true, Auto: true}}
		buffersB = append(sointu.Buffers{}, buffersA[0], buffersA[0])
		buffersB[1].ID = 2
	}
	return song(patchA, buffersA, modules), song(patchB, buffersB, nil)
}

// withoutIDs returns the patch without the IDs of its units and with the
// targets of its sends as the indices of the targeted units, as the IDs of
// the units copied from modules are arbitrary.
func withoutIDs(patch sointu.Patch) sointu.Patch {
	patch = patch.Copy()
	index := map[int]int{}
	n := 0
	for _, instr := range patch {
		for _, u := range instr.Units {
			n++
			if u.ID != 0 {
				index[u.ID] = n
			}
		}
	}
	for _, instr := range patch {
		for i := range instr.Units {
			u := &instr.Units[i]
			u.ID = 0
			if u.Type == "send" {
				u.Parameters["target"] = index[u.Parameters["target"]]
			}
		}
	}
	return patch
}

// TestModulesExpandToHandWrittenSong checks that a song with modules expands
// to the same song written without them, and renders and compiles the same.
func TestModulesExpandToHandWrittenSong(t *testing.T) {
	for _, reverb := range []bool{false, true} {
		modular, plain := moduleTestSongs(reverb)
		expanded, expansion := modular.Expand()
		if len(expansion.Problems) > 0 {
			t.Fatalf("problems: %v", expansion.Problems)
		}
		if got, want := withoutIDs(expanded.Patch), withoutIDs(plain.Patch); !reflect.DeepEqual(got, want) {
			t.Errorf("reverb %v: the expanded patch is\n%v\nwant\n%v", reverb, got, want)
		}
		if !reflect.DeepEqual(expanded.Buffers, plain.Buffers) {
			t.Errorf("reverb %v: the expanded song has the buffers %v, want %v", reverb, expanded.Buffers, plain.Buffers)
		}
		want, err := sointu.Play(vm.GoSynther{}, plain, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := sointu.Play(vm.GoSynther{}, modular, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reverb %v: the song with modules renders differently in the Go synth", reverb)
		}
		archs := []string{"wasm", "386", "amd64"}
		if reverb {
			archs = archs[:1] // mc units are wasm only
		}
		for _, arch := range archs {
			com, err := compiler.New("linux", arch, false, false)
			if err != nil {
				t.Fatal(err)
			}
			a, _, err := com.Song(&modular)
			if err != nil {
				t.Fatalf("compiling the song with modules for %v: %v", arch, err)
			}
			b, _, err := com.Song(&plain)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("reverb %v: the song with modules compiles differently for %v", reverb, arch)
			}
		}
	}
}

func TestModulesWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, reverb := range []bool{false, true} {
		song, _ := moduleTestSongs(reverb)
		want, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("Go synth failed: %v", err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
	}
}

// TestModuleProblemsRefused checks that songs whose modules cannot be
// expanded as meant do not compile or play.
func TestModuleProblemsRefused(t *testing.T) {
	song, _ := moduleTestSongs(false)
	song.Modules[0].Units = append(song.Modules[0].Units, moduleTestUnit("module", 0, sointu.ParamMap{"module": 2}, nil)) // uses itself
	com, err := compiler.New("linux", "wasm", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := com.Song(&song); err == nil {
		t.Errorf("a song with a module using itself compiled")
	}
	if _, err := sointu.Play(vm.GoSynther{}, song, nil); err == nil {
		t.Errorf("a song with a module using itself played")
	}
	// the synths never see module units
	if _, err := vm.NewBytecode(song.Patch, vm.AllFeatures{}, 120); err == nil {
		t.Errorf("a patch with module units was encoded")
	}
}

// TestReverbModulePresetWasmMatchesGoSynth renders the Reverb module preset
// that the tracker comes with, fed by bursts of noise, in both synths: with
// the defaults of its parameters, and with all of them set.
func TestReverbModulePresetWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	data, err := os.ReadFile("../../tracker/modules/Reverb.yml")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Modules sointu.Modules }
	if err := yaml.Unmarshal(data, &file); err != nil || len(file.Modules) != 1 {
		t.Fatalf("reading the preset: %v, %d modules", err, len(file.Modules))
	}
	mod := file.Modules[0]
	for name, params := range map[string]sointu.ParamMap{
		"defaults": {"module": mod.ID},
		"all set":  {"module": mod.ID, "p1": 20, "p2": 70, "p3": 100, "p4": 40, "p5": 60, "p6": 80, "p7": 60, "p8": 20},
	} {
		t.Run(name, func(t *testing.T) {
			song := sointu.Song{BPM: 120, RowsPerBeat: 4,
				Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
					{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 0, 1, 1, 1, 64, 1, 0, 1, 1, 1, 1, 1}}},
					{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{make(sointu.Pattern, 16)}},
				}},
				Patch: sointu.Patch{{Name: "burst", NumVoices: 1, Units: []sointu.Unit{
					{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
					{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
					{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
					{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
					{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 2}},
				}}, {Name: "reverb", NumVoices: 1, Units: []sointu.Unit{
					{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}},
					{Type: "module", ID: 1000, Parameters: params},
					{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
				}}},
				Modules: file.Modules,
			}
			if _, x := song.Expand(); len(x.Problems) > 0 {
				t.Fatalf("problems expanding the song: %v", x.Problems)
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			silent := true
			for _, v := range want {
				silent = silent && v[0] == 0 && v[1] == 0
			}
			if silent {
				t.Fatal("the reverb is silent")
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}
