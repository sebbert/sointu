package vm

import (
	"github.com/vsariola/sointu"
)

// The convolution unit convolves its input with an impulse response h read
// from an audio buffer, without latency (Gardner's scheme with a fixed
// partition):
//
//   - The head, h[0..64), is summed directly every sample.
//   - Level 0 convolves h[64..512) in blocks of 64 frames, level 1
//     h[512..4096) in blocks of 512 and level 2 the rest in blocks of 4096.
//     A level with the block size B has the partitions p = 0, 1, ... of B
//     frames each, partition p being h[B·(1+p)..B·(2+p)), and keeps the
//     spectrum of each (H), and the spectra of the last blocks of the input
//     (X), all of FFTs of 2B frames. Every B samples it transforms the last 2B
//     input samples, multiplies the spectrum of the block p blocks ago with
//     that of partition p, adds the products up and transforms the sum back.
//     The second half of the result is the output of the level for the next
//     B samples: what its part of h does to the input is at least B samples
//     late, so nothing is late.
//
// The outputs of the head and the levels are added into a ring, predelay
// frames ahead of the sample.
//
// The response is read from the buffer partition by partition (convLoad):
// all of it when the buffers of the synth are set or the unit changes. A
// buffer that is written is read again while playing. The head is read
// every sample. A level reads Follow of its partitions at each of its
// blocks, one after the other: with Follow 1, in block j partition j-2,
// which holds the frames that a oneshot bufwrite that started at sample 0
// has just written, and each partition again after as many blocks as the
// level has partitions: 448 samples for level 0, 3584 for level 1, and the
// length of the rest of the response for level 2. The block is computed
// with the partitions as they were, and what the new ones change, the
// spectrum of their block of the input times the change of theirs,
// transformed back, is added: all of it, or with Fade along a ramp from 0 to
// 1 over the block. A sample is not read again, so it has none of this.
//
// The arithmetic matches the wasm player ($su_op_convolution) operation by
// operation, in float32, with products wrapped in float32() so that they are
// not fused into multiply-adds.

const (
	// convLevels is the number of block sizes: 64, 512 and 4096 frames.
	convLevels = 3
	// convRingLog2 is the base 2 logarithm of the frames of the rings of
	// the input and the output: the input ring has to hold two of the
	// largest blocks, the output ring one and the longest predelay.
	convRingLog2 = 14
	convRing     = 1 << convRingLog2
	// convMaxBlock is the largest block.
	convMaxBlock = sointu.ConvolutionHead << (3 * (convLevels - 1))
)

// Conv is the constant data of a convolution unit: the buffer of its impulse
// response, the first frame and the number of frames of the response in it,
// the predelay in frames and the channels of the unit.
type Conv struct {
	BufferID int
	Start    uint32
	Length   uint32
	Predelay uint32
	Channels int
	// Follow is the number of partitions of each level that are read again
	// from a written buffer at each of its blocks, and Fade whether what
	// they change is faded in over the block.
	Follow uint32
	Fade   bool
}

func newConv(p sointu.ParamMap) Conv {
	return Conv{
		BufferID: p["buffer"],
		Start:    uint32(max(p["start"], 0)),
		Length:   uint32(sointu.ConvolutionLength(p["length"])),
		Predelay: uint32(sointu.ConvolutionPredelay(p["predelay"])),
		Channels: 1 + p["stereo"]&1,
		Follow:   uint32(sointu.ConvolutionFollow(p["follow"])),
		Fade:     p["fade"] == 1,
	}
}

// ConvBlock returns the block size of a level of the convolution unit.
func ConvBlock(level int) uint32 { return sointu.ConvolutionHead << (3 * level) }

// Partitions returns the number of partitions of each level: the blocks of
// the response after the head that the level convolves. A level without
// partitions does nothing.
func (c Conv) Partitions() (ret [convLevels]uint32) {
	for l := range ret {
		b, end := ConvBlock(l), c.Length
		if l+1 < convLevels {
			end = min(end, ConvBlock(l+1))
		}
		if end > b {
			ret[l] = (end - 1) / b // ⌈(end-b)/b⌉
		}
	}
	return
}

// convStride is the number of floats of a spectrum of a level with block
// size b: the real parts of bins 0 to b and then their imaginary parts, each
// padded to a multiple of 4 floats, b+4, which the wasm player adds up 4 at
// a time.
func convStride(b uint32) uint32 { return 2 * (b + 4) }

// convState is the state of a convolution unit in a voice. Like those of
// ott, the synths keep the states of all of them in a table of their own,
// in the order the units run, voice by voice.
type convState struct {
	pos  uint32      // the sample
	conv Conv        // what the state is allocated for
	ch   [2]convChan // of the channels of the unit
}

