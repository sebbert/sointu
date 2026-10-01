package gioui

import (
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// What Gio (third_party/gio) reports in pointer.Event.Scroll.Y, per platform:
//
//   - macOS (app/os_macos.m, os_macos.go): scrollingDeltaY times the backing
//     scale. Trackpads, Magic Mice and smooth wheels have precise deltas:
//     pixels, in many small events, followed by the momentum of the gesture.
//     A notched wheel counts rows, which Gio takes as 10 points each; macOS
//     scales the rows by the speed of the wheel, from 0.1 for a slow notch
//     (1 point) to several rows. So the distance tells nothing about the
//     number of notches, and the events are marked with Event.Notch instead
//     (added to the vendored Gio).
//   - Windows (app/os_windows.go): the wheel delta of WM_MOUSEWHEEL as it is,
//     120 for a notch, whatever the display scale. Precision touchpads and
//     free-spinning wheels send fractions and multiples of 120.
//   - X11 (app/os_x11.go): wheels are buttons 4 and 5; every notch is
//     exactly 10. There is no smooth scrolling.
//   - Wayland (app/os_wayland.go): the axis value, in surface coordinates;
//     times 10 for a wheel, whose notches then are 100 to 150, depending on
//     the compositor. Touchpads give small distances and a fling.
//
// The router (io/input/pointer.go, clampScroll) offers a scroll to the
// handlers under the pointer from the topmost down. Each takes what fits
// the ScrollRange of its filter and leaves the rest for the next one, so a
// range of -1 to 1 turned every event into a whole unit. The tracker's
// handler is the topmost one, so that Ctrl/Cmd+scroll zooms wherever the
// pointer is: its range is wide while the modifier is held, taking the
// scroll from the lists, parameters and plots below it, and empty otherwise,
// leaving all of it to them. The range is set before the event arrives, so
// the tracker follows the key of the modifier (zoomModifierKey).
const (
	// zoomScrollDp is how far to scroll for one step of ZoomFactors, where
	// the distance is known. The steps are 0.2 apart on average on the log
	// scale, so this is about as fast as the plots zoom (zoomPerPx).
	zoomScrollDp = 50
	// zoomWheelDelta is a notch of a wheel on Windows (WHEEL_DELTA).
	zoomWheelDelta = 120
	// zoomX11Notch is a notch of a wheel on X11 (scrollScale in Gio).
	zoomX11Notch = 10
	// maxZoomStepsPerEvent limits the distance of one event, like
	// maxZoomPerEvent of the plots: a notch of a wheel on Wayland is longer
	// than zoomScrollDp, and should still be one step.
	maxZoomStepsPerEvent = 1
	// zoomScrollTimeout is how long a scroll too short for a step is kept.
	zoomScrollTimeout = 500 * time.Millisecond
	// zoomScrollSlack makes up for rounding errors of the sum, so that e.g.
	// ten tenths of a step make a step.
	zoomScrollSlack = 1e-3
)

// zoomScrollRange returns the scroll range of the tracker's handler: all of
// the scroll while the modifier is held, none of it otherwise.
func zoomScrollRange(modifier bool) pointer.ScrollRange {
	if modifier {
		return pointer.ScrollRange{Min: -1e6, Max: 1e6}
	}
	return pointer.ScrollRange{}
}

// zoomModifierKey returns the key of key.ModShortcut on the platform goos
// (runtime.GOOS).
func zoomModifierKey(goos string) key.Name {
	if goos == "darwin" || goos == "ios" {
		return key.NameCommand
	}
	return key.NameCtrl
}

// zoomScrollSteps returns how many steps of ZoomFactors the scroll event is
// worth, on the platform goos (runtime.GOOS): positive zooms in. pxPerDp is
// the scale of the display, without the zoom of the UI.
func zoomScrollSteps(goos string, e pointer.Event, pxPerDp float32) float32 {
	d := -e.Scroll.Y
	if d == 0 {
		return 0
	}
	notch := float32(1)
	if d < 0 {
		notch = -1
	}
	switch goos {
	case "windows":
		return d / zoomWheelDelta
	case "darwin":
		if e.Notch {
			return notch
		}
		d /= max(pxPerDp, 1e-3) // the distance is in pixels
	default:
		// X11 or Wayland, where the distance does not follow the scale
		if d == zoomX11Notch || d == -zoomX11Notch {
			return notch
		}
	}
	return min(max(d/zoomScrollDp, -maxZoomStepsPerEvent), maxZoomStepsPerEvent)
}

// zoomScroll adds up the scrolling that zooms the UI, until it is enough for
// a step.
type zoomScroll struct {
	rest float32   // scrolled since the last step, in steps
	last time.Time // when it was last scrolled
}

// Add scrolls by the given number of steps, which can be a fraction, and
// returns how many whole steps to take now. The rest is kept for the next
// call, unless that scrolls the other way or comes too late.
func (z *zoomScroll) Add(steps float32, now time.Time) int {
	if steps == 0 {
		return 0
	}
	if z.rest*steps < 0 || now.Sub(z.last) > zoomScrollTimeout {
		z.rest = 0
	}
	z.last = now
	z.rest += steps
	slack := float32(zoomScrollSlack)
	if z.rest < 0 {
		slack = -slack
	}
	n := int(z.rest + slack)
	z.rest -= float32(n)
	return n
}

// Reset forgets the scrolling that was not enough for a step.
func (z *zoomScroll) Reset() { z.rest = 0 }

// zoomBy returns the index in ZoomFactors that is the given number of steps
// from zoom, within the range.
func zoomBy(zoom, steps int) int {
	return min(max(zoom+steps, 0), len(ZoomFactors)-1)
}
