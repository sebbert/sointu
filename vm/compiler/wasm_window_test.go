package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// Windows timed by pitch, with transpose and a modulated detune, and by
// tempo, next to one timed in frames, in the wasm player and the Go synth.
func TestWindowTimingWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	type M = sointu.ParamMap
	song := func(windows ...M) sointu.Song {
		s := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 8, Length: 1}}
		for i, w := range windows {
			units := []sointu.Unit{
				fu("loadval", M{"value": 100}),
				{ID: 100 + i*10, Type: "send", Parameters: M{"stereo": 0, "amount": 80, "target": 101 + i*10, "port": 3, "sendpop": 1}}, // detune
				{ID: 101 + i*10, Type: "window", Parameters: w},
				fu("loadnote", nil),
				fu("out", M{"stereo": 1, "gain": 128}),
			}
			s.Patch = append(s.Patch, sointu.Instrument{NumVoices: 2, Units: units})
			s.Score.Tracks = append(s.Score.Tracks, sointu.Track{NumVoices: 2, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60 + byte(i), 1, 72, 0, 50, 1, 1, 84}}})
		}
		return s
	}
	pitch := M{"timing": 1, "time": 3 * 10788, "shape": 40, "transpose": 64, "detune": 64}
	transposed := M{"timing": 1, "time": 3596, "shape": 0, "transpose": 76, "detune": 70}
	bpm := M{"timing": 2, "time": 9, "shape": 128, "transpose": 64, "detune": 64}
	frames := M{"timing": 0, "length": 20, "time": 10788, "shape": 64, "transpose": 64, "detune": 64}
	for name, s := range map[string]sointu.Song{
		"all":   song(pitch, transposed, bpm, frames),
		"pitch": song(pitch, transposed),
		"bpm":   song(bpm, frames),
	} {
		want, err := sointu.Play(vm.GoSynther{}, s, nil)
		if err != nil {
			t.Fatalf("%s: Go synth failed: %v", name, err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, s, nil, nil, nil))
	}
}
