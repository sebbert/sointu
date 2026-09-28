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

// dcPatch returns a patch pushing value (loadval) through spfft, the given
// units and spifft, with the resynthesis on the left and the input on the
// right.
func dcPatch(value int, units ...sointu.Unit) sointu.Patch {
	patch := spectralPatch(0, units...)
	patch[0].Units[0] = sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": value}}
	return patch
}

func TestSpectralModifiersNeutral(t *testing.T) {
	for _, u := range []sointu.Unit{
		{Type: "spfilter", Parameters: sointu.ParamMap{"low": 0, "high": 128, "tilt": 64, "buffer": 1}},
		{Type: "spcompress", Parameters: sointu.ParamMap{"amount": 64, "width": 16, "buffer": 1}},
		{Type: "spblur", Parameters: sointu.ParamMap{"amount": 0, "freeze": 0, "buffer": 1}},
		{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseDisperse, "amount": 0, "buffer": 1}},
		{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseRandom, "amount": 0, "buffer": 1}},
		{Type: "spphase", Parameters: sointu.ParamMap{"mode": sointu.SpphaseRobot, "amount": 0, "buffer": 1}},
		{Type: "spscale", Parameters: sointu.ParamMap{"scale": 64, "shift": 64, "buffer": 1}},
		{Type: "spformant", Parameters: sointu.ParamMap{"shift": 64, "width": 16, "buffer": 1}},
	} {
		out := render(t, newSynth(t, spectralPatch(1, u)), 4*512)
		if delay, diff := reconstructionError(out, 1024); delay != 511 || diff > 1e-5 {
			t.Errorf("%v: delay %d, want 511; max difference %v", u.Type, delay, diff)
		}
	}
}

func TestSpfilterRemovesDC(t *testing.T) {
	// the Hann window spreads DC to bin 1 too; 64 cuts below bin 3.9 of 128
	out := render(t, newSynth(t, dcPatch(96, sointu.Unit{Type: "spfilter", Parameters: sointu.ParamMap{"low": 64, "high": 128, "tilt": 64, "buffer": 1}})), 4*256)
	for i := 512; i < len(out); i++ {
		if math.Abs(float64(out[i][0])) > 1e-5 {
			t.Fatalf("frame %d: %v, want 0", i, out[i][0])
		}
	}
}

func TestSpcompressFlattens(t *testing.T) {
	// white noise has a flat envelope already; a lowpassed spectrum gets its
	// highs back: compare the energy above the cutoff with and without
	energy := func(units ...sointu.Unit) float64 {
		units = append([]sointu.Unit{{Type: "spfilter", Parameters: sointu.ParamMap{"low": 0, "high": 100, "tilt": 20, "buffer": 1}}}, units...)
		out := render(t, newSynth(t, spectralPatch(2, units...)), 8*1024)
		e := 0.0
		for i := 2048; i < len(out)-1; i++ {
			d := float64(out[i+1][0] - out[i][0]) // emphasizes highs
			e += d * d
		}
		return e
	}
	plain := energy()
	flat := energy(sointu.Unit{Type: "spcompress", Parameters: sointu.ParamMap{"amount": 128, "width": 16, "buffer": 1}})
	if flat < 2*plain {
		t.Errorf("high frequency energy %v with spcompress, %v without", flat, plain)
	}
}

func TestSpblurFreeze(t *testing.T) {
	// freeze noise, then silence the input: the frozen spectrum continues,
	// as steady noise of about the same level
	blur := func(freeze int) sointu.Unit {
		return sointu.Unit{Type: "spblur", Parameters: sointu.ParamMap{"amount": 0, "freeze": freeze, "buffer": 1}}
	}
	synth := newSynth(t, spectralPatch(0, blur(0)))
	before := render(t, synth, 8*256)
	patch := spectralPatch(0, blur(128))
	patch[0].Units[0].Parameters["gain"] = 0
	if err := synth.Update(patch, 120); err != nil {
		t.Fatal(err)
	}
	after := render(t, synth, 32*256)
	rms := func(out sointu.AudioBuffer) float64 {
		e := 0.0
		for _, f := range out {
			e += float64(f[0] * f[0])
		}
		return math.Sqrt(e / float64(len(out)))
	}
	want := rms(before[1024:])
	for i := 512; i+1024 <= len(after); i += 1024 {
		if got := rms(after[i : i+1024]); got < want/3 || got > want*3 {
			t.Errorf("frames %d-%d: rms %v, before freezing %v", i, i+1024, got, want)
		}
	}
}

func TestSpgate(t *testing.T) {
	// a constant 0.25 is 6 dB below a full scale sine
	for invert, want := range []float32{0, 0.25} {
		out := render(t, newSynth(t, dcPatch(80, sointu.Unit{Type: "spgate", Parameters: sointu.ParamMap{"threshold": 128, "invert": invert, "buffer": 1}})), 4*256)
		for i := 512; i < len(out); i++ {
			if math.Abs(float64(out[i][0]-want)) > 1e-5 {
				t.Fatalf("invert %d, frame %d: %v, want %v", invert, i, out[i][0], want)
			}
		}
	}
}

func TestSpscaleShiftsUp(t *testing.T) {
	// shifting by 1 kHz moves DC up: the constant disappears
	out := render(t, newSynth(t, dcPatch(96, sointu.Unit{Type: "spscale", Parameters: sointu.ParamMap{"scale": 64, "shift": 128, "buffer": 1}})), 8*256)
	mean := 0.0
	for i := 1024; i < len(out); i++ {
		mean += float64(out[i][0])
	}
	if mean /= float64(len(out) - 1024); math.Abs(mean) > 1e-3 {
		t.Errorf("mean %v after shifting, want 0", mean)
	}
}
