package vm

import (
	"math"
	"math/rand"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	for _, n := range []uint32{1, 2, 8, 256} {
		x := make([]float32, 2*n)
		for i := range x {
			x[i] = rand.Float32()*2 - 1
		}
		want := make([]complex128, n)
		for k := range want {
			for j := uint32(0); j < n; j++ {
				a := -2 * math.Pi * float64(j) * float64(k) / float64(n)
				want[k] += complex(float64(x[2*j]), float64(x[2*j+1])) * complex(math.Cos(a), math.Sin(a))
			}
		}
		fft(x, n)
		for k, w := range want {
			if d := math.Hypot(float64(x[2*k])-real(w), float64(x[2*k+1])-imag(w)); d > 1e-4*float64(n) {
				t.Errorf("n %d: X[%d] = %v%+vi, want %v, off by %v", n, k, x[2*k], x[2*k+1], w, d)
			}
		}
	}
}

func TestPowf(t *testing.T) {
	worst := 0.0
	for _, x := range []float32{1e-9, 1e-5, 0.01, 0.3, 0.7071, 0.99, 1, 1.01, 1.4142, 1.5, 2, 3.7, 100, 12345.6, 1e9} {
		for _, y := range []float32{-3, -2, -1, -0.5, -0.1, 0, 0.1, 0.5, 1, 1.3, 2} {
			want := math.Pow(float64(x), float64(y))
			if want > math.MaxFloat32 || want < 1e-37 {
				continue
			}
			rel := math.Abs(float64(powf(x, y))-want) / want
			worst = max(worst, rel)
			// y·log2(x) is rounded to float32: results far from 1 have
			// relative errors of about 1e-7 times |y·log2(x)|
			if rel > 1e-5 {
				t.Errorf("powf(%v, %v) = %v, want %v", x, y, powf(x, y), want)
			}
		}
	}
	for x := float32(0.5); x < 2; x += 0.001 {
		if rel := math.Abs(float64(log2f(x))-math.Log2(float64(x))) / max(math.Abs(math.Log2(float64(x))), 1); rel > 1e-6 {
			t.Fatalf("log2f(%v) = %v, want %v", x, log2f(x), math.Log2(float64(x)))
		}
	}
	for y := float32(-20); y < 20; y += 0.01 {
		want := math.Exp2(float64(y))
		if rel := math.Abs(float64(exp2f(y))-want) / want; rel > 1e-6 {
			t.Fatalf("exp2f(%v) = %v, want %v", y, exp2f(y), want)
		}
	}
	if powf(2, 0) != 1 || exp2f(0) != 1 {
		t.Errorf("powf(2, 0) = %v, exp2f(0) = %v, want 1", powf(2, 0), exp2f(0))
	}
	t.Logf("worst relative error of powf %g", worst)
}

func TestSinTurns(t *testing.T) {
	for x := float32(-3); x < 3; x += 0.0001 {
		want := math.Sin(2 * math.Pi * float64(x))
		if d := math.Abs(float64(sinTurns(x)) - want); d > 1e-6 {
			t.Fatalf("sinTurns(%v) = %v, want %v", x, sinTurns(x), want)
		}
	}
}
