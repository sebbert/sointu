package gioui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type (
	// ModulePanel is the left side of the Modules tab: the list of the
	// modules of the song, and the name, inputs and parameters of the
	// selected one. The unit editor next to it edits the units of the
	// selected module.
	ModulePanel struct {
		list        *DragList
		nameEditor  *Editor
		inputs      *NumericUpDownState
		addBtn      *Clickable
		deleteBtn   *Clickable
		addParamBtn *Clickable
		presetsBtn  *Clickable
		presetsMenu *MenuState
		params      [sointu.MaxModuleParams]moduleParamRow
		paramList   *layout.List

		smallEnabled, smallDisabled IconButtonStyle
	}

	moduleParamRow struct {
		name          *Editor
		def, min, max *NumericUpDownState
		bind, del     *Clickable
	}
)

func NewModulePanel(m *tracker.Model) *ModulePanel {
	ret := &ModulePanel{
		list:        NewDragList(m.Module().List(), layout.Vertical),
		nameEditor:  NewEditor(true, true, text.Start),
		inputs:      NewNumericUpDownState(),
		addBtn:      new(Clickable),
		deleteBtn:   new(Clickable),
		addParamBtn: new(Clickable),
		presetsBtn:  new(Clickable),
		presetsMenu: new(MenuState),
		paramList:   &layout.List{Axis: layout.Vertical},
	}
	for i := range ret.params {
		ret.params[i] = moduleParamRow{
			name: NewEditor(true, true, text.Start),
			def:  NewNumericUpDownState(), min: NewNumericUpDownState(), max: NewNumericUpDownState(),
			bind: new(Clickable), del: new(Clickable),
		}
	}
	return ret
}

func (mp *ModulePanel) Tags(level int, yield TagYieldFunc) bool {
	return yield(level, mp.list) && yield(level+1, &mp.nameEditor.widgetEditor) && mp.presetsMenu.Tags(level+1, yield)
}

func (mp *ModulePanel) update(gtx C, t *Tracker) {
	for {
		event, ok := gtx.Event(
			key.Filter{Focus: mp.list, Name: key.NameRightArrow},
			key.Filter{Focus: mp.list, Name: key.NameReturn},
			key.Filter{Focus: mp.list, Name: key.NameEnter},
		)
		if !ok {
			break
		}
		if e, ok := event.(key.Event); ok && e.State == key.Press {
			switch e.Name {
			case key.NameRightArrow:
				t.PatchPanel.instrEditor.dragList.Focus()
			case key.NameReturn, key.NameEnter:
				mp.nameEditor.Focus()
			}
		}
	}
}

// Sizes of the module panel.
const (
	modulePanelWidth  = unit.Dp(264)
	modulePanelMargin = unit.Dp(8)
	moduleRowHeight   = unit.Dp(22)
	moduleListRows    = 5
)

var (
	// modulePanelBox is the background of the boxes of the module panel:
	// the list of the modules, the name and each parameter
	modulePanelBox = color.NRGBA{R: 255, G: 255, B: 255, A: 8}
	// modulePanelDivider separates the module panel from the unit editor
	modulePanelDivider = color.NRGBA{R: 0, G: 0, B: 0, A: 255}
)

// box lays out w with a margin on a rounded background as wide as there is
// room.
func (mp *ModulePanel) box(gtx C, inset unit.Dp, w layout.Widget) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Background{}.Layout(gtx,
		func(gtx C) D {
			defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(4)).Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, modulePanelBox)
			return D{Size: gtx.Constraints.Min}
		},
		func(gtx C) D { return layout.UniformInset(inset).Layout(gtx, w) },
	)
}

// row lays out a label at the left and w at the right, on a row of a fixed
// height.
func (mp *ModulePanel) row(gtx C, t *Tracker, label string, w layout.Widget) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: 28}.Layout),
		layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.Properties.Label, label).Layout),
		layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
		layout.Rigid(w),
	)
}

