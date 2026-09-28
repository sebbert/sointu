package gioui

import (
	"image"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/vsariola/sointu/tracker"
)

// layoutUnitPreview draws, after the parameters of unit y, a small view of
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
	size := image.Pt(tracker.UnitPreviewCells*gtx.Constraints.Max.X, gtx.Constraints.Max.Y)
	s := size.Sub(image.Pt(2*inset, 2*inset))
	if s.X <= 1 || s.Y <= 1 {
		return
	}
	defer op.Offset(image.Pt(inset, inset)).Push(gtx.Ops).Pop()
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