// convChan is the state of a channel: the rings of the input and the output,
// the head of the response, and for each level the spectra of its partitions
// of the response (h) and of the last blocks of the input (x), one for each
// partition.
type convChan struct {
	in, out []float32
	head    [sointu.ConvolutionHead]float32
	h, x    [convLevels][]float32
}

func (st *convState) alloc(cv *Conv) {
	*st = convState{conv: *cv}
	for c := range cv.Channels {
		ch := &st.ch[c]
		ch.in, ch.out = make([]float32, convRing), make([]float32, convRing)
		for l, p := range cv.Partitions() {
			ch.h[l] = make([]float32, p*convStride(ConvBlock(l)))
			ch.x[l] = make([]float32, p*convStride(ConvBlock(l)))
		}
	}
}

// loadConvs reads the impulse responses of all convolution units from their
// buffers, and allocates the states that are new or whose unit changed.
// With all false, it leaves the units alone that it has read before.
func (s *GoSynth) loadConvs(all bool) {
	for len(s.convs) < len(s.bytecode.ConvStates) {
		s.convs = append(s.convs, convState{})
	}
	for i, index := range s.bytecode.ConvStates {
		cv, st := &s.bytecode.Convs[index], &s.convs[i]
		if st.conv == *cv && st.ch[0].in != nil && !all {
			continue
		}
		if st.conv != *cv || st.ch[0].in == nil {
			st.alloc(cv)
		}
		buf := s.buffers[cv.BufferID]
		for c := range cv.Channels {
			s.convLoad(cv, st, buf, c, -1, 0)
			for l, parts := range cv.Partitions() {
				for p := range parts {
					s.convLoad(cv, st, buf, c, l, p)
				}
			}
		}
	}
}

// convScratch returns the scratch space of the convolution units: room for
// the FFT of the largest block, of two of its lengths and a value more, and
// for three spectra: the sum of the products, the spectrum that a partition
// had before it was read again and the sum of what the partitions change.
func (s *GoSynth) convScratch() (x, acc []float32) {
	const n = 2 * convMaxBlock
	if s.convX == nil {
		s.convX, s.convAcc = make([]float32, 2*n+2), make([]float32, 3*convStride(convMaxBlock))
	}
	return s.convX, s.convAcc
}

// convFrame returns frame k of the response of channel c: silence beyond
// the length of the response and where the buffer has no valid frame.
func convFrame(cv *Conv, buf *synthBuffer, c int, k uint32) float32 {
	if buf == nil || k >= cv.Length {
		return 0
	}
	f := uint64(cv.Start) + uint64(k)
	if f >= uint64(buf.filled) || f >= uint64(buf.audio.Frames()) {
		return 0
	}
	return buf.audio.Data[int(f)*buf.audio.Channels+min(c, buf.audio.Channels-1)]
}

// convLoad reads a part of the response of channel c from the buffer: with
// level -1 the head, otherwise partition p of the level, whose spectrum it
// computes.
func (s *GoSynth) convLoad(cv *Conv, st *convState, buf *synthBuffer, c, level int, p uint32) {
	ch := &st.ch[c]
	if level < 0 {
		for k := range ch.head {
			ch.head[k] = convFrame(cv, buf, c, uint32(k))
		}
		return
	}
	b := ConvBlock(level)
	x, _ := s.convScratch()
	x = x[:4*b]
	clear(x)
	for i := range b {
		x[2*i] = convFrame(cv, buf, c, b*(1+p)+i)
	}
	fft(x, 2*b)
	convSplit(ch.h[level][p*convStride(b):], x, b)
}

// convSplit stores bins 0 to b of the spectrum x, interleaved, as their real
// parts followed, from b+4, by their imaginary parts.
func convSplit(dst, x []float32, b uint32) {
	for k := uint32(0); k <= b; k++ {
		dst[k], dst[b+4+k] = x[2*k], x[2*k+1]
	}
}

// convMultiply adds, for each bin, the product of the spectra xs and hs to
// acc, or with a third spectrum old, the product of xs and hs - old.
func convMultiply(acc, xs, hs, old []float32, b uint32) {
	ar, ai := acc[:b+4], acc[b+4:][:b+4]
	xr, xi, hr, hi := xs[:b+4], xs[b+4:][:b+4], hs[:b+4], hs[b+4:][:b+4]
	if old == nil {
		for k := range ar {
			ar[k] += float32(xr[k]*hr[k]) - float32(xi[k]*hi[k])
			ai[k] += float32(xr[k]*hi[k]) + float32(xi[k]*hr[k])
		}
		return
	}
	or, oi := old[:b+4], old[b+4:][:b+4]
	for k := range ar {
		dr, di := hr[k]-or[k], hi[k]-oi[k]
		ar[k] += float32(xr[k]*dr) - float32(xi[k]*di)
		ai[k] += float32(xr[k]*di) + float32(xi[k]*dr)
	}
}

