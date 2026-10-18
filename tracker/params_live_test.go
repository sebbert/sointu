package tracker

import (
	"slices"
	"testing"

	"github.com/vsariola/sointu"
)

// TestParamLive checks the values that the knob of a parameter shows as
// played: only of a parameter that a send targets, once the player has sent
// the ports of the unit in each voice, as the parameter plus what they add
// in each voice, within its range, and which is the voice of the last note.
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
	if _, last := m.Params().Live(freq, nil); last >= 0 {
		t.Error("a parameter that nothing targets has live values")
	}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch[0].NumVoices = 2
		m.d.Song.Patch[0].Units[1].Parameters["target"] = 6
		m.d.Song.Patch[0].Units[1].Parameters["port"] = port
	}()
	if _, last := m.Params().Live(freq, nil); last >= 0 {
		t.Error("live values before the player sent any")
	}
	w := m.taps[tapKey{Unit: 6, Ports: true}]
	if want := (sointu.TapPoint{Instrument: 0, Unit: row}); w == nil || w.point != want {
		t.Fatalf("the knob watches %+v, not the ports before the filter in each voice", w)
	}
	send := func(voices ...float32) {
		buf := m.broker.GetAudioBuffer()
		*buf = append((*buf)[:0], [2]float32{})
		ports := make([][8]float32, len(voices))
		for i, v := range voices {
			ports[i][port] = v
		}
		m.tapped(TapAudio{Point: w.point, Buffer: buf, VoicePorts: ports})
	}
	send(-0.25, 0.25) // 32 steps down, up
	m.triggered(0, 2)
	if v, last := m.Params().Live(freq, nil); !slices.Equal(v, []int{40 - 32, 40 + 32}) || last != 1 {
		t.Errorf("the live frequencies are %v, of the last note %d, want [8 72], 1", v, last)
	}
	if _, last := m.Params().Live(res, nil); last >= 0 {
		t.Error("the resonance, which nothing targets, has live values")
	}
	send(-1, 0)
	if v, _ := m.Params().Live(freq, nil); len(v) != 2 || v[0] != 0 {
		t.Errorf("the live frequencies are %v, not kept within the range", v)
	}
}
