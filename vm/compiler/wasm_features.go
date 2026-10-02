package compiler

import (
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// wasmUnitFeatures tells which parts of the units the song needs, where the
// feature set cannot tell: it knows the values of each parameter, not which
// units have them together. The wasm player leaves the other parts out. Each
// part does nothing in a song that does not need it, so the player renders
// the same without it. The parts that follow from parameter values alone are
// methods of FeatureSetMacros.
type wasmUnitFeatures struct {
	MCVoices bool // an instrument with mc units has several voices: the units check that they run in the first
}

// unitFeatures finds the parts that the units of the song use. The song has
// no module units: it is expanded.
func unitFeatures(song *sointu.Song, b *vm.Bytecode) (f wasmUnitFeatures) {
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "" || u.Disabled {
				continue
			}
			if len(sointu.BusParams(u.Type)) > 0 {
				f.MCVoices = f.MCVoices || instr.NumVoices > 1
			}
		}
	}
	return
}