// convBack transforms the spectrum acc back and adds the second half of the
// signal, the b samples that the level puts out, to the ring out from frame
// at on: sample i times base + i·step.
func convBack(x, acc, out []float32, at, b uint32, base, step float32) {
	// the real part of the inverse FFT of a spectrum Y is that of the FFT
	// of its conjugate, over the size. The bins above b mirror those below
	// it; bin b is written last, as the conjugate
	ar, ai := acc[:b+4], acc[b+4:]
	for k := uint32(0); k <= b; k++ {
		x[2*(2*b-k)], x[2*(2*b-k)+1] = ar[k], ai[k]
		x[2*k], x[2*k+1] = ar[k], -ai[k]
	}
	fft(x[:4*b], 2*b)
	for i := range b {
		out[(at+i)&(convRing-1)] += float32(x[2*(b+i)] * (float32(float32(i)*step) + base))
	}
}

// convolution replaces the signals on top of the stack, one for each channel
// of the unit, with their convolution with the response of the unit, times
// gain, plus the signals times dry.
func (s *GoSynth) convolution(cv *Conv, st *convState, gain, dry float32, stack []float32) {
	if st.ch[0].in == nil || st.conv != *cv {
		st.alloc(cv) // without a response: SetBuffers and Update read it
	}
	const mask = convRing - 1
	n := st.pos
	buf := s.buffers[cv.BufferID]
	scan := buf != nil && buf.audio.Writable
	for c := range cv.Channels {
		ch := &st.ch[c]
		for l, parts := range cv.Partitions() {
			b := ConvBlock(l)
			if parts == 0 || n&(b-1) != 0 {
				continue
			}
			stride := convStride(b)
			x, scratch := s.convScratch()
			acc, old, diff := scratch[:stride], scratch[stride:2*stride], scratch[2*stride:3*stride]
			j, q := n/b, n/b%parts
			// the spectrum of the last two blocks of the input
			for i := range 2 * b {
				x[2*i], x[2*i+1] = ch.in[(n-2*b+i)&mask], 0
			}
			fft(x[:4*b], 2*b)
			convSplit(ch.x[l][q*stride:], x, b)
			// the sum over the partitions of the spectrum of the block p
			// blocks ago times that of partition p
			clear(acc)
			for p := range parts {
				convMultiply(acc, ch.x[l][(q+parts-p)%parts*stride:][:stride], ch.h[l][p*stride:][:stride], nil, b)
			}
			scale := 0.5 / float32(b)
			convBack(x, acc, ch.out, n+cv.Predelay, b, scale, 0)
			if !scan {
				continue
			}
			// the partitions that the scan passes are read again: the
			// one whose frames a oneshot bufwrite that started at sample
			// 0 has just written, and with follow those after it. What
			// that changes in this block, the spectrum of the block of
			// the input that each belongs to times how its spectrum
			// changed, is added: all of it, or with fade along a ramp
			clear(diff)
			for i := range min(cv.Follow, parts) {
				p := ((j+2*parts-2)*cv.Follow + i) % parts
				h := ch.h[l][p*stride:][:stride]
				copy(old, h)
				s.convLoad(cv, st, buf, c, l, p)
				convMultiply(diff, ch.x[l][(q+parts-p)%parts*stride:][:stride], h, old, b)
			}
			if cv.Fade {
				convBack(x, diff, ch.out, n+cv.Predelay, b, 0, scale/float32(b))
			} else {
				convBack(x, diff, ch.out, n+cv.Predelay, b, scale, 0)
			}
		}
		if scan {
			s.convLoad(cv, st, buf, c, -1, 0) // the head follows the buffer
		}
		in := &stack[len(stack)-1-c]
		ch.in[n&mask] = *in
		var sum float32
		for k, h := range ch.head {
			sum += float32(h * ch.in[(n-uint32(k))&mask])
		}
		ch.out[(n+cv.Predelay)&mask] += sum
		y := float32(ch.out[n&mask] * gain)
		ch.out[n&mask] = 0
		if dry != 0 {
			y += float32(*in * dry)
		}
		*in = y
	}
	st.pos = n + 1
}

// ConvStateBytes returns the size of the state of a convolution unit in the
// wasm player: the sample and 4 unused bytes, and for each channel the rings
// of the input and the output, the head, and for each level the spectra of
// its partitions and as many of the input.
func (c Conv) ConvStateBytes() int { return 8 + c.Channels*c.ConvChannelBytes() }

// ConvChannelBytes returns the size of a channel of that state.
func (c Conv) ConvChannelBytes() int {
	n := 2*4*convRing + 4*sointu.ConvolutionHead
	for l, p := range c.Partitions() {
		n += 2 * int(p) * 4 * int(convStride(ConvBlock(l)))
	}
	return n
}

// NewConv returns the constant data of a convolution unit with the
// parameters p.
func NewConv(p sointu.ParamMap) Conv { return newConv(p) }
