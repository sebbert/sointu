package vm

import "math"

// log2f, exp2f, exp2m1f, powf and sinTurns are the float32 math functions of the synths.
// The wasm player computes them the same way, operation by operation, instead
// of calling Math.pow and Math.sin of JavaScript, so that the Go synth and the
// wasm player render exactly the same (unless the wasm player is compiled to
// import them). log2f and exp2f are accurate to about 1e-7 relative, powf to
// about 1e-7 times |y·log2(x)|, as that is rounded to float32, and sinTurns
// to 7e-7.

// log2f returns the base 2 logarithm of x > 0: the exponent of x plus
// ln(m)/ln(2) of its mantissa m in [√½, √2), from the series
// ln(m) = 2(s + s³/3 + s⁵/5 + s⁷/7), s = (m-1)/(m+1).
func log2f(x float32) float32 {
	b := math.Float32bits(x)
	e := int32(b>>23&0xff) - 127
	m := math.Float32frombits(b&0x7fffff | 0x3f800000)
	if m > 1.4142135 {
		m *= 0.5
		e++
	}
	s := (m - 1) / (m + 1)
	s2 := s * s
	p := float32(s2*0.14285715) + 0.2
	p = float32(p*s2) + 0.33333334
	p = float32(p*s2) + 1
	return float32(float32(p*s)*2.8853900) + float32(e) // 2/ln(2)
}

// exp2f returns 2^y, for y clamped to [-126, 126]: 2 to the nearest integer
// i of y times e^z, z = (y-i)·ln(2), from the Taylor series up to z⁶.
func exp2f(y float32) float32 {
	if !(y > -126) { // also NaN
		y = -126
	}
	y = min(y, 126)
	i := float32(math.RoundToEven(float64(y)))
	z := float32(y-i) * 0.6931472
	p := float32(z*0.0013888889) + 0.008333334
	p = float32(p*z) + 0.041666668
	p = float32(p*z) + 0.16666667
	p = float32(p*z) + 0.5
	p = float32(p*z) + 1
	p = float32(p*z) + 1
	return p * math.Float32frombits(uint32(int32(i)+127)<<23)
}

// exp2m1f returns 2^y - 1, also for y near 0, where exp2f(y) - 1 would lose
// its digits: 2^i·e^z - 1 = 2^i·(e^z - 1) + (2^i - 1), for i and z as in
// exp2f, with e^z - 1 from the same series without its constant term.
func exp2m1f(y float32) float32 {
	if !(y > -126) { // also NaN
		y = -126
	}
	y = min(y, 126)
	i := float32(math.RoundToEven(float64(y)))
	z := float32(y-i) * 0.6931472
	p := float32(z*0.0013888889) + 0.008333334
	p = float32(p*z) + 0.041666668
	p = float32(p*z) + 0.16666667
	p = float32(p*z) + 0.5
	p = float32(p*z) + 1
	p *= z
	e := math.Float32frombits(uint32(int32(i)+127) << 23)
	return float32(p*e) + (e - 1)
}

// powf returns x^y for x > 0.
func powf(x, y float32) float32 { return exp2f(float32(y * log2f(x))) }

// sinTurns returns sin(2π·t): t is in turns, not radians. t is folded to x in
// [-1/4, 1/4] turns, where an odd polynomial fitted to sin(2π·x) gives it.
func sinTurns(t float32) float32 {
	x := t - float32(math.RoundToEven(float64(t)))
	if x > 0.25 {
		x = 0.5 - x
	} else if x < -0.25 {
		x = -0.5 - x
	}
	z := x * x
	p := float32(z*-70.9940414) + 81.3408279
	p = float32(p*z) + -41.3371429
	p = float32(p*z) + 6.28316402
	return p * x
}
