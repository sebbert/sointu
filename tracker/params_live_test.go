package tracker

import (
	"slices"
	"testing"

	"github.com/vsariola/sointu"
)

// TestParamLive checks the value that the knob of a parameter shows as
// played: only of a parameter that a send targets, once the player has sent
// the ports of the unit in the last voice, as the parameter plus what they
// add, within its range.
func TestParamLive(t *testing.T) {
	m, _ := newModuleTestModel(t)
	const row = 5 // the filter with ID 6
	port := slices.Index(sointu.Ports["filter"], "frequency")
	column := func(name string) Point {
		for x := 0; x < m.Params().RowWidth(row); x++ {
			if p := m.Params().Item(Point{x, row}); p.Name() == name {
				return Point{x, row}
			}
		}
		t.Fatalf("the filter has no %s", name)
		return Point{}
	}
	freq, res := column("frequency"), column("resonance")
	if _, ok := m.Params().Live(freq); ok {
		t.Error("a parameter that nothing targets has a live value")
	}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch[0].Units[1].Parameters["target"] = 6
		m.d.Song.Patch[0].Units[1].Parameters["port"] = port
	}()
	if _, ok := m.Params().Live(freq); ok {
		t.Error("a live value before the player sent any")
	}
	w := m.taps[tapKey{Unit: 6, Ports: true}]
	if want := (sointu.TapPoint{Instrument: 0, Unit: row, Voice: 1}); w == nil || w.point != want {
		t.Fatalf("the knob watches %+v, not the ports before the filter in the last voice", w)
	}
	send := func(ports [8]float32) {
		buf := m.broker.GetAudioBuffer()
		*buf = append((*buf)[:0], [2]float32{})
		m.tapped(TapAudio{Point: w.point, Buffer: buf, Ports: ports, HasPorts: true})
	}
	var ports [8]float32
	ports[port] = -0.25 // 32 steps down
	send(ports)
	if v, ok := m.Params().Live(freq); !ok || v != 40-32 {
		t.Errorf("the live frequency is %d (%v), want 8", v, ok)
	}
	if _, ok := m.Params().Live(res); ok {
		t.Error("the resonance, which nothing targets, has a live value")
	}
	ports[port] = -1
	send(ports)
	if v, _ := m.Params().Live(freq); v != 0 {
		t.Errorf("the live frequency is %d, not kept within its range", v)
	}
}
