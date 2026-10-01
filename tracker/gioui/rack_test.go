package gioui

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"os"
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
	ops    op.Ops
	router input.Router
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
		Patch: sointu.Patch{{Name: "test", NumVoices: 1, Units: units}}}
	b, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	model.Song().Read(io.NopCloser(bytes.NewReader(b)))
	if got := model.Unit().List().Count(); got != len(units) {
		t.Fatalf("the song has %v units, want %v", got, len(units))
	}
	r := &rackTest{t: t, model: model, tr: NewTracker(model), size: image.Pt(1000, 600)}
	r.frame()
	return r
}

func (r *rackTest) gtx() C {
	return layout.Context{Ops: &r.ops, Now: time.Now(), Source: r.router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
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
	left := gtx.Dp(th.UnitEditor.UnitList.LabelWidth) + gtx.Dp(th.SignalRail.SignalWidth)*r.model.Unit().RailWidth()
	return f32.Pt(float32(left+x*w+w/2), float32(y*h+h/2))
}

// pointer sends a pointer event of the mouse at a position in the window,
// and lays out a frame.
func (r *rackTest) pointer(kind pointer.Kind, buttons pointer.Buttons, pos f32.Point) {
	r.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: pos})
	r.frame()
}

// spawnRack is a rack whose rail is 3 signals wide with no args of the
// spawn, on row 2, and 2 signals wide with 1 or 2.
func spawnRack(t *testing.T) *rackTest {
	return newRackTest(t, sointu.MakeUnit("loadval"), sointu.MakeUnit("loadval"), sointu.MakeUnit("spawn"), sointu.MakeUnit("loadval"))
}

// Dragging a knob follows the pointer in the window, also when the knob
// moves: the args of a spawn change the width of the signal rail, and with
// it where the knob is.
func TestKnobDragWhenRackMoves(t *testing.T) {
	r := spawnRack(t)
	args, col := r.param(2, "args")
	if w := r.model.Unit().RailWidth(); args.Value() != 0 || w != 3 {
		t.Fatalf("args %v, rail width %v", args.Value(), w)
	}
	// the knob goes through its range of 4 in 512 dp: a value every 128 dp
	start := r.cellCenter(col, 2)
	r.pointer(pointer.Press, pointer.ButtonPrimary, start)
	r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(130, 0)))
	if w := r.model.Unit().RailWidth(); args.Value() != 1 || w != 2 {
		t.Fatalf("dragged 130 dp: args %v, rail width %v", args.Value(), w)
	}
	if moved := r.cellCenter(col, 2); moved == start {
		t.Fatalf("the knob did not move")
	}
	r.frame() // the knob has moved
	for _, d := range []float32{250, 131, 250, 127, 129} {
		want := int(d / 128)
		r.pointer(pointer.Move, pointer.ButtonPrimary, start.Add(f32.Pt(d, 0)))
		if args.Value() != want {
			t.Errorf("dragged %v dp: args %v, want %v", d, args.Value(), want)
		}
		r.frame()
	}
	r.pointer(pointer.Release, 0, start.Add(f32.Pt(129, 0)))
	if args.Value() != 1 {
		t.Errorf("released: args %v", args.Value())
	}
}
