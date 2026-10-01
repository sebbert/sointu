package gioui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
)

func scrollEvent(y float32) pointer.Event {
	return pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, y)}
}

// zoomScrolled zooms like the tracker does, by scroll events dt apart on a
// display of scale 2, and returns the zoom index.
func zoomScrolled(z *zoomScroll, goos string, zoom int, dt time.Duration, events ...pointer.Event) int {
	now := z.last
	for _, e := range events {
		now = now.Add(dt)
		zoom = zoomBy(zoom, z.Add(zoomScrollSteps(goos, e, 2), now))
	}
	return zoom
}

func repeated(e pointer.Event, n int) []pointer.Event {
	ret := make([]pointer.Event, n)
	for i := range ret {
		ret[i] = e
	}
	return ret
}

func TestZoomScrollSmallDeltas(t *testing.T) {
	const frame = 16 * time.Millisecond
	// a trackpad on macOS: 10 px are 5 dp, a tenth of a step
	z := zoomScroll{last: time.Now()}
	if zoom := zoomScrolled(&z, "darwin", 6, frame, repeated(scrollEvent(-10), 9)...); zoom != 6 {
		t.Errorf("45 dp zoomed from 6 to %v", zoom)
	}
	if zoom := zoomScrolled(&z, "darwin", 6, frame, scrollEvent(-10)); zoom != 7 {
		t.Errorf("50 dp zoomed from 6 to %v, want 7", zoom)
	}
	// the remainder is kept: 100 events of 7 px are 350 dp, 7 steps
	z = zoomScroll{last: time.Now()}
	if zoom := zoomScrolled(&z, "darwin", 10, frame, repeated(scrollEvent(7), 100)...); zoom != 3 {
		t.Errorf("350 dp zoomed from 10 to %v, want 3", zoom)
	}
	// a precision touchpad on Windows: 120 make a step, whatever the scale
	z = zoomScroll{last: time.Now()}
	if zoom := zoomScrolled(&z, "windows", 6, frame, repeated(scrollEvent(-8), 45)...); zoom != 9 {
		t.Errorf("a wheel delta of 360 zoomed from 6 to %v, want 9", zoom)
	}
	// a touchpad on Wayland: the distance is not scaled
	z = zoomScroll{last: time.Now()}
	if zoom := zoomScrolled(&z, "linux", 6, frame, repeated(scrollEvent(-2.5), 40)...); zoom != 8 {
		t.Errorf("100 units zoomed from 6 to %v, want 8", zoom)
	}
}

func TestZoomScrollNotches(t *testing.T) {
	// one notch of a wheel is one step, however slowly the wheel turns
	tests := []struct {
		name  string
		goos  string
		event pointer.Event
	}{
		{"macOS, slow", "darwin", pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, -2), Notch: true}},
		{"macOS, fast", "darwin", pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, -340), Notch: true}},
		{"Windows", "windows", scrollEvent(-120)},
		{"X11", "linux", scrollEvent(-10)},
		{"X11 on FreeBSD", "freebsd", scrollEvent(-10)},
		{"Wayland", "linux", scrollEvent(-100)},
		{"Wayland with libinput", "linux", scrollEvent(-150)},
	}
	for _, tt := range tests {
		for _, dt := range []time.Duration{20 * time.Millisecond, 2 * time.Second} {
			z := zoomScroll{last: time.Now()}
			if zoom := zoomScrolled(&z, tt.goos, 6, dt, repeated(tt.event, 3)...); zoom != 9 {
				t.Errorf("%v: 3 notches %v apart zoomed from 6 to %v, want 9", tt.name, dt, zoom)
			}
			tt.event.Scroll.Y = -tt.event.Scroll.Y
			if zoom := zoomScrolled(&z, tt.goos, 6, dt, repeated(tt.event, 2)...); zoom != 4 {
				t.Errorf("%v: 2 notches %v apart zoomed from 6 to %v, want 4", tt.name, dt, zoom)
			}
			tt.event.Scroll.Y = -tt.event.Scroll.Y
		}
	}
}

