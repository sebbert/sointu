package gioui

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// rackTest lays out the rack of a tracker alone, at the origin of a window
// of its own, and sends it pointer events.
type rackTest struct {
	t      *testing.T
	model  *tracker.Model
	tr     *Tracker
	size   image.Point
	now    time.Time
	ops    op.Ops
	router input.Router
}

// rackTestBuffers are the buffers of the song of a rackTest: one that is
// written, and one with a sample (which is not decoded here).
var rackTestBuffers = sointu.Buffers{
	{ID: 1, Name: "response", Channels: 2, Frames: 88200},
	{ID: 2, Name: "cabinet", Channels: 1, Sample: &sointu.AudioSample{FileName: "cabinet.wav", Data: sointu.Blob("RIFF")}},
}

// newRackTest returns a rackTest of a song with one instrument of the units.
func newRackTest(t *testing.T, units ...sointu.Unit) *rackTest {
	t.Helper()
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	for i := range units {
		units[i].ID = i + 1
	}
	song := sointu.Song{BPM: 100, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1}}},
		Patch: sointu.Patch{{Name: "test", NumVoices: 1, Units: units}}, Buffers: rackTestBuffers}
	b, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	model.Song().Read(io.NopCloser(bytes.NewReader(b)))
	if got := model.Unit().List().Count(); got != len(units) {
		t.Fatalf("the song has %v units, want %v", got, len(units))
	}
	r := &rackTest{t: t, model: model, tr: NewTracker(model), size: image.Pt(1000, 600), now: time.Now()}
	r.frame()
	return r
}

// settle lays out frames until the signal rail has stopped widening.
func (r *rackTest) settle() {
	r.frame()
	r.now = r.now.Add(time.Second)
	r.frame()
}

// railWidth returns the width of the signal rail in pixels, as last laid
// out.
func (r *rackTest) railWidth() int {
	return int(r.tr.PatchPanel.instrEditor.railLane.at(r.now) + 0.5)
}

func (r *rackTest) gtx() C {
	return layout.Context{Ops: &r.ops, Now: r.now, Source: r.router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(r.size), Values: map[string]any{"Tracker": r.tr}}
}

// frame lays out the rack, which handles the events sent since the last
// frame.
func (r *rackTest) frame() {
	r.ops.Reset()
	gtx := r.gtx()
	r.tr.PatchPanel.instrEditor.layoutRack(gtx)
	r.router.Frame(gtx.Ops)
}

// screenshot draws the rack into a PNG in the directory that
// SOINTU_TEST_SCREENSHOTS names, if it is set.
func (r *rackTest) screenshot(name string) {
	r.t.Helper()
	dir := os.Getenv("SOINTU_TEST_SCREENSHOTS")
	if dir == "" {
		return
	}
	window, err := headless.NewWindow(r.size.X, r.size.Y)
	if err != nil {
		r.t.Skipf("no headless window: %v", err)
	}
	defer window.Release()
	r.frame()
	if err := window.Frame(&r.ops); err != nil {
		r.t.Fatalf("%v: %v", name, err)
	}
	img := image.NewRGBA(image.Rectangle{Max: r.size})
	if err := window.Screenshot(img); err != nil {
		r.t.Fatalf("%v: %v", name, err)
	}
	f, err := os.Create(dir + "/" + name + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

// param returns the parameter of the unit on a row by its name, and its
// column.
func (r *rackTest) param(row int, name string) (tracker.Parameter, int) {
	r.t.Helper()
	for x := range r.model.Params().RowWidth(row) {
		if p := r.model.Params().Item(tracker.Point{X: x, Y: row}); p.Name() == name {
			return p, x
		}
	}
	r.t.Fatalf("row %v has no parameter %v", row, name)
	return tracker.Parameter{}, 0
}

// cellCenter returns where the middle of a cell of the rack is in the
// window, as the rack is laid out now.
func (r *rackTest) cellCenter(x, y int) f32.Point {
	gtx := r.gtx()
	th := r.tr.Theme
	w, h := gtx.Dp(th.UnitEditor.Width), gtx.Dp(th.UnitEditor.Height)
	left := gtx.Dp(th.UnitEditor.UnitList.LabelWidth) + r.railWidth()
	return f32.Pt(float32(left+x*w+w/2), float32(y*h+h/2))
}

// pointer sends a pointer event of the mouse at a position in the window,
// and lays out a frame.
func (r *rackTest) pointer(kind pointer.Kind, buttons pointer.Buttons, pos f32.Point) {
	r.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: pos})
	r.frame()
}