// heading lays out the title of a part of the panel, with buttons at the
// right.
func (mp *ModulePanel) heading(gtx C, t *Tracker, title string, buttons ...layout.Widget) D {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	children := []layout.FlexChild{
		layout.Rigid(layout.Spacer{Height: 32}.Layout),
		layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, title).Layout),
		layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
	}
	for _, b := range buttons {
		children = append(children, layout.Rigid(b))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

// smallIcons makes an icon button small enough for a row of the panel.
func (mp *ModulePanel) smallIcons(t *Tracker, b *IconButton, disabled **IconButtonStyle) {
	mp.smallEnabled, mp.smallDisabled = t.Theme.IconButton.Enabled, t.Theme.IconButton.Disabled
	mp.smallEnabled.Size, mp.smallDisabled.Size = 18, 18
	mp.smallEnabled.Inset, mp.smallDisabled.Inset = layout.UniformInset(4), layout.UniformInset(4)
	b.Style = &mp.smallEnabled
	if disabled != nil {
		*disabled = &mp.smallDisabled
	}
}

func (mp *ModulePanel) layout(gtx C) D {
	t := TrackerFromContext(gtx)
	mp.update(gtx, t)
	width := min(gtx.Dp(modulePanelWidth), gtx.Constraints.Max.X)
	gtx.Constraints = layout.Exact(image.Pt(width, gtx.Constraints.Max.Y))
	comment := &t.Theme.InstrumentEditor.UnitList.Comment
	element := func(gtx C, i int) D {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(moduleRowHeight)))
		name, uses, ok := t.Module().Item(i)
		if !ok {
			return D{Size: gtx.Constraints.Min}
		}
		used := fmt.Sprintf("used %d×", uses)
		if uses == 0 {
			used = "unused"
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Flexed(1, Label(t.Theme, &t.Theme.InstrumentEditor.Presets.Directory, name).Layout),
			layout.Rigid(Label(t.Theme, comment, used).Layout),
			layout.Rigid(layout.Spacer{Width: 12}.Layout),
		)
	}
	list := func(gtx C) D {
		return mp.box(gtx, 0, func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, min(moduleListRows*gtx.Dp(moduleRowHeight), gtx.Constraints.Max.Y)))
			defer clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(4)).Push(gtx.Ops).Pop()
			fdl := FilledDragList(t.Theme, mp.list)
			dims := fdl.Layout(gtx, element, nil)
			fdl.LayoutScrollBar(gtx)
			return dims
		})
	}
	addBtn := ActionIconBtn(t.Module().Add(), t.Theme, mp.addBtn, icons.ContentAdd, "Add module")
	deleteBtn := ActionIconBtn(t.Module().Delete(), t.Theme, mp.deleteBtn, icons.ActionDelete, "Delete module\nModule units using it\nare left without a module")
	addParamBtn := ActionIconBtn(t.Module().AddParam(), t.Theme, mp.addParamBtn, icons.ContentAdd, "Add a parameter that the\nmodule units set, and bind\nparameters of the units to it")
	mp.smallIcons(t, &addBtn.IconButton, &addBtn.DisabledStyle)
	mp.smallIcons(t, &deleteBtn.IconButton, &deleteBtn.DisabledStyle)
	mp.smallIcons(t, &addParamBtn.IconButton, &addParamBtn.DisabledStyle)
	// the module presets: saving the selected module as one, adding one to
	// the song, and deleting one
	presets := func(gtx C) D {
		btn := MenuBtn(mp.presetsMenu, mp.presetsBtn, "Presets").
			WithBtnStyle(&t.Theme.Button.Text).WithPopupStyle(&t.Theme.Popup.ContextMenu).
			WithTip("Module presets: save the selected\nmodule, add one to the song,\nor delete one")
		children := []MenuChild{
			ActionMenuChild(t.Module().SavePreset(), "Save module as preset", "", icons.ContentSave),
			DividerMenuChild(),
			IntMenuChild(t.Module().Presets(), icons.ContentAdd),
		}
		if r := t.Module().DeletePresets().Range(); r.Max >= r.Min {
			children = append(children, DividerMenuChild(), IntMenuChild(t.Module().DeletePresets(), icons.ActionDelete))
		}
		return btn.Layout(gtx, children...)
	}
	numParams := t.Module().NumParams()
	// the parts under the list, which scroll: the module, a heading and
	// the parameters
	properties := func(gtx C) D {
		if t.Module().List().Count() == 0 {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(layout.Spacer{Height: 8}.Layout),
				layout.Rigid(Label(t.Theme, comment, "No modules yet. Add one with +,").Layout),
				layout.Rigid(Label(t.Theme, comment, "or select units of an instrument").Layout),
				layout.Rigid(Label(t.Theme, comment, "and make a module of them.").Layout),
			)
		}
		return mp.paramList.Layout(gtx, numParams+3, func(gtx C, index int) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			switch {
			case index == 0:
				return mp.layoutModule(gtx, t)
			case index == 1:
				return mp.heading(gtx, t, "Parameters", addParamBtn.Layout)
			case index == numParams+2:
				if numParams > 0 {
					return D{}
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(Label(t.Theme, comment, "None. Add one with +, then bind").Layout),
					layout.Rigid(Label(t.Theme, comment, "parameters of the units to it.").Layout),
				)
			}
			return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx C) D { return mp.layoutParam(gtx, t, index-1) })
		})
	}
	return Surface{Height: 2, Focus: t.PatchPanel.TreeFocused(gtx)}.Layout(gtx, func(gtx C) D {
		// a line between the panel and the unit editor
		paint.FillShape(gtx.Ops, modulePanelDivider, clip.Rect{Min: image.Pt(width-max(gtx.Dp(1), 1), 0), Max: image.Pt(width, gtx.Constraints.Max.Y)}.Op())
		return layout.Inset{Left: modulePanelMargin, Right: modulePanelMargin + 1}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return mp.heading(gtx, t, "Modules", presets, deleteBtn.Layout, addBtn.Layout) }),
				layout.Rigid(list),
				layout.Rigid(layout.Spacer{Height: 6}.Layout),
				layout.Flexed(1, properties),
			)
		})
	})
}

