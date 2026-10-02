package mcp

import (
	"fmt"
	"math"
	"math/cmplx"
	"sort"
	"strings"

	"github.com/vsariola/sointu"
)

const sampleRate = 44100.0

// Measurements are what Measure finds in a render: numbers for someone who
// cannot hear it.
type Measurements struct {
	Seconds float64 // length of the render
	Silent  bool    // nothing above -120 dBFS

	Peak        float64    // dBFS, the larger of the channels
	PeakChannel [2]float64 // dBFS
	PeakAt      float64    // seconds
	Clipped     int        // samples above full scale
	RMS         float64    // dB, of both channels
	Crest       float64    // dB, Peak - RMS
	DC          [2]float64 // mean of each channel
	Correlation float64    // of left and right, -1 to 1
	SideToMid   float64    // dB, the level of (l-r)/2 against (l+r)/2

	Bands   []Band    // octave bands
	Pitch   float64   // Hz, the median of PitchAt; 0 if none was found
	PitchAt []float64 // Hz, at evenly spaced times of the render; 0 where none was found

	EnvelopeStep float64   // seconds
	Envelope     []float64 // dB RMS of each step

	Attack     float64 // seconds from the start of the sound to 90 % of its peak
	Onset      float64 // seconds until the sound starts
	TailTo60   float64 // seconds after the release until the level stays below -60 dBFS; negative if it does not
	TailAtEnd  float64 // dB RMS of the last 10 ms
	HasRelease bool
}

// Band is the level of one octave band.
type Band struct {
	Center float64 // Hz
	Level  float64 // dB: the levels of all bands add up to the RMS
}

func db(amplitude float64) float64 {
	if amplitude <= 1e-10 {
		return -200
	}
	return 20 * math.Log10(amplitude)
}

// Measure measures a render. release is the frame where the note was
// released, or negative if it was not.
func Measure(buf sointu.AudioBuffer, release int) Measurements {
	m := Measurements{Seconds: float64(len(buf)) / sampleRate, HasRelease: release >= 0 && release < len(buf)}
	if len(buf) == 0 {
		m.Silent = true
		return m
	}
	n := float64(len(buf))
	var peak [2]float64
	var sum, sumSq [2]float64
	var lr, mid, side float64
	peakAt := 0
	for i, f := range buf {
		for c := 0; c < 2; c++ {
			v := float64(f[c])
			if a := math.Abs(v); a > peak[c] {
				peak[c] = a
				if a >= max(peak[0], peak[1]) {
					peakAt = i
				}
			}
			if math.Abs(v) > 1 {
				m.Clipped++
			}
			sum[c] += v
			sumSq[c] += v * v
		}
		l, r := float64(f[0]), float64(f[1])
		lr += l * r
		mid += (l + r) * (l + r) / 4
		side += (l - r) * (l - r) / 4
	}
	top := max(peak[0], peak[1])
	if top < 1e-6 {
		m.Silent = true
		return m
	}
	m.Peak, m.PeakChannel, m.PeakAt = db(top), [2]float64{db(peak[0]), db(peak[1])}, float64(peakAt)/sampleRate
	rms := math.Sqrt((sumSq[0] + sumSq[1]) / (2 * n))
	m.RMS = db(rms)
	m.Crest = m.Peak - m.RMS
	m.DC = [2]float64{sum[0] / n, sum[1] / n}
	if d := math.Sqrt(sumSq[0] * sumSq[1]); d > 0 {
		m.Correlation = lr / d
	}
	m.SideToMid = db(math.Sqrt(side/n)) - db(math.Sqrt(mid/n))

	mono := make([]float64, len(buf))
	for i, f := range buf {
		mono[i] = (float64(f[0]) + float64(f[1])) / 2
	}
	m.Bands = bands(buf, rms)
	m.PitchAt = pitches(mono, 8)
	var found []float64
	for _, p := range m.PitchAt {
		if p > 0 {
			found = append(found, p)
		}
	}
	if len(found) > 0 {
		sort.Float64s(found)
		m.Pitch = found[len(found)/2]
	}

	// the envelope: about 16 steps, of at least 5 ms
	step := max(len(buf)/16, int(sampleRate)/200)
	m.EnvelopeStep = float64(step) / sampleRate
	for i := 0; i+step <= len(buf) || i == 0; i += step {
		m.Envelope = append(m.Envelope, rmsOf(buf, i, min(i+step, len(buf))))
	}

	// onset and attack: from where the sound is first 60 dB below its peak to
	// where it first reaches 90 % of it
	onset := 0
	for i, f := range buf {
		if max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))) > top/1000 {
			onset = i
			break
		}
	}
	m.Onset = float64(onset) / sampleRate
	for i := onset; i < len(buf); i++ {
		if max(math.Abs(float64(buf[i][0])), math.Abs(float64(buf[i][1]))) >= 0.9*top {
			m.Attack = float64(i-onset) / sampleRate
			break
		}
	}

	win := int(sampleRate) / 100
	m.TailAtEnd = rmsOf(buf, max(len(buf)-win, 0), len(buf))
	m.TailTo60 = -1
	if m.HasRelease {
		// the last window after the release that is not below -60 dBFS
		last := release
		quiet := false
		for i := release; i < len(buf); i += win {
			if rmsOf(buf, i, min(i+win, len(buf))) > -60 {
				last = min(i+win, len(buf))
			}
		}
		quiet = last < len(buf)
		if quiet {
			m.TailTo60 = float64(last-release) / sampleRate
		}
	}
	return m
}

