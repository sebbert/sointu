package tracker

import (
	"strings"
	"testing"

	"github.com/vsariola/sointu"
)

// A bufwrite unit without a buffer gets one of its own, as long as its
// length parameter and stereo if it is; a bufread unit without one plays
// that of the nearest bufwrite unit; the buffer goes with the units.
func TestWriteBuffers(t *testing.T) {
	m, r := newRemoteTestModel(t)
	units := func() []sointu.Unit { return m.d.Song.Patch[0].Units }
	first := units()[0].ID
	// a tap before the writer, as in a delay with feedback, and one after
	text := remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "", 0, first, []RemoteNewUnit{
			{Type: "bufread", Params: map[string]any{"mode": "tap", "start": -22050.0}},
			{Type: "pop"},
			{Type: "loadval"},
			{Type: "bufwrite"},
			{Type: "bufread", Params: map[string]any{"start": 100.0}},
			{Type: "pop"},
		})
	})
	if len(m.d.Song.Buffers) != 1 {
		t.Fatalf("the song has %d buffers, not the one of the bufwrite unit:\n%s", len(m.d.Song.Buffers), text)
	}
	buf := m.d.Song.Buffers[0]
	if !buf.Auto || !buf.Writable() || buf.Frames != sointu.BufwriteDefaultLength || buf.Channels != 1 {
		t.Errorf("the buffer of the bufwrite unit: %+v", buf)
	}
	write := findType(t, units(), "bufwrite")
	if write.Parameters["buffer"] != buf.ID {
		t.Errorf("the bufwrite unit writes buffer %d, not %d", write.Parameters["buffer"], buf.ID)
	}
	for _, u := range units() {
		if u.Type == "bufread" && u.Parameters["buffer"] != buf.ID {
			t.Errorf("a bufread unit plays buffer %d, not %d", u.Parameters["buffer"], buf.ID)
		}
	}
	if u := units()[0]; u.Parameters["start"] != -22050 || u.Parameters["mode"] != 1 {
		t.Errorf("the tap: %v", u.Parameters)
	}
	// the buffer follows the unit
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: write.ID, Params: map[string]any{"length": 88200.0, "stereo": 1.0}}})
	})
	if buf := m.d.Song.Buffers[0]; buf.Frames != 88200 || buf.Channels != 2 {
		t.Errorf("the buffer after its unit got 2 s and stereo: %+v", buf)
	}
	// a second writer gets its own buffer, and the reader after it plays that
	remoteStep(t, m, func() (string, error) {
		return r.AddUnits("0", "", 0, 0, []RemoteNewUnit{{Type: "loadval"}, {Type: "bufwrite", Params: map[string]any{"length": 4410.0}}, {Type: "bufread"}, {Type: "pop"}})
	})
	if len(m.d.Song.Buffers) != 2 || m.d.Song.Buffers[1].Frames != 4410 {
		t.Fatalf("the buffers after a second bufwrite unit: %+v", m.d.Song.Buffers)
	}
	last := units()[len(units())-2]
	if last.Type != "bufread" || last.Parameters["buffer"] != m.d.Song.Buffers[1].ID {
		t.Errorf("the bufread unit after the second writer plays buffer %d", last.Parameters["buffer"])
	}
	// the timing of the start is shown as what it counts
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"timing": "BPM", "start": -36.0}}})
	})
	text, err := r.Instrument("0", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "start=-36(1/8 dotted: 0.75 beats") || !strings.Contains(text, "behind the write head") {
		t.Errorf("the start of a tap with BPM timing is not shown in beats:\n%s", text)
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"timing": "pitch", "start": -21576.0}}})
	})
	if text, _ = r.Instrument("0", false); !strings.Contains(text, "start=-21576(2 periods of the note behind the write head") {
		t.Errorf("the start of a tap with pitch timing is not shown in periods:\n%s", text)
	}
	// and the knob names it so, and steps by half periods
	start := func() Parameter {
		for x := 0; x < m.Params().RowWidth(0); x++ {
			if p := m.Params().Item(Point{x, 0}); p.up != nil && p.up.Name == "start" {
				return p
			}
		}
		t.Fatal("the bufread unit has no start")
		return Parameter{}
	}
	if p := start(); p.Label() != "2p" || p.vtable.RoundToGrid(&p, -21576, true) != -16182 || p.vtable.RoundToGrid(&p, -21576, false) != -26970 || p.vtable.RoundToGrid(&p, 100, true) != 5394 || p.vtable.RoundToGrid(&p, 5394, false) != 0 {
		t.Errorf("pitch timing: label %q, steps %d %d %d %d", p.Label(), p.vtable.RoundToGrid(&p, -21576, true), p.vtable.RoundToGrid(&p, -21576, false), p.vtable.RoundToGrid(&p, 100, true), p.vtable.RoundToGrid(&p, 5394, false))
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"timing": "BPM", "start": -36.0}}})
	})
	if p := start(); p.Label() != "1/8D" || p.Name() != "delay" {
		t.Errorf("BPM timing: label %q, name %q", p.Label(), p.Name())
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"start": -50.0}}})
	})
	if p := start(); p.Label() != "1.04b" {
		t.Errorf("BPM timing off the note lengths: label %q", p.Label())
	}
	// the knob moves over the grid: note lengths, or parts and multiples of
	// a period, unless the unit is free
	step := func(delta int, large bool) int {
		p := start()
		return p.vtable.(parameterStepper).Step(&p, delta, large)
	}
	if a, b, c := step(1, false), step(-1, false), step(1, true); a != -64 || b != -48 || c != -96 {
		t.Errorf("BPM timing from -50: steps to %d, %d and %d, not -64, -48 and -96", a, b, c)
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"timing": "pitch", "start": -21576.0}}})
	})
	if a, b, c, d := step(1, false), step(-1, false), step(1, true), step(-1, true); a != -32364 || b != -16182 || c != -32364 || d != -10788 {
		t.Errorf("pitch timing from -2p: steps to %d, %d, %d and %d", a, b, c, d)
	}
	p := start()
	g, _ := bufreadGrid(&p)
	n := float32(len(g.knots) - 1)
	if v := p.Dragged(-21576, 0.4/n); v != -21576 {
		t.Errorf("a drag of less than half a step moves the start to %d", v)
	}
	if v := p.Dragged(-21576, -6/n); v != -3596 {
		t.Errorf("a drag of six steps from -2p moves the start to %d, not -1/3p", v)
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: units()[0].ID, Params: map[string]any{"start": -3596.0}}})
	})
	if p := start(); p.Label() != "1/3p" {
		t.Errorf("a third of a period is labelled %q", p.Label())
	}
	m.setGridFree(&units()[0], true)
	if a := step(-1, false); a != -3595 {
		t.Errorf("a free start steps to %d, not -3595", a)
	}
	m.setGridFree(&units()[0], false)
	// the mode is a choice by name, and a tap timed by pitch keeps transpose
	// and detune, which tune it
	shown := map[string]Parameter{}
	for x := 0; x < m.Params().RowWidth(0); x++ {
		if p := m.Params().Item(Point{x, 0}); p.up != nil {
			shown[p.up.Name] = p
		}
	}
	if p := shown["mode"]; p.Type() != ChoiceParameter || (parameterInt{p}).StringOf(1) != "tap" {
		t.Errorf("mode: type %v, value 1 named %q", p.Type(), (parameterInt{p}).StringOf(1))
	}
	if _, ok := shown["transpose"]; !ok {
		t.Error("a tap timed by pitch has no transpose")
	}
	if _, ok := shown["speed"]; ok {
		t.Error("a tap shows its speed")
	}
	// without its units, the buffer goes
	var ids []int
	for _, u := range units() {
		if u.Type == "bufread" || u.Type == "bufwrite" {
			ids = append(ids, u.ID)
		}
	}
	if _, err := r.DeleteUnits(ids); err != nil {
		t.Fatal(err)
	}
	if len(m.d.Song.Buffers) != 0 {
		t.Errorf("the buffers stayed without their units: %+v", m.d.Song.Buffers)
	}
	m.History().Undo().Do()
	if len(m.d.Song.Buffers) != 2 {
		t.Errorf("undoing brought back %d buffers, not 2", len(m.d.Song.Buffers))
	}
}

// A buffer that the user made keeps its length and its channels, whatever
// the bufwrite unit that writes it says.
func TestWriteBuffersOfTheUser(t *testing.T) {
	m, r := newRemoteTestModel(t)
	remoteStep(t, m, func() (string, error) { return r.AddBuffer("Ring", 3, 1) })
	remoteStep(t, m, func() (string, error) {
		return r.AddUnits("0", "", 0, 0, []RemoteNewUnit{{Type: "loadval"}, {Type: "bufwrite", Params: map[string]any{"buffer": 1.0, "length": 4410.0, "stereo": 0.0}}})
	})
	if len(m.d.Song.Buffers) != 1 || m.d.Song.Buffers[0].Frames != 3*44100 || m.d.Song.Buffers[0].Auto {
		t.Errorf("the buffer of the user: %+v", m.d.Song.Buffers)
	}
	text, _ := r.Instrument("0", false)
	if !strings.Contains(text, "not used: the buffer has its own length") {
		t.Errorf("the length of a bufwrite unit with a buffer of the user is not marked:\n%s", text)
	}
}
