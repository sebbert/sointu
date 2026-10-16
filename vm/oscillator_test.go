package vm_test

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// TestOscillatorCycle checks that the cycle of an oscillator is what the Go
// synth plays: each sample of a note is the point of the cycle at the phase
// the synth has reached, within what the points of the cycle are apart.
func TestOscillatorCycle(t *testing.T) {
	const n = 1 << 16
	for _, c := range []struct {
		name   string
		params sointu.ParamMap
		tol    float64
	}{
		{"sine", sointu.ParamMap{"type": sointu.Sine, "color": 100, "shape": 80, "phase": 30, "gain": 100}, 1e-3},
		{"trisaw unison", sointu.ParamMap{"type": sointu.Trisaw, "color": 40, "shape": 64, "phase": 0, "gain": 128, "unison": 2}, 1e-3},
		{"pulse", sointu.ParamMap{"type": sointu.Pulse, "color": 50, "shape": 40, "phase": 90, "gain": 64}, 1e-3},
		{"bandlimited saw", sointu.ParamMap{"type": sointu.Trisaw, "color": 128, "shape": 64, "gain": 128, "bandlimit": 1}, 0.05},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64}
			for k, v := range c.params {
				p[k] = v
			}
			osc := sointu.Unit{Type: "oscillator", Parameters: p}
			out := sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}}
			synth, err := vm.GoSynther{}.Synth(sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{osc, out}}}, 120)
			if err != nil {
				t.Fatal(err)
			}
			const note, frames = 48, 2000
			synth.Trigger(0, note)
			buf := tapRender(t, synth, frames)
			cycle := vm.OscillatorCycle(osc, n)
			if len(cycle) != n {
				t.Fatalf("%d points", len(cycle))
			}
			// the phase advance, as the synth computes it
			advance := math.Exp2(float64(note)/12) * 0.000092696138
			bad := 0
			for k := range buf {
				phase := float64(k+1) * advance
				phase -= math.Floor(phase)
				i := int(math.Round(phase*n)) % n
				if d := math.Abs(float64(buf[k][0] - cycle[i])); d > c.tol {
					bad++
				}
			}
			// next to a jump, a sample can be on either side of it, and the
			// corrections of a bandlimited one are as wide as a sample
			if bad > frames/100 {
				t.Errorf("%d of %d samples off the cycle", bad, frames)
			}
		})
	}
	if vm.OscillatorCycle(sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"type": sointu.Sample}}, 16) != nil {
		t.Error("a sample oscillator has a cycle")
	}
}
