package compiler_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// TestCompilerRefusesUnknownUnits checks that a song with a unit of a type
// that Sointu does not have is refused, for every architecture, with an
// error naming the type and the instrument; and that with
// AllowUnknownUnits it compiles to the player of the song without the unit,
// with a warning.
func TestCompilerRefusesUnknownUnits(t *testing.T) {
	unit := func(typ string, params sointu.ParamMap) sointu.Unit {
		u := sointu.MakeUnit(typ)
		for k, v := range params {
			u.Parameters[k] = v
		}
		return u
	}
	units := []sointu.Unit{
		unit("envelope", nil), unit("oscillator", nil), unit("mulp", nil),
		{Type: "futureunit", Parameters: sointu.ParamMap{"stereo": 0, "amount": 5}},
		unit("out", sointu.ParamMap{"stereo": 0}),
	}
	song := func(units []sointu.Unit) sointu.Song {
		return sointu.Song{BPM: 100, RowsPerBeat: 4, Patch: sointu.Patch{{Name: "lead", NumVoices: 1, Units: units}},
			Score: sointu.Score{RowsPerPattern: 4, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1}}}}}}
	}
	with, without := song(units), song(append(append([]sointu.Unit{}, units[:3]...), units[4]))
	for _, arch := range []string{"wasm", "386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = com.Song(&with)
		if err == nil || !errors.As(err, new(sointu.UnknownUnit)) {
			t.Fatalf("%s: compiling a song with an unknown unit: %v", arch, err)
		}
		if want := `unit 3 of instrument 0 / lead has the unknown type "futureunit"`; !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the error is %q, without %q", arch, err, want)
		}
		com.AllowUnknownUnits = true
		got, warnings, err := com.Song(&with)
		if err != nil {
			t.Fatalf("%s: with unknown units allowed: %v", arch, err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], `"futureunit"`) || !strings.Contains(warnings[0], "left out") {
			t.Errorf("%s: the warnings are %q", arch, warnings)
		}
		want, moreWarnings, err := com.Song(&without)
		if err != nil || len(moreWarnings) != 0 {
			t.Fatalf("%s: the song without the unit: %v, warnings %q", arch, err, moreWarnings)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: with unknown units allowed, the song compiles differently from the song without the unit", arch)
		}
	}
	// the bytecode has no opcode to give an unknown type, whatever the
	// features
	for _, features := range []vm.FeatureSet{vm.AllFeatures{}, vm.NecessaryFeaturesFor(with.Patch)} {
		if _, err := vm.NewBytecode(with.Patch, features, 100); err == nil || !errors.As(err, new(sointu.UnknownUnit)) {
			t.Errorf("the bytecode of a patch with an unknown unit: %v", err)
		}
	}
}
