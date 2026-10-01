package vm

import (
	"sort"

	"github.com/vsariola/sointu"
)

type (
	// FeatureSet defines what opcodes / parameters are included in the compiled virtual machine
	// It is used by the compiler to decide how to encode opcodes
	FeatureSet interface {
		Opcode(unitType string) (int, bool)
		TransformCount(unitType string) int
		Instructions() []string
		InputNumber(unitType string, paramName string) int
		SupportsParamValue(unitType string, paramName string, value int) bool
		SupportsParamValueOtherThan(unitType string, paramName string, value int) bool
		SupportsModulation(unitType string, paramName string) bool
		SupportsPolyphony() bool
		SupportsGlobalSend() bool
	}

	// AllFeatures is used by the library compilation / bridging to configure a virtual machine
	// that supports every conceivable parameter, so it needs no members and just returns "true" to all
	// queries about what it supports. Contrast this NecessaryFeatures that only returns true if the patch
	// needs support for that feature
	AllFeatures struct {
	}

	// NecessaryFeatures returns true only if the patch actually needs the support for the feature
	NecessaryFeatures struct {
		opcodes            map[string]int
		instructions       []string
		supportsParamValue map[paramKey](map[int]bool)
		supportsModulation map[paramKey]bool
		globalSend         bool
		polyphony          bool
	}
)

type paramKey struct {
	Unit  string
	Param string
}

// optionalParams are transformed parameters that songs compiled before they
// existed did without, with the value that does nothing. Each is the last
// transformed parameter of its unit type. NecessaryFeatures leaves it out of
// the transform count, and so out of the bytecode and the players, unless a
// unit of the song sets it to another value or something modulates it; such
// songs then compile exactly as before.
var optionalParams = map[string]struct {
	Name  string
	Value int
}{
	"envelope": {"curve", 0},
	"limiter":  {"drive", 0},
}

var allOpcodes map[string]int
var allInstructions []string
var allInputs map[paramKey]int
var allTransformCounts map[string]int

func init() {
	allInstructions = make([]string, 0, len(sointu.UnitTypes))
	allOpcodes = map[string]int{}
	allTransformCounts = map[string]int{}
	allInputs = map[paramKey]int{}
	for k, v := range sointu.UnitTypes {
		if v.Virtual {
			continue // replaced by Song.Expand: no opcode
		}
		inputCount := 0
		transformCount := 0
		for _, t := range v.Params {
			if t.CanModulate {
				allInputs[paramKey{k, t.Name}] = inputCount
				inputCount++
			}
			if t.CanModulate && t.CanSet && !t.NoTransform {
				transformCount++
			}
		}
		allInstructions = append(allInstructions, k) // Opcode 0 is reserved for instrument advance, so opcodes start from 1
		allTransformCounts[k] = transformCount
	}
	sort.Strings(allInstructions) // sort the opcodes to have predictable ordering, as maps don't guarantee the order the items
	for i, instruction := range allInstructions {
		allOpcodes[instruction] = (i + 1) * 2 // make a map to find out the opcode number based on the type
	}
}

func (_ AllFeatures) SupportsParamValue(unit string, paramName string, value int) bool {
	return true
}

func (_ AllFeatures) SupportsParamValueOtherThan(unit string, paramName string, value int) bool {
	return true
}

func (_ AllFeatures) SupportsModulation(unit string, port string) bool {
	return true
}

func (_ AllFeatures) SupportsPolyphony() bool {
	return true
}

func (_ AllFeatures) SupportsGlobalSend() bool {
	return true
}

func (_ AllFeatures) Opcode(unitType string) (int, bool) {
	code, ok := allOpcodes[unitType]
	return code, ok
}

func (_ AllFeatures) TransformCount(unitType string) int {
	return allTransformCounts[unitType]
}

func (_ AllFeatures) Instructions() []string {
	return allInstructions
}

func (_ AllFeatures) InputNumber(unitType string, paramName string) int {
	return allInputs[paramKey{unitType, paramName}]
}

