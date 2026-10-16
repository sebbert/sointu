package compiler_test

import (
	"math"
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
)

// TestBufreadSlicesWasmMatchesGoSynth plays slices of a sample: by the slice
// parameter, by the note, by the note with its pitch, modulated by a loadnote, stopping at the next
// marker with an edge fade, and looping in parts of the slice, also in a
// voice of an instrument with a buffer per voice.
func TestBufreadSlicesWasmMatchesGoSynth(t *testing.T) {
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
	for _, tc := range []struct {
		name  string
		units []sointu.Unit
	}{
		{"slice", []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1, "slices": 1, "slice": 2, "start": 300}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
		{"note stop", []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 128, "speed": 100, "buffer": 2, "note": 2, "slices": 2, "edgefade": 200}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
		}},
		{"loadnote loop", []sointu.Unit{
			{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 100, "port": 7, "sendpop": 1}},
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 40, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 70, "target": 100, "port": 6, "sendpop": 1}},
			{ID: 100, Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1, "slices": 1, "slice": 3,
				"mode": 1, "loopstart": 32, "looplength": 64, "loopfade": 100, "timing": 2}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
		{"slice and pitch", []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 66, "detune": 70, "gain": 128, "speed": 128, "buffer": 1, "note": 3, "slices": 2, "slice": 1, "edgefade": 30}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
		{"backwards stop", []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 20, "buffer": 2, "note": 0, "slices": 2, "slice": 1, "start": 2000, "edgefade": 50}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
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

func slicesTestSong(units []sointu.Unit) sointu.Song {
	saw := func(i, c int) float64 { return math.Mod(float64(i+c*3)*0.01, 1) - 0.5 + 0.3*math.Sin(float64(i)*0.003) }
	flac := &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Buffers: sointu.Buffers{
			{ID: 1, Name: "mono", Channels: 1, Slices: []int{3000, 7000, 12000, 15000}, Sample: &sointu.AudioSample{Data: testWav(1, 20000, saw), Encoding: flac}},
			{ID: 2, Name: "stereo", Channels: 2, Slices: []int{5000, 9000}, Sample: &sointu.AudioSample{Data: testWav(2, 16000, saw), Encoding: flac}},
		},
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 61, 1, 66, 0, 58, 1}}},
		}},
		Patch: sointu.Patch{{Name: "sliced", NumVoices: 1, Units: units}},
	}
}

// onlyPlayedBuffers keeps the buffers that a bufread unit of the song plays,
// as the runner wants a custom section for each buffer.
func onlyPlayedBuffers(song sointu.Song) sointu.Song {
	played := map[int]bool{}
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "bufread" {
				played[u.Parameters["buffer"]] = true
			}
		}
	}
	var bufs sointu.Buffers
	for _, b := range song.Buffers {
		if played[b.ID] {
			bufs = append(bufs, b)
		}
	}
	song.Buffers = bufs
	return song
}
