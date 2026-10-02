package gioui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// The plot of the eq editor: 20 Hz to 20 kHz on a logarithmic axis, and 24 dB
// up and down.
const (
	eqPlotMinHz = 20
	eqPlotMaxHz = 20000
	eqPlotDb    = 24
	// eqHandleDp is the radius of the handle of a band, and eqHitDp how far
	// from its center the pointer still takes it
	eqHandleDp = 8
	eqHitDp    = 12
	// eqQDragDp is how far to drag for the Q to double
	eqQDragDp = 60
	// eqFineDrag is how much less a drag moves with Shift held
	eqFineDrag = 0.2
	// eqScrollDp is how far to scroll for a step of the Q
	eqScrollDp = 20
	// eqCurveStep is the distance of the points of the curves, in pixels
	eqCurveStep = 2
	// eqEditorHeight is the height of the editor, plot and values, at most
	eqEditorHeight unit.Dp = 300
)

type (
	// EQEditor is the editor of an eq unit: the plot of what the unit does
	// to each frequency, with a handle for every band, and under it the
	// values of the selected band.
	EQEditor struct {
		typeBtn      *Clickable
		typeMenu     *MenuState
		onBtn        *Clickable
		addBtn       *Clickable
		deleteBtn    *Clickable
		spectrumBtn  *Clickable
		freqEditor   *DraftEditor
		gainEditor   *DraftEditor
		qEditor      *DraftEditor
		showSpectrum tracker.Bool
		spectrumOn   bool

		hovered bool // the pointer is over the plot
		hover   int  // the band whose handle it is over, or -1
		drag    eqDrag
		// the last press, to tell a double click
		pressTime time.Duration
		pressPos  f32.Point
		// scrolling changes the Q; the scrolling of half a second is one
		// step of the undo history, and scrollRest what was too little
		scrollUntil time.Time
		scrolling   bool

		curves eqCurves
		// geometry is the plot as it was last laid out
		geometry eqGeometry
	}

	// eqDrag is the drag of a handle: the band as it was when the drag
	// began, and how far the pointer has moved since, less where Shift was
	// held; q is the part of the vertical movement that changes the Q.
	eqDrag struct {
		active bool
		id     pointer.ID
		band   int
		start  sointu.EQBand
		last   f32.Point
		moved  f32.Point
		q      float32
	}

	// eqGeometry maps frequencies and levels to the pixels of a plot of the
	// given size.
	eqGeometry struct{ w, h float32 }

	// eqCurves are the curves of the plot, as levels in dB every eqCurveStep
	// pixels: of all the units, and of each band that has units.
	eqCurves struct {
		version, width int
		sum            []float32
		bands          [][]float32
	}
)

func NewEQEditor() *EQEditor {
	ret := &EQEditor{
		typeBtn:     new(Clickable),
		typeMenu:    new(MenuState),
		onBtn:       new(Clickable),
		addBtn:      new(Clickable),
		deleteBtn:   new(Clickable),
		spectrumBtn: new(Clickable),
		freqEditor:  NewDraftEditor(text.End),
		gainEditor:  NewDraftEditor(text.End),
		qEditor:     NewDraftEditor(text.End),
		hover:       -1,
		spectrumOn:  true,
	}
	ret.showSpectrum = tracker.MakeBoolFromPtr(&ret.spectrumOn)
	return ret
}

func (e *EQEditor) Tags(level int, yield TagYieldFunc) bool {
	return yield(level, e) && yield(level+1, &e.freqEditor.widgetEditor) && yield(level+1, &e.gainEditor.widgetEditor) &&
		yield(level+1, &e.qEditor.widgetEditor) && e.typeMenu.Tags(level+1, yield)
}

// X returns the pixel column of a frequency, and Freq the frequency of one.
func (g eqGeometry) X(freq float64) float32 {
	return float32(math.Log(freq/eqPlotMinHz)/math.Log(eqPlotMaxHz/eqPlotMinHz)) * (g.w - 1)
}

func (g eqGeometry) Freq(x float32) float64 {
	return eqPlotMinHz * math.Pow(eqPlotMaxHz/eqPlotMinHz, float64(x/(g.w-1)))
}

