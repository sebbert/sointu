// Package resample converts stereo audio from one sample rate to another, in
// blocks of any size. The synth runs at 44100 Hz only; this plays it on
// audio devices and in plugin hosts that run at another rate.
package resample

import (
	"fmt"
	"math"

	"github.com/vsariola/sointu"
)

// SynthRate is the sample rate of the synth, in Hz.
const SynthRate = 44100

const (
	// halfTaps is how many frames of the slower of the two rates the kernel
	// reaches to each side of an output frame.
	halfTaps = 36
	// kaiserBeta is the shape of the Kaiser window of the kernel. With
	// halfTaps it puts the stopband 100 dB down, 2050 Hz (at 44100 Hz) to
	// each side of half the slower rate.
	kaiserBeta = 10.06
	// maxPhases is the largest number of rows of the table for which every
	// position of an output frame between two input frames has a row of
	// its own. With more positions, the table has interpPhases rows, and a
	// kernel is interpolated from the two rows next to the position.
	maxPhases    = 2048
	interpPhases = 1024
)

// Resampler converts a stream of stereo frames from one sample rate to
// another with a windowed sinc kernel: a low-pass at half the slower rate,
// evaluated where each output frame lies between the input frames. The
// output is the input delayed by Latency output frames, exactly, whatever
// the sizes of the blocks.
//
// After h output frames the Resampler has taken the first ⌈h·in/out⌉ input
// frames, so the blocks of a caller that renders the input on demand are as
// long as the output blocks, in time, within a frame.
type Resampler struct {
	a, b    int64              // the input and the output rate, divided by their gcd
	half    int                // input frames the kernel reaches to each side
	latency int                // in output frames
	phases  int64              // rows of the table per input frame
	exact   bool               // every output frame falls on a row
	table   []float32          // phases+1 rows of 2·half taps
	coef    []float32          // a kernel between two rows
	keep    int                // input frames kept from block to block
	buf     sointu.AudioBuffer // the kept frames, then those of the block
	rem     int64              // how far the input is ahead of the output, in 1/b input frames
}

// New returns a Resampler from inRate to outRate, in Hz. It allocates;
// Process does not, once it has seen the largest block.
func New(inRate, outRate int) *Resampler {
	if inRate <= 0 || outRate <= 0 {
		panic(fmt.Sprintf("resample.New: rates %d and %d", inRate, outRate))
	}
	g := gcd(inRate, outRate)
	r := &Resampler{a: int64(inRate / g), b: int64(outRate / g)}
	r.half, r.latency = size(inRate, outRate)
	r.keep = int(int64(r.latency)*r.a/r.b) + r.half + 1
	r.buf = make(sointu.AudioBuffer, r.keep, r.keep+4096)
	r.exact = r.b <= maxPhases
	r.phases = interpPhases
	if r.exact {
		r.phases = r.b
	}
	// the cutoff is half the slower rate, as a fraction of half the input
	// rate, and the window is as long as the kernel
	cutoff := math.Min(1, float64(outRate)/float64(inRate))
	n := 2 * r.half
	r.table = make([]float32, (int(r.phases)+1)*n)
	r.coef = make([]float32, n)
	row := make([]float64, n)
	norm := besselI0(kaiserBeta)
	for p := 0; p <= int(r.phases); p++ {
		// tap j of row p is the weight of the input frame j-half+1 frames
		// after the one before the output frame, which is p/phases of a
		// frame after that one
		sum := 0.0
		for j := range row {
			t := float64(j-r.half+1) - float64(p)/float64(r.phases)
			u := t / float64(r.half)
			row[j] = sinc(cutoff*t) * besselI0(kaiserBeta*math.Sqrt(math.Max(1-u*u, 0))) / norm
			sum += row[j]
		}
		// every row passes DC as it is, so that a constant stays one
		for j, v := range row {
			r.table[p*n+j] = float32(v / sum)
		}
	}
	return r
}

