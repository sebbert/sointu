package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// velocitySong has an oscillator times a mono velocity unit on the left and
// times a stereo one on both sides: notes of a song have velocity 1, in the
// Go synth and in the compiled players.
func velocitySong() sointu.Song {
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 0, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
			{Type: "velocity", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 40}},
			{Type: "velocity", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
		}}},
	}
}

// The wasm player pushes 1 for a velocity unit, as the Go synth does for
// the notes of a song; the x86 players compile it too.
func TestVelocityWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	song := velocitySong()
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err != nil {
			t.Errorf("%s: %v", arch, err)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
