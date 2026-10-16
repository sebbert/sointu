package tracker

import (
	"strconv"

	"github.com/vsariola/sointu"
)

// Analysis: a panel under a unit of the rack shows the signal after the unit
// as the synth plays it, with an oscilloscope and a spectrum (see taps.go).
// Which panels are open is a setting of the tracker, by unit, not part of
// the song: the song has no unit for it, and the synth records the signal
// only while a panel is shown.

type (
	// analysisKey is a unit whose panel is open: by its ID and, for an
	// inner unit, the ID of the outermost module unit it is inside, as the
	// units of a module are inner units of every module unit using it.
	analysisKey struct{ unit, call int }

	// analysisState is what an open panel keeps: which voice it shows,
	// one of the AnalysisVoice values.
	analysisState struct{ voice int }

	// tapScope is the latest frames of the signal of a tap, for an
	// oscilloscope, from the last note of its instrument on.
	tapScope struct {
		ring RingBuffer[[2]float32]
	}

	// UnitAnalysis is what the panel of a unit shows.
	UnitAnalysis struct {
		// Scope is the frames of the signal, as the oscilloscope of the
		// song shows them: written at the cursor, which goes back to the
		// start at each note of the instrument, and as long as that one.
		Scope RingBuffer[[2]float32]
		// Spectrum is that of each channel, in dB for each bin: bin i at
		// (i+1)/len of half the sample rate; nil until there is enough of
		// the signal.
		Spectrum [2][]float32
		// Stereo tells whether the signal is a stereo one: the first of
		// the two signals is then the left channel.
		Stereo bool
		// Before tells that the signal is the one before the unit, as the
		// unit leaves nothing on the stack (out, a popping send, pop).
		Before bool
		// Voice is the voice shown, from 1, or 0 for the sum of all.
		Voice int
	}
)

// The voices that an analysis panel can show: the sum of all the voices of
// the instrument, the voice that the last note played (as the player picks
// it), or one voice, from 1.
const (
	AnalysisVoiceLast = -1
	AnalysisVoiceSum  = 0
)

// AnalysisOpen reports whether the analysis panel of the unit on a row is
// open.
func (m *UnitModel) AnalysisOpen(row int) bool {
	key, ok := (*Model)(m).analysisKey(row)
	return ok && m.analysis[key] != nil
}

// ToggleAnalysis returns an Action that opens or closes the analysis panel
// of the unit on a row.
func (m *UnitModel) ToggleAnalysis(row int) Action {
	return MakeAction(toggleAnalysis{row: row, UnitModel: m})
}

type toggleAnalysis struct {
	row int
	*UnitModel
}

func (m toggleAnalysis) Enabled() bool {
	_, ok := (*Model)(m.UnitModel).analysisKey(m.row)
	return ok
}

func (m toggleAnalysis) Do() {
	key, ok := (*Model)(m.UnitModel).analysisKey(m.row)
	if !ok {
		return
	}
	if m.analysis[key] != nil {
		delete(m.analysis, key)
		// the synth stops recording there at once
		mm := (*Model)(m.UnitModel)
		if tk := (tapKey{Unit: key.unit, Call: key.call, Analysis: true}); mm.taps[tk] != nil {
			delete(mm.taps, tk)
			mm.sendTaps()
		}
		return
	}
	if m.analysis == nil {
		m.analysis = map[analysisKey]*analysisState{}
	}
	m.analysis[key] = &analysisState{}
}

// Analysis returns a Bool that opens and closes the analysis panel of the
// selected unit.
func (m *UnitModel) Analysis() Bool { return MakeBool((*unitAnalysis)(m)) }

type unitAnalysis UnitModel

func (m *unitAnalysis) row() int { return (*Model)(m).rowOfUnit((*Model)(m).d.UnitIndex) }
func (m *unitAnalysis) Enabled() bool {
	return (*UnitModel)(m).ToggleAnalysis(m.row()).Enabled()
}
func (m *unitAnalysis) Value() bool { return (*UnitModel)(m).AnalysisOpen(m.row()) }
func (m *unitAnalysis) SetValue(val bool) {
	if val != m.Value() {
		(*UnitModel)(m).ToggleAnalysis(m.row()).Do()
	}
}

// analysisKey returns the key of the panel of the unit on a row; a unit
// without an ID has none.
func (m *Model) analysisKey(row int) (analysisKey, bool) {
	u, r, ok := m.rowUnit(row)
	if !ok || u.ID == 0 {
		return analysisKey{}, false
	}
	return analysisKey{unit: u.ID, call: r.call}, true
}

// AnalysisVoice returns an Int choosing the voice that the analysis panel of
// the unit on a row shows: AnalysisVoiceLast, AnalysisVoiceSum, or a voice
// of the instrument, from 1.
func (m *UnitModel) AnalysisVoice(row int) Int {
	return MakeInt(analysisVoice{row: row, m: (*Model)(m)})
}

type analysisVoice struct {
	row int
	m   *Model
}

func (v analysisVoice) state() *analysisState {
	key, ok := v.m.analysisKey(v.row)
	if !ok {
		return nil
	}
	return v.m.analysis[key]
}

func (v analysisVoice) Value() int {
	if s := v.state(); s != nil {
		return s.voice
	}
	return AnalysisVoiceSum
}

func (v analysisVoice) SetValue(value int) bool {
	s := v.state()
	if s == nil {
		return false
	}
	s.voice = value
	return true
}

