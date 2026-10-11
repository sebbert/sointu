package vm_test

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// fullRing returns the audio of a ring of the given data that is full, with
// its write head at the first frame: the frames are from the oldest to the
// newest.
func fullRing(data ...float32) sointu.BufferAudio {
	return sointu.BufferAudio{Channels: 1, Data: data, Writable: true, Filled: len(data)}
}

func readParams(params sointu.ParamMap) sointu.ParamMap {
	p := sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "notetracking": 0}
	for k, v := range params {
		p[k] = v
	}
	return p
}

func renderPatch(t *testing.T, patch sointu.Patch, bufs map[int]sointu.BufferAudio, notes map[int]byte, n int) sointu.AudioBuffer {
	t.Helper()
	synth, err := vm.GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatalf("Synth failed: %v", err)
	}
	synth.(sointu.BufferSetter).SetBuffers(bufs)
	for voice, note := range notes {
		synth.Trigger(voice, note)
	}
	out := make(sointu.AudioBuffer, n)
	if _, _, err := synth.Render(out, n); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return out
}

var outUnit = sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}}

// A tap reads the frame at start every sample: it stays at the same frame
// of a buffer that is not written, sounds without a note, and follows the
// modulation of start while it sounds.
func TestBufreadTap(t *testing.T) {
	ring := func() map[int]sointu.BufferAudio {
		return map[int]sointu.BufferAudio{1: fullRing(0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7)}
	}
	tap := func(params sointu.ParamMap) sointu.Patch {
		params["mode"] = 1
		return sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(params)}, outUnit}}}
	}
	// 3 frames behind the write head, with and without a note
	checkLeft(t, renderPatch(t, tap(sointu.ParamMap{"start": -3}), ring(), map[int]byte{0: 60}, 4), []float32{0.5, 0.5, 0.5, 0.5})
	checkLeft(t, renderPatch(t, tap(sointu.ParamMap{"start": -3}), ring(), nil, 4), []float32{0.5, 0.5, 0.5, 0.5})
	// from the oldest frame, and not past the newest one
	checkLeft(t, renderPatch(t, tap(sointu.ParamMap{"start": 2}), ring(), nil, 2), []float32{0.2, 0.2})
	// the speed, the pitch and the loop are not used
	checkLeft(t, renderPatch(t, tap(sointu.ParamMap{"start": -3, "speed": 0, "transpose": 100, "loop": 1, "looplength": 2}), ring(), map[int]byte{0: 72}, 3), []float32{0.5, 0.5, 0.5})
	// a play head goes on from start, a tap does not
	play := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -3})}, outUnit}}}
	checkLeft(t, renderPatch(t, play, ring(), map[int]byte{0: 60}, 3), []float32{0.5, 0.6, 0.7})
	// start modulated by a quarter of the 8 frames: 2 frames later, and by
	// 3/16: a frame and a half
	for amount, want := range map[int]float32{80: 0.7, 76: 0.65} {
		mod := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
			{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": amount, "voice": 0, "target": 5, "port": 4, "sendpop": 1}},
			{Type: "bufread", ID: 5, Parameters: readParams(sointu.ParamMap{"start": -3, "mode": 1})},
			outUnit,
		}}}
		checkLeft(t, renderPatch(t, mod, ring(), nil, 3), []float32{want, want, want})
	}
}

// A tap behind a bufwrite unit is a delay: what was written start frames
// ago, also when the tap comes before the writer in the same voice.
func TestBufreadTapIsDelay(t *testing.T) {
	source := []sointu.Unit{rampUnit}
	dry := renderPatch(t, sointu.Patch{{NumVoices: 1, Units: append(append([]sointu.Unit{}, source...), outUnit)}}, nil, map[int]byte{0: 60}, 64)
	for _, delay := range []int{1, 5, 16} {
		units := []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -delay, "mode": 1})}, outUnit}
		units = append(units, source...)
		units = append(units, sointu.Unit{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "buffer": 1, "pop": 1}})
		bufs := map[int]sointu.BufferAudio{1: (&sointu.Buffer{ID: 1, Channels: 1, Frames: 16}).NewAudio()}
		out := renderPatch(t, sointu.Patch{{NumVoices: 1, Units: units}}, bufs, map[int]byte{0: 60}, 64)
		for i := range out {
			want := float32(0)
			if i >= delay {
				want = dry[i-delay][0]
			}
			if out[i][0] != want {
				t.Fatalf("a tap %d frames behind the write head: frame %d is %v, not %v", delay, i, out[i][0], want)
			}
		}
	}
}

