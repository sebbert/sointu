package compiler_test

import (
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm/compiler"
)

// automationTestSong has lanes bound to the frequency of a filter, to the
// transpose of the oscillator of a polyphonic instrument whose notes are
// triggered while the lane moves, and to a parameter of a module unit, and
// an automation unit placed by hand that moves the gain of an out through a
// send. With fine, the points are between the steps of the parameters; a
// lane has a step, one starts after the start of the song and one ends
// before its end.
func automationTestSong(fine bool) sointu.Song {
	f := func(v float64) float64 {
		if fine {
			return v + 0.37
		}
		return v
	}
	return sointu.Song{
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
		Modules: sointu.Modules{{ID: 1, Name: "tone", Inputs: 1, Params: []sointu.ModuleParam{{Name: "cutoff", Default: 64}}, Units: []sointu.Unit{
			{Type: "filter", ID: 40, Parameters: sointu.ParamMap{"stereo": 0, "frequency": 64, "resonance": 100, "lowpass": 1}, Bind: map[string]sointu.Binding{"frequency": {Param: 1}}},
		}}},
		Patch: sointu.Patch{
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 70, "sustain": 90, "release": 70, "gain": 128}},
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "filter", ID: 10, Parameters: sointu.ParamMap{"stereo": 0, "frequency": 40, "resonance": 60, "lowpass": 1}},
				{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 64}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
			{Name: "poly", NumVoices: 3, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 40, "decay": 70, "sustain": 64, "release": 72, "gain": 128}},
				{Type: "oscillator", ID: 20, Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "module", ID: 21, Parameters: sointu.ParamMap{"module": 1, "p1": 70}},
				{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 40}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
			{Name: "byhand", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 128, "release": 64, "gain": 128}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "automation", Parameters: sointu.ParamMap{"slot": 5}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 30, "port": 0, "sendpop": 1}},
				{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 90}},
				{Type: "out", ID: 30, Parameters: sointu.ParamMap{"stereo": 1, "gain": 32}},
			}},
		},
		Automation: sointu.Automation{
			{Slot: 0, Unit: 10, Param: "frequency", Points: sointu.AutomationCurve{{0, f(0)}, {8, f(50)}, {8, f(10)}, {20.5, f(-20)}, {31, f(30)}}},
			{Slot: 3, Unit: 20, Param: "transpose", Points: sointu.AutomationCurve{{2.25, f(0)}, {14, f(12)}, {24, f(-7)}}},
			{Slot: 4, Unit: 21, Param: "p1", Points: sointu.AutomationCurve{{0, f(-30)}, {32, f(40)}}},
			{Slot: 5, Points: sointu.AutomationCurve{{0, f(0)}, {4, f(64)}, {12, f(-10)}, {30, f(100)}}},
		},
	}
}

func TestAutomationWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, fine := range []bool{false, true} {
		song := automationTestSong(fine)
		want := playGo(t, song)
		compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		// the lanes are heard
		still := song
		still.Automation = nil
		plain := playGo(t, still)
		differ := 0
		for i := range want {
			if want[i] != plain[i] {
				differ++
			}
		}
		if differ < len(want)/2 {
			t.Errorf("fine %v: the automation changes %d of %d frames", fine, differ, len(want))
		}
	}
}

func compileWat(t *testing.T, arch string, song sointu.Song) (string, error) {
	t.Helper()
	com, err := compiler.New("linux", arch, false, false)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := com.Song(&song)
	if err != nil {
		return "", err
	}
	all := ""
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if arch != "wasm" || strings.HasSuffix(name, ".wat") {
			all += files[name]
		}
	}
	return all, nil
}

// TestAutomationOnlyWhenUsed checks that the table has 8 bits for a value
// when every value is a whole step and 16 otherwise, and that lanes that
// add nothing, bound or not, compile to the player of the song without
// them, for every target.
func TestAutomationOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	for _, fine := range []bool{false, true} {
		wat, err := compileWat(t, "wasm", automationTestSong(fine))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(wat, "$su_op_automation") {
			t.Errorf("fine %v: the player has no automation", fine)
		}
		if got := strings.Contains(wat, "i32.load16_s"); got != fine {
			t.Errorf("fine %v: the player reads values of 16 bits: %v", fine, got)
		}
		if got := strings.Contains(wat, "i32.load8_s"); got == fine {
			t.Errorf("fine %v: the player reads values of 8 bits: %v", fine, got)
		}
	}
	plain := automationTestSong(false)
	plain.Automation = nil
	plain.Patch[2].Units = append(plain.Patch[2].Units[:3:3], plain.Patch[2].Units[5:]...) // without the units placed by hand
	idle := plain
	idle.Automation = sointu.Automation{
		{Slot: 0, Unit: 10, Param: "frequency"},
		{Slot: 1, Unit: 20, Param: "transpose", Points: sointu.AutomationCurve{{0, 0}, {8, 0}}},
		{Slot: 2, Points: sointu.AutomationCurve{{0, 0.001}}},
	}
	for _, arch := range []string{"wasm", "386", "amd64"} {
		want, err := compileWat(t, arch, plain)
		if err != nil {
			if arch == "wasm" {
				t.Fatal(err)
			}
			continue // the song has a module with a filter only: x86 compiles it too
		}
		got, err := compileWat(t, arch, idle)
		if err != nil {
			t.Fatalf("%v: %v", arch, err)
		}
		if got != want {
			t.Errorf("%v: lanes that add nothing change the player", arch)
		}
		if strings.Contains(strings.ToLower(want), "automation") {
			t.Errorf("%v: a song without automation compiles to a player that mentions it", arch)
		}
	}
}

func TestAutomationX86Refused(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"386", "amd64"} {
		if _, err := compileWat(t, arch, automationTestSong(false)); err == nil || !strings.Contains(err.Error(), "only supported when compiling for wasm") {
			t.Errorf("compiling automation for %v: %v, want it refused", arch, err)
		}
	}
}
