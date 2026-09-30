package gioui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strconv"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type (
	// InstrumentBuffers is the tab for managing the song's buffers: importing
	// samples and choosing how they are encoded.
	InstrumentBuffers struct {
		list         *DragList
		addBtn       *Clickable
		newEmptyBtn  *Clickable
		clearBtn     *Clickable
		fitBtn       *Clickable
		lengthEditor *DraftEditor
		replaceBtn   *Clickable
		deleteBtn    *Clickable
		originalBtn  *Clickable
		previewBtn   *Clickable
		channelsBtn  *Clickable
		channelsMenu *MenuState
		presetBtn    *Clickable
		presetMenu   *MenuState
		formatBtn    *Clickable
		formatMenu   *MenuState
		newPreset    *Clickable
		deletePreset *Clickable
		presetEditor *DraftEditor
		nameEditor   *Editor
		formatEditor *DraftEditor
		argsEditor   *DraftEditor
		props        *layout.List
		info         *widget.Selectable
		waveform     *Plot
		waveformOf   int // the index of the buffer drawn, to reset the view when it changes
		spectrum     *Plot
	}
)

func NewInstrumentBuffers(m *tracker.Model) *InstrumentBuffers {
	return &InstrumentBuffers{
		list:         NewDragList(m.Buffer().List(), layout.Vertical),
		addBtn:       new(Clickable),
		newEmptyBtn:  new(Clickable),
		clearBtn:     new(Clickable),
		fitBtn:       new(Clickable),
		lengthEditor: NewDraftEditor(text.Start),
		replaceBtn:   new(Clickable),
		deleteBtn:    new(Clickable),
		originalBtn:  new(Clickable),
		previewBtn:   new(Clickable),
		channelsBtn:  new(Clickable),
		channelsMenu: new(MenuState),
		presetBtn:    new(Clickable),
		presetMenu:   new(MenuState),
		formatBtn:    new(Clickable),
		formatMenu:   new(MenuState),
		newPreset:    new(Clickable),
		deletePreset: new(Clickable),
		presetEditor: NewDraftEditor(text.Start),
		nameEditor:   NewEditor(true, true, text.Start),
		formatEditor: NewDraftEditor(text.Start),
		argsEditor:   NewDraftEditor(text.Start),
		props:        &layout.List{Axis: layout.Vertical},
		info:         new(widget.Selectable),
		waveform:     NewPlot(plotRange{0, 1}, plotRange{-1, 1}, 0),
		spectrum:     NewPlot(plotRange{-3.8, 0}, plotRange{bufferSpectrumDbMax, bufferSpectrumDbMin}, bufferSpectrumDbMin),
	}
}

func (ib *InstrumentBuffers) Tags(level int, yield TagYieldFunc) bool {
	return yield(level, ib.list) &&
		yield(level+1, &ib.nameEditor.widgetEditor) &&
		yield(level+1, &ib.presetEditor.widgetEditor) &&
		yield(level+1, &ib.lengthEditor.widgetEditor) &&
		yield(level+1, &ib.formatEditor.widgetEditor) &&
		yield(level+1, &ib.argsEditor.widgetEditor) &&
		ib.channelsMenu.Tags(level+1, yield) &&
		yield(level+1, ib.info) &&
		ib.presetMenu.Tags(level+1, yield) &&
		ib.formatMenu.Tags(level+1, yield)
}

func (ib *InstrumentBuffers) update(gtx C, tr *Tracker) {
	for ib.addBtn.Clicked(gtx) {
		ib.chooseSample(tr, false)
	}
	for ib.replaceBtn.Clicked(gtx) {
		if tr.Buffer().HasSelection() && !tr.Buffer().IsSpectrum() && !tr.Buffer().IsBus() {
			ib.chooseSample(tr, true)
		}
	}
}

// chooseSample opens a file dialog and imports the chosen file as a new
// buffer, or into the selected buffer if replace is true.
func (ib *InstrumentBuffers) chooseSample(tr *Tracker, replace bool) {
	if tr.Exploring {
		return
	}
	tr.Exploring = true
	go func() {
		file, err := tr.Explorer.ChooseFile(tracker.AudioFileExtensions...)
		tr.Broker().ToModel <- tracker.MsgToModel{Data: func() {
			tr.Exploring = false
			if err != nil {
				if err != explorer.ErrUserDecline {
					tr.Alerts().Add(err.Error(), tracker.Error)
				}
				return
			}
			tr.Buffer().Import(file, replace)
		}}
	}()
}

