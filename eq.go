package sointu

import (
	"math"
	"math/cmplx"

	"gopkg.in/yaml.v3"
)

// A unit of the type "eq" is a parametric equalizer: a list of bands
// (Unit.Bands), each with a type, a frequency, a gain and a Q. Like the module
// unit, it is virtual: Song.Expand replaces it with the units that it stands
// for, a chain of filter, belleq, ladder and gain units, so the synths and
// the compiled players never see it. CompileEQ tells which units, and
// EQResponse what they do to each frequency.

// The types of the bands of an eq unit.
const (
	// EQBell is a peak or a dip: one belleq unit.
	EQBell = "bell"
	// EQLowCut and EQHighCut are a high-pass and a low-pass of 12 dB per
	// octave: one filter unit, and a belleq unit if Q is below 1, which the
	// filter unit cannot do by itself.
	EQLowCut  = "lowcut"
	EQHighCut = "highcut"
	// EQLowCut24 and EQHighCut24 are 24 dB per octave: two such stages,
	// with Q 0.71 a Butterworth filter.
	EQLowCut24  = "lowcut24"
	EQHighCut24 = "highcut24"
	// EQLadder is a high cut of 24 dB per octave made of one ladder unit,
	// which only the Go synth and the wasm player have. It reaches higher
	// than the filter unit.
	EQLadder = "ladder"
	// EQLowShelf and EQHighShelf raise or lower everything below or above
	// the frequency: the signal plus a filtered copy of it (push, filter,
	// gain, addp).
	EQLowShelf  = "lowshelf"
	EQHighShelf = "highshelf"
	// EQNotch removes the frequency, and EQBandPass everything but it: one
	// filter unit.
	EQNotch    = "notch"
	EQBandPass = "bandpass"
)

// EQBandTypes are the types of the bands of an eq unit, in the order they
// are offered.
var EQBandTypes = []string{EQBell, EQLowCut, EQLowCut24, EQHighCut, EQHighCut24, EQLadder, EQLowShelf, EQHighShelf, EQNotch, EQBandPass}

// The ranges of the values of a band, and of the gain of an eq unit.
const (
	EQMinFrequency = 10
	EQMaxFrequency = 22000
	EQMaxGain      = 24 // dB, up and down
	EQMinQ         = 0.3
	EQMaxQ         = 32
)

// eqGainTolerance is the gain, in dB, below which the eq leaves out the
// gain unit at its end.
const eqGainTolerance = 0.1

// eqSampleRate is the sample rate that the units run at.
const eqSampleRate = 44100

// EQBand is a band of an eq unit.
type EQBand struct {
	// Type is one of EQBandTypes; anything else is a bell.
	Type string
	// Frequency is the center or the corner of the band, in Hz.
	Frequency float64
	// Gain is the gain of a bell or a shelf, in dB.
	Gain float64 `yaml:",omitempty"`
	// Q is how narrow a bell, a notch or a band-pass is, and how resonant a
	// cut or a shelf is. 0 means the default of the type: see EQDefaultQ.
	Q float64 `yaml:",omitempty"`
	// Off bands are kept, but have no units.
	Off bool `yaml:",omitempty"`
}

// MarshalYAML implements yaml.Marshaler: a band is written on one line.
func (b EQBand) MarshalYAML() (any, error) {
	type plain EQBand // without the MarshalYAML method
	var node yaml.Node
	if err := node.Encode(plain(b)); err != nil {
		return nil, err
	}
	node.Style = yaml.FlowStyle
	return &node, nil
}

// EQHasGain reports whether bands of the type have a gain.
func EQHasGain(bandType string) bool {
	switch bandType {
	case EQLowCut, EQLowCut24, EQHighCut, EQHighCut24, EQLadder, EQNotch, EQBandPass:
		return false
	}
	return true
}

// EQDefaultQ returns the Q of new bands of the type, and of bands with Q 0.
// For the cuts of 12 dB per octave and the shelves it is 1, the lowest that
// one filter unit can do, which raises the level next to the corner by about
// a decibel; 0.71 is flat and costs a belleq unit more. The cuts of 24 dB
// per octave cost the same either way and start flat.
func EQDefaultQ(bandType string) float64 {
	switch bandType {
	case EQLowCut24, EQHighCut24, EQLadder:
		return math.Sqrt2 / 2
	case EQNotch:
		return 4
	}
	return 1
}

