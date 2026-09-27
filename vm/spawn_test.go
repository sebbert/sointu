package vm_test

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// spawnPatch returns a patch with a spawner instrument with the given units
// before a spawn unit, and a target instrument with n voices, which outputs
// arg 0 on the left and arg 1 on the right channel.
func spawnPatch(pre []sointu.Unit, spawn sointu.ParamMap, n int) sointu.Patch {
	p := sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 64, "transpose": 64, "notetracking": 1, "args": 0, "instrument": 2}
	for k, v := range spawn {
		p[k] = v
	}
	return sointu.Patch{
		{NumVoices: 1, Units: append(pre, sointu.Unit{Type: "spawn", Parameters: p})},
		{NumVoices: n, Units: []sointu.Unit{
			{Type: "arg", Parameters: sointu.ParamMap{"index": 1}},
			{Type: "arg", Parameters: sointu.ParamMap{"index": 0}}, // left is on top
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
		}},
	}
}

func newSynth(t *testing.T, patch sointu.Patch) sointu.Synth {
	t.Helper()
	synth, err := vm.GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatalf("Synth failed: %v", err)
	}
	return synth
}

func render(t *testing.T, synth sointu.Synth, n int) sointu.AudioBuffer {
	t.Helper()
	out := make(sointu.AudioBuffer, n)
	if _, _, err := synth.Render(out, n); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return out
}

// spawnFrames returns the frames where the right channel changes, i.e. a
// new voice started, when arg 1 is random. prev is the value before out.
func spawnFrames(out sointu.AudioBuffer, prev float32) []int {
	var ret []int
	for i, f := range out {
		if f[1] != prev {
			ret = append(ret, i)
		}
		prev = f[1]
	}
	return ret
}

func TestSpawnRate(t *testing.T) {
	// rate 64 = 8 Hz: a spawn right away, then every 5512.5 frames. Each
	// spawn passes a growing value, so the output of the single target
	// voice changes at each spawn.
	pre := []sointu.Unit{
		{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
		{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
	}
	synth := newSynth(t, spawnPatch(pre, sointu.ParamMap{"args": 2}, 1))
	out := render(t, synth, 100)
	if out[0][0] != 0 {
		t.Fatalf("spawned before the spawner was triggered: %v", out[0])
	}
	synth.Trigger(0, 60)
	out = render(t, synth, 20000)
	frames := spawnFrames(out, 0)
	if len(frames) != 4 || frames[0] != 0 || frames[1] < 5510 || frames[1] > 5515 || frames[2] < 11020 || frames[2] > 11030 {
		t.Errorf("spawned at frames %v, want 0 and about every 5512.5 frames", frames)
	}
	if out[0][0] != 1 {
		t.Errorf("arg 0: got %v, want 1", out[0][0])
	}
	synth.Release(0)
	if frames := spawnFrames(render(t, synth, 20000), out[len(out)-1][1]); len(frames) != 0 {
		t.Errorf("spawned at %v after the spawner was released", frames)
	}
}

func TestSpawnEdge(t *testing.T) {
	// an LFO pulse: spawns on each rising edge while held; with 2 voices, the
	// output grows to 2 and stays
	pre := []sointu.Unit{
		{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
		{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Pulse, "lfo": 1}},
	}
	synth := newSynth(t, spawnPatch(pre, sointu.ParamMap{"mode": sointu.SpawnModeEdge, "args": 1}, 2))
	synth.Trigger(0, 60)
	out := render(t, synth, 44100*4)
	if last := out[len(out)-1][0]; last != 2 {
		t.Errorf("last frame: got %v, want 2 from two spawned voices", last)
	}
}

func TestSpawnStealsVoices(t *testing.T) {
	pre := []sointu.Unit{
		{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}},
	}
	patch := spawnPatch(pre, sointu.ParamMap{"args": 1, "rate": 128}, 3)
	synth := newSynth(t, patch)
	synth.Trigger(0, 60)
	out := render(t, synth, 200)
	// 2048 Hz: a spawn every 21.5 frames; three voices take 0.5 each, and
	// after that voices are stolen, so the sum stays 1.5
	if out[0][0] != 0.5 || out[30][0] != 1 || out[50][0] != 1.5 || out[199][0] != 1.5 {
		t.Errorf("got %v %v %v %v, want 0.5 1 1.5 1.5", out[0][0], out[30][0], out[50][0], out[199][0])
	}
}

func TestSpawnNote(t *testing.T) {
	// the target plays an oscillator following the spawned note
	play := func(note byte, spawn sointu.ParamMap) sointu.AudioBuffer {
		p := sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 64, "args": 0, "instrument": 2}
		for k, v := range spawn {
			p[k] = v
		}
		synth := newSynth(t, sointu.Patch{
			{NumVoices: 1, Units: []sointu.Unit{{Type: "spawn", Parameters: p}}},
			{NumVoices: 1, Units: []sointu.Unit{
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
		})
		synth.Trigger(0, note)
		return render(t, synth, 1000)
	}
	same := func(a, b sointu.AudioBuffer) bool {
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	a := play(60, sointu.ParamMap{"notetracking": 1, "transpose": 76}) // 60 + 12
	b := play(48, sointu.ParamMap{"notetracking": 0, "transpose": 76}) // C-4 + 12
	c := play(48, sointu.ParamMap{"notetracking": 1, "transpose": 76}) // 48 + 12
	if !same(a, b) {
		t.Error("note 60 + 12 with note tracking differs from C-4 + 12 without")
	}
	if same(a, c) {
		t.Error("note tracking made no difference")
	}
	if d := play(60, sointu.ParamMap{"notetracking": 1, "transpose": 0}); !same(d, play(60, sointu.ParamMap{"notetracking": 0, "transpose": 5})) {
		t.Error("notes below 1 are not clamped to 1") // 60-64 and 60-59 both clamp to 1
	}
}

func TestSpawnMultithread(t *testing.T) {
	// the spawner and its target are on the second thread, after an
	// instrument on the first thread, so their indices differ per thread
	patch := sointu.Patch{
		{NumVoices: 1, ThreadMaskM1: 0, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 80}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
	}
	patch = append(patch, spawnPatch([]sointu.Unit{{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 96}}}, sointu.ParamMap{"args": 1, "instrument": 3}, 2)...)
	patch[1].ThreadMaskM1, patch[2].ThreadMaskM1 = 1, 1
	want := func(s sointu.Synth) sointu.AudioBuffer {
		s.Trigger(0, 60)
		s.Trigger(1, 60)
		return render(t, s, 100)
	}
	single := want(newSynth(t, patch))
	multi, err := vm.MakeMultithreadSynther(vm.GoSynther{}).Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	defer multi.Close()
	got := want(multi)
	if single[99][0] != 0.25+0.5 {
		t.Fatalf("single thread: got %v, want 0.75", single[99][0])
	}
	for i := range single {
		if got[i] != single[i] {
			t.Fatalf("frame %d: multithreaded %v, single threaded %v", i, got[i], single[i])
		}
	}
	if patch[1].Units[1].Parameters["instrument"] != 3 {
		t.Error("splitting the patch changed the original")
	}
}

func TestSpawnLength(t *testing.T) {
	// the target outputs 1 while its note is held: an envelope with instant
	// attack and release, and full sustain
	synth := newSynth(t, sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{{Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 0, "transpose": 64, "length": 16, "args": 0, "instrument": 2}}}},
		{NumVoices: 1, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 0, "sustain": 128, "release": 0, "gain": 128}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}},
	})
	synth.Trigger(0, 60)
	out := render(t, synth, 100)
	// length 16 is floor(4410 * 2^(16/8 - 8)) = 68 frames
	if out[1][0] != 1 || out[67][0] != 1 || out[70][0] != 0 {
		t.Errorf("got %v at 1, %v at 67 and %v at 70, want 1 1 0", out[1][0], out[67][0], out[70][0])
	}
	if got := sointu.LengthFrames(16.0 / 128); got != 68 {
		t.Errorf("LengthFrames: got %v, want 68", got)
	}
}

