package vm_test

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

// spectralPatch returns a patch whose single voice sends noise through spfft
// and the given spectral units to spifft: the resynthesized signal is on the
// left, the dry noise on the right.
func spectralPatch(size int, units ...sointu.Unit) sointu.Patch {
	us := []sointu.Unit{
		{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
		{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}},
		{Type: "spfft", Parameters: sointu.ParamMap{"size": size, "buffer": 1}},
	}
	us = append(us, units...)
	us = append(us,
		sointu.Unit{Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
		sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	)
	return sointu.Patch{{NumVoices: 1, Units: us}}
}

// reconstructionError returns the delay with the smallest difference between
// the left channel and the delayed right channel, and that difference.
func reconstructionError(out sointu.AudioBuffer, from int) (delay int, maxDiff float64) {
	maxDiff = math.Inf(1)
	for d := 0; d < from; d++ {
		m := 0.0
		for i := from; i < len(out); i++ {
			m = max(m, math.Abs(float64(out[i][0]-out[i-d][1])))
		}
		if m < maxDiff {
			delay, maxDiff = d, m
		}
	}
	return
}

func TestSpfftSpifftReconstruct(t *testing.T) {
	for size := 0; size <= sointu.SpectrumSizeMax; size++ {
		n := sointu.SpectrumSize(size)
		out := render(t, newSynth(t, spectralPatch(size)), 4*n)
		delay, diff := reconstructionError(out, 2*n)
		if delay != n-1 || diff > 1e-5 {
			t.Errorf("size %d: delay %d, want %d; max difference %v", n, delay, n-1, diff)
		}
	}
}

func TestSpcopy(t *testing.T) {
	// spifft reads the copy; spfft writes buffer 1, spcopy copies it to 2
	patch := spectralPatch(1, sointu.Unit{Type: "spcopy", Parameters: sointu.ParamMap{"source": 1, "buffer": 2}})
	patch[0].Units[4].Parameters["buffer"] = 2
	out := render(t, newSynth(t, patch), 4*512)
	if delay, diff := reconstructionError(out, 1024); delay != 511 || diff > 1e-5 {
		t.Errorf("delay %d, want 511; max difference %v", delay, diff)
	}
}

func TestSpectralOnlyFirstVoice(t *testing.T) {
	// both voices push 0.5; only the first one resynthesizes it
	patch := spectralPatch(0)
	patch[0].NumVoices = 2
	patch[0].Units[0] = sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}}
	out := render(t, newSynth(t, patch), 4*256)
	for i := 512; i < len(out); i++ {
		if math.Abs(float64(out[i][0]-0.5)) > 1e-5 || out[i][1] != 1 {
			t.Fatalf("frame %d: %v, want [0.5 1]", i, out[i])
		}
	}
}
