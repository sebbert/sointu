package vm

import (
	"math"
	"sync"
)

// The spectral units: spfft analyses a signal into a spectrum buffer every
// size/4 samples, other units change the spectrum, and spifft resynthesizes
// it with overlap-add. The arithmetic matches the wasm player operation by
// operation, in float32, with products wrapped in float32() so that they are
// not fused into multiply-adds.

const pi32 = float32(math.Pi)

// maxSpectrumLog2Size is the base 2 logarithm of the largest spectrum size.
const maxSpectrumLog2Size = 13

// spectralTables are the Hann window of the largest spectrum size, and the
// twiddle factors of the FFT stages: the stage combining transforms of size
// half has half factors, e^(-πik/half), at half-1+k. The window of a smaller
// size n is every (max size/n)th value of the table: scaling by a power of 2
// does not change rounding, so they equal sin²(πj/n) computed directly. The
// wasm player computes the same tables when it starts.
var spectralTables = sync.OnceValue(func() (t struct{ hann, wr, wi []float32 }) {
	const n = 1 << maxSpectrumLog2Size
	t.hann = make([]float32, n)
	for j := range t.hann {
		sn := float32(math.Sin(float64(float32(float32(j)*pi32) / float32(n))))
		t.hann[j] = float32(sn * sn)
	}
	t.wr, t.wi = make([]float32, n-1), make([]float32, n-1)
	for half := 1; half < n; half <<= 1 {
		for k := 0; k < half; k++ {
			a := float32(float32(k)*-pi32) / float32(half)
			t.wr[half-1+k] = float32(math.Sin(float64(a + pi32/2)))
			t.wi[half-1+k] = float32(math.Sin(float64(a)))
		}
	}
	return
})

type (
	// spectrum is a spectrum buffer: size complex values, interleaved, and
	// the number of spectra written to it, so that the units using it know
	// when there is a new one.
	spectrum struct {
		data  []float32
		count uint32
	}

	// spectralState is the state of a spectral unit, kept outside the voice
	// so that retriggering the voice does not reset it: the position in its
	// ring and the count of the spectrum it processed last.
	spectralState struct {
		pos, seen uint32
		ring      []float32
	}
)

// setSpectra allocates the spectra and the states of the spectral units of
// the bytecode, keeping the ones of old that did not change.
func (s *GoSynth) setSpectra(old *Bytecode) {
	spectra := make([]spectrum, len(s.bytecode.Spectra))
	maxSize := 0
	for i, sp := range s.bytecode.Spectra {
		size := 1 << sp.Log2Size
		maxSize = max(maxSize, size)
		if old != nil {
			if j := findSpectrum(old.Spectra, sp); j >= 0 {
				spectra[i] = s.spectra[j]
				continue
			}
		}
		spectra[i] = spectrum{data: make([]float32, 2*size)}
	}
	states := make([]spectralState, len(s.bytecode.SpectralUnits))
	for i, u := range s.bytecode.SpectralUnits {
		sp := s.bytecode.Spectra[u.Spectrum]
		if old != nil && i < len(old.SpectralUnits) && i < len(s.spectral) {
			if o := old.SpectralUnits[i]; o.Type == u.Type && old.Spectra[o.Spectrum] == sp && (o.Source < 0) == (u.Source < 0) {
				states[i] = s.spectral[i]
				continue
			}
		}
		if u.Type == "spfft" || u.Type == "spifft" {
			states[i].ring = make([]float32, 1<<sp.Log2Size)
		}
	}
	s.spectra, s.spectral = spectra, states
	if len(s.scratch) < 2*maxSize {
		s.scratch = make([]float32, 2*maxSize)
	}
}

func findSpectrum(spectra []Spectrum, sp Spectrum) int {
	for i, o := range spectra {
		if o == sp {
			return i
		}
	}
	return -1
}

// spfft pushes a sample to the ring of the unit, and every size/4 samples
// replaces the spectrum with the FFT of the last size samples, windowed.
func (s *GoSynth) spfft(index int, input float32) {
	u := s.bytecode.SpectralUnits[index]
	st, sp := &s.spectral[index], &s.spectra[u.Spectrum]
	n := uint32(len(st.ring))
	st.ring[st.pos] = input
	st.pos = (st.pos + 1) & (n - 1)
	if st.pos&(n/4-1) != 0 {
		return
	}
	hann := hannTable(n)
	for j := uint32(0); j < n; j++ {
		sp.data[2*j] = float32(st.ring[(st.pos+j)&(n-1)] * hann(j))
		sp.data[2*j+1] = 0
	}
	fft(sp.data[:2*n], n)
	sp.count++
}

