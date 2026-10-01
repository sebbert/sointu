package gioui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestModulesTabLayout lays out the tracker while making a module, editing
// it on the Modules tab and binding a parameter, without a window. With
// SOINTU_TEST_SCREENSHOTS set to a directory, it also draws each step into a
// PNG there.
func TestModulesTabLayout(t *testing.T) {
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
	frame("1-instrument")
	units := model.Unit().List()
	units.SetSelected(0)
	units.SetSelected2(2)
	model.Unit().MakeModule().Do()
	frame("2-module-unit")
	model.Unit().OpenModule().Do()
	if !model.Module().Editing() {
		t.Fatal("the Modules tab is not shown")
	}
	model.Module().Name().SetValue("voice")
	model.Module().AddParam().Do()
	model.Module().ParamName(1).SetValue("attack")
	model.Module().AddParam().Do()
	model.Params().SetCursor(tracker.Point{X: 1, Y: 0})
	if !model.Module().ParamBound(1).Enabled() {
		t.Fatal("the attack of the envelope cannot be bound")
	}
	model.Module().ParamBound(1).SetValue(true)
	frame("3-modules-tab")
	model.Instrument().Tab().SetValue(int(tracker.InstrumentEditorTab))
	frame("4-module-unit-with-parameter")
	model.Unit().Unfold().SetValue(true)
	frame("4b-unfolded")
	model.Unit().Unfold().SetValue(false)
	model.Module().Delete().Do()
	model.Instrument().Tab().SetValue(int(tracker.InstrumentModulesTab))
	frame("5-no-modules")
	// the example song
	f, err := os.Open("../../examples/modules.yml")
	if err != nil {
		t.Fatal(err)
	}
	model.Song().Read(f)
	model.Module().List().SetSelected(1)
	model.Params().SetCursor(tracker.Point{X: 1, Y: 3}) // the cutoff of the filter, with a scaled binding
	frame("6-example-modules-tab")
	model.Instrument().Tab().SetValue(int(tracker.InstrumentEditorTab))
	model.Unit().List().SetSelected(2)
	model.Unit().Unfold().SetValue(true)
	frame("7-example-unfolded")
	// a module with an input: the signals pass through the module unit
	model.Instrument().List().SetSelected(1)
	model.Unit().List().SetSelected(1)
	model.Unit().Unfold().SetValue(true)
	frame("8-example-unfolded-with-input")
	// the cursor on an inner unit: the units of the module are edited there
	model.Unit().List().SetSelected(model.Unit().List().Selected() + 2)
	model.Unit().List().SetSelected2(model.Unit().List().Selected())
	if name, _, ok := model.Unit().InModule(); !ok || name == "" {
		t.Fatal("the cursor is not on an inner unit")
	}
	model.Params().SetCursor(tracker.Point{X: 1, Y: model.Params().Cursor().Y})
	frame("9-example-cursor-on-inner-unit")
	// a module used twice: the lead of the first instrument
	model.Instrument().List().SetSelected(0)
	rows := model.Unit().List()
	for i := 0; i < rows.Count(); i++ {
		if item := model.Unit().Item(i); item.Module && !item.Unfolded {
			model.Unit().ToggleUnfold(i).Do()
		}
	}
	for i := 0; i < rows.Count(); i++ {
		if model.Unit().Item(i).Inner {
			model.Params().SetCursor(tracker.Point{X: 0, Y: i + 1})
			break
		}
	}
	frame("10-example-all-unfolded")
}
