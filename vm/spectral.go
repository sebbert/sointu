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
	// spectrum is a spectrum buffer: size complex values, interleaved, for
	// each channel, and the number of spectra written to it, so that the
	// units using it know when there is a new one.
	spectrum struct {
		data  []float32
		count uint32
	}

	// spectralState is the state of a spectral unit, kept outside the voice
	// so that retriggering the voice does not reset it: the position in its
	// ring and the count of the spectrum it processed last.
	spectralState struct {
		pos, seen, rng uint32
		ring           []float32
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
		spectra[i] = spectrum{data: make([]float32, 2*size*sp.Channels)}
	}
	states := make([]spectralState, len(s.bytecode.SpectralUnits))
	for i, u := range s.bytecode.SpectralUnits {
		sp := s.bytecode.Spectra[u.Spectrum]
		if old != nil && i < len(old.SpectralUnits) && i < len(s.spectral) {
			if o := old.SpectralUnits[i]; o.Type == u.Type && o.Channels == u.Channels && old.Spectra[o.Spectrum] == sp && (o.Source < 0) == (u.Source < 0) {
				states[i] = s.spectral[i]
				continue
			}
		}
		switch u.Type {
		case "spfft", "spifft": // a ring for each channel of the unit
			states[i].ring = make([]float32, (1<<sp.Log2Size)*u.Channels)
		case "spblur": // the held spectrum
			states[i].ring = make([]float32, (1<<sp.Log2Size+2)*sp.Channels)
		}
	}
	s.spectra, s.spectral = spectra, states
	if len(s.scratch) < 2*maxSize+8 {
		s.scratch = make([]float32, 2*maxSize+8)
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

// spfft pushes a sample of each channel of the unit to its rings, and every
// size/4 samples replaces the spectrum with the FFT of the last size samples,
// windowed. input is left, right.
func (s *GoSynth) spfft(index int, input [2]float32) {
	u := s.bytecode.SpectralUnits[index]
	st, sp := &s.spectral[index], &s.spectra[u.Spectrum]
	n := uint32(1) << s.bytecode.Spectra[u.Spectrum].Log2Size
	for c := range u.Channels {
		st.ring[uint32(c)*n+st.pos] = input[c]
	}
	st.pos = (st.pos + 1) & (n - 1)
	if st.pos&(n/4-1) != 0 {
		return
	}
	hann := hannTable(n)
	for c := range uint32(min(u.Channels, s.bytecode.Spectra[u.Spectrum].Channels)) {
		x, ring := sp.data[2*n*c:2*n*(c+1)], st.ring[n*c:n*(c+1)]
		for j := uint32(0); j < n; j++ {
			x[2*j] = float32(ring[(st.pos+j)&(n-1)] * hann(j))
			x[2*j+1] = 0
		}
		fft(x, n)
	}
	sp.count++
}

// spifft overlap-adds each new spectrum, transformed back and windowed, to the
// rings of the unit, and returns the next sample of each channel times gain:
// left, right. A mono unit averages the channels of a stereo spectrum, a
// stereo unit repeats a mono one.
func (s *GoSynth) spifft(index int, gain float32) (out [2]float32) {
	u := s.bytecode.SpectralUnits[index]
	st, sp := &s.spectral[index], &s.spectra[u.Spectrum]
	n := uint32(1) << s.bytecode.Spectra[u.Spectrum].Log2Size
	spc := uint32(s.bytecode.Spectra[u.Spectrum].Channels)
	rings := min(uint32(u.Channels), spc)
	if sp.count != st.seen {
		st.seen = sp.count
		// the squared Hann windows overlapping by 3/4 sum to 3/2
		scale := float32(0.6666667) / float32(n)
		if spc > rings {
			scale *= 0.5
		}
		hann := hannTable(n)
		for c := range spc {
			// the inverse FFT of a spectrum with conjugate symmetry is real:
			// real(ifft(X)) = real(fft(conj(X)))/n. Bins above n/2 mirror
			// the bins below it; conjugating the mirrored conjugate leaves
			// them as is
			data, x := sp.data[2*n*c:2*n*(c+1)], s.scratch[:2*n]
			for k := uint32(0); k <= n/2; k++ {
				x[2*k], x[2*k+1] = data[2*k], -data[2*k+1]
			}
			for k := n/2 + 1; k < n; k++ {
				x[2*k], x[2*k+1] = data[2*(n-k)], data[2*(n-k)+1]
			}
			fft(x, n)
			ring := st.ring[n*min(c, rings-1):]
			for j := uint32(0); j < n; j++ {
				k := (st.pos + j) & (n - 1)
				ring[k] += float32(float32(x[2*j]*scale) * hann(j))
			}
		}
	}
	for c := range u.Channels {
		out[c] = float32(st.ring[n*min(uint32(c), rings-1)+st.pos] * gain)
	}
	for c := range rings {
		st.ring[n*c+st.pos] = 0
	}
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
		for c := range sp.Channels {
			for k := 0; k <= n/2; k++ {
				dst = append(dst, float32(math.Hypot(float64(data[2*(c*n+k)]), float64(data[2*(c*n+k)+1]))))
			}
		}
		return dst, n
	}
	return dst, 0
}