func (ib *InstrumentBuffers) layout(gtx C) D {
	tr := TrackerFromContext(gtx)
	ib.update(gtx, tr)
	th := tr.Theme
	hasSel := tr.Buffer().HasSelection()

	addBtn := IconBtn(th, &th.IconButton.Enabled, ib.addBtn, icons.ContentAdd, "Import a sample as a new buffer")
	newEmptyBtn := ActionIconBtn(tr.Buffer().NewEmpty(), th, ib.newEmptyBtn, icons.AVMic, "Add an empty buffer for bufwrite units to record into")
	replaceStyle := &th.IconButton.Enabled
	if !hasSel || tr.Buffer().IsSpectrum() || tr.Buffer().IsBus() {
		replaceStyle = &th.IconButton.Disabled
	}
	replaceBtn := IconBtn(th, replaceStyle, ib.replaceBtn, icons.FileFolderOpen, "Replace the sample of the buffer")
	deleteBtn := ActionIconBtn(tr.Buffer().Delete(), th, ib.deleteBtn, icons.ActionDelete, "Delete buffer")
	originalBtn := ToggleBtn(tr.Buffer().Original(), th, ib.originalBtn, "Original", "Play the samples without encoding,\nto compare with the encoded versions")
	previewBtn := ToggleIconBtn(tr.Buffer().Preview(), th, ib.previewBtn, icons.AVPlayArrow, icons.AVStop, "Preview the sample as encoded\n(or original, with Original on)", "Stop the preview")
	toolbar := func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(addBtn.Layout),
			layout.Rigid(newEmptyBtn.Layout),
			layout.Rigid(replaceBtn.Layout),
			layout.Rigid(deleteBtn.Layout),
			layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
			layout.Rigid(previewBtn.Layout),
			layout.Rigid(originalBtn.Layout),
			layout.Rigid(layout.Spacer{Width: 4}.Layout),
		)
	}

	elem := func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		name, info := tr.Buffer().Item(i)
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(Label(th, &th.InstrumentEditor.Presets.Results.Builtin, name).Layout),
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(Label(th, &th.InstrumentEditor.Presets.Results.UserDir, info).Layout),
		)
	}
	list := func(gtx C) D {
		gtx.Constraints = layout.Exact(image.Pt(min(gtx.Dp(220), gtx.Constraints.Max.X), gtx.Constraints.Max.Y))
		fdl := FilledDragList(th, ib.list)
		dims := fdl.Layout(gtx, elem, nil)
		fdl.LayoutScrollBar(gtx)
		return dims
	}
	listSurface := func(gtx C) D {
		return Surface{Height: 5, Focus: tr.PatchPanel.TreeFocused(gtx)}.Layout(gtx, list)
	}

	props := func(gtx C) D {
		if !hasSel {
			return layout.UniformInset(unit.Dp(12)).Layout(gtx,
				Label(th, &th.InstrumentEditor.Properties.Label, "No buffers. Import a sample with +, or add\nan empty buffer to record into with the microphone.").Layout)
		}
		channels := MenuBtn(ib.channelsMenu, ib.channelsBtn, tr.Buffer().Channels().String()).
			WithBtnStyle(&th.Button.Text).WithPopupStyle(&th.Popup.ContextMenu)
		preset := MenuBtn(ib.presetMenu, ib.presetBtn, tr.Buffer().Preset().String()).
			WithBtnStyle(&th.Button.Text).WithPopupStyle(&th.Popup.ContextMenu)
		monoStyle := th.InstrumentEditor.InstrumentComment
		monoStyle.Font.Typeface = "Go Mono"
		styledEditor := func(e interface {
			Layout(C, tracker.String, *Theme, *EditorStyle, string) D
		}, s tracker.String, style *EditorStyle, hint string) layout.Widget {
			return func(gtx C) D {
				return layoutField(gtx, th, func(gtx C) D { return e.Layout(gtx, s, th, style, hint) })
			}
		}
		editor := func(e interface {
			Layout(C, tracker.String, *Theme, *EditorStyle, string) D
		}, s tracker.String, hint string) layout.Widget {
			return styledEditor(e, s, &th.InstrumentEditor.InstrumentComment, hint)
		}
		newPresetBtn := ActionBtn(tr.Buffer().NewPreset(), th, ib.newPreset, "New preset", "Add a preset with this encoding\nand use it for this sample")
		deletePresetBtn := ActionBtn(tr.Buffer().DeletePreset(), th, ib.deletePreset, "Delete preset", "Delete the preset; the samples using it\nkeep its encoding as their own")
		presetLine := func(gtx C) D {
			if tr.Buffer().IsCustom() {
				return layoutBufferLine(gtx, "Preset", false, func(gtx C) D {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(Label(th, &th.InstrumentEditor.Properties.Label, "none, this sample only").Layout),
						layout.Rigid(newPresetBtn.Layout),
					)
				})
			}
			users := tr.Buffer().PresetUsers()
			shared := ""
			if users > 1 {
				shared = fmt.Sprintf("shared by %d samples", users)
			}
			return layoutBufferLine(gtx, "Preset", true, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, editor(ib.presetEditor, tr.Buffer().PresetName(), "Name")),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Rigid(Label(th, &th.InstrumentEditor.Presets.Results.UserDir, shared).Layout),
				)
			})
		}
		presetBtns := func(gtx C) D {
			if tr.Buffer().IsCustom() {
				return D{}
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
				layout.Rigid(newPresetBtn.Layout),
				layout.Rigid(deletePresetBtn.Layout),
			)
		}
		info := func(gtx C) D {
			// selectable, so that e.g. ffmpeg's error messages can be copied
			style := th.InstrumentEditor.Properties.Label
			l := material.Label(&th.Material, style.TextSize, tr.Buffer().Info())
			l.Color, l.Font, l.State = style.Color, style.Font, ib.info
			l.SelectionColor = th.Material.ContrastBg
			l.SelectionColor.A = 0x60
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, l.Layout)
		}
		common := []layout.Widget{
			func(gtx C) D {
				return layoutBufferLine(gtx, "Name", true, editor(ib.nameEditor, tr.Buffer().Name(), "Name"))
			},
			func(gtx C) D {
				return layoutBufferLine(gtx, "Channels", false, func(gtx C) D {
					return channels.Layout(gtx, IntMenuChild(tr.Buffer().Channels(), icons.NavigationCheck))
				})
			},
		}
		var lines []layout.Widget
		if tr.Buffer().IsBus() {
			lines = append(common[:1:1], nil, info)
		} else if tr.Buffer().IsSpectrum() {
			lines = append(common[:1:1], nil, info, ib.layoutSpectrum)
		} else if tr.Buffer().IsWritable() {
			clearBtn := ActionBtn(tr.Buffer().Clear(), th, ib.clearBtn, "Clear", "Discard what has been recorded")
			fitBtn := ActionBtn(tr.Buffer().FitToRecording(), th, ib.fitBtn, "Fit to recording", "Make the buffer as long as\nwhat has been recorded")
			lines = append(common,
				func(gtx C) D {
					return layoutBufferLine(gtx, "Length (s)", true, editor(ib.lengthEditor, tr.Buffer().Length(), "seconds"))
				},
				func(gtx C) D {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
						layout.Rigid(fitBtn.Layout),
						layout.Rigid(clearBtn.Layout),
					)
				},
				nil,
				info,
				ib.layoutWaveform,
			)
		} else {
			lines = append(common, nil,
				func(gtx C) D {
					return layoutBufferLine(gtx, "Encoding", false, func(gtx C) D {
						return preset.Layout(gtx, IntMenuChild(tr.Buffer().Preset(), icons.NavigationCheck))
					})
				},
				presetLine,
				func(gtx C) D {
					format := MenuBtn(ib.formatMenu, ib.formatBtn, tr.Buffer().FormatChoice().String()).
						WithBtnStyle(&th.Button.Text).WithPopupStyle(&th.Popup.ContextMenu)
					menu := func(gtx C) D {
						return format.Layout(gtx, IntMenuChild(tr.Buffer().FormatChoice(), icons.NavigationCheck))
					}
					if !tr.Buffer().IsCustomFormat() {
						return layoutBufferLine(gtx, "Format", false, menu)
					}
					return layoutBufferLine(gtx, "Format", true, func(gtx C) D {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(menu),
							layout.Rigid(layout.Spacer{Width: 6}.Layout),
							layout.Flexed(1, styledEditor(ib.formatEditor, tr.Buffer().Format(), &monoStyle, "ffmpeg -f format, e.g. matroska")),
						)
					})
				},
				func(gtx C) D {
					return layoutBufferLine(gtx, "ffmpeg args", true, styledEditor(ib.argsEditor, tr.Buffer().Args(), &monoStyle, "e.g. -c:a libopus -b:a 32k"))
				},
				presetBtns,
				nil,
				info,
				ib.layoutWaveform,
			)
		}
		return ib.props.Layout(gtx, len(lines), func(gtx C, i int) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			if lines[i] == nil { // divider
				px := max(gtx.Dp(unit.Dp(1)), 1)
				paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 3}, clip.Rect(image.Rect(0, 0, gtx.Constraints.Max.X, px)).Op())
				return D{Size: image.Pt(gtx.Constraints.Max.X, px)}
			}
			return lines[i](gtx)
		})
	}

	f := func(gtx C) D {
		m := gtx.Constraints.Max
		layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(toolbar),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(listSurface),
					layout.Flexed(1, props),
				)
			}),
		)
		return D{Size: m}
	}
	return Surface{Height: 3, Focus: tr.PatchPanel.TreeFocused(gtx)}.Layout(gtx, f)
}

