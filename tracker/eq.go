package tracker

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vsariola/sointu"
)

// EQ returns the EQ view of the model, with which the editor of the eq unit
// shows and changes the bands of the selected unit, if it is an eq unit.
// Every change goes through Model.change, so it can be undone, and the
// player gets the units that the eq unit then stands for.
func (m *Model) EQ() *EQModel { return (*EQModel)(m) }

type EQModel Model

type eqState struct {
	// band is the selected band
	band int
	// gesture tells if the changes are those of one gesture, e.g. the drag
	// of a handle, which is undone as one: started before its first change,
	// merging from then on
	gesture eqGesture
	// what the unit was last compiled from, and to
	stereo, gain int
	bands        []sointu.EQBand
	compiled     sointu.EQCompiled
	version      int
	// spectrumAsked is when the editor last asked for the spectrum of the
	// master, which it shows behind the curve
	spectrumAsked time.Time
}

type eqGesture int

const (
	eqNoGesture eqGesture = iota
	eqGestureStarted
	eqGestureMerging
)

// The steps of the values of a band when they are changed by keys or by
// scrolling: the frequency by a twelfth of an octave, the gain by half a
// decibel, the Q by a sixth of an octave; a quarter of that when fine.
const (
	EQFrequencyStep = 1.0 / 12 // octaves
	EQGainStep      = 0.5      // dB
	EQQStep         = 1.0 / 6  // octaves
	EQFineStep      = 0.25
)

func (m *EQModel) unit() *sointu.Unit {
	if u := (*Model)(m).selectedUnit(); u != nil && u.Type == "eq" {
		return u
	}
	return nil
}

// Active reports whether the selected unit is an eq unit.
func (m *EQModel) Active() bool { return m.unit() != nil }

// NumBands returns the number of bands of the eq unit.
func (m *EQModel) NumBands() int {
	if u := m.unit(); u != nil {
		return len(u.Bands)
	}
	return 0
}

// Band returns band i of the eq unit, with its values within their ranges.
func (m *EQModel) Band(i int) (sointu.EQBand, bool) {
	if u := m.unit(); u != nil && i >= 0 && i < len(u.Bands) {
		return u.Bands[i].Normalized(), true
	}
	return sointu.EQBand{}, false
}

// Compiled returns what the eq unit stands for, and a number that changes
// whenever that does. ok is false if the selected unit is not an eq unit.
func (m *EQModel) Compiled() (c *sointu.EQCompiled, version int, ok bool) {
	u := m.unit()
	if u == nil {
		return nil, 0, false
	}
	e := &m.eq
	if stereo, gain := u.Parameters["stereo"], u.Parameters["gain"]; e.version == 0 || stereo != e.stereo || gain != e.gain || !slices.Equal(u.Bands, e.bands) {
		e.stereo, e.gain = stereo, gain
		e.bands = append(e.bands[:0], u.Bands...)
		e.compiled = u.CompileEQ()
		e.version++
	}
	return &e.compiled, e.version, true
}

// Selected returns the selected band, or -1 if there are no bands.
func (m *EQModel) Selected() int {
	return min(max(m.eq.band, 0), m.NumBands()-1)
}

// SetSelected selects a band.
func (m *EQModel) SetSelected(i int) { m.eq.band = min(max(i, 0), max(m.NumBands()-1, 0)) }

// BeginGesture tells that the changes that follow, until EndGesture, are
// one gesture, e.g. the drag of a handle: they are undone as one.
func (m *EQModel) BeginGesture() { m.eq.gesture = eqGestureStarted }

// EndGesture ends what BeginGesture started.
func (m *EQModel) EndGesture() { m.eq.gesture = eqNoGesture }

// change changes the eq unit. The changes of a gesture are one step of the
// undo history.
func (m *EQModel) change(f func(u *sointu.Unit)) bool {
	if m.unit() == nil {
		return false
	}
	n := len(m.undoStack)
	merge := m.eq.gesture == eqGestureMerging
	done := (*Model)(m).change("EQ", PatchChange, MajorChange)
	f(m.unit())
	done()
	if merge && len(m.undoStack) == n+1 {
		m.undoStack = m.undoStack[:n] // the step of the gesture is there already
	}
	if m.eq.gesture == eqGestureStarted {
		m.eq.gesture = eqGestureMerging
	}
	return true
}