// loadvals returns n loadval units.
func loadvals(n int) []sointu.Unit {
	ret := make([]sointu.Unit, n)
	for i := range ret {
		ret[i] = sointu.MakeUnit("loadval")
	}
	return ret
}

// deepRack is a rack whose stack is 7 signals deep, more than the rail has
// room for at least: 7 loadvals, a spawn with 2 args on row 7, and 2
// loadvals. With fewer args, the stack is deeper after the spawn.
func deepRack(t *testing.T) *rackTest {
	spawn := sointu.MakeUnit("spawn")
	spawn.Parameters["args"] = 2
	return newRackTest(t, append(append(loadvals(7), spawn), loadvals(2)...)...)
}

// Dragging a knob follows the pointer in the window, also when the knob
// moves: the args of a spawn change the width of the signal rail, and with
// it where the knob is.
func TestKnobDragWhenRackMoves(t *testing.T) {
	r := deepRack(t)
	args, col := r.param(7, "args")
	if w := r.railWidth(); args.Value() != 2 || w != 70 {
		t.Fatalf("args %v, rail width %v", args.Value(), w)
	}
	// the knob goes through its range of 4 in 512 dp: a value every 128 dp
	start := r.cellCenter(col, 7)
	r.pointer(pointer.Press, pointer.ButtonPrimary, start)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(-100, 0)))
	r.settle()
	if w := r.railWidth(); args.Value() != 1 || w != 80 {
		t.Fatalf("dragged -100 dp: args %v, rail width %v", args.Value(), w)
	}
	for _, d := range []float32{-120, -5, -120, 5, -10} {
		want := int(2 + d/128)
		r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(d, 0)))
		if args.Value() != want {
			t.Errorf("dragged %v dp: args %v, want %v", d, args.Value(), want)
		}
		r.settle()
	}
	r.pointer(pointer.Release, 0, start.Add(f32.Pt(-10, 0)))
	if args.Value() != 1 {
		t.Errorf("released: args %v", args.Value())
	}
}

// The rail has room for 6 signals, so that the rack stays where it is when
// the stack changes within that. A deeper stack widens it, over
// railLaneDuration, and it stays that wide until other units are edited.
func TestRailLaneOfRack(t *testing.T) {
	spawn := sointu.MakeUnit("spawn")
	r := newRackTest(t, append(append(loadvals(4), spawn), sointu.MakeUnit("oscillator"))...)
	if w := r.railWidth(); w != 60 || r.model.Unit().RailWidth() != 5 {
		t.Fatalf("rail width %v, of %v signals", w, r.model.Unit().RailWidth())
	}
	stereo, _ := r.param(5, "stereo")
	args, _ := r.param(4, "args")
	for _, set := range []func(){func() { stereo.SetValue(1) }, func() { args.SetValue(3) }, func() { stereo.SetValue(0) }, func() { args.SetValue(0) }} {
		set()
		r.frame()
		if w := r.railWidth(); w != 60 {
			t.Errorf("%v signals: rail width %v", r.model.Unit().RailWidth(), w)
		}
	}
	// 7 signals: it widens by 10 dp, not at once
	r.model.Unit().List().SetSelected(5)
	r.model.Unit().Add(false).Do()
	r.model.Unit().SetType("oscillator")
	r.model.Unit().Add(false).Do()
	r.model.Unit().SetType("oscillator")
	if n := r.model.Unit().RailWidth(); n != 7 {
		t.Fatalf("%v signals, want 7", n)
	}
	r.frame()
	if w := r.railWidth(); w != 60 {
		t.Errorf("as it starts to widen: rail width %v", w)
	}
	r.now = r.now.Add(railLaneDuration / 3)
	r.frame()
	if w := r.railWidth(); w <= 60 || w >= 70 {
		t.Errorf("widening: rail width %v", w)
	}
	r.settle()
	if w := r.railWidth(); w != 70 {
		t.Errorf("widened: rail width %v", w)
	}
	r.screenshot("rack-7-signals")
	// it does not narrow again
	r.model.Unit().Delete().Do()
	r.settle()
	if w := r.railWidth(); w != 70 || r.model.Unit().RailWidth() != 6 {
		t.Errorf("%v signals: rail width %v", r.model.Unit().RailWidth(), w)
	}
	// until other units are edited
	r.model.Instrument().Add().Do()
	r.frame()
	if w := r.railWidth(); w != 60 {
		t.Errorf("another instrument: rail width %v", w)
	}
	r.model.Instrument().List().SetSelected(0)
	r.frame()
	if w := r.railWidth(); w != 60 {
		t.Errorf("back in the instrument, 6 signals: rail width %v", w)
	}
}

