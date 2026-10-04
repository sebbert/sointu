package resample

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"testing"

	"github.com/vsariola/sointu"
)

// rates are the output rates of the tests: the common ones, and two that
// have no small ratio to 44100, for which the kernels are interpolated.
var rates = []int{48000, 88200, 96000, 176400, 192000, 32000, 22050, 47999, 50000}

// run resamples in to outRate in blocks of the sizes that size returns.
func run(r *Resampler, in sointu.AudioBuffer, size func() int) sointu.AudioBuffer {
	var out sointu.AudioBuffer
	for {
		n := size()
		m := r.Need(n)
		if m > len(in) {
			return out
		}
		block := make(sointu.AudioBuffer, n)
		r.Process(block, in[:m])
		in = in[m:]
		out = append(out, block...)
	}
}

func sine(frames int, freq, rate float64) sointu.AudioBuffer {
	ret := make(sointu.AudioBuffer, frames)
	for i := range ret {
		v := float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/rate))
		ret[i] = [2]float32{v, -v}
	}
	return ret
}

// fit returns the amplitudes of the sines of the frequencies freqs in the
// left channel of buf, by least squares, and the RMS of what is left of the
// signal without them.
func fit(buf sointu.AudioBuffer, rate float64, freqs ...float64) (amplitudes []float64, residual float64) {
	n := 2 * len(freqs)
	basis := func(i int, dst []float64) {
		for k, f := range freqs {
			dst[2*k+1], dst[2*k] = math.Sincos(2 * math.Pi * f / rate * float64(i))
		}
	}
	// the normal equations, solved by elimination
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n+1)
	}
	v := make([]float64, n)
	for i, f := range buf {
		basis(i, v)
		for j := range v {
			for k := range v {
				m[j][k] += v[j] * v[k]
			}
			m[j][n] += v[j] * float64(f[0])
		}
	}
	for i := range m {
		for j := i + 1; j < n; j++ {
			q := m[j][i] / m[i][i]
			for k := i; k <= n; k++ {
				m[j][k] -= q * m[i][k]
			}
		}
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		x[i] = m[i][n]
		for k := i + 1; k < n; k++ {
			x[i] -= m[i][k] * x[k]
		}
		x[i] /= m[i][i]
	}
	for i, f := range buf {
		basis(i, v)
		d := float64(f[0])
		for j := range v {
			d -= x[j] * v[j]
		}
		residual += d * d
	}
	for k := range freqs {
		amplitudes = append(amplitudes, math.Hypot(x[2*k], x[2*k+1]))
	}
	return amplitudes, math.Sqrt(residual / float64(len(buf)))
}

func dB(x float64) float64 { return 20 * math.Log10(math.Max(x, 1e-12)) }

// edges returns the ends of the passband and the start of the stopband of
// the kernel, in Hz: 20 kHz and 24.1 kHz for 44100 Hz and a faster rate.
func edges(outRate int) (pass, stop float64) {
	nyquist := float64(min(SynthRate, outRate)) / 2
	return nyquist * 20000 / 22050, nyquist * 24100 / 22050
}

// TestPassband checks that tones up to the end of the passband keep their
// level within 0.01 dB, and that all that the resampler adds to them
// (images, aliases, noise) is 96 dB below them.
func TestPassband(t *testing.T) {
	for _, rate := range rates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			pass, _ := edges(rate)
			worstGain, worstRest := 0.0, -200.0
			for _, f := range []float64{31, 440, 1000, 5000, 0.5 * pass, 0.8 * pass, 0.95 * pass, pass} {
				r := New(SynthRate, rate)
				out := run(r, sine(SynthRate/2, f, SynthRate), func() int { return 1000 })
				out = out[2*r.Latency():] // after the start of the tone
				amplitudes, rest := fit(out, float64(rate), f)
				gain, rest := dB(amplitudes[0]/0.5), dB(rest/(0.5/math.Sqrt2))
				if math.Abs(gain) > math.Abs(worstGain) {
					worstGain = gain
				}
				worstRest = math.Max(worstRest, rest)
				if math.Abs(gain) > 0.01 {
					t.Errorf("%.0f Hz: the level changes by %.4f dB", f, gain)
				}
				if rest > -96 {
					t.Errorf("%.0f Hz: images, aliases and noise at %.1f dB", f, rest)
				}
			}
			t.Logf("to %.0f Hz: level within %.5f dB, everything else below %.1f dB", pass, worstGain, worstRest)
		})
	}
}

