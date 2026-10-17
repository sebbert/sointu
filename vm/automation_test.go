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

// TestAutomationTargetsMatchUnits renders a patch whose lanes offset
// parameters through the automation table of its instruments, and the same
// patch with an automation unit and a send before each target, as the
// expansion made them before: from the points of the lanes and from the
// live values, in several voices, with amounts and a second instrument.
func TestAutomationTargetsMatchUnits(t *testing.T) {
	ramp := []int{0, 0, 8000, 40 * 128, 16000, -20 * 128}
	saw := []int{0, 10 * 128, 4000, -64 * 128, 12000, 64 * 128}
	voice := func(id int) []sointu.Unit {
		return []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 96, "release": 64, "gain": 128}},
			{Type: "oscillator", ID: id, Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "filter", ID: id + 1, Parameters: sointu.ParamMap{"stereo": 0, "frequency": 64, "resonance": 64, "lowpass": 1}},
			{Type: "out", ID: id + 2, Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}},
		}
	}
	table := sointu.Patch{
		{NumVoices: 3, Units: voice(10), Automation: []sointu.AutomationTarget{
			{Slot: 1, Unit: 10, Port: sointu.PortOf("oscillator", "detune"), Amount: 128, Points: ramp},
			{Slot: 1, Unit: 11, Port: sointu.PortOf("filter", "frequency"), Amount: 32, Points: ramp},
			{Slot: 2, Unit: 12, Port: sointu.PortOf("out", "gain"), Amount: 96, Points: saw},
		}},
		{NumVoices: 1, Units: voice(20), Automation: []sointu.AutomationTarget{
			{Slot: 1, Unit: 21, Port: sointu.PortOf("filter", "frequency"), Amount: 0, Points: ramp},
		}},
	}
	// the same with units: a pair before each target
	units := table.Copy()
	for i := range units {
		for _, at := range units[i].Automation {
			for j, u := range units[i].Units {
				if u.ID != at.Unit {
					continue
				}
				pair := []sointu.Unit{
					{Type: "automation", Parameters: sointu.ParamMap{"slot": at.Slot}, VarArgs: at.Points},
					{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": at.Amount, "voice": 0, "target": at.Unit, "port": at.Port, "sendpop": 1}},
				}
				units[i].Units = append(units[i].Units[:j], append(pair, units[i].Units[j:]...)...)
				break
			}
		}
		units[i].Automation = nil
	}
	render := func(patch sointu.Patch, live bool) []float32 {
		synth, err := GoSynther{}.Synth(patch, 120)
		if err != nil {
			t.Fatal(err)
		}
		if live {
			synth.(*GoSynth).SetSongTime(-1)
			synth.(*GoSynth).SetAutomation(1, 30)
			synth.(*GoSynth).SetAutomation(2, -50)
		}
		for v, note := range []byte{60, 64, 67, 48} {
			synth.Trigger(v, note)
		}
		return renderAutomation(t, synth, 20000)
	}
	for _, live := range []bool{false, true} {
		want, got := render(units, live), render(table, live)
		differ := 0
		for i := range want {
			if want[i] != got[i] {
				differ++
			}
		}
		if differ > 0 {
			t.Errorf("live %v: %d of %d frames differ", live, differ, len(want))
		}
	}
	// the targets are no units
	a, _ := NewBytecode(table, AllFeatures{}, 120)
	b, _ := NewBytecode(units, AllFeatures{}, 120)
	if len(b.Opcodes)-len(a.Opcodes) != 8 || a.AutomationLanes != 2 {
		t.Errorf("opcodes %d with the table, %d with units; %d lanes", len(a.Opcodes), len(b.Opcodes), a.AutomationLanes)
	}
}