// spectralFrame returns the channels of the spectrum of a modifying spectral
// unit and its size, if there is a new spectrum the unit has not processed
// yet, marking it processed.
func (s *GoSynth) spectralFrame(index int) ([][]float32, uint32, bool) {
	u := s.bytecode.SpectralUnits[index]
	st, sp := &s.spectral[index], &s.spectra[u.Spectrum]
	if sp.count == st.seen {
		return nil, 0, false
	}
	st.seen = sp.count
	n := uint32(1) << s.bytecode.Spectra[u.Spectrum].Log2Size
	channels := make([][]float32, 0, 2)
	for c := uint32(0); c < uint32(len(sp.data))/(2*n); c++ {
		channels = append(channels, sp.data[2*n*c:2*n*(c+1)])
	}
	return channels, n, true
}

func pow32(a, b float32) float32 { return float32(math.Pow(float64(a), float64(b))) }

func sqrt32(a float32) float32 { return float32(math.Sqrt(float64(a))) }

// spfilter removes the bins below low and above high, and tilts the rest.
func (s *GoSynth) spfilter(index int, low, high, tilt float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	h := n / 2
	lo := float32(h) * float32(float32(pow32(2, float32(low*10))-1)/1023)
	hi := float32(h) * float32(float32(pow32(2, float32(high*10))-1)/1023)
	e := float32(tilt*4) - 2 // amplitude ∝ frequency^e
	ref := float32(n) / 44.1 // the bin of 1 kHz
	for _, x := range xs {
		for k := uint32(0); k <= h; k++ {
			fk := float32(k)
			if fk < lo || fk > hi {
				x[2*k], x[2*k+1] = 0, 0
			} else if e != 0 {
				g := pow32(max(fk, 1)/ref, e)
				x[2*k], x[2*k+1] = x[2*k]*g, x[2*k+1]*g
			}
		}
	}
}

// spcompress scales each bin by (mean/envelope)^amount.
func (s *GoSynth) spcompress(index int, amount, width float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	a := float32(amount*2) - 1
	if a == 0 {
		return
	}
	h := n / 2
	w := int32(1 + uint32(float32(min(max(width, 0), 1)*float32(n/32))))
	for _, x := range xs {
		// prefix sums of the magnitudes
		sums := s.scratch[:h+2]
		sum := float32(0)
		sums[0] = 0
		for k := uint32(0); k <= h; k++ {
			sum += sqrt32(float32(x[2*k]*x[2*k]) + float32(x[2*k+1]*x[2*k+1]))
			sums[k+1] = sum
		}
		mean := sum / float32(h+1)
		for k := int32(0); k <= int32(h); k++ {
			lo, hi := max(k-w, 0), min(k+w, int32(h))
			env := float32(sums[hi+1]-sums[lo]) / float32(hi+1-lo)
			g := pow32(float32(mean+1e-9)/float32(env+1e-9), a)
			x[2*k], x[2*k+1] = x[2*k]*g, x[2*k+1]*g
		}
	}
}

// tablePhase returns the cosine and sine of the phase -πp/128, p modulo 256,
// from the twiddle factors e^(-πik/128), k < 128.
func tablePhase(p uint32) (c, s float32) {
	t := spectralTables()
	j := 127 + p&127
	c, s = t.wr[j], t.wi[j]
	if p&128 != 0 {
		return -c, -s
	}
	return c, s
}

// randomPhase returns the cosine and sine of a random phase, one of 256, and
// the next state of the random number generator.
func randomPhase(rng uint32) (c, s float32, next uint32) {
	next = rng*1664525 + 1013904223
	c, s = tablePhase(next >> 24)
	return
}

// rotate returns x times e^(iθ), given the cosine and sine of θ.
func rotate(xr, xi, c, s float32) (float32, float32) {
	return float32(xr*c) - float32(xi*s), float32(xr*s) + float32(xi*c)
}

