package gioui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

type (
	Plot struct {
		origXlim, origYlim plotRange
		fixedYLevel        float32

		xScale, yScale float32
		xOffset        float32
		dragging       bool
		dragId         pointer.ID
		dragStartPoint f32.Point
		fixedY         bool // the y range cannot be zoomed
		hovered        bool
		lastPressTime  time.Duration
		lastPressPos   f32.Point
	}

	PlotStyle struct {
		CurveColors [3]color.NRGBA `yaml:",flow"`
		LimitColor  color.NRGBA    `yaml:",flow"`
		CursorColor color.NRGBA    `yaml:",flow"`
		Ticks       LabelStyle
		DpPerTick   unit.Dp
	}

	PlotDataFunc func(chn int, xr plotRange) (yr plotRange, ok bool)
	PlotTickFunc func(r plotRange, num int, yield func(pos float32, label string))
	plotRange    struct{ a, b float32 }
	plotRel      float32
	plotPx       int
	plotLogScale float32
)

func NewPlot(xlim, ylim plotRange, fixedYLevel float32) *Plot {
	return &Plot{
		origXlim:    xlim,
		origYlim:    ylim,
		fixedYLevel: fixedYLevel,
	}
}

func (p *Plot) Layout(gtx C, data PlotDataFunc, xticks, yticks PlotTickFunc, cursornx float32, numchns int) D {
	p.update(gtx)
	t := TrackerFromContext(gtx)
	style := t.Theme.Plot
	s := gtx.Constraints.Max
	if s.X <= 1 || s.Y <= 1 {
		return D{}
	}
	defer clip.Rect(image.Rectangle{Max: s}).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, p)

	xlim := p.xlim()
	ylim := p.ylim()

	// draw tick marks
	numxticks := s.X / gtx.Dp(style.DpPerTick)
	xticks(xlim, numxticks, func(x float32, txt string) {
		paint.ColorOp{Color: style.LimitColor}.Add(gtx.Ops)
		sx := plotPx(s.X).toScreen(xlim.toRelative(x))
		fillRect(gtx, clip.Rect{Min: image.Pt(sx, 0), Max: image.Pt(sx+1, s.Y)})
		defer op.Offset(image.Pt(sx, gtx.Dp(2))).Push(gtx.Ops).Pop()
		Label(t.Theme, &t.Theme.Plot.Ticks, txt).Layout(gtx)
	})

	numyticks := s.Y / gtx.Dp(style.DpPerTick)
	yticks(ylim, numyticks, func(y float32, txt string) {
		paint.ColorOp{Color: style.LimitColor}.Add(gtx.Ops)
		sy := plotPx(s.Y).toScreen(ylim.toRelative(y))
		fillRect(gtx, clip.Rect{Min: image.Pt(0, sy), Max: image.Pt(s.X, sy+1)})
		defer op.Offset(image.Pt(gtx.Dp(2), sy)).Push(gtx.Ops).Pop()
		Label(t.Theme, &t.Theme.Plot.Ticks, txt).Layout(gtx)
	})

	// draw cursor
	if cursornx == cursornx { // check for NaN
		paint.ColorOp{Color: style.CursorColor}.Add(gtx.Ops)
		csx := plotPx(s.X).toScreen(xlim.toRelative(cursornx))
		fillRect(gtx, clip.Rect{Min: image.Pt(csx, 0), Max: image.Pt(csx+1, s.Y)})
	}

	// how to use it, while hovered
	if p.hovered {
		hint := "alt+scroll: zoom · drag: pan · double-click: reset"
		if !p.fixedY {
			hint = "alt+scroll: zoom · drag: pan · alt+drag: zoom y · double-click: reset"
		}
		stack := op.Offset(image.Pt(gtx.Dp(2), s.Y-gtx.Dp(16))).Push(gtx.Ops)
		Label(t.Theme, &t.Theme.Plot.Ticks, hint).Layout(gtx)
		stack.Pop()
	}

	// draw curves
	for chn := range numchns {
		paint.ColorOp{Color: style.CurveColors[chn]}.Add(gtx.Ops)
		right := xlim.fromRelative(plotPx(s.X).fromScreen(0))
		for sx := range s.X {
			// left and right is the sample range covered by the pixel
			left := right
			right = xlim.fromRelative(plotPx(s.X).fromScreen(sx + 1))
			yr, ok := data(chn, plotRange{left, right})
			if !ok {
				continue
			}
			y1 := plotPx(s.Y).toScreen(ylim.toRelative(yr.a))
			y2 := plotPx(s.Y).toScreen(ylim.toRelative(yr.b))
			fillRect(gtx, clip.Rect{Min: image.Pt(sx, min(y1, y2)), Max: image.Pt(sx+1, max(y1, y2)+1)})
		}
	}
	return D{Size: s}
}

func (r plotRange) toRelative(f float32) plotRel    { return plotRel((f - r.a) / (r.b - r.a)) }
func (r plotRange) fromRelative(pr plotRel) float32 { return float32(pr)*(r.b-r.a) + r.a }
func (r plotRange) offset(o float32) plotRange      { return plotRange{r.a + o, r.b + o} }
func (r plotRange) scale(logScale float32) plotRange {
	s := float32(math.Exp(float64(logScale)))
	return plotRange{r.a * s, r.b * s}
}

func (s plotPx) toScreen(pr plotRel) int          { return int(float32(pr)*float32(s-1) + 0.5) }
func (s plotPx) fromScreen(px int) plotRel        { return plotRel(float32(px) / float32(s-1)) }
func (s plotPx) fromScreenF32(px float32) plotRel { return plotRel(px / float32(s-1)) }

