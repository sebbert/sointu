package tracker

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// newAnalysisTest returns a tapTest of a song whose instrument of two
// voices is a sine at the note through an envelope, panned into stereo, and
// put out, then an instrument of noise; the first is selected.
func newAnalysisTest(t *testing.T) *tapTest {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	r := &tapTest{t: t, m: m, broker: broker, player: NewPlayer(broker, vm.GoSynther{})}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{
			{Name: "sine", NumVoices: 2, Units: []sointu.Unit{
				{ID: 4, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 64, "sustain": 128, "release": 64, "gain": 128}},
				{ID: 1, Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 0}},
				{ID: 5, Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{ID: 2, Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 32}},
				{ID: 3, Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64}},
			}},
			{Name: "noise", NumVoices: 1, Units: []sointu.Unit{
				{ID: 11, Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{ID: 13, Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
			}}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 2}, {NumVoices: 1}}
	}()
	m.d.InstrIndex, m.d.UnitIndex, m.d.UnitIndex2 = 0, 1, 1
	return r
}

// note plays a note of the sine instrument: 494 Hz, note 83.
func (r *tapTest) note(on bool) {
	TrySend(r.broker.ToPlayer, any(&NoteEvent{On: on, Channel: 0, Note: 83, Source: r}))
}

// TestAnalysis checks the analysis panels of units: the place of the signal
// after each, the voice shown, the oscilloscope and the spectra of each
// channel, and that a closed panel records nothing.
func TestAnalysis(t *testing.T) {
	r := newAnalysisTest(t)
	m := r.m
	if _, ok := m.Unit().AnalysisOf(1); ok || m.Unit().AnalysisOpen(1) {
		t.Fatal("a panel is open before it was opened")
	}
	m.Unit().Analysis().SetValue(true) // the oscillator, selected
	m.Unit().ToggleAnalysis(3).Do()
	m.Unit().ToggleAnalysis(4).Do()
	rows := []int{1, 3, 4}
	for i, want := range []struct {
		unit           int
		stereo, before bool
	}{{2, false, false}, {4, true, false}, {4, true, true}} {
		point, before, ok := m.analysisPoint(rows[i])
		a, open := m.Unit().AnalysisOf(rows[i])
		if !ok || !open || point != (sointu.TapPoint{Unit: want.unit}) || before != want.before || a.Before != want.before || a.Stereo != want.stereo {
			t.Errorf("row %d: the panel is at %+v (open %v), before %v, stereo %v", rows[i], point, open, a.Before, a.Stereo)
		}
	}
	// the panel of the oscillator follows the last note to its voice
	m.Unit().AnalysisVoice(1).SetValue(AnalysisVoiceLast)
	if got := m.Unit().AnalysisVoice(1).Range(); got != (RangeInclusive{AnalysisVoiceLast, 2}) {
		t.Errorf("the voices to choose from are %v", got)
	}
	// the sine after the oscillator and after the pan, in the oscilloscope
	// and the spectrum: a peak at 494 Hz; the pan puts more of it left
	ask := func() {
		for _, row := range rows {
			m.Unit().AnalysisOf(row)
		}
	}
	r.spectra = func() ([]float32, []float32) { ask(); return nil, nil }
	r.note(true)
	r.run(40, true)
	osc, _ := m.Unit().AnalysisOf(1)
	pan, _ := m.Unit().AnalysisOf(3)
	if len(osc.Scope.Buffer) != m.scopeLength() || osc.Scope.Cursor == 0 {
		t.Fatalf("the oscilloscope has %d frames, the cursor at %d", len(osc.Scope.Buffer), osc.Scope.Cursor)
	}
	peak := float32(0)
	for _, f := range osc.Scope.Buffer[:osc.Scope.Cursor] {
		peak = max(peak, f[0])
	}
	if peak < 0.9 || peak > 1.01 {
		t.Errorf("the oscilloscope after the sine peaks at %v", peak)
	}
	if len(osc.Spectrum[0]) != 2048 || osc.Spectrum[1] != nil || len(pan.Spectrum[1]) != 2048 {
		t.Fatalf("the spectra have %d, %d and %d bins", len(osc.Spectrum[0]), len(osc.Spectrum[1]), len(pan.Spectrum[1]))
	}
	if at, off := tapMax(osc.Spectrum[0], 450, 550), tapMax(osc.Spectrum[0], 4000, 8000); at < -20 || at-off < 40 {
		t.Errorf("the spectrum of the sine is at %.1f dB at 494 Hz and at %.1f dB from 4 to 8 kHz", at, off)
	}
	if l, rt := tapMax(pan.Spectrum[0], 450, 550), tapMax(pan.Spectrum[1], 450, 550); math.Abs(l-rt) < 1 {
		t.Errorf("the pan leaves both channels at %.1f and %.1f dB", l, rt)
	}
	// a second note, while the first goes on, plays the other voice: the
	// panel follows it, and its oscilloscope starts again
	first := osc.Voice
	r.note(true)
	r.run(1, true)
	osc, _ = m.Unit().AnalysisOf(1)
	if first < 1 || osc.Voice < 1 || osc.Voice == first || osc.Scope.Cursor > 1024 {
		t.Errorf("the notes are in voices %d and %d, the oscilloscope at %d", first, osc.Voice, osc.Scope.Cursor)
	}
	r.run(1, true) // the player gets the new place with the next buffer
	found := false
	for _, p := range r.player.taps {
		found = found || p == sointu.TapPoint{Unit: 2, Voice: osc.Voice}
	}
	if !found {
		t.Errorf("the player taps %v", r.player.taps)
	}
	// closed: the watches end, and the synth records nothing more, at once
	for _, row := range rows {
		m.Unit().ToggleAnalysis(row).Do()
	}
	if _, ok := m.Unit().AnalysisOf(1); ok || m.Unit().Analysis().Value() {
		t.Error("a closed panel is open")
	}
	r.run(1, false)
	if len(m.taps) != 0 || len(r.player.taps) != 0 {
		t.Errorf("with the panels closed, %d watches and %d taps are left", len(m.taps), len(r.player.taps))
	}
}

