package sointu_test

import (
	"testing"

	"github.com/vsariola/sointu"
)

// The aux and in units reach all 16 channels, 7 aux pairs after left and
// right, and a stereo unit on the last channel allowed ends on the last one.
func TestChannelParameter(t *testing.T) {
	for _, typ := range []string{"aux", "in"} {
		found := false
		for _, p := range sointu.UnitTypes[typ].Params {
			if p.Name != "channel" {
				continue
			}
			found = true
			if p.MinValue != 0 || p.MaxValue != sointu.NumChannels-2 {
				t.Errorf("%s: channel from %d to %d", typ, p.MinValue, p.MaxValue)
			}
			for v, want := range map[int]string{0: "left", 1: "right", 2: "aux1 left", 7: "aux3 right", 8: "aux4 left", 13: "aux6 right", 14: "aux7 left"} {
				if got, _ := p.DisplayFunc(v); got != want {
					t.Errorf("%s: channel %d is shown as %q, want %q", typ, v, got, want)
				}
			}
		}
		if !found {
			t.Errorf("%s has no channel parameter", typ)
		}
	}
}

func TestPatchMaxChannel(t *testing.T) {
	unit := func(typ string, channel, stereo int) sointu.Unit {
		return sointu.Unit{Type: typ, Parameters: sointu.ParamMap{"channel": channel, "stereo": stereo}}
	}
	disabled := unit("aux", 14, 1)
	disabled.Disabled = true
	for _, tc := range []struct {
		units []sointu.Unit
		want  int
	}{
		{nil, 1},
		{[]sointu.Unit{{Type: "out", Parameters: sointu.ParamMap{"stereo": 1}}, {Type: "outaux", Parameters: sointu.ParamMap{"stereo": 1}}}, 1},
		{[]sointu.Unit{unit("aux", 2, 0)}, 2},
		{[]sointu.Unit{unit("aux", 6, 1), unit("in", 4, 1)}, 7},
		{[]sointu.Unit{unit("in", 7, 1)}, 8},
		{[]sointu.Unit{unit("aux", 6, 1), disabled}, 7},
		{[]sointu.Unit{unit("in", 14, 1), unit("aux", 8, 0)}, 15},
		// the channel parameter of another unit is not an output channel
		{[]sointu.Unit{unit("oscillator", 12, 0)}, 1},
	} {
		patch := sointu.Patch{{NumVoices: 1}, {NumVoices: 1, Units: tc.units}}
		if got := patch.MaxChannel(); got != tc.want {
			t.Errorf("MaxChannel of %v = %d, want %d", tc.units, got, tc.want)
		}
	}
}
