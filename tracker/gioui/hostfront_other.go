//go:build !(plugin && darwin)

package gioui

import "gioui.org/io/event"

// watchHostFront is for the plugins on macOS: see hostfront_darwin.go.
func (t *Tracker) watchHostFront() {}

func (t *Tracker) unwatchHostFront() {}

func (t *Tracker) platformEvent(event.Event) {}

func (t *Tracker) sendWindowBack() {}
