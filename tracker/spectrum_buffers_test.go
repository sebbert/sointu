package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func newSpectrumTestModel(t *testing.T) *Model {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{{Name: "fx", NumVoices: 1, Units: []sointu.Unit{{ID: 1, Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}}}}
		m.d.Song.Buffers = sointu.Buffers{{ID: 1, Name: "sample", Channels: 1, Frames: 100}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 1}}
	}()
	drainPlayer(broker)
	return m
}

// addTestUnit adds a unit of the given type after the selected one.
func addTestUnit(m *Model, unitType string) {
	m.Unit().Add(false).Do()
	m.Unit().SetType(unitType)
}

func spectrumBuffers(m *Model) []sointu.Buffer {
	var ret []sointu.Buffer
	for _, b := range m.d.Song.Buffers {
		if b.Spectrum {
			ret = append(ret, b)
		}
	}
	return ret
}

func TestSpectrumBufferLifetime(t *testing.T) {
	m := newSpectrumTestModel(t)
	addTestUnit(m, "spfft")
	bufs := spectrumBuffers(m)
	if len(bufs) != 1 || !bufs[0].Auto {
		t.Fatalf("after adding spfft: spectrum buffers %v, want one automatic", bufs)
	}
	id := bufs[0].ID
	units := m.d.Song.Patch[0].Units
	if units[1].Parameters["buffer"] != id {
		t.Fatalf("spfft writes buffer %d, want %d", units[1].Parameters["buffer"], id)
	}
	addTestUnit(m, "spifft")
	if got := m.d.Song.Patch[0].Units[2].Parameters["buffer"]; got != id {
		t.Errorf("spifft reads buffer %d, want the spectrum above it, %d", got, id)
	}
	if size, ok := m.spectrumSize(id); !ok || size != 1024 {
		t.Errorf("spectrum size %d, %v, want 1024", size, ok)
	}
	// deleting the spfft keeps the buffer, as spifft uses it
	m.d.UnitIndex, m.d.UnitIndex2 = 1, 1
	m.Unit().Delete().Do()
	if len(spectrumBuffers(m)) != 1 {
		t.Errorf("the buffer was deleted while spifft uses it")
	}
	m.d.UnitIndex, m.d.UnitIndex2 = 1, 1
	m.Unit().Delete().Do()
	if len(spectrumBuffers(m)) != 0 {
		t.Errorf("the buffer was kept after deleting its units: %v", m.d.Song.Buffers)
	}
	if len(m.d.Song.Buffers) != 1 {
		t.Errorf("the audio buffer was deleted")
	}
	m.History().Undo().Do()
	if len(spectrumBuffers(m)) != 1 {
		t.Errorf("undo did not bring the buffer back")
	}
}

func TestSpectrumBufferRecreated(t *testing.T) {
	m := newSpectrumTestModel(t)
	func() {
		defer m.change("Paste", PatchChange, MajorChange)()
		m.d.Song.Patch[0].Units = append(m.d.Song.Patch[0].Units,
			sointu.Unit{ID: 2, Type: "spfft", Parameters: sointu.ParamMap{"size": 2, "buffer": 7}},
			sointu.Unit{ID: 3, Type: "spfft", Parameters: sointu.ParamMap{"size": 2, "buffer": 1}}, // an audio buffer here
			sointu.Unit{ID: 4, Type: "spifft", Parameters: sointu.ParamMap{"gain": 128, "buffer": 1}},
		)
	}()
	if b, ok := m.d.Song.Buffers.Find(7); !ok || !b.Spectrum {
		t.Errorf("missing buffer 7 was not recreated: %v", m.d.Song.Buffers)
	}
	units := m.d.Song.Patch[0].Units
	id := units[2].Parameters["buffer"]
	if b, ok := m.d.Song.Buffers.Find(id); id == 1 || !ok || !b.Spectrum {
		t.Errorf("spfft on an audio buffer got buffer %d: %v", id, m.d.Song.Buffers)
	}
	if units[3].Parameters["buffer"] != id {
		t.Errorf("spifft reads buffer %d, want %d", units[3].Parameters["buffer"], id)
	}
}

func TestSpectrumWriterCannotBeNone(t *testing.T) {
	m := newSpectrumTestModel(t)
	addTestUnit(m, "spfft")
	for _, p := range m.deriveParams(&m.d.Song.Patch[0].Units[1], nil) {
		if p.Name() == "buffer" {
			if r := p.Range(); r.Min != 1 {
				t.Errorf("spfft buffer range %v, want from 1", r)
			}
			if p.Hint().Label != "Spectrum 2" {
				t.Errorf("spfft buffer shown as %q", p.Hint().Label)
			}
		}
	}
}

func TestPlayerReportsSpectrum(t *testing.T) {
	broker := NewBroker()
	p := NewPlayer(broker, vm.GoSynther{})
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{make(sointu.Pattern, 16)}}}},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}},
			{Type: "spfft", Parameters: sointu.ParamMap{"size": 0, "buffer": 5}},
		}}}}
	broker.ToPlayer <- any(song)
	broker.ToPlayer <- any(SpectrumWatchMsg(5))
	out := make(sointu.AudioBuffer, 512)
	for range 10 {
		p.Process(out, NullPlayerProcessContext{})
	}
	for {
		select {
		case msg := <-broker.ToModel:
			if s, ok := msg.Data.(SpectrumMsg); ok {
				// a constant 0.5: the DC bin has 0.5 times the sum of the window
				if s.ID != 5 || s.Size != 256 || len(s.Magnitudes) != 129 || s.Magnitudes[0] < 60 || s.Magnitudes[0] > 68 {
					t.Errorf("got spectrum %d of size %d, %d bins, DC %v", s.ID, s.Size, len(s.Magnitudes), s.Magnitudes[0])
				}
				return
			}
		default:
			t.Fatal("no spectrum reported")
		}
	}
}
