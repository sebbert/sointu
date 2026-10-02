package compiler_test

import (
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
				{Type: "reverb", Parameters: sointu.ParamMap{"size": 20, "decay": 70, "highs": 100, "lows": 40, "predelay": 60, "mod": m(80), "highcut": 60, "lowcut": 20}},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 40}},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 40}},
			}},
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 64}},
				{ID: 10, Type: "reverb", Parameters: sointu.ParamMap{"size": 128, "decay": 0, "highs": 128, "lows": 128, "predelay": 128, "mod": 0, "highcut": 128, "lowcut": 0}},
				{Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1, "outgain": 64, "auxgain": 64}},
				// a slow oscillator moves the high cut and the low cut
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 70, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 30, "target": 10, "port": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 110, "target": 10, "port": 2, "sendpop": 1}},
			}},
			{Name: "global", NumVoices: 1, Units: []sointu.Unit{
				{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}},
				{Type: "limiter", Parameters: sointu.ParamMap{"stereo": 1, "threshold": 100, "release": 64, "lookahead": 32, "drive": 0}},
				{ID: 20, Type: "reverb", Parameters: sointu.ParamMap{"size": 64, "decay": 90, "highs": 48, "lows": 72, "predelay": 13, "mod": m(24), "highcut": 98, "lowcut": 56}},
				{Type: "reverb", Parameters: sointu.ParamMap{"size": 0, "decay": 30, "highs": 0, "lows": 0, "predelay": 0, "mod": 0, "highcut": 0, "lowcut": 128}},
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
