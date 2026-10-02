package gioui

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// TestEQEditorInTracker lays out the tracker with an eq unit selected,
// without a window: folded, with its small plot in its row, and unfolded,
// with its editor under it. With SOINTU_TEST_SCREENSHOTS set to a
// directory, it also draws each step into a PNG there.
func TestEQEditorInTracker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	size := image.Pt(1500, 1000)
	var ops op.Ops
	var router input.Router
	dir := os.Getenv("SOINTU_TEST_SCREENSHOTS")
	ie := &tr.PatchPanel.instrEditor
	var focused bool // the plot of the eq unit has the focus
	frame := func(name string, before ...func(gtx C)) {
		t.Helper()
		for i := range 2 { // the second frame sees what the first one changed
			ops.Reset()
			gtx := layout.Context{Ops: &ops, Now: time.Now(), Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Exact(size), Values: map[string]any{"Tracker": tr}}
			if i == 0 {
				for _, f := range before {
					f(gtx)
				}
			}
			tr.Layout(gtx)
			row := model.EQ().Row()
			focused = row < len(ie.eqEditors) && gtx.Focused(ie.eqEditors[row])
			router.Frame(gtx.Ops)
		}
		if dir == "" || name == "" {
			return
		}
		window, err := headless.NewWindow(size.X, size.Y)
		if err != nil {
			t.Skipf("no headless window: %v", err)
		}
		defer window.Release()
		if err := window.Frame(&ops); err != nil {
			t.Fatalf("%v: %v", name, err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatalf("%v: %v", name, err)
		}
		f, err := os.Create(dir + "/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, img)
	}
	model.Unit().List().SetSelected(model.Unit().List().Count() - 2)
	model.Unit().Add(false).Do()
	model.Unit().SetType("eq")
	if !model.EQ().Active() {
		t.Fatal("the selected unit is not an eq unit")
	}
	frame("eq-1-empty")
	eq := model.EQ()
	eq.Add(sointu.EQBand{Type: sointu.EQLowCut, Frequency: 40})
	eq.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 250, Gain: -6, Q: 2})
	eq.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 3000, Gain: 4, Q: 1})
	eq.Add(sointu.EQBand{Type: sointu.EQHighShelf, Frequency: 2000, Gain: 6})
	eq.Add(sointu.EQBand{Type: sointu.EQLadder, Frequency: 14000})
	eq.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 800, Gain: 3, Disabled: true})
	eq.SetSelected(1)
	frame("eq-2-folded")
	if n := eq.NumBands(); n != 6 {
		t.Fatalf("%d bands", n)
	}
	row := eq.Row()
	if row >= len(ie.eqEditors) || !ie.eqEditors[row].small {
		t.Fatal("the folded eq unit has no small plot in its row")
	}
	// Tab reaches the small plot, and the keys then change the selected band
	gain := func() float64 { b, _ := eq.Band(eq.Selected()); return b.Gain }
	for range 60 {
		if frame("", func(gtx C) { tr.FocusNext(gtx, true) }); focused {
			break
		}
	}
	if !focused {
		t.Fatal("Tab does not reach the small plot")
	}
	was := gain()
	router.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	frame("")
	if got := gain(); got != was+tracker.EQGainStep {
		t.Errorf("Up with the focus on the small plot: the gain is %v, was %v", got, was)
	}
	// unfolded, the editor is under the row, with the spectra of the signal
	// before and after the eq behind the curves: the editor has asked the
	// player to tap the signal there, and gets what the synth recorded
	model.Unit().Unfold().SetValue(true)
	frame("")
	var taps tracker.TapsMsg
	for more := true; more; {
		select {
		case msg := <-broker.ToPlayer:
			if m, ok := msg.(tracker.TapsMsg); ok {
				taps = m
			}
		default:
			more = false
		}
	}
	if len(taps) != 2 || taps[0].Unit >= taps[1].Unit {
		t.Fatalf("the editor asked for the taps %v", taps)
	}
	seed := uint32(1)
	for i, point := range taps {
		buf := broker.GetAudioBuffer()
		low := float32(0)
		for range 4 * 4096 {
			seed = seed*1664525 + 1013904223
			x := float32(int32(seed)) / (1 << 31) * 0.1
			low += (x - low) * 0.1
			if i == 1 { // after the eq: fewer highs
				x = low
			}
			*buf = append(*buf, [2]float32{x, x})
		}
		model.ProcessMsg(tracker.MsgToModel{Data: tracker.TapAudio{Point: point, Buffer: buf}})
	}
	if before, after := eq.Spectra(); len(before) != 2048 || len(after) != 2048 {
		t.Fatalf("the spectra have %d and %d bins", len(before), len(after))
	}
	frame("eq-3-unfolded-spectra")
	ed := ie.eqEditors[row]
	if g := ed.geometry; ed.small || g.w < 600 || g.h < 150 {
		t.Fatalf("unfolded, the plot is %v by %v pixels, small: %v", g.w, g.h, ed.small)
	}
	for range 60 {
		if frame("", func(gtx C) { tr.FocusNext(gtx, true) }); focused {
			break
		}
	}
	if !focused {
		t.Fatal("Tab does not reach the plot of the unfolded eq unit")
	}
	was = gain()
	router.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	frame("eq-4-unfolded-focused")
	if got := gain(); got != was-tracker.EQGainStep {
		t.Errorf("Down with the focus on the plot: the gain is %v, was %v", got, was)
	}
}

