package plugin

import (
	"math"
	"math/rand"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/resample"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

type testHost struct{ rate float64 }

func (testHost) BPM() (float64, bool)          { return 0, false }
func (h testHost) SampleRate() (float64, bool) { return h.rate, h.rate > 0 }

// stepSong is a song whose instrument puts out a constant while a note is
// held, from the frame of the note on: a step, whose place in the output
// tells when the note was played.
func stepSong() sointu.Song {
	return sointu.Song{
		BPM:         100,
		RowsPerBeat: 4,
		Score:       sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1}}},
		Patch: sointu.Patch{{Name: "step", NumVoices: 1, Units: []sointu.Unit{
			{ID: 1, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 0, "decay": 0, "sustain": 128, "release": 0, "gain": 64}},
			{ID: 2, Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
		}}},
	}
}

// event is a MIDI message at a frame of the host, counted from the start.
type event struct {
	frame int
	data  [3]byte
}

func newPlayer(t *testing.T) *tracker.Player {
	t.Helper()
	broker := tracker.NewBroker()
	player := tracker.NewPlayer(broker, vm.GoSynther{})
	broker.ToPlayer <- stepSong()
	return player
}

// play renders frames frames at the rate of the host in blocks of the sizes
// that size returns, with the events in the blocks they fall into, and
// returns the left channel.
func play(t *testing.T, rate, frames int, size func() int, events []event) ([]float32, *output) {
	t.Helper()
	o := &output{player: newPlayer(t)}
	host := testHost{float64(rate)}
	if rate > 0 {
		o.SetSampleRate(float64(rate)) // as hosts do before they process
	}
	var left []float32
	for len(left) < frames {
		n := min(size(), frames-len(left))
		for _, e := range events {
			if e.frame >= len(left) && e.frame < len(left)+n {
				o.MIDI(e.frame-len(left), 0, e.data)
			}
		}
		l, r := make([]float32, n), make([]float32, n)
		o.Process(l, r, host)
		for i := range l {
			if l[i] != r[i] {
				t.Fatalf("frame %d: left %v, right %v", len(left)+i, l[i], r[i])
			}
		}
		left = append(left, l...)
	}
	return left, o
}

func noteOn(frame int) event  { return event{frame, [3]byte{0x90, 60, 100}} }
func noteOff(frame int) event { return event{frame, [3]byte{0x80, 60, 0}} }

var testRates = []int{44100, 48000, 88200, 96000, 192000, 32000, 22050, 47999}

// TestBlockSizes checks that the output does not depend on how the host
// cuts it into blocks, to the bit: notes at the first and the last frame of
// a block, in blocks of one frame and of none, come out as in large blocks.
func TestBlockSizes(t *testing.T) {
	for _, rate := range testRates {
		rnd := rand.New(rand.NewSource(int64(rate)))
		frames := rate / 4
		var events []event
		for f := 300; f < frames-600; f += 2048 { // 2048 and 2559 are edges of blocks of 512
			events = append(events, noteOn(f), noteOff(f+511), noteOn(f+512), noteOff(f+700+rnd.Intn(300)))
		}
		want, _ := play(t, rate, frames, func() int { return 512 }, events)
		peak := float32(0)
		for _, v := range want {
			peak = max(peak, v)
		}
		if peak < 0.2 {
			t.Fatalf("%d Hz: the notes did not play: peak %v", rate, peak)
		}
		for name, size := range map[string]func() int{
			"1":            func() int { return 1 },
			"0 to 3":       func() int { return rnd.Intn(4) },
			"0 to 700":     func() int { return rnd.Intn(701) },
			"4096":         func() int { return 4096 },
			"64 and 1 000": func() int { return []int{64, 1000}[rnd.Intn(2)] },
		} {
			got, _ := play(t, rate, frames, size, events)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%d Hz, blocks of %s: frame %d is %v, in blocks of 512 %v", rate, name, i, got[i], want[i])
				}
			}
		}
	}
}

// crossing returns where signal first rises through level, between frames.
func crossing(signal []float32, level float32) float64 {
	for i := 1; i < len(signal); i++ {
		if signal[i-1] < level && signal[i] >= level {
			return float64(i-1) + float64(level-signal[i-1])/float64(signal[i]-signal[i-1])
		}
	}
	return math.NaN()
}

