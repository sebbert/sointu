package tracker

import (
	"math"
	"math/cmplx"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// tapTest is a model and a player of a song whose instrument is noise
// through an eq unit; run plays buffers and passes what the player sends to
// the model, asking for the spectra of the eq before each, like the editor.
type tapTest struct {
	t       *testing.T
	m       *Model
	broker  *Broker
	player  *Player
	spectra func() ([]float32, []float32)
}

func newTapTest(t *testing.T, bands ...sointu.EQBand) *tapTest {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	r := &tapTest{t: t, m: m, broker: broker, player: NewPlayer(broker, vm.GoSynther{})}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{
			{Name: "other", NumVoices: 2, Units: []sointu.Unit{{ID: 20, Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}, {ID: 21, Type: "pop", Parameters: sointu.ParamMap{"stereo": 0}}}},
			{Name: "noise", NumVoices: 1, Units: []sointu.Unit{
				{ID: 1, Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 64}},
				{ID: 2, Type: "eq", Parameters: sointu.ParamMap{"stereo": 0}, Bands: bands},
				{ID: 3, Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
			}}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 1}}
	}()
	m.d.InstrIndex, m.d.UnitIndex, m.d.UnitIndex2 = 1, 1, 1
	if !m.EQ().Active() {
		t.Fatal("the eq unit is not selected")
	}
	r.spectra = m.EQ().Spectra
	return r
}

// run plays n buffers of 1024 frames.
func (r *tapTest) run(n int, ask bool) (before, after []float32) {
	r.t.Helper()
	buf := make(sointu.AudioBuffer, 1024)
	for range n {
		if ask {
			before, after = r.spectra()
		}
		r.player.Process(buf, NullPlayerProcessContext{})
		for more := true; more; {
			select {
			case msg := <-r.broker.ToModel:
				r.m.ProcessMsg(msg)
			default:
				more = false
			}
		}
	}
	return before, after
}

// mean returns the mean level of the bins from lo to hi Hz, in dB.
func tapMean(levels []float32, lo, hi float64) float64 {
	sum, n := 0.0, 0
	for i, l := range levels {
		if f := float64(i+1) / float64(len(levels)) * 22050; f >= lo && f <= hi {
			sum += float64(l)
			n++
		}
	}
	return sum / float64(n)
}

// TestEQSpectra checks that the editor of the eq unit gets the spectrum of
// the signal before and after the eq from the synth, that they follow the
// unit when the units of the eq change, and that the synth stops recording
// once nobody asks.
func TestEQSpectra(t *testing.T) {
	r := newTapTest(t, sointu.EQBand{Type: sointu.EQLowCut24, Frequency: 2000, Q: 0.707})
	before, after := r.run(60, true)
	if len(before) != 2048 || len(after) != 2048 {
		t.Fatalf("the spectra have %d and %d bins", len(before), len(after))
	}
	if got := r.player.taps; len(got) != 2 || got[0] != (sointu.TapPoint{Instrument: 1, Unit: 1}) || got[1] != (sointu.TapPoint{Instrument: 1, Unit: 4}) {
		t.Errorf("the player taps %v", got)
	}
	// noise is as loud everywhere; after the eq, each band of frequencies is
	// lower or higher by what the curve of the editor says
	check := func(name string, before, after []float32) {
		t.Helper()
		lows, highs := tapMean(before, 100, 400), tapMean(before, 6000, 12000)
		if lows-highs > 3 || lows-highs < -3 || highs < -80 {
			t.Errorf("%s: the noise before the eq is at %.1f dB from 100 to 400 Hz and %.1f dB from 6 to 12 kHz", name, lows, highs)
		}
		c, _, _ := r.m.EQ().Compiled()
		for _, band := range [][2]float64{{100, 400}, {500, 1500}, {6000, 12000}} {
			want, n := 0.0, 0
			for f := band[0]; f <= band[1]; f += 10 {
				want += 20 * math.Log10(cmplx.Abs(c.Response(f)))
				n++
			}
			want /= float64(n)
			if got := tapMean(after, band[0], band[1]) - tapMean(before, band[0], band[1]); math.Abs(got-want) > 1.5 {
				t.Errorf("%s: from %.0f to %.0f Hz the signal after the eq is %.1f dB higher, the curve says %.1f dB", name, band[0], band[1], got, want)
			}
		}
	}
	check("low cut", before, after)
	// another band: the units of the eq are others, the place after it
	// moves, and the spectra go on
	r.m.EQ().Set(0, sointu.EQBand{Type: sointu.EQLowCut, Frequency: 2000, Q: 1})
	if windows := r.m.taps[tapKey{Unit: 2, After: true}].spectrum.windows; windows < 10 {
		t.Errorf("the watch after the eq has analyzed %d windows", windows)
	}
	before, after = r.run(60, true)
	if got := r.player.taps; len(got) != 2 || got[1] != (sointu.TapPoint{Instrument: 1, Unit: 2}) {
		t.Errorf("with one unit, the player taps %v", got)
	}
	check("low cut of 12 dB", before, after)
	// an eq that does nothing: the same before and after
	r.m.EQ().SetOn(0, false)
	before, after = r.run(60, true)
	if d := tapMean(before, 100, 400) - tapMean(after, 100, 400); d > 0.5 || d < -0.5 || len(r.player.taps) != 1 {
		t.Errorf("without units, the lows are %.1f dB lower after the eq, and the player taps %v", d, r.player.taps)
	}
	// the unit disabled, or another one selected: no spectra
	r.m.Unit().Disabled().SetValue(true)
	if before, after := r.spectra(); before != nil || after != nil {
		t.Error("a disabled eq has spectra")
	}
	r.m.Unit().Disabled().SetValue(false)
	// nobody asks for a second: the synth records nothing
	for _, w := range r.m.taps {
		w.asked = time.Now().Add(-2 * time.Second)
	}
	r.run(4, false)
	if len(r.m.taps) != 0 || len(r.player.taps) != 0 {
		t.Errorf("a second after the last time they were asked for, %d watches and %d taps are left", len(r.m.taps), len(r.player.taps))
	}
	select {
	case msg := <-r.broker.ToModel:
		if _, ok := msg.Data.(TapAudio); ok {
			t.Error("the player still sends tapped audio")
		}
	default:
	}
}

// TestEQSpectraInModule checks that an eq unit of a module shows the signal
// of the copy that is played for the module unit.
func TestEQSpectraInModule(t *testing.T) {
	r := newTapTest(t, sointu.EQBand{Type: sointu.EQLowCut24, Frequency: 2000, Q: 0.707})
	r.m.Unit().MakeModule().Do()
	if u := r.m.d.Song.Patch[1].Units[1]; u.Type != "module" {
		t.Fatalf("the eq is not in a module: %+v", r.m.d.Song.Patch[1].Units)
	}
	r.m.Unit().Unfold().SetValue(true)
	r.m.Unit().List().SetSelected(r.m.Unit().List().Selected() + 1)
	r.m.Unit().List().SetSelected2(r.m.Unit().List().Selected())
	if _, _, ok := r.m.Unit().InModule(); !ok || !r.m.EQ().Active() {
		t.Fatal("the cursor is not on the eq unit inside the module unit")
	}
	before, after := r.run(60, true)
	if len(before) == 0 || len(after) == 0 {
		t.Fatalf("no spectra of the eq in the module; the player taps %v", r.player.taps)
	}
	if d := tapMean(before, 100, 400) - tapMean(after, 100, 400); d < 40 {
		t.Errorf("after the eq in the module the lows are %.1f dB lower", d)
	}
}
