package vm

import (
	"testing"

	"github.com/vsariola/sointu"
)

// TestReverbUnitsShareConstants checks that reverb units with the same
// lengths and decay share their constant data, whatever their modulated
// parameters, and that the others get their own.
func TestReverbUnitsShareConstants(t *testing.T) {
	reverb := func(size, mod int) sointu.Unit {
		return sointu.Unit{Type: "reverb", Parameters: sointu.ParamMap{"size": size, "decay": 90, "highs": 48, "lows": 72, "predelay": 13, "mod": mod, "highcut": 98, "lowcut": 56,
			"gain": 76, "early": 52, "earlywidth": 80, "tailwidth": 96, "modrate": 56, "steps": 4, "spread": 77}}
	}
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 1, "value": 64}},
		reverb(64, 24), reverb(64, 0), reverb(20, 24), reverb(64, 100),
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}}}
	b, err := NewBytecode(patch, NecessaryFeaturesFor(patch), 120)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Reverbs) != 2 {
		t.Fatalf("4 reverb units of two sizes have %d sets of constants, want 2", len(b.Reverbs))
	}
	var indices []byte
	for i := 1 + 3; i < len(b.Operands); i += 4 { // after the value of loadval: mod, highcut, lowcut, index
		indices = append(indices, b.Operands[i])
	}
	if want := []byte{0, 0, 1, 0}; string(indices[:4]) != string(want) {
		t.Errorf("the units use the constants %v, want %v", indices[:4], want)
	}
	if patch.NumReverbs() != 4 {
		t.Errorf("NumReverbs is %d, want 4", patch.NumReverbs())
	}
}

// TestReverbTapsFit checks that the taps of the diffuser fit in their 16
// bits and their rings, and the lines of the network in theirs, with the
// largest size and predelay.
func TestReverbTapsFit(t *testing.T) {
	for _, size := range []int{0, 64, 128} {
		for _, predelay := range []int{0, 128} {
			r := newReverb(sointu.ParamMap{"size": size, "predelay": predelay, "decay": 90, "highs": 48, "lows": 72, "steps": 4, "spread": 77})
			var st reverbState
			st.alloc()
			for k, taps := range r.Taps {
				for c, tap := range taps {
					if behind := int(tap >> 1); behind < 1 || behind >= len(st.rings[k]) {
						t.Errorf("size %d, predelay %d: tap %d of step %d reads %d floats behind, the ring has %d", size, predelay, c, k, behind, len(st.rings[k]))
					}
				}
			}
			for c, l := range r.Lengths {
				if l < 1 || l+mcMaxModSamples+2 > 1<<reverbLog2Frames {
					t.Errorf("size %d: line %d is %v samples, the ring has %d frames", size, c, l, 1<<reverbLog2Frames)
				}
			}
		}
	}
}
