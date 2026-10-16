// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"gioui.org/f32"
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
}

func (FileDropEvent) ImplementsEvent() {}
