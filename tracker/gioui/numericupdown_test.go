package gioui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

type testIntValue struct{ v int }

func (t *testIntValue) Value() int                    { return t.v }
func (t *testIntValue) SetValue(v int) bool           { t.v = v; return true }
func (t *testIntValue) Range() tracker.RangeInclusive { return tracker.RangeInclusive{Min: 0, Max: 200} }

// A click on the number of a NumericUpDown types a value: Enter sets it,
// clamped to the range, Escape discards it, and a drag still changes it
// without typing.
func TestNumericUpDownTyping(t *testing.T) {
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	val := &testIntValue{v: 10}
	state := NewNumericUpDownState()
	var router input.Router
	var ops op.Ops
	clock := time.Duration(0)
	frame := func() {
		for range 2 {
			ops.Reset()
			gtx := layout.Context{Ops: &ops, Now: time.Now(), Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Constraints{Max: image.Pt(400, 100)}, Values: map[string]any{"Tracker": tr}}
			n := NumUpDown(tracker.MakeInt(val), tr.Theme, state, "")
			n.Layout(gtx)
			router.Frame(gtx.Ops)
		}
	}
	center := f32.Pt(float32(tr.Theme.NumericUpDown.Width)/2, float32(tr.Theme.NumericUpDown.Height)/2)
	ptr := func(kind pointer.Kind, pos f32.Point) {
		clock += 50 * time.Millisecond
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: clock})
		frame()
	}
	click := func() {
		ptr(pointer.Press, center)
		ptr(pointer.Release, center)
	}
	typ := func(s string) {
		for _, r := range s { // as the window inserts typed text
			a, b := state.editor.widgetEditor.Selection()
			start := min(a, b)
			router.Queue(key.EditEvent{Range: key.Range{Start: start, End: max(a, b)}, Text: string(r)})
			router.Queue(key.SelectionEvent{Start: start + 1, End: start + 1})
			frame()
		}
	}
	press := func(name key.Name) {
		router.Queue(key.Event{Name: name, State: key.Press})
		frame()
	}
	frame()
	click()
	if !state.editing {
		t.Fatal("a click did not start typing")
	}
	typ("123")
	press(key.NameReturn)
	if val.v != 123 || state.editing {
		t.Errorf("after typing 123 and Enter: %d, editing %v", val.v, state.editing)
	}
	click()
	typ("999")
	press(key.NameReturn)
	if val.v != 200 {
		t.Errorf("999 was not clamped to 200: %d", val.v)
	}
	click()
	typ("5")
	press(key.NameEscape)
	if val.v != 200 || state.editing {
		t.Errorf("Escape set the value: %d, editing %v", val.v, state.editing)
	}
	ptr(pointer.Press, center)
	ptr(pointer.Move, center.Add(f32.Pt(-40, 0)))
	ptr(pointer.Release, center.Add(f32.Pt(-40, 0)))
	if val.v != 196 || state.editing {
		t.Errorf("after a drag: %d, editing %v", val.v, state.editing)
	}
}
