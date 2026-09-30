package vm

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

// aliasing renders an oscillator playing a note (127 is 6.3 kHz) and returns
// the energy of the spectrum away from its harmonics relative to the total,
// in dB.
func aliasing(t *testing.T, note float64, typ, color, bandlimit int) float64 {
	t.Helper()
	const n = 1 << maxSpectrumLog2Size
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": color, "shape": 64, "gain": 128, "type": typ, "bandlimit": bandlimit}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}}
	synth, err := GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	synth.Trigger(0, byte(note))
	out := make(sointu.AudioBuffer, 1000+n)
	if _, _, err := synth.Render(out, len(out)); err != nil {
		t.Fatal(err)
	}
	x := make([]float32, 2*n)
	for j := range n {
		// a 4-term Blackman-Harris window, whose side lobes (-92 dB) leave
		// the aliasing measurable
		a := 2 * math.Pi * float64(j) / n
		w := 0.35875 - 0.48829*math.Cos(a) + 0.14128*math.Cos(2*a) - 0.01168*math.Cos(3*a)
		x[2*j] = out[1000+j][0] * float32(w)
	}
	fft(x, n)
	// the harmonics, in bins, and the window's main lobe around them
	f0 := 0.000092696138 * math.Exp2(note/12.0) * n
	var harmonics, other float64
	for k := 1; k < n/2; k++ {
		e := float64(x[2*k]*x[2*k] + x[2*k+1]*x[2*k+1])
		h := float64(k) / f0
		if math.Abs(h-math.Round(h))*f0 <= 6 && math.Round(h) >= 1 {
			harmonics += e
		} else if k > 6 {
			other += e
		}
	}
	return 10 * math.Log10(other/(harmonics+other))
}

// TestBandlimitReducesAliasing checks that bandlimited waveforms at 6.3 kHz
// have clearly less energy between their harmonics than naive ones.
func TestBandlimitReducesAliasing(t *testing.T) {
	for _, c := range []struct {
		name       string
		typ, color int
		minImprove float64
	}{
		{"pulse 50%", sointu.Pulse, 64, 15},
		{"pulse 25%", sointu.Pulse, 32, 15},
		{"saw (trisaw color 0)", sointu.Trisaw, 0, 15},
		{"saw (trisaw color 128)", sointu.Trisaw, 128, 15},
		{"triangle (trisaw color 64)", sointu.Trisaw, 64, 15},
		// color 16 is closer to 0 than dt, so the bandlimited one has a
		// ramp of one sample instead
		{"trisaw color 16", sointu.Trisaw, 16, 10},
		{"half sine (sine color 64)", sointu.Sine, 64, 10},
	} {
		naive, bandlimited := aliasing(t, 127, c.typ, c.color, 0), aliasing(t, 127, c.typ, c.color, 1)
		t.Logf("%-28s aliasing %6.1f dB naive, %6.1f dB bandlimited: %5.1f dB less", c.name, naive, bandlimited, naive-bandlimited)
		if naive-bandlimited < c.minImprove {
			t.Errorf("%v: aliasing went from %.1f dB to %.1f dB, want at least %v dB less", c.name, naive, bandlimited, c.minImprove)
		}
	}
}
