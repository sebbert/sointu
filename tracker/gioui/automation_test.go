package gioui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestAutomationNameField lays out the tracker with an automated parameter
// under the cursor, without a window: the toolbar of the unit editor then
// has the field for the name of its slot, which is in the order of the
// focus only then. With SOINTU_TEST_SCREENSHOTS set to a directory, it also
// draws each step into a PNG there.
func TestAutomationNameField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	size := image.Pt(1500, 1000)
	var ops op.Ops
	var router input.Router
	dir := os.Getenv("SOINTU_TEST_SCREENSHOTS")
	var window *headless.Window
	if dir != "" {
		var err error
		if window, err = headless.NewWindow(size.X, size.Y); err != nil {
			t.Skipf("no headless window: %v", err)
		}
		defer window.Release()
	}
	frame := func(name string) {
		t.Helper()
		for range 2 { // the second frame sees what the first one changed
			ops.Reset()
			gtx := layout.Context{Ops: &ops, Now: time.Now(), Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Exact(size), Values: map[string]any{"Tracker": tr}}
			tr.Layout(gtx)
			router.Frame(gtx.Ops)
		}
		if window == nil {
			return
		}
		if err := window.Frame(&ops); err != nil {
			t.Fatalf("%v: %v", name, err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatalf("%v: %v", name, err)
		}
		f, err := os.Create(dir + "/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, img)
	}
	ie := &tr.PatchPanel.instrEditor
	frame("automation-1-plain")
	if ie.automationShown {
		t.Error("the name field is shown without a lane")
	}
	// the first parameter under the cursor that can be automated
	auto := model.Automation().Automated()
	found := false
	for y := 0; y < 8 && !found; y++ {
		for x := 0; x < 8 && !found; x++ {
			model.Params().SetCursor(tracker.Point{X: x, Y: y})
			found = auto.Enabled()
		}
	}
	if !found {
		t.Fatal("no parameter of the default instrument can be automated")
	}
	auto.SetValue(true)
	frame("automation-2-automated")
	if !ie.automationShown {
		t.Error("the name field is not shown for an automated parameter")
	}
	if !model.Automation().Name().SetValue("Sweep") {
		t.Error("the slot cannot be named")
	}
	frame("automation-3-named")
	if got := ie.automationEditor.widgetEditor.Text(); got != "Sweep" {
		t.Errorf("the field shows %q", got)
	}
	auto.SetValue(false)
	frame("automation-4-removed")
	if ie.automationShown {
		t.Error("the name field is shown after the lane was removed")
	}
}

// TestAutomationAmountKnob lays out the cell of an automated parameter and
// drives its small amount knob and its context menu with the pointer.
func TestAutomationAmountKnob(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	auto := model.Automation().Automated()
	var point tracker.Point
	found := false
	for y := 0; y < 8 && !found; y++ {
		for x := 0; x < 8 && !found; x++ {
			point = tracker.Point{X: x, Y: y}
			model.Params().SetCursor(point)
			item := model.Params().Item(point)
			found = auto.Enabled() && item.Type() == tracker.IntegerParameter
		}
	}
	if !found {
		t.Fatal("no knob of the default instrument can be automated")
	}
	auto.SetValue(true)
	size := image.Pt(60, 70)
	state := &ParamState{}
	var ops op.Ops
	var router input.Router
	now := time.Now()
	clock := time.Second // a press at 0 would be a double click
	frame := func() {
		for range 2 {
			ops.Reset()
			gtx := layout.Context{Ops: &ops, Now: now.Add(clock), Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Exact(size), Values: map[string]any{"Tracker": tr}}
			Param(model.Params().Item(point), tr.Theme, state, true, false).Layout(gtx)
			router.Frame(gtx.Ops)
		}
	}
	ptr := func(kind pointer.Kind, buttons pointer.Buttons, pos f32.Point) {
		clock += 50 * time.Millisecond
		router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: buttons, Position: pos, Time: clock})
		frame()
	}
	amount := func() float64 {
		item := model.Params().Item(point)
		a, _ := model.Automation().AmountOf(&item)
		return a
	}
	item := model.Params().Item(point)
	model.Automation().SetAmountOf(&item, 0)
	frame()
	knob := f32.Pt(51, 61)
	ptr(pointer.Press, pointer.ButtonPrimary, knob)
	ptr(pointer.Move, pointer.ButtonPrimary, knob.Add(f32.Pt(0, -20)))
	ptr(pointer.Release, 0, knob.Add(f32.Pt(0, -20)))
	if a := amount(); a != 0.25 {
		t.Errorf("dragged 20 up: amount %v, want 0.25", a)
	}
	ptr(pointer.Press, pointer.ButtonSecondary, knob)
	ptr(pointer.Release, 0, knob)
	if a := amount(); a != 1 {
		t.Errorf("right click: amount %v, want 1", a)
	}
	if state.ctxMenu.visible {
		t.Error("a right click on the amount knob opened the menu")
	}
	clock += 50 * time.Millisecond
	router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: knob, Scroll: f32.Pt(0, 1), Time: clock})
	frame()
	if a := amount(); a != 63.0/64 {
		t.Errorf("scrolled down: amount %v, want 63/64", a)
	}
	// a right click on the knob opens the context menu
	ptr(pointer.Press, pointer.ButtonSecondary, f32.Pt(30, 35))
	ptr(pointer.Release, 0, f32.Pt(30, 35))
	if !state.ctxMenu.visible {
		t.Error("a right click did not open the menu")
	}
}
