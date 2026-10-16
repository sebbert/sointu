package gioui

import (
	"fmt"
	"math"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"github.com/vsariola/sointu/tracker"
)

// bufferSlices edits the slice markers of the selected buffer on its
// waveform, selects a range of it, and lays out the slice and sample editing
// tools below it.
//
// On the waveform: ctrl/cmd+click adds a marker, dragging a marker's handle
// moves it, right-clicking a marker deletes it, and shift+drag selects a
// range, which shift+click clears. The rest pans and zooms as in every plot.
type bufferSlices struct {
	tr             *Tracker
	frames, origin int // of the waveform drawn last: its length, and the frame the markers count from

	drag      int // the marker being dragged, or -1
	dragFrame int // where it is dragged to, from the origin
	selecting bool
	selFrom   int
	pointerID pointer.ID

	equalBtn, detectBtn, clearBtn                   *Clickable
	cropBtn, deleteBtn, trimBtn, fadeInBtn, fadeOut *Clickable
	count, sensitivity, threshold, fadeLen, curve   *NumericUpDownState
}

// sliceGrabPx is how near, in pixels, a press is to a marker to grab it.
const sliceGrabPx = 5

func newBufferSlices() *bufferSlices {
	return &bufferSlices{drag: -1,
		equalBtn: new(Clickable), detectBtn: new(Clickable), clearBtn: new(Clickable),
		cropBtn: new(Clickable), deleteBtn: new(Clickable), trimBtn: new(Clickable), fadeInBtn: new(Clickable), fadeOut: new(Clickable),
		count: NewNumericUpDownState(), sensitivity: NewNumericUpDownState(), threshold: NewNumericUpDownState(),
		fadeLen: NewNumericUpDownState(), curve: NewNumericUpDownState(),
	}
}

// toX returns where a frame from the origin is on the waveform, 0 to 1.
func (bs *bufferSlices) toX(f int) float32 {
	return float32((bs.origin+f)%bs.frames) / float32(bs.frames)
}

// fromX returns the frame from the origin at x on the waveform.
func (bs *bufferSlices) fromX(x float32) int {
	f := min(max(int(math.Round(float64(x)*float64(bs.frames))), 0), bs.frames)
	return ((f-bs.origin)%bs.frames + bs.frames) % bs.frames
}

// decorate adds the slice markers and the selected range to the waveform
// plot, and makes the plot hand its pointer events to bs first.
func (bs *bufferSlices) decorate(tr *Tracker, plot *Plot, frames int) {
	bs.tr, bs.frames = tr, frames
	plot.Spans = plot.Spans[:0]
	plot.Editor = nil
	if frames <= 0 || !tr.Buffer().CanSlice() {
		bs.drag, bs.selecting = -1, false
		return
	}
	plot.Editor = bs
	bs.origin = tr.Buffer().SliceOrigin()
	for i, m := range tr.Buffer().Slices() {
		if i == bs.drag {
			m = bs.dragFrame
		}
		plot.Markers = append(plot.Markers, PlotMarker{X: bs.toX(m), Alpha: 1, Slice: true})
	}
	if a, b, ok := tr.Buffer().Selection(); ok {
		if bs.origin == 0 {
			plot.Spans = append(plot.Spans, plotRange{bs.toX(a), float32(b) / float32(frames)})
		}
	}
}

func (bs *bufferSlices) PlotHint() string {
	return "ctrl+click: marker · right-click: delete · shift+drag: select"
}

func (bs *bufferSlices) PlotPointer(e pointer.Event, x, pxPerX float32) bool {
	if bs.tr == nil || bs.frames <= 0 {
		return false
	}
	b := bs.tr.Buffer()
	switch e.Kind {
	case pointer.Press:
		near := -1
		for i, m := range b.Slices() {
			if d := math.Abs(float64(bs.toX(m)-x)) * float64(pxPerX); d <= sliceGrabPx && (near < 0 || d < math.Abs(float64(bs.toX(b.Slices()[near])-x))*float64(pxPerX)) {
				near = i
			}
		}
		switch {
		case e.Buttons&pointer.ButtonSecondary != 0 && near >= 0:
			b.DeleteSlice(near)
			return true
		case e.Buttons&pointer.ButtonPrimary == 0:
			return false
		case e.Modifiers.Contain(key.ModShift):
			bs.selecting, bs.selFrom, bs.pointerID = true, bs.fromX(x), e.PointerID
			b.SetSelection(0, 0)
			return true
		case e.Modifiers.Contain(key.ModShortcut):
			b.AddSlice(bs.fromX(x))
			return true
		case near >= 0:
			bs.drag, bs.dragFrame, bs.pointerID = near, b.Slices()[near], e.PointerID
			return true
		}
	case pointer.Drag:
		if e.PointerID != bs.pointerID {
			return false
		}
		switch {
		case bs.drag >= 0:
			bs.dragFrame = bs.fromX(x)
			return true
		case bs.selecting:
			f := bs.fromX(x)
			b.SetSelection(min(bs.selFrom, f), max(bs.selFrom, f))
			return true
		}
	case pointer.Release, pointer.Cancel:
		if e.PointerID != bs.pointerID {
			return false
		}
		switch {
		case bs.drag >= 0:
			if e.Kind == pointer.Release {
				b.MoveSlice(bs.drag, bs.dragFrame)
			}
			bs.drag = -1
			return true
		case bs.selecting:
			bs.selecting = false
			return true
		}
	}
	return false
}

