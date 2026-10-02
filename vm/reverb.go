package vm

import (
	"github.com/vsariola/sointu"
)

// The reverb unit is the Reverb module preset (tracker/modules/Reverb.yml)
// as one opcode: what the 23 units of the module compute, in the same
// operations and the same order, so that it renders what the module renders,
// without what the mc units need to be flexible: no bus, no table of units,
// one function. What the module leaves to its units is fixed here: 8
// channels, the seeds of the lengths and of the shuffles, the mixes, the
// gains and widths of the two sums, the rate of the modulation.
//
// Three things are computed differently, with the same result for finite
// signals (a zero can come out with the other sign):
//
//   - The 8 channels are two signals, left and right, with the polarities
//     of mcspread, until the first step of the diffuser delays them by
//     different lengths. So the low cut, the high cut and the predelay work
//     on left and right only, and the predelay and the first step of the
//     diffuser are one stereo ring, read at the sum of their lengths.
//   - The delays of the diffuser have whole lengths and no decay: they are
//     read without interpolation and without the decay filter. The shuffle
//     after each is in the taps: channel c of the step reads the channel
//     that the shuffle moves to c, and flips its sign bit.
//   - The Householder mix of the network, x - Σx/4, is not stored: the
//     outputs of the lines and Σx/4 are kept, and subtracted when the lines
//     are fed in the next sample.
//
// The lengths of the lines and the coefficients of their decay are computed
// when the patch is encoded, by the code of mcdelay, and read from a table by
// the players.

const (
	// reverbStereoLog2 is the base 2 logarithm of the frames of the stereo
	// ring of the predelay and the first step of the diffuser: 200 ms and
	// 70 ms fit. The rings of the other steps have 2^11, 2^10 and 2^9
	// frames of 8 channels.
	reverbStereoLog2 = 14
	// reverbLog2Frames is the base 2 logarithm of the frames of the ring
	// of the network, for 280 ms and 32 ms of modulation.
	reverbLog2Frames = 14
	// reverbSteps is the number of steps of the diffuser.
	reverbSteps = 4
)

var (
	// reverbGain is the gain of the input (gain 76 of mcspread, +7.5 dB),
	// reverbEarlyGain the gain of the early reflections (gain 52 of mcsum,
	// -7.5 dB), reverbRate the rate of the modulation in turns per sample
	// (modrate 56 of mcdelay, 0.71 Hz). The wasm player has them as
	// constants.
	reverbGain      = mcGain(76.0 / 128)
	reverbEarlyGain = mcGain(52.0 / 128)
	reverbRate      = float32(exp2f(float32(float32(56.0/128)*8)-4) * 2.2675737e-05)

	// reverbTaps are the taps of the steps of the diffuser: for channel c
	// of step k, the channel of the mcdelay of the step that it reads, which
	// the shuffle of the step (mcmix of type shuffle with seed k+1) moves
	// to c, the byte offset of that channel in a frame of the ring of the
	// step, and whether its sign is flipped: by the shuffle, and in the
	// first step, which reads the stereo ring, also by the polarities of
	// mcspread.
	reverbTaps [reverbSteps][sointu.MCChannels]struct {
		source, offset int
		negate         bool
	}
)

func init() {
	for k := range reverbTaps {
		s := newMCShuffle(k + 1)
		for c := range reverbTaps[k] {
			tap := &reverbTaps[k][c]
			tap.source, tap.offset, tap.negate = s.Source[c], 4*s.Source[c], s.Sign[c] < 0
			if k == 0 {
				// the stereo ring: left on the even channels, right on
				// the odd ones, channels 2, 3, 6 and 7 negated
				tap.offset = 4 * (tap.source & 1)
				tap.negate = tap.negate != (tap.source&2 != 0)
			}
		}
	}
}

// reverbFrameBytes is the size of a frame of the ring of step k of the
// diffuser in the wasm player: two floats in the first, the stereo ring,
// and 8 in the others.
func reverbFrameBytes(k int) int {
	if k == 0 {
		return 8
	}
	return 4 * sointu.MCChannels
}

