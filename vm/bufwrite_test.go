package vm_test

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// ramp is an envelope whose level goes up by 1/64 every frame, starting at
// 1/64 on the first frame.
var rampUnit = sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 64, "release": 64, "gain": 128}}

// newBufwriteSynth returns a synth with a writer instrument, which writes
// the given units' output to buffer 1, and a reader instrument playing
// buffer 1 with bufread.
func newBufwriteSynth(t *testing.T, units []sointu.Unit, write, read sointu.ParamMap, buf sointu.Buffer) (sointu.Synth, map[int]sointu.BufferAudio) {
	t.Helper()
	w := sointu.ParamMap{"stereo": 0, "feedback": 0, "buffer": 1, "mode": sointu.BufwriteModeOnce}
	for k, v := range write {
		w[k] = v
	}
	r := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "notetracking": 0}
	for k, v := range read {
		r[k] = v
	}
	patch := sointu.Patch{
		{NumVoices: 1, Units: append(units, sointu.Unit{Type: "bufwrite", Parameters: w})},
		{NumVoices: 1, Units: []sointu.Unit{
			{Type: "bufread", Parameters: r},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
	}
	synth := newSynth(t, patch)
	bufs := map[int]sointu.BufferAudio{1: buf.NewAudio()}
	synth.(sointu.BufferSetter).SetBuffers(bufs)
	return synth, bufs
}

func written(t *testing.T, synth sointu.Synth) sointu.BufferAudio {
	t.Helper()
	return synth.(sointu.BufferWriter).WrittenBuffers()[1]
}

func checkData(t *testing.T, what string, got []float32, want ...float32) {
	t.Helper()
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Errorf("%s: got %v, want %v", what, got, want)
			return
		}
	}
}

func TestBufwriteOnce(t *testing.T) {
	synth, _ := newBufwriteSynth(t, []sointu.Unit{rampUnit}, nil, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 4})
	render(t, synth, 3)
	if b := written(t, synth); b.Head != 0 || b.Filled != 0 {
		t.Errorf("wrote before the writer was triggered: head %d, filled %d", b.Head, b.Filled)
	}
	synth.Trigger(0, 60)
	render(t, synth, 2)
	b := written(t, synth)
	if b.Head != 2 || b.Filled != 2 {
		t.Errorf("after 2 frames: head %d, filled %d, want 2 2", b.Head, b.Filled)
	}
	render(t, synth, 5)
	b = written(t, synth)
	if b.Head != 4 || b.Filled != 4 {
		t.Errorf("after the end: head %d, filled %d, want 4 4", b.Head, b.Filled)
	}
	checkData(t, "data", b.Data, 1.0/64, 2.0/64, 3.0/64, 4.0/64)

	// playing it back
	synth.Trigger(1, 60)
	checkLeft(t, render(t, synth, 5), []float32{1.0 / 64, 2.0 / 64, 3.0 / 64, 4.0 / 64, 0})

	// a new note starts a new recording, and releasing stops it
	synth.Trigger(0, 60)
	render(t, synth, 1)
	synth.Release(0)
	render(t, synth, 3)
	if b := written(t, synth); b.Head != 1 || b.Filled != 1 {
		t.Errorf("after recording 1 frame: head %d, filled %d, want 1 1", b.Head, b.Filled)
	}
	synth.Trigger(1, 60)
	checkLeft(t, render(t, synth, 2), []float32{1.0 / 64, 0}) // the rest is not valid
}

func TestBufwriteRing(t *testing.T) {
	synth, _ := newBufwriteSynth(t, []sointu.Unit{rampUnit}, sointu.ParamMap{"mode": sointu.BufwriteModeRing}, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 4})
	synth.Trigger(0, 60)
	render(t, synth, 6)
	b := written(t, synth)
	if b.Head != 2 || b.Filled != 4 {
		t.Errorf("after 6 frames: head %d, filled %d, want 2 4", b.Head, b.Filled)
	}
	checkData(t, "data", b.Data, 5.0/64, 6.0/64, 3.0/64, 4.0/64)
	// the reader starts from the oldest frame at its trigger; the writer
	// keeps overwriting the frames behind it
	synth.Release(0)
	synth.Trigger(1, 60)
	checkLeft(t, render(t, synth, 5), []float32{3.0 / 64, 4.0 / 64, 5.0 / 64, 6.0 / 64, 0})
}

