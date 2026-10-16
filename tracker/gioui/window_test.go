package gioui

import (
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// What a host does with the window: hiding closes it and it stays away
// until shown; closing by the user brings it back, unless a host manages
// the window, which is then told.
func TestWindowControl(t *testing.T) {
	model := tracker.NewModel(tracker.NewBroker(), []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	tr := NewTracker(model)
	w := &tr.window

	// no host: the user closes the window, and it comes back
	tr.windowDestroyed()
	if w.hidden {
		t.Error("a window that no host manages stays closed")
	}
	// the host hides it
	if tr.windowCommand(windowCmd{kind: windowHide}) != windowHide || !w.hiding {
		t.Error("hiding does not close the window")
	}
	if tr.windowCommand(windowCmd{kind: windowHide}) != windowNothing {
		t.Error("hiding twice closes the window twice")
	}
	tr.windowDestroyed()
	if !w.hidden || w.hiding {
		t.Errorf("the hidden window: hidden %v, hiding %v", w.hidden, w.hiding)
	}
	if tr.windowCommand(windowCmd{kind: windowHide}) != windowNothing {
		t.Error("hiding a hidden window does something")
	}
	if tr.windowCommand(windowCmd{kind: windowShow}) != windowShow || w.hidden {
		t.Error("showing does not bring the window back")
	}
	// a host manages it: closing hides it, and the host is told
	closed := 0
	tr.windowCommand(windowCmd{kind: windowManage, closed: func() { closed++ }})
	tr.windowDestroyed()
	if !w.hidden || closed != 1 {
		t.Errorf("the user closed a managed window: hidden %v, the host told %d times", w.hidden, closed)
	}
	tr.windowCommand(windowCmd{kind: windowShow})
	tr.windowCommand(windowCmd{kind: windowHide})
	tr.windowDestroyed()
	if !w.hidden || closed != 1 {
		t.Errorf("the host hid the window: hidden %v, the host told %d times", w.hidden, closed)
	}
	// the title that the host suggests
	if got := tr.windowTitle("", false); got != "Sointu Tracker" {
		t.Errorf("the title without a host: %q", got)
	}
	tr.windowCommand(windowCmd{kind: windowTitle, title: " Bass "})
	if got := tr.windowTitle("song.yml", true); got != "Sointu Tracker - song.yml * - Bass" {
		t.Errorf("the title with what the host suggests: %q", got)
	}
	// on top: by the setting, or while the host is in front
	tr.preferences.Window.AlwaysOnTop = false
	if tr.windowOnTop() {
		t.Error("the window is on top without the setting or the host in front")
	}
	w.front = true
	if !tr.windowOnTop() {
		t.Error("the window is not on top while the host is in front")
	}
}

// A tracker that starts hidden runs the model without a window: messages
// to the model are answered, and closing ends it.
func TestWindowHidden(t *testing.T) {
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	t.Cleanup(model.Close)
	tr := NewTracker(model)
	tr.StartHidden()
	go tr.Main()
	for range 3 {
		done := make(chan struct{})
		if !tracker.TrySend(broker.ToModel, tracker.MsgToModel{Data: func() { close(done) }}) {
			t.Fatal("the message was not sent")
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the hidden tracker did not answer a message to the model")
		}
	}
	tr.HideWindow() // nothing to hide
	tr.SetWindowTitle("Lead")
	tracker.TrySend(broker.CloseGUI, struct{}{})
	select {
	case <-broker.FinishedGUI:
	case <-time.After(5 * time.Second):
		t.Fatal("the hidden tracker did not end")
	}
}
