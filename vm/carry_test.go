package vm_test

import (
	"reflect"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func carryUnit(typ string, id int, params sointu.ParamMap, varArgs ...int) sointu.Unit {
	u := sointu.MakeUnit(typ)
	u.ID = id
	for k, v := range params {
		u.Parameters[k] = v
	}
	if varArgs != nil {
		u.VarArgs = varArgs
	}
	return u
}

// carryVoice returns the units of a voice with state of every kind: an
// envelope, an oscillator, a resonant filter, a delay, an ott and a limiter.
// The IDs start from id.
func carryVoice(id int) []sointu.Unit {
	return []sointu.Unit{
		carryUnit("envelope", id, sointu.ParamMap{"attack": 60, "decay": 72, "sustain": 40, "release": 70, "gain": 128}),
		carryUnit("oscillator", id+1, sointu.ParamMap{"transpose": 64, "detune": 64, "color": 100, "shape": 64, "gain": 128, "type": sointu.Trisaw}),
		carryUnit("mulp", id+2, nil),
		carryUnit("filter", id+3, sointu.ParamMap{"frequency": 50, "resonance": 16, "lowpass": 1}),
		carryUnit("delay", id+4, sointu.ParamMap{"pregain": 80, "dry": 128, "feedback": 110, "damp": 20, "notetracking": 0}, 1117, 1493),
		carryUnit("ott", id+5, sointu.ParamMap{"depth": 100, "time": 64, "upward": 64, "downward": 64, "lowgain": 64, "midgain": 64, "highgain": 64}),
		carryUnit("limiter", id+6, sointu.ParamMap{"threshold": 100, "release": 64, "lookahead": 32}),
		carryUnit("out", id+7, sointu.ParamMap{"stereo": 0, "gain": 128}),
	}
}

// unity is a unit that leaves the signal exactly as it is: a gain of 1.
func unity(id int) sointu.Unit { return carryUnit("gain", id, sointu.ParamMap{"gain": 128}) }

func withUnit(units []sointu.Unit, at int, u sointu.Unit) []sointu.Unit {
	ret := append([]sointu.Unit{}, units[:at]...)
	return append(append(ret, u), units[at:]...)
}

func withoutIDs(units []sointu.Unit) []sointu.Unit {
	ret := append([]sointu.Unit{}, units...)
	for i := range ret {
		ret[i].ID = 0
	}
	return ret
}

type carryStep struct {
	patch    sointu.Patch // nil: as before
	triggers map[int]byte // voice -> note
	releases []int
	frames   int
}

// carryRender plays the steps: each updates the patch, if it has one,
// triggers and releases voices, and renders. It returns what was rendered
// from the step from on.
func carryRender(t *testing.T, steps []carryStep, from int) sointu.AudioBuffer {
	t.Helper()
	var synth sointu.Synth
	var ret sointu.AudioBuffer
	for i, step := range steps {
		var err error
		switch {
		case synth == nil:
			synth, err = vm.GoSynther{}.Synth(step.patch, 120)
		case step.patch != nil:
			err = synth.Update(step.patch, 120)
		}
		if err != nil {
			t.Fatal(err)
		}
		for v, note := range step.triggers {
			synth.Trigger(v, note)
		}
		for _, v := range step.releases {
			synth.Release(v)
		}
		buf := make(sointu.AudioBuffer, step.frames)
		if n, _, err := synth.Render(buf, step.frames); err != nil || n != step.frames {
			t.Fatalf("step %d: rendered %d of %d frames: %v", i, n, step.frames, err)
		}
		if i >= from {
			ret = append(ret, buf...)
		}
	}
	return ret
}

func loud(t *testing.T, name string, audio sointu.AudioBuffer) {
	t.Helper()
	var peak float32
	for _, s := range audio {
		peak = max(peak, s[0], -s[0])
	}
	if peak < 0.01 {
		t.Errorf("%s: the render is silent (peak %v)", name, peak)
	}
}

// TestUpdateKeepsState checks that the units that a change of the patch
// leaves as they are keep their state: the render goes on exactly as that of
// a synth that had the new patch all along, when the change is a unit that
// leaves the signal as it is, or that does not reach the output.
func TestUpdateKeepsState(t *testing.T) {
	const n = 3000 // frames before and after the change
	base := carryVoice(1)
	one := func(units []sointu.Unit) sointu.Patch { return sointu.Patch{{NumVoices: 2, Units: units}} }
	play := map[int]byte{0: 60, 1: 67}
	tests := []struct {
		name          string
		before, after sointu.Patch
	}{
		{"nothing", one(base), one(base)},
		{"a parameter that does nothing yet", one(withUnit(base, 3, unity(20))), one(withUnit(base, 3, unity(20)))},
		{"unit added", one(base), one(withUnit(base, 3, unity(20)))},
		{"unit added at the start", one(base), one(withUnit(withUnit(base, 0, carryUnit("loadval", 21, nil)), 1, carryUnit("pop", 22, nil)))},
		{"unit added after the delay", one(base), one(withUnit(base, 5, unity(20)))},
		{"unit removed", one(withUnit(base, 3, unity(20))), one(base)},
		{"unit moved", one(withUnit(base, 3, unity(20))), one(withUnit(base, 6, unity(20)))},
		{"two units added, one removed", one(withUnit(base, 4, unity(20))), one(withUnit(withUnit(base, 3, unity(21)), 6, unity(22)))},
		{"unit disabled", one(withUnit(base, 3, unity(20))), one(withUnit(base, 3, func() sointu.Unit { u := unity(20); u.Disabled = true; return u }()))},
		{"without IDs", one(withoutIDs(base)), one(withoutIDs(withUnit(base, 3, unity(20))))},
		{"without IDs, removed", one(withoutIDs(withUnit(base, 5, unity(20)))), one(withoutIDs(base))},
		{"other IDs", one(base), one(withUnit(carryVoice(100), 3, unity(20)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := carryRender(t, []carryStep{{patch: test.before, triggers: play, frames: n}, {patch: test.after, releases: []int{1}, frames: n}}, 0)
			want := carryRender(t, []carryStep{{patch: test.after, triggers: play, frames: n}, {releases: []int{1}, frames: n}}, 0)
			loud(t, test.name, want[n:])
			if !reflect.DeepEqual(got, want) {
				t.Errorf("after the change, the render differs from that of a synth that had the patch all along")
			}
		})
	}
}

// TestUpdateKeepsStateOfOthers checks that the states follow the units when
// instruments and voices before them come and go, also the delay lines, otts
// and limiters, which are kept in the order the units run.
func TestUpdateKeepsStateOfOthers(t *testing.T) {
	const n = 3000
	first, second := carryVoice(1), carryVoice(11)
	instr := func(voices int, units []sointu.Unit) sointu.Instrument {
		return sointu.Instrument{NumVoices: voices, Units: units}
	}
	// what the second instrument plays alone, on its voices 0 and 1
	alone := sointu.Patch{instr(2, second)}
	want := carryRender(t, []carryStep{{patch: alone, triggers: map[int]byte{0: 55, 1: 62}, frames: n}, {releases: []int{0}, frames: n}}, 1)
	loud(t, "alone", want)
	// the first instrument is silent: its voices are never triggered
	tests := []struct {
		name          string
		before, after sointu.Patch
		voice, moved  int // the first voice of the second instrument, before and after
	}{
		{"instrument removed before it", sointu.Patch{instr(1, first), instr(2, second)}, alone, 1, 0},
		{"instrument added before it", alone, sointu.Patch{instr(3, first), instr(2, second)}, 0, 3},
		{"voices added before it", sointu.Patch{instr(1, first), instr(2, second)}, sointu.Patch{instr(4, first), instr(2, second)}, 1, 4},
		{"voices removed before it", sointu.Patch{instr(3, first), instr(2, second)}, sointu.Patch{instr(1, first), instr(2, second)}, 3, 1},
		{"instruments swapped", sointu.Patch{instr(1, first), instr(2, second)}, sointu.Patch{instr(2, second), instr(1, first)}, 1, 0},
		{"a delay removed before it", sointu.Patch{instr(1, first), instr(2, second)}, sointu.Patch{instr(1, append(append([]sointu.Unit{}, first[:4]...), first[7])), instr(2, second)}, 1, 1},
		{"voices of its own added", alone, sointu.Patch{instr(4, second)}, 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := carryRender(t, []carryStep{
				{patch: test.before, triggers: map[int]byte{test.voice: 55, test.voice + 1: 62}, frames: n},
				{patch: test.after, releases: []int{test.moved}, frames: n}}, 1)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("after the change, the second instrument does not go on as it would have alone")
			}
		})
	}
}

