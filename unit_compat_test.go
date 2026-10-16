package sointu_test

import (
	"encoding/json"
	"testing"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

func TestOldBufreadPlaysForwards(t *testing.T) {
	var u sointu.Unit
	if err := yaml.Unmarshal([]byte("type: bufread\nparameters: {buffer: 1, gain: 128}\n"), &u); err != nil {
		t.Fatal(err)
	}
	if u.Parameters["speed"] != 128 || u.Parameters["buffer"] != 1 {
		t.Errorf("YAML: got %v, want speed 128 filled in", u.Parameters)
	}
	var j sointu.Unit
	if err := json.Unmarshal([]byte(`{"Type":"bufread","Parameters":{"buffer":1}}`), &j); err != nil {
		t.Fatal(err)
	}
	if j.Parameters["speed"] != 128 {
		t.Errorf("JSON: got %v, want speed 128 filled in", j.Parameters)
	}
	var reverse sointu.Unit
	yaml.Unmarshal([]byte("type: bufread\nparameters: {speed: 0}\n"), &reverse)
	if reverse.Parameters["speed"] != 0 {
		t.Errorf("a saved speed was changed: %v", reverse.Parameters)
	}
	var other sointu.Unit
	yaml.Unmarshal([]byte("type: noise\nparameters: {gain: 1}\n"), &other)
	if len(other.Parameters) != 1 {
		t.Errorf("parameters added to another unit type: %v", other.Parameters)
	}
}

func TestOldOscillatorIsNotBandlimited(t *testing.T) {
	var u sointu.Unit
	if err := yaml.Unmarshal([]byte("type: oscillator\nparameters: {type: 2, gain: 128}\n"), &u); err != nil {
		t.Fatal(err)
	}
	if b, ok := u.Parameters["bandlimit"]; !ok || b != 0 || sointu.OscillatorBandlimited(u) {
		t.Errorf("got %v, want bandlimit 0 filled in", u.Parameters)
	}
}

// A bufread saved before mode, note and slices took their present values,
// which has no note parameter, gets the parameters that compile to the same.
func TestOldBufreadParametersAreConverted(t *testing.T) {
	for _, tc := range []struct{ old, want string }{
		{"{buffer: 1, notetracking: 1, loop: 0, fade: 0, mode: 0, slices: 0, sliceend: 0}", "{buffer: 1, note: 1, mode: 0, slices: 0, loopfade: 0}"},
		{"{notetracking: 0, loop: 1, fade: 300}", "{note: 0, mode: 1, loopfade: 300}"},
		{"{notetracking: 1, loop: 1, mode: 1, start: -100}", "{note: 1, mode: 3, start: -100}"},
		{"{mode: 1, start: -10788, timing: 1}", "{note: 0, mode: 3, start: -10788, timing: 1}"}, // as the modules had it
		{"{notetracking: 1, slices: 1, slice: 3}", "{note: 1, mode: 0, slices: 1, slice: 3}"},
		{"{notetracking: 1, slices: 2, sliceend: 1}", "{note: 2, mode: 0, slices: 2}"},
		{"{notetracking: 0, slices: 2, sliceend: 0, loop: 1}", "{note: 2, mode: 1, slices: 1}"},
		{"{slices: 1, sliceend: 1}", "{note: 0, mode: 0, slices: 2}"},
		{"{note: 1, mode: 1, slices: 2}", "{note: 1, mode: 1, slices: 2}"}, // already converted
	} {
		var u, want sointu.Unit
		if err := yaml.Unmarshal([]byte("type: bufread\nparameters: "+tc.old+"\n"), &u); err != nil {
			t.Fatal(err)
		}
		yaml.Unmarshal([]byte("type: bufread\nparameters: "+tc.want+"\n"), &want)
		want.Parameters["speed"] = 128 // filled in when missing
		if len(u.Parameters) != len(want.Parameters) {
			t.Errorf("%s: got %v, want %v", tc.old, u.Parameters, want.Parameters)
			continue
		}
		for k, v := range want.Parameters {
			if g, ok := u.Parameters[k]; !ok || g != v {
				t.Errorf("%s: got %v, want %v", tc.old, u.Parameters, want.Parameters)
				break
			}
		}
	}
	var j sointu.Unit
	if err := json.Unmarshal([]byte(`{"Type":"bufread","Parameters":{"mode":1,"notetracking":1}}`), &j); err != nil {
		t.Fatal(err)
	}
	if j.Parameters["mode"] != sointu.BufreadTap || j.Parameters["note"] != sointu.BufreadNotePitch {
		t.Errorf("JSON: got %v", j.Parameters)
	}
	// bindings of a module unit move with the parameters whose values stay
	var b sointu.Unit
	yaml.Unmarshal([]byte("type: bufread\nparameters: {loop: 1}\nbind: {notetracking: 1, fade: 2, loop: 3, sliceend: 4}\n"), &b)
	if b.Bind["note"].Param != 1 || b.Bind["loopfade"].Param != 2 || b.Bind["mode"].Param != 3 || len(b.Bind) != 3 {
		t.Errorf("bindings: %v", b.Bind)
	}
}
