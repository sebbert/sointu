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
