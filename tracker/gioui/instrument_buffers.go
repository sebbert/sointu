package gioui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/x/explorer"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type (
	// InstrumentBuffers is the tab for managing the song's buffers: importing
	// samples and choosing how they are encoded.
	InstrumentBuffers struct {
		list         *DragList
		addBtn       *Clickable
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
	}
)

func NewInstrumentBuffers(m *tracker.Model) *InstrumentBuffers {
	return &InstrumentBuffers{
		list:         NewDragList(m.Buffer().List(), layout.Vertical),
		addBtn:       new(Clickable),
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
	}
}

func (ib *InstrumentBuffers) Tags(level int, yield TagYieldFunc) bool {
	return yield(level, ib.list) &&
		yield(level+1, &ib.nameEditor.widgetEditor) &&
		yield(level+1, &ib.presetEditor.widgetEditor) &&
		yield(level+1, &ib.formatEditor.widgetEditor) &&
		yield(level+1, &ib.argsEditor.widgetEditor) &&
		ib.channelsMenu.Tags(level+1, yield) &&
		ib.presetMenu.Tags(level+1, yield) &&
		ib.formatMenu.Tags(level+1, yield)
}

func (ib *InstrumentBuffers) update(gtx C, tr *Tracker) {
	for ib.addBtn.Clicked(gtx) {
		ib.chooseSample(tr, false)
	}
	for ib.replaceBtn.Clicked(gtx) {
		if tr.Buffer().HasSelection() {
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
	replaceStyle := &th.IconButton.Enabled
	if !hasSel {
		replaceStyle = &th.IconButton.Disabled
	}
	replaceBtn := IconBtn(th, replaceStyle, ib.replaceBtn, icons.FileFolderOpen, "Replace the sample of the buffer")
	deleteBtn := ActionIconBtn(tr.Buffer().Delete(), th, ib.deleteBtn, icons.ActionDelete, "Delete buffer")
	originalBtn := ToggleBtn(tr.Buffer().Original(), th, ib.originalBtn, "Original", "Play the samples without encoding,\nto compare with the encoded versions")
	previewBtn := ToggleIconBtn(tr.Buffer().Preview(), th, ib.previewBtn, icons.AVPlayArrow, icons.AVStop, "Preview the sample as encoded\n(or original, with Original on)", "Stop the preview")
	toolbar := func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(addBtn.Layout),
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
				Label(th, &th.InstrumentEditor.Properties.Label, "No buffers. Import a sample with +.").Layout)
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
		lines := []layout.Widget{
			func(gtx C) D {
				return layoutBufferLine(gtx, "Name", true, editor(ib.nameEditor, tr.Buffer().Name(), "Name"))
			},
			func(gtx C) D {
				return layoutBufferLine(gtx, "Channels", false, func(gtx C) D {
					return channels.Layout(gtx, IntMenuChild(tr.Buffer().Channels(), icons.NavigationCheck))
				})
			},
			nil,
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
			func(gtx C) D {
				return layout.UniformInset(unit.Dp(6)).Layout(gtx,
					Label(th, &th.InstrumentEditor.Properties.Label, tr.Buffer().Info()).Layout)
			},
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
