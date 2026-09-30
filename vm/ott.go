package vm

// ottState is the state of an ott unit in a voice: for each channel i, the
// low and band of the 88 Hz crossover at 4i and 4i+1 and of the 2.5 kHz
// crossover at 4i+2 and 4i+3, then the power levels of the low, mid and high
// bands. It does not fit in a unit, so the synths keep the states of all ott
// units in a table of their own, one after the other in the order the units
// run (voice by voice), like delay lines. They are not cleared when a note is
// triggered.
type ottState [11]float32

// The constants of ott, as float32. The crossovers are state-variable filters
// like the filter unit: f = 2·sin(π·fc/44100), damping √2 (Q 0.707).
// Thresholds are base 2 logarithms of the mean square, dB·log2(10)/10. The
// attacks and releases are -log2(e)/(T·44.1) for T in ms, so that the level
// moves by 1-2^(k/s) each sample, where s is the time multiplier. Gains are in
// base 2 logarithms of amplitude: half of the power's.
const (
	ottLowFreq   = 0.012580535 // 88.3 Hz
	ottHighFreq  = 0.35430971  // 2.5 kHz
	ottDamping   = 1.4142135   // √2
	ottDownSlope = 0.49250376  // (1-1/66.7)/2, 66.7:1
	ottUpSlope   = 0.375       // (1-1/4)/2, 4:1
	ottUpMax     = 3.9863138   // 24 dB
)

var ottBands = [3]struct{ attack, release, down, up float32 }{
	{-0.00068439695, -0.00011600771, -11.228117, -13.553467}, // 47.8 ms, 282 ms, -33.8 dB, -40.8 dB
	{-0.0014604542, -0.00011600771, -10.032223, -13.885659},  // 22.4 ms, 282 ms, -30.2 dB, -41.8 dB
	{-0.0024232720, -0.00024783466, -11.792845, -13.553467},  // 13.5 ms, 132 ms, -35.5 dB, -40.8 dB
}

// ott compresses the channels on top of the stack in three bands, upward and
// downward. p holds depth, time, upward, downward and the gains of the low,
// mid and high bands. Matches $su_op_ott in the wasm player, operation by
// operation.
func ott(st *ottState, p *[8]float32, channels int, stack []float32) {
	l := len(stack)
	var bands [2][3]float32
	var power [3]float32
	for i := range channels {
		f := st[4*i : 4*i+4]
		x := stack[l-1-i]
		low := f[0] + float32(ottLowFreq*f[1])
		rest := x - low
		mid := f[2] + float32(ottHighFreq*f[3])
		high := rest - mid
		f[1] += float32(ottLowFreq * float32(rest-float32(ottDamping*f[1])))
		f[3] += float32(ottHighFreq * float32(high-float32(ottDamping*f[3])))
		f[0], f[2] = low, mid
		bands[i] = [3]float32{low, mid, high}
		for b, v := range bands[i] {
			power[b] += float32(v * v)
		}
	}
	inv := exp2f(float32(float32(0.5-p[1]) * 8)) // 1 / the time multiplier
	var gain [3]float32
	for b, c := range ottBands {
		level := st[8+b]
		k := c.attack
		if power[b] < level {
			k = c.release
		}
		level += float32((power[b] - level) * (1 - exp2f(float32(k*inv))))
		st[8+b] = level
		lg := log2f(level)
		g := float32(float32(p[4+b]-0.5) * 8)
		if lg > c.down {
			g -= float32(float32((lg-c.down)*ottDownSlope) * p[3])
		}
		if lg < c.up {
			g += min(float32(float32((c.up-lg)*ottUpSlope)*p[2]), ottUpMax)
		}
		gain[b] = exp2f(g)
	}
	for i := range channels {
		x := stack[l-1-i]
		wet := float32(bands[i][0]*gain[0]) + float32(bands[i][1]*gain[1])
		wet += float32(bands[i][2] * gain[2])
		stack[l-1-i] = x + float32((wet-x)*p[0])
	}
}
