package vm

import "github.com/vsariola/sointu"

// OscillatorCycle returns one cycle of the waveform of an oscillator unit, n
// points from the start of the cycle, as the Go synth plays it with its
// parameters unmodulated: its type, color, shape, phase, gain and gate
// pattern, its unison voices summed, each started later by unisonPhaseStep,
// and with bandlimit, corrected as if a cycle were n samples long. Left out
// are the detune of the unison voices and of the channels, as over one cycle
// they are all but in phase, and the smoothing of a gate, which only a slow
// gate (an LFO) does not hide. An LFO has the same cycle, slower. A sample
// oscillator has none: nil.
func OscillatorCycle(u sointu.Unit, n int) []float32 {
	if u.Type != "oscillator" || n <= 0 {
		return nil
	}
	flags := oscillatorFlags(u)
	if flags&0x80 == 0x80 {
		return nil
	}
	p := u.Parameters
	param := func(name string) float32 { return float32(byte(p[name])) / 128.0 }
	phase0, color, shape, gain := param("phase"), param("color"), param("shape"), param("gain")
	gate := flags&0x74 == 0x04
	var dt float32
	if flags&0x04 == 0x04 && !gate {
		dt = min(max(1/float32(n), minBandlimitDt), 0.5)
	}
	var bits int32
	if gate {
		bits = gateBits([]byte{0, 0, 0, byte(p["color"]), byte(p["shape"])})
	}
	ret := make([]float32, n)
	for i := range ret {
		offset := phase0
		var output float32
		for j := byte(0); j <= flags&3; j++ {
			phase := float32(i)/float32(n) + offset
			phase -= floor32(phase)
			var amplitude float32
			if gate {
				amplitude = oscillatorGate(phase, bits)
			} else {
				amplitude = oscillatorWave(flags, phase, color, shape, dt)
			}
			output += float32(amplitude * gain)
			offset += unisonPhaseStep
		}
		ret[i] = output
	}
	return ret
}