// TestNoteTiming checks that a note at a frame of the host is heard at
// that frame plus the latency: the synth plays it at its first frame at or
// after that time, so less than a frame of the synth late and never early.
func TestNoteTiming(t *testing.T) {
	// when the step of a note at frame 5000 comes at 44100 Hz, where the
	// frames go to the host as they are
	direct, o := play(t, 44100, 8000, func() int { return 512 }, []event{noteOn(5000)})
	if o.resampler != nil || o.Latency() != 0 {
		t.Fatalf("at 44100 Hz there is a resampler, or a latency of %d", o.Latency())
	}
	level := direct[7000] / 2
	first := 0
	for direct[first] < level {
		first++
	}
	if first != 5000 && first != 5001 {
		t.Fatalf("at 44100 Hz the note at frame 5000 starts at frame %d", first)
	}
	for _, rate := range testRates[1:] {
		rnd := rand.New(rand.NewSource(int64(rate)))
		worst := 0.0
		for _, frame := range []int{0, 1, 511, 512, 513, 1000, 4096, 5000 + rnd.Intn(1000), 7777} {
			for _, size := range []func() int{func() int { return 512 }, func() int { return rnd.Intn(300) }} {
				got, o := play(t, rate, frame+rate/10, size, []event{noteOn(frame)})
				latency := resample.Latency(44100, rate)
				if o.Latency() != latency || o.resampler.Latency() != latency {
					t.Fatalf("%d Hz: latency %d, want %d", rate, o.Latency(), latency)
				}
				// A step at a frame of the synth rises through half its
				// height half a frame before it.
				at := crossing(got, level) + (0.5-float64(first-5000))*float64(rate)/44100
				late := at - float64(frame+latency) // in frames of the host
				worst = max(worst, late)
				if late < -0.06 || late >= float64(rate)/44100+0.06 { // the crossing is found to about 0.06 frames
					t.Errorf("%d Hz: the note at frame %d is heard %.3f frames after frame %d (latency %d)", rate, frame, late, frame+latency, latency)
				}
			}
		}
		t.Logf("%d Hz: latency %d frames, notes at most %.2f frames (%.1f µs) late", rate, resample.Latency(44100, rate), worst, worst/float64(rate)*1e6)
	}
}

// TestNoResampling checks that at 44100 Hz the host gets the frames of the
// player as they are, with the notes at their frames: what a player gives
// that is played without the plugin.
func TestNoResampling(t *testing.T) {
	events := []event{noteOn(100), noteOff(1023), noteOn(1024), noteOff(3000), noteOn(3001)}
	for _, known := range []bool{true, false} { // a host that tells its rate, and one that does not
		rate := 0
		if known {
			rate = 44100
		}
		got, o := play(t, rate, 6000, func() int { return 512 }, events)
		if o.resampler != nil {
			t.Fatal("there is a resampler")
		}
		player := newPlayer(t)
		buf := make(sointu.AudioBuffer, 512)
		for start := 0; start < 6000; start += 512 {
			n := min(512, 6000-start)
			for _, e := range events {
				if e.frame >= start && e.frame < start+n {
					player.EmitMIDIMsg(&tracker.MIDIMessage{Timestamp: int64(e.frame), Data: e.data, Source: t})
				}
			}
			player.Process(buf[:n], testHost{})
			for i, f := range buf[:n] {
				if got[start+i] != f[0] {
					t.Fatalf("frame %d is %v, the player gives %v", start+i, got[start+i], f[0])
				}
			}
		}
	}
}

// TestSetSampleRate checks the latency that the plugin tells the host, and
// that it plays at the rate it was last told.
func TestSetSampleRate(t *testing.T) {
	o := &output{player: newPlayer(t)}
	for _, c := range []struct {
		rate    float64
		latency int
		actual  int
	}{{48000, 40, 48000}, {0, 40, 48000}, {44100, 0, 44100}, {96000, 79, 96000}, {math.NaN(), 79, 96000}, {1e9, 79, 96000}, {44099.9999, 0, 44100}} {
		if got := o.SetSampleRate(c.rate); got != c.latency || o.Latency() != c.latency {
			t.Errorf("SetSampleRate(%v) returns the latency %d, Latency() %d, want %d", c.rate, got, o.Latency(), c.latency)
		}
		l, r := make([]float32, 100), make([]float32, 100)
		o.Process(l, r, testHost{}) // a host that tells nothing while processing
		if o.rate != c.actual || (o.resampler != nil) != (c.actual != 44100) {
			t.Errorf("after SetSampleRate(%v) the plugin plays at %d Hz, resampling: %v", c.rate, o.rate, o.resampler != nil)
		}
	}
}

