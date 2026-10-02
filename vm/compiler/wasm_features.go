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
	MCVoices       bool // an instrument with mc units has several voices: the units check that they run in the first
	SpectralVoices bool // the same for spectral units
	SpectralStereo bool // a spectrum has two channels

	SpphaseDisperse bool // an spphase unit has the mode disperse
	SpphaseRandom   bool
	SpphaseRobot    bool

	BandlimitSine   bool // a sine oscillator is bandlimited
	BandlimitTrisaw bool
	BandlimitPulse  bool

	SpawnEdge       bool // a spawn unit spawns on rising edges
	SpawnSync       bool // at a rate in beats
	SpawnRate       bool // at a rate in Hz
	SpawnTracking   bool // a spawn unit follows the note
	SpawnNoTracking bool // a spawn unit does not
	SpawnSteal      bool // a spawn unit steals held voices
	SpawnNoSteal    bool // a spawn unit does not
	SpawnArgs       int  // the most arguments a spawn unit passes
	SpawnNoTarget   bool // a spawn unit has no instrument to spawn
}

// unitFeatures finds the parts that the units of the song use. The song has
// no module units: it is expanded.
func unitFeatures(song *sointu.Song, b *vm.Bytecode) (f wasmUnitFeatures) {
	for _, sp := range b.Spectra {
		f.SpectralStereo = f.SpectralStereo || sp.Channels > 1
	}
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "" || u.Disabled {
				continue
			}
			p := u.Parameters
			switch {
			case len(sointu.BusParams(u.Type)) > 0:
				f.MCVoices = f.MCVoices || instr.NumVoices > 1
			case len(sointu.SpectrumBufferParams(u.Type)) > 0:
				f.SpectralVoices = f.SpectralVoices || instr.NumVoices > 1
				if u.Type == "spphase" {
					switch min(max(p["mode"], 0), 2) { // like vm.NewBytecode
					case 0:
						f.SpphaseDisperse = true
					case 1:
						f.SpphaseRandom = true
					default:
						f.SpphaseRobot = true
					}
				}
			case u.Type == "oscillator" && sointu.OscillatorBandlimited(u):
				switch p["type"] {
				case sointu.Sine:
					f.BandlimitSine = true
				case sointu.Trisaw:
					f.BandlimitTrisaw = true
				case sointu.Pulse:
					f.BandlimitPulse = true
				}
			case u.Type == "spawn":
				// like the flags of the unit in vm.NewBytecode
				switch p["mode"] {
				case sointu.SpawnModeEdge:
					f.SpawnEdge = true
				case sointu.SpawnModeSync:
					f.SpawnSync = true
				default:
					f.SpawnRate = true
				}
				if p["notetracking"]&1 == 1 {
					f.SpawnTracking = true
				} else {
					f.SpawnNoTracking = true
				}
				if p["steal"]&1 == 1 {
					f.SpawnSteal = true
				} else {
					f.SpawnNoSteal = true
				}
				f.SpawnArgs = max(f.SpawnArgs, min(max(p["args"], 0), sointu.MaxSpawnArgs))
				if t := p["instrument"] - 1; t < 0 || t >= len(song.Patch) {
					f.SpawnNoTarget = true
				}
			}
		}
	}
	return
}
