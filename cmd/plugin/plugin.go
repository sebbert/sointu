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
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/tracker/gioui"
	"github.com/vsariola/sointu/tracker/mcp"
)

type (
	// Instance is one instance of the plugin, with its own model, player and
	// tracker window.
	Instance struct {
		broker         *tracker.Broker
		model          *tracker.Model
		player         *tracker.Player
		tracker        *gioui.Tracker
		mcp            *mcp.Host
		buf            sointu.AudioBuffer
		totalFrames    int64
		lastAlertCheck time.Time
		// changed is true when the host has been told of changes that it has
		// not saved yet
		changed atomic.Bool
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
func New(name string, markDirty func()) *Instance {
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
		broker:         broker,
		model:          model,
		player:         player,
		tracker:        t,
		mcp:            host,
		buf:            make(sointu.AudioBuffer, 1024),
		lastAlertCheck: time.Now(),
	}
	model.SetHostSavesState(func() {
		if markDirty != nil && !i.changed.Swap(true) {
			markDirty()
		}
	})
	go t.Main()
	return i
}

// MIDI handles a MIDI message arriving on input port delta frames into the
// next processed block. Only note on/off and control change messages on ports
// below tracker.MAX_MIDI_PORTS are used. Call before Process.
func (i *Instance) MIDI(delta, port int, data [3]byte) {
	if port < 0 || port >= tracker.MAX_MIDI_PORTS {
		return
	}
	if (data[0] >= 0x80 && data[0] <= 0x9F) || (data[0] >= 0xB0 && data[0] <= 0xBF) {
		i.player.EmitMIDIMsg(&tracker.MIDIMessage{Timestamp: int64(delta) + i.totalFrames, Data: data, Source: i, Port: port})
	}
}

// Process renders the next block of stereo audio into left and right, which
// must have the same length.
func (i *Instance) Process(left, right []float32, host Host) {
	if time.Since(i.lastAlertCheck) > 2*time.Second { // limit the rate we query the samplerate from the host and send alerts
		if s, ok := host.SampleRate(); ok && math.Abs(s-44100.0) > 1e-6 {
			i.player.SendAlert("WrongSampleRate", fmt.Sprintf("Plugin host sample rate is %.0f Hz; Sointu supports 44100 Hz only", s), tracker.Error)
		}
		i.lastAlertCheck = time.Now()
	}
	frames := len(left)
	if len(i.buf) < frames {
		i.buf = append(i.buf, make(sointu.AudioBuffer, frames-len(i.buf))...)
	}
	buf := i.buf[:frames]
	i.player.Process(buf, host)
	for j := range buf {
		left[j], right[j] = buf[j][0], buf[j][1]
	}
	i.totalFrames += int64(frames)
}

// Close closes the tracker window and waits for it to finish.
func (i *Instance) Close() {
	i.mcp.Close()
	tracker.TrySend(i.broker.CloseGUI, struct{}{})
	i.model.Close()
	tracker.TimeoutReceive(i.broker.FinishedGUI, 3*time.Second)
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