// roundEQ rounds the values of a band to what is worth saving: three
// digits and a tenth of a Hz for the frequency, a hundredth of a decibel
// and of the Q.
func roundEQ(b sointu.EQBand) sointu.EQBand {
	digits := func(v float64, n int) float64 {
		if v == 0 {
			return 0
		}
		scale := math.Pow(10, float64(n)-math.Ceil(math.Log10(math.Abs(v))))
		return math.Round(v*scale) / scale
	}
	b = b.Normalized()
	b.Frequency = math.Round(digits(b.Frequency, 4)*10) / 10
	b.Gain = math.Round(b.Gain*100) / 100
	b.Q = digits(b.Q, 3)
	return b
}

// Set sets the values of band i.
func (m *EQModel) Set(i int, b sointu.EQBand) bool {
	old, ok := m.Band(i)
	if !ok {
		return false
	}
	if b.Type != old.Type && b.Q == old.Q && old.Q == roundEQ(sointu.EQBand{Type: old.Type}).Q {
		b.Q = 0 // a band that had the Q of its type gets that of the new one
	}
	if b = roundEQ(b); b == m.unit().Bands[i] {
		return false
	}
	return m.change(func(u *sointu.Unit) { u.Bands[i] = b })
}

// Add adds a band, selects it and returns its index, or -1 if the selected
// unit is not an eq unit or has EQMaxBands bands.
func (m *EQModel) Add(b sointu.EQBand) int {
	n := m.NumBands()
	if n >= EQMaxBands || !m.change(func(u *sointu.Unit) { u.Bands = append(slices.Clone(u.Bands), roundEQ(b)) }) {
		return -1
	}
	m.eq.band = n
	return n
}

// EQMaxBands is the most bands the editor adds to an eq unit.
const EQMaxBands = 16

// Delete deletes band i.
func (m *EQModel) Delete(i int) bool {
	if i < 0 || i >= m.NumBands() {
		return false
	}
	ok := m.change(func(u *sointu.Unit) { u.Bands = slices.Delete(slices.Clone(u.Bands), i, i+1) })
	if m.eq.band > i || m.eq.band >= m.NumBands() {
		m.eq.band = max(m.eq.band-1, 0)
	}
	return ok
}

// Step changes the frequency, the gain and the Q of band i by the given
// numbers of steps, a quarter of a step each if fine: see EQFrequencyStep.
func (m *EQModel) Step(i int, frequency, gain, q float64, fine bool) bool {
	b, ok := m.Band(i)
	if !ok {
		return false
	}
	if fine {
		frequency, gain, q = frequency*EQFineStep, gain*EQFineStep, q*EQFineStep
	}
	b.Frequency *= math.Pow(2, frequency*EQFrequencyStep)
	b.Q *= math.Pow(2, q*EQQStep)
	if sointu.EQHasGain(b.Type) {
		b.Gain += gain * EQGainStep
	}
	return m.Set(i, b)
}

// DefaultBand returns the band that a double click at a frequency and a
// level of the plot adds: a low cut below 40 Hz, a high cut above 12 kHz (a
// ladder, as the filter unit does not reach there), a bell with the gain
// of the level between.
func (m *EQModel) DefaultBand(frequency, gain float64) sointu.EQBand {
	switch {
	case frequency < 40:
		return sointu.EQBand{Type: sointu.EQLowCut, Frequency: frequency}
	case frequency > 12000:
		return sointu.EQBand{Type: sointu.EQLadder, Frequency: frequency}
	}
	return sointu.EQBand{Type: sointu.EQBell, Frequency: frequency, Gain: gain}
}

// Type returns an Int for choosing the type of the selected band, among
// sointu.EQBandTypes.
func (m *EQModel) Type() Int { return MakeInt((*eqType)(m)) }

type eqType EQModel

