package compiler_test

import (
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

// eqTestSongs returns a song with an eq unit and the same song with the
// units of the eq written by hand. The instrument, with two voices, is a
// saw through a stereo eq with a band of each kind (and one switched off),
// and a gain of -3 dB. With ladder, the eq also has a ladder high cut, which
// only the Go synth and the wasm player have.
func eqTestSongs(ladder bool) (withEQ, byHand sointu.Song) {
	unit := func(typ string, params sointu.ParamMap) sointu.Unit {
		u := sointu.MakeUnit(typ)
		for k, v := range params {
			u.Parameters[k] = v
		}
		return u
	}
	stereo := func(typ string, params sointu.ParamMap) sointu.Unit {
		params["stereo"] = 1
		return sointu.Unit{Type: typ, Parameters: params}
	}
	eq := sointu.Unit{Type: "eq", Parameters: sointu.ParamMap{"stereo": 1, "gain": -30}, Bands: []sointu.EQBand{
		{Type: sointu.EQLowCut, Frequency: 80, Q: 1},
		{Type: sointu.EQBell, Frequency: 1000, Gain: 6, Q: 1},
		{Type: sointu.EQHighCut, Frequency: 2000, Q: 0.71},
		{Type: sointu.EQLowShelf, Frequency: 120, Gain: 6, Q: 1},
		{Type: sointu.EQNotch, Frequency: 500, Q: 4},
		{Type: sointu.EQBell, Frequency: 3000, Gain: 9, Q: 1, Disabled: true},
		{Type: sointu.EQLowCut24, Frequency: 30, Q: 0.71},
		{Type: sointu.EQHighShelf, Frequency: 3000, Gain: -4, Q: 1},
	}}
	units := []sointu.Unit{
		// low cut: a filter with Q 1
		stereo("filter", sointu.ParamMap{"frequency": 14, "resonance": 128, "lowpass": 0, "bandpass": 0, "highpass": 1}),
		// bell
		stereo("belleq", sointu.ParamMap{"frequency": 34, "bandwidth": 32, "gain": 74}),
		// high cut with Q 0.71: a filter with Q 1 and a bell that damps it
		stereo("filter", sointu.ParamMap{"frequency": 68, "resonance": 128, "lowpass": 1, "bandpass": 0, "highpass": 0}),
		stereo("belleq", sointu.ParamMap{"frequency": 48, "bandwidth": 38, "gain": 59}),
		// low shelf: the signal plus its lows
		stereo("push", sointu.ParamMap{}),
		stereo("filter", sointu.ParamMap{"frequency": 13, "resonance": 128, "lowpass": 1, "bandpass": 1, "highpass": 0}),
		stereo("gain", sointu.ParamMap{"gain": 126}),
		stereo("addp", sointu.ParamMap{}),
		// notch
		stereo("filter", sointu.ParamMap{"frequency": 34, "resonance": 32, "lowpass": 1, "bandpass": 0, "highpass": 1}),
		// low cut of 24 dB per octave: two filters, a bell for the Q of the first
		stereo("filter", sointu.ParamMap{"frequency": 8, "resonance": 128, "lowpass": 0, "bandpass": 0, "highpass": 1}),
		stereo("belleq", sointu.ParamMap{"frequency": 6, "bandwidth": 43, "gain": 56}),
		stereo("filter", sointu.ParamMap{"frequency": 8, "resonance": 98, "lowpass": 0, "bandpass": 0, "highpass": 1}),
		// high shelf that lowers: the lows raised, and the gain at the end
		stereo("push", sointu.ParamMap{}),
		stereo("filter", sointu.ParamMap{"frequency": 65, "resonance": 128, "lowpass": 1, "bandpass": 1, "highpass": 0}),
		stereo("gain", sointu.ParamMap{"gain": 95}),
		stereo("addp", sointu.ParamMap{}),
	}
	if ladder {
		eq.Bands = append(eq.Bands, sointu.EQBand{Type: sointu.EQLadder, Frequency: 8000, Q: 0.71})
		units = append(units,
			stereo("ladder", sointu.ParamMap{"frequency": 92, "resonance": 7, "drive": 0}),
			stereo("gain", sointu.ParamMap{"gain": 57})) // -3 dB, the shelf, and what the resonance of the ladder takes
	} else {
		units = append(units, stereo("gain", sointu.ParamMap{"gain": 52})) // -3 dB and the shelf
	}
	before := []sointu.Unit{
		unit("envelope", sointu.ParamMap{"attack": 40, "decay": 70, "sustain": 80, "release": 70, "gain": 128}),
		unit("oscillator", sointu.ParamMap{"transpose": 64, "detune": 64, "color": 128, "gain": 128, "type": sointu.Trisaw}),
		unit("mulp", nil),
		unit("pan", sointu.ParamMap{"panning": 40}),
	}
	out := unit("out", sointu.ParamMap{"stereo": 1, "gain": 64})
	song := func(units ...sointu.Unit) sointu.Song {
		return sointu.Song{BPM: 120, RowsPerBeat: 4, Patch: sointu.Patch{{Name: "lead", NumVoices: 2, Units: append(append([]sointu.Unit{}, before...), units...)}},
			Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 2, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{48, 1, 67, 1, 0, 1, 31, 1, 1, 84, 1, 0, 1, 1, 1, 1}}}}},
		}
	}
	return song(eq, out), song(append(units, out)...)
}

