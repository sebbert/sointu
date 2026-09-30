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
