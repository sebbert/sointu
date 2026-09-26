package gioui

import (
	"fmt"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/vsariola/sointu/tracker"
)

type (
	// Editor wraps a widget.Editor and adds some additional key event filters,
	// to prevent key presses from flowing through to the rest of the
	// application while editing (particularly: to prevent triggering notes
	// while editing).
	Editor struct {
		widgetEditor widget.Editor
		filters      []event.Filter
		requestFocus bool
	}

	EditorStyle struct {
		Color     color.NRGBA
		HintColor color.NRGBA
		Font      font.Font
		TextSize  unit.Sp
	}

	EditorEvent int
)

const (
	EditorEventNone EditorEvent = iota
	EditorEventSubmit
	EditorEventCancel
)

func NewEditor(singleLine, submit bool, alignment text.Alignment) *Editor {
	ret := &Editor{widgetEditor: widget.Editor{SingleLine: singleLine, Submit: submit, Alignment: alignment}}
	for c := 'A'; c <= 'Z'; c++ {
		ret.filters = append(ret.filters, key.Filter{Name: key.Name(c), Focus: &ret.widgetEditor, Optional: key.ModAlt | key.ModShift | key.ModShortcut})
	}
	for c := '0'; c <= '9'; c++ {
		ret.filters = append(ret.filters, key.Filter{Name: key.Name(c), Focus: &ret.widgetEditor, Optional: key.ModAlt | key.ModShift | key.ModShortcut})
	}
	ret.filters = append(ret.filters, key.Filter{Name: key.NameSpace, Focus: &ret.widgetEditor, Optional: key.ModAlt | key.ModShift | key.ModShortcut})
	ret.filters = append(ret.filters, key.Filter{Name: key.NameEscape, Focus: &ret.widgetEditor, Optional: key.ModAlt | key.ModShift | key.ModShortcut})
	return ret
}

func (s *EditorStyle) AsLabelStyle() LabelStyle {
	return LabelStyle{
		Color:    s.Color,
		Font:     s.Font,
		TextSize: s.TextSize,
	}
}

func (e *Editor) Layout(gtx C, str tracker.String, th *Theme, style *EditorStyle, hint string) D {
	for e.Update(gtx, str) != EditorEventNone {
		// just consume all events if the user did not consume them
	}
	if gtx.Focused(&e.widgetEditor) {
		if t, ok := gtx.Values["Tracker"].(*Tracker); ok {
			t.textFocused = true
		}
	}
	if e.widgetEditor.Text() != str.Value() {
		e.widgetEditor.SetText(str.Value())
		l := len(e.widgetEditor.Text())
		e.widgetEditor.SetCaret(l, l)
	}
	me := material.Editor(&th.Material, &e.widgetEditor, hint)
	me.Font = style.Font
	me.TextSize = style.TextSize
	me.Color = style.Color
	me.HintColor = style.HintColor
	return me.Layout(gtx)
}

func (e *Editor) Update(gtx C, str tracker.String) EditorEvent {
	if e.requestFocus {
		e.requestFocus = false
		gtx.Execute(key.FocusCmd{Tag: &e.widgetEditor})
		l := len(e.widgetEditor.Text())
		e.widgetEditor.SetCaret(l, l)
	}
	for {
		ev, ok := e.widgetEditor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.ChangeEvent); ok {
			str.SetValue(e.widgetEditor.Text())
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			return EditorEventSubmit
		}
	}
	for {
		event, ok := gtx.Event(e.filters...)
		if !ok {
			break
		}
		if e, ok := event.(key.Event); ok && e.State == key.Press && e.Name == key.NameEscape {
			return EditorEventCancel
		}
	}
	return EditorEventNone
}

func (e *Editor) Focus() {
	e.requestFocus = true
}

// DraftEditor is a single line Editor that keeps the text being edited to
// itself, and sets the value only when editing ends: on Enter or when the
// editor loses focus. Escape discards the edit. This suits values that are
// invalid while being typed, or expensive to apply. If the value is rejected,
// the text reverts and an alert is shown.
type DraftEditor struct {
	*Editor
	draft  string
	active bool
}

type draftValue struct {
	d      *DraftEditor
	target tracker.String
}

func (v *draftValue) Value() string {
	if v.d.active {
		return v.d.draft
	}
	return v.target.Value()
}

func (v *draftValue) SetValue(value string) bool {
	v.d.draft, v.d.active = value, true
	return true
}

func NewDraftEditor(alignment text.Alignment) *DraftEditor {
	return &DraftEditor{Editor: NewEditor(true, true, alignment)} // a pointer: the key filters refer to its address
}

func (d *DraftEditor) Layout(gtx C, str tracker.String, th *Theme, style *EditorStyle, hint string) D {
	s := tracker.MakeString(&draftValue{d: d, target: str})
loop:
	for {
		switch d.Editor.Update(gtx, s) {
		case EditorEventNone:
			break loop
		case EditorEventSubmit:
			d.commit(gtx, str)
		case EditorEventCancel:
			d.active = false
		}
	}
	if d.active && !gtx.Focused(&d.widgetEditor) {
		d.commit(gtx, str)
	}
	return d.Editor.Layout(gtx, s, th, style, hint)
}

func (d *DraftEditor) commit(gtx C, str tracker.String) {
	if !d.active {
		return
	}
	d.active = false
	if d.draft != str.Value() && !str.SetValue(d.draft) {
		TrackerFromContext(gtx).Alerts().Add(fmt.Sprintf("Invalid value %q", d.draft), tracker.Warning)
	}
}
