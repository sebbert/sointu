package compiler_test

import (
	"fmt"
	"maps"
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// reverbTestSong has reverb units with different settings: one in each voice
// of a polyphonic instrument, one on a mono signal made stereo, with its low
// cut and high cut modulated, and one on the aux signals of both, after
// another unit that keeps its state outside the voices. With mod, the lines
// of some are modulated, and the mod of one by an oscillator.
func reverbTestSong(mod bool) sointu.Song {
	m := func(v int) int {
		if mod {
			return v
		}
		return 0
	}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{
			{NumVoices: 3, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{
				{48, 1, 55, 1, 60, 1, 0, 1, 1, 1, 67, 1, 0, 1, 1, 1},
				{36, 72, 1, 0, 1, 50, 1, 1, 0, 1, 62, 1, 1, 1, 0, 1},
			}},
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{60, 1, 0, 1, 1, 1, 0, 1, 64, 0, 1, 1, 1, 1, 1, 0}}},
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 20, "decay": 60, "sustain": 30, "release": 60, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 70, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "reverb", Parameters: reverbParams(sointu.ParamMap{"size": 20, "decay": 70, "highs": 100, "lows": 40, "predelay": 60, "mod": m(80), "highcut": 60, "lowcut": 20})},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 40}},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 40}},
			}},
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 64}},
				{ID: 10, Type: "reverb", Parameters: reverbParams(sointu.ParamMap{"size": 128, "decay": 0, "highs": 128, "lows": 128, "predelay": 128, "mod": 0, "highcut": 128, "lowcut": 0})},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 64}},
				// a slow oscillator moves the high cut and the low cut
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 70, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 30, "target": 10, "port": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 110, "target": 10, "port": 2, "sendpop": 1}},
			}},
			{Name: "global", NumVoices: 1, Units: []sointu.Unit{
				{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}},
				{Type: "limiter", Parameters: sointu.ParamMap{"stereo": 1, "threshold": 100, "release": 64, "lookahead": 32, "drive": 0}},
				{ID: 20, Type: "reverb", Parameters: reverbParams(sointu.ParamMap{"size": 64, "decay": 90, "highs": 48, "lows": 72, "predelay": 13, "mod": m(24), "highcut": 98, "lowcut": 56})},
				{Type: "reverb", Parameters: reverbParams(sointu.ParamMap{"size": 0, "decay": 30, "highs": 0, "lows": 0, "predelay": 0, "mod": 0, "highcut": 0, "lowcut": 128})},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
		},
	}
	if mod { // far beyond its range, where the lengths are clamped
		u := &song.Patch[2].Units
		*u = append(*u,
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 76, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			sointu.Unit{Type: "gain", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 128, "target": 20, "port": 0, "sendpop": 1}})
	}
	return song
}

// reverbParams returns the parameters of a reverb unit: those given, and the
// others as in the Reverb module.
func reverbParams(params sointu.ParamMap) sointu.ParamMap {
	p := sointu.ParamMap{}
	maps.Copy(p, sointu.AddedParameters("reverb"))
	maps.Copy(p, params)
	return p
}

func TestReverbWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, mod := range []bool{false, true} {
		song := reverbTestSong(mod)
		want, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("Go synth failed: %v", err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
	}
}

// TestReverbModOnlyWhenUsed checks that the modulation of the lines is in the
// player only for songs that use it, and that a song without the unit has
// nothing of it.
func TestReverbModOnlyWhenUsed(t *testing.T) {
	compile := func(song sointu.Song) string {
		com, err := compiler.New("linux", "wasm", false, false)
		if err != nil {
			t.Fatal(err)
		}
		files, _, err := com.Song(&song)
		if err != nil {
			t.Fatalf("compiling failed: %v", err)
		}
		for name, content := range files {
			if strings.HasSuffix(name, ".wat") {
				return content
			}
		}
		t.Fatal("no .wat")
		return ""
	}
	for _, mod := range []bool{false, true} {
		wat := compile(reverbTestSong(mod))
		for _, s := range []string{"(local.set $depth", "(f32.const 16382)"} {
			if got := strings.Contains(wat, s); got != mod {
				t.Errorf("mod %v: the player has %q: %v", mod, s, got)
			}
		}
		if !strings.Contains(wat, "$su_op_reverb") {
			t.Errorf("mod %v: the player has no $su_op_reverb", mod)
		}
	}
	song := reverbTestSong(false)
	for i := range song.Patch {
		units := song.Patch[i].Units[:0]
		for _, u := range song.Patch[i].Units {
			if u.Type != "reverb" && u.Type != "send" {
				units = append(units, u)
			}
		}
		song.Patch[i].Units = units
	}
	if wat := compile(song); strings.Contains(strings.ToLower(wat), "reverb") {
		t.Errorf("a song without the unit compiles to a player that mentions reverb")
	}
}

