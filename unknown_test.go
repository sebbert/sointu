package sointu_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// unknownSong returns a song with units of a type that Sointu does not
// have: one in its second instrument, and one in a module that it uses. A
// disabled one is not played, and no matter.
func unknownSong() sointu.Song {
	osc := unit("oscillator", 1, map[string]int{"gain": 64})
	out := unit("out", 2, map[string]int{"stereo": 0, "gain": 64})
	future := sointu.Unit{Type: "futureunit", ID: 3, Parameters: sointu.ParamMap{"amount": 5}}
	disabled := sointu.Unit{Type: "otherfuture", ID: 4, Parameters: sointu.ParamMap{}, Disabled: true}
	return sointu.Song{BPM: 100, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 4, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1}}}}},
		Modules: sointu.Modules{{ID: 7, Name: "tone", Inputs: 1, Units: []sointu.Unit{
			unit("gain", 5, map[string]int{"gain": 64}), {Type: "futurefilter", ID: 6, Parameters: sointu.ParamMap{}}}}},
		Patch: sointu.Patch{
			{Name: "plain", NumVoices: 1, Units: []sointu.Unit{osc, out}},
			{Name: "lead", NumVoices: 1, Units: []sointu.Unit{osc, future, disabled, unit("module", 8, map[string]int{"module": 7}), out}},
		}}
}

func TestUnknownUnits(t *testing.T) {
	song := unknownSong()
	unknown := song.UnknownUnits()
	if len(unknown) != 2 || unknown[0] != (sointu.UnknownUnit{Instrument: 1, Name: "lead", Unit: 1, Type: "futureunit"}) ||
		unknown[1] != (sointu.UnknownUnit{Module: 7, Name: "tone", Unit: 1, Type: "futurefilter"}) {
		t.Fatalf("the unknown units are %+v", unknown)
	}
	err := song.CheckUnitTypes()
	for _, want := range []string{`unit 1 of instrument 1 / lead has the unknown type "futureunit"`, `unit 1 of module 7 / tone has the unknown type "futurefilter"`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, without %q", err, want)
		}
	}
	// playing it is that error
	_, err = sointu.Play(vm.GoSynther{}, song, nil)
	if err == nil || !errors.As(err, new(sointu.UnknownUnit)) || !strings.Contains(err.Error(), `"futureunit"`) || !strings.Contains(err.Error(), "lead") {
		t.Errorf("playing a song with unknown units: %v", err)
	}
	// so is making a synth of its patch
	if _, err := (vm.GoSynther{}).Synth(song.Patch, 100); err == nil || !errors.As(err, new(sointu.UnknownUnit)) || !strings.Contains(err.Error(), "lead") {
		t.Errorf("a synth of a patch with unknown units: %v", err)
	}
	// without them, it plays: the same as the song written without them
	stripped, left := song.WithoutUnknownUnits()
	if len(left) != 2 || stripped.CheckUnitTypes() != nil {
		t.Fatalf("left out: %+v; still unknown: %v", left, stripped.CheckUnitTypes())
	}
	if got := unitTypes(stripped.Patch[1].Units); got != "oscillator otherfuture module out" {
		t.Errorf("without the unknown units, the instrument is %q", got)
	}
	if got := unitTypes(stripped.Modules[0].Units); got != "gain" {
		t.Errorf("without the unknown units, the module is %q", got)
	}
	if got := unitTypes(song.Patch[1].Units); got != "oscillator futureunit otherfuture module out" || len(song.Modules[0].Units) != 2 {
		t.Errorf("the song itself changed: %q", got)
	}
	if &stripped.Patch[0].Units[0] != &song.Patch[0].Units[0] {
		t.Error("the units of an instrument without unknown units were copied")
	}
	got, err := sointu.Play(vm.GoSynther{}, stripped, nil)
	if err != nil {
		t.Fatal(err)
	}
	plain := unknownSong()
	plain.Patch[1].Units = append(plain.Patch[1].Units[:1:1], plain.Patch[1].Units[3:]...)
	plain.Modules[0].Units = plain.Modules[0].Units[:1]
	want, err := sointu.Play(vm.GoSynther{}, plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d: without its unknown units the song renders %v, not %v", i, got[i], want[i])
		}
	}
	// a song with known units only is returned as it is
	if same, left := plain.WithoutUnknownUnits(); left != nil || &same.Patch[0] != &plain.Patch[0] {
		t.Error("a song without unknown units was copied")
	}
}