// TestUpdateStartsChangedUnits checks that a unit of another type than
// before starts from nothing, and the units around it go on.
func TestUpdateStartsChangedUnits(t *testing.T) {
	const n = 3000
	units := carryVoice(1)[:4]
	units = append(units, carryUnit("out", 9, sointu.ParamMap{"stereo": 0, "gain": 128}))
	before := sointu.Patch{{NumVoices: 1, Units: units}}
	// the filter becomes a hold, with the ID of the filter: a hold whose
	// state were that of the filter would hold another value
	changed := append([]sointu.Unit{}, units...)
	changed[3] = carryUnit("hold", 4, sointu.ParamMap{"holdfreq": 20})
	after := sointu.Patch{{NumVoices: 1, Units: changed}}
	got := carryRender(t, []carryStep{{patch: before, triggers: map[int]byte{0: 60}, frames: n}, {patch: after, frames: n}}, 1)
	// the same as a hold added, with another ID, where there was no unit: a
	// new unit starts from nothing
	without := append(append([]sointu.Unit{}, units[:3]...), units[4])
	added := withUnit(without, 3, carryUnit("hold", 40, sointu.ParamMap{"holdfreq": 20}))
	want := carryRender(t, []carryStep{{patch: sointu.Patch{{NumVoices: 1, Units: without}}, triggers: map[int]byte{0: 60}, frames: n}, {patch: sointu.Patch{{NumVoices: 1, Units: added}}, frames: n}}, 1)
	loud(t, "changed", want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after the type of a unit changed, the render differs from that of a unit added after the same envelope and oscillator")
	}
}

