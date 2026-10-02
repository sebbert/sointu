package vm

import (
	"github.com/vsariola/sointu"
)

// The reverb unit is the Reverb module preset (tracker/modules/Reverb.yml)
// as one opcode: what the 23 units of the module compute, in the same
// operations and the same order, so that it renders what the module renders,
// without what the mc units need to be flexible: no bus, no table of units,
// one function. Of what the module leaves to its units, this is fixed here:
// 8 channels, the seeds of the lengths and of the shuffles, the mixes. The
// gains and widths of the two sums, the rate of the modulation, the number
// of steps of the diffuser, the lengths and the filters of the input are
// parameters of the unit, with the values of the module as defaults; the
// wasm player has the code and the data for them only in songs that set
// them. Two more parts are only in units that use them: the delays of the
// diffuser as allpasses, and a second set of lines in the network. A unit
// with allpasses does not read the stereo ring in its first step: each
// channel has its own allpass, so the predelayed input is spread on a ring
// of 8 channels first.
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
)

// The levels of a reverb unit, in Reverb.Levels.
const (
	reverbGain       = iota // the gain of the input, as that of mcspread
	reverbEarlyGain         // the gain of the early reflections, as that of mcsum
	reverbEarlyWidth        // twice the width of the early reflections
	reverbTailWidth         // twice the width of the tail
	reverbRate              // the rate of the modulation, in turns per sample
	reverbLevels
)

// reverbShuffles are the shuffles after the steps of the diffuser: those of
// mcmix of type shuffle with the seeds 1 to 4.
var reverbShuffles [sointu.ReverbSteps]*MCShuffle

func init() {
	for k := range reverbShuffles {
		reverbShuffles[k] = newMCShuffle(k + 1)
	}
}

// Reverb is the constant data of a reverb unit: the coefficients of the
// decay of the lines of the network (see mcDecay) and their lengths in
// samples, and the taps of the diffuser. A tap is how far behind the frame
// being written it reads its ring, in floats, twice, with bit 0 set if it
// flips the sign: the delay of its channel, in the first step plus the
// predelay, in frames, minus the offset of the channel in a frame.
//
// Levels are the gains, widths and the rate of the modulation, End is 32
// times the number of steps of the diffuser, and Bypass tells which filters
// of the input the unit leaves out. With the defaults of the parameters
// that set them, they are what the Reverb module has, and the wasm player
// has them as constants.
//
// APGain is the coefficient of the allpasses of the diffuser, 0 for plain
// delays. A unit with allpasses reads the stereo ring Predelay bytes behind
// (8 for each sample), and its first step a ring of 8 channels like the
// others. Loop is the second set of lines of the network, if its first
// length is not 0.
type Reverb struct {
	A, B, C, Lengths [sointu.MCChannels]float32
	Taps             [sointu.ReverbSteps][sointu.MCChannels]uint16
	Levels           [reverbLevels]float32
	End, Bypass      uint8
	APGain           float32
	Predelay         uint32
	Loop             ReverbLoop
}

// ReverbLoop is the second set of lines of the network of a reverb unit: the
// coefficients of their decay and their lengths, their allpass coefficient,
// the rate of their modulation in turns per sample and its depth in samples.
type ReverbLoop struct {
	A, B, C, Lengths  [sointu.MCChannels]float32
	Gain, Rate, Depth float32
}

