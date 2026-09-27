package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func TestWritableBufferAudio(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	drainPlayer(broker)
	m.Buffer().NewEmpty().Do()
	if !m.Buffer().IsWritable() {
		t.Fatal("the new buffer is not writable")
	}
	id := m.d.Song.Buffers[0].ID
	a := lastBufferAudio(t, broker)[id]
	if !a.Writable || a.Frames() != 44100 || a.Channels != 1 {
		t.Fatalf("got audio with %d frames, %d channels, writable %v", a.Frames(), a.Channels, a.Writable)
	}

	m.Buffer().Name().SetValue("renamed")
	drainPlayer(broker) // renaming keeps the audio, and what is written to it

	if !m.Buffer().Length().SetValue("0.5") || m.d.Song.Buffers[0].Frames != 22050 {
		t.Fatalf("setting the length to 0.5 s: got %d frames", m.d.Song.Buffers[0].Frames)
	}
	if m.Buffer().Length().SetValue("-1") || m.Buffer().Length().SetValue("x") {
		t.Error("accepted an invalid length")
	}
	b := lastBufferAudio(t, broker)[id]
	if b.Frames() != 22050 || sameData(a.Data, b.Data) {
		t.Errorf("changing the length did not give new audio of %d frames", 22050)
	}

	m.Buffer().Clear().Do()
	if c := lastBufferAudio(t, broker)[id]; sameData(b.Data, c.Data) || c.Frames() != 22050 {
		t.Error("clearing did not give new audio")
	}

	// fitting uses what the player reports
	if m.Buffer().FitToRecording().Enabled() {
		t.Error("fit to recording enabled before recording")
	}
	m.playerStatus.BufferFills[0] = BufferFill{ID: id, Filled: 1000}
	m.Buffer().FitToRecording().Do()
	if m.d.Song.Buffers[0].Frames != 1000 {
		t.Errorf("after fitting: %d frames, want 1000", m.d.Song.Buffers[0].Frames)
	}
}

func TestPlayerKeepsWrittenBuffers(t *testing.T) {
	broker := NewBroker()
	p := NewPlayer(broker, vm.GoSynther{})
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 1, Length: 1}, Patch: sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
			{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "feedback": 0, "buffer": 1, "mode": sointu.BufwriteModeOnce}},
		}},
	}}
	buf := sointu.Buffer{ID: 1, Channels: 1, Frames: 100}
	audio := map[int]sointu.BufferAudio{1: buf.NewAudio()}
	TrySend(broker.ToPlayer, any(song))
	TrySend(broker.ToPlayer, any(BufferAudioMsg{Audio: map[int]sointu.BufferAudio{1: audio[1]}}))
	out := make(sointu.AudioBuffer, 10)
	p.Process(out, NullPlayerProcessContext{})
	p.synth.Trigger(0, 60)
	p.Process(out, NullPlayerProcessContext{})
	if f := p.status.BufferFills[0]; f != (BufferFill{ID: 1, Filled: 10}) {
		t.Fatalf("got fill %+v, want 10 frames of buffer 1", f)
	}

	// the same audio again, e.g. after renaming the buffer: the recording
	// continues
	TrySend(broker.ToPlayer, any(BufferAudioMsg{Audio: map[int]sointu.BufferAudio{1: audio[1]}}))
	p.Process(out, NullPlayerProcessContext{})
	if f := p.status.BufferFills[0]; f.Filled != 20 {
		t.Errorf("after the same audio: filled %d, want 20", f.Filled)
	}

	// a new synth continues too
	p.destroySynth()
	TrySend(broker.ToPlayer, any(song))
	p.Process(out, NullPlayerProcessContext{})
	if f := p.status.BufferFills[0]; f.Filled != 20 {
		t.Errorf("after a new synth: filled %d, want 20", f.Filled)
	}

	// new audio, e.g. after clearing: starts empty
	TrySend(broker.ToPlayer, any(BufferAudioMsg{Audio: map[int]sointu.BufferAudio{1: buf.NewAudio()}}))
	p.Process(out, NullPlayerProcessContext{})
	if f := p.status.BufferFills[0]; f.Filled != 0 {
		t.Errorf("after new audio: filled %d, want 0", f.Filled)
	}
}