// releasedPlayheadFade is how many frames the playheads of released notes take
// to fade out.
const releasedPlayheadFade = 22050

// layoutWaveform draws the audio of the selected buffer, fitted to its peak.
// Valid frames are drawn in the channel colors, the rest dimmed, the write
// head of a writable buffer as the cursor and the notes of bufread units as
// markers, released ones fading out.
func (ib *InstrumentBuffers) layoutWaveform(gtx C) D {
	tr := TrackerFromContext(gtx)
	audio, head, filled := tr.Buffer().Waveform()
	frames := audio.Frames()
	data, peak := waveformData(audio, head, filled)
	seconds := float32(frames) / 44100
	xticks := func(r plotRange, count int, yield func(pos float32, label string)) {
		if seconds <= 0 || count <= 0 {
			return
		}
		span := (r.b - r.a) * seconds
		step := float32(0.001) // steps of 1, 2 and 5 times powers of ten
		for i := 0; step*float32(count) < span; i++ {
			step *= [...]float32{2, 2.5, 2}[i%3]
		}
		for i := math.Ceil(float64(r.a * seconds / step)); float32(i)*step <= r.b*seconds; i++ {
			t := float64(i) * float64(step)
			yield(float32(t)/seconds, strconv.FormatFloat(t, 'f', -1, 32))
		}
	}
	yticks := func(r plotRange, count int, yield func(pos float32, label string)) {
		yield(0, "")
	}
	if sel := tr.Buffer().List().Selected(); sel != ib.waveformOf {
		ib.waveformOf = sel
		ib.waveform.Reset()
	}
	ib.waveform.SetYRange(plotRange{-peak * 1.05, peak * 1.05})
	ib.waveform.Markers = ib.waveform.Markers[:0]
	if frames > 0 {
		tr.Buffer().Playheads(func(frame, released int) {
			// released notes fade out, as it is not known when they fall silent
			alpha := 1 - float32(released)/releasedPlayheadFade
			if alpha > 0 {
				ib.waveform.Markers = append(ib.waveform.Markers, PlotMarker{X: float32(frame) / float32(frames), Alpha: alpha})
			}
		})
	}
	cursor := float32(math.NaN())
	if audio.Writable && frames > 0 {
		cursor = float32(head) / float32(frames)
	}
	h := gtx.Dp(140)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx C) D {
		return ib.waveform.Layout(gtx, data, xticks, yticks, cursor, 3)
	})
}

