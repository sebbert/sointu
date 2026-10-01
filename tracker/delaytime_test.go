package tracker

import (
	"slices"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
)

func TestDelayNoteNames(t *testing.T) {
	want := map[int][2]string{
		1: {"1/128T", "1/128 triplet"}, 2: {"1/64T", "1/64 triplet"}, 3: {"1/64", "1/64"},
		4: {"1/32T", "1/32 triplet"}, 6: {"1/32", "1/32"}, 8: {"1/16T", "1/16 triplet"},
		9: {"1/32D", "1/32 dotted"}, 12: {"1/16", "1/16"}, 16: {"1/8T", "1/8 triplet"},
		18: {"1/16D", "1/16 dotted"}, 24: {"1/8", "1/8"}, 32: {"1/4T", "1/4 triplet"},
		36: {"1/8D", "1/8 dotted"}, 48: {"1/4", "1/4"}, 64: {"1/2T", "1/2 triplet"},
		72: {"1/4D", "1/4 dotted"}, 96: {"1/2", "1/2"}, 128: {"1/1T", "1/1 triplet"},
		144: {"1/2D", "1/2 dotted"}, 192: {"1/1", "1/1"}, 256: {"2/1T", "2/1 triplet"},
		288: {"1/1D", "1/1 dotted"}, 384: {"2/1", "2/1"}, 512: {"4/1T", "4/1 triplet"},
		576: {"2/1D", "2/1 dotted"},
	}
	var grid []int
	for v := 0; v <= delayBPMMax+1; v++ {
		short, ok := delayNoteName(v, false)
		long, _ := delayNoteName(v, true)
		w, isNote := want[v]
		if ok != isNote || short != w[0] || long != w[1] {
			t.Errorf("value %d: got %q, %q, %v, want %q, %q, %v", v, short, long, ok, w[0], w[1], isNote)
		}
		if ok {
			grid = append(grid, v)
		}
	}
	if g := delayGrids[delayBPM]; !slices.Equal(g.values, grid) || !slices.Equal(g.knots, grid) {
		t.Errorf("grid %v, knots %v, want the note lengths %v", g.values, g.knots, grid)
	}
	if got, want := delayGrids[delayBPM].coarse, []int{3, 6, 12, 24, 48, 96, 192, 384}; !slices.Equal(got, want) {
		t.Errorf("coarse grid %v, want the straight notes %v", got, want)
	}
}

func TestDelayGrids(t *testing.T) {
	for mode, g := range delayGrids {
		if !slices.IsSorted(g.values) || !slices.IsSorted(g.knots) || len(slices.Compact(slices.Clone(g.knots))) != len(g.knots) {
			t.Errorf("mode %d: the grid is not strictly ascending", mode)
		}
		for _, v := range g.coarse {
			if !g.contains(v) {
				t.Errorf("mode %d: coarse value %d is not on the grid", mode, v)
			}
		}
		lo, hi := 1, delaySamplesMax
		if mode == delayBPM {
			hi = delayBPMMax
		}
		if g.knots[0] != lo || g.knots[len(g.knots)-1] != hi || g.position(lo) != 0 || g.position(hi) != 1 {
			t.Errorf("mode %d: the scale does not span the range %d to %d", mode, lo, hi)
		}
		prev := float32(-1)
		for v := lo; v <= hi; v++ {
			pos := g.position(v)
			if pos <= prev {
				t.Fatalf("mode %d: position of %d is %v, not above %v", mode, v, pos, prev)
			}
			prev = pos
			// every value can be reached by dragging when free, and no drag is no change
			if got := g.dragged(lo, pos, true); got != v {
				t.Fatalf("mode %d: free drag to the position of %d gave %d", mode, v, got)
			}
			if got := g.dragged(v, 0, false); got != v {
				t.Fatalf("mode %d: a drag of 0 changed %d to %d", mode, v, got)
			}
			// a drag on the grid ends on the grid, in the direction of the drag
			for _, amount := range []float32{-0.3, 0.3} {
				got := g.dragged(v, amount, false)
				if !g.contains(got) || amount > 0 && got < v && v <= g.values[len(g.values)-1] || amount < 0 && got > v && v >= g.values[0] {
					t.Fatalf("mode %d: drag of %v from %d gave %d", mode, amount, v, got)
				}
			}
		}
	}
	// fixed: whole milliseconds, large steps of 10 ms; pitch: semitones and octaves
	if g := delayGrids[delayFixed]; g.values[0] != 44 || g.values[9] != 441 || g.coarse[0] != 441 || g.coarse[1] != 882 {
		t.Errorf("fixed grid starts %v, coarse %v", g.values[:10], g.coarse[:2])
	}
	if g := delayGrids[delayPitch]; !slices.Equal(g.values, delayNoteTrackGrid) || !slices.Equal(g.coarse, []int{2697, 5394, 10787, 21574, 43148}) {
		t.Errorf("pitch grid %v, coarse %v", g.values, g.coarse)
	}
}