// newEQRack returns a rackTest of an instrument with an oscillator, an eq
// unit folded, an eq unit unfolded, an unfolded module unit of a module of
// two oscillators, an addp and an out: on rows 0, 1, 2, 3 (its inner units
// on 4 to 6), 7 and 8.
func newEQRack(t *testing.T) *rackTest {
	t.Helper()
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	osc := sointu.MakeUnit("oscillator")
	osc.Parameters["stereo"] = 0
	folded, unfolded := sointu.MakeUnit("eq"), sointu.MakeUnit("eq")
	folded.Bands = []sointu.EQBand{{Type: sointu.EQLowCut, Frequency: 60}, {Type: sointu.EQBell, Frequency: 400, Gain: -4, Q: 1.5}, {Type: sointu.EQHighShelf, Frequency: 5000, Gain: 5}}
	unfolded.Bands = []sointu.EQBand{{Type: sointu.EQBell, Frequency: 150, Gain: 6}, {Type: sointu.EQNotch, Frequency: 2000, Q: 4}, {Type: sointu.EQHighCut24, Frequency: 3000}}
	unfolded.Unfolded = true
	mod := sointu.MakeUnit("module")
	mod.Parameters["module"] = 1
	mod.Unfolded = true
	out := sointu.MakeUnit("out")
	out.Parameters["stereo"] = 0
	units := []sointu.Unit{osc, folded, unfolded, mod, sointu.MakeUnit("addp"), out}
	for i := range units {
		units[i].ID = i + 1
	}
	inner := []sointu.Unit{osc.Copy(), osc.Copy(), sointu.MakeUnit("addp")}
	for i := range inner {
		inner[i].ID = 100 + i
	}
	song := sointu.Song{BPM: 100, RowsPerBeat: 4,
		Score:   sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1}}},
		Patch:   sointu.Patch{{Name: "test", NumVoices: 1, Units: units}},
		Modules: sointu.Modules{{ID: 1, Name: "saws", Units: inner}}}
	b, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	model.Song().Read(io.NopCloser(bytes.NewReader(b)))
	if got := model.Unit().List().Count(); got != 9 {
		t.Fatalf("the rack has %v rows, not 9", got)
	}
	r := &rackTest{t: t, model: model, tr: NewTracker(model), size: image.Pt(1200, 900), now: time.Now()}
	r.settle()
	return r
}