// Y returns the pixel row of a level in dB, and Db the level of one.
func (g eqGeometry) Y(db float64) float32 {
	return float32((eqPlotDb-db)/(2*eqPlotDb)) * (g.h - 1)
}

func (g eqGeometry) Db(y float32) float64 {
	return eqPlotDb - float64(y/(g.h-1))*2*eqPlotDb
}

// Handle returns where the handle of a band is: at its frequency and its
// gain, or for a band without gain, at 0 dB; inside the plot.
func (g eqGeometry) Handle(b sointu.EQBand) f32.Point {
	gain := 0.0
	if sointu.EQHasGain(b.Type) {
		gain = b.Gain
	}
	return f32.Pt(min(max(g.X(b.Frequency), 0), g.w-1), min(max(g.Y(gain), 0), g.h-1))
}

// eqHit returns the handle that a pointer at p takes: the nearest one within
// the radius, the selected one if it is as near as another, or -1.
func eqHit(handles []f32.Point, p f32.Point, radius float32, selected int) int {
	best, bestDist := -1, radius*radius
	for i, h := range handles {
		d := h.Sub(p)
		if dist := d.X*d.X + d.Y*d.Y; dist < bestDist || dist == bestDist && i == selected && dist <= radius*radius {
			best, bestDist = i, dist
		}
	}
	return best
}

// eqDragged returns the band that a drag of its handle gives: start, moved
// by the pixels of moved. Moving sideways changes the frequency, moving up
// and down the gain, or the Q of a band without gain; q is vertical
// movement that changes the Q of any band, doubling it every qPx pixels up.
func eqDragged(g eqGeometry, start sointu.EQBand, moved f32.Point, q, qPx float32) sointu.EQBand {
	b := start
	if moved.X != 0 {
		b.Frequency = min(max(g.Freq(g.X(start.Frequency)+moved.X), sointu.EQMinFrequency), sointu.EQMaxFrequency)
	}
	if !sointu.EQHasGain(b.Type) {
		q += moved.Y
	} else if moved.Y != 0 {
		b.Gain = min(max(g.Db(g.Y(start.Gain)+moved.Y), -sointu.EQMaxGain), sointu.EQMaxGain)
	}
	if q != 0 {
		b.Q = min(max(start.Q*math.Pow(2, float64(-q/qPx)), sointu.EQMinQ), sointu.EQMaxQ)
	}
	return b
}

// update computes the curves anew if the eq, or the width of the plot,
// changed.
func (c *eqCurves) update(compiled *sointu.EQCompiled, version, width int) {
	if c.version == version && c.width == width {
		return
	}
	c.version, c.width = version, width
	n := (width+eqCurveStep-1)/eqCurveStep + 1
	g := eqGeometry{w: float32(width), h: 1}
	level := func(dst []float32, response func(float64) complex128) []float32 {
		dst = dst[:0]
		for i := range n {
			r := response(g.Freq(float32(i * eqCurveStep)))
			dst = append(dst, float32(10*math.Log10(real(r)*real(r)+imag(r)*imag(r)+1e-30)))
		}
		return dst
	}
	c.sum = level(c.sum, compiled.Response)
	for len(c.bands) < len(compiled.Bands) {
		c.bands = append(c.bands, nil)
	}
	c.bands = c.bands[:len(compiled.Bands)]
	for i := range compiled.Bands {
		if len(compiled.Bands[i].Units) == 0 {
			c.bands[i] = c.bands[i][:0]
			continue
		}
		c.bands[i] = level(c.bands[i], compiled.Bands[i].Response)
	}
}

// Layout lays out the plot and, under it, the values of the selected band.
func (e *EQEditor) Layout(gtx C) D {
	t := TrackerFromContext(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			paint.FillShape(gtx.Ops, t.Theme.UnitEditor.Divider, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, 1)}.Op())
			return D{Size: image.Pt(gtx.Constraints.Max.X, 1)}
		}),
		layout.Flexed(1, func(gtx C) D {
			return layout.Inset{Left: 6, Right: 6, Top: 4}.Layout(gtx, e.layoutPlot)
		}),
		layout.Rigid(e.layoutBand),
	)
}

