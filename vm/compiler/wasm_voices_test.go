package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// manyVoicesSong has 70 voices: a track and an instrument crossing voices 32
// and 64, local sends, global sends to single voices and to all voices of an
// instrument above voice 64, both before and after the target is defined, and
// a send without a target.
func manyVoicesSong() sointu.Song {
	pattern := func(first int) sointu.Pattern {
		p := make(sointu.Pattern, 64)
		for i := range p {
			p[i] = byte(first + (i*7)%40)
		}
		return p
	}
	return sointu.Song{
		BPM:         480,
		RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 64, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 36, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern(40)}},
			{NumVoices: 30, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern(50)}},
			{NumVoices: 4, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 61, 1, 62, 1, 63}}},
		}},
		Patch: sointu.Patch{
			{Name: "poly", NumVoices: 36, Units: []sointu.Unit{
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 80}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 70, "target": 20, "port": 0, "sendpop": 1}}, // local
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 16, "decay": 64, "sustain": 64, "release": 64, "gain": 128}},
				{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{ID: 20, Type: "gain", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 30, "voice": 3, "port": 0, "sendpop": 0}}, // one voice, above 64
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 80, "target": 30, "voice": 0, "port": 0, "sendpop": 0}}, // all voices
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 32}},
			}},
			{Name: "fill", NumVoices: 30, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 16, "decay": 64, "sustain": 64, "release": 64, "gain": 128}},
				{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": 20, "voice": 35, "port": 0, "sendpop": 0}}, // defined before, voice 34
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": 999, "port": 0, "sendpop": 0}},             // no target
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 32}},
			}},
			{Name: "receiver", NumVoices: 4, Units: []sointu.Unit{
				{ID: 30, Type: "receive", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
			}},
		},
	}
}

func TestManyVoicesWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	song := manyVoicesSong()
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

func TestManyVoicesX86Refused(t *testing.T) {
	t.Parallel()
	song := manyVoicesSong()
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("windows", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := com.Song(&song); err == nil || !strings.Contains(err.Error(), "voices") {
			t.Errorf("%v: compiling %v voices did not fail: %v", arch, song.Patch.NumVoices(), err)
		}
	}
}
