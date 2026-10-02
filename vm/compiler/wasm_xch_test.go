package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// xchSong is a song with four different signals on the stack, exchanged by
// the xch units of the case: then the top pair is made quieter and the top
// signal quieter still, so that every order of the four sounds different.
// With delay, the sum goes through a stereo delay, which calls the mono xch
// of the player.
func xchSong(mono, stereo, delay bool) sointu.Song {
	osc := func(transpose, typ int) sointu.Unit {
		return sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": transpose, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": typ}}
	}
	units := []sointu.Unit{osc(64, sointu.Sine), osc(71, sointu.Trisaw), osc(76, sointu.Sine), osc(83, sointu.Trisaw)}
	if mono {
		units = append(units, sointu.Unit{Type: "xch", Parameters: sointu.ParamMap{"stereo": 0}})
	}
	if stereo {
		units = append(units, sointu.Unit{Type: "xch", Parameters: sointu.ParamMap{"stereo": 1}})
	}
	if mono {
		units = append(units, sointu.Unit{Type: "xch", Parameters: sointu.ParamMap{"stereo": 0}})
	}
	units = append(units,
		sointu.Unit{Type: "gain", Parameters: sointu.ParamMap{"stereo": 1, "gain": 40}},
		sointu.Unit{Type: "gain", Parameters: sointu.ParamMap{"stereo": 0, "gain": 50}},
		sointu.Unit{Type: "addp", Parameters: sointu.ParamMap{"stereo": 1}},
	)
	if delay {
		units = append(units, sointu.Unit{Type: "delay", Parameters: sointu.ParamMap{"stereo": 1, "pregain": 80, "dry": 100, "feedback": 60, "damp": 40, "notetracking": 0}, VarArgs: []int{1116, 1188, 1277, 1356}})
	}
	units = append(units, sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 40}})
	return sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 0, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{{NumVoices: 1, Units: units}},
	}
}

// TestXchWasmMatchesGoSynth checks that the wasm player exchanges signals
// like the Go synth, whichever of mono xch, stereo xch and stereo delay the
// song has: a player with both a mono and a stereo xch did not assemble, and
// a stereo xch next to a stereo delay exchanged wrongly.
func TestXchWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	const pairs = ";; exchange the pairs"
	for _, c := range []struct {
		name                string
		mono, stereo, delay bool
	}{
		{"mono", true, false, false},
		{"stereo", false, true, false},
		{"mono and stereo", true, true, false},
		{"mono and delay", true, false, true},
		{"stereo and delay", false, true, true},
		{"mono, stereo and delay", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			song := xchSong(c.mono, c.stereo, c.delay)
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			files, _, err := com.Song(&song)
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(files[".wat"], pairs); has != c.stereo {
				t.Errorf("the player has the stereo xch: %v, the song: %v", has, c.stereo)
			}
			if has := strings.Contains(files[".wat"], "call $swap\n    call $push"); has != (c.mono || c.delay) {
				t.Errorf("the player has the mono xch: %v, the song needs it: %v", has, c.mono || c.delay)
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}
