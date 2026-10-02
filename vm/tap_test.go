package vm_test

import (
	"reflect"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func tapRender(t *testing.T, synth sointu.Synth, frames int) sointu.AudioBuffer {
	t.Helper()
	buf := make(sointu.AudioBuffer, frames)
	if n, _, err := synth.Render(buf, frames); err != nil || n != frames {
		t.Fatalf("rendered %d of %d frames: %v", n, frames, err)
	}
	return buf
}

func channel(audio sointu.AudioBuffer, c int) []float32 {
	ret := make([]float32, len(audio))
	for i := range audio {
		ret[i] = audio[i][c]
	}
	return ret
}

// TestTaps checks that the taps of the Go synth record the signal before
// and after a unit, of one voice or of all, that they leave the render as it
// is, and that they follow the patch.
func TestTaps(t *testing.T) {
	const n = 2000
	units := carryVoice(1)[:4] // envelope, oscillator, mulp, filter
	units = append(units, carryUnit("out", 9, sointu.ParamMap{"stereo": 0, "gain": 128}))
	patch := sointu.Patch{{NumVoices: 1, Units: carryVoice(20)}, {NumVoices: 2, Units: units}}
	// the signal before the filter is what a synth without the filter puts out
	bare := sointu.Patch{patch[0], {NumVoices: 2, Units: append(append([]sointu.Unit{}, units[:3]...), units[4])}}
	trigger := func(s sointu.Synth) {
		s.Trigger(1, 60)
		s.Trigger(2, 67)
	}
	render := func(patch sointu.Patch, points ...sointu.TapPoint) (sointu.Synth, sointu.AudioBuffer) {
		synth, err := vm.GoSynther{}.Synth(patch, 120)
		if err != nil {
			t.Fatal(err)
		}
		if points != nil {
			synth.(sointu.Tapper).SetTaps(points)
		}
		trigger(synth)
		return synth, tapRender(t, synth, n)
	}
	_, plain := render(patch)
	_, before := render(bare)
	loud(t, "plain", plain)
	synth, tapped := render(patch,
		sointu.TapPoint{Instrument: 1, Unit: 3}, sointu.TapPoint{Instrument: 1, Unit: 4},
		sointu.TapPoint{Instrument: 1, Unit: 4, Voice: 1}, sointu.TapPoint{Instrument: 1, Unit: 4, Voice: 2},
		sointu.TapPoint{Instrument: 1, Unit: 5}, sointu.TapPoint{Instrument: 1, Unit: 6}, sointu.TapPoint{Instrument: 7, Unit: 0},
		sointu.TapPoint{Instrument: 1, Unit: 4, Voice: 3})
	if !reflect.DeepEqual(plain, tapped) {
		t.Fatal("the taps change the render")
	}
	tapper := synth.(sointu.Tapper)
	take := func(i int) sointu.AudioBuffer { return tapper.Tapped(i, nil) }
	if got := take(0); len(got) != n || !reflect.DeepEqual(channel(got, 0), channel(before, 0)) {
		t.Errorf("the tap before the filter has %d frames, or not the signal before it", len(got))
	}
	after := take(1)
	if len(after) != n || !reflect.DeepEqual(channel(after, 0), channel(plain, 0)) {
		t.Errorf("the tap after the filter has %d frames, or not the signal after it", len(after))
	}
	one, two := take(2), take(3)
	for i := range after {
		if one[i][0]+two[i][0] != after[i][0] || one[i][0] == 0 && i > 100 {
			t.Fatalf("frame %d: the voices have %v and %v, their sum %v", i, one[i][0], two[i][0], after[i][0])
		}
	}
	// after the last unit, and where the patch has nothing: frames of silence
	for i := 4; i < 8; i++ {
		got := take(i)
		if len(got) != n {
			t.Errorf("tap %d has %d frames", i, len(got))
		}
		for _, f := range got {
			if f[0] != 0 {
				t.Fatalf("tap %d is not silent", i)
			}
		}
	}
	// taken frames are gone; the next ones follow
	more := tapRender(t, synth, 100)
	if got := take(1); len(got) != 100 || !reflect.DeepEqual(channel(got, 0), channel(more, 0)) {
		t.Errorf("after 100 more frames, the tap has %d frames, or not those", len(got))
	}
	if got := tapper.Tapped(1, nil); len(got) != 0 {
		t.Errorf("the tap has %d frames that were taken", len(got))
	}
	// units are counted as the patch has them, also disabled ones; after
	// the patch changes, the places are those of the new patch
	changed := sointu.Patch{patch[0], {NumVoices: 2, Units: withUnit(units, 1, func() sointu.Unit { u := unity(40); u.Disabled = true; return u }())}}
	if err := synth.Update(changed, 120); err != nil {
		t.Fatal(err)
	}
	tapper.SetTaps([]sointu.TapPoint{{Instrument: 1, Unit: 5}})
	take(0) // what the tap at that place had
	more = tapRender(t, synth, 100)
	if got := take(0); len(got) != 100 || !reflect.DeepEqual(channel(got, 0), channel(more, 0)) {
		t.Errorf("with a disabled unit before it, the tap after the filter has %d frames, or not the signal after it", len(got))
	}
	// a tap that stays keeps its frames when the taps are set again; one
	// that is not taken keeps the latest
	tapRender(t, synth, 50)
	tapper.SetTaps([]sointu.TapPoint{{Instrument: 0, Unit: 1}, {Instrument: 1, Unit: 5}})
	if got := take(1); len(got) != 50 {
		t.Errorf("the tap that stayed has %d frames", len(got))
	}
	tapRender(t, synth, 1<<16+10)
	if got := take(1); len(got) != 10 {
		t.Errorf("a tap that nobody took from has %d frames", len(got))
	}
	// no taps: nothing recorded
	tapper.SetTaps(nil)
	tapRender(t, synth, 50)
	if got := take(0); len(got) != 0 {
		t.Errorf("without taps, %d frames were recorded", len(got))
	}
}

// TestTapStereo checks that a tap has the left signal first.
func TestTapStereo(t *testing.T) {
	units := []sointu.Unit{
		carryUnit("envelope", 1, sointu.ParamMap{"attack": 60, "decay": 72, "sustain": 40, "release": 70, "gain": 128}),
		carryUnit("oscillator", 2, sointu.ParamMap{"transpose": 64, "detune": 64, "color": 100, "shape": 64, "gain": 128, "type": sointu.Trisaw}),
		carryUnit("mulp", 3, nil),
		carryUnit("pan", 4, sointu.ParamMap{"panning": 30}),
		carryUnit("out", 5, sointu.ParamMap{"stereo": 1, "gain": 128}),
	}
	for _, synther := range []sointu.Synther{vm.GoSynther{}, vm.MakeMultithreadSynther(vm.GoSynther{})} {
		// on the second thread, after an instrument of the first
		patch := sointu.Patch{{NumVoices: 1, Units: carryVoice(20)}, {NumVoices: 1, Units: units, ThreadMaskM1: 1}}
		synth, err := synther.Synth(patch, 120)
		if err != nil {
			t.Fatal(err)
		}
		tapper, ok := synth.(sointu.Tapper)
		if !ok {
			t.Fatalf("the %s synth has no taps", synther.Name())
		}
		tapper.SetTaps([]sointu.TapPoint{{Instrument: 1, Unit: 4}})
		synth.Trigger(1, 60)
		audio := tapRender(t, synth, 1000)
		loud(t, synther.Name(), audio)
		if got := tapper.Tapped(0, nil); !reflect.DeepEqual(got, audio) {
			t.Errorf("%s synth: the tap before the stereo out is not what it puts out", synther.Name())
		}
		synth.Close()
	}
}

// BenchmarkRenderTaps measures what taps cost: none, and two.
func BenchmarkRenderTaps(b *testing.B) {
	patch := sointu.Patch{{NumVoices: 8, Units: carryVoice(1)}}
	for _, taps := range []int{0, 2} {
		name := "none"
		if taps > 0 {
			name = "two"
		}
		b.Run(name, func(b *testing.B) {
			synth, err := vm.GoSynther{}.Synth(patch, 120)
			if err != nil {
				b.Fatal(err)
			}
			if taps > 0 {
				synth.(sointu.Tapper).SetTaps([]sointu.TapPoint{{Unit: 3}, {Unit: 4}})
			}
			for v := range 8 {
				synth.Trigger(v, byte(60+v))
			}
			buf := make(sointu.AudioBuffer, 4410)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				synth.Render(buf, len(buf))
				if taps > 0 {
					synth.(sointu.Tapper).Tapped(0, buf[:0])
					synth.(sointu.Tapper).Tapped(1, buf[:0])
				}
			}
		})
	}
}

