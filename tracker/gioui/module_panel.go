package gioui

import (
	"fmt"
	"image"

	"gioui.org/io/key"
	"gioui.org/layout"
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
		params      [sointu.MaxModuleParams]moduleParamRow
		paramList   *layout.List
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
	return yield(level, mp.list) && yield(level+1, &mp.nameEditor.widgetEditor)
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

func (mp *ModulePanel) layout(gtx C) D {
	t := TrackerFromContext(gtx)
	mp.update(gtx, t)
	gtx.Constraints = layout.Exact(image.Pt(min(gtx.Dp(250), gtx.Constraints.Max.X), gtx.Constraints.Max.Y))
	labelStyle := &t.Theme.InstrumentEditor.Properties.Label
	header := func(gtx C) D {
		addBtn := ActionIconBtn(t.Module().Add(), t.Theme, mp.addBtn, icons.ContentAdd, "Add module")
		deleteBtn := ActionIconBtn(t.Module().Delete(), t.Theme, mp.deleteBtn, icons.ActionDelete, "Delete module\nModule units using it\nare left without a module")
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Rigid(Label(t.Theme, labelStyle, "Modules").Layout),
			layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
			layout.Rigid(deleteBtn.Layout),
			layout.Rigid(addBtn.Layout),
		)
	}
	element := func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		name, uses, ok := t.Module().Item(i)
		if !ok {
			return D{}
		}
		used := fmt.Sprintf("used %d×", uses)
		if uses == 0 {
			used = "unused"
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(layout.Spacer{Width: 6}.Layout),
			layout.Flexed(1, Label(t.Theme, &t.Theme.InstrumentEditor.Presets.Directory, name).Layout),
			layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, used).Layout),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
		)
	}
	list := func(gtx C) D {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, min(gtx.Dp(110), gtx.Constraints.Max.Y)))
		fdl := FilledDragList(t.Theme, mp.list)
		dims := fdl.Layout(gtx, element, nil)
		fdl.LayoutScrollBar(gtx)
		return dims
	}
	numParams := t.Module().NumParams()
	properties := func(gtx C) D {
		if t.Module().List().Count() == 0 {
			style := &t.Theme.InstrumentEditor.UnitList.Comment
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(Label(t.Theme, style, "No modules yet. Add one with +,").Layout),
					layout.Rigid(Label(t.Theme, style, "or select units of an instrument").Layout),
					layout.Rigid(Label(t.Theme, style, "and make a module of them.").Layout),
				)
			})
		}
		// rows: name, inputs and outputs, heard in, the parameters, add parameter
		return mp.paramList.Layout(gtx, numParams+4, func(gtx C, index int) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			switch {
			case index == 0:
				return layoutInstrumentPropertyLine(gtx, "Name", func(gtx C) D {
					gtx.Constraints.Max.X = gtx.Dp(170)
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return mp.nameEditor.Layout(gtx, t.Module().Name(), t.Theme, &t.Theme.InstrumentEditor.InstrumentComment, "Module")
				})
			case index == 1:
				outputs, err := t.Module().Outputs()
				out := fmt.Sprintf("→ %d out", outputs)
				if err != nil {
					out = "→ error"
				}
				inputs := NumUpDown(t.Module().Inputs(), t.Theme, mp.inputs, "The number of signals that the units\nof the module take from the stack")
				return layoutInstrumentPropertyLine(gtx, "Inputs", func(gtx C) D {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(inputs.Layout),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
						layout.Rigid(Label(t.Theme, labelStyle, out).Layout),
						layout.Rigid(layout.Spacer{Width: 6}.Layout),
					)
				})
			case index == 2:
				name, uses := t.Module().HeardIn()
				heard := "Heard in: " + name
				if !uses {
					heard = "Not used by instrument " + name
				}
				l := Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, heard)
				return layout.Inset{Left: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, l.Layout)
			case index == numParams+3:
				addParamBtn := ActionBtn(t.Module().AddParam(), t.Theme, mp.addParamBtn, "Add parameter", "Add a parameter that the\nmodule units set, and bind\nparameters of the units to it")
				return layout.UniformInset(unit.Dp(6)).Layout(gtx, addParamBtn.Layout)
			}
			return mp.layoutParam(gtx, t, index-2)
		})
	}
	return Surface{Height: 4, Focus: t.PatchPanel.TreeFocused(gtx)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(header),
			layout.Rigid(list),
			layout.Flexed(1, properties),
		)
	})
}

// layoutParam lays out parameter k (from 1) of the selected module: its
// name, default and range, and buttons to bind the parameter under the cursor
// of the unit editor to it and to delete it.
func (mp *ModulePanel) layoutParam(gtx C, t *Tracker, k int) D {
	row := &mp.params[k-1]
	m := t.Module()
	def := m.ParamDefault(k)
	source := "nothing bound to it yet"
	if _, ok := m.Param(k); ok {
		if typ, name, ok := m.ParamSource(k); ok {
			source = fmt.Sprintf("like %s of %s", name, typ)
		}
	}
	defUpDown := NumUpDown(def, t.Theme, row.def, fmt.Sprintf("Default: %s\n(%s)", def.String(), source))
	minUpDown := NumUpDown(m.ParamMin(k), t.Theme, row.min, "Lowest value")
	maxUpDown := NumUpDown(m.ParamMax(k), t.Theme, row.max, "Highest value")
	bindBtn := ToggleIconBtn(m.ParamBound(k), t.Theme, row.bind, icons.ToggleCheckBoxOutlineBlank, icons.ContentLink,
		"Bind the parameter under the cursor\nto this parameter of the module",
		"Unbind the parameter under the cursor")
	delBtn := ActionIconBtn(m.DeleteParam(k), t.Theme, row.del, icons.ActionDelete, "Delete parameter")
	labelStyle := &t.Theme.InstrumentEditor.Properties.Label
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(Label(t.Theme, labelStyle, fmt.Sprintf("%d", k)).Layout),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return row.name.Layout(gtx, m.ParamName(k), t.Theme, &t.Theme.InstrumentEditor.InstrumentComment, "name")
				}),
				layout.Rigid(defUpDown.Layout),
				layout.Rigid(bindBtn.Layout),
				layout.Rigid(delBtn.Layout),
			)
		}),
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
				layout.Rigid(Label(t.Theme, &t.Theme.InstrumentEditor.UnitList.Comment, "range").Layout),
				layout.Rigid(layout.Spacer{Width: 6}.Layout),
				layout.Rigid(minUpDown.Layout),
				layout.Rigid(layout.Spacer{Width: 4}.Layout),
				layout.Rigid(maxUpDown.Layout),
				layout.Rigid(layout.Spacer{Width: 6, Height: 28}.Layout),
			)
		}),
	)
}