// TestEQInRack checks the eq units in the rack: folded, the small plot in
// the row, which can be dragged; unfolded, the editor under the row, which
// moves the rows after it down; the chevron folding it.
func TestEQInRack(t *testing.T) {
	r := newEQRack(t)
	// pointer events of the mouse, 50 ms apart, from a second on: a press
	// at 0 would be a double click with the time of no press
	clock := time.Second
	ptr := func(kind pointer.Kind, buttons pointer.Buttons, pos f32.Point) {
		clock += 50 * time.Millisecond
		r.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: pos, Time: clock})
		r.frame()
	}
	r.screenshot("eq-rack-1")
	ie := &r.tr.PatchPanel.instrEditor
	gtx := r.gtx()
	th := r.tr.Theme
	cellW, cellH := gtx.Dp(th.UnitEditor.Width), gtx.Dp(th.UnitEditor.Height)
	left := gtx.Dp(th.UnitEditor.UnitList.LabelWidth) + r.railWidth()
	if len(ie.eqEditors) < 3 || !ie.eqEditors[1].small || ie.eqEditors[2].small || len(ie.eqShown) != 2 {
		t.Fatalf("%d eq editors, shown on rows %v", len(ie.eqEditors), ie.eqShown)
	}
	small, big := ie.eqEditors[1], ie.eqEditors[2]
	if g := small.geometry; g.w != float32(tracker.EQInlineCells*cellW-8) || g.h != float32(cellH-8) {
		t.Errorf("the small plot is %v by %v pixels", g.w, g.h)
	}
	if g := big.geometry; g.w < 900 || g.h < 200 {
		t.Errorf("the plot of the unfolded eq unit is %v by %v pixels", g.w, g.h)
	}
	// the rows after the unfolded eq unit are lower by its editor
	for row, top := range []int{0, cellH, 2 * cellH, 3*cellH + gtx.Dp(eqEditorHeight)} {
		if got := ie.rowTop(gtx, row); got != top {
			t.Errorf("row %d is at %d, not %d", row, got, top)
		}
	}
	if p, ok := r.model.Params().Item(tracker.Point{X: 0, Y: 3}), r.model.Unit().Item(3).Module; p.Name() != "p1" && !ok {
		t.Errorf("row 3 is not the module unit: %+v", r.model.Unit().Item(3))
	}
	// the small plot: dragging a handle selects the row and moves the band,
	// in one step of the undo history; going down past the row selects no
	// other rows
	r.model.Unit().List().SetSelected(0)
	r.frame()
	origin := f32.Pt(float32(left+r.model.Params().RowWidth(1)*cellW+4), float32(cellH+4))
	c, _, _ := r.model.EQAt(1).Compiled()
	start := small.geometry.Handle(c.Bands[1].Actual).Add(origin)
	before, _ := r.model.EQAt(1).Band(1)
	ptr(pointer.Move, 0, start)
	ptr(pointer.Press, pointer.ButtonPrimary, start)
	if row := r.model.Params().Cursor().Y; row != 1 || r.model.EQAt(1).Selected() != 1 {
		t.Errorf("pressing the handle of band 2 of the small plot: the cursor is on row %d, band %d selected", row, r.model.EQAt(1).Selected()+1)
	}
	ptr(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(20, 0)))
	ptr(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(40, 3*float32(cellH))))
	r.screenshot("eq-rack-2-dragged")
	ptr(pointer.Release, 0, start.Add(f32.Pt(40, 3*float32(cellH))))
	after, _ := r.model.EQAt(1).Band(1)
	if after.Frequency <= before.Frequency*1.5 || after.Gain >= before.Gain {
		t.Errorf("dragged right and down: %+v from %+v", after, before)
	}
	if c1, c2 := r.model.Params().Cursor(), r.model.Params().Cursor2(); c1.Y != 1 || c2.Y != 1 {
		t.Errorf("the drag selected rows %d to %d", c1.Y, c2.Y)
	}
	r.model.History().Undo().Do()
	if b, _ := r.model.EQAt(1).Band(1); b != before {
		t.Errorf("undoing the drag left %+v, not %+v", b, before)
	}
	// the unfolded editor: a double click on empty space adds a band, and
	// selects the row
	plot := f32.Pt(float32(left+6), float32(3*cellH+1+4))
	n := r.model.EQAt(2).NumBands()
	at := f32.Pt(big.geometry.X(600), big.geometry.Y(-12)).Add(plot)
	ptr(pointer.Move, 0, at)
	ptr(pointer.Press, pointer.ButtonPrimary, at)
	ptr(pointer.Release, 0, at)
	ptr(pointer.Press, pointer.ButtonPrimary, at)
	ptr(pointer.Release, 0, at)
	if b, ok := r.model.EQAt(2).Band(n); !ok || !near(b.Frequency, 600, 10) || !near(b.Gain, -12, 0.3) || r.model.Params().Cursor().Y != 2 {
		t.Errorf("a double click on the unfolded plot: %d bands, the new one %+v, the cursor on row %d", r.model.EQAt(2).NumBands(), b, r.model.Params().Cursor().Y)
	}
	// scrolling over empty space leaves the Q alone
	q := func() float64 { b, _ := r.model.EQAt(2).Band(1); return b.Q }
	was := q()
	empty := f32.Pt(big.geometry.X(40), big.geometry.Y(20)).Add(plot)
	ptr(pointer.Move, 0, empty)
	r.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: empty, Scroll: f32.Pt(0, -40), Time: clock})
	r.frame()
	if q() != was {
		t.Errorf("scrolling over empty space changed the Q from %v to %v", was, q())
	}
	r.screenshot("eq-rack-3-added")
	// the chevron above the name folds the unfolded eq unit
	chevron := f32.Pt(float32(r.railWidth()+gtx.Dp(th.UnitEditor.UnitList.LabelWidth)/2), float32(2*cellH+gtx.Dp(th.UnitEditor.UnitList.LabelWidth)/2))
	ptr(pointer.Press, pointer.ButtonPrimary, chevron)
	ptr(pointer.Release, 0, chevron)
	r.frame()
	if r.model.Unit().EQExpanded(2) || !big.small || ie.rowTop(gtx, 3) != 3*cellH {
		t.Errorf("the chevron did not fold the eq unit: unfolded %v, small %v", r.model.Unit().EQExpanded(2), big.small)
	}
	r.screenshot("eq-rack-4-folded")
}