func (e *EQEditor) handles(t *Tracker, g eqGeometry, compiled *sointu.EQCompiled) []f32.Point {
	handles := make([]f32.Point, len(compiled.Bands))
	for i := range compiled.Bands {
		handles[i] = g.Handle(compiled.Bands[i].Actual)
	}
	return handles
}

// endScroll ends the step of the undo history that scrolling made.
func (e *EQEditor) endScroll(t *Tracker) {
	if e.scrolling {
		e.scrolling = false
		t.EQ().EndGesture()
	}
}

func (e *EQEditor) update(gtx C, t *Tracker, g eqGeometry, compiled *sointu.EQCompiled) {
	eq := t.EQ()
	if e.scrolling && gtx.Now.After(e.scrollUntil) {
		e.endScroll(t)
	}
	hit := float32(gtx.Dp(eqHitDp))
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target:  e,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Enter | pointer.Leave | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6},
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		// the handles as they are now: an event before may have changed them
		if c, _, ok := eq.Compiled(); ok {
			compiled = c
		}
		handles := e.handles(t, g, compiled)
		switch pe.Kind {
		case pointer.Enter, pointer.Move:
			e.hovered = true
			e.hover = eqHit(handles, pe.Position, hit, eq.Selected())
		case pointer.Leave:
			e.hovered, e.hover = false, -1
		case pointer.Scroll:
			band := eqHit(handles, pe.Position, hit, eq.Selected())
			if band < 0 {
				band = eq.Selected()
			}
			steps := -pe.Scroll.Y / float32(gtx.Dp(eqScrollDp))
			if pe.Notch {
				steps = float32(math.Copysign(1, float64(-pe.Scroll.Y)))
			}
			if band < 0 || steps == 0 {
				break
			}
			if !e.scrolling {
				e.scrolling = true
				eq.BeginGesture()
			}
			e.scrollUntil = gtx.Now.Add(500 * time.Millisecond)
			eq.SetSelected(band)
			eq.Step(band, 0, 0, float64(steps), pe.Modifiers.Contain(key.ModShift))
		case pointer.Press:
			e.endScroll(t)
			gtx.Execute(key.FocusCmd{Tag: e})
			band := eqHit(handles, pe.Position, hit, eq.Selected())
			if pe.Buttons.Contain(pointer.ButtonSecondary) {
				if band >= 0 { // switches the band on or off
					b, _ := eq.Band(band)
					eq.SetSelected(band)
					eq.SetOn(band, b.Disabled)
				}
				break
			}
			if !pe.Buttons.Contain(pointer.ButtonPrimary) {
				break
			}
			d := pe.Position.Sub(e.pressPos)
			double := pe.Time-e.pressTime < doubleClickTime && d.X*d.X+d.Y*d.Y < doubleClickSlopP*doubleClickSlopP
			e.pressTime, e.pressPos = pe.Time, pe.Position
			if double {
				e.pressTime = 0
				if band >= 0 { // on a handle: removes the band
					eq.Delete(band)
					e.hover = -1
					break
				}
				// on empty space: adds a band there, which the drag then moves
				band = eq.Add(eq.DefaultBand(g.Freq(pe.Position.X), g.Db(pe.Position.Y)))
			}
			if band < 0 {
				break
			}
			eq.SetSelected(band)
			start, _ := eq.Band(band)
			e.drag = eqDrag{active: true, id: pe.PointerID, band: band, start: start, last: pe.Position}
			eq.BeginGesture()
		case pointer.Drag:
			if !e.drag.active || pe.PointerID != e.drag.id {
				break
			}
			d := pe.Position.Sub(e.drag.last)
			e.drag.last = pe.Position
			if pe.Modifiers.Contain(key.ModShift) {
				d = d.Mul(eqFineDrag)
			}
			if pe.Modifiers.Contain(key.ModAlt) { // the Q, and nothing else
				e.drag.q += d.Y
			} else {
				e.drag.moved = e.drag.moved.Add(d)
			}
			eq.Set(e.drag.band, eqDragged(g, e.drag.start, e.drag.moved, e.drag.q, float32(gtx.Dp(eqQDragDp))))
		case pointer.Release, pointer.Cancel:
			if e.drag.active {
				e.drag.active = false
				eq.EndGesture()
			}
		}
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: e},
			key.Filter{Focus: e, Name: key.NameLeftArrow, Optional: key.ModShift | key.ModAlt | key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameRightArrow, Optional: key.ModShift | key.ModAlt | key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameUpArrow, Optional: key.ModShift | key.ModAlt | key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameDownArrow, Optional: key.ModShift | key.ModAlt | key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameReturn, Optional: key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameEnter, Optional: key.ModShortcut},
			key.Filter{Focus: e, Name: key.NameDeleteBackward},
			key.Filter{Focus: e, Name: key.NameDeleteForward},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		e.endScroll(t)
		e.keyPressed(t, ke)
	}
}

