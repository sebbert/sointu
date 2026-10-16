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
	p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1}
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
	checkLeft(t, renderBufread(t, sointu.ParamMap{"note": 0}, bufs, 72, 4), []float32{0, 0.1, 0.2, 0.3})
}

func TestBufreadStartAndLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	checkLeft(t, renderBufread(t, sointu.ParamMap{"start": 5}, bufs, 60, 4), []float32{0.5, 0.6, 0.7, 0})
	loop := sointu.ParamMap{"mode": 1, "loopstart": 2, "looplength": 3}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 9), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.2, 0.3, 0.4, 0.2})
	loop["mode"] = 0 // loop points are ignored when looping is off
	checkLeft(t, renderBufread(t, loop, bufs, 60, 6), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5})
}

func TestBufreadInterpolatesAcrossLoopEnd(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}
	loop := sointu.ParamMap{"mode": 1, "loopstart": 0, "looplength": 4}
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
		{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1}},
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
	loop := sointu.ParamMap{"mode": 1, "loopstart": 4, "looplength": 4, "loopfade": 2}
	// the last two frames of the loop fade to the two frames before the loop
	// start, which the loop start continues
	checkLeft(t, renderBufread(t, loop, bufs, 60, 12), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.5, 0.4, 0.5, 0.6, 0.5})
	// limited by the loop start and length; a ramp crossfaded over the whole
	// loop is flat
	loop["loopfade"] = 10
	checkLeft(t, renderBufread(t, loop, bufs, 60, 10), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.4, 0.4, 0.4, 0.4, 0.4})
}

// renderModulatedBufread is like renderBufread, but first sends constant
// values to the modulation ports of the bufread unit. The constants are the
// values of loadval units, i.e. (v-64)/64.
func renderModulatedBufread(t *testing.T, params sointu.ParamMap, mods map[int]int, bufs map[int]sointu.BufferAudio, n int) sointu.AudioBuffer {
	t.Helper()
	p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 1}
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
	checkLeft(t, renderModulatedBufread(t, sointu.ParamMap{"start": 1}, map[int]int{4: 96}, bufs, 4), []float32{0.5, 0.6, 0.7, 0})
	// clamped to the beginning
	checkLeft(t, renderModulatedBufread(t, sointu.ParamMap{"start": 1}, map[int]int{4: 0}, bufs, 3), []float32{0, 0.1, 0.2})
	// a fraction of a frame is kept: +0.25 of a frame
	checkLeft(t, renderModulatedBufread(t, nil, map[int]int{4: 66}, bufs, 3), []float32{0.025, 0.125, 0.225})
}

func TestBufreadModulatedLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// loop start 1 + 0.25*8 = 3, loop length 4 - 0.25*8 = 2
	loop := sointu.ParamMap{"mode": 1, "loopstart": 1, "looplength": 4}
	checkLeft(t, renderModulatedBufread(t, loop, map[int]int{5: 80, 6: 48}, bufs, 8), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.3, 0.4, 0.3})
	// a zero length loop does not loop
	checkLeft(t, renderModulatedBufread(t, loop, map[int]int{6: 0}, bufs, 9), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0})
}

func TestBufreadPlayheads(t *testing.T) {
	patch := sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}, {Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 0}}}},
		{NumVoices: 2, Units: []sointu.Unit{
			{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 0, "start": 2}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
	}
	for _, synther := range []sointu.Synther{vm.GoSynther{}, vm.MakeMultithreadSynther(vm.GoSynther{})} {
		synth, err := synther.Synth(patch, 120)
		if err != nil {
			t.Fatal(err)
		}
		synth.(sointu.BufferSetter).SetBuffers(map[int]sointu.BufferAudio{1: ramp(100000, 1, 0.01)})
		synth.Trigger(1, 60)
		render(t, synth, 3)
		synth.Trigger(2, 60)
		render(t, synth, 1)
		got := synth.(sointu.PlayheadReporter).Playheads(nil)
		want := []sointu.Playhead{{BufferID: 1, Frame: 6}, {BufferID: 1, Frame: 3}} // started at 2
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s: got %v, want %v", synther.Name(), got, want)
		}
		synth.Release(1)
		render(t, synth, 10)
		// released notes are reported with the time since the release, for a while
		if got := synth.(sointu.PlayheadReporter).Playheads(nil); len(got) != 2 || got[0].Released != 11 || got[1].Released != 0 {
			t.Errorf("%s: after releasing: %v", synther.Name(), got)
		}
		render(t, synth, sointu.MaxPlayheadRelease)
		if got := synth.(sointu.PlayheadReporter).Playheads(nil); len(got) != 1 {
			t.Errorf("%s: long released notes reported: %v", synther.Name(), got)
		}
		synth.Close()
	}
}