func TestBufwriteFeedback(t *testing.T) {
	one := sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}}
	synth, _ := newBufwriteSynth(t, []sointu.Unit{one}, sointu.ParamMap{"mode": sointu.BufwriteModeRing, "feedback": 64}, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 2})
	synth.Trigger(0, 60)
	render(t, synth, 6) // each frame written 3 times: 1, 1.5, 1.75
	checkData(t, "data", written(t, synth).Data, 1.75, 1.75)
}

func TestBufwriteChannels(t *testing.T) {
	l := sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}} // 1
	r := sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 0}}   // -1
	// stereo unit, stereo buffer; the left channel is on top of the stack
	synth, _ := newBufwriteSynth(t, []sointu.Unit{r, l}, sointu.ParamMap{"stereo": 1}, nil, sointu.Buffer{ID: 1, Channels: 2, Frames: 2})
	synth.Trigger(0, 60)
	render(t, synth, 1)
	checkData(t, "stereo to stereo", written(t, synth).Data, 1, -1)
	// stereo unit, mono buffer: mixed
	synth, _ = newBufwriteSynth(t, []sointu.Unit{r, l}, sointu.ParamMap{"stereo": 1}, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 2})
	synth.Trigger(0, 60)
	render(t, synth, 1)
	checkData(t, "stereo to mono", written(t, synth).Data, 0)
	// mono unit, stereo buffer: both channels
	synth, _ = newBufwriteSynth(t, []sointu.Unit{l}, nil, nil, sointu.Buffer{ID: 1, Channels: 2, Frames: 2})
	synth.Trigger(0, 60)
	render(t, synth, 1)
	checkData(t, "mono to stereo", written(t, synth).Data, 1, 1)
}

func TestBufwriteContinuesInAnotherSynth(t *testing.T) {
	synth, _ := newBufwriteSynth(t, []sointu.Unit{rampUnit}, nil, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 4})
	synth.Trigger(0, 60)
	render(t, synth, 3)
	other, _ := newBufwriteSynth(t, []sointu.Unit{rampUnit}, nil, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 4})
	other.(sointu.BufferSetter).SetBuffers(synth.(sointu.BufferWriter).WrittenBuffers())
	other.Trigger(1, 60)
	checkLeft(t, render(t, other, 4), []float32{1.0 / 64, 2.0 / 64, 3.0 / 64, 0})
}

func TestBufwriteIgnoresSampleBuffers(t *testing.T) {
	synth, _ := newBufwriteSynth(t, []sointu.Unit{rampUnit}, nil, nil, sointu.Buffer{ID: 1, Channels: 1, Frames: 4})
	sample := ramp(4, 1, 0.1)
	synth.(sointu.BufferSetter).SetBuffers(map[int]sointu.BufferAudio{1: sample})
	synth.Trigger(0, 60)
	render(t, synth, 3)
	checkData(t, "sample", sample.Data, 0, 0.1, 0.2, 0.3)
}

func TestBufwriteMultithread(t *testing.T) {
	// the writer and the reader on different threads: the reader does not see
	// the writes, but the written buffers come from the writer's thread
	patch := sointu.Patch{
		{NumVoices: 1, ThreadMaskM1: 0, Units: []sointu.Unit{rampUnit, {Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "feedback": 0, "buffer": 1, "mode": sointu.BufwriteModeOnce}}}},
		{NumVoices: 1, ThreadMaskM1: 1, Units: []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "notetracking": 0}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
	}
	synth, err := vm.MakeMultithreadSynther(vm.GoSynther{}).Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	defer synth.Close()
	synth.(sointu.BufferSetter).SetBuffers(map[int]sointu.BufferAudio{1: (&sointu.Buffer{ID: 1, Channels: 1, Frames: 8}).NewAudio()})
	synth.Trigger(0, 60)
	render(t, synth, 3)
	if b := synth.(sointu.BufferWriter).WrittenBuffers()[1]; b.Head != 3 || b.Filled != 3 {
		t.Errorf("head %d, filled %d, want 3 3", b.Head, b.Filled)
	}
}
