package tracker

import (
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// OscillatorInlineCells is how many parameter cells wide the plot of the
// cycle of an oscillator unit is, in its row of the rack, after its
// parameters.
const OscillatorInlineCells = 3

// oscillatorCyclePoints is how many points a cycle of an oscillator has.
const oscillatorCyclePoints = 512

// cycleKey is what the cycle of an oscillator depends on: see
// vm.OscillatorCycle.
type cycleKey struct {
	typ, color, shape, phase, gain, unison, bandlimit, lfo int
}

// maxCycles is how many cycles the model keeps; with more, it starts anew.
const maxCycles = 256

// OscillatorCycle returns one cycle of the waveform of the oscillator unit on
// a row of the unit editor, as its parameters make it, as played: for an
// inner unit, with the values that the module unit gives its bound
// parameters. Modulation is not in it. It is nil for other units and for
// sample oscillators. The cycles are kept by their parameters, and computed
// again only when those change.
func (m *UnitModel) OscillatorCycle(row int) []float32 {
	u, ok := (*Model)(m).previewUnit(row)
	if !ok || u.Type != "oscillator" || u.Parameters["type"] == sointu.Sample {
		return nil
	}
	p := u.Parameters
	key := cycleKey{p["type"], p["color"], p["shape"], p["phase"], p["gain"], p["unison"], p["bandlimit"], p["lfo"]}
	if c, ok := m.cycles[key]; ok {
		return c
	}
	if m.cycles == nil || len(m.cycles) >= maxCycles {
		m.cycles = map[cycleKey][]float32{}
	}
	c := vm.OscillatorCycle(*u, oscillatorCyclePoints)
	m.cycles[key] = c
	return c
}

// InlineCells returns how many parameter cells wide what the rack shows
// after the parameters of the unit on a row is: the small plot of a folded
// eq unit, the cycle of an oscillator, or nothing.
func (m *UnitModel) InlineCells(row int) int {
	switch {
	case (*Model)(m).EQAt(row).Active() && !m.EQExpanded(row):
		return EQInlineCells
	case m.OscillatorCycle(row) != nil:
		return OscillatorInlineCells
	}
	return 0
}