func rmsOf(buf sointu.AudioBuffer, from, to int) float64 {
	if to <= from {
		return -200
	}
	s := 0.0
	for _, f := range buf[from:to] {
		s += float64(f[0])*float64(f[0]) + float64(f[1])*float64(f[1])
	}
	return db(math.Sqrt(s / float64(2*(to-from))))
}

// bands returns the levels of the octave bands from 31.5 Hz to 16 kHz, from
// the mean of the power spectra of Hann windows, half overlapping, of both
// channels, scaled so that the powers of the bands add up to that of the
// signal, whose rms is given.
func bands(buf sointu.AudioBuffer, rms float64) []Band {
	size := 8192
	for size > len(buf) && size > 256 {
		size /= 2
	}
	window := make([]float64, size)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(size))
	}
	power := make([]float64, size/2+1)
	frame := make([]complex128, size)
	for start := 0; start == 0 || start+size <= len(buf); start += size / 2 {
		for c := 0; c < 2; c++ {
			for i := range frame {
				v := 0.0
				if start+i < len(buf) {
					v = float64(buf[start+i][c])
				}
				frame[i] = complex(v*window[i], 0)
			}
			fft(frame)
			for k := range power {
				p := real(frame[k])*real(frame[k]) + imag(frame[k])*imag(frame[k])
				if k > 0 && k < size/2 {
					p *= 2 // and the negative frequency
				}
				power[k] += p
			}
		}
	}
	total := 0.0
	for _, p := range power {
		total += p
	}
	if total <= 0 {
		total = 1
	}
	norm := rms * rms / total
	var ret []Band
	for center := 31.25; center < 20000; center *= 2 {
		lo, hi := center/math.Sqrt2, center*math.Sqrt2
		if center == 31.25 {
			lo = 0 // with everything below
		}
		if center > 15000 {
			hi = sampleRate // and above
		}
		s := 0.0
		for k := range power {
			if f := float64(k) * sampleRate / float64(size); f >= lo && f < hi {
				s += power[k]
			}
		}
		ret = append(ret, Band{Center: center, Level: db(math.Sqrt(s * norm))})
	}
	return ret
}

// fft transforms a in place; its length is a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		w := cmplx.Rect(1, -2*math.Pi/float64(length))
		for i := 0; i < n; i += length {
			x := complex(1, 0)
			for j := 0; j < length/2; j++ {
				u, v := a[i+j], a[i+j+length/2]*x
				a[i+j], a[i+j+length/2] = u+v, u-v
				x *= w
			}
		}
	}
}

// pitches estimates the pitch at count evenly spaced times: see pitchOf.
func pitches(mono []float64, count int) []float64 {
	const window = 4096
	if len(mono) < window {
		return []float64{pitchOf(mono)}
	}
	ret := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		start := 0
		if count > 1 {
			start = (len(mono) - window) * i / (count - 1)
		}
		ret = append(ret, pitchOf(mono[start:start+window]))
	}
	return ret
}