func (v *eqType) Value() int {
	b, _ := (*EQModel)(v).Band((*EQModel)(v).Selected())
	return max(slices.Index(sointu.EQBandTypes, b.Type), 0)
}
func (v *eqType) SetValue(value int) bool {
	i := (*EQModel)(v).Selected()
	b, ok := (*EQModel)(v).Band(i)
	if !ok {
		return false
	}
	b.Type = sointu.EQBandTypes[value]
	return (*EQModel)(v).Set(i, b)
}
func (v *eqType) Range() RangeInclusive { return RangeInclusive{0, len(sointu.EQBandTypes) - 1} }
func (v *eqType) StringOf(value int) string {
	if value < 0 || value >= len(sointu.EQBandTypes) {
		return ""
	}
	return EQTypeName(sointu.EQBandTypes[value])
}

// EQTypeName returns the type of a band as the editor shows it.
func EQTypeName(bandType string) string {
	switch bandType {
	case sointu.EQBell:
		return "Bell"
	case sointu.EQLowCut:
		return "Low cut 12 dB"
	case sointu.EQLowCut24:
		return "Low cut 24 dB"
	case sointu.EQHighCut:
		return "High cut 12 dB"
	case sointu.EQHighCut24:
		return "High cut 24 dB"
	case sointu.EQLadder:
		return "High cut 24 dB ladder"
	case sointu.EQLowShelf:
		return "Low shelf"
	case sointu.EQHighShelf:
		return "High shelf"
	case sointu.EQNotch:
		return "Notch"
	case sointu.EQBandPass:
		return "Band-pass"
	}
	return bandType
}

// On returns a Bool telling whether the selected band is on.
func (m *EQModel) On() Bool { return MakeBool((*eqOn)(m)) }

type eqOn EQModel

func (v *eqOn) Enabled() bool { return (*EQModel)(v).NumBands() > 0 }
func (v *eqOn) Value() bool {
	b, ok := (*EQModel)(v).Band((*EQModel)(v).Selected())
	return ok && !b.Disabled
}
func (v *eqOn) SetValue(val bool) { (*EQModel)(v).SetOn((*EQModel)(v).Selected(), val) }

// SetOn switches band i on or off.
func (m *EQModel) SetOn(i int, on bool) bool {
	b, ok := m.Band(i)
	if !ok {
		return false
	}
	b.Disabled = !on
	return m.Set(i, b)
}

// DeleteBand returns an Action that deletes the selected band, and AddBand
// one that adds a bell at 1 kHz, or between the two bands furthest apart.
func (m *EQModel) DeleteBand() Action { return MakeAction((*eqDelete)(m)) }

type eqDelete EQModel

func (v *eqDelete) Enabled() bool { return (*EQModel)(v).NumBands() > 0 }
func (v *eqDelete) Do()           { (*EQModel)(v).Delete((*EQModel)(v).Selected()) }

func (m *EQModel) AddBand() Action { return MakeAction((*eqAdd)(m)) }

type eqAdd EQModel

func (v *eqAdd) Enabled() bool {
	return (*EQModel)(v).Active() && (*EQModel)(v).NumBands() < EQMaxBands
}
func (v *eqAdd) Do() {
	m := (*EQModel)(v)
	// in the widest gap between the bands, and the ends of the plot
	freqs := []float64{math.Log(20), math.Log(20000)}
	for i := range m.NumBands() {
		b, _ := m.Band(i)
		freqs = append(freqs, math.Log(b.Frequency))
	}
	slices.Sort(freqs)
	at, gap := math.Log(1000), 0.0
	for i := 1; i < len(freqs) && len(freqs) > 2; i++ {
		if d := freqs[i] - freqs[i-1]; d > gap {
			at, gap = (freqs[i]+freqs[i-1])/2, d
		}
	}
	m.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: math.Exp(at)})
}

// Frequency, Gain and Q return Strings with the values of the selected
// band as numbers, as they were set; setting them takes a number, with or
// without its unit (k for kHz).
func (m *EQModel) Frequency() String { return MakeString(&eqNumber{m, 0}) }
func (m *EQModel) Gain() String      { return MakeString(&eqNumber{m, 1}) }
func (m *EQModel) Q() String         { return MakeString(&eqNumber{m, 2}) }

