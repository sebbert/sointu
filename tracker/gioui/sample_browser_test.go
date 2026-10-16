package gioui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestSampleBrowserLayout lays out the Buffers tab with the sample browser
// in a folder, moves through it and drops a folder and files onto the
// window. With SOINTU_TEST_SCREENSHOTS set to a directory, it also draws
// each step into a PNG there.
func TestSampleBrowserLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	samples := filepath.Join(home, "Samples")
	for _, p := range []string{"Drums/kick.wav", "Drums/snare.wav", "Drums/Loops/break 170.wav", "Bass/reese.flac", "pad.ogg", "readme.txt"} {
		path := filepath.Join(samples, p)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, nil, 0o644)
	}
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	if !tr.ShowBrowser().Value() {
		t.Fatal("the browser is not shown by default")
	}
	model.Browser().SetLastFolder(samples)
	model.Instrument().Tab().SetValue(int(tracker.InstrumentBuffersTab))
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
		for range 2 {
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
		f, err := os.Create(filepath.Join(dir, "browser-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, img)
	}
	frame("1-root")
	b := model.Browser()
	if b.Root() != samples {
		t.Fatalf("the browser starts in %q, want the last folder %q", b.Root(), samples)
	}
	// the list takes the keys once focused
	router.Queue(key.FocusEvent{Focus: true})
	ib := tr.PatchPanel.instrBuffers
	router.Frame(&ops)
	ops.Reset()
	gtx := layout.Context{Ops: &ops, Now: time.Now(), Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(size), Values: map[string]any{"Tracker": tr}}
	gtx.Execute(key.FocusCmd{Tag: ib.browser.list})
	tr.Layout(gtx)
	router.Frame(gtx.Ops)
	for _, k := range []key.Name{key.NameDownArrow, key.NameRightArrow} { // Drums, expanded
		router.Queue(key.Event{Name: k, State: key.Press})
		frame("keys")
	}
	if e, _ := b.Selected(); e.Name != "Drums" || !e.Expanded {
		t.Fatalf("after Down and Right got %+v, want Drums expanded", e)
	}
	b.Right()
	b.Right()
	frame("2-expanded")
	tr.dropFiles(app.FileDropEvent{Paths: []string{filepath.Join(samples, "Bass")}})
	if b.Root() != filepath.Join(samples, "Bass") {
		t.Errorf("a dropped folder did not become the root: %q", b.Root())
	}
	frame("3-dropped-folder")
}