// TestSyncsWithTapsAndUpdate checks that the sync values that the Go synth
// records are the same with taps as without, and go on as those of a synth
// that had the patch all along when a unit is added before the sync unit.
func TestSyncsWithTapsAndUpdate(t *testing.T) {
	const n = 4096
	units := carryVoice(1)[:4] // envelope, oscillator, mulp, filter
	units = append(units, carryUnit("sync", 8, nil), carryUnit("out", 9, sointu.ParamMap{"stereo": 0, "gain": 128}))
	patch := sointu.Patch{{NumVoices: 1, Units: units}}
	added := sointu.Patch{{NumVoices: 1, Units: withUnit(units, 3, unity(20))}}
	run := func(first, second sointu.Patch, taps bool) ([]float32, sointu.AudioBuffer) {
		var syncs []float32
		synth, err := vm.GoSynther{Syncs: &syncs}.Synth(first, 120)
		if err != nil {
			t.Fatal(err)
		}
		if taps {
			synth.(sointu.Tapper).SetTaps([]sointu.TapPoint{{Unit: 4}, {Unit: 5}})
		}
		synth.Trigger(0, 60)
		audio := tapRender(t, synth, n)
		if err := synth.Update(second, 120); err != nil {
			t.Fatal(err)
		}
		return syncs, append(audio, tapRender(t, synth, n)...)
	}
	want, audio := run(added, added, false)
	if len(want) != 2*n/256 {
		t.Fatalf("%d sync values of %d frames", len(want), 2*n)
	}
	loud(t, "sync", audio)
	for _, test := range []struct {
		name          string
		first, second sointu.Patch
		taps          bool
	}{{"with taps", added, added, true}, {"a unit added", patch, added, false}, {"a unit added, with taps", patch, added, true}} {
		got, gotAudio := run(test.first, test.second, test.taps)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotAudio, audio) {
			t.Errorf("%s: the sync values or the render differ", test.name)
		}
	}
}