func TestBufreadSpeed(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// backwards from frame 5, silent before the beginning
	checkLeft(t, renderBufread(t, sointu.ParamMap{"speed": 0, "start": 5}, bufs, 60, 8), []float32{0.5, 0.4, 0.3, 0.2, 0.1, 0, 0, 0})
	// frozen
	checkLeft(t, renderBufread(t, sointu.ParamMap{"speed": 64, "start": 3}, bufs, 60, 3), []float32{0.3, 0.3, 0.3})
	// half speed backwards
	checkLeft(t, renderBufread(t, sointu.ParamMap{"speed": 32, "start": 2}, bufs, 60, 5), []float32{0.2, 0.15, 0.1, 0.05, 0})
}

func TestBufreadBackwardsLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// starting in the loop 2-5 and going backwards wraps to the loop end
	loop := sointu.ParamMap{"speed": 0, "start": 3, "mode": 1, "loopstart": 2, "looplength": 3}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 7), []float32{0.3, 0.2, 0.4, 0.3, 0.2, 0.4, 0.3})
	// starting before the loop and going backwards does not loop
	loop["start"] = 1
	checkLeft(t, renderBufread(t, loop, bufs, 60, 3), []float32{0.1, 0, 0})
}

func TestBufreadNegativeStart(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// 3 frames back from the end
	checkLeft(t, renderBufread(t, sointu.ParamMap{"start": -3}, bufs, 60, 4), []float32{0.5, 0.6, 0.7, 0})
}

func TestBufreadEdgeFade(t *testing.T) {
	ones := sointu.BufferAudio{Channels: 1, Data: []float32{1, 1, 1, 1, 1, 1, 1, 1}}
	bufs := map[int]sointu.BufferAudio{1: ones}
	// 4 frames of fade from each edge: 0 at the first and last frame
	checkLeft(t, renderBufread(t, sointu.ParamMap{"edgefade": 4}, bufs, 60, 9), []float32{0, 0.25, 0.5, 0.75, 0.75, 0.5, 0.25, 0, 0})
}

func TestBufreadNegativeLoopStart(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// loop the 3rd and 2nd last frames
	loop := sointu.ParamMap{"mode": 1, "loopstart": -3, "looplength": 2}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 10), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.5, 0.6, 0.5})
}

func slicedRamp() map[int]sointu.BufferAudio {
	a := ramp(16, 1, 0.1)
	a.Slices = []int{4, 10} // slices [0,4), [4,10), [10,16)
	return map[int]sointu.BufferAudio{1: a}
}

func TestBufreadSlice(t *testing.T) {
	bufs := slicedRamp()
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "slice": 1}, bufs, 60, 3), []float32{0.4, 0.5, 0.6})
	// start is an offset from the marker
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "slice": 2, "start": 2}, bufs, 60, 3), []float32{1.2, 1.3, 1.4})
	// continues past the next marker, or stops there
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "slice": 0}, bufs, 60, 6), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.5})
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 2, "slice": 0}, bufs, 60, 6), []float32{0, 0.1, 0.2, 0.3, 0, 0})
	// wraps around the number of slices
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "slice": 4}, bufs, 60, 2), []float32{0.4, 0.5})
	// no markers: one slice
	one := map[int]sointu.BufferAudio{1: ramp(16, 1, 0.1)}
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "slice": 3, "start": 1}, one, 60, 2), []float32{0.1, 0.2})
}

func TestBufreadSliceByNote(t *testing.T) {
	bufs := slicedRamp()
	// C-3 plays slice 0, chromatic up; the pitch does not follow the note
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 2}, bufs, 60, 2), []float32{0, 0.1})
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 2}, bufs, 62, 2), []float32{1.0, 1.1})
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 2, "slice": 1}, bufs, 60, 2), []float32{0.4, 0.5})
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 2}, bufs, 59, 2), []float32{1.0, 1.1}) // wraps down
}

func TestBufreadSliceAndPitchByNote(t *testing.T) {
	bufs := slicedRamp()
	// the note picks the slice and the pitch: C-3 is slice 0 at the recorded pitch
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 3}, bufs, 60, 3), []float32{0, 0.1, 0.2})
	step := float32(0.1 * math.Pow(2, 2.0/12))
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 3}, bufs, 62, 3), []float32{1.0, 1.0 + step, 1.0 + 2*step})
	// without slices, it is the pitch alone
	checkLeft(t, renderBufread(t, sointu.ParamMap{"note": 3}, bufs, 72, 3), []float32{0, 0.2, 0.4})
	// transpose applies too
	checkLeft(t, renderBufread(t, sointu.ParamMap{"slices": 1, "note": 3, "transpose": 76}, bufs, 60, 3), []float32{0, 0.2, 0.4})
}

func TestBufreadSliceModulated(t *testing.T) {
	bufs := slicedRamp()
	// loadval 66 is 2/64; sent at 128, 2/64·128 = 4 slices: 4 wraps to slice 1
	checkLeft(t, renderModulatedBufread(t, sointu.ParamMap{"slices": 1}, map[int]int{7: 66}, bufs, 2), []float32{0.4, 0.5})
}

