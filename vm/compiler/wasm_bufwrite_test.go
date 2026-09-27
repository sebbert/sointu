package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

func TestBufwriteWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// noise and envelopes only: they are identical in Go and wasm
	envelope := sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 40, "decay": 64, "sustain": 96, "release": 64, "gain": 128}}
	noise := sointu.Unit{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Buffers: sointu.Buffers{
			{ID: 1, Name: "recording", Channels: 1, Frames: 12000},
			{ID: 2, Name: "ring", Channels: 2, Frames: 3000},
		},
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 0, 1, 1, 1, 1}}},  // recorder
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1}}},  // ring writer
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 60, 1, 1, 72, 1, 1, 1}}}, // player
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 60, 1, 1, 1, 1, 0}}},  // spawner
		}},
		Patch: sointu.Patch{
			{Name: "recorder", NumVoices: 1, Units: []sointu.Unit{
				noise, envelope,
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "feedback": 0, "buffer": 1, "mode": sointu.BufwriteModeOnce}},
			}},
			{Name: "ring writer", NumVoices: 1, Units: []sointu.Unit{
				noise, envelope,
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 1, "feedback": 50, "buffer": 2, "mode": sointu.BufwriteModeRing}},
			}},
			{Name: "player", NumVoices: 1, Units: []sointu.Unit{
				// plays the recording while it is recorded, then loops it
				{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "notetracking": 1, "loop": 1, "loopstart": 2000, "looplength": 5000, "fade": 1000}},
				// and backwards in the loop, at 3/4 speed
				{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 16, "buffer": 1, "notetracking": 1, "start": 3000, "loop": 1, "loopstart": 2000, "looplength": 5000, "fade": 1000}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "spawner", NumVoices: 1, Units: []sointu.Unit{
				noise,
				{Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 100, "transpose": 64, "notetracking": 1, "args": 1, "instrument": 5}},
			}},
			{Name: "grains", NumVoices: 4, Units: []sointu.Unit{
				// grains from random positions of the ring buffer
				{Type: "arg", Parameters: sointu.ParamMap{"index": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 30, "port": 4, "sendpop": 1}},
				// backwards, counted from the write head, faded near it
				{ID: 30, Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 128, "speed": 20, "buffer": 2, "notetracking": 1, "start": -1500, "edgefade": 300}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))

	song.Patch[0].Units[3].Parameters["buffer"] = 3
	song.Buffers = append(song.Buffers, sointu.Buffer{ID: 3, Channels: 1, Sample: &sointu.AudioSample{}})
	com, err := compiler.New("", "wasm", false, false)
	if err != nil {
		t.Fatal(err)
	}
	com.Buffers = map[int]compiler.EncodedBuffer{3: {Frames: 1, Channels: 1}}
	if _, _, err := com.Song(&song); err == nil {
		t.Error("compiled a bufwrite unit writing to a buffer with a sample")
	}
}
