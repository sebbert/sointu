package vm

import (
	"math"
	"math/rand"
	"testing"

	"github.com/vsariola/sointu"
)

// convTestSynth returns a synth with one convolution state for cv, and the
// buffer of its response.
func convTestSynth(cv Conv, audio sointu.BufferAudio) *GoSynth {
	s := &GoSynth{bytecode: Bytecode{Convs: []Conv{cv}, ConvStates: []int{0}}}
	s.SetBuffers(map[int]sointu.BufferAudio{cv.BufferID: audio})
	return s
}

// convRun convolves in, a signal for each channel of the unit, with the
// response of the synth's convolution unit.
func convRun(s *GoSynth, in [][]float32, gain, dry float32) [][]float32 {
	cv, st := &s.bytecode.Convs[0], &s.convs[0]
	out := make([][]float32, len(in))
	for c := range out {
		out[c] = make([]float32, len(in[c]))
	}
	stack := make([]float32, len(in))
	for i := range in[0] {
		for c := range in {
			stack[len(in)-1-c] = in[c][i]
		}
		s.convolution(cv, st, gain, dry, stack)
		for c := range in {
			out[c][i] = stack[len(in)-1-c]
		}
	}
	return out
}

func convNoise(rng *rand.Rand, n int, decay float64) []float32 {
	ret := make([]float32, n)
	for i := range ret {
		ret[i] = float32((rng.Float64()*2 - 1) * math.Exp(-decay*float64(i)/float64(n)))
	}
	return ret
}

// convReference returns x convolved with h, delayed, in float64, at the
// samples from.. of every step-th sample, and the largest value.
func convReference(x, h []float32, delay, i int) float64 {
	sum := 0.0
	for k, v := range h {
		if j := i - delay - k; j >= 0 && j < len(x) {
			sum += float64(v) * float64(x[j])
		}
	}
	return sum
}

// TestConvolutionMatchesDirectSum checks the unit against the convolution
// sum, for responses that end in the head, in each level, and at and next
// to the ends of blocks, mono and stereo, with a start and a predelay.
func TestConvolutionMatchesDirectSum(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, c := range []struct {
		length, frames, bufCh, ch int
		start, predelay           uint32
	}{
		{64, 64, 1, 1, 0, 0},
		{64, 40, 1, 1, 0, 0}, // a buffer shorter than the response
		{70, 200, 1, 1, 3, 0},
		{128, 128, 1, 1, 0, 0},
		{512, 600, 2, 2, 17, 0},
		{558, 558, 1, 2, 0, 64},
		{4096, 4096, 2, 1, 0, 0},
		{4467, 5000, 1, 1, 100, 0},
		{8192, 8192, 1, 1, 0, 8128},
		{26909, 30000, 2, 2, 1000, 640},
	} {
		audio := sointu.BufferAudio{Channels: c.bufCh, Data: convNoise(rng, c.frames*c.bufCh, 3)}
		cv := Conv{BufferID: 5, Start: c.start, Length: uint32(c.length), Predelay: c.predelay, Channels: c.ch}
		s := convTestSynth(cv, audio)
		n := 3*c.length + 20000
		in := make([][]float32, c.ch)
		for j := range in {
			in[j] = convNoise(rng, n, 0)
			clear(in[j][n-c.length-9000:]) // and the tail after the input ends
		}
		out := convRun(s, in, 1, 0)
		for j := range in {
			bc := min(j, c.bufCh-1)
			h := make([]float32, 0, c.length)
			for k := range c.length {
				if f := int(c.start) + k; f < c.frames {
					h = append(h, audio.Data[f*c.bufCh+bc])
				}
			}
			maxErr, peak := 0.0, 0.0
			for i := 0; i < n; i += 1 + i/997%7 {
				want := convReference(in[j], h, int(c.predelay), i)
				maxErr = max(maxErr, math.Abs(want-float64(out[j][i])))
				peak = max(peak, math.Abs(want))
			}
			if peak < 1 || maxErr > 2e-5*peak {
				t.Errorf("%+v channel %d: differs from the sum by %g, peak %g", c, j, maxErr, peak)
			}
		}
	}
}

