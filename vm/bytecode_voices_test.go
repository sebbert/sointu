package vm_test

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func TestPolyphonyTableMatchesBitmask(t *testing.T) {
	for _, voices := range [][]int{{1}, {3, 2, 4}, {1, 1, 1}, {8, 8, 8, 8}, {31, 1}} {
		var patch sointu.Patch
		for _, n := range voices {
			patch = append(patch, sointu.Instrument{NumVoices: n, Units: []sointu.Unit{{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}}})
		}
		b, err := vm.NewBytecode(patch, vm.AllFeatures{}, 120)
		if err != nil {
			t.Fatal(err)
		}
		if b.WideVoices {
			t.Errorf("%v: wide voices", voices)
		}
		for n, p := range b.Polyphony {
			if want := byte(b.PolyphonyBitmask >> n & 1); p != want {
				t.Errorf("%v: Polyphony[%d] = %d, bitmask has %d", voices, n, p, want)
			}
		}
	}
}

func TestTooManyVoices(t *testing.T) {
	patch := sointu.Patch{{NumVoices: vm.MAX_VOICES + 1, Units: []sointu.Unit{{Type: "loadnote", Parameters: sointu.ParamMap{"stereo": 0}}}}}
	if _, err := vm.NewBytecode(patch, vm.AllFeatures{}, 120); err == nil {
		t.Error("no error with too many voices")
	}
	patch[0].NumVoices = vm.MAX_VOICES
	if b, err := vm.NewBytecode(patch, vm.AllFeatures{}, 120); err != nil || !b.WideVoices {
		t.Errorf("%v voices: %v, wide %v", vm.MAX_VOICES, err, b != nil && b.WideVoices)
	}
}