// newReverb computes the constant data of a reverb unit from its
// parameters, as the mcdelay units of the Reverb module get theirs.
func newReverb(p sointu.ParamMap) Reverb {
	var r Reverb
	size := p["size"]
	delay := func(seed, size, spread int) *MCDelay {
		return newMCDelay(sointu.ParamMap{"size": size, "spread": spread, "seed": seed, "hfdecay": 64, "lfdecay": 64})
	}
	level := func(name string) float32 { return float32(min(max(p[name], 0), 128)) / 128 }
	r.Levels = [reverbLevels]float32{
		reverbGain:       mcGain(level("gain")),
		reverbEarlyGain:  mcGain(level("early")),
		reverbEarlyWidth: float32(level("earlywidth") * 2),
		reverbTailWidth:  float32(level("tailwidth") * 2),
		reverbRate:       float32(exp2f(float32(level("modrate")*8)-4) * 2.2675737e-05),
	}
	steps := min(max(p["steps"], 1), sointu.ReverbSteps)
	r.End = uint8(4 * sointu.MCChannels * steps)
	r.Bypass = uint8(p["bypass"] & (sointu.ReverbBypassLowcut | sointu.ReverbBypassHighcut))
	apgain := min(max(p["allpass"], 0), sointu.MCAllpassGainMax)
	r.APGain = float32(float64(apgain) / 128) // as that of mcdelay
	var predelay float32
	if p["bypass"]&sointu.ReverbBypassPredelay == 0 {
		size := sointu.ReverbScale(p["predelay"], 1, 2000)
		if p["pretime"] > 0 {
			size = min(p["pretime"], 2000)
		}
		predelay = delay(0, size, 0).Lengths[0]
	}
	if apgain > 0 {
		r.Predelay, predelay = 8*uint32(predelay), 0
	}
	for k, s := range [][2]int{{100, 700}, {50, 350}, {25, 175}, {12, 88}}[:steps] {
		stepSize := sointu.ReverbScale(size, s[0], s[1])
		if p["diffuser"] > 0 {
			stepSize = max(min(p["diffuser"], 700)>>k, 1) // each step half the one before
		}
		d := delay(k+1, stepSize, 128)
		for c, source := range reverbShuffles[k].Source {
			// the tap reads the channel that the shuffle moves to c
			frame, offset, negate := 4*sointu.MCChannels, 4*source, reverbShuffles[k].Sign[c] < 0
			l := int(d.Lengths[source])
			if k == 0 && apgain == 0 {
				// the stereo ring: left on the even channels, right on
				// the odd ones, channels 2, 3, 6 and 7 negated, all
				// delayed by the predelay
				frame, offset, negate = 8, 4*(source&1), negate != (source&2 != 0)
				l += int(predelay)
			}
			r.Taps[k][c] = uint16((l*frame - offset) / 4 << 1)
			if negate {
				r.Taps[k][c] |= 1
			}
		}
	}
	network := sointu.ReverbScale(size, 400, 2800)
	if p["network"] > 0 {
		network = min(p["network"], 2800)
	}
	d := newMCDelay(sointu.ParamMap{"size": network, "spread": p["spread"], "seed": 7,
		"decay": p["decay"], "hfdecay": p["highs"], "lfdecay": p["lows"]})
	r.A, r.B, r.C, r.Lengths = d.A, d.B, d.C, d.Lengths
	if p["loopsize"] > 0 {
		// as an mcdelay with allpass before that one, with seed 5
		d := newMCDelay(sointu.ParamMap{"size": min(p["loopsize"], 2800), "spread": p["spread"], "seed": 5, "allpass": 1, "apgain": p["loopgain"],
			"decay": p["decay"], "hfdecay": p["highs"], "lfdecay": p["lows"]})
		mod := level("loopmod")
		r.Loop = ReverbLoop{A: d.A, B: d.B, C: d.C, Lengths: d.Lengths, Gain: d.APGain,
			Rate:  float32(exp2f(float32(level("looprate")*8)-4) * 2.2675737e-05),
			Depth: float32(float32(mod*mod) * 352.8)}
	}
	return r
}

// reverbState is the state of a reverb unit in a voice. It does not fit in
// a unit, so the synths keep the states of all reverbs in a table of their
// own, one after the other in the order the units run (voice by voice), like
// those of ott. They are not cleared when a note is triggered.
type reverbState struct {
	t               uint32                        // the sample
	sum             float32                       // Σx/4 of the outputs of the lines
	lowcut, highcut [2]float32                    // the states of the filters of the input
	out             [sointu.MCChannels]float32    // the outputs of the lines
	lines           reverbLines                   // of the lines: the phase of the modulation, the decay filters
	bus             [sointu.MCChannels]float32    // the diffused input
	rings           [sointu.ReverbSteps][]float32 // of the steps of the diffuser: the stereo ring, then frames of 8 channels
	network         []float32                     // the ring of the network, frames of 8 channels
	spread          []float32                     // of a unit with allpasses: the ring of its first step, frames of 8 channels
	loop            reverbLines                   // of a unit with a second set of lines: their state
	loopRing        []float32                     // and their ring
}

// reverbLines is the state of 8 lines of the network: the phases of their
// modulation and the states of their decay filters.
type reverbLines struct {
	phase, lo, hi [sointu.MCChannels]float32
}

