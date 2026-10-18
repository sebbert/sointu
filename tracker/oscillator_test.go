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

// TestOscillatorCycleLive checks that the cycle of an oscillator is that of
// its parameters plus what the synth last added to them in the voice of the
// last note (sends, automation), within their ranges, once the player has
// sent them.
func TestOscillatorCycleLive(t *testing.T) {
	m, _ := newModuleTestModel(t)
	const row = 3 // the oscillator with ID 4
	unit := m.d.Song.Patch[0].Units[row]
	plain := m.Unit().OscillatorCycle(row)
	if !slices.Equal(plain, vm.OscillatorCycle(unit, oscillatorCyclePoints)) {
		t.Fatal("without what the synth plays, the cycle is not that of the parameters")
	}
	w := m.taps[tapKey{Unit: 4, Ports: true}]
	if want := (sointu.TapPoint{Instrument: 0, Unit: row}); w == nil || w.point != want {
		t.Fatalf("the cycle watches %+v, not the ports before the oscillator in each voice", w)
	}
	send := func(ports [8]float32) {
		buf := m.broker.GetAudioBuffer()
		*buf = append((*buf)[:0], [2]float32{})
		m.tapped(TapAudio{Point: w.point, Buffer: buf, VoicePorts: [][8]float32{ports}})
	}
	with := func(name string, value int) []float32 {
		u := unit.Copy()
		u.Parameters[name] = value
		return vm.OscillatorCycle(u, oscillatorCyclePoints)
	}
	send([8]float32{3: 0.25}) // color +32
	live := m.Unit().OscillatorCycle(row)
	if !slices.Equal(live, with("color", unit.Parameters["color"]+32)) || slices.Equal(live, plain) {
		t.Error("the cycle is not that of the color plus what the synth adds to it")
	}
	send([8]float32{4: 10}) // shape far above its range
	if !slices.Equal(m.Unit().OscillatorCycle(row), with("shape", 128)) {
		t.Error("the shape is not kept within its range")
	}
}
