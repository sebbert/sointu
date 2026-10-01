package gioui

import (
	"image"
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
func (pe *InstrumentEditor) layoutUnitPreview(gtx C, r rackView, y, id int, spectrum bool) {
	t := TrackerFromContext(gtx)
	for len(*r.previews) <= y {
		*r.previews = append(*r.previews, Clickable{})
	}
	click := &(*r.previews)[y]
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
		mags, n := r.units.Spectrum(y)
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
func (pe *InstrumentEditor) layoutBusPreview(gtx C, r rackView, y, id int) {
	t := TrackerFromContext(gtx)
	for len(*r.previews) <= y {
		*r.previews = append(*r.previews, Clickable{})
	}
	click := &(*r.previews)[y]
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
	levels := r.units.Levels(y)
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
