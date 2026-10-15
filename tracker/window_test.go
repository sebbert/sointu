package tracker

import "testing"

// A window timed by pitch or tempo shows its time as its length, on the grid
// of periods or note lengths, with transpose and detune only for pitch.
func TestWindowTiming(t *testing.T) {
	m, r := newRemoteTestModel(t)
	first := m.d.Song.Patch[0].Units[0].ID
	remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "", 0, first, []RemoteNewUnit{{Type: "window", Params: map[string]any{"timing": "pitch"}}})
	})
	unit := &m.d.Song.Patch[0].Units[0]
	if unit.Type != "window" || unit.Parameters["time"] != 10788 || unit.Parameters["transpose"] != 64 {
		t.Fatalf("the new window: %v", unit.Parameters)
	}
	shown := func() map[string]Parameter {
		ret := map[string]Parameter{}
		for x := 0; x < m.Params().RowWidth(0); x++ {
			p := m.Params().Item(Point{x, 0})
			if p.up != nil {
				ret[p.up.Name] = p
			} else {
				ret[p.Name()] = p
			}
		}
		return ret
	}
	s := shown()
	for _, name := range []string{"timing", "time", "shape", "transpose", "detune", "free"} {
		if _, ok := s[name]; !ok {
			t.Errorf("a window timed by pitch has no %s", name)
		}
	}
	if _, ok := s["length"]; ok {
		t.Error("a window timed by pitch shows its length in frames")
	}
	p := s["time"]
	if p.Name() != "length" || p.Label() != "1p" || p.Hint().Label != "1 period of the note (10788)" {
		t.Errorf("the time: name %q, label %q, hint %q", p.Name(), p.Label(), p.Hint().Label)
	}
	step := func(delta int, large bool) int {
		p := shown()["time"]
		return p.vtable.(parameterStepper).Step(&p, delta, large)
	}
	if a, b, c := step(1, false), step(-1, false), step(1, true); a != 16182 || b != 8091 || c != 21576 {
		t.Errorf("pitch timing from 1p: steps to %d, %d and %d, not 1.5p, 3/4p and 2p", a, b, c)
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: unit.ID, Params: map[string]any{"timing": "BPM", "time": 12.0}}})
	})
	s = shown()
	if p := s["time"]; p.Label() != "1/16" {
		t.Errorf("BPM timing: label %q", p.Label())
	}
	if _, ok := s["transpose"]; ok {
		t.Error("a window timed by tempo shows transpose")
	}
	remoteStep(t, m, func() (string, error) {
		return r.EditUnits([]RemoteUnitEdit{{Unit: unit.ID, Params: map[string]any{"timing": "frames"}}})
	})
	s = shown()
	if _, ok := s["time"]; ok {
		t.Error("a window timed in frames shows its time")
	}
	if _, ok := s["length"]; !ok {
		t.Error("a window timed in frames has no length")
	}
}
