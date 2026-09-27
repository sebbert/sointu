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
	var osc sointu.Unit
	yaml.Unmarshal([]byte("type: oscillator\nparameters: {gain: 1}\n"), &osc)
	if len(osc.Parameters) != 1 {
		t.Errorf("parameters added to another unit type: %v", osc.Parameters)
	}
}