// Layout lays out the slice tools, and for samples the sample editing tools.
func (bs *bufferSlices) Layout(gtx C) D {
	tr := TrackerFromContext(gtx)
	b := tr.Buffer()
	if !b.CanSlice() {
		return D{}
	}
	th := tr.Theme
	upDown := func(v tracker.Int, s *NumericUpDownState, tip string) layout.Widget {
		n := NumUpDown(v, th, s, tip)
		return n.Layout
	}
	btn := func(a tracker.Action, c *Clickable, txt, tip string) layout.Widget {
		ab := ActionBtn(a, th, c, txt, tip)
		return ab.Layout
	}
	label := func(s string) layout.Widget {
		return Label(th, &th.InstrumentEditor.Properties.Label, s).Layout
	}
	gap := layout.Rigid(layout.Spacer{Width: 6}.Layout)
	row := func(name string, ws ...layout.FlexChild) layout.Widget {
		return func(gtx C) D {
			return layoutBufferLine(gtx, name, false, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, ws...)
			})
		}
	}
	n := len(b.Slices()) + 1
	rows := []layout.Widget{
		row("Slices",
			layout.Rigid(label(plural(n, "slice"))),
			gap,
			layout.Rigid(upDown(b.SliceCount(), bs.count, "How many slices of equal length\nSlice equally makes")),
			layout.Rigid(btn(b.SliceEqually(), bs.equalBtn, "Slice equally", "Put markers that cut the buffer\ninto slices of equal length")),
			gap,
			layout.Rigid(upDown(b.SliceSensitivity(), bs.sensitivity, "The sensitivity of Detect transients,\n0 to 100: more finds more")),
			layout.Rigid(btn(b.DetectTransients(), bs.detectBtn, "Detect transients", "Put markers at the transients of the audio,\nreplacing the markers")),
			gap,
			layout.Rigid(btn(b.ClearSlices(), bs.clearBtn, "Clear", "Delete all markers")),
		),
	}
	if b.IsWritable() {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(rows[0]))
	}
	editing := ""
	if b.IsEditing() {
		editing = "editing..."
	} else if a, e, ok := b.Selection(); ok {
		editing = fmt.Sprintf("%s selected", formatSeconds(e-a))
	}
	rows = append(rows,
		row("Edit",
			layout.Rigid(btn(b.Crop(), bs.cropBtn, "Crop", "Keep only the selected range\n(shift+drag on the waveform)")),
			layout.Rigid(btn(b.DeleteRange(), bs.deleteBtn, "Delete", "Delete the selected range")),
			gap,
			layout.Rigid(btn(b.TrimSilence(), bs.trimBtn, "Trim silence", "Remove the ends of the sample\nthat are below the level")),
			layout.Rigid(upDown(b.TrimThreshold(), bs.threshold, "The level of Trim silence, in dB")),
			layout.Rigid(label("dB")),
			gap,
			layout.Rigid(label(editing)),
		),
		row("Fades",
			layout.Rigid(upDown(b.FadeLength(), bs.fadeLen, "The length of the fades, in ms")),
			layout.Rigid(label("ms")),
			gap,
			layout.Rigid(upDown(b.FadeCurve(), bs.curve, "The curve of the fades: 0 is linear,\nabove it they start slowly, below it quickly")),
			layout.Rigid(label("curve")),
			gap,
			layout.Rigid(btn(b.FadeIn(), bs.fadeInBtn, "Fade in", "Fade in the start of the sample")),
			layout.Rigid(btn(b.FadeOut(), bs.fadeOut, "Fade out", "Fade out the end of the sample")),
		),
	)
	children := make([]layout.FlexChild, len(rows))
	for i, r := range rows {
		children[i] = layout.Rigid(r)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// formatSeconds formats frames at 44100 Hz as seconds.
func formatSeconds(frames int) string {
	return fmt.Sprintf("%.3f s", float64(frames)/44100)
}
