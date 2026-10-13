// Package plugin implements the host-independent part of the Sointu audio
// plugins, shared by the VST2 and CLAP versions.
package plugin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/cmd"
	"github.com/vsariola/sointu/resample"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/tracker/gioui"
	"github.com/vsariola/sointu/tracker/mcp"
)

type (
	// Instance is one instance of the plugin, with its own model, player and
	// tracker window.
	Instance struct {
		output
		broker  *tracker.Broker
		model   *tracker.Model
		tracker *gioui.Tracker
		mcp     *mcp.Host
		// changed is true when the host has been told of changes that it has
		// not saved yet
		changed atomic.Bool
		// managed is true once the host shows and hides the window
		managed atomic.Bool
		// closed is true from Close on: the host is not told anything more
		closed atomic.Bool
	}

	// output plays the player, whose synth runs at 44100 Hz, in the blocks
	// and at the sample rate of the host. At 44100 Hz the frames of the
	// player go to the host as they are; at any other rate through a
	// resampler, which delays them by resample.Latency frames of the host.
	output struct {
		player *tracker.Player
		buf    sointu.AudioBuffer // the frames of the player for a block
		block  sointu.AudioBuffer // the same at the rate of the host
		// totalFrames is how many frames the player has rendered: the time
		// of the next one on its clock, which MIDI events are stamped with
		totalFrames int64
		// hostRate is the last rate that SetSampleRate was told, and
		// rateChange what Process is to change to: both are set from any
		// thread. rate and resampler are those of the audio thread.
		hostRate      atomic.Int64
		rateChange    atomic.Pointer[rateChange]
		rate          int
		resampler     *resample.Resampler // nil at 44100 Hz
		lastRateCheck time.Time
	}

	// rateChange is a sample rate of the host and the resampler for it, nil
	// for 44100 Hz.
	rateChange struct {
		rate      int
		resampler *resample.Resampler
	}

	// Host provides information about the host during processing.
	Host interface {
		BPM() (bpm float64, ok bool)
		SampleRate() (samplerate float64, ok bool)
	}
)

// New creates a plugin instance and opens its tracker window. name is used to
// name the recovery file, e.g. "sointu-vsti". The song is saved in the host's
// project; markDirty, if not nil, tells the host that the song changed, and is
// called from the tracker's goroutine.
func New(name string, markDirty func()) *Instance { return newInstance(name, markDirty, false) }

// windowFallback is how long an instance made with NewHidden waits for the
// host to take over its window before it opens the window itself.
const windowFallback = time.Second

// NewHidden creates a plugin instance like New, for a host that may show and
// hide the window (ManageWindow, ShowWindow, HideWindow): the window is not
// opened until the host shows it. A host that has not taken the window over
// within a second is taken not to: the window then opens as with New, so
// that it is never out of reach.
func NewHidden(name string, markDirty func()) *Instance {
	i := newInstance(name, markDirty, true)
	time.AfterFunc(windowFallback, func() {
		if !i.managed.Load() && !i.closed.Load() {
			i.tracker.ShowWindow()
		}
	})
	return i
}

// ManageWindow tells that the host shows and hides the window from now on.
// closed is called, from the tracker's goroutine, when the user closes the
// window; the host can show it again.
func (i *Instance) ManageWindow(closed func()) {
	i.managed.Store(true)
	i.tracker.HostManagesWindow(func() {
		if !i.closed.Load() {
			closed()
		}
	})
}

// ShowWindow opens the window, or raises it if it is open, and HideWindow
// closes it; the instance plays on without one.
func (i *Instance) ShowWindow() { i.tracker.ShowWindow() }
func (i *Instance) HideWindow() { i.tracker.HideWindow() }

// SetWindowTitle sets what the title of the window names besides the
// tracker: what the host suggests, e.g. the name of the track.
func (i *Instance) SetWindowTitle(title string) { i.tracker.SetWindowTitle(title) }

func newInstance(name string, markDirty func(), hidden bool) *Instance {
	recoveryFile := ""
	if configDir, err := os.UserConfigDir(); err == nil {
		randBytes := make([]byte, 16)
		rand.Read(randBytes)
		recoveryFile = filepath.Join(configDir, "sointu", "recovery", name+"-recovery-"+hex.EncodeToString(randBytes)+".json")
	}
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, cmd.Synthers, cmd.NewMidiContext(broker), recoveryFile)
	player := tracker.NewPlayer(broker, cmd.Synthers[0])

	t := gioui.NewTracker(model)
	host := mcp.NewHost(model, name)
	t.SetRemoteControl(host)
	model.Play().TrackerHidden().SetValue(true)
	// since the plugin is usually working without any regard for the tracks
	// until recording, disable the Instrument-Track linking by default
	// because it might just confuse the user why instrument cannot be
	// swapped/added etc.
	model.Track().LinkInstrument().SetValue(false)
	i := &Instance{
		output:  output{player: player},
		broker:  broker,
		model:   model,
		tracker: t,
		mcp:     host,
	}
	model.SetHostSavesState(func() {
		if markDirty != nil && !i.closed.Load() && !i.changed.Swap(true) {
			markDirty()
		}
	})
	if hidden {
		t.StartHidden()
	}
	go t.Main()
	return i
}

// minRate and maxRate are the sample rates of a host that the plugin plays
// at; it takes anything else for a mistake of the host and ignores it.
const minRate, maxRate = 1000, 1 << 20