// keyPressed handles a key pressed while the plot has the focus:
//
//   - Left and Right select the band before and after the selected one
//   - Shift+Left and Shift+Right change its frequency, Up and Down its gain
//     (with or without Shift), Alt+Up and Alt+Down its Q, and
//     Alt+Left and Alt+Right its type
//   - with Ctrl or Cmd, the steps are a quarter as large
//   - Enter adds a band, Ctrl/Cmd+Enter switches the selected band on or
//     off, Delete and Backspace delete it
func (e *EQEditor) keyPressed(t *Tracker, ke key.Event) {
	eq := t.EQ()
	sel := eq.Selected()
	shift, alt, fine := ke.Modifiers.Contain(key.ModShift), ke.Modifiers.Contain(key.ModAlt), ke.Modifiers.Contain(key.ModShortcut)
	dir := 1.0
	if ke.Name == key.NameLeftArrow || ke.Name == key.NameDownArrow {
		dir = -1
	}
	switch ke.Name {
	case key.NameLeftArrow, key.NameRightArrow:
		switch {
		case alt:
			eq.Type().Add(int(dir))
		case shift:
			eq.Step(sel, dir, 0, 0, fine)
		default:
			eq.SetSelected(sel + int(dir))
		}
	case key.NameUpArrow, key.NameDownArrow:
		if alt {
			eq.Step(sel, 0, 0, dir, fine)
		} else {
			eq.Step(sel, 0, dir, 0, fine)
		}
	case key.NameReturn, key.NameEnter:
		if fine {
			eq.On().Toggle()
		} else {
			eq.AddBand().Do()
		}
	case key.NameDeleteBackward, key.NameDeleteForward:
		eq.DeleteBand().Do()
	}
}

var eqFreqTicks = [...]struct {
	freq  float64
	label string
}{
	{20, "20"}, {30, ""}, {40, ""}, {50, "50"}, {60, ""}, {70, ""}, {80, ""}, {90, ""},
	{100, "100"}, {200, "200"}, {300, ""}, {400, ""}, {500, "500"}, {600, ""}, {700, ""}, {800, ""}, {900, ""},
	{1000, "1k"}, {2000, "2k"}, {3000, ""}, {4000, ""}, {5000, "5k"}, {6000, ""}, {7000, ""}, {8000, ""}, {9000, ""},
	{10000, "10k"}, {20000, ""},
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = uint8(int(c.A) * int(a) / 255)
	return c
}

