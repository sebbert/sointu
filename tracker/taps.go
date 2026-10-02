package tracker

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/vsariola/sointu"
)

// Taps: the signal at a unit, for what shows it. The synth can record the
// signal before any unit of the patch it plays (sointu.Tapper). The model
// tells the player where (TapsMsg), the player takes what the synth
// recorded after every buffer it renders and sends it to the model
// (TapAudio), and the model gives it to whatever watches that place: so far
// the spectra behind the curve of the eq editor (tapSpectrum).
//
// A watch is asked for by whoever shows it, every time it is drawn
// (Model.watchTap); a watch not asked for in a second ends, and with the
// last one, the synth records nothing. The key of a watch says what is
// watched, not where: the place changes when units are added or removed
// before it, and the watch then goes on with what it has.

type (
	// TapsMsg tells the player the places where the synth should record the
	// signal, in the patch that the player has.
	TapsMsg []sointu.TapPoint

	// TapAudio is what the synth recorded at a place since the last
	// TapAudio of that place. The buffer goes back to the broker.
	TapAudio struct {
		Point  sointu.TapPoint
		Buffer *sointu.AudioBuffer
	}

	// tapKey is what a watch watches: the signal before or after the unit
	// with the given ID, as it is played, for one purpose.
	tapKey struct {
		Unit  int
		After bool
	}

	tapWatch struct {
		point    sointu.TapPoint
		asked    time.Time
		spectrum *tapSpectrum
	}

	// tapSpectrum is the spectrum of the signal of a tap, smoothed over
	// time.
	tapSpectrum struct {
		analyzer specAnalyzer
		stereo   bool
		windows  int // analyzed so far
		levels   []float32
	}
)

// The spectra of the taps have 2048 bins, 10.8 Hz apart, and follow the
// signal with a quarter of each new window.
const (
	tapSpectrumResolution = 2
	tapSpectrumSmooth     = 1
)

// applyTaps tells the synth where to record the signal.
func (p *Player) applyTaps() {
	if t, ok := p.synth.(sointu.Tapper); ok {
		t.SetTaps(p.taps)
	}
}

// reportTaps sends what the synth recorded at the taps to the model.
func (p *Player) reportTaps() {
	if len(p.taps) == 0 {
		return
	}
	t, ok := p.synth.(sointu.Tapper)
	if !ok {
		return
	}
	for i, point := range p.taps {
		buf := p.broker.GetAudioBuffer()
		*buf = t.Tapped(i, (*buf)[:0])
		if len(*buf) == 0 || !TrySend(p.broker.ToModel, MsgToModel{Data: TapAudio{Point: point, Buffer: buf}}) {
			p.broker.PutAudioBuffer(buf)
		}
	}
}

// watchTap returns the watch of the signal at the place, which whoever
// shows it asks for every time: a new one if there is none, and with the
// player told to record there.
func (m *Model) watchTap(key tapKey, point sointu.TapPoint) *tapWatch {
	if m.taps == nil {
		m.taps = map[tapKey]*tapWatch{}
	}
	w := m.taps[key]
	if w == nil {
		w = &tapWatch{point: point}
		m.taps[key] = w
		m.sendTaps()
	} else if w.point != point {
		w.point = point
		m.sendTaps()
	}
	w.asked = time.Now()
	return w
}

// sendTaps tells the player the places of the watches.
func (m *Model) sendTaps() {
	points := make(TapsMsg, 0, len(m.taps))
	for _, w := range m.taps {
		if !slices.Contains(points, w.point) {
			points = append(points, w.point)
		}
	}
	slices.SortFunc(points, func(a, b sointu.TapPoint) int { // the same for the same watches
		return cmp.Or(cmp.Compare(a.Instrument, b.Instrument), cmp.Compare(a.Unit, b.Unit), cmp.Compare(a.Voice, b.Voice))
	})
	TrySend(m.broker.ToPlayer, any(points))
}

// tapped gives what the synth recorded at a place to the watches of that
// place, and ends the watches that were not asked for in a second.
func (m *Model) tapped(e TapAudio) {
	ended := false
	for key, w := range m.taps {
		if time.Since(w.asked) > time.Second {
			delete(m.taps, key)
			ended = true
			continue
		}
		if w.point == e.Point && w.spectrum != nil {
			w.spectrum.write(*e.Buffer)
		}
	}
	if ended {
		m.sendTaps()
	}
	m.broker.PutAudioBuffer(e.Buffer)
}

// spectrumOfTap returns the spectrum of the signal at the place, in dB for
// each bin: bin i is at (i+1)/len of half the sample rate. Of a stereo
// signal it is that of the louder channel. It is nil until the synth has
// recorded enough for it.
func (m *Model) spectrumOfTap(key tapKey, point sointu.TapPoint, stereo bool) []float32 {
	w := m.watchTap(key, point)
	if w.spectrum == nil {
		w.spectrum = &tapSpectrum{}
		w.spectrum.analyzer.init(specAnSettings{ChnMode: SpecChnModeSeparate, Smooth: tapSpectrumSmooth, Resolution: tapSpectrumResolution})
	}
	w.spectrum.stereo = stereo
	return w.spectrum.levels
}

func (t *tapSpectrum) write(buf sointu.AudioBuffer) {
	a := &t.analyzer
	l := len(a.temp.window)
	a.chunker.Process(buf, l, l>>1, func(chunk sointu.AudioBuffer) {
		a.process(chunk, 0)
		power := a.temp.power[0]
		if t.stereo {
			a.process(chunk, 1)
		}
		t.windows++
		t.levels = t.levels[:0]
		for i, p := range power {
			if t.stereo {
				p = max(p, a.temp.power[1][i])
			}
			t.levels = append(t.levels, float32(10*math.Log10(float64(p)+1e-30)))
		}
	})
}