// SetSampleRate tells the plugin the sample rate of the host, and returns
// the latency of the plugin at that rate, in frames of the host: 0 at
// 44100 Hz, else that of the resampler. It can be called from any thread;
// the next Process plays at the rate.
func (o *output) SetSampleRate(rate float64) (latency int) {
	r := int(math.Round(rate))
	if r < minRate || r > maxRate {
		return o.Latency()
	}
	if o.hostRate.Swap(int64(r)) != int64(r) {
		c := &rateChange{rate: r}
		if r != resample.SynthRate {
			c.resampler = resample.New(resample.SynthRate, r)
		}
		o.rateChange.Store(c)
	}
	return resample.Latency(resample.SynthRate, r)
}

// Latency returns by how many frames of the host the output is late, at
// the sample rate last set.
func (o *output) Latency() int {
	if r := int(o.hostRate.Load()); r != 0 {
		return resample.Latency(resample.SynthRate, r)
	}
	return 0
}

// changeRate makes the audio thread play at the rate that SetSampleRate was
// last told.
func (o *output) changeRate() {
	c := o.rateChange.Swap(nil)
	if c == nil || c.rate == o.rate {
		return
	}
	o.rate, o.resampler = c.rate, c.resampler
	if r := o.resampler; r != nil {
		o.player.SendAlert("SampleRate", fmt.Sprintf("The host runs at %d Hz: resampling from %d Hz, %d samples (%.1f ms) late", c.rate, resample.SynthRate, r.Latency(), 1000*float64(r.Latency())/float64(c.rate)), tracker.Info)
	}
}

// MIDI handles a MIDI message arriving on input port delta frames into the
// next processed block. Only note on/off and control change messages on ports
// below tracker.MAX_MIDI_PORTS are used. Call before Process.
func (o *output) MIDI(delta, port int, data [3]byte) {
	if port < 0 || port >= tracker.MAX_MIDI_PORTS {
		return
	}
	if (data[0] >= 0x80 && data[0] <= 0x9F) || (data[0] >= 0xB0 && data[0] <= 0xBF) {
		o.changeRate()
		if o.resampler != nil {
			// the frame of the player at the time of that frame of the host
			delta = o.resampler.Need(max(delta, 0))
		}
		o.player.EmitMIDIMsg(&tracker.MIDIMessage{Timestamp: int64(delta) + o.totalFrames, Data: data, Source: o, Port: port})
	}
}

// Process renders the next block of stereo audio into left and right, which
// must have the same length.
func (o *output) Process(left, right []float32, host Host) {
	if time.Since(o.lastRateCheck) > 2*time.Second { // limit the rate we query the samplerate from the host
		if s, ok := host.SampleRate(); ok {
			o.SetSampleRate(s)
		}
		o.lastRateCheck = time.Now()
	}
	o.changeRate()
	frames := len(left)
	if o.resampler == nil {
		if len(o.buf) < frames {
			o.buf = append(o.buf, make(sointu.AudioBuffer, frames-len(o.buf))...)
		}
		buf := o.buf[:frames]
		o.player.Process(buf, host)
		for j := range buf {
			left[j], right[j] = buf[j][0], buf[j][1]
		}
		o.totalFrames += int64(frames)
		return
	}
	// the player renders up to the time of the end of the block: as long a
	// block, within a frame
	need := o.resampler.Need(frames)
	if len(o.buf) < need {
		o.buf = append(o.buf, make(sointu.AudioBuffer, need-len(o.buf))...)
	}
	if len(o.block) < frames {
		o.block = append(o.block, make(sointu.AudioBuffer, frames-len(o.block))...)
	}
	buf, block := o.buf[:need], o.block[:frames]
	if need > 0 {
		o.player.Process(buf, host)
	}
	o.resampler.Process(block, buf)
	for j := range block {
		left[j], right[j] = block[j][0], block[j][1]
	}
	o.totalFrames += int64(need)
}

// Close closes the tracker window and waits for it to finish, where it can:
// on the thread that the window is closed on, the main thread of macOS, the
// window cannot close while Close waits there, and the tracker would only
// finish when Close gave up, with the host standing still until then. Hosts
// close their plugins on that thread. The tracker then finishes on its own,
// a moment after Close; the host is told nothing more from Close on.
func (i *Instance) Close() {
	i.closed.Store(true)
	i.mcp.Close()
	tracker.TrySend(i.broker.CloseGUI, struct{}{})
	i.model.Close()
	if !onWindowThread() {
		tracker.TimeoutReceive(i.broker.FinishedGUI, 3*time.Second)
	}
}

// State returns the current song and settings, or nil on failure.
func (i *Instance) State() []byte {
	i.changed.Store(false)
	retChn := make(chan []byte)
	if !tracker.TrySend(i.broker.ToModel, tracker.MsgToModel{Data: func() { retChn <- i.tracker.History().MarshalRecovery() }}) {
		return nil
	}
	ret, _ := tracker.TimeoutReceive(retChn, 5*time.Second) // ret will be nil if timeout or channel closed
	return ret
}

// SetState restores state returned by State.
func (i *Instance) SetState(data []byte) {
	tracker.TrySend(i.broker.ToModel, tracker.MsgToModel{Data: func() { i.tracker.History().UnmarshalRecovery(data) }})
}
