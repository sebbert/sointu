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
