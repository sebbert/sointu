package gioui

import (
	"math"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
)

// TestAnalysisPanelInRack opens the analysis panels of units by their
// buttons: the rows after a panel move down by its height, the cursor stays
// on the cells, and the panels show what the synth recorded.
func TestAnalysisPanelInRack(t *testing.T) {
	osc, pan, out := sointu.MakeUnit("oscillator"), sointu.MakeUnit("pan"), sointu.MakeUnit("out")
	osc.Parameters["type"] = sointu.Trisaw
	pan.Parameters["panning"] = 40
	r := newRackTest(t, osc, pan, out)
	r.size.Y = 800
	r.settle()
	ed := r.tr.PatchPanel.instrEditor
	gtx := r.gtx()
	cellHeight := gtx.Dp(r.tr.Theme.UnitEditor.Height)
	// the button of a row: at the top left of the cell after its parameters
	// and the plot of the oscillator
	button := func(row int) f32.Point {
		x := r.model.Params().RowWidth(row) + r.model.Unit().InlineCells(row)
		c := r.cellCenter(x, 0)
		return f32.Pt(c.X-float32(gtx.Dp(r.tr.Theme.UnitEditor.Width))/2+10, float32(ed.rowTop(gtx, row)+10))
	}
	click := func(at f32.Point) {
		r.pointer(pointer.Press, pointer.ButtonPrimary, at)
		r.pointer(pointer.Release, 0, at)
	}
	click(button(1))
	if !r.model.Unit().AnalysisOpen(1) || r.model.Unit().AnalysisOpen(0) {
		t.Fatal("the button of the pan does not open its panel alone")
	}
	click(button(0))
	if !r.model.Unit().AnalysisOpen(0) {
		t.Fatal("the button of the oscillator does not open its panel")
	}
	panel := gtx.Dp(analysisPanelHeight)
	if got := ed.rowHeight(gtx, 0); got != cellHeight+panel {
		t.Errorf("the row with a panel is %d high, not %d", got, cellHeight+panel)
	}
	if got := ed.rowTop(gtx, 2); got != 2*cellHeight+2*panel {
		t.Errorf("the out unit is at %d, not under both panels", got)
	}
	// a click on a knob of the out unit, under the panels, puts the cursor
	// there
	_, x := r.param(2, "gain")
	at := r.cellCenter(x, 0)
	at.Y = float32(ed.rowTop(gtx, 2) + cellHeight/2)
	click(at)
	if c := r.model.Params().Cursor(); c.Y != 2 {
		t.Errorf("a click on the out unit puts the cursor on row %d", c.Y)
	}
	// what the synth recorded: a sine left and its half right after the
	// pan, a ramp after the oscillator
	r.frame() // the watches are asked for
	frames := make(sointu.AudioBuffer, 4096)
	for i := range frames {
		v := float32(math.Sin(float64(i) * 2 * math.Pi / 200))
		frames[i] = [2]float32{v, v / 2}
	}
	ramp := make(sointu.AudioBuffer, 4096)
	for i := range ramp {
		ramp[i][0] = float32(i%300)/150 - 1
	}
	r.model.ProcessMsg(tracker.MsgToModel{Data: tracker.TapAudio{Point: sointu.TapPoint{Unit: 2}, Buffer: &frames}})
	r.model.ProcessMsg(tracker.MsgToModel{Data: tracker.TapAudio{Point: sointu.TapPoint{Unit: 1}, Buffer: &ramp}})
	if a, ok := r.model.Unit().AnalysisOf(1); !ok || !a.Stereo || a.Scope.Cursor != 4096 || len(a.Spectrum[1]) == 0 {
		t.Errorf("the panel of the pan has no stereo signal: %v, cursor %d, %d bins", ok, a.Scope.Cursor, len(a.Spectrum[1]))
	}
	r.screenshot("analysis_panels")
	// closed by the key
	r.model.Params().SetCursor(tracker.Point{Y: 1})
	r.model.Unit().Analysis().Toggle()
	if r.model.Unit().AnalysisOpen(1) {
		t.Error("the panel of the pan stays open")
	}
}
