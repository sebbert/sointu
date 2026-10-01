package gioui

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/vsariola/sointu/tracker"
)

const maxSignalsDrawn = 16

type (
	RailStyle struct {
		Color        color.NRGBA
		LineWidth    unit.Dp
		SignalWidth  unit.Dp
		PortDiameter unit.Dp
		PortColor    color.NRGBA
		// MinSignals is how many signals the rail of the rack has room for
		// at least: see RailLane
		MinSignals int
	}

	// RailLane is the width of the signal rails of the rack. It is
	// that of the deepest stack that the units being edited have had since
	// they were chosen, and at least Style.MinSignals signals, so that
	// parameters changing the signals on the stack do not move the rack
	// every time. When it does have to widen, it does so over
	// railLaneDuration.
	RailLane struct {
		key         any
		signals     int
		signalWidth int
		from, to    float32 // the widening, in pixels
		start       time.Time
	}

	RailWidget struct {
		Style  *RailStyle
		Signal tracker.Rail
		Height unit.Dp
		// FaintFrom, if not 0, draws the signals from the index
		// FaintFrom-1 half transparent, and the others as usual: for the
		// inner units of a module unit, whose own signals are faint
		FaintFrom int
	}
)

func Rail(th *Theme, signal tracker.Rail) RailWidget {
	return RailWidget{
		Style:  &th.SignalRail,
		Signal: signal,
		Height: th.UnitEditor.Height,
	}
}

func (s RailWidget) Layout(gtx C) D {
	sw := gtx.Dp(s.Style.SignalWidth)
	h := gtx.Dp(s.Height)
	if s.Signal.PassThrough == 0 && len(s.Signal.StackUse.Inputs) == 0 && s.Signal.StackUse.NumOutputs == 0 {
		return D{Size: image.Pt(sw, h)}
	}
	lineColor, portColor := s.Style.Color, s.Style.PortColor
	bright := s.Signal.PassThrough // the signals passing by that are not faint
	if s.FaintFrom > 0 {
		lineColor.A, portColor.A = uint8(int(lineColor.A)*2/5), uint8(int(portColor.A)*2/5)
		bright = min(bright, s.FaintFrom-1)
	} else {
		bright = 0 // all in one path
	}
	lw := gtx.Dp(s.Style.LineWidth)
	pd := gtx.Dp(s.Style.PortDiameter)
	center := sw / 2
	if bright > 0 {
		var by clip.Path
		by.Begin(gtx.Ops)
		for i := range min(maxSignalsDrawn, bright) {
			x := float32(i*sw + center)
			by.MoveTo(f32.Pt(x, 0))
			by.LineTo(f32.Pt(x, float32(h)))
		}
		paint.FillShape(gtx.Ops, s.Style.Color, clip.Stroke{Path: by.End(), Width: float32(lw)}.Op())
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	// Draw pass through signals
	for i := range min(maxSignalsDrawn, s.Signal.PassThrough) {
		if i < bright {
			continue
		}
		x := float32(i*sw + center)
		path.MoveTo(f32.Pt(x, 0))
		path.LineTo(f32.Pt(x, float32(h)))
	}
	// Draw the routing of input signals
	for i := range min(len(s.Signal.StackUse.Inputs), maxSignalsDrawn-s.Signal.PassThrough) {
		input := s.Signal.StackUse.Inputs[i]
		x1 := float32((i+s.Signal.PassThrough)*sw + center)
		for _, link := range input {
			x2 := float32((link+s.Signal.PassThrough)*sw + center)
			path.MoveTo(f32.Pt(x1, 0))
			path.LineTo(f32.Pt(x2, float32(h/2)))
		}
	}
	if s.Signal.Send {
		for i := range min(len(s.Signal.StackUse.Inputs), maxSignalsDrawn-s.Signal.PassThrough) {
			d := gtx.Dp(8)
			from := f32.Pt(float32((i+s.Signal.PassThrough)*sw+center), float32(h/2))
			to := f32.Pt(float32(gtx.Constraints.Max.X), float32(h)-float32(d))
			ctrl := f32.Pt(from.X, to.Y)
			path.MoveTo(from)
			path.QuadTo(ctrl, to)
		}
	}
	// Draw the routing of output signals
	for i := range min(s.Signal.StackUse.NumOutputs, maxSignalsDrawn-s.Signal.PassThrough) {
		x := float32((i+s.Signal.PassThrough)*sw + center)
		path.MoveTo(f32.Pt(x, float32(h/2)))
		path.LineTo(f32.Pt(x, float32(h)))
	}
	// Signal paths finished
	paint.FillShape(gtx.Ops, lineColor,
		clip.Stroke{
			Path:  path.End(),
			Width: float32(lw),
		}.Op())
	// Draw the circles on signals that get modified
	var circle clip.Path
	circle.Begin(gtx.Ops)
	for i := range min(len(s.Signal.StackUse.Modifies), maxSignalsDrawn-s.Signal.PassThrough) {
		if !s.Signal.StackUse.Modifies[i] {
			continue
		}
		f := f32.Pt(float32((i+s.Signal.PassThrough)*sw+center), float32(h/2))
		circle.MoveTo(f32.Pt(f.X-float32(pd/2), float32(h/2)))
		circle.ArcTo(f, f, float32(2*math.Pi))
	}
	p := clip.Outline{Path: circle.End()}.Op().Push(gtx.Ops)
	paint.ColorOp{Color: portColor}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	p.Pop()
	return D{Size: image.Pt(sw, h)}
}

const railLaneDuration = 150 * time.Millisecond

// Update returns the width of the lane in pixels at the time now, and
// whether it is still widening, for the units identified by the key, whose
// deepest stack has the given number of signals, each signalWidth pixels
// wide. With another key or signalWidth than the last time, the lane starts
// anew, at the width of the signals.
func (l *RailLane) Update(now time.Time, key any, signals, signalWidth int) (width int, widening bool) {
	if key != l.key || signalWidth != l.signalWidth {
		w := float32(signals * signalWidth)
		*l = RailLane{key: key, signals: signals, signalWidth: signalWidth, from: w, to: w}
		return signals * signalWidth, false
	}
	l.signals = max(l.signals, signals)
	if target := float32(l.signals * signalWidth); target != l.to {
		l.from, l.to, l.start = l.at(now), target, now
	}
	w := l.at(now)
	return int(w + 0.5), w != l.to
}

// at returns the width at the time now: it eases out from l.from to l.to.
func (l *RailLane) at(now time.Time) float32 {
	t := float32(now.Sub(l.start)) / float32(railLaneDuration)
	if l.from == l.to || t >= 1 {
		l.from = l.to
		return l.to
	}
	t = 1 - max(t, 0)
	return l.to + (l.from-l.to)*t*t*t
}