// Reverb is the constant data of a reverb unit: the coefficients of the
// decay of the lines of the network (see mcDecay) and their lengths in
// samples, and the taps of the diffuser. A tap is how far behind the frame
// being written it reads its ring, in floats, twice, with bit 0 set if it
// flips the sign: the delay of its channel, in the first step plus the
// predelay, in frames, minus the offset of the channel in a frame.
type Reverb struct {
	A, B, C, Lengths [sointu.MCChannels]float32
	Taps             [reverbSteps][sointu.MCChannels]uint16
}

// newReverb computes the constant data of a reverb unit from its
// parameters, as the mcdelay units of the Reverb module get theirs.
func newReverb(p sointu.ParamMap) Reverb {
	var r Reverb
	size := p["size"]
	delay := func(seed, size, spread int) *MCDelay {
		return newMCDelay(sointu.ParamMap{"size": size, "spread": spread, "seed": seed, "hfdecay": 64, "lfdecay": 64})
	}
	predelay := delay(0, sointu.ReverbScale(p["predelay"], 1, 2000), 0).Lengths[0]
	for k, s := range [reverbSteps][2]int{{100, 700}, {50, 350}, {25, 175}, {12, 88}} {
		d := delay(k+1, sointu.ReverbScale(size, s[0], s[1]), 128)
		for c, tap := range reverbTaps[k] {
			l := d.Lengths[tap.source]
			if k == 0 {
				l += predelay
			}
			r.Taps[k][c] = uint16((int(l)*reverbFrameBytes(k) - tap.offset) / 4 << 1)
			if tap.negate {
				r.Taps[k][c] |= 1
			}
		}
	}
	d := newMCDelay(sointu.ParamMap{"size": sointu.ReverbScale(size, 400, 2800), "spread": 77, "seed": 7,
		"decay": p["decay"], "hfdecay": p["highs"], "lfdecay": p["lows"]})
	r.A, r.B, r.C, r.Lengths = d.A, d.B, d.C, d.Lengths
	return r
}

// reverbState is the state of a reverb unit in a voice. It does not fit in
// a unit, so the synths keep the states of all reverbs in a table of their
// own, one after the other in the order the units run (voice by voice), like
// those of ott. They are not cleared when a note is triggered.
type reverbState struct {
	t               uint32                     // the sample
	sum             float32                    // Σx/4 of the outputs of the lines
	lowcut, highcut [2]float32                 // the states of the filters of the input
	out             [sointu.MCChannels]float32 // the outputs of the lines
	phase, lo, hi   [sointu.MCChannels]float32 // of the lines: the phase of the modulation, the decay filters
	bus             [sointu.MCChannels]float32 // the diffused input
	rings           [reverbSteps][]float32     // of the steps of the diffuser: the stereo ring, then frames of 8 channels
	network         []float32                  // the ring of the network, frames of 8 channels
}

func (st *reverbState) alloc() {
	st.rings[0] = make([]float32, 2<<reverbStereoLog2)
	for k := 1; k < reverbSteps; k++ {
		st.rings[k] = make([]float32, sointu.MCChannels<<(reverbStereoLog2-2-k))
	}
	st.network = make([]float32, sointu.MCChannels<<reverbLog2Frames)
}

// reverbCoef is the coefficient of the one-pole filters of the input, as
// that of mcfilter: 1 - 2^(-2π·20·2^(10f)/44100·log2(e)).
func reverbCoef(f float32) float32 {
	return 1 - exp2f(float32(exp2f(float32(f*10))*-0.004110984))
}

// reverbHalf returns x[c] + x[c+4].
func reverbHalf(x *[sointu.MCChannels]float32, c int) float32 { return x[c] + x[c+4] }

// reverbSum adds the sum of the 8 channels to the stereo signal on top of
// the stack, as a stereo mcsum followed by a stereo addp: the even channels
// to the left and the odd ones to the right, with the polarities of
// mcspread, the side signal times width, all times gain.
func reverbSum(x *[sointu.MCChannels]float32, width, gain float32, stack []float32) {
	n := len(stack)
	l := reverbHalf(x, 0) - reverbHalf(x, 2)
	r := reverbHalf(x, 1) - reverbHalf(x, 3)
	mid := float32(float32(l+r) * 0.125)
	side := float32(float32(float32(l-r)*0.125) * width)
	stack[n-1] += float32(float32(mid+side) * gain)
	stack[n-2] += float32(float32(mid-side) * gain)
}

