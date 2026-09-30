package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// TestStereoPushWasmMatchesGoSynth uses the copies of a stereo push: the
// left and right signals differ, so copying the top twice instead of the pair
// would change the output.
func TestStereoPushWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{64, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0}}},
		}},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 76, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 64, "type": sointu.Trisaw}},
			{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 100}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
		}}},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
