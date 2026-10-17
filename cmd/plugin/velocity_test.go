package plugin

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestVelocity checks that the velocity of a MIDI note on reaches the
// velocity units of the voice it triggers, and that a note on with velocity
// 0 releases the note, as MIDI has it.
func TestVelocity(t *testing.T) {
	song := stepSong()
	song.Patch[0].Units = []sointu.Unit{
		{ID: 1, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 0, "decay": 0, "sustain": 128, "release": 0, "gain": 128}},
		{ID: 2, Type: "velocity", Parameters: sointu.ParamMap{"stereo": 1}},
		{ID: 3, Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
		{ID: 4, Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}
	broker := tracker.NewBroker()
	player := tracker.NewPlayer(broker, vm.GoSynther{})
	broker.ToPlayer <- song
	buf := make(sointu.AudioBuffer, 512)
	block := func(data [3]byte) float32 {
		player.EmitMIDIMsg(&tracker.MIDIMessage{Timestamp: 0, Data: data, Source: t})
		player.Process(buf, testHost{})
		return buf[511][0]
	}
	for _, c := range []struct {
		data [3]byte
		want float64
	}{
		{[3]byte{0x90, 60, 100}, 100.0 / 127},
		{[3]byte{0x80, 60, 0}, 0},
		{[3]byte{0x90, 60, 30}, 30.0 / 127},
		{[3]byte{0x90, 60, 0}, 0}, // a note off
		{[3]byte{0x90, 60, 127}, 1},
	} {
		if got := float64(block(c.data)); math.Abs(got-c.want) > 1e-5 {
			t.Errorf("after %v: %v, want %v", c.data, got, c.want)
		}
	}
}
