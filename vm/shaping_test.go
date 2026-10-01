package vm

import (
	"math"
	"testing"
)

func TestSoftclip(t *testing.T) {
	// as it is below the knee, full scale from 2 - knee, never above, and
	// never falling
	for _, knee := range []float32{0, 0.25, 0.5, 0.75, 1} {
		last := float32(-2)
		for i := -3000; i <= 3000; i++ {
			x := float32(i) / 1000
			y := softclip(x, knee)
			if y != -softclip(-x, knee) {
				t.Fatalf("knee %v: not symmetric at %v", knee, x)
			}
			if a := abs32(x); a <= knee && y != x {
				t.Fatalf("knee %v: softclip(%v) = %v below the knee", knee, x, y)
			} else if a >= 2-knee && abs32(y) != 1 {
				t.Fatalf("knee %v: softclip(%v) = %v, want full scale", knee, x, y)
			}
			if abs32(y) > 1 || y < last {
				t.Fatalf("knee %v: softclip(%v) = %v after %v", knee, x, y, last)
			}
			last = y
		}
	}
	// knee 1 is a hard clip
	for _, x := range []float32{-3, -1, -0.3, 0, 0.99, 1.5} {
		if got := softclip(x, 1); got != clip(x) {
			t.Errorf("softclip(%v) with knee 1 = %v, want %v", x, got, clip(x))
		}
	}
	// the bend is smooth: the slope falls evenly from 1 at the knee to 0
	if got := softclip(1, 0.5); math.Abs(float64(got)-0.875) > 1e-6 {
		t.Errorf("softclip(1) with knee 0.5 = %v, want 0.875", got)
	}
}

// rmsOf returns the RMS of the second half of a signal.
func rmsOf(x []float32) float64 {
	sum := 0.0
	for _, v := range x[len(x)/2:] {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(x)/2))
}

func TestWidth(t *testing.T) {
	var st [8]float32
	// width 0.5 leaves the signal, 0 makes it mono, 1 doubles the side
	if l, r := width(&st, 0.75, -0.25, 0.5, 0); math.Abs(float64(l)-0.75) > 1e-6 || math.Abs(float64(r)+0.25) > 1e-6 {
		t.Errorf("width 0.5: %v %v, want 0.75 -0.25", l, r)
	}
	if l, r := width(&st, 0.75, -0.25, 0, 0); l != 0.25 || r != 0.25 {
		t.Errorf("width 0: %v %v, want 0.25 0.25", l, r)
	}
	if l, r := width(&st, 0.75, -0.25, 1, 0); l != 1.25 || r != -0.75 {
		t.Errorf("width 1: %v %v, want 1.25 -0.75", l, r)
	}
	if st != [8]float32{} {
		t.Errorf("without lowcut the state changed: %v", st)
	}
	// lowcut 0.13 is about 120 Hz: a 40 Hz tone on the left only becomes
	// nearly mono, a 2 kHz tone stays on the left
	side := func(freq float64) float64 {
		var st [8]float32
		n := 44100
		diff := make([]float32, n)
		for i := range diff {
			x := float32(math.Sin(2 * math.Pi * freq * float64(i) / 44100))
			l, r := width(&st, x, 0, 0.5, 0.13)
			diff[i] = l - r
		}
		return 20 * math.Log10(rmsOf(diff)/math.Sqrt(0.5))
	}
	if low, high := side(40), side(2000); low > -15 || high < -0.5 {
		t.Errorf("lowcut: the side of a 40 Hz tone is at %.1f dB, of a 2 kHz tone at %.1f dB", low, high)
	} else {
		t.Logf("lowcut: side at %.1f dB for 40 Hz, %.1f dB for 2 kHz", low, high)
	}
}