func (st *reverbState) alloc() {
	st.rings[0] = make([]float32, 2<<reverbStereoLog2)
	for k := 1; k < sointu.ReverbSteps; k++ {
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
		x := float32(stack[n-1-j] * r.Levels[reverbGain])
		if r.Bypass&sointu.ReverbBypassLowcut == 0 {
			lo := st.lowcut[j] + float32(a*(x-st.lowcut[j]))
			st.lowcut[j] = lo
			x -= lo
		}
		if r.Bypass&sointu.ReverbBypassHighcut == 0 {
			x = st.highcut[j] + float32(b*(x-st.highcut[j]))
			st.highcut[j] = x
		}
		st.rings[0][(t&(1<<reverbStereoLog2-1))*2+uint32(j)] = x
		stack[n-1-j] = 0
	}
	// the diffuser: each step reads the 8 channels from its ring, delayed
	// and shuffled, mixes them and writes them to the ring of the next
	// step; the last one to the bus
	steps := int(r.End) / (4 * sointu.MCChannels)
	for k := range steps {
		ring, frame := st.rings[k], uint32(sointu.MCChannels)
		if k == 0 {
			frame = 2
		}
		x := &st.bus
		if k+1 < steps {
			x = (*[sointu.MCChannels]float32)(st.rings[k+1][(t&(1<<(reverbStereoLog2-3-k)-1))*sointu.MCChannels:])
		}
		if r.APGain != 0 && k == 0 {
			// the predelayed input on the 8 channels, with the polarities
			// of mcspread
			if st.spread == nil {
				st.spread = make([]float32, len(st.rings[0]))
			}
			ring, frame = st.spread, sointu.MCChannels
			in := ring[t*frame&uint32(len(ring)-1):]
			for j := range uint32(2) {
				y := st.rings[0][((t-r.Predelay/8)&(1<<reverbStereoLog2-1))*2+j]
				in[j], in[j+4], in[j+2], in[j+6] = y, y, -y, -y
			}
		}
		mask := uint32(len(ring) - 1)
		for c, tap := range r.Taps[k] {
			behind := uint32(tap >> 1) // in floats
			y := ring[(t*frame-behind)&mask]
			if r.APGain != 0 {
				// an allpass: the frame of the ring being written holds
				// the input of the step, and the tap its channel of it
				in := &ring[t*frame&mask+(0-behind)&(sointu.MCChannels-1)]
				*in += float32(r.APGain * y)
				y -= float32(r.APGain * *in)
			}
			if tap&1 != 0 {
				y = -y
			}
			x[c] = y
		}
		mcmix(x, sointu.MCMixHadamard, nil)
	}
	// the network: each line is fed the diffused input plus the Householder
	// mix of the outputs of the lines in the last sample; with a second set
	// of lines, through its line of that first
	depth := float32(float32(p[0]*p[0]) * 352.8)
	for c := range st.out {
		x := st.bus[c] + (st.out[c] - st.sum)
		if l := &r.Loop; l.Lengths[0] != 0 {
			if st.loopRing == nil {
				st.loopRing = make([]float32, len(st.network))
			}
			x = reverbLine(x, c, t, l.Lengths[c], l.A[c], l.B[c], l.C[c], l.Depth, l.Rate, l.Gain, &st.loop, st.loopRing)
		}
		st.out[c] = reverbLine(x, c, t, r.Lengths[c], r.A[c], r.B[c], r.C[c], depth, r.Levels[reverbRate], 0, &st.lines, st.network)
	}
	st.sum = float32(float32(float32(reverbHalf(&st.out, 0)+reverbHalf(&st.out, 2))+float32(reverbHalf(&st.out, 1)+reverbHalf(&st.out, 3))) * 0.25)
	// the early reflections, the diffused input, and the tail, the outputs
	// of the lines
	reverbSum(&st.bus, r.Levels[reverbEarlyWidth], r.Levels[reverbEarlyGain], stack)
	reverbSum(&st.out, r.Levels[reverbTailWidth], 1, stack)
	st.t = t + 1
}

// reverbLine is line c of the network: it reads the ring at the length plus
// the modulation, interpolating, writes x to it, or as an allpass with the
// coefficient g x plus g times what it read, which is then minus g times
// what it wrote, and returns what it read through the decay filter. As
// mcdelay.
func reverbLine(x float32, c int, t uint32, length, a, b, cc, depth, turns, g float32, st *reverbLines, ring []float32) float32 {
	mask := uint32(1)<<reverbLog2Frames - 1
	rate := float32(float32(c)*0.125) + 1
	phase := st.phase[c] + float32(turns*rate)
	phase -= floor32(phase)
	st.phase[c] = phase
	tri := phase + float32(float32(c)*0.125)
	tri -= floor32(tri)
	tri = abs32(float32(tri*2) - 1)
	delay := length + float32(depth*tri)
	delay = min(max(delay, 1), float32(mask-1))
	i := uint32(int32(delay))
	f := delay - float32(int32(i))
	ya := ring[((t-i)&mask)*sointu.MCChannels+uint32(c)]
	yb := ring[((t-i-1)&mask)*sointu.MCChannels+uint32(c)]
	y := ya + float32(f*float32(yb-ya))
	if g != 0 {
		x += float32(g * y)
		y -= float32(g * x)
	}
	ring[(t&mask)*sointu.MCChannels+uint32(c)] = x
	lo := st.lo[c] + float32(mcDecayLow*float32(y-st.lo[c]))
	st.lo[c] = lo
	y += float32(a * lo)
	hi := st.hi[c] + float32(mcDecayHigh*float32(y-st.hi[c]))
	st.hi[c] = hi
	return float32(b*y) + float32(cc*hi)
}