// spblur smooths the magnitudes over time, keeping the phases, or while
// frozen holds the magnitudes with random phases, for a steady texture.
func (s *GoSynth) spblur(index int, amount, freeze float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	st := &s.spectral[index]
	for c, x := range xs {
		spblurChannel(st, x, st.ring[uint32(c)*(n+2):], n, amount, freeze)
	}
}

func spblurChannel(st *spectralState, x, y []float32, n uint32, amount, freeze float32) {
	for k := uint32(0); k <= n/2; k++ {
		yr, yi := y[2*k], y[2*k+1]
		if freeze > 0.5 {
			var c, sn float32
			c, sn, st.rng = randomPhase(st.rng)
			m := sqrt32(float32(yr*yr) + float32(yi*yi))
			x[2*k], x[2*k+1] = m*c, m*sn
			continue
		}
		xr, xi := x[2*k], x[2*k+1]
		m := sqrt32(float32(xr*xr) + float32(xi*xi))
		p := sqrt32(float32(yr*yr) + float32(yi*yi))
		b := float32(float32(p-m)*amount) + m
		if m > 0 {
			sc := b / m
			yr, yi = xr*sc, xi*sc
		} else {
			yr, yi = b, 0
		}
		y[2*k], y[2*k+1] = yr, yi
		x[2*k], x[2*k+1] = yr, yi
	}
}

// spgate removes the bins quieter than the threshold, or the louder ones.
func (s *GoSynth) spgate(index int, threshold float32, invert bool) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	// -96 to 0 dB; a full scale sine has the magnitude n/4
	thr := pow32(2, float32(threshold*16)-16) * float32(n/4)
	thr *= thr
	for _, x := range xs {
		for k := uint32(0); k <= n/2; k++ {
			if (float32(float32(x[2*k]*x[2*k])+float32(x[2*k+1]*x[2*k+1])) < thr) != invert {
				x[2*k], x[2*k+1] = 0, 0
			}
		}
	}
}

// spphase changes the phases: disperse rotates bin k by -πp/128, p growing
// with k², random by a random phase, robot blends toward phase 0.
func (s *GoSynth) spphase(index int, mode byte, amount float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	for _, x := range xs {
		spphaseChannel(&s.spectral[index], x, n, mode, amount)
	}
}

func spphaseChannel(st *spectralState, x []float32, n uint32, mode byte, amount float32) {
	h := n / 2
	for k := uint32(0); k <= h; k++ {
		xr, xi := x[2*k], x[2*k+1]
		switch mode {
		case 0: // disperse
			f := float32(float32(float32(amount*float32(k))*float32(k))*64) / float32(h)
			c, sn := tablePhase(uint32(int32(f)))
			xr, xi = rotate(xr, xi, c, sn)
		case 1: // random
			st.rng = st.rng*1664525 + 1013904223
			c, sn := tablePhase(uint32(int32(amount * float32(st.rng>>24))))
			xr, xi = rotate(xr, xi, c, sn)
		default: // robot
			m := sqrt32(float32(xr*xr) + float32(xi*xi))
			xr = float32(float32(m-xr)*amount) + xr
			xi -= float32(xi * amount)
		}
		x[2*k], x[2*k+1] = xr, xi
	}
}

// spscale moves bin k to k*scale+shift.
func (s *GoSynth) spscale(index int, scale, shift float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	h := n / 2
	ratio := pow32(2, float32(scale*2)-1)
	offset := float32(float32(shift*2)-1) * float32(float32(n)*0.022675737) // up to 1 kHz
	out := s.scratch[:2*(h+1)]
	for _, x := range xs {
		clear(out)
		for k := uint32(0); k <= h; k++ {
			j := float32(float32(k)*ratio) + offset
			if j < 0 {
				continue
			}
			if i := uint32(j + 0.5); i <= h {
				out[2*i] += x[2*k]
				out[2*i+1] += x[2*k+1]
			}
		}
		copy(x, out)
	}
}

// spformant moves the envelope, the average magnitude within width, by
// scaling its frequencies.
func (s *GoSynth) spformant(index int, shift, width float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	h := n / 2
	ratio := pow32(2, float32(shift*2)-1)
	w := int32(1 + uint32(float32(min(max(width, 0), 1)*float32(n/32))))
	for _, x := range xs {
		env := s.scratch[n : n+h+1]
		envelope(x, h, w, s.scratch[:h+2], env)
		spformantChannel(x, env, h, ratio)
	}
}