// TestConvolutionGainAndDry checks the gain of the wet signal and the dry
// signal: an impulse as the response passes the input.
func TestConvolutionGainAndDry(t *testing.T) {
	cv := Conv{BufferID: 1, Length: 600, Channels: 1}
	s := convTestSynth(cv, sointu.BufferAudio{Channels: 1, Data: []float32{1}})
	in := [][]float32{convNoise(rand.New(rand.NewSource(2)), 3000, 0)}
	out := convRun(s, in, 0.5, 0.25)
	for i, x := range in[0] {
		if want := float32(x*0.5) + float32(x*0.25); out[0][i] != want {
			t.Fatalf("sample %d is %v, want %v", i, out[0][i], want)
		}
	}
}

// convWritten runs a convolution unit whose buffer is written while it runs:
// write is called before each sample with the buffer.
func convWritten(cv Conv, frames int, in []float32, write func(i int, buf *synthBuffer)) []float32 {
	s := convTestSynth(cv, sointu.BufferAudio{Channels: 1, Data: make([]float32, frames), Writable: true})
	out, stack := make([]float32, len(in)), []float32{0}
	for i := range in {
		write(i, s.buffers[cv.BufferID])
		stack[0] = in[i]
		s.convolution(&s.bytecode.Convs[0], &s.convs[0], 1, 0, stack)
		out[i] = stack[0]
	}
	return out
}

// TestConvolutionFollowsOneshot checks that a response written from the
// first sample on, a frame for each sample as a oneshot bufwrite does, is
// read as it is written: input that starts two of the largest blocks later
// gets all of it (a partition is read a block after its first frame is
// written, and used for the input from a block before that on). And that a
// buffer cleared by a new recording is silent once the scan has been around.
func TestConvolutionFollowsOneshot(t *testing.T) {
	const length = 10700
	for _, fade := range []bool{false, true} {
		rng := rand.New(rand.NewSource(3))
		cv := Conv{BufferID: 1, Length: length, Channels: 1, Follow: 1, Fade: fade}
		h := convNoise(rng, length, 3)
		n := 5 * convMaxBlock
		in := convNoise(rng, n+3*length, 0)
		clear(in[:2*convMaxBlock])
		out := convWritten(cv, length, in, func(i int, buf *synthBuffer) {
			if i < length {
				buf.audio.Data[i] = h[i]
				buf.head, buf.filled = uint32(i+1), uint32(i+1)
			}
			if i == n {
				buf.head, buf.filled = 0, 0
			}
		})
		maxErr, peak := 0.0, 0.0
		for i := 0; i < n; i += 7 {
			want := convReference(in, h, 0, i)
			maxErr = max(maxErr, math.Abs(want-float64(out[i])))
			peak = max(peak, math.Abs(want))
		}
		if peak < 1 || maxErr > 2e-5*peak {
			t.Errorf("fade %v: differs from the sum by %g, peak %g", fade, maxErr, peak)
		}
		for i := n + 2*length; i < len(out); i++ {
			if out[i] != 0 {
				t.Fatalf("fade %v: sample %d after the buffer was cleared is %v", fade, i-n, out[i])
			}
		}
	}
}

