package gioui

import "testing"

func TestPlotZoomIsBounded(t *testing.T) {
	for _, orig := range []plotRange{{0, 1}, {-3.8, 0}} {
		p := NewPlot(orig, plotRange{-1, 1}, 0)
		p.xScale, p.xOffset, p.yScale = 5, 100, 100 // zoomed far out and panned away
		p.clamp()
		if x := p.xlim(); x.a < orig.a-1e-6 || x.b > orig.b+1e-6 {
			t.Errorf("%v: zoomed out to %v", orig, x)
		}
		if y := p.ylim(); y.b-y.a > 2*maxYZoomOut+1e-3 {
			t.Errorf("%v: y zoomed out to %v", orig, y)
		}
		p.xScale, p.xOffset = -2, -100 // zoomed in and panned past the start
		p.clamp()
		if x := p.xlim(); x.a < orig.a-1e-6 || x.b > orig.b+1e-6 || x.b-x.a > (orig.b-orig.a)/2 {
			t.Errorf("%v: zoomed in view %v outside the original range", orig, x)
		}
	}
}