func NecessaryFeaturesFor(patch sointu.Patch) NecessaryFeatures {
	features := NecessaryFeatures{opcodes: map[string]int{}, supportsParamValue: map[paramKey](map[int]bool){}, supportsModulation: map[paramKey]bool{}}
	for instrIndex, instrument := range patch {
		for _, unit := range instrument.Units {
			if unit.Type == "" || unit.Disabled {
				continue
			}
			if _, ok := features.opcodes[unit.Type]; !ok {
				features.instructions = append(features.instructions, unit.Type)
				features.opcodes[unit.Type] = len(features.instructions) * 2 // note that the first opcode gets value 1, as 0 is always reserved for advance
			}
			for _, paramType := range sointu.UnitTypes[unit.Type].Params {
				v := unit.Parameters[paramType.Name]
				if unit.Type == "oscillator" && paramType.Name == "bandlimit" && !sointu.OscillatorBandlimited(unit) {
					v = 0 // ignored, so the player needs no code for it
				}
				key := paramKey{unit.Type, paramType.Name}
				if features.supportsParamValue[key] == nil {
					features.supportsParamValue[key] = map[int]bool{}
				}
				features.supportsParamValue[key][v] = true
			}
			if unit.Type == "send" {
				targetInstrIndex, targetUnitIndex, err := patch.FindUnit(unit.Parameters["target"])
				if err != nil {
					continue
				}
				targetUnit := patch[targetInstrIndex].Units[targetUnitIndex]
				portList := sointu.Ports[targetUnit.Type]
				portIndex := unit.Parameters["port"]
				if portIndex < 0 || portIndex >= len(portList) {
					continue
				}
				if targetInstrIndex != instrIndex || unit.Parameters["voice"] > 0 {
					features.globalSend = true
				}
				features.supportsModulation[paramKey{targetUnit.Type, portList[portIndex]}] = true
			}
		}
		if instrument.NumVoices > 1 {
			features.polyphony = true
		}
	}
	return features
}

func (n NecessaryFeatures) SupportsParamValue(unit string, paramName string, value int) bool {
	m, ok := n.supportsParamValue[paramKey{unit, paramName}]
	if !ok {
		return false
	}
	return m[value]
}

func (n NecessaryFeatures) SupportsParamValueOtherThan(unit string, paramName string, value int) bool {
	for paramValue := range n.supportsParamValue[paramKey{unit, paramName}] {
		if paramValue != value {
			return true
		}
	}
	return false
}

func (n NecessaryFeatures) SupportsModulation(unit string, param string) bool {
	return n.supportsModulation[paramKey{unit, param}]
}

func (n NecessaryFeatures) SupportsPolyphony() bool {
	return n.polyphony
}

func (n NecessaryFeatures) Opcode(unitType string) (int, bool) {
	code, ok := n.opcodes[unitType]
	return code, ok
}

func (n NecessaryFeatures) Instructions() []string {
	return n.instructions
}

func (n NecessaryFeatures) InputNumber(unitType string, paramName string) int {
	return allInputs[paramKey{unitType, paramName}]
}

func (n NecessaryFeatures) TransformCount(unitType string) int {
	count := allTransformCounts[unitType]
	if o, ok := optionalParams[unitType]; ok && !n.SupportsParamValueOtherThan(unitType, o.Name, o.Value) && !n.SupportsModulation(unitType, o.Name) {
		count--
	}
	return count
}

// TransformsParam reports whether the players of the feature set transform,
// and so read, the parameter of the unit type. It is false for the optional
// parameters the feature set leaves out.
func TransformsParam(f FeatureSet, unitType string, paramName string) bool {
	i := 0
	for _, t := range sointu.UnitTypes[unitType].Params {
		if t.CanModulate && t.CanSet && !t.NoTransform {
			if t.Name == paramName {
				return i < f.TransformCount(unitType)
			}
			i++
		}
	}
	return false
}

func (n NecessaryFeatures) SupportsGlobalSend() bool {
	return n.globalSend
}