func (e *EQEditor) layoutPlot(gtx C) D {
	t := TrackerFromContext(gtx)
	eq := t.EQ()
	s := gtx.Constraints.Max
	compiled, version, ok := eq.Compiled()
	if !ok || s.X < 8 || s.Y < 8 {
		return D{Size: s}
	}
	g := eqGeometry{w: float32(s.X), h: float32(s.Y)}
	e.geometry = g
	e.update(gtx, t, g, compiled)
	if compiled, version, ok = eq.Compiled(); !ok { // as the events left it
		return D{Size: s}
	}
	if e.scrolling { // to end the step of the undo history
		gtx.Execute(op.InvalidateCmd{At: e.scrollUntil.Add(time.Millisecond)})
	}
	defer clip.UniformRRect(image.Rectangle{Max: s}, gtx.Dp(4)).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, e)
	paint.Fill(gtx.Ops, t.Theme.UnitEditor.Preview)
	style := &t.Theme.Plot
	sumColor, bandColor, offColor := style.CurveColors[0], style.CurveColors[1], style.CurveColors[2]

	// the grid
	for _, tick := range eqFreqTicks {
		x := int(g.X(tick.freq) + 0.5)
		paint.FillShape(gtx.Ops, style.LimitColor, clip.Rect{Min: image.Pt(x, 0), Max: image.Pt(x+1, s.Y)}.Op())
		if tick.label != "" && x+gtx.Dp(24) < s.X {
			o := op.Offset(image.Pt(x+gtx.Dp(3), s.Y-gtx.Dp(16))).Push(gtx.Ops)
			Label(t.Theme, &style.Ticks, tick.label).Layout(gtx)
			o.Pop()
		}
	}
	for db := -eqPlotDb + 6; db < eqPlotDb; db += 6 {
		y := int(g.Y(float64(db)) + 0.5)
		c := style.LimitColor
		if db == 0 {
			c = withAlpha(style.Ticks.Color, 90)
		}
		paint.FillShape(gtx.Ops, c, clip.Rect{Min: image.Pt(0, y), Max: image.Pt(s.X, y+1)}.Op())
		label := strconv.Itoa(db)
		if db > 0 {
			label = "+" + label
		}
		o := op.Offset(image.Pt(gtx.Dp(3), y-gtx.Dp(15))).Push(gtx.Ops)
		Label(t.Theme, &style.Ticks, label).Layout(gtx)
		o.Pop()
	}

	// the spectrum of the master, behind the curves
	if e.spectrumOn {
		e.drawSpectrum(gtx, g, eq.Spectrum(), withAlpha(offColor, 80))
	}

	// the curve of each band, and of all the units
	e.curves.update(compiled, version, s.X)
	sel := eq.Selected()
	zero := g.Y(0)
	for i, levels := range e.curves.bands {
		if len(levels) == 0 {
			continue
		}
		if i == sel {
			drawEQArea(gtx, g, levels, zero, withAlpha(bandColor, 40))
			drawEQCurve(gtx, g, levels, float32(gtx.Dp(1)), bandColor)
		} else {
			drawEQCurve(gtx, g, levels, float32(gtx.Dp(1)), withAlpha(bandColor, 110))
		}
	}
	drawEQCurve(gtx, g, e.curves.sum, float32(gtx.Dp(2)), sumColor)

	// the handles
	handles := e.handles(t, g, compiled)
	r := gtx.Dp(eqHandleDp)
	numbers := style.Ticks
	numbers.Alignment = text.Middle
	for i, h := range handles {
		c := bandColor
		switch {
		case compiled.Bands[i].Actual.Disabled:
			c = offColor
		case len(compiled.Bands[i].Units) == 0:
			c = withAlpha(bandColor, 150) // does nothing
		}
		p := image.Pt(int(h.X+0.5), int(h.Y+0.5))
		circle := func(r int, c color.NRGBA) {
			paint.FillShape(gtx.Ops, c, clip.Ellipse{Min: p.Sub(image.Pt(r, r)), Max: p.Add(image.Pt(r, r))}.Op(gtx.Ops))
		}
		if i == sel {
			ring := t.Theme.Material.Fg
			if !gtx.Focused(e) {
				ring = withAlpha(ring, 150)
			}
			circle(r+gtx.Dp(2), ring)
		} else if i == e.hover {
			circle(r+gtx.Dp(2), withAlpha(t.Theme.Material.Fg, 80))
		}
		circle(r, t.Theme.Material.Bg)
		circle(r, c)
		numbers.Color = t.Theme.Material.ContrastFg
		o := op.Offset(p.Sub(image.Pt(r, gtx.Dp(8)))).Push(gtx.Ops)
		ngtx := gtx
		ngtx.Constraints = layout.Exact(image.Pt(2*r, gtx.Dp(16)))
		Label(t.Theme, &numbers, strconv.Itoa(i+1)).Layout(ngtx)
		o.Pop()
	}

	// how to use it
	if e.hovered || gtx.Focused(e) {
		hint := "double-click: add, remove · drag: frequency, gain · alt+drag, scroll: Q · shift: fine · right-click: on/off"
		if len(handles) == 0 {
			hint = "double-click to add a band"
		}
		o := op.Offset(image.Pt(gtx.Dp(30), gtx.Dp(2))).Push(gtx.Ops)
		Label(t.Theme, &style.Ticks, hint).Layout(gtx)
		o.Pop()
	}
	return D{Size: s}
}