// eqTest lays out the editor of an eq unit alone, at the origin of a window
// of its own, and sends it pointer and key events.
type eqTest struct {
	t      *testing.T
	model  *tracker.Model
	tr     *Tracker
	ed     *EQEditor
	size   image.Point
	now    time.Time
	clock  time.Duration // of the pointer events
	ops    op.Ops
	router input.Router
}

// eqPlotOrigin is where the plot is in the editor: under the divider, inset.
var eqPlotOrigin = f32.Pt(6, 5)

// newEQTest returns an eqTest of a song whose instrument is an oscillator,
// an eq unit with the bands, and an out.
func newEQTest(t *testing.T, bands ...sointu.EQBand) *eqTest {
	t.Helper()
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	eq := sointu.MakeUnit("eq")
	eq.Bands = bands
	units := []sointu.Unit{sointu.MakeUnit("oscillator"), eq, sointu.MakeUnit("out")}
	units[2].Parameters["stereo"] = 0
	for i := range units {
		units[i].ID = i + 1
	}
	song := sointu.Song{BPM: 100, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1}}},
		Patch: sointu.Patch{{Name: "test", NumVoices: 1, Units: units}}}
	b, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	model.Song().Read(io.NopCloser(bytes.NewReader(b)))
	model.Unit().List().SetSelected(1)
	if !model.EQ().Active() || model.EQ().NumBands() != len(bands) {
		t.Fatalf("the eq unit is not selected, or has %d bands", model.EQ().NumBands())
	}
	tr := NewTracker(model)
	ed := NewEQEditor()
	ed.row = 1
	r := &eqTest{t: t, model: model, tr: tr, ed: ed, size: image.Pt(912, 341), now: time.Now()}
	r.frame()
	if g := r.ed.geometry; g.w != 900 || g.h != 300 {
		t.Fatalf("the plot is %v by %v pixels", g.w, g.h)
	}
	return r
}

func (r *eqTest) frame() {
	r.ops.Reset()
	gtx := layout.Context{Ops: &r.ops, Now: r.now, Source: r.router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(r.size), Values: map[string]any{"Tracker": r.tr}}
	r.ed.Layout(gtx)
	r.router.Frame(gtx.Ops)
}

