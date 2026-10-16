package gioui

import (
	"math"
	"strconv"

	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type (
	OscilloscopeState struct {
		onceBtn             *Clickable
		wrapBtn             *Clickable
		triggerBtn          *Clickable
		lengthInBeatsNumber *NumericUpDownState
		triggerMenuState    *MenuState
		plot                *Plot
	}

	Oscilloscope struct {
		Theme *Theme
		State *OscilloscopeState
	}
)

func NewOscilloscope() *OscilloscopeState {
	return &OscilloscopeState{
		plot:                NewPlot(plotRange{0, 1}, plotRange{-1, 1}, 0),
		onceBtn:             new(Clickable),
		wrapBtn:             new(Clickable),
		lengthInBeatsNumber: NewNumericUpDownState(),
		triggerBtn:          new(Clickable),
		triggerMenuState:    &MenuState{},
	}
}

func Scope(th *Theme, st *OscilloscopeState) Oscilloscope {
	return Oscilloscope{
		Theme: th,
		State: st,
	}
}

func (s *Oscilloscope) Layout(gtx C) D {
	t := TrackerFromContext(gtx)
	leftSpacer := layout.Spacer{Width: unit.Dp(6), Height: unit.Dp(24)}.Layout
	rightSpacer := layout.Spacer{Width: unit.Dp(6)}.Layout

	lengthInBeats := NumUpDown(t.Scope().LengthInBeats(), s.Theme, s.State.lengthInBeatsNumber, "Buffer length in beats")

	onceBtn := ToggleBtn(t.Scope().Once(), s.Theme, s.State.onceBtn, "Once", "Trigger once on next event")
	wrapBtn := ToggleBtn(t.Scope().Wrap(), s.Theme, s.State.wrapBtn, "Wrap", "Wrap buffer when full")

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			w := t.Scope().Waveform()
			cx := float32(w.Cursor) / float32(len(w.Buffer))

			env, envOk := t.Scope().Envelope()

			data := scopeData(w)
			if envOk {
				frames := data
				data = func(chn int, xr plotRange) (yr plotRange, ok bool) {
					if chn != 2 {
						return frames(chn, xr)
					}
					x1 := max(int(xr.a*float32(len(w.Buffer))), 0)
					x2 := min(int(xr.b*float32(len(w.Buffer))), len(w.Buffer)-1)
					if x1 > x2 {
						return plotRange{}, false
					}
					y1 := env.Value(x1)
					y2 := env.Value(x2)
					return plotRange{-max(y1, y2), -min(y1, y2)}, true
				}
			}
			xticks := beatTicks(t.Scope().LengthInBeats().Value(), max(t.Song().RowsPerBeat().Value(), 1))
			yticks := scopeYTicks

			numChannels := 2
			if envOk {
				numChannels = 3
			}

			return s.State.plot.Layout(gtx, data, xticks, yticks, cx, numChannels)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(leftSpacer),
				layout.Rigid(Label(s.Theme, &s.Theme.SongPanel.RowHeader, "Trigger").Layout),
				layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
				layout.Rigid(onceBtn.Layout),
				layout.Rigid(func(gtx C) D {
					triggerBtn := MenuBtn(s.State.triggerMenuState, s.State.triggerBtn, t.Scope().TriggerChannel().String()).
						WithBtnStyle(&t.Theme.Button.Text).WithPopupStyle(&t.Theme.Popup.ContextMenu)
					return triggerBtn.Layout(gtx, IntMenuChild(t.Scope().TriggerChannel(), icons.NavigationCheck))
				}),
				layout.Rigid(rightSpacer),
			)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(leftSpacer),
				layout.Rigid(Label(s.Theme, &s.Theme.SongPanel.RowHeader, "Buffer").Layout),
				layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
				layout.Rigid(wrapBtn.Layout),
				layout.Rigid(lengthInBeats.Layout),
				layout.Rigid(rightSpacer),
			)
		}),
	)
}

// scopeData returns the frames of an oscilloscope as plot data: the x range
// is that of the buffer, from 0 to 1, and the y axis upside down.
func scopeData(w tracker.RingBuffer[[2]float32]) PlotDataFunc {
	return func(chn int, xr plotRange) (yr plotRange, ok bool) {
		x1 := max(int(xr.a*float32(len(w.Buffer))), 0)
		x2 := min(int(xr.b*float32(len(w.Buffer))), len(w.Buffer)-1)
		if x1 > x2 || chn > 1 {
			return plotRange{}, false
		}
		step := max((x2-x1)/1000, 1) // if the range is too large, sample only ~ 1000 points
		y1 := float32(math.Inf(-1))
		y2 := float32(math.Inf(+1))
		for i := x1; i <= x2; i += step {
			sample := w.Buffer[i][chn]
			y1 = max(y1, sample)
			y2 = min(y2, sample)
		}
		return plotRange{-y1, -y2}, true
	}
}

// beatTicks returns the ticks of an oscilloscope of a number of beats: at
// each row, labeled at each beat.
func beatTicks(lengthInBeats, rpb int) PlotTickFunc {
	return func(r plotRange, count int, yield func(pos float32, label string)) {
		l := lengthInBeats * rpb
		a := max(int(math.Ceil(float64(r.a*float32(l)))), 0)
		b := min(int(math.Floor(float64(r.b*float32(l)))), l)
		step := 1
		n := rpb
		for (b-a+1)/step > count {
			step *= n
			n = 2
		}
		a = (a / step) * step
		for i := a; i <= b; i += step {
			if i%rpb == 0 {
				beat := i / rpb
				yield(float32(i)/float32(l), strconv.Itoa(beat))
			} else {
				yield(float32(i)/float32(l), "")
			}
		}
	}
}

// scopeYTicks yields lines at full scale.
func scopeYTicks(r plotRange, count int, yield func(pos float32, label string)) {
	yield(-1, "")
	yield(1, "")
}