// TestStopband checks what is left of tones that the output must not have:
// when the output rate is lower, tones from the start of the stopband up,
// which would alias; when it is higher, the images of the tones of the
// passband, which are the tones mirrored at half the input rate.
func TestStopband(t *testing.T) {
	for _, rate := range rates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			pass, stop := edges(rate)
			worst := -200.0
			if rate < SynthRate {
				for _, f := range []float64{stop, 1.05 * stop, 1.2 * stop, 0.5 * (stop + SynthRate/2), 21000, 22000} {
					if f >= SynthRate/2 {
						continue
					}
					r := New(SynthRate, rate)
					out := run(r, sine(SynthRate/2, f, SynthRate), func() int { return 1000 })
					out = out[2*r.Latency():]
					sum := 0.0
					for _, v := range out {
						sum += float64(v[0]) * float64(v[0])
					}
					level := dB(math.Sqrt(sum/float64(len(out))) / (0.5 / math.Sqrt2))
					worst = math.Max(worst, level)
					if level > -96 {
						t.Errorf("%.0f Hz comes through at %.1f dB", f, level)
					}
				}
			} else {
				for _, f := range []float64{100, 1000, 10000, 0.9 * pass, pass} {
					image := SynthRate - f
					if alias := float64(rate) - image; image > float64(rate)/2 {
						image = alias // the image folds at half the output rate
					}
					if image < 0 || image > float64(rate)/2 {
						continue // above 88200 Hz the first image is the one that matters
					}
					r := New(SynthRate, rate)
					out := run(r, sine(SynthRate/2, f, SynthRate), func() int { return 1000 })
					out = out[2*r.Latency():]
					amplitudes, _ := fit(out, float64(rate), f, image)
					level := dB(amplitudes[1] / 0.5)
					worst = math.Max(worst, level)
					if level > -96 {
						t.Errorf("the image of %.0f Hz at %.0f Hz is at %.1f dB", f, image, level)
					}
				}
			}
			t.Logf("from %.0f Hz: below %.1f dB", stop, worst)
		})
	}
}

// TestLatency checks that the output is the input Latency output frames
// late, to the sample and below: a tone comes out as the same tone,
// evaluated Latency frames earlier.
func TestLatency(t *testing.T) {
	for _, rate := range rates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			const freq = 1000
			r := New(SynthRate, rate)
			if r.Latency() != Latency(SynthRate, rate) {
				t.Fatalf("Latency() is %d, Latency(%d, %d) is %d", r.Latency(), SynthRate, rate, Latency(SynthRate, rate))
			}
			out := run(r, sine(SynthRate/2, freq, SynthRate), func() int { return 777 })
			for i := 4 * r.Latency(); i < len(out); i++ {
				want := 0.5 * math.Sin(2*math.Pi*freq*float64(i-r.Latency())/float64(rate))
				if d := math.Abs(float64(out[i][0]) - want); d > 1e-5 {
					t.Fatalf("frame %d is %v, want %v: the latency is not %d frames", i, out[i][0], want, r.Latency())
				}
				if out[i][1] != -out[i][0] {
					t.Fatalf("frame %d: the right channel is %v, want %v", i, out[i][1], -out[i][0])
				}
			}
			t.Logf("latency %d frames, %.2f ms", r.Latency(), 1000*float64(r.Latency())/float64(rate))
		})
	}
}

// TestBlocks checks that the output does not depend on the sizes of the
// blocks, to the bit, and that the input taken after h output frames is
// ⌈h·in/out⌉ frames.
func TestBlocks(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	in := make(sointu.AudioBuffer, 30000)
	for i := range in {
		in[i] = [2]float32{rnd.Float32()*2 - 1, rnd.Float32()*2 - 1}
	}
	for _, rate := range rates {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			whole := run(New(SynthRate, rate), in, func() int { return 1 << 14 })
			for _, size := range []func() int{
				func() int { return 1 },
				func() int { return 64 },
				func() int { return rnd.Intn(5) },
				func() int { return rnd.Intn(3000) },
			} {
				r := New(SynthRate, rate)
				taken, made := 0, 0
				out := run(r, in, func() int {
					n := size()
					taken += r.Need(n)
					made += n
					if want := (made*SynthRate + rate - 1) / rate; taken != want {
						t.Fatalf("%d input frames taken after %d output frames, want %d", taken, made, want)
					}
					return n
				})
				for i := range min(len(out), len(whole)) {
					if out[i] != whole[i] {
						t.Fatalf("frame %d is %v in small blocks and %v in one block", i, out[i], whole[i])
					}
				}
				if len(out) < len(whole)-3000 {
					t.Fatalf("only %d of %d frames", len(out), len(whole))
				}
			}
		})
	}
}