// size returns how many input frames the kernel reaches to each side, and
// the latency in output frames: the kernel of an output frame must end at
// an input frame that the Resampler has by then.
func size(inRate, outRate int) (half, latency int) {
	half = halfTaps
	if outRate < inRate {
		half = (halfTaps*inRate + outRate - 1) / outRate
	}
	return half, (half*outRate + inRate - 1) / inRate
}

// Latency returns by how many output frames a Resampler from inRate to
// outRate delays the signal; 0 for equal rates, which need none.
func Latency(inRate, outRate int) int {
	if inRate == outRate {
		return 0
	}
	_, latency := size(inRate, outRate)
	return latency
}

// Latency returns by how many output frames the output is behind the input.
func (r *Resampler) Latency() int { return r.latency }

// Need returns how many input frames the next n output frames take: those
// up to the time of the end of the n frames, rounded up. Need(k) for k < n
// is thus the input frame that is at the time of output frame k of the next
// block, counted from the first frame of the next input block.
func (r *Resampler) Need(n int) int {
	return int((int64(n)*r.a - r.rem + r.b - 1) / r.b)
}

// Process fills out with the next output frames, from the next input
// frames in, which must be Need(len(out)) frames.
func (r *Resampler) Process(out, in sointu.AudioBuffer) {
	if len(in) != r.Need(len(out)) {
		panic(fmt.Sprintf("resample.Process: %d input frames for %d output frames, which need %d", len(in), len(out), r.Need(len(out))))
	}
	r.buf = append(r.buf[:r.keep], in...)
	buf := r.buf
	n := 2 * r.half
	// The next output frame is latency output frames behind the end of the
	// input before this block, less what the input is ahead: at frame idx
	// from there and frac/b of a frame, rounding down.
	e := -r.rem - int64(r.latency)*r.a
	idx := e / r.b
	if idx*r.b > e {
		idx--
	}
	frac := e - idx*r.b
	base := r.keep + int(idx) - r.half + 1
	for j := range out {
		var coef []float32
		if r.exact {
			coef = r.table[int(frac)*n:][:n]
		} else {
			p := frac * r.phases / r.b
			w := float32(frac*r.phases-p*r.b) / float32(r.b)
			t0, t1 := r.table[int(p)*n:][:n], r.table[int(p+1)*n:][:n]
			coef = r.coef
			for k := range coef {
				coef[k] = t0[k] + w*(t1[k]-t0[k])
			}
		}
		x := buf[base:][:n]
		var l0, r0, l1, r1 float32
		for k := 0; k < n; k += 2 {
			l0 += coef[k] * x[k][0]
			r0 += coef[k] * x[k][1]
			l1 += coef[k+1] * x[k+1][0]
			r1 += coef[k+1] * x[k+1][1]
		}
		out[j] = [2]float32{l0 + l1, r0 + r1}
		if frac += r.a; frac >= r.b {
			base += int(frac / r.b)
			frac %= r.b
		}
	}
	r.rem += int64(len(in))*r.b - int64(len(out))*r.a
	copy(buf[:r.keep], buf[len(in):])
}

// Reset forgets the input so far: the Resampler is as New returned it.
func (r *Resampler) Reset() {
	clear(r.buf[:r.keep])
	r.rem = 0
}

// Source returns an AudioSource of frames at outRate that takes its frames
// from src, which gives frames at inRate. With equal rates it returns src
// itself: nothing touches the frames.
func Source(src sointu.AudioSource, inRate, outRate int) sointu.AudioSource {
	if inRate == outRate {
		return src
	}
	r := New(inRate, outRate)
	var in sointu.AudioBuffer
	return func(out sointu.AudioBuffer) error {
		m := r.Need(len(out))
		if len(in) < m {
			in = append(in, make(sointu.AudioBuffer, m-len(in))...)
		}
		err := src(in[:m])
		r.Process(out, in[:m])
		return err
	}
}

// sinc returns sin(πx)/(πx), which is exactly 0 at the whole numbers but 0.
func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	if x == math.Trunc(x) {
		return 0
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// besselI0 returns the modified Bessel function of the first kind and order
// 0, by its series.
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1.0; term > sum*1e-18; k++ {
		term *= x * x / (4 * k * k)
		sum += term
	}
	return sum
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
