package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// limiterTestSong has limiters on signals that rise above their thresholds
// and fall back: mono and stereo, in a polyphonic instrument, with lookahead
// from none to the longest, and with the threshold modulated. With drive,
// some of them have the gain before the limiter, one of them modulated.
func limiterTestSong(drive bool) sointu.Song {
	d := func(v int) int {
		if drive {
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
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 40, "target": 10, "port": 0}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{ID: 10, Type: "limiter", Parameters: sointu.ParamMap{"stereo": 0, "threshold": 40, "release": 60, "lookahead": 32, "drive": d(30)}},
				{Type: "limiter", Parameters: sointu.ParamMap{"stereo": 0, "threshold": 20, "release": 40, "lookahead": 0, "drive": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 50, "decay": 60, "sustain": 64, "release": 60, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 70, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 60}},
				{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{Type: "limiter", Parameters: sointu.ParamMap{"stereo": 1, "threshold": 30, "release": 70, "lookahead": 127, "drive": d(128)}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
			{Name: "stereo", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 40, "decay": 80, "sustain": 10, "release": 80, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
				{ID: 20, Type: "limiter", Parameters: sointu.ParamMap{"stereo": 1, "threshold": 128, "release": 128, "lookahead": 1, "drive": 0}},
				{Type: "limiter", Parameters: sointu.ParamMap{"stereo": 1, "threshold": 0, "release": 0, "lookahead": 64, "drive": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
		},
	}
	if drive { // a slow oscillator moves the drive of the first limiter of the last instrument
		u := &song.Patch[2].Units
		*u = append(*u,
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 70, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 20, "port": 2, "sendpop": 1}})
	}
	return song
}

func TestLimiterWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, drive := range []bool{false, true} {
		song := limiterTestSong(drive)
		want, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("Go synth failed: %v", err)
		}
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
	}
}

// TestLimiterDriveOnlyWhenUsed checks that the gain before the limiter is in
// the bytecode and the player only for songs that use it.
func TestLimiterDriveOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	for _, drive := range []bool{false, true} {
		song := limiterTestSong(drive)
		features := vm.NecessaryFeaturesFor(song.Patch)
		if got := vm.TransformsParam(features, "limiter", "drive"); got != drive {
			t.Errorf("drive %v: TransformsParam = %v", drive, got)
		}
		com, err := compiler.New("linux", "wasm", false, false)
		if err != nil {
			t.Fatal(err)
		}
		files, _, err := com.Song(&song)
		if err != nil {
			t.Fatalf("drive %v: compiling failed: %v", drive, err)
		}
		var wat string
		for name, content := range files {
			if strings.HasSuffix(name, ".wat") {
				wat = content
			}
		}
		if got := strings.Contains(wat, "$drive"); got != drive {
			t.Errorf("drive %v: the player has the drive: %v", drive, got)
		}
		if !strings.Contains(wat, "$su_op_limiter") {
			t.Errorf("drive %v: the player has no limiter", drive)
		}
	}
	// and a song without limiters has none of it
	song := limiterTestSong(false)
	for i := range song.Patch {
		units := song.Patch[i].Units[:0]
		for _, u := range song.Patch[i].Units {
			if u.Type != "limiter" && u.Type != "send" {
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
		if strings.HasSuffix(name, ".wat") && strings.Contains(strings.ToLower(content), "limiter") {
			t.Errorf("a song without limiters compiles to a player that mentions them")
		}
	}
}

func TestLimiterX86Refused(t *testing.T) {
	t.Parallel()
	song := limiterTestSong(false)
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err == nil {
			t.Errorf("compiling limiter for %v succeeded, want an error", arch)
		}
	}
}