// spifft overlap-adds each new spectrum, transformed back and windowed, to the
// ring of the unit, and returns the next sample of the ring times gain.
func (s *GoSynth) spifft(index int, gain float32) float32 {
	u := s.bytecode.SpectralUnits[index]
	st, sp := &s.spectral[index], &s.spectra[u.Spectrum]
	n := uint32(len(st.ring))
	if sp.count != st.seen {
		st.seen = sp.count
		// the inverse FFT of a spectrum with conjugate symmetry is real:
		// real(ifft(X)) = real(fft(conj(X)))/n. Bins above n/2 mirror the
		// bins below it; conjugating the mirrored conjugate leaves them as is
		x := s.scratch[:2*n]
		for k := uint32(0); k <= n/2; k++ {
			x[2*k], x[2*k+1] = sp.data[2*k], -sp.data[2*k+1]
		}
		for k := n/2 + 1; k < n; k++ {
			x[2*k], x[2*k+1] = sp.data[2*(n-k)], sp.data[2*(n-k)+1]
		}
		fft(x, n)
		// the squared Hann windows overlapping by 3/4 sum to 3/2
		scale := float32(0.6666667) / float32(n)
		hann := hannTable(n)
		for j := uint32(0); j < n; j++ {
			k := (st.pos + j) & (n - 1)
			st.ring[k] += float32(float32(x[2*j]*scale) * hann(j))
		}
	}
	out := float32(st.ring[st.pos] * gain)
	st.ring[st.pos] = 0
	st.pos = (st.pos + 1) & (n - 1)
	return out
}

// spcopy copies each new spectrum of the source to the spectrum of the unit.
func (s *GoSynth) spcopy(index int) {
	u := s.bytecode.SpectralUnits[index]
	st, src, dst := &s.spectral[index], &s.spectra[u.Source], &s.spectra[u.Spectrum]
	if src.count == st.seen {
		return
	}
	st.seen = src.count
	copy(dst.data, src.data)
	dst.count++
}

// hannTable returns the Hann window of size n: sin²(πj/n) at j.
func hannTable(n uint32) func(j uint32) float32 {
	table, stride := spectralTables().hann, (1<<maxSpectrumLog2Size)/n
	return func(j uint32) float32 { return table[j*stride] }
}

// fft transforms n complex values, interleaved, in place: X[k] is the sum of
// x[j]·e^(-2πijk/n). n is a power of 2.
func fft(x []float32, n uint32) {
	// bit reversal permutation
	for i, j := uint32(0), uint32(0); i < n; i++ {
		if i < j {
			x[2*i], x[2*j] = x[2*j], x[2*i]
			x[2*i+1], x[2*j+1] = x[2*j+1], x[2*i+1]
		}
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j |= bit
	}
	// the stage of size 1 has the twiddle factor 1: a sum and a difference
	for i := uint32(0); i+1 < n; i += 2 {
		x[2*i], x[2*i+2] = x[2*i]+x[2*i+2], x[2*i]-x[2*i+2]
		x[2*i+1], x[2*i+3] = x[2*i+1]+x[2*i+3], x[2*i+1]-x[2*i+3]
	}
	t := spectralTables()
	for half := uint32(2); half < n; half <<= 1 {
		for k := uint32(0); k < half; k++ {
			wr, wi := t.wr[half-1+k], t.wi[half-1+k]
			for i := k; i < n; i += 2 * half {
				j := i + half
				tr := float32(wr*x[2*j]) - float32(wi*x[2*j+1])
				ti := float32(wr*x[2*j+1]) + float32(wi*x[2*j])
				x[2*j], x[2*j+1] = x[2*i]-tr, x[2*i+1]-ti
				x[2*i], x[2*i+1] = x[2*i]+tr, x[2*i+1]+ti
			}
		}
	}
}

// Spectrum implements sointu.SpectrumReporter.
func (s *GoSynth) Spectrum(bufferID int, dst []float32) ([]float32, int) {
	for i, sp := range s.bytecode.Spectra {
		if sp.BufferID != bufferID {
			continue
		}
		data, n := s.spectra[i].data, 1<<sp.Log2Size
		for k := 0; k <= n/2; k++ {
			dst = append(dst, float32(math.Hypot(float64(data[2*k]), float64(data[2*k+1]))))
		}
		return dst, n
	}
	return dst, 0
}