// reverbVoice returns the units of a voice with a reverb unit, whose state
// the synth keeps in a table of its own. The IDs start from id.
func reverbVoice(id int) []sointu.Unit {
	return []sointu.Unit{
		carryUnit("envelope", id, sointu.ParamMap{"attack": 40, "decay": 60, "sustain": 0, "release": 60, "gain": 128}),
		carryUnit("oscillator", id+1, sointu.ParamMap{"transpose": 64, "detune": 64, "color": 100, "shape": 64, "gain": 128, "type": sointu.Trisaw}),
		carryUnit("mulp", id+2, nil),
		carryUnit("pan", id+3, sointu.ParamMap{"panning": 50}),
		carryUnit("reverb", id+4, sointu.ParamMap{"decay": 100, "mod": 30}),
		carryUnit("out", id+5, sointu.ParamMap{"stereo": 1, "gain": 128}),
	}
}

// TestUpdateKeepsReverb checks that a reverb unit keeps its state, the
// tail of what it was given, when units before it and instruments with
// reverbs before its own come and go.
func TestUpdateKeepsReverb(t *testing.T) {
	const n = 6000
	base := reverbVoice(1)
	stereoUnity := carryUnit("gain", 30, sointu.ParamMap{"gain": 128, "stereo": 1})
	one := func(units []sointu.Unit) sointu.Patch { return sointu.Patch{{NumVoices: 1, Units: units}} }
	other := sointu.Instrument{NumVoices: 2, Units: reverbVoice(11)}
	// a reverb of its own in the same instrument, whose output is dropped:
	// with it, the state of the other reverb is the second of the table
	aside := func() []sointu.Unit {
		return []sointu.Unit{
			carryUnit("loadval", 40, sointu.ParamMap{"stereo": 1, "value": 100}),
			carryUnit("reverb", 41, nil),
			carryUnit("pop", 42, sointu.ParamMap{"stereo": 1}),
		}
	}
	tests := []struct {
		name          string
		before, after sointu.Patch
		voice, moved  int
	}{
		{"unit added before the reverb", one(base), one(withUnit(base, 4, stereoUnity)), 0, 0},
		{"unit removed after the reverb", one(withUnit(base, 5, stereoUnity)), one(base), 0, 0},
		{"without IDs", one(withoutIDs(base)), one(withoutIDs(withUnit(base, 4, stereoUnity))), 0, 0},
		{"instrument with reverbs removed before it", sointu.Patch{other, one(base)[0]}, one(base), 2, 0},
		{"instrument with reverbs added before it", one(base), sointu.Patch{other, one(base)[0]}, 0, 2},
		{"another reverb removed before it", one(append(aside(), base...)), one(base), 0, 0},
		{"another reverb added before it", one(base), one(append(aside(), base...)), 0, 0},
	}
	// what the instrument plays alone: a note that ends before the change,
	// so that after it only the tail of the reverb is left
	want := carryRender(t, []carryStep{{patch: one(base), triggers: map[int]byte{0: 60}, frames: n}, {releases: []int{0}, frames: n}}, 1)
	loud(t, "the tail of the reverb", want)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := carryRender(t, []carryStep{
				{patch: test.before, triggers: map[int]byte{test.voice: 60}, frames: n},
				{patch: test.after, releases: []int{test.moved}, frames: n}}, 1)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("after the change, the reverb does not go on as it would have")
			}
		})
	}
}