func TestReverbX86Refused(t *testing.T) {
	song := reverbTestSong(false)
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err == nil {
			t.Errorf("compiling the reverb unit for %v succeeded, want an error", arch)
		}
	}
}

// TestReverbPartsOnlyWhenUsed checks, for songs whose reverb units set the
// parameters that the Reverb module fixes, that the player has the code and
// the data of what they use and nothing else, and renders them like the Go
// synth. The first song has the defaults, and so the plain player.
func TestReverbPartsOnlyWhenUsed(t *testing.T) {
	node, _ := exec.LookPath("node")
	wat2wasm, _ := exec.LookPath("wat2wasm")
	const (
		levels   = "(f32.load offset=192 (local.get $k))"
		lowcut   = "(f32.load offset=8 (local.get $q))"
		highcut  = "(f32.load offset=16 (local.get $q))"
		coef     = "(func $reverbCoef"
		switches = "(if (i32.eqz (i32.and (i32.load8_u offset="
		allpass  = ";; a tap of an allpass"
		plain    = ";; a tap: how far behind frame t it reads"
		unitAP   = "(local $ap i32)" // tells a unit with allpasses from one without
		line     = "(func $reverbLine"
	)
	ringAfter := "fewer steps, the ring starts where the one after the last step would.\n    (local.set $r (i32.add (i32.add"
	plate := sointu.ParamMap{"gain": 85, "early": 40, "earlywidth": 64, "tailwidth": 64, "modrate": 64, "steps": 2, "spread": 64, "diffuser": 90, "network": 1100, "bypass": 5, "highcut": 91, "decay": 98, "highs": 80, "lows": 56, "mod": 10,
		"allpass": 96, "loopsize": 300, "loopgain": 64, "loopmod": 12, "looprate": 70}
	room := sointu.ParamMap{"gain": 74, "early": 58, "earlywidth": 64, "tailwidth": 64, "modrate": 64, "steps": 3, "spread": 64, "diffuser": 200, "network": 400, "bypass": 7, "decay": 58, "highs": 48, "lows": 64, "mod": 8}
	for _, c := range []struct {
		name    string
		units   []sointu.ParamMap // set on top of the defaults of the Reverb module
		record  int               // bytes of constants for each unit
		has     []string
		hasNot  []string
		reverbs int // sets of constants
	}{
		{"defaults", []sointu.ParamMap{{}}, 192, []string{lowcut, highcut, coef, "(f32.const 2.3713737)", "(f32.const 1.25) (f32.const 0.4216965)", "(f32.const 1.5)", "(i32.const 128)"}, []string{levels, ringAfter, switches, allpass, unitAP, line}, 1},
		{"gain", []sointu.ParamMap{{"gain": 60}}, 212, []string{levels, "offset=196", "offset=200", "offset=204", "offset=208", lowcut, highcut}, []string{ringAfter, switches, "(f32.const 2.3713737)"}, 1},
		{"widths", []sointu.ParamMap{{}, {"earlywidth": 64, "tailwidth": 64, "size": 30}}, 212, []string{levels}, []string{ringAfter, switches}, 2},
		{"modrate", []sointu.ParamMap{{"modrate": 90, "mod": 60}}, 212, []string{levels}, []string{"(f32.const 1.603417e-05)"}, 1},
		{"steps", []sointu.ParamMap{{"steps": 2}, {"steps": 1, "size": 100}, {}}, 193, []string{ringAfter, "(i32.load8_u offset=192 (local.get $k))"}, []string{levels, "(i32.const 0x2000)"}, 3},
		{"sizes", []sointu.ParamMap{{"network": 900, "diffuser": 333, "pretime": 777, "spread": 20}}, 192, []string{lowcut, highcut}, []string{levels, ringAfter, switches}, 1},
		{"no predelay", []sointu.ParamMap{{"bypass": sointu.ReverbBypassPredelay}}, 192, []string{lowcut, highcut}, []string{levels, ringAfter, switches}, 1},
		{"no low cut", []sointu.ParamMap{{"bypass": sointu.ReverbBypassLowcut}}, 192, []string{highcut, coef}, []string{lowcut, switches}, 1},
		{"no high cut", []sointu.ParamMap{{"bypass": sointu.ReverbBypassHighcut}, {"bypass": sointu.ReverbBypassHighcut, "size": 10}}, 192, []string{lowcut, coef}, []string{highcut, switches}, 2},
		{"no filters", []sointu.ParamMap{{"bypass": 3}}, 192, nil, []string{lowcut, highcut, coef, switches}, 1},
		{"filters in some", []sointu.ParamMap{{"bypass": 3}, {}, {"bypass": 1, "size": 90}, {"bypass": 2, "size": 20}}, 193, []string{lowcut, highcut, coef, "(i32.load8_u offset=192 (local.get $k)) (i32.const 1)", "(i32.load8_u offset=192 (local.get $k)) (i32.const 2)"}, []string{levels}, 4},
		{"low cut in some", []sointu.ParamMap{{"bypass": 2}, {"bypass": 3}}, 193, []string{lowcut, "(i32.const 1)))"}, []string{highcut}, 2},
		{"room", []sointu.ParamMap{room}, 213, []string{levels, ringAfter, "(i32.load8_u offset=212 (local.get $k))"}, []string{lowcut, highcut, coef}, 1},
		{"allpass", []sointu.ParamMap{{"allpass": 96}, {"allpass": 20, "predelay": 128, "size": 128}}, 200, []string{allpass, "(local $g f32)", "(f32.store offset=131232", "(f32.const 2.3713737)"}, []string{plain, unitAP, line}, 2},
		{"allpass in some", []sointu.ParamMap{{"allpass": 96}, {}, {"allpass": 40, "size": 20, "bypass": sointu.ReverbBypassPredelay}}, 200, []string{allpass, plain, unitAP, "(f32.const 2.3713737)"}, []string{line}, 3},
		{"second lines", []sointu.ParamMap{{"loopsize": 300, "loopgain": 64, "loopmod": 12, "looprate": 70}}, 332, []string{line, plain}, []string{allpass, levels}, 1},
		{"second lines in some, no mod", []sointu.ParamMap{{"loopsize": 2800, "loopmod": 128, "looprate": 128, "mod": 0}, {"mod": 0, "size": 20}, {"loopsize": 1, "mod": 0, "size": 100}}, 332, []string{line, "(f32.const 0) (f32.const 1.603417e-05) (f32.const 0)"}, []string{allpass, "(local.set $depth"}, 3},
		{"plate", []sointu.ParamMap{plate}, 361, []string{allpass, line, highcut, levels, "(i32.load8_u offset=360 (local.get $k))"}, []string{plain, unitAP, lowcut, switches}, 1},
		{"everything", []sointu.ParamMap{room, {}, {"bypass": 1, "steps": 2, "gain": 128, "early": 0, "mod": 0}, plate}, 362, []string{levels, ringAfter, lowcut, highcut, allpass, plain, unitAP, line, "(i32.load8_u offset=360 (local.get $k))", "(i32.load8_u offset=361 (local.get $k))"}, nil, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			units := []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 0, "decay": 50, "sustain": 20, "release": 50, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
			}
			for i, set := range c.units {
				p := reverbParams(sointu.ParamMap{"size": 64, "decay": 90, "highs": 48, "lows": 72, "predelay": 13, "mod": 24, "highcut": 98, "lowcut": 56})
				maps.Copy(p, set)
				if i > 0 { // each in parallel, on a copy of the signal
					units = append(units, sointu.Unit{Type: "xch", Parameters: sointu.ParamMap{"stereo": 1}})
				}
				if i+1 < len(c.units) {
					units = append(units, sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}})
				}
				units = append(units, sointu.Unit{Type: "reverb", Parameters: p})
				if i > 0 {
					units = append(units, sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}})
				}
			}
			units = append(units, sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}})
			song := sointu.Song{BPM: 120, RowsPerBeat: 4,
				Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
					{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 0, 1, 1, 1, 64, 0, 1, 1, 1, 1, 1, 1, 1, 1}}},
				}},
				Patch: sointu.Patch{{Name: "reverbs", NumVoices: 1, Units: units}},
			}
			b, err := vm.NewBytecode(song.Patch, vm.NecessaryFeaturesFor(song.Patch), song.BPM)
			if err != nil {
				t.Fatal(err)
			}
			if len(b.Reverbs) != c.reverbs {
				t.Fatalf("the song has %d sets of constants, want %d", len(b.Reverbs), c.reverbs)
			}
			com, err := compiler.New("linux", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			files, _, err := com.Song(&song)
			if err != nil {
				t.Fatalf("compiling failed: %v", err)
			}
			wat := files[".wat"]
			if record := fmt.Sprintf("(i32.mul (call $scanOperand) (i32.const %d))", c.record); !strings.Contains(wat, record) {
				t.Errorf("the player does not have %q", record)
			}
			for _, s := range c.has {
				if !strings.Contains(wat, s) {
					t.Errorf("the player does not have %q", s)
				}
			}
			for _, s := range c.hasNot {
				if strings.Contains(wat, s) {
					t.Errorf("the player has %q", s)
				}
			}
			if node == "" || wat2wasm == "" {
				t.Skip("node or wat2wasm not found: not rendered")
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}
