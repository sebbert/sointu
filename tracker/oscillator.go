package tracker

import (
	"maps"
	"math"

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
// a row of the unit editor, as the synth plays it in the voice that the last
// note of the instrument played: its parameters, for an inner unit with the
// values that the module unit gives its bound parameters, plus what the
// sends and the automation add to them there (see liveParams). It is nil
// for other units and for sample oscillators. The cycles are kept by their
// parameters, and computed again only when those change.
func (m *UnitModel) OscillatorCycle(row int) []float32 {
	u, ok := (*Model)(m).previewUnit(row)
	if !ok || u.Type != "oscillator" || u.Parameters["type"] == sointu.Sample {
		return nil
	}
	if live, ok := (*Model)(m).liveParams(row, u); ok {
		c := *u
		c.Parameters = live
		u = &c
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

// liveParams returns the parameters of the unit u on a row as the synth last
// ran it in the voice of the last note of its instrument: those of u plus
// what the sends and the automation added to them, rounded to steps and
// within their ranges. Asking for them keeps the synth recording them, for
// a second (see taps.go); false until it has.
func (m *Model) liveParams(row int, u *sointu.Unit) (sointu.ParamMap, bool) {
	key, ok := m.analysisKey(row)
	span, ok2 := m.playedSpan(row)
	if !ok || !ok2 || span.Count != 1 {
		return nil, false
	}
	point := sointu.TapPoint{Instrument: span.Instrument, Unit: span.First, Voice: m.lastVoice(span.Instrument)}
	w := m.watchTap(tapKey{Unit: key.unit, Call: key.call, Ports: true}, point)
	if w.ports == nil {
		return nil, false
	}
	ret := maps.Clone(u.Parameters)
	for i, name := range sointu.Ports[u.Type] {
		if i >= len(w.ports) || w.ports[i] == 0 {
			continue
		}
		v := float64(ret[name]) + math.Round(float64(w.ports[i])*128)
		if math.IsNaN(v) {
			continue
		}
		for _, p := range sointu.UnitTypes[u.Type].Params {
			if p.Name == name {
				v = min(max(v, float64(p.MinValue)), float64(p.MaxValue))
			}
		}
		ret[name] = int(v)
	}
	return ret, true
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
