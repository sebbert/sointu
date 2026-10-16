package tracker

import (
	"slices"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// TestOscillatorCycle checks the cycles of the oscillators of the rack: that
// of the unit as it is played, kept while the parameters stay, and none for
// other units and sample oscillators.
func TestOscillatorCycle(t *testing.T) {
	m, _ := newModuleTestModel(t)
	cycle := m.Unit().OscillatorCycle(0)
	if want := vm.OscillatorCycle(m.d.Song.Patch[0].Units[0], oscillatorCyclePoints); len(cycle) != oscillatorCyclePoints || !slices.Equal(cycle, want) {
		t.Fatalf("the cycle of the LFO has %d points, or other ones", len(cycle))
	}
	if again := m.Unit().OscillatorCycle(0); &again[0] != &cycle[0] {
		t.Error("the cycle is computed again without a change")
	}
	if m.Unit().OscillatorCycle(1) != nil || m.Unit().InlineCells(1) != 0 {
		t.Error("a send has a cycle")
	}
	if m.Unit().InlineCells(0) != OscillatorInlineCells {
		t.Errorf("the oscillator has %d cells after its parameters", m.Unit().InlineCells(0))
	}
	// a change of a parameter makes another cycle
	for x := 0; x < m.Params().RowWidth(0); x++ {
		if p := m.Params().Item(Point{x, 0}); p.Name() == "type" {
			p.SetValue(sointu.Pulse)
		}
	}
	pulse := m.Unit().OscillatorCycle(0)
	if pulse == nil || pulse[0] <= 0 || pulse[len(pulse)-1] >= 0 {
		t.Fatalf("the pulse is not high, then low: %v", pulse)
	}
	if slices.Equal(pulse, cycle) {
		t.Error("the cycle stays the same when the type changes")
	}
	for x := 0; x < m.Params().RowWidth(0); x++ {
		if p := m.Params().Item(Point{x, 0}); p.Name() == "type" {
			p.SetValue(sointu.Sample)
		}
	}
	if m.Unit().OscillatorCycle(0) != nil || m.Unit().InlineCells(0) != 0 {
		t.Error("a sample oscillator has a cycle")
	}
}