// pitchOf estimates the pitch of a signal, from 25 Hz to 4 kHz, like YIN:
// the first lag where the difference of the signal and itself delayed,
// relative to its mean over the lags before, has a minimum below 0.15. It
// returns 0 if there is none, as for noise and silence.
func pitchOf(x []float64) float64 {
	maxLag := min(int(sampleRate)/25, len(x)/2)
	minLag := int(sampleRate) / 4000
	if maxLag <= minLag+2 {
		return 0
	}
	energy := 0.0
	for _, v := range x {
		energy += v * v
	}
	if energy/float64(len(x)) < 1e-8 {
		return 0
	}
	n := len(x) - maxLag
	d := make([]float64, maxLag+1)
	running := 0.0
	for lag := 1; lag <= maxLag; lag++ {
		s := 0.0
		for i := 0; i < n; i++ {
			diff := x[i] - x[i+lag]
			s += diff * diff
		}
		running += s
		if running > 0 {
			d[lag] = s * float64(lag) / running
		} else {
			d[lag] = 1
		}
	}
	for lag := minLag; lag < maxLag; lag++ {
		if d[lag] >= 0.15 {
			continue
		}
		for lag+1 < maxLag && d[lag+1] < d[lag] {
			lag++
		}
		// the minimum of the parabola through the three lags
		a, b, c := d[lag-1], d[lag], d[lag+1]
		shift := 0.0
		if den := a - 2*b + c; den != 0 {
			shift = 0.5 * (a - c) / den
		}
		return sampleRate / (float64(lag) + shift)
	}
	return 0
}

var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// noteOf names a frequency: the nearest note and how many cents off it is.
func noteOf(hz float64) string {
	if hz <= 0 {
		return ""
	}
	midi := 69 + 12*math.Log2(hz/440)
	nearest := math.Round(midi)
	cents := int(math.Round((midi - nearest) * 100))
	name := fmt.Sprintf("%s%d", noteNames[(int(nearest)%12+12)%12], int(nearest)/12-1)
	if cents == 0 {
		return name
	}
	return fmt.Sprintf("%s %+d cents", name, cents)
}

func hzString(hz float64) string {
	if hz >= 1000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", hz/1000), ".0") + "k"
	}
	return fmt.Sprintf("%.0f", hz)
}

// String formats the measurements compactly, for a language model to read.
func (m Measurements) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "length %.3f s", m.Seconds)
	if m.Silent {
		b.WriteString("\nsilent: nothing above -120 dBFS")
		return b.String()
	}
	fmt.Fprintf(&b, "\npeak %.1f dBFS (L %.1f, R %.1f) at %.3f s | rms %.1f dB | crest %.1f dB", m.Peak, m.PeakChannel[0], m.PeakChannel[1], m.PeakAt, m.RMS, m.Crest)
	if m.Clipped > 0 {
		fmt.Fprintf(&b, " | %d samples above full scale", m.Clipped)
	}
	fmt.Fprintf(&b, "\ndc L %.4f R %.4f | correlation %.2f | side/mid %.1f dB", m.DC[0], m.DC[1], m.Correlation, m.SideToMid)
	if m.Pitch > 0 {
		fmt.Fprintf(&b, "\npitch %.1f Hz (%s); over time:", m.Pitch, noteOf(m.Pitch))
		for _, p := range m.PitchAt {
			if p > 0 {
				fmt.Fprintf(&b, " %.1f", p)
			} else {
				b.WriteString(" -")
			}
		}
	} else {
		b.WriteString("\npitch: none found (noise, a chord, or too short)")
	}
	b.WriteString("\noctave bands, dB:")
	for _, band := range m.Bands {
		fmt.Fprintf(&b, " %s:%.0f", hzString(band.Center), band.Level)
	}
	fmt.Fprintf(&b, "\nenvelope, dB rms per %.0f ms:", m.EnvelopeStep*1000)
	for _, e := range m.Envelope {
		fmt.Fprintf(&b, " %.0f", e)
	}
	fmt.Fprintf(&b, "\nonset %.1f ms | attack to 90 %% of the peak %.1f ms", m.Onset*1000, m.Attack*1000)
	if m.HasRelease {
		if m.TailTo60 >= 0 {
			fmt.Fprintf(&b, " | below -60 dBFS %.3f s after the release", m.TailTo60)
		} else {
			fmt.Fprintf(&b, " | not below -60 dBFS at the end (%.0f dB): render a longer tail", m.TailAtEnd)
		}
	}
	return b.String()
}