type eqNumber struct {
	m     *EQModel
	which int
}

func (v *eqNumber) Value() string {
	b, ok := v.m.Band(v.m.Selected())
	if !ok {
		return ""
	}
	switch v.which {
	case 0:
		return strconv.FormatFloat(b.Frequency, 'f', -1, 64)
	case 1:
		if !sointu.EQHasGain(b.Type) {
			return ""
		}
		return strconv.FormatFloat(b.Gain, 'f', -1, 64)
	}
	return strconv.FormatFloat(b.Q, 'f', -1, 64)
}

func (v *eqNumber) SetValue(s string) bool {
	i := v.m.Selected()
	b, ok := v.m.Band(i)
	if !ok {
		return false
	}
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ",", ".")
	scale := 1.0
	for _, suffix := range []string{"hz", "db", "q"} {
		s = strings.TrimSpace(strings.TrimSuffix(s, suffix))
	}
	if strings.HasSuffix(s, "k") {
		s, scale = strings.TrimSpace(strings.TrimSuffix(s, "k")), 1000
	}
	x, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return false
	}
	x *= scale
	switch v.which {
	case 0:
		if x <= 0 {
			return false
		}
		b.Frequency = x
	case 1:
		if !sointu.EQHasGain(b.Type) {
			return false
		}
		b.Gain = x
	default:
		if x <= 0 {
			return false
		}
		b.Q = x
	}
	v.m.Set(i, b)
	return true
}

// FormatEQFrequency returns a frequency as the editor shows it: 84 Hz,
// 1.25 kHz.
func FormatEQFrequency(f float64) string {
	switch {
	case f >= 10000:
		return strconv.FormatFloat(f/1000, 'f', 1, 64) + " kHz"
	case f >= 1000:
		return strconv.FormatFloat(f/1000, 'f', 2, 64) + " kHz"
	case f >= 100:
		return strconv.FormatFloat(f, 'f', 0, 64) + " Hz"
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " Hz"
}

// Info returns what the selected band is compiled to, as text: the values
// that its units have, how many units, and whether all players have them.
func (m *EQModel) Info() string {
	c, _, ok := m.Compiled()
	i := m.Selected()
	if !ok || i < 0 || i >= len(c.Bands) {
		return ""
	}
	band := &c.Bands[i]
	a := band.Actual
	if a.Disabled {
		return "off: no units"
	}
	var s strings.Builder
	s.WriteString(FormatEQFrequency(a.Frequency))
	if sointu.EQHasGain(a.Type) {
		s.WriteString(" · " + strconv.FormatFloat(a.Gain, 'f', 2, 64) + " dB")
	}
	s.WriteString(" · Q " + strconv.FormatFloat(a.Q, 'f', 2, 64))
	switch n := len(band.Units); {
	case n == 0:
		s.WriteString(" · does nothing: no units")
	case n == 1:
		s.WriteString(" · 1 unit")
	default:
		s.WriteString(" · " + strconv.Itoa(n) + " units")
	}
	if band.Makeup != 1 {
		s.WriteString(" + gain")
	}
	if band.GoWasmOnly {
		s.WriteString(" · Go synth and wasm only")
	}
	return s.String()
}

// Units returns the number of units that the eq unit stands for, and the
// gain of the gain unit among them in dB, if there is one.
func (m *EQModel) Units() (units int, gain float64, hasGain bool) {
	c, _, ok := m.Compiled()
	if !ok {
		return 0, 0, false
	}
	return len(c.Units), 20 * math.Log10(c.ActualGain), len(c.GainUnits) > 0
}

// Spectrum returns the spectrum of the master, as the spectrum analyzer has
// it, which the editor shows behind the curve. Asking for it keeps the
// spectrum analyzer running for a second, also when its own panel is closed.
func (m *EQModel) Spectrum() Spectrum {
	m.eq.spectrumAsked = time.Now()
	return *m.spectrum
}

// spectrumWanted reports whether the editor of the eq unit has asked for the
// spectrum of the master within the last second.
func (m *Model) spectrumWanted() bool {
	return !m.eq.spectrumAsked.IsZero() && time.Since(m.eq.spectrumAsked) < time.Second
}
