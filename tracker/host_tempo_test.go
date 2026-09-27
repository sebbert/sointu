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
	if m.Song().BPM().SetValue(100) || m.d.Song.BPM != 140 {
		t.Error("BPM can be edited while following the host")
	}

	// a song with another tempo follows the host too
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.BPM = 90
	}()
	p.processMessages(hostContext{140})
	p.Process(out, hostContext{140})
	sync()
	if p.song.BPM != 140 || m.d.Song.BPM != 140 {
		t.Errorf("after loading: player %d, song %d, want 140", p.song.BPM, m.d.Song.BPM)
	}
}
