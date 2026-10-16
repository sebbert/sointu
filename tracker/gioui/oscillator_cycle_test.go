package gioui

import (
	"testing"

	"github.com/vsariola/sointu"
)

// TestOscillatorCycleRow lays out oscillators of every type, whose cycles
// follow their parameters, and a sample oscillator, which has none.
func TestOscillatorCycleRow(t *testing.T) {
	osc := func(params sointu.ParamMap) sointu.Unit {
		u := sointu.MakeUnit("oscillator")
		for k, v := range params {
			u.Parameters[k] = v
		}
		return u
	}
	r := newRackTest(t,
		osc(sointu.ParamMap{"type": sointu.Sine, "shape": 90}),
		osc(sointu.ParamMap{"type": sointu.Trisaw, "color": 128, "unison": 2, "gain": 40}),
		osc(sointu.ParamMap{"type": sointu.Pulse, "color": 32, "phase": 32, "bandlimit": 1}),
		osc(sointu.ParamMap{"type": sointu.Gate, "color": 0b10110101, "shape": 0b00001111, "lfo": 1}),
		osc(sointu.ParamMap{"type": sointu.Sample}),
	)
	r.settle()
	views := r.tr.PatchPanel.instrEditor.cycles
	if len(views) < 4 {
		t.Fatalf("%d cycles laid out", len(views))
	}
	for i, v := range views[:4] {
		if v == nil || v.cycle == nil {
			t.Errorf("the oscillator on row %d has no cycle", i)
		}
	}
	if len(views) > 4 && views[4] != nil && views[4].cycle != nil {
		t.Error("the sample oscillator has a cycle")
	}
	// another frame draws the recorded cycle again
	first := views[0].cycle
	r.frame()
	if views[0].cycle != first {
		t.Error("the cycle is recorded again without a change")
	}
	r.screenshot("oscillator_cycles")
}
