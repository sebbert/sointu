package compiler_test

import (
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func TestSpectralWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// Noise is the same in Go and wasm. Two chains of different sizes, one
	// through a copy, and a second voice that does not run the spectral
	// units.
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 2, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 62, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "spectral", NumVoices: 2, Units: []sointu.Unit{
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 0, "buffer": 1}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 3, "buffer": 2}},
				{Type: "spcopy", Parameters: sointu.ParamMap{"source": 2, "buffer": 3}},
				{Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
				{Type: "spifft", Parameters: sointu.ParamMap{"gain": 100, "buffer": 3}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestSpectralModifiersWasmMatchGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// the envelope of the note freezes spblur while it is held
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 60, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "spectral", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 0, "sustain": 128, "release": 0, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 128, "target": 10, "port": 1, "sendpop": 1}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 1, "buffer": 1}},
				{Type: "spfilter", Parameters: sointu.ParamMap{"low": 40, "high": 110, "tilt": 80, "buffer": 1}},
				{Type: "spcompress", Parameters: sointu.ParamMap{"amount": 110, "width": 30, "buffer": 1}},
				{ID: 10, Type: "spblur", Parameters: sointu.ParamMap{"amount": 90, "freeze": 0, "buffer": 1}},
				{Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestSpectralModifiers2WasmMatchGoSynth(t *testing.T) {
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
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "spectral", NumVoices: 1, Units: []sointu.Unit{
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 1, "buffer": 1}},
				{Type: "spgate", Parameters: sointu.ParamMap{"threshold": 70, "invert": 0, "buffer": 1}},
				{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseDisperse, "amount": 40, "buffer": 1}},
				{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseRandom, "amount": 30, "buffer": 1}},
				{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseRobot, "amount": 50, "buffer": 1}},
				{Type: "spscale", Parameters: sointu.ParamMap{"scale": 80, "shift": 50, "buffer": 1}},
				{Type: "spformant", Parameters: sointu.ParamMap{"shift": 90, "width": 20, "buffer": 1}},
				{Type: "spformant", Parameters: sointu.ParamMap{"shift": 30, "width": 10, "buffer": 1}},
				{Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestSpectralCombineWasmMatchGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// spcomb takes the chord held in the second instrument while it is held,
	// and the note of its own voice with intervals after it is released
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{48, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
			{NumVoices: 3, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 64, 67, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "fx", NumVoices: 1, Units: []sointu.Unit{
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 2, "buffer": 1}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 20, "gain": 128}},
				{Type: "spfft", Parameters: sointu.ParamMap{"size": 2, "buffer": 2}},
				{Type: "spcross", Parameters: sointu.ParamMap{"amount": 100, "width": 20, "source": 2, "buffer": 1}},
				{Type: "spcomb", Parameters: sointu.ParamMap{"q": 50, "amount": 110, "instrument": 2, "interval1": 3, "interval2": 7, "interval3": 10, "buffer": 1}},
				{Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "chord", NumVoices: 3, Units: []sointu.Unit{
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 0}},
			}},
		},
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