// reverb replaces the stereo signal on top of the stack with its reverb. p
// holds mod, highcut and lowcut. Matches $su_op_reverb in the wasm player,
// operation by operation.
func reverb(r *Reverb, st *reverbState, p *[8]float32, stack []float32) {
	if st.network == nil {
		st.alloc()
	}
	n := len(stack)
	t := st.t
	// the input, times the gain, through the low cut and the high cut into
	// the stereo ring
	a, b := reverbCoef(p[2]), reverbCoef(p[1])
	for j := range 2 {
		x := float32(stack[n-1-j] * reverbGain)
		lo := st.lowcut[j] + float32(a*(x-st.lowcut[j]))
		st.lowcut[j] = lo
		x -= lo
		lo = st.highcut[j] + float32(b*(x-st.highcut[j]))
		st.highcut[j] = lo
		st.rings[0][(t&(1<<reverbStereoLog2-1))*2+uint32(j)] = lo
		stack[n-1-j] = 0
	}
	// the diffuser: each step reads the 8 channels from its ring, delayed
	// and shuffled, mixes them and writes them to the ring of the next
	// step; the last one to the bus
	for k := range reverbSteps {
		ring := st.rings[k]
		x := &st.bus
		if k+1 < reverbSteps {
			x = (*[sointu.MCChannels]float32)(st.rings[k+1][(t&(1<<(reverbStereoLog2-3-k)-1))*sointu.MCChannels:])
		}
		at := t * uint32(reverbFrameBytes(k)/4) // frame t, in floats
		for c, tap := range r.Taps[k] {
			y := ring[(at-uint32(tap>>1))&uint32(len(ring)-1)]
			if tap&1 != 0 {
				y = -y
			}
			x[c] = y
		}
		mcmix(x, sointu.MCMixHadamard, nil)
	}
	// the network: each line is fed the diffused input plus the Householder
	// mix of the outputs of the lines in the last sample, and read at its
	// length plus the modulation, through its decay filter
	depth := float32(float32(p[0]*p[0]) * 352.8)
	mask := uint32(1)<<reverbLog2Frames - 1
	for c := range st.out {
		st.network[(t&mask)*sointu.MCChannels+uint32(c)] = st.bus[c] + (st.out[c] - st.sum)
		rate := float32(float32(c)*0.125) + 1
		phase := st.phase[c] + float32(reverbRate*rate)
		phase -= floor32(phase)
		st.phase[c] = phase
		tri := phase + float32(float32(c)*0.125)
		tri -= floor32(tri)
		tri = abs32(float32(tri*2) - 1)
		delay := r.Lengths[c] + float32(depth*tri)
		delay = min(max(delay, 1), float32(mask-1))
		i := uint32(int32(delay))
		f := delay - float32(int32(i))
		ya := st.network[((t-i)&mask)*sointu.MCChannels+uint32(c)]
		yb := st.network[((t-i-1)&mask)*sointu.MCChannels+uint32(c)]
		y := ya + float32(f*float32(yb-ya))
		lo := st.lo[c] + float32(mcDecayLow*float32(y-st.lo[c]))
		st.lo[c] = lo
		y += float32(r.A[c] * lo)
		hi := st.hi[c] + float32(mcDecayHigh*float32(y-st.hi[c]))
		st.hi[c] = hi
		st.out[c] = float32(r.B[c]*y) + float32(r.C[c]*hi)
	}
	st.sum = float32(float32(float32(reverbHalf(&st.out, 0)+reverbHalf(&st.out, 2))+float32(reverbHalf(&st.out, 1)+reverbHalf(&st.out, 3))) * 0.25)
	// the early reflections, the diffused input, and the tail, the outputs
	// of the lines
	reverbSum(&st.bus, 1.25, reverbEarlyGain, stack)
	reverbSum(&st.out, 1.5, 1, stack)
	st.t = t + 1
}
