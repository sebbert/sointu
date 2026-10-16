package gioui

import (
	"image"
	"image/color"
	"math"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/vsariola/sointu/tracker"
)

// layoutUnitPreview draws, at the right edge of the row of unit y, a small view of
// what its buffer holds: the waveform of an audio buffer, with its write head
// and the notes playing it, or a spectrum as the spectral unit left it.
// Clicking it shows the buffer in the Buffers tab.
func (pe *InstrumentEditor) layoutUnitPreview(gtx C, y, id int, spectrum bool) {
	t := TrackerFromContext(gtx)
	for len(pe.previews) <= y {
		pe.previews = append(pe.previews, Clickable{})
	}
	click := &pe.previews[y]
	for click.Clicked(gtx) {
		t.Buffer().Show(id).Do()
	}
	inset := gtx.Dp(4)
	width := tracker.UnitPreviewCells * gtx.Dp(t.Theme.UnitEditor.Width)
	s := image.Pt(width, gtx.Constraints.Max.Y).Sub(image.Pt(2*inset, 2*inset))
	if s.X <= 1 || s.Y <= 1 {
		return
	}
	defer op.Offset(image.Pt(gtx.Constraints.Max.X-width+inset, inset)).Push(gtx.Ops).Pop()
	defer clip.UniformRRect(image.Rectangle{Max: s}, gtx.Dp(4)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, t.Theme.UnitEditor.Preview)
	style := t.Theme.Plot
	line := func(x float32, c paint.ColorOp) {
		if sx := plotPx(s.X).toScreen(plotRel(x)); sx >= 0 && sx < s.X {
			c.Add(gtx.Ops)
			fillRect(gtx, clip.Rect{Min: image.Pt(sx, 0), Max: image.Pt(sx+1, s.Y)})
		}
	}
	if spectrum {
		mags, n := t.Unit().Spectrum(y)
		if n == 0 { // the unit has not processed a spectrum yet
			mags, n = t.Buffer().SpectrumOf(id)
		}
		data, channels := spectrumData(mags, n)
		drawPlotCurves(gtx, style.CurveColors, s, plotRange{-3.8, 0}, plotRange{bufferSpectrumDbMax, bufferSpectrumDbMin}, data, channels)
	} else {
		audio, head, filled := t.Buffer().WaveformOf(id)
		frames := audio.Frames()
		data, peak := waveformData(audio, head, filled)
		drawPlotCurves(gtx, style.CurveColors, s, plotRange{0, 1}, plotRange{-peak * 1.05, peak * 1.05}, data, 3)
		if frames > 0 {
			t.Buffer().PlayheadsOf(id, func(frame, released int) {
				// released notes fade out, as it is not known when they fall silent
				if alpha := 1 - float32(released)/releasedPlayheadFade; alpha > 0 {
					c := style.MarkerColor
					c.A = uint8(float32(c.A) * alpha)
					line(float32(frame)/float32(frames), paint.ColorOp{Color: c})
				}
			})
			if audio.Writable {
				line(float32(head)/float32(frames), paint.ColorOp{Color: style.CursorColor})
			}
		}
	}
	gtx.Constraints.Min, gtx.Constraints.Max = s, s
	click.Layout(gtx, func(gtx C) D { return D{Size: s} })
}

// busLevelDbMin is the level at the bottom of the level meters of buses.
const busLevelDbMin = -60

// layoutBusPreview draws, at the right edge of the row of unit y, an mc
// unit, a level meter for each channel of its bus right after the unit: the
// peak since the last report of the player, from -60 dB to 0 dB, the even
// channels (summed to the left) in the color of the left channel and the odd
// ones in the color of the right one. Clicking it shows the bus in the
// Buffers tab.
func (pe *InstrumentEditor) layoutBusPreview(gtx C, y, id int) {
	t := TrackerFromContext(gtx)
	for len(pe.previews) <= y {
		pe.previews = append(pe.previews, Clickable{})
	}
	click := &pe.previews[y]
	for click.Clicked(gtx) {
		t.Buffer().Show(id).Do()
	}
	inset := gtx.Dp(4)
	width := tracker.UnitPreviewCells * gtx.Dp(t.Theme.UnitEditor.Width)
	s := image.Pt(width, gtx.Constraints.Max.Y).Sub(image.Pt(2*inset, 2*inset))
	if s.X <= 1 || s.Y <= 1 {
		return
	}
	defer op.Offset(image.Pt(gtx.Constraints.Max.X-width+inset, inset)).Push(gtx.Ops).Pop()
	defer clip.UniformRRect(image.Rectangle{Max: s}, gtx.Dp(4)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, t.Theme.UnitEditor.Preview)
	levels := t.Unit().Levels(y)
	for c, l := range levels {
		db := 20 * math.Log10(float64(l))
		h := int(float64(s.Y) * min(max((db-busLevelDbMin)/-busLevelDbMin, 0), 1))
		x0, x1 := c*s.X/len(levels), (c+1)*s.X/len(levels)
		if x1-x0 > 2 {
			x1-- // a gap between the bars
		}
		paint.ColorOp{Color: t.Theme.Plot.CurveColors[c&1]}.Add(gtx.Ops)
		fillRect(gtx, clip.Rect{Min: image.Pt(x0, s.Y-h), Max: image.Pt(x1, s.Y)})
	}
	gtx.Constraints.Min, gtx.Constraints.Max = s, s
	click.Layout(gtx, func(gtx C) D { return D{Size: s} })
}

