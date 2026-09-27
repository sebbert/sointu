package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

type hostContext struct{ bpm float64 }

func (c hostContext) BPM() (float64, bool) { return c.bpm, c.bpm > 0 }

func TestSongFollowsHostTempo(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	p := NewPlayer(broker, vm.GoSynther{})
	sync := func() {
		for {
			select {
			case msg := <-broker.ToModel:
				m.ProcessMsg(msg)
			default:
				return
			}
		}
	}
	out := make(sointu.AudioBuffer, 64)
	p.Process(out, NullPlayerProcessContext{})
	sync()
	if !m.Song().BPM().SetValue(120) {
		t.Fatal("BPM cannot be set without a host tempo")
	}

	p.Process(out, hostContext{139.6})
	sync()
	if p.song.BPM != 140 || m.d.Song.BPM != 140 {
		t.Errorf("player %d, song %d, want 140", p.song.BPM, m.d.Song.BPM)
	}
	// the song's tempo can be edited, and stays until the host's tempo changes
	if !m.Song().BPM().SetValue(100) {
		t.Fatal("BPM cannot be edited while following the host")
	}
	p.processMessages(hostContext{140})
	p.Process(out, hostContext{140})
	sync()
	if p.song.BPM != 100 || m.d.Song.BPM != 100 {
		t.Errorf("after editing: player %d, song %d, want 100", p.song.BPM, m.d.Song.BPM)
	}
	p.Process(out, hostContext{150})
	sync()
	if p.song.BPM != 150 || m.d.Song.BPM != 150 {
		t.Errorf("after host change: player %d, song %d, want 150", p.song.BPM, m.d.Song.BPM)
	}
}