func TestSoftclipOversampled(t *testing.T) {
	// without clipping the level of every frequency stays
	run := func(freq, amp float64, knee float32, oversample bool) []float32 {
		s := make([]float32, 4)
		out := make([]float32, 44100)
		for i := range out {
			x := float32(amp * math.Sin(2*math.Pi*freq*float64(i)/44100))
			if oversample {
				out[i] = softclipOversampled(s, x, knee)
			} else {
				out[i] = softclip(x, knee)
			}
		}
		return out
	}
	for _, freq := range []float64{50, 1000, 10000, 20000} {
		if db := 20 * math.Log10(rmsOf(run(freq, 0.4, 0.5, true))/(0.4*math.Sqrt(0.5))); math.Abs(db) > 0.01 {
			t.Errorf("a quiet tone of %v Hz changes by %.3f dB", freq, db)
		}
	}
	// a 5 kHz tone clipped hard: the overtones 5, 7 and 9 are at 25, 35 and
	// 45 kHz and fold back to 19.1, 9.1 and 0.9 kHz
	alias := func(oversample bool) (ret [3]float64) {
		out := run(5000, 4, 1, oversample)
		for j, freq := range []float64{19100, 9100, 900} {
			var re, im float64
			for i, v := range out {
				re += float64(v) * math.Cos(2*math.Pi*freq*float64(i)/44100)
				im += float64(v) * math.Sin(2*math.Pi*freq*float64(i)/44100)
			}
			ret[j] = 20 * math.Log10(2*math.Hypot(re, im)/float64(len(out)))
		}
		return
	}
	plain, over := alias(false), alias(true)
	t.Logf("aliases of a hard clipped 5 kHz tone at 19.1, 9.1 and 0.9 kHz: %.1f dB without, %.1f dB with oversampling", plain, over)
	if over[1] > plain[1]-20 || over[2] > plain[2]-20 {
		t.Errorf("oversampling takes less than 20 dB off the aliases: %.1f dB, %.1f dB without", over, plain)
	}
	// and it stays near full scale
	var peak float32
	for _, freq := range []float64{100, 1000, 5000, 9000} {
		for _, v := range run(freq, 4, 0.5, true) {
			peak = max(peak, abs32(v))
		}
	}
	t.Logf("the oversampled softclip reaches %v", peak)
	if peak > 1.3 {
		t.Errorf("the oversampled softclip reaches %v", peak)
	}
}

func TestLadder(t *testing.T) {
	run := func(freq float64, amp float64, frequency, resonance, drive float32) []float32 {
		s := make([]float32, 4)
		out := make([]float32, 44100)
		for i := range out {
			out[i] = ladder(s, float32(amp*math.Sin(2*math.Pi*freq*float64(i)/44100)), frequency, resonance, drive)
		}
		return out
	}
	level := func(freq float64, frequency, resonance float32) float64 {
		return 20 * math.Log10(rmsOf(run(freq, 0.01, frequency, resonance, 0))/(0.01*math.Sqrt(0.5)))
	}
	// frequency 0.3 is about 1380 Hz: flat below, 24 dB per octave above
	low, oct1, oct2 := level(60, 0.3, 0), level(5000, 0.3, 0), level(10000, 0.3, 0)
	t.Logf("no resonance: %.1f dB at 60 Hz, %.1f dB at 5 kHz, %.1f dB at 10 kHz", low, oct1, oct2)
	if math.Abs(low) > 0.5 || oct1-oct2 < 20 || oct1-oct2 > 34 {
		t.Errorf("not a low-pass of 24 dB per octave: %.1f dB at 60 Hz, %.1f dB from 5 to 10 kHz", low, oct1-oct2)
	}
	// with resonance there is a peak near the cutoff, and the bass drops by less than 6 dB
	peak, bass := level(1380, 0.3, 0.8), level(60, 0.3, 0.8)
	t.Logf("resonance 0.8: %.1f dB at 1380 Hz, %.1f dB at 60 Hz", peak, bass)
	if peak < bass+6 || bass < -6 {
		t.Errorf("resonance 0.8: %.1f dB at the cutoff, %.1f dB in the bass", peak, bass)
	}
	// at full resonance it rings by itself after a click, at every cutoff,
	// and at resonance 0.8 the ringing dies
	for _, f := range []float32{0.1, 0.3, 0.6, 0.9} {
		for _, r := range []float32{0.8, 1} {
			s := make([]float32, 4)
			var level float32
			for i := 0; i < 88200; i++ {
				x := float32(0)
				if i == 0 {
					x = 1
				}
				level = max(abs32(ladder(s, x, f, r, 0)), level*0.999)
			}
			t.Logf("frequency %v, resonance %v: the level 2 s after a click is %v", f, r, level)
			if r == 1 && (level < 0.1 || level > 1.5) || r < 1 && level > 1e-4 {
				t.Errorf("frequency %v, resonance %v: the level 2 s after a click is %v", f, r, level)
			}
		}
	}
	// a hot signal with full drive stays bounded at every frequency
	for _, f := range []float32{0, 0.1, 0.5, 0.9, 1, 1.3} {
		for _, r := range []float32{0, 0.5, 1, 1.5} {
			for _, v := range run(100, 4, f, r, 1) {
				if !(abs32(v) <= 2) {
					t.Fatalf("frequency %v, resonance %v: the output reaches %v", f, r, v)
				}
			}
		}
	}
}