// TestEQExpandsToHandWrittenSong checks that a song with an eq unit expands
// to the same song written with the units by hand, and renders and compiles
// the same: for wasm, and without a ladder, for 386 and amd64.
func TestEQExpandsToHandWrittenSong(t *testing.T) {
	t.Parallel()
	for _, ladder := range []bool{false, true} {
		withEQ, plain := eqTestSongs(ladder)
		expanded, expansion := withEQ.Expand()
		if len(expansion.Problems) > 0 {
			t.Fatalf("problems: %v", expansion.Problems)
		}
		if got, want := expanded.Patch, plain.Patch; !reflect.DeepEqual(got, want) {
			t.Errorf("ladder %v: the expanded patch is\n%v\nwant\n%v", ladder, got, want)
		}
		want, err := sointu.Play(vm.GoSynther{}, plain, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := sointu.Play(vm.GoSynther{}, withEQ, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ladder %v: the song with the eq renders differently in the Go synth", ladder)
		}
		for _, arch := range []string{"wasm", "386", "amd64"} {
			com, err := compiler.New("linux", arch, false, false)
			if err != nil {
				t.Fatal(err)
			}
			a, _, err := com.Song(&withEQ)
			if ladder && arch != "wasm" {
				if err == nil || !strings.Contains(err.Error(), "ladder") {
					t.Errorf("compiling an eq with a ladder for %v: %v", arch, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("compiling the song with the eq for %v: %v", arch, err)
			}
			b, _, err := com.Song(&plain)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("ladder %v: the song with the eq compiles differently for %v", ladder, arch)
			}
		}
	}
	// and with the progressive player, its JavaScript module and stages
	withEQ, plain := eqTestSongs(true)
	for _, option := range []func(*compiler.Compiler){
		func(c *compiler.Compiler) { c.Progressive = true },
		func(c *compiler.Compiler) { c.JS = true },
		func(c *compiler.Compiler) { c.Progressive, c.Stages = true, 2 },
	} {
		var out [2]map[string]string
		for i, song := range []*sointu.Song{&withEQ, &plain} {
			com, err := compiler.New("linux", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			option(com)
			if out[i], _, err = com.Song(song); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(out[0], out[1]) || len(out[0]) == 0 {
			t.Errorf("with an option of the progressive player, the song with the eq compiles differently")
		}
	}
	// an eq that does nothing compiles like no eq at all
	withEQ, _ = eqTestSongs(false)
	units := withEQ.Patch[0].Units
	eq := &units[len(units)-2]
	for i := range eq.Bands {
		eq.Bands[i].Disabled = true
	}
	eq.Parameters["gain"] = 0
	without := withEQ
	without.Patch = sointu.Patch{withEQ.Patch[0]}
	without.Patch[0].Units = append(append([]sointu.Unit{}, units[:len(units)-2]...), units[len(units)-1])
	for _, arch := range []string{"wasm", "386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		a, _, err := com.Song(&withEQ)
		if err != nil {
			t.Fatal(err)
		}
		b, _, err := com.Song(&without)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("a song with an eq that does nothing compiles differently for %v", arch)
		}
	}
}

func TestEQWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, ladder := range []bool{false, true} {
		song, _ := eqTestSongs(ladder)
		want, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("Go synth failed: %v", err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
	}
}

// TestEQExample checks that examples/eq.yml has eq units that do something,
// renders, compiles for wasm, 386 and amd64, and that the wasm player
// renders it like the Go synth.
func TestEQExample(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../examples/eq.yml")
	if err != nil {
		t.Fatal(err)
	}
	var song sointu.Song
	if err := yaml.Unmarshal(data, &song); err != nil {
		t.Fatal(err)
	}
	eqs := 0
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "eq" {
				eqs++
				if n := u.NumEQUnits(); n < len(u.Bands)-1 {
					t.Errorf("the eq of %s stands for %d units", instr.Name, n)
				}
			}
		}
	}
	if eqs != 3 {
		t.Fatalf("the example has %d eq units", eqs)
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	clipped := 0
	for _, s := range want {
		sum += float64(s[0])*float64(s[0]) + float64(s[1])*float64(s[1])
		if max(s[0], -s[0], s[1], -s[1]) >= 1 {
			clipped++
		}
	}
	rms := math.Sqrt(sum / float64(2*len(want)))
	t.Logf("RMS level %.3f, %d of %d samples clipped", rms, clipped, len(want))
	if rms < 0.05 || rms > 0.5 || math.IsNaN(rms) || clipped > len(want)/1000 {
		t.Errorf("the example renders with the RMS level %v, %d of %d samples clipped", rms, clipped, len(want))
	}
	for _, arch := range []string{"wasm", "386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err != nil {
			t.Errorf("compiling the example for %v: %v", arch, err)
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
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
