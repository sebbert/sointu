package gioui

import (
	"slices"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// The mouse on the waveform: ctrl/cmd+click adds a marker, dragging one
// moves it on release, right-click deletes it, shift+drag selects.
func TestWaveformSliceEditing(t *testing.T) {
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	tr := NewTracker(model)
	if _, err := model.Remote().AddBuffer("rec", 1, 1); err != nil {
		t.Fatal(err)
	}
	b := tr.Buffer()
	bs := newBufferSlices()
	plot := NewPlot(plotRange{0, 1}, plotRange{-1, 1}, 0)
	bs.decorate(tr, plot, 44100)
	if plot.Editor == nil {
		t.Fatal("the waveform of a recording buffer is not editable")
	}
	const pxPerX = 1000 // a plot 1000 pixels wide
	ev := func(kind pointer.Kind, x float32, buttons pointer.Buttons, mods key.Modifiers) bool {
		return bs.PlotPointer(pointer.Event{Kind: kind, Buttons: buttons, Modifiers: mods}, x, pxPerX)
	}
	if !ev(pointer.Press, 0.5, pointer.ButtonPrimary, key.ModShortcut) {
		t.Fatal("ctrl+click was not taken")
	}
	if got := b.Slices(); !slices.Equal(got, []int{22050}) {
		t.Fatalf("after ctrl+click: %v", got)
	}
	if ev(pointer.Press, 0.3, pointer.ButtonPrimary, 0) {
		t.Error("a press away from the markers is taken, and does not pan")
	}
	ev(pointer.Release, 0.3, 0, 0)
	if !ev(pointer.Press, 0.502, pointer.ButtonPrimary, 0) {
		t.Fatal("a press on the marker does not grab it")
	}
	ev(pointer.Drag, 0.25, pointer.ButtonPrimary, 0)
	if got := b.Slices(); !slices.Equal(got, []int{22050}) {
		t.Errorf("the marker moved before the release: %v", got)
	}
	ev(pointer.Release, 0.25, 0, 0)
	if got := b.Slices(); !slices.Equal(got, []int{11025}) {
		t.Fatalf("after the drag: %v", got)
	}
	ev(pointer.Press, 0.75, pointer.ButtonPrimary, key.ModShift)
	ev(pointer.Drag, 0.5, pointer.ButtonPrimary, key.ModShift)
	ev(pointer.Release, 0.5, 0, key.ModShift)
	if a, e, ok := b.Selection(); !ok || a != 22050 || e != 33075 {
		t.Errorf("the selection: %d to %d, %v", a, e, ok)
	}
	ev(pointer.Press, 0.25, pointer.ButtonSecondary, 0)
	if got := b.Slices(); len(got) != 0 {
		t.Errorf("after right-click: %v", got)
	}
}
