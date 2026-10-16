// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
)

// FileDropEvent is sent when files or folders are dropped onto the window
// from the system, e.g. from the Finder. Only the macOS backend sends it; a
// backend of another platform would send the same event.
type FileDropEvent struct {
	// Paths are the absolute paths of the dropped files and folders.
	Paths []string
	// Position is where they were dropped, in pixels from the top left of
	// the window.
	Position f32.Point
	// Modifiers are the modifier keys held at the drop.
	Modifiers key.Modifiers
	// Targets are the tags of the pointer handlers at Position, topmost
	// first, as the last frame placed them: what a press there would reach.
	Targets []event.Tag
}

func (FileDropEvent) ImplementsEvent() {}