// cycleView is the plot of the cycle of an oscillator unit in its row of the
// rack, recorded once, and again only when the cycle or its size changes.
type cycleView struct {
	ops   op.Ops
	call  op.CallOp
	cycle *float32 // the first point of the cycle drawn: the model keeps a cycle until the parameters change
	size  image.Point
	color color.NRGBA
}

// layoutOscillatorCycle draws, after the parameters of the oscillator unit on
// row y, one cycle of its waveform (see tracker.UnitModel.OscillatorCycle),
// from -1 to 1 or the peak of the cycle, with a line at 0.
func (pe *InstrumentEditor) layoutOscillatorCycle(gtx C, y int, cycle []float32) {
	t := TrackerFromContext(gtx)
	pe.keepCursorOnParams(gtx, y)
	cellWidth := gtx.Dp(t.Theme.UnitEditor.Width)
	inset := gtx.Dp(4)
	colP := pe.paramTable.ColTitleList.List.Position
	x := (t.Params().RowWidth(y)-colP.First)*cellWidth - colP.Offset
	s := image.Pt(tracker.OscillatorInlineCells*cellWidth-2*inset, gtx.Constraints.Max.Y-2*inset)
	if s.X <= 8 || s.Y <= 8 {
		return
	}
	for len(pe.cycles) <= y {
		pe.cycles = append(pe.cycles, &cycleView{})
	}
	v := pe.cycles[y]
	style := t.Theme.Plot
	if v.cycle != &cycle[0] || v.size != s || v.color != style.CurveColors[0] {
		v.cycle, v.size, v.color = &cycle[0], s, style.CurveColors[0]
		v.ops.Reset()
		rec := op.Record(&v.ops)
		drawCycle(gtx, &v.ops, s, cycle, t.Theme)
		v.call = rec.Stop()
	}
	defer op.Offset(image.Pt(x+inset, inset)).Push(gtx.Ops).Pop()
	v.call.Add(gtx.Ops)
}

// drawCycle draws a cycle of an oscillator into ops, over an area of size s.
func drawCycle(gtx C, ops *op.Ops, s image.Point, cycle []float32, th *Theme) {
	gtx.Ops = ops
	defer clip.UniformRRect(image.Rectangle{Max: s}, gtx.Dp(4)).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, th.UnitEditor.Preview)
	peak := float32(1)
	for _, v := range cycle {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	peak *= 1.05
	paint.ColorOp{Color: th.Plot.LimitColor}.Add(gtx.Ops)
	fillRect(gtx, clip.Rect{Min: image.Pt(0, s.Y/2), Max: image.Pt(s.X, s.Y/2+1)})
	drawPlotCurves(gtx, th.Plot.CurveColors, s, plotRange{0, 1}, plotRange{-peak, peak}, cycleData(cycle), 1)
}

// cycleData returns the points of a cycle as plot data from 0 to 1, each
// range reaching to the point after it, so that the curve is connected.
func cycleData(cycle []float32) PlotDataFunc {
	n := len(cycle)
	return func(chn int, xr plotRange) (plotRange, bool) {
		i1 := max(int(xr.a*float32(n)), 0)
		i2 := min(int(xr.b*float32(n))+1, n-1)
		if chn != 0 || i1 > i2 {
			return plotRange{}, false
		}
		lo, hi := cycle[i1], cycle[i1]
		for _, v := range cycle[i1 : i2+1] {
			lo, hi = min(lo, v), max(hi, v)
		}
		return plotRange{-hi, -lo}, true
	}
}