// The decibel range of the spectrum plot: 0 dB is a full scale sine.
const (
	bufferSpectrumDbMin = -96
	bufferSpectrumDbMax = 12
)

// layoutSpectrum draws the latest spectrum of the selected spectrum buffer,
// over logarithmic frequency like the spectrum analyzer.
func (ib *InstrumentBuffers) layoutSpectrum(gtx C) D {
	tr := TrackerFromContext(gtx)
	data, channels := spectrumData(tr.Buffer().Spectrum())
	h := gtx.Dp(180)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx C) D {
		return ib.spectrum.Layout(gtx, data, spectrumXTicks, spectrumYTicks, float32(math.NaN()), max(channels, 1))
	})
}

// layoutField draws a text field: the widget on a rounded background, so that
// it is recognizable as editable.
func layoutField(gtx C, th *Theme, w layout.Widget) D {
	bg := func(gtx C) D {
		rr := gtx.Dp(4)
		defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, rr).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.InstrumentEditor.Presets.SearchBg)
		return D{Size: gtx.Constraints.Min}
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(bg),
		layout.Stacked(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Top: 4, Bottom: 4, Left: 6, Right: 6}.Layout(gtx, w)
		}),
	)
}

// layoutBufferLine lays out a labeled line of the buffer properties, with the
// labels in a column. If fill is true, the content fills the rest of the
// line; otherwise it is left aligned at its own size.
func layoutBufferLine(gtx C, label string, fill bool, content layout.Widget) D {
	tr := TrackerFromContext(gtx)
	l := func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Dp(90)
		return Label(tr.Theme, &tr.Theme.InstrumentEditor.Properties.Label, label).Layout(gtx)
	}
	c := layout.Rigid(content)
	if fill {
		c = layout.Flexed(1, content)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Width: 6, Height: 36}.Layout),
		layout.Rigid(l),
		c,
		layout.Rigid(layout.Spacer{Width: 6}.Layout),
	)
}

