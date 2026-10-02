package vm

import "github.com/vsariola/sointu"

// The operands below follow the transformed parameters of units that only the
// Go synth and the wasm player have. A song in which every unit of the type
// has the same value for them is encoded without them, and the wasm player
// has only the code for that value. AllFeatures, which the Go synth encodes
// with, has every value, so its bytecode always has all operands. The
// bytecode and the templates of the player both ask these functions.

// uniformParam reports whether the feature set has exactly one of the values
// for the parameter of the unit type, and no value besides.
func uniformParam(f FeatureSet, unitType, paramName string, values ...int) bool {
	for _, v := range values {
		if f.SupportsParamValue(unitType, paramName, v) && !f.SupportsParamValueOtherThan(unitType, paramName, v) {
			return true
		}
	}
	return false
}

// MCSpreadAddOperand is true when an mcspread unit adds to its bus.
func MCSpreadAddOperand(f FeatureSet) bool {
	return f.SupportsParamValueOtherThan("mcspread", "add", 0)
}

// MCMixTypeOperand is true when the mcmix units have different types.
func MCMixTypeOperand(f FeatureSet) bool {
	return !uniformParam(f, "mcmix", "type", sointu.MCMixHadamard, sointu.MCMixHouseholder, sointu.MCMixShuffle)
}

// MCFilterTypeOperand is true when the mcfilter units have different types.
func MCFilterTypeOperand(f FeatureSet) bool {
	return !uniformParam(f, "mcfilter", "type", 0, 1)
}

// MCDelayFlagsOperand is true when an mcdelay unit tracks the note or is an
// allpass.
func MCDelayFlagsOperand(f FeatureSet) bool {
	return f.SupportsParamValue("mcdelay", "notetracking", 1) || f.SupportsParamValue("mcdelay", "allpass", 1)
}

// SpgateInvertOperand is true when some spgate units invert and others do
// not.
func SpgateInvertOperand(f FeatureSet) bool {
	return !uniformParam(f, "spgate", "invert", 0, 1)
}

// SpphaseModeOperand is true when the spphase units have different modes.
func SpphaseModeOperand(f FeatureSet) bool {
	return !uniformParam(f, "spphase", "mode", 0, 1, 2)
}

// SpcombVoicesOperands is true when an spcomb unit takes its notes from an
// instrument: the units then have the first voice and the number of voices.
func SpcombVoicesOperands(f FeatureSet) bool {
	return f.SupportsParamValueOtherThan("spcomb", "instrument", 0)
}

// SpcombIntervalOperands is true when an spcomb unit has an interval: the
// units then have the three intervals.
func SpcombIntervalOperands(f FeatureSet) bool {
	return f.SupportsParamValueOtherThan("spcomb", "interval1", 0) ||
		f.SupportsParamValueOtherThan("spcomb", "interval2", 0) ||
		f.SupportsParamValueOtherThan("spcomb", "interval3", 0)
}

// SpawnFlagsOperand is true when the spawn units differ in mode, note
// tracking, number of arguments or stealing.
func SpawnFlagsOperand(f FeatureSet) bool {
	return !(uniformParam(f, "spawn", "mode", sointu.SpawnModeRate, sointu.SpawnModeEdge, sointu.SpawnModeSync) &&
		uniformParam(f, "spawn", "notetracking", 0, 1) &&
		uniformParam(f, "spawn", "args", 0, 1, 2, 3, 4) &&
		uniformParam(f, "spawn", "steal", 0, 1))
}
