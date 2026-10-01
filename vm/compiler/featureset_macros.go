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
