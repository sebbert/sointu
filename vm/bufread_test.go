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

func TestBufreadLoopCrossfade(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	loop := sointu.ParamMap{"loop": 1, "loopstart": 4, "looplength": 4, "fade": 2}
	// the last two frames of the loop fade to the two frames before the loop
	// start, which the loop start continues
	checkLeft(t, renderBufread(t, loop, bufs, 60, 12), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.5, 0.4, 0.5, 0.6, 0.5})
	// limited by the loop start and length; a ramp crossfaded over the whole
	// loop is flat
	loop["fade"] = 10
	checkLeft(t, renderBufread(t, loop, bufs, 60, 10), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.4, 0.4, 0.4, 0.4, 0.4})
}

// renderModulatedBufread is like renderBufread, but first sends constant
// values to the modulation ports of the bufread unit. The constants are the
// values of loadval units, i.e. (v-64)/64.
func renderModulatedBufread(t *testing.T, params sointu.ParamMap, mods map[int]int, bufs map[int]sointu.BufferAudio, n int) sointu.AudioBuffer {
	t.Helper()
	p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "buffer": 1, "notetracking": 1}
	for k, v := range params {
		p[k] = v
	}
	var units []sointu.Unit
	for port, v := range mods {
		units = append(units,
			sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": v}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 128, "target": 100, "port": port, "sendpop": 1}})
	}
	units = append(units,
		sointu.Unit{ID: 100, Type: "bufread", Parameters: p},
		sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}})
	synth, err := vm.GoSynther{}.Synth(sointu.Patch{{NumVoices: 1, Units: units}}, 120)
	if err != nil {
		t.Fatalf("Synth failed: %v", err)
	}
	synth.(sointu.BufferSetter).SetBuffers(bufs)
	synth.Trigger(0, 60)
	out := make(sointu.AudioBuffer, n)
	if _, _, err := synth.Render(out, n); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return out
}

func TestBufreadModulatedStart(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// +0.5 times the 8 frames of the buffer
	checkLeft(t, renderModulatedBufread(t, sointu.ParamMap{"start": 1}, map[int]int{3: 96}, bufs, 4), []float32{0.5, 0.6, 0.7, 0})
	// clamped to the beginning
	checkLeft(t, renderModulatedBufread(t, sointu.ParamMap{"start": 1}, map[int]int{3: 0}, bufs, 3), []float32{0, 0.1, 0.2})
	// a fraction of a frame is kept: +0.25 of a frame
	checkLeft(t, renderModulatedBufread(t, nil, map[int]int{3: 66}, bufs, 3), []float32{0.025, 0.125, 0.225})
}

func TestBufreadModulatedLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// loop start 1 + 0.25*8 = 3, loop length 4 - 0.25*8 = 2
	loop := sointu.ParamMap{"loop": 1, "loopstart": 1, "looplength": 4}
	checkLeft(t, renderModulatedBufread(t, loop, map[int]int{4: 80, 5: 48}, bufs, 8), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.3, 0.4, 0.3})
	// a zero length loop does not loop
	checkLeft(t, renderModulatedBufread(t, loop, map[int]int{5: 0}, bufs, 9), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0})
}