func (o *Plot) xlim() plotRange { return o.origXlim.scale(o.xScale).offset(o.xOffset) }
func (o *Plot) ylim() plotRange {
	return o.origYlim.offset(-o.fixedYLevel).scale(o.yScale).offset(o.fixedYLevel)
}

func fillRect(gtx C, rect clip.Rect) {
	stack := rect.Push(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	stack.Pop()
}

// maxYZoomOut is how many times the original y range can be shown when
// zooming out; the x range cannot be zoomed out beyond the original range.
const maxYZoomOut = 4

// minXScale limits zooming in to about a millionth of the original x range.
const minXScale = -14

// clamp keeps the view within the zoom limits, and the x range within the
// original x range.
func (o *Plot) clamp() {
	o.xScale = min(max(o.xScale, minXScale), 0)
	o.yScale = min(max(o.yScale, -1e3), float32(math.Log(maxYZoomOut)))
	s := float32(math.Exp(float64(o.xScale)))
	lo := o.origXlim.a - o.origXlim.a*s
	hi := o.origXlim.b - o.origXlim.b*s
	o.xOffset = min(max(o.xOffset, min(lo, hi)), max(lo, hi))
}

// zoomPerPx is how much scrolling by a pixel zooms, as a change of the log
// scale; maxZoomPerEvent limits it for coarse mouse wheels.
const (
	zoomPerPx        = 0.004
	maxZoomPerEvent  = 0.3
	doubleClickTime  = 400 * time.Millisecond
	doubleClickSlopP = 8 // how far apart, in pixels, the clicks of a double click can be
)

// Reset shows the original ranges again.
func (o *Plot) Reset() { o.xScale, o.xOffset, o.yScale = 0, 0, 0 }

// SetYRange sets the y range, which the user then cannot zoom.
func (o *Plot) SetYRange(r plotRange) {
	o.origYlim, o.yScale, o.fixedY = r, 0, true
}

// zoomX zooms the x range by the log scale delta, keeping the point at the
// screen position px in place.
func (o *Plot) zoomX(delta float32, px float32, width int) {
	x1 := o.xlim().fromRelative(plotPx(width).fromScreenF32(px))
	o.xScale = min(max(o.xScale+delta, minXScale), 0)
	x2 := o.xlim().fromRelative(plotPx(width).fromScreenF32(px))
	o.xOffset += x1 - x2
}

func (o *Plot) update(gtx C) {
	defer o.clamp()
	t := TrackerFromContext(gtx)
	s := gtx.Constraints.Max
	for {
		// Plots are in vertically scrolled lists, so vertical scrolling zooms
		// only with Alt held (Cmd/Ctrl+scroll zooms the whole UI); otherwise
		// the list scrolls. Which handler gets a scroll is decided before
		// the event, so the tracker follows Alt from key and pointer events.
		// Horizontal scrolling pans, when zoomed in.
		var scrollX, scrollY pointer.ScrollRange
		if t.plotZoomModifier {
			scrollY = pointer.ScrollRange{Min: -1e6, Max: 1e6}
		}
		if o.xScale < 0 {
			scrollX = pointer.ScrollRange{Min: -1e6, Max: 1e6}
		}
		ev, ok := gtx.Event(pointer.Filter{
			Target:  o,
			Kinds:   pointer.Scroll | pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Enter | pointer.Leave,
			ScrollX: scrollX,
			ScrollY: scrollY,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		alt := e.Modifiers.Contain(key.ModAlt)
		switch e.Kind {
		case pointer.Enter, pointer.Move:
			o.hovered = true
			t.plotZoomModifier = alt
		case pointer.Leave:
			o.hovered = false
		case pointer.Scroll:
			t.plotZoomModifier = alt
			if e.Scroll.X != 0 && o.xScale < 0 {
				xl := o.xlim()
				o.xOffset += e.Scroll.X / float32(max(s.X, 1)) * (xl.b - xl.a)
			}
			if e.Scroll.Y != 0 && alt {
				o.zoomX(min(max(e.Scroll.Y*zoomPerPx, -maxZoomPerEvent), maxZoomPerEvent), e.Position.X, s.X)
			}
		case pointer.Press:
			if e.Buttons&pointer.ButtonSecondary != 0 {
				o.Reset()
			}
			if e.Buttons&pointer.ButtonPrimary != 0 {
				d := e.Position.Sub(o.lastPressPos)
				if e.Time-o.lastPressTime < doubleClickTime && d.X*d.X+d.Y*d.Y < doubleClickSlopP*doubleClickSlopP {
					o.Reset()
					o.lastPressTime = 0
					break
				}
				o.lastPressTime, o.lastPressPos = e.Time, e.Position
				o.dragging = true
				o.dragId = e.PointerID
				o.dragStartPoint = e.Position
			}
		case pointer.Drag:
			if e.Buttons&pointer.ButtonPrimary == 0 || !o.dragging || e.PointerID != o.dragId {
				break
			}
			// dragging pans; with Alt, dragging vertically zooms the y range
			x1 := o.xlim().fromRelative(plotPx(s.X).fromScreenF32(o.dragStartPoint.X))
			x2 := o.xlim().fromRelative(plotPx(s.X).fromScreenF32(e.Position.X))
			o.xOffset += x1 - x2
			if alt && !o.fixedY {
				num := o.ylim().fromRelative(plotPx(s.Y).fromScreenF32(e.Position.Y))
				den := o.ylim().fromRelative(plotPx(s.Y).fromScreenF32(o.dragStartPoint.Y))
				num -= o.fixedYLevel
				den -= o.fixedYLevel
				if l := math.Abs(float64(num / den)); l > 1e-3 && l < 1e3 {
					o.yScale -= float32(math.Log(l))
				}
			}
			o.dragStartPoint = e.Position
		case pointer.Release, pointer.Cancel:
			o.dragging = false
		}
	}
}
