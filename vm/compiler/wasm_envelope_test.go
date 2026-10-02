package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// envelopeCurveSong has envelopes of several curves, linear ones among them,
// with notes long enough to reach the sustain and notes released during the
// attack and the decay, stereo and polyphonic envelopes, and envelopes whose
// attack, decay, sustain, release and curve are modulated.
func envelopeCurveSong() sointu.Song {
	env := func(stereo, attack, decay, sustain, release, curve int) sointu.Unit {
		return sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": stereo, "attack": attack, "decay": decay, "sustain": sustain, "release": release, "gain": 128, "curve": curve}}
	}
	out := func(stereo int) sointu.Unit {
		return sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": stereo, "gain": 32}}
	}
	// short notes: released in the attack (attack 72 is 1.3 rows), long
	// notes: through the decay to the sustain, medium notes: released in
	// the decay
	pattern := sointu.Pattern{60, 0, 0, 0, 61, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 62, 1, 1, 0, 0, 0, 0, 0}
	var tracks []sointu.Track
	var patch sointu.Patch
	for i, curve := range []int{0, 1, 16, 48, 64, 100, 128} {
		tracks = append(tracks, sointu.Track{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern}})
		patch = append(patch, sointu.Instrument{Name: "curve", NumVoices: 1, Units: []sointu.Unit{
			env(i%2, 72, 70-i, 40+8*i, 70, curve),
			out(i % 2),
		}})
	}
	// three voices, overlapping notes
	tracks = append(tracks, sointu.Track{NumVoices: 3, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{50, 52, 54, 1, 1, 0, 56, 0, 58, 1, 60, 1, 1, 1, 0, 0, 0, 0, 62, 0, 0, 0, 0, 0}}})
	patch = append(patch, sointu.Instrument{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
		env(1, 60, 66, 64, 72, 90),
		out(1),
	}})
	// modulated attack, decay, sustain, release and curve, from LFOs
	lfo := func(transpose, target, port int) []sointu.Unit {
		return []sointu.Unit{
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": transpose, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": target, "port": port, "sendpop": 1}},
		}
	}
	var mod []sointu.Unit
	for port := 0; port < 6; port++ {
		if port == 4 { // gain
			continue
		}
		mod = append(mod, lfo(40+port*5, 10, port)...)
	}
	mod = append(mod, sointu.Unit{ID: 10, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 70, "decay": 70, "sustain": 64, "release": 72, "gain": 128, "curve": 80}}, out(0))
	for port := 0; port < 6; port++ {
		if port == 4 {
			continue
		}
		mod = append(mod, lfo(30+port*7, 11, port)...)
	}
	mod = append(mod, sointu.Unit{ID: 11, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 68, "decay": 72, "sustain": 80, "release": 70, "gain": 128, "curve": 0}}, out(1))
	tracks = append(tracks, sointu.Track{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern}})
	patch = append(patch, sointu.Instrument{Name: "modulated", NumVoices: 1, Units: mod})
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score:       sointu.Score{RowsPerPattern: len(pattern), Length: 1, Tracks: tracks},
		Patch:       patch,
	}
}

func TestEnvelopeCurveWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	song := envelopeCurveSong()
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

// TestEnvelopeCurveOnlyWhenUsed checks that the players get the curve only
// when a song curves or modulates it, and that x86 refuses those songs.
func TestEnvelopeCurveOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	linear := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 64, "decay": 64, "sustain": 64, "release": 64, "gain": 64, "curve": 0}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
	}}}
	curved := linear.Copy()
	curved[0].Units[0].Parameters["curve"] = 1
	modulated := linear.Copy()
	modulated[0].Units[0].ID = 1
	modulated[0].Units = append(modulated[0].Units,
		sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
		sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 64, "target": 1, "port": 5, "sendpop": 1}})
	for _, c := range []struct {
		name  string
		patch sointu.Patch
		curve bool
	}{{"linear", linear, false}, {"curved", curved, true}, {"modulated", modulated, true}} {
		features := vm.NecessaryFeaturesFor(c.patch)
		if got := vm.TransformsParam(features, "envelope", "curve"); got != c.curve {
			t.Errorf("%v: TransformsParam = %v, want %v", c.name, got, c.curve)
		}
		bytecode, err := vm.NewBytecode(c.patch, features, 120)
		if err != nil {
			t.Fatal(err)
		}
		// the envelope's operands, then the gain of out
		want := []byte{64, 64, 64, 64, 64, 64}
		if c.curve {
			want = []byte{64, 64, 64, 64, 64, byte(c.patch[0].Units[0].Parameters["curve"]), 64}
		}
		if got := bytecode.Operands[:len(want)]; string(got) != string(want) {
			t.Errorf("%v: operands start with %v, want %v", c.name, got, want)
		}
		song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 1, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60}}}}}, Patch: c.patch}
		wasm, err := compiler.New("", "wasm", false, false)
		if err != nil {
			t.Fatal(err)
		}
		code, _, err := wasm.Song(&song)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(code[".wat"], "$envelopeStep"); got != c.curve {
			t.Errorf("%v: wasm player has the curved envelope: %v, want %v", c.name, got, c.curve)
		}
		for _, arch := range []string{"386", "amd64"} {
			com, err := compiler.New("windows", arch, false, false)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = com.Song(&song)
			if c.curve && (err == nil || !strings.Contains(err.Error(), "envelope")) {
				t.Errorf("%v: %v did not refuse the curved envelope: %v", c.name, arch, err)
			}
			if !c.curve && err != nil {
				t.Errorf("%v: %v failed: %v", c.name, arch, err)
			}
		}
	}
}