func TestWindow(t *testing.T) {
	synth := newSynth(t, sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "window", Parameters: sointu.ParamMap{"length": 16, "shape": 128}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}})
	if out := render(t, synth, 2); out[0][0] != 0 || out[1][0] != 0 {
		t.Errorf("not silent before the note: %v", out)
	}
	synth.Trigger(0, 60)
	out := render(t, synth, 80) // 68 frames long
	if out[0][0] != 0 || out[34][0] != 1 || out[68][0] != 0 || out[79][0] != 0 {
		t.Errorf("got %v at 0, %v at 34, %v at 68, want 0 1 0", out[0][0], out[34][0], out[68][0])
	}
	if d := out[10][0] - out[58][0]; d > 1e-6 || d < -1e-6 || out[10][0] <= 0 || out[10][0] >= 1 {
		t.Errorf("not symmetric: %v at 10, %v at 58", out[10][0], out[58][0])
	}
	// a narrower taper
	synth = newSynth(t, sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "window", Parameters: sointu.ParamMap{"length": 16, "shape": 32}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}})
	synth.Trigger(0, 60)
	out = render(t, synth, 70)
	if out[10][0] != 1 || out[4][0] <= 0 || out[4][0] >= 1 {
		t.Errorf("with a quarter taper: got %v at 4, %v at 10", out[4][0], out[10][0])
	}
}

func TestWindowTakesSpawnLength(t *testing.T) {
	play := func(windowLength, spawnLength int) sointu.AudioBuffer {
		synth := newSynth(t, sointu.Patch{
			{NumVoices: 1, Units: []sointu.Unit{{Type: "spawn", Parameters: sointu.ParamMap{"mode": sointu.SpawnModeRate, "rate": 0, "transpose": 64, "length": spawnLength, "args": 0, "instrument": 2}}}},
			{NumVoices: 1, Units: []sointu.Unit{
				{Type: "window", Parameters: sointu.ParamMap{"length": windowLength, "shape": 128}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
		})
		synth.Trigger(0, 60)
		return render(t, synth, 80)
	}
	fromNote, explicit := play(0, 16), play(16, 0)
	for i := range fromNote {
		if fromNote[i] != explicit[i] {
			t.Fatalf("frame %d: window from the note %v, explicit %v", i, fromNote[i], explicit[i])
		}
	}
	if out := play(0, 0); out[0][0] != 1 || out[79][0] != 1 {
		t.Errorf("a note without a length: got %v, want an open window (1)", out[0][0])
	}
	if out := play(8, 16); out[34][0] == 1 {
		t.Error("the window length did not override the note length")
	}
}

func TestSpawnSync(t *testing.T) {
	// at 120 BPM, rate 64 spawns once per beat, i.e. every 22050 frames
	pre := []sointu.Unit{
		{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 128}},
		{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
	}
	synth := newSynth(t, spawnPatch(pre, sointu.ParamMap{"mode": sointu.SpawnModeSync, "rate": 64, "args": 2}, 1))
	synth.Trigger(0, 60)
	frames := spawnFrames(render(t, synth, 50000), 0)
	if len(frames) != 3 || frames[0] != 0 || frames[1] < 22049 || frames[1] > 22051 || frames[2] < 44099 || frames[2] > 44101 {
		t.Errorf("spawned at frames %v, want 0, 22050 and 44100", frames)
	}
}
