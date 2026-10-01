package vm

import "math"

// The units softclip, width and ladder. Each matches its opcode in the wasm
// player, operation by operation.

// softclip is the softclip unit for one channel: x passes as it is up to the
// knee, bends from there to full scale, reached at 2 - knee, and stays
// there.
func softclip(x, knee float32) float32 {
	a := abs32(x)
	if a > knee {
		if a >= 2-knee {
			a = 1
		} else {
			t := a - knee
			a -= float32(float32(t*t) / float32(4*float32(1-knee)))
		}
	}
	return float32(math.Copysign(float64(a), float64(x)))
}

// The coefficients of the two allpasses of the half-band filter of the
// oversampled softclip: (A0(z²) + z⁻¹·A1(z²))/2 passes up to 15.4 kHz and
// is 44 dB down from 28.7 kHz, at 88.2 kHz.
const (
	halfbandA0 = 0.19104233
	halfbandA1 = 0.66083542
)

// allpass is a first-order allpass (a + z⁻¹)/(1 + a·z⁻¹) with the state s.
func allpass(s *float32, x, a float32) float32 {
	y := float32(a*x) + *s
	*s = x - float32(a*y)
	return y
}

// softclipOversampled is softclip at twice the sample rate, which keeps most
// of the overtones it makes above 22 kHz from folding back: the two
// allpasses of the half-band filter give the two samples for one of the
// input, and after the clipper the same two, crossed, give the average of
// the two. Without clipping what is left is the two allpasses in a row: the
// level at every frequency stays as it is. s is the states of the four
// allpasses.
func softclipOversampled(s []float32, x, knee float32) float32 {
	even := softclip(allpass(&s[0], x, halfbandA0), knee)
	odd := softclip(allpass(&s[1], x, halfbandA1), knee)
	return float32(float32(allpass(&s[2], odd, halfbandA0)+allpass(&s[3], even, halfbandA1)) * 0.5)
}

// widthDamping is the damping of the high-pass on the side signal of the
// width unit: √2, Q 0.707.
const widthDamping = 1.4142135

// width is the width unit: it scales the side signal of left and right by
// 2·w, after a high-pass at the frequency lowcut, whose low and band are
// state[0] and state[1].
func width(state *[8]float32, left, right, w, lowcut float32) (float32, float32) {
	mid := float32(float32(left+right) * 0.5)
	side := float32(float32(left-right) * 0.5)
	freq2 := float32(lowcut * lowcut)
	low := state[0] + float32(freq2*state[1])
	side = float32(side-low) - float32(widthDamping*state[1])
	state[1] += float32(freq2 * side)
	state[0] = low
	side = float32(side * float32(w+w))
	return mid + side, mid - side
}

// ladderFeedback is the feedback of the ladder unit at resonance 1. Four
// low-passes in a row oscillate from a feedback of 4; the saturator in the
// loop takes some of it away, so the most is a little more.
const ladderFeedback = 4.5

// ladderMaxG is the largest coefficient of the low-passes of the ladder
// unit: at 1 they would pass the signal as it is, but their states would
// grow without bound.
const ladderMaxG = 0.99

// ladder is the ladder unit for one channel, whose four low-passes are s.
// The low-passes are trapezoidal one-poles, y = G·x + (1-G)·s, with
// G = frequency², and the feedback has no delay: the output the ladder
// would have without the saturator is solved first, y = L(x)/(1 + k·G⁴),
// where L(x) is x through the four low-passes; the input of the first
// low-pass is then x - k·y through the saturator. This way the resonance
// is at the cutoff and equally strong for every cutoff.
func ladder(s []float32, x, frequency, resonance, drive float32) float32 {
	g := min(float32(frequency*frequency), ladderMaxG)
	k := float32(ladderFeedback * resonance)
	// the gain, and the feedback against half of the input
	x = float32(float32(x*float32(1+float32(7*drive))) * float32(1+float32(0.5*k)))
	y := x
	for _, v := range s {
		y = float32(y*g) + float32(float32(1-g)*v)
	}
	g2 := float32(g * g)
	x -= float32(k * float32(y/float32(1+float32(k*float32(g2*g2)))))
	// the saturator: 1.5·(u - u³/3) for u = x/1.5 clipped to [-1, 1], which
	// has slope 1 at 0 and reaches full scale
	x = clip(float32(x * 0.6666667))
	x = float32(float32(x-float32(float32(float32(x*x)*x)*0.33333334)) * 1.5)
	for i, v := range s {
		d := float32(g * float32(x-v))
		x = d + v
		s[i] = x + d
	}
	return x
}