func TestZoomScrollLargeDelta(t *testing.T) {
	// a free-spinning wheel on Windows sends several notches at once
	var z zoomScroll
	if zoom := zoomScrolled(&z, "windows", 6, time.Millisecond, scrollEvent(-480)); zoom != 10 {
		t.Errorf("4 notches at once zoomed from 6 to %v, want 10", zoom)
	}
	// one event of a trackpad is at most a step, and leaves nothing behind
	z = zoomScroll{}
	if zoom := zoomScrolled(&z, "darwin", 6, time.Millisecond, scrollEvent(-5000)); zoom != 7 {
		t.Errorf("a long event zoomed from 6 to %v, want 7", zoom)
	}
	if z.rest != 0 {
		t.Errorf("a long event left %v", z.rest)
	}
	// but a long scroll is many steps
	z = zoomScroll{}
	if zoom := zoomScrolled(&z, "darwin", 6, time.Millisecond, repeated(scrollEvent(-80), 10)...); zoom != 14 {
		t.Errorf("400 dp zoomed from 6 to %v, want 14", zoom)
	}
}

func TestZoomScrollReversal(t *testing.T) {
	z := zoomScroll{last: time.Now()}
	// 0.9 steps in, then 0.9 out: neither is a step, and nor is their sum
	zoom := zoomScrolled(&z, "darwin", 6, time.Millisecond, scrollEvent(-90), scrollEvent(90))
	if zoom != 6 {
		t.Errorf("zoomed from 6 to %v", zoom)
	}
	if z.rest > -0.89 || z.rest < -0.91 {
		t.Errorf("the rest is %v after reversing, want -0.9", z.rest)
	}
	// the rest of the way out
	if zoom = zoomScrolled(&z, "darwin", zoom, time.Millisecond, scrollEvent(10)); zoom != 5 {
		t.Errorf("zoomed to %v, want 5", zoom)
	}
}

func TestZoomScrollStale(t *testing.T) {
	z := zoomScroll{last: time.Now()}
	zoom := zoomScrolled(&z, "darwin", 6, time.Millisecond, scrollEvent(-90))
	// much later, 0.2 steps more are not a step
	if zoom = zoomScrolled(&z, "darwin", zoom, 2*zoomScrollTimeout, scrollEvent(-20)); zoom != 6 {
		t.Errorf("a stale scroll zoomed to %v", zoom)
	}
	// and neither after the modifier was released
	zoom = zoomScrolled(&z, "darwin", zoom, time.Millisecond, scrollEvent(-70))
	z.Reset()
	if zoom = zoomScrolled(&z, "darwin", zoom, time.Millisecond, scrollEvent(-20)); zoom != 6 {
		t.Errorf("a scroll after a reset zoomed to %v", zoom)
	}
	// scrolling sideways changes nothing
	before := z
	if n := z.Add(zoomScrollSteps("darwin", pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(30, 0)}, 2), z.last.Add(time.Hour)); n != 0 || z != before {
		t.Errorf("a horizontal scroll stepped by %v and changed %v to %v", n, before, z)
	}
}

func TestZoomScrollLimits(t *testing.T) {
	last := len(ZoomFactors) - 1
	var z zoomScroll
	if zoom := zoomScrolled(&z, "windows", last-1, time.Millisecond, repeated(scrollEvent(-120), 5)...); zoom != last {
		t.Errorf("zoomed in to %v, want %v", zoom, last)
	}
	// nothing piled up past the limit: one notch back is one step
	if zoom := zoomScrolled(&z, "windows", last, time.Millisecond, scrollEvent(120)); zoom != last-1 {
		t.Errorf("zoomed out to %v, want %v", zoom, last-1)
	}
	if zoom := zoomScrolled(&z, "windows", 1, time.Millisecond, scrollEvent(2400)); zoom != 0 {
		t.Errorf("zoomed out to %v, want 0", zoom)
	}
	if zoom := zoomScrolled(&z, "windows", 0, time.Millisecond, scrollEvent(-120)); zoom != 1 {
		t.Errorf("zoomed in to %v, want 1", zoom)
	}
}

