package vm

import (
	"math"
	"math/bits"

	"github.com/vsariola/sointu"
)

// The mc units process buses of 8 channels in place, sample by sample:
// mcspread spreads a signal from the stack over a bus, mcdelay delays the
// channels, mcmix mixes them, mcfilter filters them, mcloop and mcloopend
// feed the bus back to the next sample, and mcsum sums the bus back onto the
// stack. Chained, they make diffusers and feedback delay networks, i.e.
// reverbs. Like the spectral units, they run only in the first voice of their
// instrument, and a bus is shared by all the units referring to it.
//
// What needs pow and log, the lengths of the delay lines and the coefficients
// of their decay, is computed here when the patch is encoded, from the unit's
// parameters, and read from tables by the players. The rest matches the wasm
// player operation by operation, in float32, with products wrapped in
// float32() so that they are not fused into multiply-adds; the wasm player
// computes the 8 channels as two f32x4 vectors, lane by lane the same.

type (
	// MCUnit is an mc unit: the voice that runs it, the first voice of its
	// instrument, and its bus, an index in Bytecode.Buses. Delay is the
	// constant data of an mcdelay unit, Shuffle the permutation of an mcmix
	// unit of type shuffle.
	MCUnit struct {
		Type    string
		UnitID  int // for UnitLevels
		Voice   int
		Bus     int
		Delay   *MCDelay
		Shuffle *MCShuffle
	}

	// MCDelay is the constant data of an mcdelay unit: the lengths of its
	// lines in samples, the coefficients of their decay (see mcDecay), the
	// base 2 logarithm of the frames of its ring and the allpass
	// coefficient.
	MCDelay struct {
		Lengths, A, B, C [sointu.MCChannels]float32
		Log2Frames       int
		APGain           float32
	}

	// MCShuffle is the permutation of an mcmix unit of type shuffle:
	// channel c becomes channel Source[c] times Sign[c].
	MCShuffle struct {
		Source [sointu.MCChannels]int
		Sign   [sointu.MCChannels]float32
	}
)

// Flags of the mcdelay unit, in the operand after its index.
const (
	MCDelayNoteTracking = 1
	MCDelayAllpass      = 2
)

// The crossover coefficients of the decay filters of mcdelay: one-pole
// low-passes at 250 Hz and 3 kHz, 1 - e^(-2πf/44100).
const (
	mcDecayLow  = float32(0.034992073)
	mcDecayHigh = float32(0.34781536)
)

// mcMaxModSamples is the most an mcdelay's modulation lengthens a line when
// moddepth is modulated up to 2: 8·2² ms.
const mcMaxModSamples = 32 * 44.1

// mcMaxLog2Frames is the base 2 logarithm of the largest ring of an mcdelay:
// 131072 frames, about 3 seconds.
const mcMaxLog2Frames = 17

// mcRates are the rates of the modulation of the channels of mcdelay,
// relative to modrate, and mcPhases the offsets of their phases.
var (
	mcRates  = [sointu.MCChannels]float32{1, 1.125, 1.25, 1.375, 1.5, 1.625, 1.75, 1.875}
	mcPhases = [sointu.MCChannels]float32{0, 0.125, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875}
)

// mcRand is the random number generator of the mc units, a 32-bit linear
// congruential generator (Numerical Recipes). It runs only when the patch is
// encoded.
type mcRand uint32

func newMCRand(seed int) mcRand {
	r := mcRand(uint32(seed)*0x9E3779B9 + 1)
	for range 4 { // mixes the seed into all bits
		r.next()
	}
	return r
}

func (r *mcRand) next() uint32 {
	*r = *r*1664525 + 1013904223
	return uint32(*r)
}

// float returns a number in [0, 1), from the 24 top bits.
func (r *mcRand) float() float64 { return float64(r.next()>>8) / (1 << 24) }

