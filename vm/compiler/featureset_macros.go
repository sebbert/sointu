package compiler

import "github.com/vsariola/sointu/vm"

type FeatureSetMacros struct {
	vm.FeatureSet
}

func (p *FeatureSetMacros) HasOp(instruction string) bool {
	_, ok := p.Opcode(instruction)
	return ok
}

func (p *FeatureSetMacros) GetOp(instruction string) int {
	v, _ := p.Opcode(instruction)
	return v
}

func (p *FeatureSetMacros) Stereo(unitType string) bool {
	return p.SupportsParamValue(unitType, "stereo", 1)
}

func (p *FeatureSetMacros) Mono(unitType string) bool {
	return p.SupportsParamValue(unitType, "stereo", 0)
}

func (p *FeatureSetMacros) StereoAndMono(unitType string) bool {
	return p.Stereo(unitType) && p.Mono(unitType)
}

// LimiterDrive is true when the limiters of the song have the drive
// parameter; otherwise the players leave the gain before the limiter out.
func (p *FeatureSetMacros) LimiterDrive() bool {
	return vm.TransformsParam(p.FeatureSet, "limiter", "drive")
}

// SoftclipOversample is true when a softclip unit of the song oversamples;
// otherwise the players leave the oversampling and its operand out.
func (p *FeatureSetMacros) SoftclipOversample() bool {
	return p.SupportsParamValue("softclip", "oversample", 1)
}

// WidthLowcut is true when the width units of the song have the lowcut
// parameter; otherwise the players leave the high-pass on the side signal
// out.
func (p *FeatureSetMacros) WidthLowcut() bool {
	return vm.TransformsParam(p.FeatureSet, "width", "lowcut")
}

// LadderDrive is true when the ladder filters of the song have the drive
// parameter; otherwise the players leave the gain before the filter out.
func (p *FeatureSetMacros) LadderDrive() bool {
	return vm.TransformsParam(p.FeatureSet, "ladder", "drive")
}

// EnvelopeCurve is true when the envelopes of the song have the curve
// parameter; otherwise the players leave the curved envelope out.
func (p *FeatureSetMacros) EnvelopeCurve() bool {
	return vm.TransformsParam(p.FeatureSet, "envelope", "curve")
}

// set is true when a unit of the type has another value than the neutral
// one for the parameter, or something modulates it. The parts of the players
// below are left out of songs that do not use them: with the neutral value
// they do nothing.
func (p *FeatureSetMacros) set(unitType, paramName string, neutral int) bool {
	return p.SupportsParamValueOtherThan(unitType, paramName, neutral) || p.SupportsModulation(unitType, paramName)
}

// The operands that only the songs have whose units differ in them, see
// vm/operands.go.
func (p *FeatureSetMacros) MCSpreadAddOperand() bool  { return vm.MCSpreadAddOperand(p.FeatureSet) }
func (p *FeatureSetMacros) MCMixTypeOperand() bool    { return vm.MCMixTypeOperand(p.FeatureSet) }
func (p *FeatureSetMacros) MCFilterTypeOperand() bool { return vm.MCFilterTypeOperand(p.FeatureSet) }
func (p *FeatureSetMacros) MCDelayFlagsOperand() bool { return vm.MCDelayFlagsOperand(p.FeatureSet) }

// MCDelayMod is true when the lines of an mcdelay are modulated: moddepth is
// not 0. A modulated modrate counts too, as it could be infinite.
func (p *FeatureSetMacros) MCDelayMod() bool {
	return p.set("mcdelay", "moddepth", 0) || p.SupportsModulation("mcdelay", "modrate")
}

// MCSpreadGain, MCSumGain, MCSumWidth and MCLoopFeedback are true when the
// song uses the parameter: otherwise it is a factor of 1.
func (p *FeatureSetMacros) MCSpreadGain() bool   { return p.set("mcspread", "gain", 64) }
func (p *FeatureSetMacros) MCSumGain() bool      { return p.set("mcsum", "gain", 64) }
func (p *FeatureSetMacros) MCSumWidth() bool     { return p.set("mcsum", "width", 64) }
func (p *FeatureSetMacros) MCLoopFeedback() bool { return p.set("mcloop", "feedback", 128) }

func (p *FeatureSetMacros) SpawnFlagsOperand() bool { return vm.SpawnFlagsOperand(p.FeatureSet) }

// SpawnTranspose and SpawnLength are true when a spawn unit transposes its
// notes or gives them a length; WindowOwnLength when a window unit has a
// length of its own, and WindowNoteLength when one takes the length of the
// spawned note.
func (p *FeatureSetMacros) SpawnTranspose() bool  { return p.set("spawn", "transpose", 64) }
func (p *FeatureSetMacros) SpawnLength() bool     { return p.set("spawn", "length", 0) }
func (p *FeatureSetMacros) WindowOwnLength() bool { return p.set("window", "length", 0) }
func (p *FeatureSetMacros) WindowNoteLength() bool {
	return p.SupportsParamValue("window", "length", 0) || p.SupportsModulation("window", "length")
}