// layoutModule lays out the name, the inputs and the outputs of the selected
// module, and the instrument it is heard in.
func (mp *ModulePanel) layoutModule(gtx C, t *Tracker) D {
	label := &t.Theme.InstrumentEditor.Properties.Label
	outputs, err := t.Module().Outputs()
	out := fmt.Sprintf("%d", outputs)
	if err != nil {
		out = "error"
	}
	name, uses := t.Module().HeardIn()
	heard := "Notes play " + name
	if !uses {
		heard = "Notes play " + name + ", which does not use it"
	}
	inputs := NumUpDown(t.Module().Inputs(), t.Theme, mp.inputs, "The number of signals that the units\nof the module take from the stack")
	return mp.box(gtx, 8, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(layout.Spacer{Height: 28}.Layout),
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Dp(64) // the names of the module and of its parameters line up
						return Label(t.Theme, label, "Name").Layout(gtx)
					}),
					layout.Flexed(1, func(gtx C) D {
						gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = gtx.Dp(20), gtx.Dp(20)
						return mp.nameEditor.Layout(gtx, t.Module().Name(), t.Theme, &t.Theme.InstrumentEditor.InstrumentComment, "Module")
					}),
				)
			}),
			layout.Rigid(func(gtx C) D { return mp.row(gtx, t, "Inputs", inputs.Layout) }),
			layout.Rigid(func(gtx C) D {
				return mp.row(gtx, t, "Outputs", func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Dp(t.Theme.NumericUpDown.Width)
					l := Label(t.Theme, label, out)
					l.Alignment = text.Middle
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, heard).Layout),
		)
	})
}

// layoutParam lays out parameter k (from 1) of the selected module: its
// name, buttons to bind the parameter under the cursor of the unit editor to
// it and to delete it, its default and its range.
func (mp *ModulePanel) layoutParam(gtx C, t *Tracker, k int) D {
	row := &mp.params[k-1]
	m := t.Module()
	def := m.ParamDefault(k)
	source := "nothing is bound to it yet"
	if typ, name, ok := m.ParamSource(k); ok {
		source = fmt.Sprintf("like %s of %s", name, typ)
	}
	defUpDown := NumUpDown(def, t.Theme, row.def, fmt.Sprintf("The value of new module units: %s\n(%s)", def.String(), source))
	// the range of the binding of the parameter under the cursor, if it is
	// bound to this parameter
	minUpDown := NumUpDown(m.BindingAt(k, false), t.Theme, row.min, "The value that the parameter under the\ncursor gets when a module unit sets\nthis parameter to its lowest value")
	maxUpDown := NumUpDown(m.BindingAt(k, true), t.Theme, row.max, "The value that the parameter under the\ncursor gets when a module unit sets\nthis parameter to its highest value.\nIt may be less than the other one")
	bound := m.ParamBound(k).Value()
	bindBtn := ToggleIconBtn(m.ParamBound(k), t.Theme, row.bind, icons.ToggleCheckBoxOutlineBlank, icons.ContentLink,
		"Bind the parameter under the cursor\nto this parameter of the module",
		"Unbind the parameter under the cursor")
	delBtn := ActionIconBtn(m.DeleteParam(k), t.Theme, row.del, icons.ActionDelete, "Delete parameter")
	mp.smallIcons(t, &bindBtn.IconButton, &bindBtn.DisabledStyle)
	mp.smallIcons(t, &delBtn.IconButton, &delBtn.DisabledStyle)
	label := &t.Theme.InstrumentEditor.Properties.Label
	return mp.box(gtx, 8, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, fmt.Sprintf("%d", k)).Layout),
					layout.Rigid(layout.Spacer{Width: 8, Height: 28}.Layout),
					layout.Flexed(1, func(gtx C) D {
						gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = gtx.Dp(20), gtx.Dp(20)
						return row.name.Layout(gtx, m.ParamName(k), t.Theme, &t.Theme.InstrumentEditor.InstrumentComment, "name")
					}),
					layout.Rigid(bindBtn.Layout),
					layout.Rigid(delBtn.Layout),
				)
			}),
			layout.Rigid(func(gtx C) D { return mp.row(gtx, t, "Default", defUpDown.Layout) }),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				if !bound {
					// the same height, so that nothing moves with the cursor
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(layout.Spacer{Height: 28}.Layout),
						layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, "Range: select a parameter bound to it").Layout),
					)
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(layout.Spacer{Height: 28}.Layout),
					layout.Rigid(Label(t.Theme, label, "Range").Layout),
					layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
					layout.Rigid(minUpDown.Layout),
					layout.Rigid(layout.Spacer{Width: 6}.Layout),
					layout.Rigid(maxUpDown.Layout),
				)
			}),
		)
	})
}