func TestDelayDragDeadZone(t *testing.T) {
	g := &delayGrids[delayBPM] // 24 steps
	if got := g.dragged(50, 0.4/24, false); got != 50 {
		t.Errorf("a drag of 0.4 steps moved 50 to %d", got)
	}
	if got := g.dragged(50, 0.6/24, false); got != 64 {
		t.Errorf("a drag of 0.6 steps up moved 50 to %d, want 64", got)
	}
	if got := g.dragged(48, -1.0/24, false); got != 36 {
		t.Errorf("a drag of a step down moved 48 to %d, want 36", got)
	}
	if got := g.dragged(48, 100, false); got != 576 {
		t.Errorf("a drag past the end gave %d, want 576", got)
	}
}

func TestStepOnGrid(t *testing.T) {
	grid := []int{3, 6, 12, 24}
	for _, c := range []struct{ value, delta, want int }{
		{6, 1, 12}, {6, -1, 3}, {6, 2, 24}, {6, 5, 24}, {6, -5, 3}, {6, 0, 6},
		{7, 1, 12}, {7, -1, 6}, {7, 2, 24}, {7, -2, 3}, // off the grid: the first step reaches it
		{24, 1, 24}, {3, -1, 3}, // the ends
		{30, 1, 30}, {30, -1, 24}, {1, -1, 1}, {1, 1, 3}, // beyond the ends
	} {
		if got := stepOnGrid(grid, c.value, c.delta); got != c.want {
			t.Errorf("stepOnGrid(%d, %+d) = %d, want %d", c.value, c.delta, got, c.want)
		}
	}
}

// delayTestModel returns a model with a delay unit, and a function returning
// its parameters with the given vtable.
func delayTestModel(t *testing.T) (*Model, func() *sointu.Unit) {
	t.Helper()
	m := newSpectrumTestModel(t)
	addTestUnit(m, "delay")
	return m, func() *sointu.Unit { return &m.d.Song.Patch[0].Units[1] }
}

func delayTimes(m *Model) (times []Parameter, free Parameter) { return delayTimesOf(m, 1) }

func delayTimesOf(m *Model, unit int) (times []Parameter, free Parameter) {
	for x := 0; x < m.Params().RowWidth(unit); x++ {
		p := m.Params().Item(Point{x, unit})
		switch p.vtable.(type) {
		case *delayTimeParameter:
			times = append(times, p)
		case *delayFreeParameter:
			free = p
		}
	}
	return
}

