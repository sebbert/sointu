package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

func mcTestUnit(typ string, bus int, params sointu.ParamMap) sointu.Unit {
	u := sointu.MakeUnit(typ)
	u.Parameters["bus"] = bus
	for k, v := range params {
		u.Parameters[k] = v
	}
	return u
}

// mcTestSong is a song whose instruments play notes into mc units: a
// polyphonic instrument whose units run only in its first voice, spreading
// into bus 1 and adding into it (it has no dry output: the wasm player's
// stereo push duplicates the left channel twice, unlike the Go synth's and
// x86's), and a hall-like reverb on bus 1 (diffuser
// with shuffles and Hadamard mixes, a feedback delay network with a modulated
// allpass stage and a Householder mix), whose feedback, gain, width, moddepth
// and modrate are modulated by an envelope; a note-tracked, filtered resonator
// on bus 2; and mono spreads and sums.
func mcTestSong() sointu.Song {
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 3, Tracks: []sointu.Track{
			{NumVoices: 2, Order: sointu.Order{0, 1, 2}, Patterns: []sointu.Pattern{
				{48, 1, 55, 1, 0, 1, 60, 1, 1, 1, 67, 1, 0, 1, 1, 1},
				{36, 1, 1, 0, 50, 1, 1, 1, 62, 1, 1, 1, 0, 1, 1, 1},
				{0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			}},
			{NumVoices: 1, Order: sointu.Order{0, 0, 0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
			{NumVoices: 1, Order: sointu.Order{0, 1, 0}, Patterns: []sointu.Pattern{{40, 1, 1, 1, 0, 1, 1, 1, 52, 1, 1, 1, 0, 1, 1, 1}, {45, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "poly source", NumVoices: 2, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 10, "decay": 60, "sustain": 20, "release": 50, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 70, "phase": 0, "color": 90, "shape": 64, "gain": 100, "type": sointu.Trisaw}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				mcTestUnit("mcspread", 1, sointu.ParamMap{"stereo": 1, "gain": 70}),
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 10}},
				mcTestUnit("mcspread", 1, sointu.ParamMap{"stereo": 0, "add": 1, "gain": 40}),
			}},
			{Name: "hall", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 80, "decay": 90, "sustain": 64, "release": 70, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 70, "target": 101, "port": 0, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 40, "target": 102, "port": 0, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 90, "target": 103, "port": 0, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 50, "target": 103, "port": 1, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 80, "target": 104, "port": 1, "sendpop": 1}},
				mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 300, "spread": 100, "seed": 1}),
				mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixShuffle, "seed": 1}),
				mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixHadamard}),
				mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 150, "spread": 100, "seed": 2}),
				mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixShuffle, "seed": 2}),
				mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixHadamard}),
				{ID: 101, Type: "mcsum", Parameters: sointu.ParamMap{"stereo": 1, "gain": 50, "width": 80, "bus": 1}},
				{ID: 102, Type: "mcloop", Parameters: sointu.ParamMap{"feedback": 100, "bus": 1}},
				mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 60, "spread": 50, "seed": 5, "allpass": 1, "apgain": 80, "moddepth": 10, "modrate": 70}),
				{ID: 103, Type: "mcdelay", Parameters: sointu.ParamMap{"size": 900, "spread": 60, "seed": 3, "decay": 70, "hfdecay": 40, "lfdecay": 90, "moddepth": 20, "modrate": 60, "bus": 1}},
				{ID: 104, Type: "mcsum", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64, "width": 64, "bus": 1}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixHouseholder}),
				mcTestUnit("mcloopend", 1, nil),
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 100}},
			}},
			{Name: "resonator", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 30, "sustain": 0, "release": 30, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 60, "decay": 70, "sustain": 40, "release": 60, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 30, "target": 201, "port": 0, "sendpop": 1}},
				mcTestUnit("mcspread", 2, sointu.ParamMap{"stereo": 0, "gain": 64}),
				mcTestUnit("mcloop", 2, sointu.ParamMap{"feedback": 128}),
				mcTestUnit("mcdelay", 2, sointu.ParamMap{"size": 76, "spread": 0, "notetracking": 1, "decay": 60, "hfdecay": 64}),
				{ID: 201, Type: "mcfilter", Parameters: sointu.ParamMap{"frequency": 110, "type": 0, "bus": 2}},
				mcTestUnit("mcfilter", 2, sointu.ParamMap{"frequency": 20, "type": 1}),
				mcTestUnit("mcmix", 2, sointu.ParamMap{"type": sointu.MCMixHouseholder}),
				mcTestUnit("mcloopend", 2, nil),
				mcTestUnit("mcsum", 2, sointu.ParamMap{"stereo": 0, "gain": 80}),
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
			}},
		},
	}
}

func TestMCWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	song := mcTestSong()
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

// TestMCWasmSubsets compiles and compares songs that each use only some of
// the code of the mc units, whose other parts the player leaves out.
func TestMCWasmSubsets(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	source := []sointu.Unit{
		{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 30, "decay": 70, "sustain": 20, "release": 60, "gain": 128}},
		{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 40, "shape": 64, "gain": 128, "type": sointu.Pulse}},
		{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
	}
	subsets := map[string][]sointu.Unit{
		"mono spread and sum": {
			mcTestUnit("mcspread", 1, nil),
			mcTestUnit("mcsum", 1, sointu.ParamMap{"stereo": 0}),
		},
		"hadamard": {
			mcTestUnit("mcspread", 1, nil),
			mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 100}),
			mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixHadamard}),
			mcTestUnit("mcsum", 1, nil),
		},
		"shuffle loop": {
			mcTestUnit("mcspread", 1, nil),
			mcTestUnit("mcloop", 1, sointu.ParamMap{"feedback": 120}),
			mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 200, "decay": 50}),
			mcTestUnit("mcmix", 1, sointu.ParamMap{"type": sointu.MCMixShuffle, "seed": 9}),
			mcTestUnit("mcloopend", 1, nil),
			mcTestUnit("mcsum", 1, nil),
		},
		"allpass only": {
			mcTestUnit("mcspread", 1, nil),
			mcTestUnit("mcdelay", 1, sointu.ParamMap{"size": 30, "allpass": 1, "apgain": 100}),
			mcTestUnit("mcfilter", 1, sointu.ParamMap{"type": 1, "frequency": 40}),
			mcTestUnit("mcsum", 1, nil),
		},
	}
	for name, units := range subsets {
		t.Run(name, func(t *testing.T) {
			song := sointu.Song{BPM: 120, RowsPerBeat: 4,
				Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 0, 1, 67, 1, 1, 0, 1, 1, 1, 1, 1, 1}}}}},
				Patch: sointu.Patch{{Name: name, NumVoices: 1, Units: append(append(append([]sointu.Unit{}, source...), units...),
					sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": units[len(units)-1].Parameters["stereo"], "gain": 128}})}},
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}

func TestMCX86Refused(t *testing.T) {
	for _, typ := range []string{"mcspread", "mcsum", "mcdelay", "mcmix", "mcloop", "mcloopend", "mcfilter"} {
		units := []sointu.Unit{mcTestUnit(typ, 1, nil)}
		switch sointu.UnitTypes[typ].StackUse(&units[0]).NumOutputs - len(sointu.UnitTypes[typ].StackUse(&units[0]).Inputs) {
		case -1:
			units = append([]sointu.Unit{{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}}}, units...)
		case 1:
			units = append(units, sointu.Unit{Type: "pop", Parameters: sointu.ParamMap{"stereo": 0}})
		}
		song := sointu.Song{BPM: 120, RowsPerBeat: 4,
			Score: sointu.Score{RowsPerPattern: 1, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60}}}}},
			Patch: sointu.Patch{{Name: typ, NumVoices: 1, Units: units}},
		}
		for _, arch := range []string{"386", "amd64"} {
			com, err := compiler.New("linux", arch, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := com.Song(&song); err == nil {
				t.Errorf("compiling %v for %v succeeded, want an error", typ, arch)
			}
		}
	}
}

// TestMCPresetsWasmMatchGoSynth renders the reverb presets made of mc units,
// fed by a burst of noise, in both synths.
func TestMCPresetsWasmMatchGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	files, err := filepath.Glob("../../tracker/presets/UTIL/Reverb_FDN_*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no presets found: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var reverb sointu.Instrument
			if err := yaml.Unmarshal(data, &reverb); err != nil {
				t.Fatal(err)
			}
			reverb.NumVoices = 1
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
				}}, reverb},
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}