func spformantChannel(x, env []float32, h uint32, ratio float32) {
	for k := uint32(0); k <= h; k++ {
		src := float32(k) / ratio
		i := uint32(src)
		e := env[h]
		if i < h {
			e = float32(float32(env[i+1]-env[i])*float32(src-float32(i))) + env[i]
		}
		g := float32(e+1e-9) / float32(env[k]+1e-9)
		x[2*k], x[2*k+1] = x[2*k]*g, x[2*k+1]*g
	}
}

// envelope writes to env the average magnitudes of the bins of x within w
// bins, using sums for the prefix sums of the magnitudes.
func envelope(x []float32, h uint32, w int32, sums, env []float32) {
	sum := float32(0)
	sums[0] = 0
	for k := uint32(0); k <= h; k++ {
		sum += sqrt32(float32(x[2*k]*x[2*k]) + float32(x[2*k+1]*x[2*k+1]))
		sums[k+1] = sum
	}
	for k := int32(0); k <= int32(h); k++ {
		lo, hi := max(k-w, 0), min(k+w, int32(h))
		env[k] = float32(sums[hi+1]-sums[lo]) / float32(hi+1-lo)
	}
}

// spcross scales each bin by (source envelope/envelope)^amount.
func (s *GoSynth) spcross(index int, amount, width float32) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	u := s.bytecode.SpectralUnits[index]
	if s.bytecode.Spectra[u.Source].Log2Size != s.bytecode.Spectra[u.Spectrum].Log2Size {
		return
	}
	src := s.spectra[u.Source].data
	h := n / 2
	w := int32(uint32(float32(min(max(width, 0), 1) * float32(n/32))))
	for c, x := range xs {
		// a mono source is used for both channels
		sc := min(uint32(c), uint32(len(src))/(2*n)-1)
		// the prefix sums and envelopes of the source, then of the spectrum
		envelope(src[2*n*sc:], h, w, s.scratch[:h+2], s.scratch[h+2:n+3])
		envelope(x, h, w, s.scratch[n+4:n+h+6], s.scratch[n+h+6:2*n+7])
		envS, envX := s.scratch[h+2:], s.scratch[n+h+6:]
		for k := uint32(0); k <= h; k++ {
			g := pow32(float32(envS[k]+1e-9)/float32(envX[k]+1e-9), amount)
			x[2*k], x[2*k+1] = x[2*k]*g, x[2*k+1]*g
		}
	}
}

// noteFrequency returns the frequency of a note in Hz: 69 is A 440 Hz.
func noteFrequency(note int32) float32 {
	return 440 * pow32(2, float32(note-69)/12)
}

// spcomb keeps the bins near the harmonics of up to 8 notes: the notes held
// in the voices given by the operands first and count, or if none, the note
// of its own voice and the intervals in the rest of the operands above it.
func (s *GoSynth) spcomb(index int, note byte, q, amount float32, operands []byte) {
	xs, n, ok := s.spectralFrame(index)
	if !ok {
		return
	}
	f0 := s.scratch[:0:8]
	for v := int(operands[0]); v < int(operands[0])+int(operands[1]) && len(f0) < 8; v++ {
		if voice := &s.state.voices[v]; voice.sustain && voice.note != 0 {
			f0 = append(f0, noteFrequency(int32(voice.note)))
		}
	}
	if len(f0) == 0 && note != 0 {
		f0 = append(f0, noteFrequency(int32(note)))
		for _, i := range operands[2:5] {
			if i != 0 {
				f0 = append(f0, noteFrequency(int32(note)+int32(i)))
			}
		}
	}
	if len(f0) == 0 {
		return
	}
	sharp := float32(q*30) + 2
	binHz := 44100 / float32(n)
	for k := uint32(0); k <= n/2; k++ {
		m := float32(0)
		for _, f := range f0 {
			m = max(m, peak(float32(float32(k)*binHz)/f, sharp))
		}
		g := float32(float32(m-1)*amount) + 1
		for _, x := range xs {
			x[2*k], x[2*k+1] = x[2*k]*g, x[2*k+1]*g
		}
	}
}

// peak returns how close the frequency ratio r is to a harmonic, from 1 on a
// harmonic to 0 at 1/sharp or further; below half of the fundamental, 0.
func peak(r, sharp float32) float32 {
	if r < 0.5 {
		return 0
	}
	d := float32(math.Abs(float64(r - float32(math.RoundToEven(float64(r))))))
	return max(1-float32(d*sharp), 0)
}