// The tracker's scroll handler covers everything else: it must take all of
// the scroll while the modifier is held and none of it otherwise, and let
// the other pointer events through.
func TestZoomScrollTakesScrollWithModifier(t *testing.T) {
	wide := pointer.ScrollRange{Min: -1e6, Max: 1e6}
	for _, modifier := range []bool{false, true} {
		var r input.Router
		var ops op.Ops
		var top, list int // the tags
		topFilter := pointer.Filter{Target: &top, Kinds: pointer.Scroll, ScrollY: zoomScrollRange(modifier)}
		listFilter := pointer.Filter{Target: &list, Kinds: pointer.Scroll | pointer.Press, ScrollY: wide}
		window := clip.Rect(image.Rect(0, 0, 200, 200)).Push(&ops)
		area := clip.Rect(image.Rect(50, 50, 150, 150)).Push(&ops)
		event.Op(&ops, &list)
		area.Pop()
		pass := pointer.PassOp{}.Push(&ops)
		area = clip.Rect(image.Rect(0, 0, 200, 200)).Push(&ops)
		event.Op(&ops, &top)
		area.Pop()
		pass.Pop()
		window.Pop()
		r.Event(topFilter)
		r.Event(listFilter)
		r.Frame(&ops)
		var mods key.Modifiers
		if modifier {
			mods = key.ModShortcut
		}
		r.Queue(
			pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(100, 100), Scroll: f32.Pt(0, 300), Modifiers: mods},
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(100, 100)},
		)
		events := func(f pointer.Filter) (scrolled float32, scrolls, presses int, held bool) {
			for {
				e, ok := r.Event(f)
				if !ok {
					return
				}
				if e, ok := e.(pointer.Event); ok {
					switch e.Kind {
					case pointer.Scroll:
						scrolled += e.Scroll.Y
						scrolls++
						held = e.Modifiers.Contain(key.ModShortcut)
					case pointer.Press:
						presses++
					}
				}
			}
		}
		want := float32(0)
		if modifier {
			want = 300
		}
		// the top handler gets the event without the modifier too, with no
		// distance: the tracker reads the modifier from it
		if got, n, _, held := events(topFilter); got != want || n != 1 || held != modifier {
			t.Errorf("modifier %v: the top handler got %v in %d events, modifier %v, want %v in 1", modifier, got, n, held, want)
		}
		if got, _, presses, _ := events(listFilter); got != 300-want || presses != 1 {
			t.Errorf("modifier %v: the list got %v and %d presses, want %v and 1", modifier, got, presses, 300-want)
		}
	}
	if zoomModifierKey("darwin") != key.NameCommand || zoomModifierKey("windows") != key.NameCtrl || zoomModifierKey("linux") != key.NameCtrl {
		t.Errorf("wrong key for the modifier")
	}
}

// The momentum of a gesture does not zoom, and the momentum of a gesture that
// zoomed is taken until it ends, also after the modifier is released.
func TestZoomScrollMomentum(t *testing.T) {
	now := time.Now()
	tick := func() time.Time { now = now.Add(16 * time.Millisecond); return now }
	scroll := func(mods key.Modifiers, momentum bool) pointer.Event {
		return pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, -200), Modifiers: mods, Momentum: momentum}
	}
	var z zoomScroll
	if z.Taking(now) {
		t.Errorf("takes the scroll without the modifier")
	}
	z.SetModifier(true)
	if !z.Taking(now) {
		t.Errorf("does not take the scroll with the modifier")
	}
	if n := z.Scroll("darwin", scroll(key.ModShortcut, false), 2, tick()); n != 1 {
		t.Errorf("a scroll of 100 dp zoomed %d steps, want 1", n)
	}
	for i := 0; i < 20; i++ {
		if n := z.Scroll("darwin", scroll(key.ModShortcut, true), 2, tick()); n != 0 {
			t.Fatalf("momentum zoomed %d steps", n)
		}
	}
	z.SetModifier(false)
	if _, ok := z.Expires(); !ok || !z.Taking(tick()) {
		t.Errorf("the momentum is not taken after releasing the modifier")
	}
	if n := z.Scroll("darwin", scroll(0, true), 2, tick()); n != 0 || !z.Taking(now) {
		t.Errorf("momentum without the modifier: %d steps, taking %v", n, z.Taking(now))
	}
	// the momentum has ended
	if at, _ := z.Expires(); z.Taking(at) || z.Taking(now.Add(time.Second)) {
		t.Errorf("still taking the scroll after the momentum has ended")
	}
	// a new gesture without the modifier, arriving while it is still taken
	z.Scroll("darwin", scroll(0, false), 2, tick())
	if _, ok := z.Expires(); ok || z.Taking(now) {
		t.Errorf("still taking the scroll after a gesture without the modifier")
	}
	// the momentum of a gesture that did not zoom is left alone
	z = zoomScroll{}
	if n := z.Scroll("darwin", scroll(0, true), 2, tick()); n != 0 || z.Taking(now) {
		t.Errorf("momentum of another gesture: %d steps, taking %v", n, z.Taking(now))
	}
	// a modifier whose key event was missed is found in the scroll event
	if z.Scroll("darwin", scroll(key.ModShortcut, false), 2, tick()); !z.Taking(now) {
		t.Errorf("the modifier of the event is not taken")
	}
}
