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

// EnvelopeCurve is true when the envelopes of the song have the curve
// parameter; otherwise the players leave the curved envelope out.
func (p *FeatureSetMacros) EnvelopeCurve() bool {
	return vm.TransformsParam(p.FeatureSet, "envelope", "curve")
}