// With pitch timing the start is in frames at note 0 and shorter by half for
// every octave of the note, in parts of frames; with BPM timing it is in
// 48ths of a beat.
func TestBufreadTiming(t *testing.T) {
	ring := map[int]sointu.BufferAudio{1: fullRing(0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7)}
	tap := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -5, "mode": 1, "timing": 1})}, outUnit}}}
	// note 12: 2.5 frames behind the write head, between frames 5 and 6
	checkLeft(t, renderPatch(t, tap, ring, map[int]byte{0: 12}, 2), []float32{0.55, 0.55})
	// note 24: 1.25 frames
	checkLeft(t, renderPatch(t, tap, ring, map[int]byte{0: 24}, 2), []float32{0.675, 0.675})
	// a play head starts there too
	play := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -5, "timing": 1})}, outUnit}}}
	checkLeft(t, renderPatch(t, play, map[int]sointu.BufferAudio{1: ramp(8, 1, 0.1)}, map[int]byte{0: 12}, 2), []float32{0.55, 0.65})

	if got := vm.BPMFrames(48, 120); got != 22050 {
		t.Errorf("a beat at 120 BPM is %d frames", got)
	}
	if got := vm.BPMFrames(-24, 100); got != -13230 {
		t.Errorf("half a beat back at 100 BPM is %d frames", got)
	}
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -48, "loop": 1, "loopstart": 12, "looplength": 96, "timing": 2})}, outUnit}}}
	bytecode, err := vm.NewBytecode(patch, vm.AllFeatures{}, 150)
	if err != nil {
		t.Fatal(err)
	}
	if r := bytecode.BufferRegions[0]; int32(r.Start) != -17640 || r.LoopStart != 4410 || r.LoopLength != 35280 {
		t.Errorf("a beat back, a loop of two beats from a quarter of one, at 150 BPM: %d, %d, %d frames", int32(r.Start), r.LoopStart, r.LoopLength)
	}
}

// ringPatch returns a patch whose first instrument reads buffer 1 and whose
// second one writes a constant 1 to it as a ring.
func ringPatch(read sointu.ParamMap) sointu.Patch {
	return sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(read)}, outUnit}},
		{NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
			{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "buffer": 1, "pop": 1}},
		}},
	}
}