// at returns where a frequency and a level are in the editor.
func (r *eqTest) at(freq, db float64) f32.Point {
	return f32.Pt(r.ed.geometry.X(freq), r.ed.geometry.Y(db)).Add(eqPlotOrigin)
}

// handle returns where the handle of band i is in the editor.
func (r *eqTest) handle(i int) f32.Point {
	c, _, _ := r.model.EQ().Compiled()
	return r.ed.geometry.Handle(c.Bands[i].Actual).Add(eqPlotOrigin)
}

// pointer sends a pointer event of the mouse, 50 ms after the one before,
// and lays out a frame.
func (r *eqTest) pointer(kind pointer.Kind, buttons pointer.Buttons, pos f32.Point, mods key.Modifiers) {
	r.clock += 50 * time.Millisecond
	r.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: pos, Modifiers: mods, Time: r.clock})
	r.frame()
}

// click presses and releases the primary button, and wait lets enough time
// pass for the next press not to be a double click.
func (r *eqTest) click(pos f32.Point) {
	r.pointer(pointer.Press, pointer.ButtonPrimary, pos, 0)
	r.pointer(pointer.Release, 0, pos, 0)
}

func (r *eqTest) wait() { r.clock += time.Second }

// key sends a key press to the editor, which has the focus after a press.
func (r *eqTest) key(name key.Name, mods key.Modifiers) {
	r.router.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
	r.frame()
}

func (r *eqTest) band(i int) sointu.EQBand {
	r.t.Helper()
	b, ok := r.model.EQ().Band(i)
	if !ok {
		r.t.Fatalf("no band %d", i)
	}
	return b
}

func near(a, b, tolerance float64) bool { return math.Abs(a-b) <= tolerance }

func TestEQGeometry(t *testing.T) {
	g := eqGeometry{w: 901, h: 301}
	for _, test := range []struct {
		freq float64
		x    float32
	}{{20, 0}, {200, 300}, {2000, 600}, {20000, 900}} {
		if x := g.X(test.freq); math.Abs(float64(x-test.x)) > 0.01 {
			t.Errorf("%v Hz is at column %v, not %v", test.freq, x, test.x)
		}
		if f := g.Freq(test.x); !near(f/test.freq, 1, 1e-4) {
			t.Errorf("column %v is %v Hz, not %v", test.x, f, test.freq)
		}
	}
	for _, test := range []struct {
		db float64
		y  float32
	}{{24, 0}, {0, 150}, {-12, 225}, {-24, 300}} {
		if y := g.Y(test.db); math.Abs(float64(y-test.y)) > 0.01 {
			t.Errorf("%v dB is at row %v, not %v", test.db, y, test.y)
		}
		if db := g.Db(test.y); !near(db, test.db, 1e-4) {
			t.Errorf("row %v is %v dB, not %v", test.y, db, test.db)
		}
	}
	// handles: at the gain, or at 0 dB without one, and inside the plot
	if h := g.Handle(sointu.EQBand{Type: sointu.EQBell, Frequency: 200, Gain: 12}); h != f32.Pt(300, 75) {
		t.Errorf("the handle of a bell is at %v", h)
	}
	if h := g.Handle(sointu.EQBand{Type: sointu.EQLowCut, Frequency: 10, Gain: 12}); h != f32.Pt(0, 150) {
		t.Errorf("the handle of a low cut below the plot is at %v", h)
	}
	// the nearest handle within the radius; the selected one of two at the
	// same place
	handles := []f32.Point{{X: 100, Y: 100}, {X: 110, Y: 100}, {X: 110, Y: 100}, {X: 400, Y: 50}}
	for _, test := range []struct {
		p        f32.Point
		selected int
		want     int
	}{
		{f32.Pt(101, 100), 0, 0}, {f32.Pt(106, 100), 0, 1}, {f32.Pt(106, 100), 2, 2}, {f32.Pt(110, 100), 2, 2},
		{f32.Pt(110, 100), 1, 1}, {f32.Pt(200, 100), 0, -1}, {f32.Pt(400, 61), 0, 3}, {f32.Pt(400, 63), 0, -1},
	} {
		if got := eqHit(handles, test.p, 12, test.selected); got != test.want {
			t.Errorf("at %v with band %d selected: handle %d, not %d", test.p, test.selected, got, test.want)
		}
	}
	// drags: an octave is 90.3 pixels wide, a decibel 6.25 pixels high
	bell := sointu.EQBand{Type: sointu.EQBell, Frequency: 1000, Gain: 3, Q: 1}
	if b := eqDragged(g, bell, f32.Pt(90.309, -37.5), 0, 60); !near(b.Frequency, 2000, 1) || !near(b.Gain, 9, 0.01) || b.Q != 1 {
		t.Errorf("a bell dragged an octave up and 6 dB up: %+v", b)
	}
	if b := eqDragged(g, bell, f32.Pt(0, 0), -60, 60); b.Frequency != 1000 || b.Gain != 3 || !near(b.Q, 2, 1e-6) {
		t.Errorf("the Q of a bell dragged up: %+v", b)
	}
	cut := sointu.EQBand{Type: sointu.EQLowCut, Frequency: 100, Q: 1}
	if b := eqDragged(g, cut, f32.Pt(-90.309, -60), 0, 60); !near(b.Frequency, 50, 0.1) || b.Gain != 0 || !near(b.Q, 2, 1e-6) {
		t.Errorf("a low cut dragged an octave down, and up: %+v", b)
	}
	if b := eqDragged(g, bell, f32.Pt(5000, -5000), 5000, 60); b.Frequency != sointu.EQMaxFrequency || b.Gain != sointu.EQMaxGain || b.Q != sointu.EQMinQ {
		t.Errorf("a bell dragged far: %+v", b)
	}
}

