package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// shapingTestSong has softclip, width and ladder units on loud signals: mono
// and stereo, in a polyphonic instrument, with their parameters modulated.
// With optional, some of them use the parts that songs can do without: the
// softclip at twice the sample rate, the lowcut of width and the drive of
// ladder, one of each modulated where that is possible.
func shapingTestSong(optional bool) sointu.Song {
	o := func(v int) int {
		if optional {
			return v
		}
		return 0
	}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 0, 1, 64, 1, 1, 1, 1, 1, 1, 0}}},
			{NumVoices: 3, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{
				{48, 1, 55, 1, 60, 1, 0, 1, 1, 1, 67, 1, 0, 1, 1, 1},
				{36, 72, 1, 0, 1, 50, 1, 1, 0, 1, 62, 1, 1, 1, 0, 1},
			}},
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 70, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 60, "decay": 70, "sustain": 20, "release": 70, "gain": 128}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": 10, "port": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 30, "target": 11, "port": 1}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 52, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 40}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{ID: 10, Type: "ladder", Parameters: sointu.ParamMap{"stereo": 0, "frequency": 20, "resonance": 100, "drive": o(40)}},
				{Type: "ladder", Parameters: sointu.ParamMap{"stereo": 0, "frequency": 128, "resonance": 128, "drive": 0}},
				{ID: 11, Type: "softclip", Parameters: sointu.ParamMap{"stereo": 0, "drive": 60, "knee": 64, "oversample": o(1)}},
				{Type: "softclip", Parameters: sointu.ParamMap{"stereo": 0, "drive": 0, "knee": 128, "oversample": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 50, "decay": 60, "sustain": 64, "release": 60, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 70, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 60}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "ladder", Parameters: sointu.ParamMap{"stereo": 1, "frequency": 50, "resonance": 128, "drive": o(128)}},
				{Type: "softclip", Parameters: sointu.ParamMap{"stereo": 1, "drive": 128, "knee": 0, "oversample": o(1)}},
				{Type: "width", Parameters: sointu.ParamMap{"width": 128, "lowcut": o(30)}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
			{Name: "stereo", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 40, "decay": 80, "sustain": 10, "release": 80, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "softclip", Parameters: sointu.ParamMap{"stereo": 1, "drive": 20, "knee": 100, "oversample": 0}},
				{ID: 20, Type: "ladder", Parameters: sointu.ParamMap{"stereo": 1, "frequency": 90, "resonance": 0, "drive": 0}},
				{ID: 21, Type: "width", Parameters: sointu.ParamMap{"width": 20, "lowcut": 0}},
				{Type: "width", Parameters: sointu.ParamMap{"width": 0, "lowcut": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
				// a slow oscillator moves the frequency of the ladder and the width
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 70, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 110, "target": 20, "port": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 21, "port": 0, "sendpop": 1}},
			}},
		},
	}
	if optional { // and the drive of the ladder and the lowcut of the width
		u := &song.Patch[2].Units
		*u = append(*u,
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 76, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 20, "port": 2}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 80, "target": 21, "port": 1, "sendpop": 1}})
	}
	return song
}

func TestShapingWasmMatchesGoSynth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, optional := range []bool{false, true} {
		song := shapingTestSong(optional)
		want, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("Go synth failed: %v", err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
	}
}

// TestShapingOptionalOnlyWhenUsed checks that the oversampling of softclip,
// the lowcut of width and the drive of ladder are in the bytecode and the
// player only for songs that use them.
func TestShapingOptionalOnlyWhenUsed(t *testing.T) {
	for _, optional := range []bool{false, true} {
		song := shapingTestSong(optional)
		features := vm.NecessaryFeaturesFor(song.Patch)
		if got := vm.TransformsParam(features, "width", "lowcut"); got != optional {
			t.Errorf("optional %v: TransformsParam of lowcut = %v", optional, got)
		}
		if got := vm.TransformsParam(features, "ladder", "drive"); got != optional {
			t.Errorf("optional %v: TransformsParam of drive = %v", optional, got)
		}
		com, err := compiler.New("linux", "wasm", false, false)
		if err != nil {
			t.Fatal(err)
		}
		files, _, err := com.Song(&song)
		if err != nil {
			t.Fatalf("optional %v: compiling failed: %v", optional, err)
		}
		var wat string
		for name, content := range files {
			if strings.HasSuffix(name, ".wat") {
				wat = content
			}
		}
		for _, s := range []string{"$allpass", "$softclipOver", "(local $freq2 f32) (local $low f32)", "(f32.const 7) (call $input (i32.const 2))"} {
			if got := strings.Contains(wat, s); got != optional {
				t.Errorf("optional %v: the player has %q: %v", optional, s, got)
			}
		}
		for _, s := range []string{"$su_op_softclip", "$su_op_width", "$su_op_ladder"} {
			if !strings.Contains(wat, s) {
				t.Errorf("optional %v: the player has no %v", optional, s)
			}
		}
	}
	// and a song without the units has none of it
	song := shapingTestSong(false)
	for i := range song.Patch {
		units := song.Patch[i].Units[:0]
		for _, u := range song.Patch[i].Units {
			if u.Type != "softclip" && u.Type != "width" && u.Type != "ladder" && u.Type != "send" {
				units = append(units, u)
			}
		}
		song.Patch[i].Units = units
	}
	com, _ := compiler.New("linux", "wasm", false, false)
	files, _, err := com.Song(&song)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if !strings.HasSuffix(name, ".wat") {
			continue
		}
		for _, s := range []string{"softclip", "su_op_width", "ladder", "allpass"} {
			if strings.Contains(strings.ToLower(content), s) {
				t.Errorf("a song without the units compiles to a player that mentions %v", s)
			}
		}
	}
}

func TestShapingX86Refused(t *testing.T) {
	for _, unit := range []string{"softclip", "width", "ladder"} {
		song := shapingTestSong(false)
		for i := range song.Patch {
			units := song.Patch[i].Units[:0]
			for _, u := range song.Patch[i].Units {
				if u.Type == unit || u.Type != "softclip" && u.Type != "width" && u.Type != "ladder" && u.Type != "send" {
					units = append(units, u)
				}
			}
			song.Patch[i].Units = units
		}
		for _, arch := range []string{"386", "amd64"} {
			com, err := compiler.New("linux", arch, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := com.Song(&song); err == nil {
				t.Errorf("compiling %v for %v succeeded, want an error", unit, arch)
			}
		}
	}
}
