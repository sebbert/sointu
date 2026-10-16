package vm

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

func TestAutomationRecords(t *testing.T) {
	// a point after the start, a step, and a record too far for 16 bits
	first, records := automationRecords([]int{160, 256, 320, 512, 320, -128, 320 + 3*65535*16, 128})
	if first != 256 {
		t.Errorf("first value %d, want 256", first)
	}
	want := []automationRecord{{10, 256}, {10, 512}, {1, -128}, {65535, -43}, {65535, 43}, {65534, 128}}
	if len(records) != len(want) {
		t.Fatalf("records %v, want %v", records, want)
	}
	for i, r := range records {
		if r != want[i] {
			t.Errorf("record %d: %v, want %v", i, r, want[i])
		}
	}
	if first, records := automationRecords(nil); first != 0 || len(records) != 0 {
		t.Errorf("no points: %v %v", first, records)
	}
}

// automationPatch is a loadval at 0 whose value a lane offsets, straight to
// the output: the output is the value of the lane over 64.
func automationPatch(args ...int) sointu.Patch {
	return sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "automation", Parameters: sointu.ParamMap{"slot": 7}, VarArgs: args},
		{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 128, "target": 1, "port": 0, "sendpop": 1}},
		{Type: "loadval", ID: 1, Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}}
}

func renderAutomation(t *testing.T, synth sointu.Synth, n int) []float32 {
	t.Helper()
	buf := make(sointu.AudioBuffer, n)
	if got, _, err := synth.Render(buf, n); err != nil || got != n {
		t.Fatalf("rendered %d of %d frames: %v", got, n, err)
	}
	ret := make([]float32, n)
	for i := range buf {
		ret[i] = buf[i][0]
	}
	return ret
}

func TestAutomationValues(t *testing.T) {
	// 0 until sample 16000, up to 32 steps at 32000, down to -16.5 in a
	// step, and that from there on
	for _, tc := range []struct {
		name string
		last int
		fine bool
	}{{"steps", -16 * 128, false}, {"fine", -16*128 - 64, true}} {
		t.Run(tc.name, func(t *testing.T) {
			synth, err := GoSynther{}.Synth(automationPatch(16000, 0, 32000, 32*128, 32000, tc.last), 120)
			if err != nil {
				t.Fatal(err)
			}
			if got := synth.(*GoSynth).bytecode.AutomationFine; got != tc.fine {
				t.Errorf("AutomationFine = %v", got)
			}
			out := renderAutomation(t, synth, 40000)
			last := float32(tc.last) / 128 / 64
			for _, c := range []struct {
				at   int
				want float32
			}{{0, 0}, {15999, 0}, {16000, 0}, {20000, 0.125}, {24000, 0.25}, {31999, 0.5 - 0.5/16000}, {32000, 0.5}, {32016, last}, {39999, last}} {
				if got := out[c.at]; math.Abs(float64(got-c.want)) > 1e-6 {
					t.Errorf("sample %d: %v, want %v", c.at, got, c.want)
				}
			}
			// a note triggered on the way finds its place again
			s := synth.(*GoSynth)
			s.SetSongTime(20000)
			s.Trigger(0, 60)
			if got := renderAutomation(t, s, 1)[0]; got != 0.125 {
				t.Errorf("after a trigger at 20000: %v", got)
			}
			// and so does the song going back
			s.SetSongTime(31999)
			renderAutomation(t, s, 1)
			s.SetSongTime(100)
			if got := renderAutomation(t, s, 1)[0]; got != 0 {
				t.Errorf("after going back: %v", got)
			}
			// a changed table
			if err := s.Update(automationPatch(0, 64*128), 120); err != nil {
				t.Fatal(err)
			}
			if got := renderAutomation(t, s, 1)[0]; got != 1 {
				t.Errorf("after an update: %v", got)
			}
		})
	}
}

func TestAutomationLive(t *testing.T) {
	synth, err := GoSynther{}.Synth(automationPatch(0, 64*128), 120)
	if err != nil {
		t.Fatal(err)
	}
	s := synth.(*GoSynth)
	s.SetSongTime(-1)
	if got := renderAutomation(t, s, 1)[0]; got != 0 {
		t.Errorf("not playing, no live value: %v", got)
	}
	s.SetAutomation(7, 32)
	s.SetAutomation(8, 100) // another slot
	out := renderAutomation(t, s, 2*automationRamp)
	if out[0] != 0 || out[automationRamp/2] <= 0.2 || out[automationRamp/2] >= 0.3 || out[automationRamp] != 0.5 || out[2*automationRamp-1] != 0.5 {
		t.Errorf("the live value: %v, %v, %v", out[0], out[automationRamp/2], out[automationRamp])
	}
	s.SetSongTime(0)
	if got := renderAutomation(t, s, 1)[0]; got != 1 {
		t.Errorf("playing: %v, want the lane", got)
	}
}