// Normalized returns the band with a known type and its values within their
// ranges.
func (b EQBand) Normalized() EQBand {
	known := false
	for _, t := range EQBandTypes {
		known = known || t == b.Type
	}
	if !known {
		b.Type = EQBell
	}
	clamp := func(v, lo, hi, def float64) float64 {
		if v != v || v == 0 && def != 0 { // NaN, or not set
			return def
		}
		return min(max(v, lo), hi)
	}
	b.Frequency = clamp(b.Frequency, EQMinFrequency, EQMaxFrequency, 1000)
	b.Q = clamp(b.Q, EQMinQ, EQMaxQ, EQDefaultQ(b.Type))
	b.Gain = clamp(b.Gain, -EQMaxGain, EQMaxGain, 0)
	if !EQHasGain(b.Type) {
		b.Gain = 0
	}
	return b
}

type (
	// EQCompiled is what an eq unit stands for.
	EQCompiled struct {
		// Units are the units that take its place: those of its bands, in
		// their order, and a gain unit, if the gains to make up are not 1.
		Units []Unit
		// Bands tells how each band of the eq unit was compiled.
		Bands []EQCompiledBand
		// Gain is the gain that the last unit should have, as a factor: the
		// gain of the eq unit and what the bands leave to it. GainUnits are
		// the units that do it, none or one, and ActualGain what they do.
		Gain, ActualGain float64
		GainUnits        []Unit
	}

	// EQCompiledBand is what a band of an eq unit stands for.
	EQCompiledBand struct {
		// Units are the units of the band: none if it is off, or does
		// nothing.
		Units []Unit
		// Makeup is the gain that the band leaves to the gain unit at the
		// end of the eq, as a factor.
		Makeup float64
		// Actual is the band with the frequency, the gain and the Q that
		// its units have: the parameters of the units are whole numbers.
		Actual EQBand
		// GoWasmOnly tells that the band has units that only the Go synth
		// and the wasm player have.
		GoWasmOnly bool
	}
)

// CompileEQ returns what an eq unit stands for: see EQCompiled. Each band
// costs as few units as its type and values allow:
//
//   - bell: belleq. No units if its gain is 0.
//   - lowcut, highcut, notch, bandpass: filter. With Q below 1, filter with
//     the resonance of Q 1 and a belleq at the same frequency that damps it
//     to Q (see eqStage): 2 units.
//   - lowcut24, highcut24: two such stages, with Q·0.765 and Q·1.848: 3
//     units with Q from 0.54 to 1.31 (Butterworth at 0.71), 4 below, 2
//     above.
//   - ladder: ladder, with the resonance for Q.
//   - lowshelf, highshelf: push, filter, gain, addp (and belleq, with Q
//     below 1): the signal plus k times its lows and its band (or its highs
//     and its band), k being the gain as a factor minus 1. A shelf that
//     lowers is the other shelf raising, at the same frequency, with the
//     whole signal lowered by the gain unit at the end.
//
// The gain unit at the end has the gain of the eq unit, times what the
// shelves that lower, the band-passes (whose filter raises the center by Q)
// and the ladders (whose resonance lowers the level) leave to it: one unit
// for all of them, the one of gain, invgain and dbgain that comes nearest.
func (u *Unit) CompileEQ() EQCompiled {
	stereo := u.Parameters["stereo"] & 1
	ret := EQCompiled{Gain: math.Pow(10, float64(u.Parameters["gain"])/200), ActualGain: 1}
	ret.Bands = make([]EQCompiledBand, len(u.Bands))
	for i, b := range u.Bands {
		c := compileEQBand(b, stereo)
		ret.Bands[i] = c
		ret.Units = append(ret.Units, c.Units...)
		ret.Gain *= c.Makeup
	}
	// less than a tenth of a decibel is not worth a unit
	if g, actual, ok := eqGainUnit(ret.Gain, stereo); ok && math.Abs(eqDb(ret.Gain)) >= eqGainTolerance {
		ret.GainUnits = []Unit{g}
		ret.ActualGain = actual
		ret.Units = append(ret.Units, g)
	}
	return ret
}

// NumUnits returns the number of units of the band.
func (b *EQCompiledBand) NumUnits() int { return len(b.Units) }