// In a ring that is full, the edge fade is a crossfade across the write
// head: a read head that the write head overtakes, or that overtakes it,
// keeps its level, instead of fading out and in again.
func TestBufreadRingCrossfade(t *testing.T) {
	ones := make([]float32, 32)
	for i := range ones {
		ones[i] = 1
	}
	for name, read := range map[string]sointu.ParamMap{
		"half the speed, from the oldest frames":  {"start": 2, "speed": 96, "edgefade": 8},
		"backwards, from the newest frames":       {"start": -3, "speed": 0, "edgefade": 8},
		"an octave up, around the ring and on":    {"start": 20, "transpose": 76, "edgefade": 8},
		"a fade longer than half of the ring":     {"start": 2, "speed": 96, "edgefade": 100},
		"the normal speed, at the oldest frames":  {"start": 1, "edgefade": 8},
		"the normal speed, in the newest frames":  {"start": -2, "edgefade": 8},
		"between frames, by a detuned fifth down": {"start": 5, "transpose": 57, "detune": 80, "edgefade": 6},
	} {
		out := renderPatch(t, ringPatch(read), map[int]sointu.BufferAudio{1: fullRing(ones...)}, map[int]byte{0: 60}, 400)
		for i := range out {
			if math.Abs(float64(out[i][0])-1) > 1e-5 {
				t.Errorf("%s: frame %d is %v, not 1", name, i, out[i][0])
				break
			}
		}
	}
	// a ring that is not full, or that no unit writes as a ring, has edges:
	// the fade stays a fade
	part := sointu.BufferAudio{Channels: 1, Data: append([]float32{}, ones...), Writable: true, Filled: 8, Head: 8}
	out := renderPatch(t, ringPatch(sointu.ParamMap{"start": 0, "edgefade": 4, "speed": 64}), map[int]sointu.BufferAudio{1: part}, map[int]byte{0: 60}, 1)
	if out[0][0] != 0 {
		t.Errorf("the oldest frame of a ring that is not full is %v, not faded to 0", out[0][0])
	}
	// the content goes on at the other side of the write head: a ramp read
	// at half the speed from its oldest frame is overtaken at once, and
	// goes on in the frames an edge fade before the newest ones, where the
	// crossfade was
	rampRing := make([]float32, 32)
	for i := range rampRing {
		rampRing[i] = float32(i)
	}
	out = renderPatch(t, sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": 0, "speed": 96, "edgefade": 4})}, outUnit}},
		{NumVoices: 1, Units: []sointu.Unit{
			{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": 0, "mode": 1})}, // writes the ring back as it is
			{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "buffer": 1, "pop": 1}},
		}},
	}, map[int]sointu.BufferAudio{1: fullRing(rampRing...)}, map[int]byte{0: 60}, 3)
	// frame 0 at weight 0 and frame 28 at weight 1; then, overtaken, on from
	// frame 28.5
	checkLeft(t, out, []float32{28, 28.5, 29})
}

// With pervoice, every voice of the instrument has its own copy of the
// buffer: a voice reads what it wrote itself, not the mix of all of them.
func TestBufwritePerVoice(t *testing.T) {
	patch := func(pervoice int) sointu.Patch {
		return sointu.Patch{
			{NumVoices: 1, Units: []sointu.Unit{{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 64}}, {Type: "pop", Parameters: sointu.ParamMap{"stereo": 0}}}},
			{NumVoices: 2, Units: []sointu.Unit{
				{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -2, "mode": 1})},
				outUnit,
				{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": 0, "buffer": 1, "pop": 1, "pervoice": pervoice}},
			}},
			// another instrument reads the copy of the first voice
			{NumVoices: 1, Units: []sointu.Unit{{Type: "bufread", Parameters: readParams(sointu.ParamMap{"start": -2, "mode": 1})}, {Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 0}}}},
		}
	}
	note := func(n byte) float32 { return float32(n)/64 - 1 } // what loadnote pushes
	bufs := func() map[int]sointu.BufferAudio {
		return map[int]sointu.BufferAudio{1: (&sointu.Buffer{ID: 1, Channels: 1, Frames: 8}).NewAudio()}
	}
	notes := map[int]byte{1: 72, 2: 100}
	shared := renderPatch(t, patch(0), bufs(), notes, 6)
	own := renderPatch(t, patch(1), bufs(), notes, 6)
	sum := note(72) + note(100)
	// shared: both voices read the mix; own: each reads its own note
	if got := shared[4][0]; math.Abs(float64(got-2*sum)) > 1e-5 {
		t.Errorf("two voices tapping a shared buffer: %v, not %v", got, 2*sum)
	}
	if got := own[4][0]; math.Abs(float64(got-sum)) > 1e-5 {
		t.Errorf("two voices tapping their own buffers: %v, not %v", got, sum)
	}
	bytecode, err := vm.NewBytecode(patch(1), vm.AllFeatures{}, 120)
	if err != nil {
		t.Fatal(err)
	}
	if pv := bytecode.PerVoiceBuffers; len(pv) != 1 || pv[0] != (vm.PerVoiceBuffer{BufferID: 1, FirstVoice: 1, NumVoices: 2}) {
		t.Errorf("the buffers per voice: %+v", pv)
	}
	if n := len(bytecode.BufferRegions); n != 3 {
		t.Errorf("%d buffer regions, not 3: the tap of the other instrument has its own", n)
	}
}