// perm returns a random permutation of 0 to 7 (Fisher-Yates).
func (r *mcRand) perm() (p [sointu.MCChannels]int) {
	for i := range p {
		p[i] = i
	}
	for i := len(p) - 1; i > 0; i-- {
		j := int(r.next()>>8) % (i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}

// newMCDelay computes the constant data of an mcdelay unit from its
// parameters: the lengths, one at random in each eighth of the range from
// size·(1-spread) to size, in random order, rounded to whole samples unless
// they follow the note; and the decay of each line, see mcDecay.
func newMCDelay(p sointu.ParamMap) *MCDelay {
	d := &MCDelay{}
	size := float64(min(max(p["size"], 1), sointu.MCSizeMax)) * 4.41 // tenths of ms to samples
	spread := float64(min(max(p["spread"], 0), 128)) / 128
	r := newMCRand(p["seed"])
	perm := r.perm()
	longest := 0.0
	for c := range d.Lengths {
		u := (float64(perm[c]) + r.float()) / sointu.MCChannels
		l := max(size*(1-spread*u), 1)
		if p["notetracking"] != 1 {
			// whole samples, which linear interpolation does not damp
			l = math.Round(l)
		}
		d.Lengths[c] = float32(l)
		longest = max(longest, l)
	}
	if p["notetracking"] == 1 {
		longest *= 32 // note 0, 5 octaves below note 60
	}
	d.Log2Frames = min(bits.Len(uint(longest+mcMaxModSamples+3)), mcMaxLog2Frames)
	d.APGain = float32(float64(min(max(p["apgain"], 0), sointu.MCAllpassGainMax)) / 128)
	decay := min(max(p["decay"], 0), 128)
	for c, l := range d.Lengths {
		// an allpass delays the frequencies where it resonates by up to
		// l(1+g)/(1-g); those decay slowest, and make the tail
		l := float64(l)
		if p["allpass"] == 1 {
			l *= float64(1+d.APGain) / float64(1-d.APGain)
		}
		d.A[c], d.B[c], d.C[c] = mcDecay(l, decay, min(max(p["hfdecay"], 0), 128), min(max(p["lfdecay"], 0), 128))
	}
	return d
}

// mcDecay returns the coefficients of the decay of a line of length l for the
// decay parameters: the gains g of the line below 250 Hz, in the middle and
// above 3 kHz, 10^(-3l/(44100·T)) for the decay times T of each band, make
// the filter
//
//	lo += 0.035·(y - lo)       one-pole low-pass at 250 Hz
//	v = y + A·lo               low shelf: glo/gmid below 250 Hz
//	hi += 0.348·(v - hi)       one-pole low-pass at 3 kHz
//	out = B·v + C·hi           high shelf and gain: gmid, ghi above 3 kHz
//
// with A = glo/gmid - 1, B = ghi and C = gmid - ghi. Each shelf's magnitude
// lies between its gains, and the high band decays at most as slowly as the
// middle, so the magnitude stays below max(glo, gmid) < 1: a loop of lines
// and orthogonal mixes is stable. decay 0 passes the line unchanged.
func mcDecay(l float64, decay, hfdecay, lfdecay int) (a, b, c float32) {
	if decay <= 0 {
		return 0, 1, 0
	}
	t := sointu.MCDecaySeconds(decay)
	gain := func(t float64) float64 {
		if t <= 0 {
			return 0
		}
		return math.Pow(10, -3*l/(44100*t))
	}
	mid, lo, hi := gain(t), gain(t*sointu.MCLFDecayRatio(lfdecay)), gain(t*sointu.MCHFDecayRatio(hfdecay))
	return float32(lo/mid - 1), float32(hi), float32(mid - hi)
}

// newMCShuffle returns the permutation and polarity flips of an mcmix unit
// of type shuffle, at random by seed.
func newMCShuffle(seed int) *MCShuffle {
	s := &MCShuffle{}
	r := newMCRand(seed)
	s.Source = r.perm()
	for c := range s.Sign {
		s.Sign[c] = 1
		if r.next()&(1<<20) != 0 {
			s.Sign[c] = -1
		}
	}
	return s
}

// busIndex returns the index of the bus with the given buffer ID in
// Bytecode.Buses, adding it if needed.
func (b *bytecodeBuilder) busIndex(id int) int {
	for i, bus := range b.Buses {
		if bus == id {
			return i
		}
	}
	b.Buses = append(b.Buses, id)
	return len(b.Buses) - 1
}

type (
	// mcBus is a bus: the frame the mc units process, and the frame stored
	// by mcloopend for the next sample.
	mcBus struct {
		v, loop [sointu.MCChannels]float32
	}

	// mcState is the state of an mc unit: the phases of the modulation and
	// the states of the decay filters of mcdelay, the position in its ring
	// and the ring, a frame of 8 channels after the other; the states of
	// mcfilter in lo. levels are the peaks of the bus after the unit since
	// they were last reported, for UnitLevels.
	mcState struct {
		phase, lo, hi [sointu.MCChannels]float32
		pos           uint32
		ring          []float32
		levels        [sointu.MCChannels]float32
	}
)

// setMC allocates the buses and the states of the mc units of the bytecode,
// keeping the ones of old that did not change.
func (s *GoSynth) setMC(old *Bytecode) {
	buses := make([]mcBus, len(s.bytecode.Buses))
	for i, id := range s.bytecode.Buses {
		if old != nil {
			for j, o := range old.Buses {
				if o == id && j < len(s.buses) {
					buses[i] = s.buses[j]
				}
			}
		}
	}
	states := make([]mcState, len(s.bytecode.MCUnits))
	for i, u := range s.bytecode.MCUnits {
		if old != nil && i < len(old.MCUnits) && i < len(s.mc) {
			if o := old.MCUnits[i]; o.Type == u.Type && (o.Delay == nil) == (u.Delay == nil) && (o.Delay == nil || o.Delay.Log2Frames == u.Delay.Log2Frames) {
				states[i] = s.mc[i]
				continue
			}
		}
		if u.Delay != nil {
			states[i].ring = make([]float32, sointu.MCChannels<<u.Delay.Log2Frames)
		}
	}
	s.buses, s.mc = buses, states
}

// runMC runs an mc unit: op is its opcode without the stereo bit, operands its
// operands after the transformed parameters. It returns the operands after
// the unit's.
func (s *GoSynth) runMC(op byte, stereo bool, operands []byte, params *[8]float32, voice int, note byte, stack *[]float32) []byte {
	index := int(operands[0])
	u := &s.bytecode.MCUnits[index]
	run := u.Voice == voice
	var bus *mcBus
	if run {
		bus = &s.buses[u.Bus]
	}
	st := &s.mc[index]
	switch op {
	case opMcspread:
		l := len(*stack)
		left, right := (*stack)[l-1], (*stack)[l-1]
		*stack = (*stack)[:l-1]
		if stereo {
			right = (*stack)[l-2]
			*stack = (*stack)[:l-2]
		}
		add := operands[1] != 0
		operands = operands[2:]
		if !run {
			break
		}
		g := mcGain(params[0])
		left, right = float32(left*g), float32(right*g)
		v := [4]float32{left, right, -left, -right}
		for c := range bus.v {
			if add {
				bus.v[c] += v[c&3]
			} else {
				bus.v[c] = v[c&3]
			}
		}
	case opMcsum:
		operands = operands[1:]
		var out [2]float32
		if run {
			var h [4]float32
			for k := range h {
				h[k] = bus.v[k] + bus.v[k+4]
			}
			l, r := h[0]-h[2], h[1]-h[3]
			g := mcGain(params[0])
			mid := float32(float32(l+r) * 0.125)
			var side float32 // mono: mid + 0, like the wasm player
			if stereo {
				side = float32(float32(float32(l-r)*0.125) * float32(params[1]*2))
				out[1] = float32(float32(mid-side) * g)
			}
			out[0] = float32(float32(mid+side) * g)
		}
		if stereo {
			*stack = append(*stack, out[1])
		}
		*stack = append(*stack, out[0])
	case opMcmix:
		typ := operands[1]
		operands = operands[2:]
		if !run {
			break
		}
		mcmix(&bus.v, typ, u.Shuffle)
	case opMcloop:
		operands = operands[1:]
		if !run {
			break
		}
		for c := range bus.v {
			bus.v[c] += float32(params[0] * bus.loop[c])
		}
	case opMcloopend:
		operands = operands[1:]
		if !run {
			break
		}
		bus.loop = bus.v
	case opMcfilter:
		highpass := operands[1] != 0
		operands = operands[2:]
		if !run {
			break
		}
		// a = 1 - 2^(-2π·20·2^(10f)/44100·log2(e))
		a := 1 - exp2f(float32(exp2f(float32(params[0]*10))*-0.004110984))
		for c, x := range bus.v {
			lo := st.lo[c] + float32(a*(x-st.lo[c]))
			st.lo[c] = lo
			if highpass {
				bus.v[c] = x - lo
			} else {
				bus.v[c] = lo
			}
		}
	case opMcdelay:
		flags := operands[1]
		operands = operands[2:]
		if !run {
			break
		}
		mcdelay(u.Delay, st, &bus.v, params[0], params[1], flags, note)
	}
	if run {
		for c, x := range bus.v {
			st.levels[c] = max(st.levels[c], abs32(x))
		}
	}
	return operands
}

// mcGain is the gain of mcspread and mcsum, ±40 dB like dbgain.
func mcGain(p float32) float32 { return exp2f(float32(p-0.5) * 13.287712379549449) }

// mcmix mixes the channels with the matrix of the type: a Hadamard matrix
// scaled by 1/√8 (the butterflies of channels 4, 2 and 1 apart), a
// Householder reflection x - (2/8)·Σx, or a shuffle.
func mcmix(x *[sointu.MCChannels]float32, typ byte, shuffle *MCShuffle) {
	switch typ {
	case sointu.MCMixHadamard:
		for _, d := range [...]int{4, 2, 1} {
			for c := range x {
				if c&d == 0 {
					a, b := x[c], x[c+d]
					x[c], x[c+d] = a+b, a-b
				}
			}
		}
		for c := range x {
			x[c] = float32(x[c] * 0.35355338)
		}
	case sointu.MCMixHouseholder:
		var h [4]float32
		for k := range h {
			h[k] = x[k] + x[k+4]
		}
		sum := float32(float32(h[0]+h[2]) + float32(h[1]+h[3]))
		sum = float32(sum * 0.25)
		for c := range x {
			x[c] -= sum
		}
	default:
		y := *x
		for c := range x {
			x[c] = float32(y[shuffle.Source[c]] * shuffle.Sign[c])
		}
	}
}

// mcdelay delays each channel of v by its line: reads the line at its length,
// with note tracking and modulation, interpolating linearly, writes the
// channel (or with allpass, the channel plus apgain times what was read), and
// replaces the channel with what was read (or the allpass' output), through
// the decay filter of mcDecay.
func mcdelay(d *MCDelay, st *mcState, v *[sointu.MCChannels]float32, depthP, rateP float32, flags byte, note byte) {
	// the rate in turns per sample, and the depth in samples
	rate := float32(exp2f(float32(rateP*8)-4) * 2.2675737e-05)
	depth := float32(float32(depthP*depthP) * 352.8)
	var nt float32 = 1
	if flags&MCDelayNoteTracking != 0 {
		nt = exp2f(float32(float32(60-float32(note)) * 0.083333336))
	}
	mask := uint32(1)<<d.Log2Frames - 1
	maxDelay := float32(mask - 1)
	t := st.pos
	for c := range v {
		phase := st.phase[c] + float32(rate*mcRates[c])
		phase -= floor32(phase)
		st.phase[c] = phase
		tri := phase + mcPhases[c]
		tri -= floor32(tri)
		tri = abs32(float32(tri*2) - 1)
		delay := float32(d.Lengths[c]*nt) + float32(depth*tri)
		delay = min(max(delay, 1), maxDelay)
		i := uint32(int32(delay))
		f := delay - float32(int32(i))
		a := st.ring[(t-i)&mask*sointu.MCChannels+uint32(c)]
		b := st.ring[(t-i-1)&mask*sointu.MCChannels+uint32(c)]
		y := a + float32(f*float32(b-a))
		w := v[c]
		if flags&MCDelayAllpass != 0 {
			w += float32(d.APGain * y)
			y -= float32(d.APGain * w)
		}
		st.ring[t*sointu.MCChannels+uint32(c)] = w
		lo := st.lo[c] + float32(mcDecayLow*float32(y-st.lo[c]))
		st.lo[c] = lo
		y += float32(d.A[c] * lo)
		hi := st.hi[c] + float32(mcDecayHigh*float32(y-st.hi[c]))
		st.hi[c] = hi
		v[c] = float32(d.B[c]*y) + float32(d.C[c]*hi)
	}
	st.pos = (t + 1) & mask
}

// UnitLevels returns the peak level of each channel of the bus after the mc
// unit with the given ID ran, since the last call, or nil if there is no
// such unit.
func (s *GoSynth) UnitLevels(unitID int, dst []float32) []float32 {
	for i, u := range s.bytecode.MCUnits {
		if u.UnitID == unitID && unitID != 0 && i < len(s.mc) {
			dst = append(dst, s.mc[i].levels[:]...)
			clear(s.mc[i].levels[:])
			return dst
		}
	}
	return nil
}