// Response returns what the band does to a sine of the given frequency, in
// Hz: its units, and the gain it leaves to the gain unit of the eq.
func (b *EQCompiledBand) Response(freq float64) complex128 {
	return EQResponse(b.Units, freq) * complex(b.Makeup, 0)
}

// Response returns what the units of the eq do to a sine of the given
// frequency, in Hz.
func (c *EQCompiled) Response(freq float64) complex128 { return EQResponse(c.Units, freq) }

func eqUnit(unitType string, stereo int, params ParamMap) Unit {
	params["stereo"] = stereo
	return Unit{Type: unitType, Parameters: params}
}

// eqFilterHz returns the frequency of the filter unit, where its resonance
// is, for the value of its frequency parameter; eqFilterValue the value
// nearest to a frequency. The highest is 7350 Hz.
func eqFilterHz(v int) float64 {
	p := float64(v) / 128
	return math.Asin(p*p/2) / math.Pi * eqSampleRate
}

func eqFilterValue(freq float64) int { return eqNearest(1, 128, eqFilterHz, freq) }

// eqBellMax is the highest frequency value of the belleq unit that still
// raises the frequency: the unit computes the cosine of its frequency as a
// square root, which is never negative, so above a quarter of the sample
// rate (11025 Hz) it comes back down.
const eqBellMax = 113

func eqBellHz(v int) float64 {
	p := float64(v) / 128
	return eqSampleRate * p * p / math.Pi
}

func eqBellValue(freq float64) int { return eqNearest(1, eqBellMax, eqBellHz, freq) }