func TestBufreadSliceFromLoadnote(t *testing.T) {
	// a loadnote sent at 96 adds the note minus 64, exactly
	a := ramp(200, 1, 0.01)
	for i := 1; i < 20; i++ {
		a.Slices = append(a.Slices, i*10)
	}
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}},
		{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 96, "target": 100, "port": 7, "sendpop": 1}},
		{ID: 100, Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "slices": 1, "slice": 4}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}}
	synth, err := vm.GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	synth.(sointu.BufferSetter).SetBuffers(map[int]sointu.BufferAudio{1: a})
	synth.Trigger(0, 67) // 67 - 64 + 4 = slice 7
	out := make(sointu.AudioBuffer, 2)
	if _, _, err := synth.Render(out, 2); err != nil {
		t.Fatal(err)
	}
	checkLeft(t, out, []float32{0.7, 0.71})
}

func TestBufreadSliceLoop(t *testing.T) {
	bufs := slicedRamp()
	// slice 1 is 6 frames: loop start 64/128 of it = 3 frames in, length
	// 128/128 held to the 3 frames left
	loop := sointu.ParamMap{"slices": 1, "slice": 1, "mode": 1, "loopstart": 64, "looplength": 128}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 9), []float32{0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.7, 0.8, 0.9})
	// with BPM timing, the loop points stay parts of the slice
	loop["timing"] = 2
	checkLeft(t, renderBufread(t, loop, bufs, 60, 9), []float32{0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.7, 0.8, 0.9})
}

func TestBufreadSliceEdgeFade(t *testing.T) {
	bufs := slicedRamp()
	// slice 1, [4,10): faded over 2 frames at both of its edges
	out := renderBufread(t, sointu.ParamMap{"slices": 2, "slice": 1, "edgefade": 2}, bufs, 60, 7)
	checkLeft(t, out, []float32{0, 0.25, 0.6, 0.7, 0.4, 0, 0})
}

func TestBufreadPingPong(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// frames 2 to 4, forwards and backwards; fade does nothing at the turns
	loop := sointu.ParamMap{"mode": 2, "loopstart": 2, "looplength": 3, "loopfade": 2}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 11), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.3, 0.2, 0.3, 0.4, 0.3, 0.2})
	// backwards from inside the loop, turning at its start
	back := sointu.ParamMap{"mode": 2, "loopstart": 2, "looplength": 3, "start": 4, "speed": 0}
	checkLeft(t, renderBufread(t, back, bufs, 60, 7), []float32{0.4, 0.3, 0.2, 0.3, 0.4, 0.3, 0.2})
	// started past the loop, folded into it as if it had bounced
	past := sointu.ParamMap{"mode": 2, "loopstart": 2, "looplength": 3, "start": 7}
	checkLeft(t, renderBufread(t, past, bufs, 60, 5), []float32{0.3, 0.4, 0.3, 0.2, 0.3}) // bounced off 4 and 2
	// a loop of one frame holds it
	one := sointu.ParamMap{"mode": 2, "loopstart": 2, "looplength": 1}
	checkLeft(t, renderBufread(t, one, bufs, 60, 5), []float32{0, 0.1, 0.2, 0.2, 0.2})
}

func TestBufreadPingPongInterpolates(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(4, 1, 0.1)}
	// at half speed, the turns are at the last and the first frame
	loop := sointu.ParamMap{"mode": 2, "loopstart": 0, "looplength": 4}
	checkLeft(t, renderBufread(t, loop, bufs, 48, 15), []float32{0, 0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.25, 0.2, 0.15, 0.1, 0.05, 0, 0.05, 0.1})
	// at 0.75 speed, a turn falls between two frames
	loop["speed"] = 112
	checkLeft(t, renderBufread(t, loop, bufs, 60, 8), []float32{0, 0.075, 0.15, 0.225, 0.3, 0.225, 0.15, 0.075})
}

func TestBufreadPingPongSlice(t *testing.T) {
	bufs := slicedRamp()
	// slice 1 is [4,10): the loop is its frames 7 to 9
	loop := sointu.ParamMap{"slices": 1, "slice": 1, "mode": 2, "loopstart": 64, "looplength": 128}
	checkLeft(t, renderBufread(t, loop, bufs, 60, 11), []float32{0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.8, 0.7, 0.8, 0.9, 0.8})
}

func TestBufreadPingPongModulatedLoop(t *testing.T) {
	bufs := map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}
	// loop start 1 + 0.25*8 = 3, loop length 4 - 0.25*8 = 2: frames 3 and 4
	loop := sointu.ParamMap{"mode": 2, "loopstart": 1, "looplength": 4}
	checkLeft(t, renderModulatedBufread(t, loop, map[int]int{5: 80, 6: 48}, bufs, 8), []float32{0, 0.1, 0.2, 0.3, 0.4, 0.3, 0.4, 0.3})
}
