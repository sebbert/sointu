package gioui

import (
	"strings"
	"time"

	"github.com/vsariola/sointu/tracker"
)

// A plugin host can show and hide the window of the tracker, as CLAP hosts
// do with the floating window of a plugin (ShowWindow, HideWindow). Without
// a window the model goes on: the tracker's goroutine still runs it, and the
// song plays. The window then also takes the title that the host suggests.
//
// On macOS the window of a plugin floats above the windows of its host while
// the host, or the plugin's own process, is the application in front, and
// goes behind the others when another application comes to the front: see
// hostfront_darwin.go. A plugin often runs in a helper process of the host,
// which is not among the applications to switch to, so without this a window
// behind the host's could only be reached by moving the others away.
type (
	windowControl struct {
		// cmds are what the host asks for, from any goroutine
		cmds chan windowCmd
		// hidden is true while there is no window
		hidden bool
		// hiding is true from when the window is told to close until it is
		// gone, so that closing is not taken for the user's
		hiding bool
		// managed is true once a host shows and hides the window: the user
		// closing it then hides it and tells the host (closed), instead of
		// the window coming back
		managed bool
		closed  func()
		title   string // suggested by the host
		// hostFront tells when the host comes to the front or goes from it,
		// and front is what it last told
		hostFront <-chan bool
		front     bool
		// view is the native view of the window, where the platform has
		// use for it
		view uintptr
	}

	windowCmd struct {
		kind   windowCmdKind
		title  string
		closed func()
	}

	windowCmdKind int
)

const (
	windowNothing windowCmdKind = iota
	windowShow
	windowHide
	windowTitle
	windowManage
)

func (t *Tracker) sendWindowCmd(c windowCmd) {
	select {
	case t.window.cmds <- c:
	default: // the host asks faster than the tracker runs; the last ones are lost
	}
}

// StartHidden makes the tracker start without a window, until ShowWindow.
// Call it before Main.
func (t *Tracker) StartHidden() { t.window.hidden = true }

// ShowWindow opens the window, or raises it if it is open. It can be called
// from any goroutine.
func (t *Tracker) ShowWindow() { t.sendWindowCmd(windowCmd{kind: windowShow}) }

// HideWindow closes the window; the tracker goes on without one. It can be
// called from any goroutine.
func (t *Tracker) HideWindow() { t.sendWindowCmd(windowCmd{kind: windowHide}) }

// SetWindowTitle sets what the title of the window names besides the
// tracker, e.g. the track of the host that the plugin is on. It can be
// called from any goroutine.
func (t *Tracker) SetWindowTitle(title string) {
	t.sendWindowCmd(windowCmd{kind: windowTitle, title: title})
}

// HostManagesWindow tells that a host shows and hides the window from now
// on: when the user closes it, it stays closed until the host shows it, and
// closed is called, from the tracker's goroutine. It can be called from any
// goroutine.
func (t *Tracker) HostManagesWindow(closed func()) {
	t.sendWindowCmd(windowCmd{kind: windowManage, closed: closed})
}

// WindowShown tells if the window is open. Call it on the tracker's
// goroutine.
func (t *Tracker) WindowShown() bool { return !t.window.hidden && !t.window.hiding }

// windowCommand does what a host asked for, as far as it does not need the
// window, and returns what is left to do with the window: windowShow to
// open or raise it, windowHide to close it.
func (t *Tracker) windowCommand(c windowCmd) windowCmdKind {
	w := &t.window
	switch c.kind {
	case windowShow:
		w.hidden, w.hiding = false, false
		return windowShow
	case windowHide:
		if !w.hidden && !w.hiding {
			w.hiding = true
			return windowHide
		}
	case windowTitle:
		w.title = c.title
	case windowManage:
		w.managed, w.closed = true, c.closed
	}
	return windowNothing
}

// windowDestroyed tells that the window is gone: hidden by the host, or
// closed by the user. A window that no host manages comes back.
func (t *Tracker) windowDestroyed() {
	w := &t.window
	switch {
	case w.hiding:
		w.hidden, w.hiding = true, false
	case w.managed && !t.Quitted():
		w.hidden = true
		if w.closed != nil {
			w.closed()
		}
	}
}

// runHidden runs the tracker without a window, until the host shows it or
// the tracker quits.
func (t *Tracker) runHidden(recovery *time.Ticker) {
	for t.window.hidden && !t.Quitted() {
		select {
		case e := <-t.Broker().ToGUI:
			if e, ok := e.(*tracker.MIDIMessage); ok {
				t.midiMsgs = append(t.midiMsgs, e)
				t.playMIDIMessages()
			}
		case e := <-t.Broker().ToModel:
			t.ProcessMsg(e)
		case <-t.Broker().CloseGUI:
			t.ForceQuit().Do()
		case c := <-t.window.cmds:
			t.windowCommand(c)
		case t.window.front = <-t.window.hostFront:
		case <-recovery.C:
			t.History().SaveRecovery()
		}
	}
}

// windowOnTop tells if the window is to float above the others: when the
// user chose so, or while the host of the plugin is in front.
func (t *Tracker) windowOnTop() bool {
	return t.preferences.Window.AlwaysOnTop || t.window.front
}

// windowTitle returns the title of the window: titleFromPath, and what the
// host suggested.
func (t *Tracker) windowTitle(path string, unsaved bool) string {
	title := titleFromPath(path, unsaved)
	if host := strings.TrimSpace(t.window.title); host != "" {
		title += " - " + host
	}
	return title
}