// TestEQEditorPointer drives the editor with the mouse: double clicks add
// and remove bands, dragging a handle moves its band and is one step of the
// undo history, scrolling changes the Q, the right button switches a band
// off.
func TestEQEditorPointer(t *testing.T) {
	r := newEQTest(t)
	eq := r.model.EQ()
	// a double click on empty space adds a bell there
	r.click(r.at(1000, 6))
	if eq.NumBands() != 0 {
		t.Fatal("a click added a band")
	}
	r.click(r.at(1000, 6))
	if b := r.band(0); eq.NumBands() != 1 || b.Type != sointu.EQBell || !near(b.Frequency, 1000, 5) || !near(b.Gain, 6, 0.1) {
		t.Fatalf("a double click at 1 kHz and 6 dB: %d bands, %+v", eq.NumBands(), b)
	}
	// near the edges, cuts
	r.wait()
	r.click(r.at(30, 0))
	r.click(r.at(30, 0))
	r.wait()
	r.click(r.at(15000, -3))
	r.click(r.at(15000, -3))
	if eq.NumBands() != 3 || r.band(1).Type != sointu.EQLowCut || r.band(2).Type != sointu.EQLadder || eq.Selected() != 2 {
		t.Fatalf("double clicks at the edges: %d bands, %+v, %+v, band %d selected", eq.NumBands(), r.band(1), r.band(2), eq.Selected())
	}
	// dragging the handle of the bell: frequency and gain follow, in one
	// step of the undo history
	r.wait()
	before := r.band(0)
	start := r.handle(0)
	r.pointer(pointer.Press, pointer.ButtonPrimary, start, 0)
	if eq.Selected() != 0 {
		t.Errorf("pressing the handle of band 1 selected band %d", eq.Selected()+1)
	}
	for _, d := range []f32.Point{{X: 10, Y: 5}, {X: 40, Y: 20}, {X: 90.3, Y: 37.5}} {
		r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(d), 0)
	}
	if b := r.band(0); !near(b.Frequency/before.Frequency, 2, 0.01) || !near(b.Gain, before.Gain-6, 0.05) || b.Q != before.Q {
		t.Errorf("dragged an octave up and 6 dB down: %+v from %+v", b, before)
	}
	// with Shift, a fifth as far; with Alt, the Q and nothing else
	moved := r.band(0)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(90.3+90.3*5, 37.5)), key.ModShift)
	if b := r.band(0); !near(b.Frequency/moved.Frequency, 2, 0.01) || b.Gain != moved.Gain {
		t.Errorf("dragged five octaves with Shift: %+v from %+v", b, moved)
	}
	moved = r.band(0)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(90.3+90.3*5+50, 37.5-60)), key.ModAlt)
	if b := r.band(0); b.Frequency != moved.Frequency || b.Gain != moved.Gain || !near(b.Q/moved.Q, 2, 0.01) {
		t.Errorf("dragged 60 pixels up with Alt: %+v from %+v", b, moved)
	}
	r.pointer(pointer.Release, 0, start, 0)
	after := r.band(0)
	r.model.History().Undo().Do()
	if b := r.band(0); b != before {
		t.Errorf("undoing the drag left %+v, not %+v", b, before)
	}
	r.model.History().Redo().Do()
	if b := r.band(0); b != after {
		t.Errorf("redoing the drag left %+v, not %+v", b, after)
	}
	r.frame()
	// the handle is where the units put the band, and a drag starts from the
	// values that were set: a second drag does not jump
	r.wait()
	start = r.handle(0)
	r.pointer(pointer.Press, pointer.ButtonPrimary, start, 0)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(0.2, 0)), 0)
	r.pointer(pointer.Release, 0, start, 0)
	if b := r.band(0); !near(b.Frequency/after.Frequency, 1, 0.005) || !near(b.Gain, after.Gain, 0.05) {
		t.Errorf("a short drag moved the band from %+v to %+v", after, b)
	}
	// a low cut has no gain: dragging its handle up raises its Q
	r.wait()
	cut := r.band(1)
	start = r.handle(1)
	r.pointer(pointer.Press, pointer.ButtonPrimary, start, 0)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(0, -60)), 0)
	r.pointer(pointer.Release, 0, start, 0)
	if b := r.band(1); !near(b.Q/cut.Q, 2, 0.01) || b.Gain != 0 || !near(b.Frequency/cut.Frequency, 1, 0.01) {
		t.Errorf("a low cut dragged 60 pixels up: %+v from %+v", b, cut)
	}
	// scrolling over a handle changes the Q of its band: up for more. What
	// is scrolled within half a second is one step of the undo history
	r.wait()
	cut = r.band(1)
	r.pointer(pointer.Move, 0, r.handle(1), 0) // the plot takes the scrolling over a handle
	for range 3 {
		r.pointer(pointer.Scroll, 0, r.handle(1), 0)
	}
	scroll := func(y float32, mods key.Modifiers) {
		r.router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: r.handle(1), Scroll: f32.Pt(0, y), Modifiers: mods, Time: r.clock})
		r.frame()
	}
	scroll(-20, 0)
	scroll(-40, 0)
	scroll(-40, key.ModShift) // fine: a quarter
	if b := r.band(1); !near(b.Q/cut.Q, math.Pow(2, 3.5/6), 0.02) {
		t.Errorf("scrolled 3.5 steps up: Q %v from %v", b.Q, cut.Q)
	}
	r.now = r.now.Add(time.Second)
	r.frame()
	scroll(20, 0)
	if b := r.band(1); !near(b.Q/cut.Q, math.Pow(2, 2.5/6), 0.02) {
		t.Errorf("scrolled a step back down: Q %v from %v", b.Q, cut.Q)
	}
	r.model.History().Undo().Do()
	if b := r.band(1); !near(b.Q/cut.Q, math.Pow(2, 3.5/6), 0.02) {
		t.Errorf("undoing the second scroll: Q %v from %v", b.Q, cut.Q)
	}
	r.model.History().Undo().Do()
	if b := r.band(1); b != cut {
		t.Errorf("undoing the first scroll left %+v, not %+v", b, cut)
	}
	r.now = r.now.Add(time.Second)
	r.frame()
	// the right button switches a band off and on
	r.pointer(pointer.Press, pointer.ButtonSecondary, r.handle(2), 0)
	r.pointer(pointer.Release, 0, r.handle(2), 0)
	if !r.band(2).Disabled || eq.Selected() != 2 {
		t.Errorf("the right button on band 3: %+v, band %d selected", r.band(2), eq.Selected()+1)
	}
	r.pointer(pointer.Press, pointer.ButtonSecondary, r.handle(2), 0)
	r.pointer(pointer.Release, 0, r.handle(2), 0)
	if r.band(2).Disabled {
		t.Error("the right button did not switch band 3 back on")
	}
	// a double click on a handle removes its band
	r.wait()
	r.click(r.handle(1))
	r.click(r.handle(1))
	if eq.NumBands() != 2 || r.band(1).Type != sointu.EQLadder {
		t.Errorf("a double click on the handle of band 2 left %d bands, the second %+v", eq.NumBands(), r.band(1))
	}
	r.model.History().Undo().Do()
	if eq.NumBands() != 3 || r.band(1).Type != sointu.EQLowCut {
		t.Errorf("undoing it left %d bands", eq.NumBands())
	}
}

