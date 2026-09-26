package vm_test

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// renderBufread plays note on an instrument with a bufread unit with the given
// parameters and returns the first n output frames.
func renderBufread(t *testing.T, params sointu.ParamMap, buffers map[int]sointu.BufferAudio, note byte, n int) sointu.AudioBuffer {
	t.Helper()
	p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "buffer": 1, "notetracking": 1}
	for k, v := range params {
		p[k] = v
	}
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "bufread", Parameters: p},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": p["stereo"], "gain": 128}},
	}}}
	synth, err := vm.GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatalf("Synth failed: %v", err)
	}
	synth.(sointu.BufferSetter).SetBuffers(buffers)
	synth.Trigger(0, note)
	out := make(sointu.AudioBuffer, n)
	if _, _, err := synth.Render(out, n); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return out
}

func ramp(frames, channels int, scale float32) sointu.BufferAudio {
	data := make([]float32, frames*channels)
	for i := range data {
		data[i] = float32(i/channels)*scale + float32(i%channels)
	}
	return sointu.BufferAudio{Channels: channels, Data: data}
}

func checkLeft(t *testing.T, got sointu.AudioBuffer, want []float32) {
	t.Helper()
	for i, w := range want {
		if math.Abs(float64(got[i][0]-w)) > 1e-5 {
			t.Fatalf("frame %d: got %v, want %v (all: %v)", i, got[i][0], w, got[:len(want)])
		}
	}
}

func TestBufreadOriginalSpeed(t *testing.T) {
	out := renderBufread(t, nil, map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}, 60, 6)
	checkLeft(t, out, []float32{0, 0.1, 0.2, 0.3, 0, 0}) // silent after the end
}

func TestBufreadPitch(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	checkLeft(t, renderBufread(t, sointu.ParamMap{"transpose": 76}, bufs, 60, 4), []float32{0, 0.2, 0.4, 0.6})
	checkLeft(t, renderBufread(t, nil, bufs, 72, 4), []float32{0, 0.2, 0.4, 0.6})
	checkLeft(t, renderBufread(t, nil, bufs, 48, 4), []float32{0, 0.05, 0.1, 0.15}) // interpolated
	checkLeft(t, renderBufread(t, sointu.ParamMap{"notetracking": 0}, bufs, 72, 4), []float32{0, 0.1, 0.2, 0.3})
}

func TestBufreadStartAndLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	checkLeft(t, renderBufread(t, sointu.ParamMap{"start": 5}, bufs, 60, 4), []float32{0.5, 0.6, 0.7, 0})
	loop := sointu.ParamMap{"loop": 1, "loopstart": 2, "looplength": 3}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 9), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.2, 0.3, 0.4, 0.2})
	loop["loop"] = 0 // loop points are ignored when looping is off
	checkLeft(t, renderBufread(t, loop, bufs, 60, 6), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5})
}

func TestBufreadInterpolatesAcrossLoopEnd(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}
	loop := sointu.ParamMap{"loop": 1, "loopstart": 0, "looplength": 4}
	// at half speed, the frame between the last and the first frame of the
	// loop is halfway between them
	checkLeft(t, renderBufread(t, loop, bufs, 48, 9), []float32{0, 0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.15, 0})
}

func TestBufreadChannels(t *testing.T) {
	stereo := map[int]sointu.BufferAudio{1: ramp(4, 2, 0.1)} // right channel is left + 1
	out := renderBufread(t, sointu.ParamMap{"stereo": 1}, stereo, 60, 2)
	if out[1][0] != 0.1 || out[1][1] != 1.1 {
		t.Errorf("stereo buffer, stereo unit: got %v, want [0.1 1.1]", out[1])
	}
	checkLeft(t, renderBufread(t, nil, stereo, 60, 2), []float32{0.5, 0.6}) // mono unit mixes down
	mono := map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}
	out = renderBufread(t, sointu.ParamMap{"stereo": 1}, mono, 60, 2)
	if out[1][0] != 0.1 || out[1][1] != 0.1 {
		t.Errorf("mono buffer, stereo unit: got %v, want [0.1 0.1]", out[1])
	}
}

func TestBufreadMissingBufferIsSilent(t *testing.T) {
	checkLeft(t, renderBufread(t, sointu.ParamMap{"buffer": 2}, map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}, 60, 3), []float32{0, 0, 0})
}

func TestBufreadGain(t *testing.T) {
	checkLeft(t, renderBufread(t, sointu.ParamMap{"gain": 64}, map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}, 60, 3), []float32{0, 0.05, 0.1})
}

func TestBufreadUntriggeredVoiceIsSilent(t *testing.T) {
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 128, "buffer": 1, "notetracking": 1}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}}}
	synth, err := vm.GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	synth.(sointu.BufferSetter).SetBuffers(map[int]sointu.BufferAudio{1: ramp(8, 2, 0.1)})
	out := make(sointu.AudioBuffer, 4)
	synth.Render(out, 4)
	for i, f := range out {
		if f != [2]float32{} {
			t.Fatalf("frame %d: got %v from a voice that was never triggered", i, f)
		}
	}
	synth.Trigger(0, 60)
	synth.Render(out, 4)
	if out[1][0] != 0.1 {
		t.Errorf("after triggering: got %v, want 0.1", out[1][0])
	}
}