// eqCurvePath makes the path of a curve from its levels every eqCurveStep
// pixels, within the plot.
func eqCurvePath(gtx C, g eqGeometry, levels []float32) (path clip.Path, first, last f32.Point) {
	path.Begin(gtx.Ops)
	for i, db := range levels {
		p := f32.Pt(float32(i*eqCurveStep), min(max(g.Y(float64(db)), -4), g.h+4))
		if i == 0 {
			path.MoveTo(p)
			first = p
		} else {
			path.LineTo(p)
		}
		last = p
	}
	return path, first, last
}

func drawEQCurve(gtx C, g eqGeometry, levels []float32, width float32, c color.NRGBA) {
	if len(levels) < 2 {
		return
	}
	path, _, _ := eqCurvePath(gtx, g, levels)
	paint.FillShape(gtx.Ops, c, clip.Stroke{Path: path.End(), Width: width}.Op())
}

// drawEQArea fills the area between a curve and the row y.
func drawEQArea(gtx C, g eqGeometry, levels []float32, y float32, c color.NRGBA) {
	if len(levels) < 2 {
		return
	}
	path, first, last := eqCurvePath(gtx, g, levels)
	path.LineTo(f32.Pt(last.X, y))
	path.LineTo(f32.Pt(first.X, y))
	path.Close()
	paint.FillShape(gtx.Ops, c, clip.Outline{Path: path.End()}.Op())
}

// The levels of the spectrum of the master at the top and the bottom of the
// plot.
const (
	eqSpectrumDbMax = 0
	eqSpectrumDbMin = -90
)

// drawSpectrum fills the area under the spectrum of the master: of the
// louder of its channels, the bins of each pixel column at their highest.
func (e *EQEditor) drawSpectrum(gtx C, g eqGeometry, spectrum tracker.Spectrum, c color.NRGBA) {
	n := len(spectrum[0])
	if n < 2 {
		return
	}
	level := func(bin int) float32 {
		l := spectrum[0][bin]
		if len(spectrum[1]) == n {
			l = max(l, spectrum[1][bin])
		}
		return l
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(0, g.h))
	// bin i is at (i+1)/n of half the sample rate
	pos := func(x float32) float64 { return g.Freq(x)/22050*float64(n) - 1 }
	for x := 0; x < int(g.w)+eqCurveStep; x += eqCurveStep {
		a, b := pos(float32(x)-eqCurveStep/2), pos(float32(x)+eqCurveStep/2)
		var l float32
		if lo, hi := int(math.Ceil(a)), int(math.Floor(b)); hi >= lo {
			lo, hi = min(max(lo, 0), n-1), min(max(hi, 0), n-1)
			l = level(lo)
			for i := lo + 1; i <= hi; i++ {
				l = max(l, level(i))
			}
		} else { // between two bins
			m := min(max((a+b)/2, 0), float64(n-1))
			i := min(int(m), n-2)
			l = smoothInterpolate(level(i), level(i+1), float32(m-float64(i)))
		}
		y := (eqSpectrumDbMax - l) / (eqSpectrumDbMax - eqSpectrumDbMin) * (g.h - 1)
		path.LineTo(f32.Pt(float32(x), min(max(y, 0), g.h)))
	}
	path.LineTo(f32.Pt(g.w+eqCurveStep, g.h))
	path.Close()
	paint.FillShape(gtx.Ops, c, clip.Outline{Path: path.End()}.Op())
}