// TestEQEditorKeys drives the editor with the keyboard, once the plot has
// the focus.
func TestEQEditorKeys(t *testing.T) {
	r := newEQTest(t, sointu.EQBand{Type: sointu.EQBell, Frequency: 100, Gain: 3, Q: 1}, sointu.EQBand{Type: sointu.EQBell, Frequency: 1000, Gain: -3, Q: 2})
	eq := r.model.EQ()
	r.click(r.at(5000, 12)) // on empty space: the plot has the focus
	r.wait()
	if eq.NumBands() != 2 || eq.Selected() != 0 {
		t.Fatalf("%d bands, band %d selected", eq.NumBands(), eq.Selected()+1)
	}
	r.key(key.NameRightArrow, 0)
	r.key(key.NameRightArrow, 0)
	if eq.Selected() != 1 {
		t.Fatalf("Right selected band %d", eq.Selected()+1)
	}
	r.key(key.NameLeftArrow, 0)
	if eq.Selected() != 0 {
		t.Fatalf("Left selected band %d", eq.Selected()+1)
	}
	r.key(key.NameRightArrow, key.ModShift)
	r.key(key.NameUpArrow, 0)
	r.key(key.NameUpArrow, key.ModShift)
	r.key(key.NameDownArrow, key.ModShortcut) // fine
	r.key(key.NameUpArrow, key.ModAlt)
	if b := r.band(0); !near(b.Frequency, 100*math.Pow(2, 1.0/12), 0.2) || !near(b.Gain, 3.875, 0.01) || !near(b.Q, math.Pow(2, 1.0/6), 0.01) {
		t.Errorf("a step of the frequency, 1.75 of the gain and one of the Q: %+v", b)
	}
	r.model.History().Undo().Do() // every key is a step of the undo history
	if b := r.band(0); b.Q != 1 || !near(b.Gain, 3.875, 0.01) {
		t.Errorf("undoing the last key: %+v", b)
	}
	r.key(key.NameRightArrow, key.ModAlt)
	if b := r.band(0); b.Type != sointu.EQLowCut {
		t.Errorf("Alt+Right made the bell a %s", b.Type)
	}
	r.key(key.NameLeftArrow, key.ModAlt)
	r.key(key.NameReturn, key.ModShortcut)
	if b := r.band(0); b.Type != sointu.EQBell || !b.Disabled {
		t.Errorf("Alt+Left and Ctrl+Enter: %+v", b)
	}
	r.key(key.NameReturn, key.ModShortcut)
	r.key(key.NameReturn, 0)
	if eq.NumBands() != 3 || eq.Selected() != 2 || r.band(2).Type != sointu.EQBell || r.band(0).Disabled {
		t.Fatalf("Enter: %d bands, band %d selected", eq.NumBands(), eq.Selected()+1)
	}
	// between the two bands furthest apart, or the ends of the plot
	if b := r.band(2); !near(b.Frequency, math.Sqrt(1000*20000), 20) || b.Gain != 0 {
		t.Errorf("the new band is %+v", b)
	}
	r.key(key.NameDeleteBackward, 0)
	r.key(key.NameDeleteForward, 0)
	if eq.NumBands() != 1 || !near(r.band(0).Frequency, 106, 1) || eq.Selected() != 0 {
		t.Errorf("deleted two bands: %d left, the first %+v, band %d selected", eq.NumBands(), r.band(0), eq.Selected()+1)
	}
}
