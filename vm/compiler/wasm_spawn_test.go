package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func TestSpawnWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// The modulations come from noise and constants, which are identical in
	// Go and wasm; the tiny differences of oscillators could move spawns by
	// a sample.
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 0, 1}}},
			{NumVoices: 4, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 1, 1, 1, 1}}}, // spawned only
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 72, 1, 1, 1, 1, 0}}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 48, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "rate spawner", NumVoices: 1, Units: []sointu.Unit{
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 72, "target": 10, "port": 1, "sendpop": 1}}, // random transpose
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{ID: 10, Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 90, "transpose": 64, "length": 30, "notetracking": 1, "args": 2, "steal": 1, "instrument": 2}},
			}},
			{Name: "grains", NumVoices: 4, Units: []sointu.Unit{
				// sustained, so that releasing a spawned voice would be heard
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 64, "release": 64, "gain": 128}},
				// no oscillators: they drift apart slightly between Go and
				// wasm over time
				{Type: "arg", Parameters: sointu.ParamMap{"index": 1}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "window", Parameters: sointu.ParamMap{"length": 0, "shape": 90}}, // from the note; open for edge spawns
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "arg", Parameters: sointu.ParamMap{"index": 0}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}, // the spawned note on the right
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
			{Name: "edge spawner", NumVoices: 1, Units: []sointu.Unit{
				// spawns into an instrument that comes earlier, so the
				// spawned voice starts on the next sample
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 112}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeEdge, "rate": 64, "transpose": 60, "notetracking": 0, "args": 1, "instrument": 2}},
			}},
			{Name: "sync spawner", NumVoices: 1, Units: []sointu.Unit{
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 100}},
				{Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeSync, "rate": 97, "transpose": 64, "length": 20, "notetracking": 1, "args": 1, "instrument": 2}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