// TestReset checks that a Resampler that was reset starts over.
func TestReset(t *testing.T) {
	in := sine(5000, 1000, SynthRate)
	r := New(SynthRate, 48000)
	first := run(r, in, func() int { return 333 })
	r.Reset()
	second := run(r, in, func() int { return 333 })
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("frame %d is %v after a reset, %v before", i, second[i], first[i])
		}
	}
}

// TestEqualRates checks that nothing happens to a signal whose rate stays:
// Source gives the frames of the source itself, the errors too, and a
// Resampler between equal rates only delays them.
func TestEqualRates(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	in := make(sointu.AudioBuffer, 10000)
	for i := range in {
		in[i] = [2]float32{rnd.Float32()*2 - 1, rnd.Float32()*2 - 1}
	}
	if Latency(SynthRate, SynthRate) != 0 {
		t.Errorf("latency %d between equal rates, want 0", Latency(SynthRate, SynthRate))
	}
	calls := 0
	src := Source(func(buf sointu.AudioBuffer) error {
		calls++
		if copy(buf, in[(calls-1)*100:]) < len(buf) {
			return io.EOF
		}
		return nil
	}, SynthRate, SynthRate)
	buf := make(sointu.AudioBuffer, 100)
	for i := 0; i < len(in); i += 100 {
		if err := src(buf); err != nil {
			t.Fatal(err)
		}
		for j := range buf {
			if buf[j] != in[i+j] {
				t.Fatalf("frame %d is %v, want %v", i+j, buf[j], in[i+j])
			}
		}
	}
	if err := src(buf); err != io.EOF {
		t.Errorf("the error of the source is %v, want io.EOF", err)
	}
	r := New(SynthRate, SynthRate)
	out := run(r, in, func() int { return 100 })
	for i := r.Latency(); i < len(out); i++ {
		if out[i] != in[i-r.Latency()] {
			t.Fatalf("frame %d is %v, want frame %d of the input, %v", i, out[i], i-r.Latency(), in[i-r.Latency()])
		}
	}
}

// TestSource checks that a Source between different rates gives what the
// Resampler gives, asking its source for the frames that takes.
func TestSource(t *testing.T) {
	in := sine(40000, 440, SynthRate)
	want := run(New(SynthRate, 48000), in, func() int { return 512 })
	pos := 0
	src := Source(func(buf sointu.AudioBuffer) error {
		pos += copy(buf, in[pos:])
		return nil
	}, SynthRate, 48000)
	buf := make(sointu.AudioBuffer, 512)
	for i := 0; i+512 <= len(want); i += 512 {
		if err := src(buf); err != nil {
			t.Fatal(err)
		}
		for j := range buf {
			if buf[j] != want[i+j] {
				t.Fatalf("frame %d is %v, want %v", i+j, buf[j], want[i+j])
			}
		}
	}
}

// TestOvershoot checks how far a full-scale signal can go beyond full scale
// after resampling: a square wave, whose samples at another rate overshoot.
func TestOvershoot(t *testing.T) {
	in := make(sointu.AudioBuffer, SynthRate/4)
	for i := range in {
		v := float32(1)
		if i/50%2 == 1 {
			v = -1
		}
		in[i] = [2]float32{v, v}
	}
	out := run(New(SynthRate, 48000), in, func() int { return 512 })
	peak := 0.0
	for _, v := range out {
		peak = math.Max(peak, math.Abs(float64(v[0])))
	}
	t.Logf("a full-scale square wave peaks at %.2f dB at 48000 Hz", dB(peak))
	if peak > 1.3 {
		t.Errorf("peak %v", peak)
	}
}

func BenchmarkProcess(b *testing.B) {
	rnd := rand.New(rand.NewSource(3))
	for _, rate := range []int{48000, 96000, 192000, 22050, 47999} {
		b.Run(fmt.Sprint(rate), func(b *testing.B) {
			r := New(SynthRate, rate)
			out := make(sointu.AudioBuffer, rate/100) // 10 ms
			in := make(sointu.AudioBuffer, r.Need(len(out))+1)
			for i := range in {
				in[i] = [2]float32{rnd.Float32()*2 - 1, rnd.Float32()*2 - 1}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.Process(out, in[:r.Need(len(out))])
			}
			// the share of one core that playing in real time takes
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e7*100, "%cpu")
		})
	}
}