func (v analysisVoice) Range() RangeInclusive {
	voices := 1
	if point, _, ok := v.m.analysisPoint(v.row); ok && point.Instrument < len(v.m.expanded) {
		voices = v.m.expanded[point.Instrument].NumVoices
	}
	return RangeInclusive{AnalysisVoiceLast, max(voices, 1)}
}

func (v analysisVoice) StringOf(value int) string {
	switch value {
	case AnalysisVoiceLast:
		return "Last note"
	case AnalysisVoiceSum:
		return "All voices"
	}
	return "Voice " + strconv.Itoa(value)
}

// analysisPoint returns the place of the signal after the unit on a row, in
// the patch that the synth plays, with voice 0: after the units that it
// stands for (those of a module or an eq unit, the sends that a send to a
// module unit becomes), or before them, if it leaves nothing on the stack.
func (m *Model) analysisPoint(row int) (point sointu.TapPoint, before bool, ok bool) {
	u, r, ok := m.rowUnit(row)
	id := m.playedID(row)
	if !ok || id == 0 || m.expansion == nil {
		return sointu.TapPoint{}, false, false
	}
	instr := m.d.InstrIndex
	if r.module >= 0 {
		instr = m.expansion.Units[id].Instrument
	}
	span, ok := m.expansion.EQs[id]
	if !ok {
		span, ok = m.expansion.Spans[id]
	}
	if !ok {
		if instr < 0 || instr >= len(m.expanded) {
			return sointu.TapPoint{}, false, false
		}
		for i := range m.expanded[instr].Units {
			if m.expanded[instr].Units[i].ID == id {
				span, ok = sointu.ExpandedEQ{Instrument: instr, First: i, Count: 1}, true
				break
			}
		}
		if !ok {
			return sointu.TapPoint{}, false, false
		}
	}
	use := m.d.Song.Modules.StackUse(u)
	before = use.NumOutputs == 0 && len(use.Inputs) > 0
	point = sointu.TapPoint{Instrument: span.Instrument, Unit: span.First + span.Count}
	if before {
		point.Unit = span.First
	}
	return point, before, true
}

// analysisStereo reports whether the signal after a unit is a stereo one:
// that of a unit with stereo set, of a pan, which makes one, or of a module
// unit with two outputs. For a unit that leaves nothing on the stack, it is
// the signal before it.
func (m *Model) analysisStereo(u *sointu.Unit) bool {
	switch {
	case u.Parameters["stereo"] == 1, u.Type == "pan":
		return true
	case u.Type == "module":
		return m.d.Song.Modules.StackUse(u).NumOutputs == 2
	}
	return false
}

// AnalysisOf returns what the analysis panel of the unit on a row shows, and
// whether the panel is open and the unit played. Asking for it keeps the synth recording the
// signal there, for a second (see taps.go).
func (m *UnitModel) AnalysisOf(row int) (ret UnitAnalysis, ok bool) {
	mm := (*Model)(m)
	key, ok := mm.analysisKey(row)
	state := m.analysis[key]
	if !ok || state == nil {
		return UnitAnalysis{}, false
	}
	point, before, ok := mm.analysisPoint(row)
	if !ok {
		return UnitAnalysis{}, false
	}
	u, _, _ := mm.rowUnit(row)
	voices := 1
	if point.Instrument < len(mm.expanded) {
		voices = mm.expanded[point.Instrument].NumVoices
	}
	point.Voice = min(state.voice, voices)
	if state.voice == AnalysisVoiceLast {
		last := 0
		if point.Instrument < len(mm.lastVoices) {
			last = mm.lastVoices[point.Instrument]
		}
		point.Voice = min(max(last, 1), voices)
	}
	ret.Stereo, ret.Before, ret.Voice = mm.analysisStereo(u), before, point.Voice
	w := mm.watchTap(tapKey{Unit: key.unit, Call: key.call, Analysis: true}, point)
	if w.spectrum == nil {
		w.spectrum = &tapSpectrum{}
		w.spectrum.analyzer.init(specAnSettings{ChnMode: SpecChnModeSeparate, Smooth: tapSpectrumSmooth, Resolution: tapSpectrumResolution})
	}
	w.spectrum.stereo = ret.Stereo
	ret.Spectrum = w.spectrum.channels
	if !ret.Stereo {
		ret.Spectrum[1] = nil
	}
	if w.scope == nil {
		w.scope = &tapScope{}
	}
	setSliceLength(&w.scope.ring.Buffer, mm.scopeLength())
	w.scope.ring.Cursor = min(w.scope.ring.Cursor, max(len(w.scope.ring.Buffer)-1, 0))
	ret.Scope = w.scope.ring
	return ret, true
}

// scopeLength returns how many frames the oscilloscope of the song has.
func (m *Model) scopeLength() int {
	return max(m.d.Song.SamplesPerRow()*m.d.Song.RowsPerBeat*max(m.scopeData.lengthInBeats, 1), 1)
}

// triggered is told of each note that the player plays: the instrument,
// from 0, and the voice of the instrument it plays, from 1. The
// oscilloscopes of the taps in the instrument start again.
func (m *Model) triggered(instr, voice int) {
	if instr < 0 {
		return
	}
	for len(m.lastVoices) <= instr {
		m.lastVoices = append(m.lastVoices, 0)
	}
	if voice > 0 {
		m.lastVoices[instr] = voice
	}
	for _, w := range m.taps {
		if w.scope != nil && w.point.Instrument == instr {
			w.scope.ring.Cursor = 0
		}
	}
}

func (s *tapScope) write(buf sointu.AudioBuffer) {
	if len(s.ring.Buffer) > 0 {
		s.ring.WriteWrap(buf)
	}
}
