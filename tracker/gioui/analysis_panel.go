package gioui

import (
	"image"
	"math"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// analysisPanelHeight is the height of the analysis panel under a unit of
// the rack.
const analysisPanelHeight = unit.Dp(200)

// AnalysisPanel shows, under a unit of the rack, the signal after the unit
// as the synth plays it: an oscilloscope and a spectrum (see
// tracker.UnitModel.AnalysisOf).
type AnalysisPanel struct {
	scope, spectrum *Plot
	voiceBtn        *Clickable
	voiceMenu       *MenuState
	fresh           bool // laid out for the first time since it was opened
}

func NewAnalysisPanel() *AnalysisPanel {
	return &AnalysisPanel{
		scope:     NewPlot(plotRange{0, 1}, plotRange{-1, 1}, 0),
		spectrum:  NewPlot(plotRange{-3.8, 0}, plotRange{SpectrumDbMax, SpectrumDbMin}, SpectrumDbMin),
		voiceBtn:  new(Clickable),
		voiceMenu: new(MenuState),
		fresh:     true,
	}
}

func (p *AnalysisPanel) Tags(level int, yield TagYieldFunc) bool {
	return p.voiceMenu.Tags(level+1, yield)
}

// Layout lays out the panel of the unit on row y, in the constraints.
func (p *AnalysisPanel) Layout(gtx C, y int) D {
	t := TrackerFromContext(gtx)
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, t.Theme.UnitEditor.Preview, clip.Rect{Max: size}.Op())
	a, ok := t.Unit().AnalysisOf(y)
	header := func(gtx C) D {
		text := "after the unit"
		switch {
		case !ok:
			text = "not played: the signal of a disabled unit, or with the native synth, is not recorded"
		case a.Before:
			text = "before the unit, which leaves nothing on the stack"
		}
		if ok && a.Stereo {
			text += " · stereo"
		}
		voice := t.Unit().AnalysisVoice(y)
		btn := MenuBtn(p.voiceMenu, p.voiceBtn, voice.String()).WithBtnStyle(&t.Theme.Button.Text).
			WithPopupStyle(&t.Theme.Popup.ContextMenu).WithTip("The voices of the instrument shown")
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return btn.Layout(gtx, IntMenuChild(voice, icons.NavigationCheck)) }),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, text).Layout),
		)
	}
	channels := 1
	if a.Stereo {
		channels = 2
	}
	scope := func(gtx C) D {
		w := a.Scope
		if len(w.Buffer) == 0 {
			return D{Size: gtx.Constraints.Max}
		}
		cx := float32(w.Cursor) / float32(len(w.Buffer))
		xticks := beatTicks(t.Scope().LengthInBeats().Value(), max(t.Song().RowsPerBeat().Value(), 1))
		return p.scope.Layout(gtx, scopeData(w), xticks, scopeYTicks, cx, channels)
	}
	spectrum := func(gtx C) D {
		data := func(chn int, xr plotRange) (plotRange, bool) {
			if chn >= channels || len(a.Spectrum[chn]) == 0 {
				return plotRange{}, false
			}
			return spectrumCurve(a.Spectrum[chn], xr)
		}
		return p.spectrum.Layout(gtx, data, spectrumXTicks, spectrumYTicks, float32(math.NaN()), channels)
	}
	gap := layout.Spacer{Width: 6}.Layout
	return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(header),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(1, scope),
					layout.Rigid(gap),
					layout.Flexed(1, spectrum),
				)
			}),
		)
	})
}

// layoutAnalysisButton lays out, at the top left of the cell after the
// parameters of row y and what follows them, the button that opens and
// closes the analysis panel of the unit: bright while it is open.
func (pe *InstrumentEditor) layoutAnalysisButton(gtx C, y int) {
	t := TrackerFromContext(gtx)
	toggle := t.Unit().ToggleAnalysis(y)
	if !toggle.Enabled() {
		return
	}
	for len(pe.analysisBtns) <= y {
		pe.analysisBtns = append(pe.analysisBtns, Clickable{})
	}
	click := &pe.analysisBtns[y]
	for click.Clicked(gtx) {
		toggle.Do()
	}
	cellWidth := gtx.Dp(t.Theme.UnitEditor.Width)
	colP := pe.paramTable.ColTitleList.List.Position
	x := (t.Params().RowWidth(y)+t.Unit().InlineCells(y)-colP.First)*cellWidth - colP.Offset
	inset, px := gtx.Dp(2), gtx.Dp(18)
	defer op.Offset(image.Pt(x+inset, inset)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(px, px))
	c := t.Theme.IconButton.Disabled.Color
	if t.Unit().AnalysisOpen(y) {
		c = t.Theme.IconButton.Enabled.Color
	}
	click.Layout(gtx, func(gtx C) D {
		t.Theme.Icon(icons.EditorShowChart).Layout(gtx, c)
		return D{Size: image.Pt(px, px)}
	})
}

// layoutAnalysis lays out the analysis panel of the unit on row y under the
// row, and under the editor of an unfolded eq unit, as wide as the rack.
func (pe *InstrumentEditor) layoutAnalysis(gtx C, y int) {
	for len(pe.analysisPanels) <= y {
		pe.analysisPanels = append(pe.analysisPanels, nil)
	}
	p := pe.analysisPanels[y]
	if p == nil {
		p = NewAnalysisPanel()
		pe.analysisPanels[y] = p
	}
	pe.analysisShown = append(pe.analysisShown, y)
	// just opened, and not all of it in view: the rack scrolls the row to
	// the top
	rowP := pe.paramTable.RowTitleList.List.Position
	if p.fresh && pe.rowTop(gtx, y)-rowP.Offset+pe.rowHeight(gtx, y) > pe.rackHeight {
		pe.paramTable.RowTitleList.List.ScrollTo(y)
		gtx.Execute(op.InvalidateCmd{})
	}
	p.fresh = false
	top := pe.rowHeight(gtx, y) - gtx.Dp(analysisPanelHeight)
	defer op.Offset(image.Pt(0, top)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(analysisPanelHeight)))
	p.Layout(gtx, y)
}