// TestAnalysisInModule checks that the panel of a module unit shows the
// signal after the units of its module, and that of an inner unit the
// signal after its copy, apart from the panel of the same unit in another
// module unit.
func TestAnalysisInModule(t *testing.T) {
	r := newAnalysisTest(t)
	m := r.m
	m.Unit().List().SetSelected(1)
	m.Unit().List().SetSelected2(3)
	m.Unit().MakeModule().Do() // oscillator, mulp and pan
	if u := m.d.Song.Patch[0].Units[1]; u.Type != "module" {
		t.Fatalf("no module: %+v", m.d.Song.Patch[0].Units)
	}
	m.Unit().ToggleAnalysis(1).Do()
	if p, _, ok := m.analysisPoint(1); !ok || p != (sointu.TapPoint{Unit: 4}) {
		t.Errorf("the module unit is at %+v", p)
	}
	m.Unit().ToggleUnfold(1).Do()
	m.Unit().ToggleAnalysis(2).Do() // the oscillator inside it
	if p, _, ok := m.analysisPoint(2); !ok || p != (sointu.TapPoint{Unit: 2}) {
		t.Errorf("the oscillator in the module unit is at %+v", p)
	}
	if !m.Unit().AnalysisOpen(1) || !m.Unit().AnalysisOpen(2) || m.Unit().AnalysisOpen(3) {
		t.Error("other panels are open than those opened")
	}
	r.note(true)
	r.spectra = func() ([]float32, []float32) {
		m.Unit().AnalysisOf(1)
		m.Unit().AnalysisOf(2)
		return nil, nil
	}
	r.run(20, true)
	if a, ok := m.Unit().AnalysisOf(1); !ok || !a.Stereo {
		t.Error("the module unit, ending with the pan, has no stereo signal")
	}
	if a, ok := m.Unit().AnalysisOf(2); !ok || len(a.Spectrum[0]) == 0 || a.Stereo {
		t.Errorf("the oscillator in the module has no spectrum (%v), or a stereo one", ok)
	}
}

// tapMax returns the highest level of the bins from lo to hi Hz, in dB.
func tapMax(levels []float32, lo, hi float64) float64 {
	ret := math.Inf(-1)
	for i, l := range levels {
		if f := float64(i+1) / float64(len(levels)) * 22050; f >= lo && f <= hi {
			ret = max(ret, float64(l))
		}
	}
	return ret
}
