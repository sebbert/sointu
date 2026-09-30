package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

func TestOttWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// mono ott with depth modulated by an envelope, two otts in one
	// instrument, a polyphonic stereo ott and the parameters at their ends,
	// on signals that rise and fall through the thresholds
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 0, 1, 64, 1, 1, 1, 1, 1, 1, 0}}},
			{NumVoices: 3, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{
				{48, 1, 55, 1, 60, 1, 0, 1, 1, 1, 67, 1, 0, 1, 1, 1},
				{36, 72, 1, 0, 1, 50, 1, 1, 0, 1, 62, 1, 1, 1, 0, 1},
			}},
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 70, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 70, "decay": 70, "sustain": 20, "release": 70, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 32, "target": 10, "port": 0}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{ID: 10, Type: "ott", Parameters: sointu.ParamMap{"stereo": 0, "depth": 96, "time": 40, "upward": 128, "downward": 128, "low": 91, "mid": 79, "high": 91}},
				{Type: "ott", Parameters: sointu.ParamMap{"stereo": 0, "depth": 128, "time": 0, "upward": 50, "downward": 0, "low": 0, "mid": 128, "high": 64}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
			}},
			{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 50, "decay": 60, "sustain": 64, "release": 60, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 70, "phase": 0, "color": 64, "shape": 64, "gain": 100, "type": sointu.Trisaw}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 30}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "ott", Parameters: sointu.ParamMap{"stereo": 1, "depth": 128, "time": 90, "upward": 100, "downward": 60, "low": 70, "mid": 60, "high": 100}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
			{Name: "stereo ends", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 40, "decay": 80, "sustain": 10, "release": 80, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "ott", Parameters: sointu.ParamMap{"stereo": 1, "depth": 64, "time": 128, "upward": 128, "downward": 128, "low": 128, "mid": 0, "high": 64}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestOttX86Refused(t *testing.T) {
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 1, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60}}},
		}},
		Patch: sointu.Patch{{Name: "ott", NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}},
			{Type: "ott", Parameters: sointu.ParamMap{"stereo": 0, "depth": 128, "time": 64, "upward": 128, "downward": 128, "low": 64, "mid": 64, "high": 64}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
		}}},
	}
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err == nil {
			t.Errorf("compiling ott for %v succeeded, want an error", arch)
		}
	}
}