func TestDelayTimeNoteLengths(t *testing.T) {
	m, unit := delayTestModel(t)
	check := func(line, value int, label, hint string) {
		t.Helper()
		times, _ := delayTimes(m)
		p := times[line]
		if p.Value() != value || p.Label() != label || !strings.HasPrefix(p.Hint().Label, hint) {
			t.Errorf("line %d: got %d %q %q, want %d %q %q...", line, p.Value(), p.Label(), p.Hint().Label, value, label, hint)
		}
		if unit().VarArgs[line] != value {
			t.Errorf("line %d: the unit has %d, want %d", line, unit().VarArgs[line], value)
		}
	}
	step := func(line, delta int, large bool) {
		times, _ := delayTimes(m)
		times[line].Add(delta, large)
	}
	if unit().Parameters["notetracking"] != delayBPM {
		t.Fatalf("a new delay does not follow the tempo")
	}
	if times, free := delayTimes(m); len(times) != 1 || free.Type() != BoolParameter || free.Name() != "free" || free.Value() != 0 {
		t.Fatalf("a new delay has %d delay times, free is %v %q %d", len(times), free.Type(), free.Name(), free.Value())
	}
	m.d.Song.BPM, m.d.Song.RowsPerBeat = 120, 4
	check(0, 48, "1/4", "1/4: 1 beat, 4 rows, 500 ms")
	step(0, -1, false) // a dotted 1/8 is one step from the default
	check(0, 36, "1/8D", "1/8 dotted: 0.75 beats, 3 rows, 375 ms")
	step(0, -1, false)
	check(0, 32, "1/4T", "1/4 triplet: 0.667 beats, 2.667 rows")
	step(0, -1, true) // large steps are the straight notes
	check(0, 24, "1/8", "1/8: 0.5 beats, 2 rows, 250 ms")
	step(0, 2, true)
	check(0, 96, "1/2", "1/2: 2 beats")
	step(0, 100, false)
	check(0, 576, "2/1D", "2/1 dotted: 12 beats, 48 rows, 1486 ms (the longest delay)")
	step(0, 1, false)
	check(0, 576, "2/1D", "2/1 dotted")

	// a value that is not a note length shows as it is, and a step puts it on the grid
	unit().VarArgs[0] = 50
	check(0, 50, "50", "50/48 beat: 1.042 beats, 4.167 rows, 520.8 ms")
	step(0, 1, false)
	check(0, 64, "1/2T", "1/2 triplet")
	unit().VarArgs[0] = 50
	step(0, -1, false)
	check(0, 48, "1/4", "1/4:")
	unit().VarArgs[0] = 50
	step(0, -1, true)
	check(0, 48, "1/4", "1/4:")

	// free: every value, shown as stored; large steps go to the note lengths as before
	song := m.d.Song.Copy()
	_, free := delayTimes(m)
	if !free.SetValue(1) || m.d.Song.Patch[0].Units[1].VarArgs[0] != 48 || len(m.d.Song.Patch[0].Units[1].Parameters) != len(song.Patch[0].Units[1].Parameters) {
		t.Fatalf("setting free failed or changed the unit: %v", m.d.Song.Patch[0].Units[1])
	}
	check(0, 48, "48", "1/4: 1 beat")
	step(0, 1, false)
	check(0, 49, "49", "49/48 beat")
	step(0, 1, false)
	check(0, 50, "50", "50/48 beat")
	step(0, 1, true)
	check(0, 64, "64", "1/2 triplet")
	step(0, -3, false)
	check(0, 61, "61", "61/48 beat")
	_, free = delayTimes(m)
	free.Reset()
	check(0, 61, "61", "61/48 beat") // leaving free does not move the value
}

func TestDelayTimePerLine(t *testing.T) {
	m, unit := delayTestModel(t)
	func() {
		defer m.change("Test", PatchChange, MajorChange)()
		unit().Parameters["stereo"] = 1
		unit().VarArgs = []int{48, 36, 50, 16}
	}()
	times, _ := delayTimes(m)
	if len(times) != 4 {
		t.Fatalf("got %d delay times, want 4", len(times))
	}
	var labels, sides []string
	for _, p := range times {
		labels = append(labels, p.Label())
		h := p.Hint().Label
		sides = append(sides, h[len(h)-1:])
	}
	if want := []string{"1/4", "1/8D", "50", "1/8T"}; !slices.Equal(labels, want) {
		t.Errorf("labels %v, want %v", labels, want)
	}
	if want := []string{"R", "R", "L", "L"}; !slices.Equal(sides, want) {
		t.Errorf("sides %v, want %v", sides, want)
	}
	times[3].Add(1, false)
	if got, want := unit().VarArgs, []int{48, 36, 50, 18}; !slices.Equal(got, want) {
		t.Errorf("after stepping the last line: %v, want %v", got, want)
	}
	// the free switch of one delay unit leaves the others alone
	addTestUnit(m, "delay")
	_, free := delayTimes(m)
	free.SetValue(1)
	if _, other := delayTimesOf(m, 2); len(m.delayFree) != 1 || !m.delayFree[unit().ID] || free.Value() != 1 || other.Name() != "free" || other.Value() != 0 {
		t.Errorf("free units %v, want only %d", m.delayFree, unit().ID)
	}
	// and is forgotten with its unit, so that a new unit with its ID starts on the grid
	m.d.UnitIndex, m.d.UnitIndex2 = 1, 1
	m.Unit().Delete().Do()
	if len(m.delayFree) != 0 || m.d.Song.Patch[0].Units[1].Type != "delay" {
		t.Errorf("free units %v after deleting the unit, want none", m.delayFree)
	}
}