// waveformData returns the plot data of a buffer's audio over x from 0 to 1,
// and its peak for fitting the height: channels 0 and 1 are the valid frames
// of the channels, the filled ones before head, and channel 2 the rest.
func waveformData(audio sointu.BufferAudio, head, filled int) (data PlotDataFunc, peak float32) {
	frames := audio.Frames()
	oldest := 0
	if frames > 0 {
		oldest = ((head-filled)%frames + frames) % frames
	}
	valid := func(f int) bool { return (f-oldest+frames)%frames < filled }
	data = func(chn int, xr plotRange) (plotRange, bool) {
		if frames == 0 || (chn == 1 && audio.Channels < 2) {
			return plotRange{}, false
		}
		f1 := max(int(xr.a*float32(frames)), 0)
		f2 := min(int(xr.b*float32(frames)), frames-1)
		if f1 > f2 {
			return plotRange{}, false
		}
		c1, c2 := chn, chn // channel 2 is the invalid frames of all channels
		if chn == 2 {
			c1, c2 = 0, audio.Channels-1
		}
		lo, hi, found := float32(math.Inf(1)), float32(math.Inf(-1)), false
		step := max((f2-f1)/500, 1) // sample long ranges
		for f := f1; f <= f2; f += step {
			if valid(f) == (chn == 2) {
				continue
			}
			for c := c1; c <= c2; c++ {
				v := audio.Data[f*audio.Channels+c]
				lo, hi, found = min(lo, v), max(hi, v), true
			}
		}
		return plotRange{-hi, -lo}, found
	}
	// long buffers are sampled
	for i := 0; i < len(audio.Data); i += max(len(audio.Data)/50000, 1) {
		peak = max(peak, float32(math.Abs(float64(audio.Data[i]))))
	}
	if peak <= 1e-4 {
		peak = 1
	}
	return data, peak
}

// spectrumData returns the plot data of spectrum magnitudes, as returned by
// BufferModel.SpectrumOf, over x = log10(frequency/22050 Hz) and y in dB
// from bufferSpectrumDbMin, and the number of channels.
func spectrumData(mags []float32, size int) (data PlotDataFunc, channels int) {
	// a full scale sine has the magnitude size/4 with the Hann window
	norm := float64(size) / 4
	bins := size/2 + 1
	if size > 0 {
		channels = len(mags) / bins
	}
	data = func(chn int, xr plotRange) (plotRange, bool) {
		if chn >= channels {
			return plotRange{}, false
		}
		db := func(k int) float32 {
			return float32(max(20*math.Log10(float64(mags[chn*bins+k])/norm+1e-12), bufferSpectrumDbMin))
		}
		// bin k is at k*2/size of 22050 Hz
		k1 := max(int(math.Pow(10, float64(xr.a))*float64(size)/2), 1)
		k2 := min(int(math.Pow(10, float64(xr.b))*float64(size)/2), bins-1)
		if k1 > k2 {
			return plotRange{}, false
		}
		hi := float32(bufferSpectrumDbMin)
		for k := k1; k <= k2; k++ {
			hi = max(hi, db(k))
		}
		return plotRange{hi, bufferSpectrumDbMin}, true
	}
	return data, channels
}