// TestConvolutionFollowAndFade changes a part of a written response while
// the unit runs: frames 512 to 1024, the one partition of level 1, which is
// read at every block of 512 samples. Without fade the block after the
// change is convolved with the new response; with fade it goes from the old
// to the new one along a ramp, and the block after it has the new one.
func TestConvolutionFollowAndFade(t *testing.T) {
	const length, at, n = 1024, 10*512 + 100, 16 * 512
	rng := rand.New(rand.NewSource(4))
	before, in := convNoise(rng, length, 2), convNoise(rng, n, 0)
	after := append([]float32{}, before...)
	copy(after[512:], convNoise(rng, 512, 2))
	for _, c := range []struct {
		follow uint32
		fade   bool
	}{{1, false}, {1, true}, {8, false}, {8, true}} {
		cv := Conv{BufferID: 1, Length: length, Channels: 1, Follow: c.follow, Fade: c.fade}
		out := convWritten(cv, length, in, func(i int, buf *synthBuffer) {
			switch i {
			case 0:
				copy(buf.audio.Data, before)
				buf.head, buf.filled = length, length
			case at:
				copy(buf.audio.Data, after)
			}
		})
		maxErr, peak := 0.0, 0.0
		for i := 4 * 512; i < n; i++ {
			old, new := convReference(in, before, 0, i), convReference(in, after, 0, i)
			// the first 512 frames are the same, and the change is read
			// at the next block, sample 11·512
			w := 0.0
			if i >= 12*512 || !c.fade && i >= 11*512 {
				w = 1
			} else if i >= 11*512 {
				w = float64(i-11*512) / 512
			}
			want := old + w*(new-old)
			maxErr = max(maxErr, math.Abs(want-float64(out[i])))
			peak = max(peak, math.Abs(new-old))
		}
		if peak < 1 || maxErr > 2e-5*peak {
			t.Errorf("%+v: differs by %g, the responses by %g", c, maxErr, peak)
		}
	}
}

// TestConvolutionHeadFollows checks that the first 64 frames of a written
// response are those of the buffer in every sample.
func TestConvolutionHeadFollows(t *testing.T) {
	cv := Conv{BufferID: 1, Length: 64, Channels: 1, Follow: 1}
	in := make([]float32, 1000)
	for i := range in {
		in[i] = 1
	}
	out := convWritten(cv, 64, in, func(i int, buf *synthBuffer) {
		buf.audio.Data[0] = float32(i)
		buf.head, buf.filled = 64, 64
	})
	for i, v := range out {
		if v != float32(i) {
			t.Fatalf("sample %d is %v", i, v)
		}
	}
}

func TestConvolutionPartitions(t *testing.T) {
	for _, c := range []struct {
		length uint32
		want   [convLevels]uint32
	}{
		{64, [convLevels]uint32{}},
		{65, [convLevels]uint32{1}},
		{512, [convLevels]uint32{7}},
		{513, [convLevels]uint32{7, 1}},
		{4096, [convLevels]uint32{7, 7}},
		{4097, [convLevels]uint32{7, 7, 1}},
		{524288, [convLevels]uint32{7, 7, 127}},
	} {
		cv := Conv{Length: c.length}
		if got := cv.Partitions(); got != c.want {
			t.Errorf("length %d: partitions %v, want %v", c.length, got, c.want)
		}
	}
}

func BenchmarkConvolution(b *testing.B) {
	for _, c := range []struct {
		name     string
		length   int
		channels int
		follow   uint32 // 0 for a sample, which is not read again
		fade     bool
	}{
		{"64", 64, 1, 0, false}, {"512", 512, 1, 0, false}, {"4096", 4096, 1, 0, false}, {"1s", 44100, 1, 0, false},
		{"3s", 131072, 1, 0, false}, {"3s stereo", 131072, 2, 0, false}, {"12s", 524288, 1, 0, false},
		{"64 written", 64, 1, 1, false}, {"512 written", 512, 1, 1, false}, {"3s written", 131072, 1, 1, false},
		{"3s written fade", 131072, 1, 1, true}, {"3s written 8x fade", 131072, 1, 8, true}, {"12s stereo written fade", 524288, 2, 1, true},
	} {
		b.Run(c.name, func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			cv := Conv{BufferID: 1, Length: uint32(c.length), Channels: c.channels, Follow: c.follow, Fade: c.fade}
			s := convTestSynth(cv, sointu.BufferAudio{Channels: 1, Data: convNoise(rng, c.length, 3), Writable: c.follow > 0})
			s.buffers[1].filled = uint32(c.length)
			s.loadConvs(true)
			stack := make([]float32, c.channels)
			b.ResetTimer()
			for i := range b.N {
				stack[0] = float32(i&255) / 256
				s.convolution(&s.bytecode.Convs[0], &s.convs[0], 1, 0, stack)
			}
		})
	}
}
