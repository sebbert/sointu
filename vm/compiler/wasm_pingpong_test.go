package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
)

// TestBufreadPingPongWasmMatchesGoSynth plays ping-pong loops: at a speed
// with fractions, backwards and in stereo, in a slice with modulated loop
// points, from a negative loop start, of one frame, and next to a forward
// loop with a crossfade.
func TestBufreadPingPongWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	ff, err := ffmpeg.Find("")
	if err != nil {
		t.Skipf("ffmpeg not found: %v", err)
	}
	bufread := func(id int, p sointu.ParamMap) sointu.Unit {
		q := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1, "mode": 2}
		for k, v := range p {
			q[k] = v
		}
		return sointu.Unit{ID: id, Type: "bufread", Parameters: q}
	}
	out := func(stereo int) sointu.Unit {
		return sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": stereo, "gain": 128}}
	}
	lfo := func(transpose int) sointu.Unit {
		return sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": transpose, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}}
	}
	send := func(port, amount int) sointu.Unit {
		return sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": amount, "target": 100, "port": port, "sendpop": 1}}
	}
	for _, tc := range []struct {
		name  string
		units []sointu.Unit
	}{
		{"fractions", []sointu.Unit{bufread(0, sointu.ParamMap{"speed": 101, "loopstart": 1000, "looplength": 3001, "edgefade": 100}), out(0)}},
		{"backwards stereo", []sointu.Unit{bufread(0, sointu.ParamMap{"stereo": 1, "buffer": 2, "speed": 10, "start": 6000, "loopstart": 2000, "looplength": 5000}), out(1)}},
		{"slice modulated", []sointu.Unit{
			lfo(30), send(5, 80),
			lfo(45), send(6, 50),
			bufread(100, sointu.ParamMap{"slices": 2, "slice": 2, "edgefade": 40, "loopstart": 30, "looplength": 70, "speed": 120}), out(0),
		}},
		{"negative loop start", []sointu.Unit{bufread(0, sointu.ParamMap{"loopstart": -4000, "looplength": 2500, "transpose": 70, "timing": 1}), out(0)}},
		{"one frame", []sointu.Unit{bufread(0, sointu.ParamMap{"loopstart": 500, "looplength": 1, "speed": 90}), out(0)}},
		{"next to forward", []sointu.Unit{
			bufread(0, sointu.ParamMap{"mode": 1, "loopstart": 1000, "looplength": 2000, "loopfade": 300, "speed": 30}),
			bufread(0, sointu.ParamMap{"loopstart": 1500, "looplength": 2000, "speed": 115}),
			{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
			out(0),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			song := onlyPlayedBuffers(slicesTestSong(tc.units))
			encoded, audio, runnerArgs, channels := encodeTestBuffers(t, ff, song)
			want, err := sointu.PlayWithBuffers(vm.GoSynther{}, song, audio, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			got := renderWasm(t, node, wat2wasm, song, encoded, runnerArgs, channels)
			compareWasmToGo(t, want, got)
		})
	}
}
