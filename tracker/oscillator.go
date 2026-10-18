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
	voices, last := m.livePorts(row)
	if last < 0 {
		return nil, false
	}
	ports := &voices[last]
	ret := maps.Clone(u.Parameters)
	for i, name := range sointu.Ports[u.Type] {
		if i < len(ports) && ports[i] != 0 {
			ret[name] = liveParam(u, name, ports[i])
		}
	}
	return ret, true
}

// livePorts returns what the sends and the automation added to the ports of
// the unit on a row in each voice of its instrument, as the synth last ran
// it there, and which of them is the voice of the last note of the
// instrument. Asking for them keeps the synth recording them, for a second
// (see taps.go); last is -1 until it has.
func (m *Model) livePorts(row int) (voices [][8]float32, last int) {
	key, ok := m.analysisKey(row)
	span, ok2 := m.playedSpan(row)
	if !ok || !ok2 || span.Count != 1 {
		return nil, -1
	}
	point := sointu.TapPoint{Instrument: span.Instrument, Unit: span.First}
	w := m.watchTap(tapKey{Unit: key.unit, Call: key.call, Ports: true}, point)
	last = m.lastVoice(span.Instrument) - 1
	if last >= len(w.voicePorts) {
		return nil, -1
	}
	return w.voicePorts, last
}

// liveParam returns the parameter of u plus what a port of port adds to it,
// rounded to steps and within its range.
func liveParam(u *sointu.Unit, name string, port float32) int {
	v := float64(u.Parameters[name]) + math.Round(float64(port)*128)
	if math.IsNaN(v) {
		return u.Parameters[name]
	}
	for _, p := range sointu.UnitTypes[u.Type].Params {
		if p.Name == name {
			v = min(max(v, float64(p.MinValue)), float64(p.MaxValue))
		}
	}
	return int(v)
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
