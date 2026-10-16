//go:build plugin

package gioui

import (
	"time"

	"gioui.org/app"
)

// A plugin draws on the main thread of its host, and a frame that waits
// there for the one before to be shown holds the host up: in Ableton Live
// the tracker had three quarters of the main thread, most of it waiting.
// So the frames are not shown in step with the display, and there are at
// most 30 of them a second.
func init() {
	app.MinFrameInterval = time.Second / 30
	app.PresentAsync = true
}
