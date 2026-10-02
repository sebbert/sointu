package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// bandlimitSong has bandlimited pulse, trisaw and sine oscillators: trisaws
// near color 0 and 1, unison, stereo, frequency and phase modulation (also
// running the phase backwards) and high notes, next to naive, gate and LFO
// oscillators that ignore bandlimit.
func bandlimitSong() sointu.Song {
	osc := func(id, typ, color, bandlimit int, extra sointu.ParamMap) sointu.Unit {
		p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": color, "shape": 64, "gain": 32, "type": typ, "bandlimit": bandlimit}
		for k, v := range extra {
			p[k] = v
		}
		return sointu.Unit{ID: id, Type: "oscillator", Parameters: p}
	}
	env := sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 16, "decay": 64, "sustain": 96, "release": 64, "gain": 128}}
	mulp := sointu.Unit{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}}
	addp := sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}}
	out := sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}}
	notes := func(n ...byte) sointu.Pattern {
		p := make(sointu.Pattern, 16)
		for i := range p {
			p[i] = 1
		}
		for i, v := range n {
			p[4*i] = v
		}
		return p
	}
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{notes(100, 120, 60, 127)}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{notes(110, 90, 127, 72)}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{notes(96, 115, 84, 124)}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{notes(105, 80, 122, 100)}},
		}},
		Patch: sointu.Patch{
			{Name: "pulse and sine", NumVoices: 1, Units: []sointu.Unit{
				env,
				osc(0, sointu.Pulse, 32, 1, nil),
				osc(0, sointu.Pulse, 0, 1, sointu.ParamMap{"transpose": 76}),
				addp,
				osc(0, sointu.Pulse, 128, 1, sointu.ParamMap{"transpose": 52, "phase": 40}),
				addp,
				osc(0, sointu.Sine, 40, 1, nil),
				addp,
				osc(0, sointu.Sine, 3, 1, sointu.ParamMap{"transpose": 60}),
				addp,
				osc(0, sointu.Sine, 128, 1, sointu.ParamMap{"transpose": 70}),
				addp,
				osc(0, sointu.Trisaw, 64, 0, nil), // naive
				addp,
				mulp,
				out,
			}},
			{Name: "trisaws", NumVoices: 1, Units: []sointu.Unit{
				env,
				osc(0, sointu.Trisaw, 0, 1, nil),
				osc(0, sointu.Trisaw, 1, 1, sointu.ParamMap{"transpose": 71}),
				addp,
				osc(0, sointu.Trisaw, 2, 1, sointu.ParamMap{"transpose": 57}),
				addp,
				osc(0, sointu.Trisaw, 64, 1, sointu.ParamMap{"transpose": 76}),
				addp,
				osc(0, sointu.Trisaw, 126, 1, sointu.ParamMap{"transpose": 52}),
				addp,
				osc(0, sointu.Trisaw, 127, 1, sointu.ParamMap{"transpose": 67}),
				addp,
				osc(0, sointu.Trisaw, 128, 1, sointu.ParamMap{"transpose": 88}),
				addp,
				mulp,
				out,
			}},
			{Name: "unison stereo", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 16, "decay": 64, "sustain": 96, "release": 64, "gain": 128}},
				osc(0, sointu.Pulse, 50, 1, sointu.ParamMap{"stereo": 1, "unison": 3, "detune": 80}),
				osc(0, sointu.Trisaw, 20, 1, sointu.ParamMap{"stereo": 1, "unison": 2, "detune": 40, "phase": 30}),
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				osc(0, sointu.Sine, 90, 1, sointu.ParamMap{"stereo": 1, "unison": 1, "detune": 100, "transpose": 76}),
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				osc(0, sointu.Gate, 85, 0, sointu.ParamMap{"stereo": 1, "unison": 1, "lfo": 1, "transpose": 40}), // a gate, bits in color and shape
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
			{Name: "modulation", NumVoices: 1, Units: []sointu.Unit{
				// an LFO with bandlimit, which is ignored, modulates the
				// frequency of a trisaw, strongly enough to run it backwards
				osc(0, sointu.Sine, 128, 1, sointu.ParamMap{"lfo": 1, "transpose": 90, "gain": 128}),
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 128, "target": 40, "port": 6, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": 41, "port": 3, "sendpop": 1}}, // color of the pulse
				// an audio-rate oscillator modulates the phase of a pulse
				osc(0, sointu.Sine, 128, 0, sointu.ParamMap{"transpose": 71, "gain": 128}),
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 110, "target": 41, "port": 2, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 90, "target": 42, "port": 2, "sendpop": 1}},
				// noise modulates the frequency of a sine with color < 1
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 70, "target": 42, "port": 6, "sendpop": 1}},
				env,
				osc(40, sointu.Trisaw, 10, 1, nil),
				osc(41, sointu.Pulse, 64, 1, sointu.ParamMap{"transpose": 52}),
				addp,
				osc(42, sointu.Sine, 60, 1, sointu.ParamMap{"transpose": 58, "unison": 1, "shape": 90}),
				addp,
				mulp,
				out,
			}},
		},
	}
}

func TestBandlimitWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	song := bandlimitSong()
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestBandlimitX86Refused(t *testing.T) {
	t.Parallel()
	song := bandlimitSong()
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("windows", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err == nil || !strings.Contains(err.Error(), "bandlimited") {
			t.Errorf("%v: compiling bandlimited oscillators did not fail: %v", arch, err)
		}
	}
	// bandlimit is ignored for LFOs, so it does not need the wasm player
	lfo := sointu.Song{BPM: 120, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 1, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60}}}}},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "color": 64, "shape": 64, "gain": 64, "type": sointu.Pulse, "lfo": 1, "bandlimit": 1}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
		}}},
	}
	com, err := compiler.New("windows", "amd64", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := com.Song(&lfo); err != nil {
		t.Errorf("an LFO with bandlimit was refused: %v", err)
	}
}
