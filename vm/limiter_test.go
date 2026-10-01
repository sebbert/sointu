package vm

import (
	"math"
	"testing"
)

// limit runs a mono or stereo signal through a limiter.
func limit(in [][2]float32, channels int, threshold, release, drive float32, lookahead int) [][2]float32 {
	var st limiterState
	p := [8]float32{threshold, release, drive}
	out := make([][2]float32, len(in))
	for i, x := range in {
		stack := []float32{x[1], x[0]}[2-channels:]
		limiter(&st, &p, lookahead, channels, stack)
		out[i][0] = stack[len(stack)-1]
		if channels == 2 {
			out[i][1] = stack[0]
		}
	}
	return out
}

func peakOf(x [][2]float32) (peak float32) {
	for _, v := range x {
		peak = max(peak, float32(math.Abs(float64(v[0]))), float32(math.Abs(float64(v[1]))))
	}
	return
}

func sine(n int, freq, amp float64) [][2]float32 {
	ret := make([][2]float32, n)
	for i := range ret {
		ret[i][0] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/44100))
	}
	return ret
}

func TestLimiterPassesQuietSignalsDelayed(t *testing.T) {
	in := sine(4000, 440, 0.4)
	for _, lookahead := range []int{0, 4, 128, 508} {
		out := limit(in, 1, 0.5, 0.5, 0, lookahead)
		for i := range in {
			var want float32
			if i >= lookahead {
				want = in[i-lookahead][0]
			}
			if out[i][0] != want {
				t.Fatalf("lookahead %d: sample %d is %v, want %v", lookahead, i, out[i][0], want)
			}
		}
	}
}

func TestLimiterLimits(t *testing.T) {
	// a burst 12 dB above the threshold after silence, the hardest case:
	// the gain has to come down from 1 within the lookahead
	in := make([][2]float32, 20000)
	copy(in[5000:], sine(8000, 1000, 1))
	for _, c := range []struct {
		lookahead int
		over      float64 // dB that the output may exceed the threshold by
	}{{0, 0}, {32, 0.6}, {128, 0.6}, {508, 0.6}} {
		out := limit(in, 1, 0.25, 0.5, 0, c.lookahead)
		over := 20 * math.Log10(float64(peakOf(out))/0.25)
		t.Logf("lookahead %d: peak %.2f dB above the threshold", c.lookahead, over)
		if over > c.over+1e-4 {
			t.Errorf("lookahead %d: the output is %.2f dB above the threshold, want at most %v", c.lookahead, over, c.over)
		}
		// and the steady part of the burst is at the threshold, not below
		if steady := 20 * math.Log10(float64(peakOf(out[9000:12000]))/0.25); steady < -0.1 {
			t.Errorf("lookahead %d: the limited signal is %.2f dB below the threshold", c.lookahead, steady)
		}
	}
}

func TestLimiterReleases(t *testing.T) {
	// after a loud burst, a quiet signal comes back to its level
	in := sine(60000, 1000, 0.2)
	copy(in[1000:], sine(2000, 1000, 1))
	out := limit(in, 1, 0.25, 0.5, 0, 128) // release 0.5: 93 ms
	if p := peakOf(out[3200:3500]); p > 0.1 {
		t.Errorf("right after the burst the quiet signal peaks at %v, want it held down", p)
	}
	if p := peakOf(out[50000:]); math.Abs(float64(p)-0.2) > 0.002 {
		t.Errorf("a second after the burst the quiet signal peaks at %v, want 0.2", p)
	}
}

func TestLimiterStereoAndDrive(t *testing.T) {
	// one gain for both channels: a loud left channel turns the right one down too
	in := make([][2]float32, 8000)
	for i, v := range sine(8000, 1000, 1) {
		in[i] = [2]float32{v[0], 0.1}
	}
	out := limit(in, 2, 0.5, 0.5, 0, 128)
	if l, r := out[6000][0], out[6000][1]; math.Abs(float64(r)) > 0.06 || math.Abs(float64(r)) < 0.04 {
		t.Errorf("the right channel is %v next to a left channel of %v, want about 0.05", r, l)
	}
	// drive 1 is 8 times, 18 dB: a signal 12 dB below the threshold is limited
	quiet := sine(8000, 1000, 0.125)
	if p := peakOf(limit(quiet, 1, 0.5, 0.5, 0, 128)[4000:]); math.Abs(float64(p)-0.125) > 1e-3 {
		t.Errorf("without drive the quiet signal peaks at %v", p)
	}
	if p := peakOf(limit(quiet, 1, 0.5, 0.5, 1, 128)[4000:]); math.Abs(float64(p)-0.5) > 0.01 {
		t.Errorf("with drive the signal peaks at %v, want the threshold 0.5", p)
	}
}