func TestDelayTimeFixedAndPitch(t *testing.T) {
	m, unit := delayTestModel(t)
	set := func(tracking, value int) Parameter {
		func() {
			defer m.change("Test", PatchChange, MajorChange)()
			unit().Parameters["notetracking"] = tracking
			unit().VarArgs = []int{value}
		}()
		times, _ := delayTimes(m)
		return times[0]
	}
	check := func(p Parameter, value int, label, hint string) {
		t.Helper()
		if p.Value() != value || p.Label() != label || !strings.HasPrefix(p.Hint().Label, hint) {
			t.Errorf("got %d %q %q, want %d %q %q...", p.Value(), p.Label(), p.Hint().Label, value, label, hint)
		}
	}
	// fixed: whole milliseconds
	p := set(delayFixed, 1116) // a line of the reverb preset
	check(p, 1116, "25.3", "25.306 ms, 1116 samples, ")
	p.Add(1, false)
	check(p, 1147, "26", "26.009 ms, 1147 samples")
	p.Add(-1, false)
	check(p, 1103, "25", "25.011 ms")
	p.Add(1, true)
	check(p, 1323, "30", "30 ms, 1323 samples")
	p.Add(-1, true)
	check(p, 882, "20", "20 ms")
	p = set(delayFixed, 1)
	check(p, 1, "0.02", "0.023 ms, 1 sample, ")
	p.Add(-1, false)
	check(p, 1, "0.02", "0.023 ms")
	p.Add(1, false)
	check(p, 44, "1", "0.998 ms")
	// pitch: semitones relative to the note
	p = set(delayPitch, 10787)
	check(p, 10787, "0", "0 st (10787)")
	p.Add(1, false) // a longer delay is a lower pitch
	check(p, 11428, "-1", "-1 st (11428)")
	p.Add(-3, false)
	check(p, 9610, "+2", "+2 st")
	p.Add(-1, true)
	check(p, 5394, "+12", "+12 st")
	p = set(delayPitch, 10000)
	check(p, 10000, "+1.3", "+1.312 st (10000)")
	p.Add(-1, false)
	check(p, 9610, "+2", "+2 st")
	// free: samples, in steps of 1 or to multiples of 16, as before
	_, free := delayTimes(m)
	free.SetValue(1)
	p = set(delayFixed, 1116)
	check(p, 1116, "1116", "25.306 ms, 1116 samples")
	p.Add(1, false)
	check(p, 1117, "1117", "25.329 ms")
	p.Add(1, true)
	check(p, 1120, "1120", "25.397 ms")
}

// Other parameters keep their linear knobs and plain labels.
func TestParameterDefaultScale(t *testing.T) {
	m, _ := delayTestModel(t)
	var p Parameter
	for x := 0; x < m.Params().RowWidth(1); x++ {
		if q := m.Params().Item(Point{x, 1}); q.Name() == "feedback" {
			p = q
		}
	}
	if p.Label() != "96" || p.Position(96) != 0.75 || p.Position(0) != 0 || p.Position(128) != 1 {
		t.Errorf("feedback: label %q, positions %v %v %v", p.Label(), p.Position(96), p.Position(0), p.Position(128))
	}
	if got := p.Dragged(96, 0.125); got != 112 {
		t.Errorf("feedback dragged by 1/8 from 96: %d, want 112", got)
	}
	if !p.Add(1, false) || p.Value() != 97 || !p.Add(1, true) || p.Value() != 104 {
		t.Errorf("feedback after steps: %d, want 104", p.Value())
	}
}
