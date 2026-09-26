package tracker

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
)

// testWav returns a mono 16-bit 44.1 kHz WAV file with the given samples.
func testWav(samples []int16) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + 2*len(samples)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(44100), uint32(88200), uint16(2), uint16(16)} {
		w(v) // fmt chunk: PCM, mono, 44.1 kHz, 16-bit
	}
	b.WriteString("data")
	w(uint32(2 * len(samples)))
	w(samples)
	return b.Bytes()
}

func newBufferTestModel(t *testing.T) (*Model, *Broker) {
	t.Helper()
	if _, err := ffmpeg.Find(""); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	drainPlayer(broker)
	return m, broker
}

func drainPlayer(broker *Broker) {
	for {
		select {
		case <-broker.ToPlayer:
		default:
			return
		}
	}
}

// processBufferResults passes buffer worker results to the model until the
// buffer is no longer processing.
func processBufferResults(t *testing.T, m *Model, broker *Broker, id int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for m.BufferStatus(id).Processing {
		select {
		case msg := <-broker.ToModel:
			if _, ok := msg.Data.(bufferResult); ok {
				m.ProcessMsg(msg)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for buffer %d", id)
		}
	}
}

// lastBufferAudio returns the last BufferAudioMsg sent to the player.
func lastBufferAudio(t *testing.T, broker *Broker) map[int]sointu.BufferAudio {
	t.Helper()
	var ret map[int]sointu.BufferAudio
	found := false
	for {
		select {
		case msg := <-broker.ToPlayer:
			if a, ok := msg.(BufferAudioMsg); ok {
				ret, found = a.Audio, true
			}
		default:
			if !found {
				t.Fatalf("no buffer audio sent to the player")
			}
			return ret
		}
	}
}

func setBuffers(m *Model, buffers sointu.Buffers) {
	defer m.change("SetBuffers", BufferChange, MajorChange)()
	m.d.Song.Buffers = buffers
}

func TestBufferAudioReachesPlayer(t *testing.T) {
	m, broker := newBufferTestModel(t)
	samples := []int16{0, 16384, -16384, 8192}
	sample := &sointu.AudioSample{Data: testWav(samples), Encoding: sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}}
	setBuffers(m, sointu.Buffers{{ID: 7, Channels: 1, Sample: sample}})
	processBufferResults(t, m, broker, 7)
	if err := m.BufferStatus(7).Err; err != nil {
		t.Fatalf("processing failed: %v", err)
	}
	audio := lastBufferAudio(t, broker)[7]
	want := []float32{0, 0.5, -0.5, 0.25}
	if audio.Channels != 1 || len(audio.Data) != len(want) {
		t.Fatalf("got %+v, want %v", audio, want)
	}
	for i := range want {
		if audio.Data[i] != want[i] {
			t.Errorf("sample %d: got %v, want %v", i, audio.Data[i], want[i])
		}
	}

	setBuffers(m, nil)
	if _, ok := lastBufferAudio(t, broker)[7]; ok {
		t.Errorf("removed buffer still sent to the player")
	}
}

func TestStaleBufferResultIsIgnored(t *testing.T) {
	m, broker := newBufferTestModel(t)
	enc := sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{100}), Encoding: enc}}})
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{200, 300}), Encoding: enc}}})
	processBufferResults(t, m, broker, 1)
	if got := m.BufferAudio()[1].Frames(); got != 2 {
		t.Errorf("got %d frames, want the 2 frames of the latest sample", got)
	}
}

func TestUndoRestoresBufferAudio(t *testing.T) {
	m, broker := newBufferTestModel(t)
	enc := sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{1, 2, 3}), Encoding: enc}}})
	processBufferResults(t, m, broker, 1)
	setBuffers(m, nil)
	m.History().Undo().Do()
	processBufferResults(t, m, broker, 1)
	if got := m.BufferAudio()[1].Frames(); got != 3 {
		t.Errorf("after undo, got %d frames, want 3", got)
	}
}