// layoutBand lays out the row under the plot: the selected band, its type,
// whether it is on, its values as numbers, what it is compiled to, and the
// number of units of the eq.
func (e *EQEditor) layoutBand(gtx C) D {
	t := TrackerFromContext(gtx)
	eq := t.EQ()
	sel := eq.Selected()
	band, has := eq.Band(sel)
	header := &t.Theme.SongPanel.RowHeader
	field := func(ed *DraftEditor, value tracker.String, width unit.Dp, hint, unitText string) layout.Widget {
		return func(gtx C) D {
			if !has || value.Value() == "" && !gtx.Focused(&ed.widgetEditor) && unitText == "dB" {
				return D{}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					w, h := gtx.Dp(width), gtx.Dp(t.Theme.NumericUpDown.Height)
					gtx.Constraints = layout.Exact(image.Pt(w, h))
					paint.FillShape(gtx.Ops, t.Theme.NumericUpDown.BgColor, clip.UniformRRect(image.Rectangle{Max: image.Pt(w, h)}, gtx.Dp(t.Theme.NumericUpDown.CornerRadius)).Op(gtx.Ops))
					return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Min.Y = 0
						return layout.Center.Layout(gtx, func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return ed.Layout(gtx, value, t.Theme, &t.Theme.InstrumentEditor.UnitComment, hint)
						})
					})
				}),
				layout.Rigid(layout.Spacer{Width: 3}.Layout),
				layout.Rigid(Label(t.Theme, header, unitText).Layout),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
			)
		}
	}
	typeBtn := func(gtx C) D {
		if !has {
			return D{}
		}
		btn := MenuBtn(e.typeMenu, e.typeBtn, fmt.Sprintf("%d  %s", sel+1, tracker.EQTypeName(band.Type))).
			WithBtnStyle(&t.Theme.Button.Text).WithPopupStyle(&t.Theme.Popup.ContextMenu).WithTip("The type of the selected band")
		return btn.Layout(gtx, IntMenuChild(eq.Type(), icons.NavigationCheck))
	}
	onBtn := ToggleIconBtn(eq.On(), t.Theme, e.onBtn, icons.AVVolumeOff, icons.AVVolumeUp, "Switch the band on", "Switch the band off")
	spectrumBtn := ToggleIconBtn(e.showSpectrum, t.Theme, e.spectrumBtn, icons.ImageBlurOff, icons.ImageBlurOn, "Show the spectrum of the master\nbehind the curve", "Hide the spectrum of the master")
	addBtn := ActionIconBtn(eq.AddBand(), t.Theme, e.addBtn, icons.ContentAdd, "Add a band (Enter)")
	deleteBtn := ActionIconBtn(eq.DeleteBand(), t.Theme, e.deleteBtn, icons.ActionDelete, "Delete the band (Delete)")
	info := func(gtx C) D {
		text := eq.Info()
		if text != "" {
			text = "→ " + text
		}
		l := Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, text)
		return l.Layout(gtx)
	}
	total := func(gtx C) D {
		units, gain, hasGain := eq.Units()
		text := fmt.Sprintf("eq: %d units", units)
		if units == 1 {
			text = "eq: 1 unit"
		}
		if hasGain {
			text += fmt.Sprintf(", gain %+.1f dB", gain)
		}
		return layout.Inset{Left: 8, Right: 4}.Layout(gtx, Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, text).Layout)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Width: 6, Height: 36}.Layout),
		layout.Rigid(typeBtn),
		layout.Rigid(func(gtx C) D {
			if !has {
				return D{}
			}
			return onBtn.Layout(gtx)
		}),
		layout.Rigid(field(e.freqEditor, eq.Frequency(), 64, "Hz", "Hz")),
		layout.Rigid(field(e.gainEditor, eq.Gain(), 48, "dB", "dB")),
		layout.Rigid(field(e.qEditor, eq.Q(), 48, "Q", "Q")),
		layout.Flexed(1, info),
		layout.Rigid(total),
		layout.Rigid(spectrumBtn.Layout),
		layout.Rigid(addBtn.Layout),
		layout.Rigid(deleteBtn.Layout),
	)
}
