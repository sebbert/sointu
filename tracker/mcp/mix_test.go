package mcp

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestPlayMix checks that the notes of two instruments sound at their
// frames, together, and that a note on an instrument of one voice takes
// the voice of the one before.
func TestPlayMix(t *testing.T) {
	step := func(gain int) sointu.Instrument {
		return sointu.Instrument{Name: "step", NumVoices: 1, Units: []sointu.Unit{
			{ID: gain, Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 0, "sustain": 128, "release": 0, "gain": gain}},
			{ID: gain + 1, Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
		}}
	}
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Patch: sointu.Patch{step(32), step(64)}}
	srcs := []tracker.RemoteRender{
		{Song: song, Synther: vm.GoSynther{}, Instrument: 0, FirstVoice: 0, NumVoices: 1},
		{Song: song, Synther: vm.GoSynther{}, Instrument: 1, FirstVoice: 1, NumVoices: 1},
	}
	note := func(instr, start, length int) []*mixEvent {
		off := &mixEvent{frame: start + length, note: 60, instr: instr}
		return []*mixEvent{{frame: start, note: 60, instr: instr, on: true, off: off}, off}
	}
	var events []*mixEvent
	events = append(events, note(0, 1000, 3000)...) // 0.25 from 1000 to 4000
	events = append(events, note(1, 2000, 1000)...) // 0.5 more from 2000 to 3000
	// on instrument 0 again at 3500, before its first note ends at 4000: it
	// takes the voice, and the end of the first note does not release it
	second := note(0, 3500, 1000)
	all := []*mixEvent{events[0], events[2], events[3], second[0], events[1], second[1]}
	buf, err := playMix(srcs, all, 6000)
	if err != nil {
		t.Fatal(err)
	}
	// the envelope starts a sample after the trigger, and ramps: look in
	// the middle of each stretch
	for _, c := range []struct {
		frame int
		want  float64
	}{{500, 0}, {1500, 0.25}, {2500, 0.75}, {3250, 0.25}, {4200, 0.25}, {5500, 0}} {
		if got := float64(buf[c.frame][0]); math.Abs(got-c.want) > 0.02 {
			t.Errorf("frame %d: %.3f, want %.2f", c.frame, got, c.want)
		}
	}
}

func TestRenderMixOverSocket(t *testing.T) {
	_, info := startTestHost(t)
	notes := []map[string]any{
		{"instrument": "0", "note": 60, "start": 0, "length": 0.5},
		{"instrument": "0", "note": 64, "start": 1, "length": 0.5},
		{"instrument": "Instr", "note": 67, "start": 1, "length": 0.5},
	}
	text, err := callTool(t, info, "render_mix", map[string]any{"notes": notes, "beats": 2})
	if err != nil {
		t.Fatalf("render_mix: %v\n%s", err, text)
	}
	if !strings.Contains(text, "3 notes of 1 instruments") || !strings.Contains(text, "per beat (2), peak dBFS:") {
		t.Errorf("render_mix did not say what it rendered:\n%s", text)
	}
	if _, err := callTool(t, info, "render_mix", map[string]any{"notes": []map[string]any{{"instrument": "0", "start": 0, "length": 0}}}); err == nil {
		t.Error("a note of no length was played")
	}
	raw, _ := json.Marshal(map[string]any{"notes": notes, "beats": 1e6})
	if _, err := Call(info.Socket, "render_mix", raw); err == nil {
		t.Error("a render far too long was made")
	}
}
