package vm

import (
	"math"
	"testing"
)

// ottRun runs ott over frames of a signal given for each channel and returns
// the output and the input.
func ottRun(p [8]float32, channels, frames int, signal func(frame, channel int) float32) (out, in [][2]float32) {
	var st ottState
	stack := make([]float32, 2)
	for n := range frames {
		var x [2]float32
		for c := range channels {
			x[c] = signal(n, c)
			stack[1-c] = x[c]
		}
		ott(&st, &p, channels, stack[2-channels:])
		in = append(in, x)
		out = append(out, [2]float32{stack[1], stack[0]})
	}
	return out, in
}

// ottTestSignal is a mix of a low, mid and high sine and noise, with an
// amplitude going up and down by 60 dB.
func ottTestSignal(n, c int) float32 {
	t := float64(n) / 44100
	amp := math.Pow(10, -3*(1+math.Sin(2*math.Pi*t*3))/2)
	x := 0.4*math.Sin(2*math.Pi*60*t+float64(c)) + 0.3*math.Sin(2*math.Pi*800*t) + 0.2*math.Sin(2*math.Pi*7000*t*float64(c+1))
	x += 0.1 * (float64(uint32(n*2654435761+c*40503)%1000)/500 - 1)
	return float32(amp * x)
}

func ottParams(depth, time, upward, downward, low, mid, high int) [8]float32 {
	var p [8]float32
	for i, v := range []int{depth, time, upward, downward, low, mid, high} {
		p[i] = float32(v) / 128
	}
	return p
}

func TestOttDepthZeroPassesInput(t *testing.T) {
	for channels := 1; channels <= 2; channels++ {
		out, in := ottRun(ottParams(0, 64, 128, 128, 100, 20, 128), channels, 44100, ottTestSignal)
		for n := range out {
			if out[n] != in[n] {
				t.Fatalf("%d channels, frame %d: output %v, input %v", channels, n, out[n], in[n])
			}
		}
	}
}

func TestOttNeutralPassesInput(t *testing.T) {
	for channels := 1; channels <= 2; channels++ {
		for _, time := range []int{0, 64, 128} {
			out, in := ottRun(ottParams(128, time, 0, 0, 64, 64, 64), channels, 44100, ottTestSignal)
			for n := range out {
				for c := range channels {
					if d := math.Abs(float64(out[n][c] - in[n][c])); d > 1e-6 {
						t.Fatalf("%d channels, time %d, frame %d: output %v, input %v", channels, time, n, out[n][c], in[n][c])
					}
				}
			}
		}
	}
}

func TestOttCompressesLoudAndLiftsQuiet(t *testing.T) {
	rms := func(b [][2]float32, c int) float64 {
		sum := 0.0
		for _, f := range b[len(b)/2:] { // after the levels have settled
			sum += float64(f[c]) * float64(f[c])
		}
		return math.Sqrt(sum / float64(len(b)/2))
	}
	for _, freq := range []float64{50, 1000, 8000} {
		for channels := 1; channels <= 2; channels++ {
			for _, amp := range []float64{1, 0.001} {
				out, in := ottRun(ottParams(128, 64, 128, 128, 64, 64, 64), channels, 44100, func(n, c int) float32 {
					return float32(amp * math.Sin(2*math.Pi*freq*float64(n)/44100))
				})
				for c := range channels {
					ratio := rms(out, c) / rms(in, c)
					if amp == 1 && ratio > 0.2 {
						t.Errorf("%v Hz, %d channels, loud: gain %v, want attenuation", freq, channels, ratio)
					}
					if amp < 1 && ratio < 2 {
						t.Errorf("%v Hz, %d channels, quiet: gain %v, want a lift", freq, channels, ratio)
					}
				}
			}
		}
	}
}