// TestParam checks that a value that the host gives a parameter moves the
// parameter that its automation slot is bound to from the frame of the
// event on, also when the host does not run at 44100 Hz, and that the
// modulation of the host is added to it.
func TestParam(t *testing.T) {
	song := stepSong()
	song.Patch[0].Units = []sointu.Unit{
		{ID: 1, Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
		{ID: 2, Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}
	song.Automation = sointu.Automation{{Slot: 7, Targets: []sointu.LaneTarget{{Unit: 1, Param: "value", Amount: 1}}}}
	expanded, _ := song.Expand()
	for _, rate := range []int{44100, 48000} {
		broker := tracker.NewBroker()
		player := tracker.NewPlayer(broker, vm.GoSynther{})
		broker.ToPlayer <- expanded
		o := &output{player: player}
		o.SetSampleRate(float64(rate))
		host := testHost{float64(rate)}
		var left []float32
		block := func(n int) {
			l, r := make([]float32, n), make([]float32, n)
			o.Process(l, r, host)
			left = append(left, l...)
		}
		block(512)
		o.Param(100, 7, 32, false) // at frame 612: to 32 steps, 0.5 at the output
		o.Param(100, 8, 99, false) // another slot
		o.Param(0, NumParams, 1, false)
		block(512)
		block(512)
		o.Param(0, 7, 16, true) // at frame 1536: 16 steps of modulation more
		block(512)
		late := 0
		if rate != 44100 {
			late = resample.Latency(resample.SynthRate, rate)
		}
		at := crossing(left, 0.25) - float64(late)
		ramp := 64 * float64(rate) / 44100 // half of the ramp of the synth
		if math.Abs(at-(612+ramp)) > 2 {
			t.Errorf("%d Hz: the value is half way at frame %.1f, want %.1f", rate, at, 612+ramp)
		}
		if got := left[1500]; math.Abs(float64(got)-0.5) > 1e-3 {
			t.Errorf("%d Hz: frame 1500 is %v, want 0.5", rate, got)
		}
		if got := left[2047]; math.Abs(float64(got)-0.75) > 1e-3 {
			t.Errorf("%d Hz: with the modulation, %v, want 0.75", rate, got)
		}
		if got := o.ParamValue(7); got != 32 {
			t.Errorf("ParamValue = %v, want 32: the value without the modulation", got)
		}
	}
}

// TestParamUnipolar checks that the host moves a unipolar slot from 0 steps
// at the bottom of its range, that the text of its values is in those
// steps, and that the values of the host apply again when the slot becomes
// bipolar.
func TestParamUnipolar(t *testing.T) {
	song := stepSong()
	song.Patch[0].Units = []sointu.Unit{
		{ID: 1, Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
		{ID: 2, Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}
	song.Automation = sointu.Automation{{Slot: 7, Unipolar: true, Targets: []sointu.LaneTarget{{Unit: 1, Param: "value", Amount: 1}}}}
	expanded, _ := song.Expand()
	broker := tracker.NewBroker()
	player := tracker.NewPlayer(broker, vm.GoSynther{})
	broker.ToPlayer <- expanded
	o := &output{player: player}
	o.SetSampleRate(44100)
	host := testHost{44100}
	var unipolar [NumParams]bool
	unipolar[7] = true
	o.unipolar.Store(&unipolar)
	last := func() float32 {
		l, r := make([]float32, 512), make([]float32, 512)
		o.Process(l, r, host)
		return l[511]
	}
	last()
	o.Param(0, 7, -96, false) // a quarter of the way up: 16 steps, 0.25 at the output
	last()
	if got := last(); math.Abs(float64(got)-0.25) > 1e-3 {
		t.Errorf("a unipolar slot a quarter of the way up gives %v, want 0.25", got)
	}
	if got := o.ParamText(7, -96); got != "16.00" {
		t.Errorf("the text of a unipolar slot is %q, want 16.00", got)
	}
	if got := o.ParamText(8, -96); got != "-96.00" {
		t.Errorf("the text of a bipolar slot is %q, want -96.00", got)
	}
	if got := o.ParamFromSteps(7, 16); got != -96 {
		t.Errorf("16 steps of a unipolar slot are %v for the host, want -96", got)
	}
	// bipolar again: the same value of the host is -96 steps: the loadval
	// at 64 - 96 steps, -1.5
	o.unipolar.Store(&[NumParams]bool{})
	o.repeat.Store(true)
	last()
	if got := last(); math.Abs(float64(got)+1.5) > 1e-3 {
		t.Errorf("the slot made bipolar gives %v, want -1.5", got)
	}
}
