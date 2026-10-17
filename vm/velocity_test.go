package vm_test

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

// velocityPatch outputs the velocity v of its voice on the right and v·v on
// the left (the top of the stack), with a mono and a stereo velocity unit.
func velocityPatch(voices int) sointu.Patch {
	return sointu.Patch{{NumVoices: voices, Units: []sointu.Unit{
		{Type: "velocity", Parameters: sointu.ParamMap{"stereo": 0}},
		{Type: "velocity", Parameters: sointu.ParamMap{"stereo": 1}},
		{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}}, // the top: v*v
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}}}
}

// A velocity unit pushes the MIDI velocity of the note over 127, and 1 for
// a note triggered without one; a new note without a velocity is at 1
// again, whatever the note before had.
func TestVelocityUnit(t *testing.T) {
	synth := newSynth(t, velocityPatch(1))
	vt, ok := synth.(sointu.VelocityTriggerer)
	if !ok {
		t.Fatal("the Go synth does not trigger with a velocity")
	}
	check := func(what string, want float64) {
		t.Helper()
		out := render(t, synth, 4)
		l, r := float64(out[3][0]), float64(out[3][1])
		if math.Abs(r-want) > 1e-6 || math.Abs(l-want*want) > 1e-6 {
			t.Errorf("%s: velocity %g and its square %g; want %g, %g", what, r, l, want, want*want)
		}
	}
	synth.Trigger(0, 60)
	check("a note without velocity", 1)
	vt.TriggerVelocity(0, 60, 64)
	check("velocity 64", 64.0/127)
	vt.TriggerVelocity(0, 60, 127)
	check("velocity 127", 1)
	vt.TriggerVelocity(0, 60, 0)
	check("velocity 0 is taken as 1", 1.0/127)
	vt.TriggerVelocity(0, 60, 20)
	synth.Release(0)
	check("released, the velocity stays", 20.0/127)
	synth.Trigger(0, 60)
	check("then a note without velocity", 1)
}

// A voice that a spawn unit triggers has the velocity of the voice that
// spawned it.
func TestVelocitySpawned(t *testing.T) {
	patch := spawnPatch(nil, nil, 1)
	patch[1].Units = []sointu.Unit{
		{Type: "velocity", Parameters: sointu.ParamMap{"stereo": 1}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}
	synth := newSynth(t, patch)
	synth.(sointu.VelocityTriggerer).TriggerVelocity(0, 60, 32)
	out := render(t, synth, 64)
	if got, want := float64(out[63][0]), 32.0/127; math.Abs(got-want) > 1e-6 {
		t.Errorf("the spawned voice has velocity %g, want %g", got, want)
	}
}