// eqNearest returns the value from lo to hi for which hz, which grows with
// the value, is nearest to freq, as a ratio.
func eqNearest(lo, hi int, hz func(int) float64, freq float64) int {
	for lo < hi {
		mid := (lo + hi) / 2
		if hz(mid+1)*hz(mid) < freq*freq { // freq is nearer to mid+1, or above
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func eqRound(v float64, lo, hi int) int { return min(max(int(math.Round(v)), lo), hi) }

func eqDb(factor float64) float64 { return 20 * math.Log10(factor) }

// eqStage returns the units of a state-variable filter of 12 dB per octave
// with the given Q, whose output is the sum of its low-pass, band-pass and
// high-pass as lp, bp and hp tell, and the Q that they have.
//
// The damping of the filter unit is its resonance parameter, 1/Q, which is at
// most 1: it cannot do a Q below 1, e.g. the 0.71 of a filter that is flat up
// to its corner. A belleq unit at the same frequency makes up for it: the
// filter with Q 1 has the poles of s² + s + 1, and a bell that lowers by Q
// (as a factor) with the Q √Q is (s² + s + 1)/(s² + s/Q + 1), which
// replaces them with those of the Q asked for.
func eqStage(freqValue int, q float64, lp, bp, hp, stereo int) (units []Unit, actualQ float64) {
	filter := eqUnit("filter", stereo, ParamMap{"frequency": freqValue, "resonance": 128, "lowpass": lp, "bandpass": bp, "highpass": hp})
	if res := int(math.Round(128 / q)); res <= 128 {
		filter.Parameters["resonance"] = max(res, 1)
		return []Unit{filter}, 128 / float64(max(res, 1))
	}
	gain := eqRound(64+eqDb(q)*1.6, 0, 64)
	if gain == 64 { // as near to Q 1 as the belleq can tell
		return []Unit{filter}, 1
	}
	actualQ = math.Pow(10, 2*(float64(gain)/64-1))
	bell := eqUnit("belleq", stereo, ParamMap{
		"frequency": eqBellValue(eqFilterHz(freqValue)),
		"bandwidth": eqRound(32/math.Sqrt(actualQ), 1, 128),
		"gain":      gain,
	})
	return []Unit{filter, bell}, actualQ
}

// eqGainUnit returns the unit that multiplies the signal by the factor, as
// nearly as its parameter allows: gain (up to 1, in steps of 1/128) or
// invgain (from 1: 128/128 to 128/1), or if it comes nearer by more than a
// tenth of a decibel, dbgain (steps of 0.625 dB), and the factor that it
// multiplies by. ok is false if no unit comes nearer than leaving the signal
// as it is.
func eqGainUnit(factor float64, stereo int) (unit Unit, actual float64, ok bool) {
	if !(factor > 0) {
		factor = 0
	}
	errOf := func(a float64) float64 { // in dB
		if a == factor {
			return 0
		}
		if a <= 0 || factor <= 0 {
			return math.Inf(1)
		}
		return math.Abs(eqDb(a / factor))
	}
	actual = 1
	best := errOf(1)
	try := func(a float64, unitType, param string, v int, handicap float64) {
		if e := errOf(a) + handicap; e < best {
			best, actual, ok = e, a, true
			unit = eqUnit(unitType, stereo, ParamMap{param: v})
		}
	}
	if factor <= 1 {
		v := eqRound(factor*128, 0, 128)
		try(float64(v)/128, "gain", "gain", v, 0)
	}
	if factor >= 1 {
		v := eqRound(128/factor, 1, 128)
		try(128/float64(v), "invgain", "invgain", v, 0)
	}
	if factor > 0 {
		// dbgain is one more kind of unit in a song without it
		v := eqRound(64+eqDb(factor)*1.6, 0, 128)
		try(math.Pow(10, 2*(float64(v)/64-1)), "dbgain", "decibels", v, eqGainTolerance)
	}
	return unit, actual, ok && actual != 1
}

// eqLadderFeedback returns the feedback of a ladder (4.5 times the
// resonance of the ladder unit) whose resonant poles have the given Q, and
// eqLadderQ the Q for a feedback. The poles of four one-pole low-passes in
// a row with the feedback k are at -1 + k^(1/4)·e^(±iπ/4), times the cutoff,
// and at -1 - k^(1/4)·e^(±iπ/4): Q 0.5 without feedback, endless at 4.
func eqLadderFeedback(q float64) float64 {
	t := math.Sqrt(max(4*q*q-1, 0))
	a := t / (1 + t)
	return 4 * a * a * a * a
}

func eqLadderQ(k float64) float64 {
	a := math.Sqrt(math.Sqrt(k / 4))
	if a >= 1 {
		return math.Inf(1)
	}
	return math.Sqrt((1-a)*(1-a)+a*a) / (2 * (1 - a))
}

func compileEQBand(b EQBand, stereo int) (ret EQCompiledBand) {
	b = b.Normalized()
	ret.Makeup = 1
	ret.Actual = b
	if b.Off {
		return ret
	}
	fv := eqFilterValue(b.Frequency)
	switch b.Type {
	case EQBell:
		p := ParamMap{"frequency": eqBellValue(b.Frequency), "bandwidth": eqRound(32/b.Q, 1, 128), "gain": eqRound(64+b.Gain*1.6, 0, 128)}
		ret.Actual.Frequency = eqBellHz(p["frequency"])
		ret.Actual.Q = 32 / float64(p["bandwidth"])
		ret.Actual.Gain = 40 * (float64(p["gain"])/64 - 1)
		if p["gain"] != 64 {
			ret.Units = []Unit{eqUnit("belleq", stereo, p)}
		}
	case EQLowCut, EQHighCut, EQNotch, EQBandPass:
		lp, bp, hp := 0, 0, 0
		switch b.Type {
		case EQLowCut:
			hp = 1
		case EQHighCut:
			lp = 1
		case EQNotch:
			lp, hp = 1, 1
		case EQBandPass:
			bp = 1
		}
		ret.Units, ret.Actual.Q = eqStage(fv, b.Q, lp, bp, hp, stereo)
		ret.Actual.Frequency = eqFilterHz(fv)
		if b.Type == EQBandPass {
			// the band-pass of the filter unit raises its center by Q
			ret.Makeup = 1 / cmplx.Abs(EQResponse(ret.Units, ret.Actual.Frequency))
		}
	case EQLowCut24, EQHighCut24:
		lp, hp := 0, 1
		if b.Type == EQHighCut24 {
			lp, hp = 1, 0
		}
		// the Qs of the two stages of a Butterworth filter of 24 dB per
		// octave are 0.5412 and 1.3066: Q 0.7071 times these
		first, q1 := eqStage(fv, b.Q*0.76537, lp, 0, hp, stereo)
		second, q2 := eqStage(fv, b.Q*1.84776, lp, 0, hp, stereo)
		ret.Units = append(first, second...)
		ret.Actual.Q = math.Sqrt(q1*q2) / math.Sqrt(0.76537*1.84776)
		ret.Actual.Frequency = eqFilterHz(fv)
	case EQLadder:
		res := eqRound(128*eqLadderFeedback(b.Q)/4.5, 0, 127)
		k := 4.5 * float64(res) / 128
		ret.Actual.Q = eqLadderQ(k)
		ret.Makeup = (1 + k) / (1 + 0.5*k) // the feedback lowers the level
		ret.GoWasmOnly = true
		// the frequency parameter that puts the level at the frequency 3 dB
		// below that of the bass
		u := eqUnit("ladder", stereo, ParamMap{"frequency": 0, "resonance": res, "drive": 0})
		level := func(v int) float64 {
			u.Parameters["frequency"] = v
			return eqDb(cmplx.Abs(eqUnitResponse(&u, eqZ(b.Frequency))) * ret.Makeup)
		}
		u.Parameters["frequency"] = eqSearch(1, 128, level, -3)
		ret.Units = []Unit{u}
		ret.Actual.Frequency = eqCrossing(func(f float64) float64 { return eqDb(cmplx.Abs(ret.Response(f))) }, -3, b.Frequency)
	case EQLowShelf, EQHighShelf:
		ret = compileEQShelf(b, stereo)
	}
	return ret
}

// The frequencies at which the levels of the two ends of a shelf are taken.
const (
	eqShelfLow  = 10
	eqShelfHigh = 16000
	eqShelfGrid = 24 // frequencies at which a shelf is compared to what it should be
)

// compileEQShelf returns the units of a shelf: the signal plus k times a
// filtered copy of it, its lows and its band (lowpass and bandpass of the
// filter unit) or its highs and its band, which raises that end. A shelf
// that lowers is the other shelf raising, with the whole signal lowered by
// the gain unit of the eq.
//
// With the filter s² + ds + 1 that the filter unit is modelled on, raising
// the lows by K = 1 + k is (s² + (d + k)s + K)/(s² + ds + 1): a shelf whose
// middle, in dB, is at √((b + √(b² + 4K))/2) times the frequency of the
// filter, with b = ((d + k)² - K·d²)/k. The filter unit is not that filter
// at the upper end: its high-pass rises above 1 towards half the sample
// rate, and its band-pass does not fall to 0 there. So k is not K - 1, but
// what gives the two ends of the shelf, at 10 Hz and 16 kHz, the gain asked
// for between them, the gain unit of the eq brings the end that the shelf
// leaves alone back to where it was, and the frequency parameter of the
// filter is the one with which the units come nearest to that shelf, from
// 20 Hz to 16 kHz.
func compileEQShelf(b EQBand, stereo int) (ret EQCompiledBand) {
	ret.Makeup, ret.Actual = 1, b
	ret.Actual.Gain = 0
	raise := b.Gain > 0
	lows := (b.Type == EQLowShelf) == raise // the end that the copy raises
	lp, hp := 0, 1
	raised, other := float64(eqShelfHigh), float64(eqShelfLow)
	if lows {
		lp, hp = 1, 0
		raised, other = other, raised
	}
	want := math.Pow(10, math.Abs(b.Gain)/20)
	if want < 1.001 {
		return ret
	}
	// the shelf to come near to, as levels in dB at the frequencies of grid
	var grid, target [eqShelfGrid]float64
	{
		k := want - 1
		dp := 1 / b.Q        // the damping of the poles
		dz := max(dp, 1) + k // and of the zeros: the filter unit has at least Q 1
		t := (dz*dz - want*dp*dp) / k
		mid := math.Sqrt((t + math.Sqrt(t*t+4*want)) / 2)
		f0 := b.Frequency / mid
		if !lows {
			f0 = b.Frequency * mid
		}
		for i := range grid {
			grid[i] = 20 * math.Pow(eqShelfHigh/20, float64(i)/(eqShelfGrid-1))
			w := grid[i] / f0
			num := complex(want-w*w, dz*w)
			if !lows {
				num = complex(1-want*w*w, dz*w)
			}
			target[i] = eqDb(cmplx.Abs(num / complex(1-w*w, dp*w)))
			if !raise {
				target[i] -= eqDb(want)
			}
		}
	}
	zr, zo := eqZ(raised), eqZ(other)
	bestErr := math.Inf(1)
	var best EQCompiledBand
	for v := 1; v <= 128; v++ {
		stage, q := eqStage(v, b.Q, lp, 1, hp, stereo)
		fr, fo := eqUnitResponse(&stage[0], zr), eqUnitResponse(&stage[0], zo)
		ends := func(k float64) (float64, float64) {
			return cmplx.Abs(1 + complex(k, 0)*fr), cmplx.Abs(1 + complex(k, 0)*fo)
		}
		// the k that raises the one end by the gain over the other
		lo, hi := 0.0, 64.0
		for range 40 {
			mid := (lo + hi) / 2
			if r, o := ends(mid); r < want*o {
				lo = mid
			} else {
				hi = mid
			}
		}
		g, k, ok := eqGainUnit((lo+hi)/2, stereo)
		if !ok || k <= 0 {
			continue
		}
		c := EQCompiledBand{Makeup: 1, Actual: b}
		c.Units = []Unit{eqUnit("push", stereo, ParamMap{}), stage[0], g, eqUnit("addp", stereo, ParamMap{})}
		c.Units = append(c.Units, stage[1:]...) // the belleq, if any, after the sum
		r, o := ends(k)
		c.Actual.Q = q
		c.Actual.Gain = eqDb(r / o)
		c.Makeup = 1 / o
		if !raise {
			c.Actual.Gain = -c.Actual.Gain
			c.Makeup = 1 / r
		}
		e := math.Abs(c.Actual.Gain - b.Gain)
		for i, f := range grid {
			e = max(e, math.Abs(eqDb(cmplx.Abs(c.Response(f)))-target[i]))
		}
		if e < bestErr {
			bestErr, best = e, c
		}
	}
	if best.Units == nil {
		return ret
	}
	half := best.Actual.Gain / 2
	best.Actual.Frequency = eqCrossing(func(f float64) float64 { return eqDb(cmplx.Abs(best.Response(f))) }, half, b.Frequency)
	return best
}

// eqSearch returns the value from lo to hi for which level is nearest to the
// target.
func eqSearch(lo, hi int, level func(int) float64, target float64) int {
	best, bestErr := lo, math.Inf(1)
	for v := lo; v <= hi; v++ {
		if e := math.Abs(level(v) - target); e < bestErr {
			best, bestErr = v, e
		}
	}
	return best
}

// eqCrossing returns the frequency nearest to near, as a ratio, at which
// level crosses the target, or near if it does not, from 10 Hz to half the
// sample rate.
func eqCrossing(level func(float64) float64, target, near float64) float64 {
	const steps = 400
	lo, hi := math.Log(EQMinFrequency), math.Log(eqSampleRate/2)
	best, bestDist := near, math.Inf(1)
	prevX := lo
	prev := level(math.Exp(lo)) - target
	for i := 1; i <= steps; i++ {
		x := lo + (hi-lo)*float64(i)/steps
		cur := level(math.Exp(x)) - target
		if (prev <= 0) != (cur <= 0) {
			a, b, fa := prevX, x, prev
			for range 30 {
				mid := (a + b) / 2
				if fm := level(math.Exp(mid)) - target; (fm <= 0) == (fa <= 0) {
					a, fa = mid, fm
				} else {
					b = mid
				}
			}
			if d := math.Abs((a+b)/2 - math.Log(near)); d < bestDist {
				best, bestDist = math.Exp((a+b)/2), d
			}
		}
		prevX, prev = x, cur
	}
	return best
}

// eqZ returns z for a frequency in Hz: e^(iω).
func eqZ(freq float64) complex128 {
	return cmplx.Rect(1, 2*math.Pi*freq/eqSampleRate)
}

// EQResponse returns what a chain of units does to a sine of the given
// frequency, in Hz, at 44100 Hz: the factor of its level and the shift of
// its phase, as a complex number. It is computed from the difference
// equations of the units as the synths run them, with their parameters as
// they are, not from the filters they are meant to be. The units are those
// that CompileEQ returns: filter, belleq, ladder (for signals small enough
// to pass its saturator unchanged), gain, invgain, dbgain, and push and addp
// around a parallel path. Other units pass the signal unchanged.
func EQResponse(units []Unit, freq float64) complex128 {
	z := eqZ(freq)
	var buf [4]complex128
	stack := append(buf[:0], 1)
	for i := range units {
		u := &units[i]
		if u.Disabled {
			continue
		}
		top := &stack[len(stack)-1]
		switch u.Type {
		case "push":
			stack = append(stack, *top)
		case "addp":
			if len(stack) > 1 {
				stack[len(stack)-2] += *top
				stack = stack[:len(stack)-1]
			}
		default:
			*top *= eqUnitResponse(u, z)
		}
	}
	return stack[len(stack)-1]
}

// eqUnitResponse returns the transfer function of a unit at z.
func eqUnitResponse(u *Unit, z complex128) complex128 {
	p := func(name string) float64 { return float64(u.Parameters[name]) / 128 }
	q := 1 / z // a delay of one sample
	switch u.Type {
	case "gain":
		return complex(p("gain"), 0)
	case "invgain":
		return complex(1/p("invgain"), 0)
	case "dbgain":
		return complex(math.Pow(10, 4*(p("decibels")-0.5)), 0)
	case "filter":
		// low += f·band; high = x - low - r·band; band += f·high, which gives
		// low = f²z/D, band = f(z²-z)/D and high = (z-1)²/D with
		// D = z² + (rf + f² - 2)z + 1 - rf
		f, r := p("frequency")*p("frequency"), p("resonance")
		d := z*z + complex(r*f+f*f-2, 0)*z + complex(1-r*f, 0)
		low := complex(f*f, 0) * z
		band := complex(f, 0) * (z*z - z)
		high := (z - 1) * (z - 1)
		lp, bp, hp := float64(u.Parameters["lowpass"]), float64(u.Parameters["bandpass"]), float64(u.Parameters["highpass"])
		return (complex(lp, 0)*low + complex(bp, 0)*band + complex(hp, 0)*high) / d
	case "belleq":
		// the peaking filter of the Audio EQ Cookbook, with the cosine of
		// the frequency computed as √(1 - sin²), as the unit does
		sinw := math.Sin(2 * p("frequency") * p("frequency"))
		cosw := math.Sqrt(1 - sinw*sinw)
		alpha := sinw * 2 * p("bandwidth")
		a := math.Pow(10, 2*(p("gain")-0.5))
		up, down := alpha*a, alpha/a
		mid := complex(2*cosw, 0) * q
		return (complex(1+up, 0) - mid + complex(1-up, 0)*q*q) / (complex(1+down, 0) - mid + complex(1-down, 0)*q*q)
	case "ladder":
		// four one-pole low-passes y = g·x + (1-g)·s, s = y + g·(x - s),
		// each g(1 + z⁻¹)/(1 - (1-2g)z⁻¹), with the feedback k around them,
		// solved without delay, and the input times 1 + k/2
		g := min(p("frequency")*p("frequency"), 0.99)
		k := 4.5 * p("resonance")
		one := complex(g, 0) * (1 + q) / (1 - complex(1-2*g, 0)*q)
		four := one * one * one * one
		return complex((1+7*p("drive"))*(1+0.5*k), 0) * four / (1 + complex(k, 0)*four)
	}
	return 1
}

// NumEQUnits returns the number of units that an eq unit stands for.
func (u *Unit) NumEQUnits() int {
	c := u.CompileEQ()
	return len(c.Units)
}

// expandEQs returns the song with every eq unit of its instruments and its
// modules replaced by the units that it stands for. The instruments and
// modules without eq units share their units with s.
func (s *Song) expandEQs() Song {
	replace := func(units []Unit) []Unit {
		var out []Unit
		for i := range units {
			u := &units[i]
			if u.Type != "eq" {
				if out != nil {
					out = append(out, *u)
				}
				continue
			}
			if out == nil {
				out = append(make([]Unit, 0, len(units)+8), units[:i]...)
			}
			if !u.Disabled {
				out = append(out, u.CompileEQ().Units...)
			}
		}
		if out == nil {
			return units
		}
		return out
	}
	ret := *s
	ret.Patch = make(Patch, len(s.Patch))
	for i, instr := range s.Patch {
		ret.Patch[i] = instr
		ret.Patch[i].Units = replace(instr.Units)
	}
	if s.Modules != nil {
		ret.Modules = make(Modules, len(s.Modules))
		for i, m := range s.Modules {
			ret.Modules[i] = m
			ret.Modules[i].Units = replace(m.Units)
		}
	}
	return ret
}

// HasEQs reports whether the song has eq units to expand, in its instruments
// or its modules.
func (s *Song) HasEQs() bool {
	for units := range s.UnitLists() {
		for _, u := range units {
			if u.Type == "eq" {
				return true
			}
		}
	}
	return false
}
