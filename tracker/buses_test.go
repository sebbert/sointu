package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func buses(m *Model) []sointu.Buffer {
	var ret []sointu.Buffer
	for _, b := range m.d.Song.Buffers {
		if b.Bus {
			ret = append(ret, b)
		}
	}
	return ret
}

func TestBusLifetime(t *testing.T) {
	m := newSpectrumTestModel(t)
	addTestUnit(m, "mcspread")
	bs := buses(m)
	if len(bs) != 1 || !bs[0].Auto || bs[0].Channels != sointu.MCChannels {
		t.Fatalf("after adding mcspread: buses %v, want one automatic", bs)
	}
	id := bs[0].ID
	if got := m.d.Song.Patch[0].Units[1].Parameters["bus"]; got != id {
		t.Fatalf("mcspread uses bus %d, want %d", got, id)
	}
	addTestUnit(m, "mcdelay")
	addTestUnit(m, "mcsum")
	for _, u := range m.d.Song.Patch[0].Units[2:] {
		if u.Parameters["bus"] != id {
			t.Errorf("%s uses bus %d, want the bus above it, %d", u.Type, u.Parameters["bus"], id)
		}
	}
	// a second mcspread gets a bus of its own
	addTestUnit(m, "mcspread")
	if len(buses(m)) != 2 {
		t.Errorf("after adding a second mcspread: buses %v, want two", buses(m))
	}
	m.d.UnitIndex, m.d.UnitIndex2 = 4, 4
	m.Unit().Delete().Do()
	// deleting the first mcspread keeps the bus, as mcdelay and mcsum use it
	m.d.UnitIndex, m.d.UnitIndex2 = 1, 1
	m.Unit().Delete().Do()
	if len(buses(m)) != 1 {
		t.Errorf("buses %v, want the one mcdelay and mcsum use", buses(m))
	}
	m.d.UnitIndex, m.d.UnitIndex2 = 1, 2
	m.Unit().Delete().Do()
	if len(buses(m)) != 0 {
		t.Errorf("the bus was kept after deleting its units: %v", m.d.Song.Buffers)
	}
	if len(m.d.Song.Buffers) != 1 {
		t.Errorf("the audio buffer was deleted")
	}
	m.History().Undo().Do()
	if len(buses(m)) != 1 {
		t.Errorf("undo did not bring the bus back")
	}
}

func TestBusRecreated(t *testing.T) {
	m := newSpectrumTestModel(t)
	func() {
		defer m.change("Paste", PatchChange, MajorChange)()
		m.d.Song.Patch[0].Units = append(m.d.Song.Patch[0].Units,
			sointu.Unit{ID: 2, Type: "mcspread", Parameters: sointu.ParamMap{"gain": 64, "bus": 7}},
			sointu.Unit{ID: 3, Type: "mcspread", Parameters: sointu.ParamMap{"gain": 64, "bus": 1}}, // an audio buffer here
			sointu.Unit{ID: 4, Type: "mcsum", Parameters: sointu.ParamMap{"gain": 64, "bus": 1}},
		)
	}()
	if b, ok := m.d.Song.Buffers.Find(7); !ok || !b.Bus {
		t.Errorf("missing bus 7 was not recreated: %v", m.d.Song.Buffers)
	}
	units := m.d.Song.Patch[0].Units
	id := units[2].Parameters["bus"]
	if b, ok := m.d.Song.Buffers.Find(id); id == 1 || !ok || !b.Bus {
		t.Errorf("mcspread on an audio buffer got bus %d: %v", id, m.d.Song.Buffers)
	}
	if units[3].Parameters["bus"] != id {
		t.Errorf("mcsum uses bus %d, want %d", units[3].Parameters["bus"], id)
	}
	if len(spectrumBuffers(m)) != 0 {
		t.Errorf("buses were taken for spectra")
	}
}

func TestBusParameters(t *testing.T) {
	m := newSpectrumTestModel(t)
	addTestUnit(m, "mcspread")
	addTestUnit(m, "mcdelay")
	for i, want := range []int{1, 0} {
		for _, p := range m.deriveParams(&m.d.Song.Patch[0].Units[1+i], nil) {
			if p.Name() == "bus" {
				if r := p.Range(); r.Min != want || r.Max != 1 {
					t.Errorf("unit %d: bus range %v, want from %d to 1", 1+i, r, want)
				}
				if p.Hint().Label != "Bus 2" {
					t.Errorf("unit %d: bus shown as %q", 1+i, p.Hint().Label)
				}
			}
		}
	}
	// audio buffer parameters do not offer buses
	addTestUnit(m, "bufread")
	for _, p := range m.deriveParams(&m.d.Song.Patch[0].Units[3], nil) {
		if p.Name() == "buffer" && p.Range().Max != 1 {
			t.Errorf("bufread offers %d buffers, want only the audio one", p.Range().Max)
		}
	}
}

func TestPlayerReportsBusLevels(t *testing.T) {
	broker := NewBroker()
	p := NewPlayer(broker, vm.GoSynther{})
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{make(sointu.Pattern, 16)}}}},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{ID: 1, Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}},
			{ID: 2, Type: "mcspread", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64, "bus": 5}},
		}}}}
	broker.ToPlayer <- any(song)
	broker.ToPlayer <- any(SpectrumWatchMsg{{Unit: 2, Levels: true}})
	out := make(sointu.AudioBuffer, 512)
	for range 10 {
		p.Process(out, NullPlayerProcessContext{})
	}
	for {
		select {
		case msg := <-broker.ToModel:
			if s, ok := msg.Data.(SpectrumMsg); ok {
				if !s.Source.Levels || len(s.Magnitudes) != sointu.MCChannels || s.Magnitudes[0] != 0.5 || s.Magnitudes[7] != 0.5 {
					t.Errorf("got levels %v from %v", s.Magnitudes, s.Source)
				}
				return
			}
		default:
			t.Fatal("no levels reported")
		}
	}
}

func TestLoadedInstrumentGetsOwnBus(t *testing.T) {
	m := newSpectrumTestModel(t)
	addTestUnit(m, "mcspread") // bus 2
	units := []sointu.Unit{
		{Type: "mcspread", Parameters: sointu.ParamMap{"gain": 64, "bus": 2}},
		{Type: "mcsum", Parameters: sointu.ParamMap{"gain": 64, "bus": 2}},
		{Type: "mcsum", Parameters: sointu.ParamMap{"gain": 64, "bus": 9}},
	}
	m.assignBuses(units)
	if units[0].Parameters["bus"] != 3 || units[1].Parameters["bus"] != 3 {
		t.Errorf("the loaded units use buses %d and %d, want 3", units[0].Parameters["bus"], units[1].Parameters["bus"])
	}
	if units[2].Parameters["bus"] != 9 {
		t.Errorf("a free bus ID was changed to %d", units[2].Parameters["bus"])
	}
}