func TestRailLane(t *testing.T) {
	var l RailLane
	now := time.Now()
	check := func(what string, key any, signals, signalWidth, want int, wantWidening bool) {
		t.Helper()
		if w, widening := l.Update(now, key, signals, signalWidth); w != want || widening != wantWidening {
			t.Errorf("%v: width %v, widening %v, want %v, %v", what, w, widening, want, wantWidening)
		}
	}
	check("first", 1, 6, 10, 60, false)
	check("fewer signals", 1, 3, 10, 60, false)
	check("more signals", 1, 8, 10, 60, true)
	now = now.Add(railLaneDuration / 2)
	check("halfway", 1, 8, 10, 78, true) // eased out: 20·(1 - 0.5³)
	check("still more signals", 1, 9, 10, 78, true)
	now = now.Add(railLaneDuration)
	check("widened", 1, 6, 10, 90, false)
	now = now.Add(time.Hour)
	check("later", 1, 10, 10, 90, true)
	check("zoomed", 1, 10, 20, 200, false)
	check("other units", 2, 4, 20, 80, false)
}

// The inner rows of an unfolded module unit have the same rail.
func TestRailLaneUnfolded(t *testing.T) {
	r := newRackTest(t, sointu.MakeUnit("loadval"))
	f, err := os.Open("../../examples/modules.yml")
	if err != nil {
		t.Fatal(err)
	}
	r.model.Song().Read(f)
	r.model.Instrument().List().SetSelected(1)
	r.model.Unit().List().SetSelected(1)
	r.settle()
	folded := r.railWidth()
	r.screenshot("rack-folded")
	r.model.Unit().Unfold().SetValue(true)
	r.settle()
	if n := r.model.Unit().RailWidth(); n > 6 || r.railWidth() != folded || folded != 60 {
		t.Errorf("unfolded, %v signals: rail width %v, folded %v", n, r.railWidth(), folded)
	}
	r.screenshot("rack-unfolded")
}

// The row of a convolution unit: its buffer as a menu, the start in the
// buffer, the length and the predelay as times, and for a buffer that is
// written, how fast it is read again and whether changes fade in; a sample
// is read once and has neither.
func TestConvolutionRow(t *testing.T) {
	written, sample := sointu.MakeUnit("convolution"), sointu.MakeUnit("convolution")
	written.Parameters["buffer"], written.Parameters["stereo"], written.Parameters["gain"] = 1, 1, 28
	written.Parameters["follow"], written.Parameters["fade"], written.Parameters["predelay"] = 2, 1, 14
	sample.Parameters["buffer"], sample.Parameters["length"], sample.Parameters["dry"] = 2, 40, 64
	noise, write := sointu.MakeUnit("noise"), sointu.MakeUnit("bufwrite")
	noise.Parameters["stereo"], write.Parameters["stereo"], write.Parameters["buffer"], write.Parameters["oneshot"] = 1, 1, 1, 1
	r := newRackTest(t, append([]sointu.Unit{noise, write}, append(loadvals(2), written, sample)...)...)
	r.settle()
	names := func(row int) (ret []string) {
		for x := range r.model.Params().RowWidth(row) {
			if p := r.model.Params().Item(tracker.Point{X: x, Y: row}); p.Name() != "" {
				ret = append(ret, p.Name())
			}
		}
		return
	}
	if got, want := names(4), []string{"stereo", "gain", "buffer", "start", "length", "predelay", "follow", "fade", "dry"}; !slices.Equal(got, want) {
		t.Errorf("the parameters of a unit with a written buffer: %v, want %v", got, want)
	}
	if got, want := names(5), []string{"stereo", "gain", "buffer", "start", "length", "predelay", "dry"}; !slices.Equal(got, want) {
		t.Errorf("the parameters of a unit with a sample: %v, want %v", got, want)
	}
	for name, want := range map[string]string{"gain": "-22.5 dB", "buffer": "response", "length": "2.97 s (131072), the buffer has 2.00 s", "predelay": "20.3 ms", "follow": "4 ×"} {
		if p, _ := r.param(4, name); p.Hint().Label != want {
			t.Errorf("%s of the unit with a written buffer shows %q, want %q", name, p.Hint().Label, want)
		}
	}
	for name, want := range map[string]string{"buffer": "cabinet", "length": "46.4 ms (2048)", "dry": "-6.02 dB"} {
		if p, _ := r.param(5, name); p.Hint().Label != want {
			t.Errorf("%s of the unit with a sample shows %q, want %q", name, p.Hint().Label, want)
		}
	}
	r.screenshot("convolution_row")
}
