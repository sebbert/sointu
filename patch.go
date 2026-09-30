package sointu

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

type (
	// Patch is a list of instruments used in a song
	Patch []Instrument

	// Instrument includes various properties of the instrument (name, comment,
	// number of polyphonic voices, etc.) and a list of units for the
	// instrument.
	Instrument struct {
		Name      string `yaml:",omitempty"`
		Comment   string `yaml:",omitempty"`
		NumVoices int    `yaml:",omitempty"`
		Mute      bool   `yaml:",omitempty"` // Mute is only used in the tracker for soloing/muting instruments; the compiled player ignores this field
		// ThreadMaskM1 is a bit mask of which threads are used, minus 1. Minus
		// 1 is done so that the default value 0 means bit mask 0b0001 i.e. only
		// thread 1 is rendering the instrument.
		ThreadMaskM1 int    `yaml:",omitempty"`
		MIDI         MIDI   `yaml:",flow,omitempty"` // MIDI contains info on how MIDI events should trigger this instrument.
		Units        []Unit // Units contains all the units of the instrument
	}

	// Unit is one small component of an instrument—e.g. a filter, an
	// oscillator, or an envelope—and its parameters
	Unit struct {
		// Type is the type of the unit, e.g. "add","oscillator" or "envelope".
		// Always in lowercase. "" type should be ignored, no invalid types should
		// be used.
		Type string `yaml:",omitempty"`

		// ID should be a unique ID for this unit, used by SEND units to target
		// specific units. ID = 0 means that no ID has been given to a unit and thus
		// cannot be targeted by SENDs. When possible, units that are not targeted
		// by any SENDs should be cleaned from having IDs, e.g. to keep the exported
		// data clean.
		ID int `yaml:",omitempty"`

		// Parameters is a map[string]int of parameters of a unit. For example, for
		// an oscillator, unit.Type == "oscillator" and unit.Parameters["attack"]
		// could be 64. Most parameters are either limites to 0 and 1 (e.g. stereo
		// parameters) or between 0 and 128, inclusive.
		Parameters ParamMap `yaml:",flow"`

		// VarArgs is a list containing the variable number arguments that some
		// units require, most notably the DELAY units. For example, for a DELAY
		// unit, VarArgs is the delaytimes, in samples, of the different delaylines
		// in the unit.
		VarArgs []int `yaml:",flow,omitempty"`

		// Disabled is a flag that can be set to true to disable the unit.
		// Disabled units are considered to be not present in the patch.
		Disabled bool `yaml:",omitempty"`

		// Comment is a free-form comment about the unit that can be displayed
		// instead of/besides the type of the unit in the GUI, to make it easier
		// to track what the unit is doing & to make it easier to target sends.
		Comment string `yaml:",omitempty"`
	}

	// MIDI contains info on how MIDI events should trigger an instrument
	MIDI struct {
		Channel       int  `yaml:",omitempty"` // 0 means automatically assigned channel, 1-16 means MIDI channel 1-16, 17-64 means channels 1-16 on MIDI inputs 2-4 (CLAP plugin only)
		Start         int  `yaml:",omitempty"` // MIDI note number to start on, 0-127
		End           int  `yaml:",omitempty"` // MIDI note number to end on, counted backwards from 127, done so that the default number of 0 corresponds to "full keyboard", without any splittings
		Transpose     int  `yaml:",omitempty"` // value to be added to the MIDI note/velocity number, can be negative
		Velocity      bool `yaml:",omitempty"` // if true, then this instrument triggered by midi event velocity instead of its note number
		NoRetrigger   bool `yaml:",omitempty"` // if true, then this instrument does not retrigger if two consecutive events have the same value
		IgnoreNoteOff bool `yaml:",omitempty"` // if true, then this instrument should ignore note off events, i.e. notes never release
	}

	ParamMap map[string]int

	// UnitType documents the parameters and stack use of a unit type
	UnitType struct {
		Params         []UnitParameter
		DefaultVarArgs []int
		StackUse       func(*Unit) StackUse
	}

	// StackUse documents how a unit will affect the signal stack.
	StackUse struct {
		Inputs     [][]int // Inputs documents which inputs contribute to which outputs. len(Inputs) is the number of inputs. Each input can contribute to multiple outputs, so its a slice.
		Modifies   []bool  // Modifies documents which of the outputs are actually modified versions of the inputs
		NumOutputs int     // NumOutputs is the number of outputs produced by the unit. This is used to determine how many outputs are needed for the unit.
	}

	// UnitParameter documents one parameter that an unit takes
	UnitParameter struct {
		Name        string // thould be found with this name in the Unit.Parameters map
		MinValue    int    // minimum value of the parameter, inclusive
		MaxValue    int    // maximum value of the parameter, inclusive
		Neutral     int    // neutral value of the parameter
		Default     int    // the default value of the parameter
		CanSet      bool   // if true, then this parameter can be set through the gui
		CanModulate bool   // if true, then this parameter can be modulated i.e. has a port number in "send" unit
		// NoTransform is true for parameters that can be set and modulated,
		// but whose value is not passed to the unit as a transformed
		// parameter, e.g. because it does not fit in a byte. The unit reads
		// the modulation port itself. Such parameters must come after the
		// transformed ones.
		NoTransform bool
		DisplayFunc UnitParameterDisplayFunc
	}

	UnitParameterDisplayFunc func(int) (value string, unit string)
)

// UnitTypes documents all the available unit types and if they support stereo
// variant and what parameters they take. If you add a new unit type, add it
// here and also add its opcode to vm/opcodes.go by running "go generate ./vm"
// in the terminal.
var UnitTypes = map[string]UnitType{
	"add": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(unit *Unit) StackUse {
			if stereo, ok := unit.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0, 2}, {1, 3}, {2}, {3}}, Modifies: []bool{false, false, true, true}, NumOutputs: 4}
			}
			return StackUse{Inputs: [][]int{{0, 1}, {1}}, Modifies: []bool{false, true}, NumOutputs: 2}
		},
	},
	"addp": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0}, {1}, {0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 2}
			}
			return StackUse{Inputs: [][]int{{0}, {0}}, Modifies: []bool{true}, NumOutputs: 1}
		},
	},
	"mul": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(unit *Unit) StackUse {
			if stereo, ok := unit.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0, 2}, {1, 3}, {2}, {3}}, Modifies: []bool{false, false, true, true}, NumOutputs: 4}
			}
			return StackUse{Inputs: [][]int{{0, 1}, {1}}, Modifies: []bool{false, true}, NumOutputs: 2}
		},
	},
	"mulp": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0}, {1}, {0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 2}
			}
			return StackUse{Inputs: [][]int{{0}, {0}}, Modifies: []bool{true}, NumOutputs: 1}
		},
	},
	"xch": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{2}, {3}, {0}, {1}}, Modifies: []bool{false, false, false, false}, NumOutputs: 4}
			}
			return StackUse{Inputs: [][]int{{1}, {0}}, Modifies: []bool{false, false}, NumOutputs: 2}
		},
	},
	"push": {
		Params: []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0, 2}, {1, 3}}, Modifies: []bool{false, false, false, false}, NumOutputs: 4}
			}
			return StackUse{Inputs: [][]int{{0, 1}}, Modifies: []bool{false, false}, NumOutputs: 2}
		},
	},
	"pop": {
		Params:   []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: stackUseSink,
	},
	"loadnote": {
		Params:   []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: stackUseSource,
	},
	"distort": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "drive", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
		},
		StackUse: stackUseEffect,
	},
	"hold": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "holdfreq", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
		},
		StackUse: stackUseEffect,
	},
	"crush": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "resolution", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(24 * float64(v) / 128), "bits" }},
		},
		StackUse: stackUseEffect,
	},
	"gain": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseEffect,
	},
	"invgain": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "invgain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(128/float64(v)), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseEffect,
	},
	"dbgain": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "decibels", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(40 * (float64(v)/64 - 1)), "dB" }},
		},
		StackUse: stackUseEffect,
	},
	"filter": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "frequency", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				// https://www.musicdsp.org/en/latest/Filters/23-state-variable.html
				// calls it cutoff" but it's actually the location of the
				// resonance peak
				freq := float64(v) / 128
				return strconv.FormatFloat(math.Asin(freq*freq/2)/math.Pi*44100, 'f', 0, 64), "Hz"
			},
			},
			{Name: "resonance", MinValue: 0, Default: 64, Neutral: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(toDecibel(128/float64(v)), 'g', 3, 64), "Q dB"
			}},
			{Name: "lowpass", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "bandpass", MinValue: -1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "highpass", MinValue: -1, MaxValue: 1, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseEffect,
	},
	"clip": {
		Params:   []UnitParameter{{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false}},
		StackUse: stackUseEffect,
	},
	"pan": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "panning", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
		},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 2}
			}
			return StackUse{Inputs: [][]int{{0, 1}}, Modifies: []bool{true, true}, NumOutputs: 2}
		},
	},
	"delay": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "pregain", MinValue: 0, Default: 40, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "dry", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "feedback", MinValue: 0, Default: 96, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "damp", MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "notetracking", MinValue: 0, Default: 2, MaxValue: 2, CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc([]string{"fixed", "pitch", "BPM"})},
			{Name: "delaytime", MinValue: 0, MaxValue: -1, CanSet: false, CanModulate: true},
		},
		DefaultVarArgs: []int{48},
		StackUse:       stackUseEffect,
	},
	"compressor": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "attack", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: compressorTimeDispFunc},
			{Name: "release", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: compressorTimeDispFunc},
			{Name: "invgain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(toDecibel(128/float64(v)), 'g', 3, 64), "dB"
			}},
			{Name: "threshold", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB"
			}},
			{Name: "ratio", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(1 - float64(v)/128), "" }},
		},
		StackUse: func(u *Unit) StackUse {
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				return StackUse{Inputs: [][]int{{0, 2, 3}, {1, 2, 3}}, Modifies: []bool{false, false, true, true}, NumOutputs: 4}
			}
			return StackUse{Inputs: [][]int{{0, 1}}, Modifies: []bool{false, true}, NumOutputs: 2}
		},
	},
	"ott": {
		// ott is a three-band upward and downward compressor, like Xfer's OTT
		// or the OTT preset of Ableton's Multiband Dynamics. It splits the
		// signal at 88.3 Hz and 2.5 kHz (2-pole state-variable low-passes, Q
		// 0.707, the rest is the difference, so the bands sum back to the
		// input) and follows the power of each band (stereo: the sum of the
		// channels' powers, one gain for both). Above the upper threshold, a
		// band is compressed downward at 66.7:1, scaled by downward; below the
		// lower threshold, it is lifted upward at 4:1, scaled by upward, by at
		// most 24 dB. Then the band's gain applies, and depth mixes the bands
		// back with the dry input. The thresholds, in dB of the mean square
		// (a full-scale sine is -3 dB), attack and release at time 64:
		//
		//	band  upper     lower     attack   release
		//	low   -33.8 dB  -40.8 dB  47.8 ms  282 ms
		//	mid   -30.2 dB  -41.8 dB  22.4 ms  282 ms
		//	high  -35.5 dB  -40.8 dB  13.5 ms  132 ms
		//
		// time scales the attacks and releases by 1/16 to 16. The OTT preset
		// also boosts the bands by 5.2 dB going in and by about 10.3, 5.7 and
		// 10.3 dB coming out; ott has no fixed gains, so that with upward and
		// downward at 0 it passes the input through: set low, mid and high
		// to taste (91, 79 and 91 for the preset's output gains).
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "depth", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "time", MinValue: 0, Default: 64, Neutral: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(math.Pow(2, (float64(v)/128-0.5)*8), 'g', 3, 64), "×"
			}},
			{Name: "upward", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "downward", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "low", MinValue: 0, Default: 64, Neutral: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: ottGainDisplay},
			{Name: "mid", MinValue: 0, Default: 64, Neutral: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: ottGainDisplay},
			{Name: "high", MinValue: 0, Default: 64, Neutral: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: ottGainDisplay},
		},
		StackUse: stackUseEffect,
	},
	"speed": {
		Params:   []UnitParameter{},
		StackUse: func(u *Unit) StackUse { return StackUse{Inputs: [][]int{{0}}, Modifies: []bool{true}, NumOutputs: 0} },
	},
	"out": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseSink,
	},
	"outaux": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "outgain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "auxgain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseSink,
	},
	"aux": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "channel", MinValue: 0, Default: 2, MaxValue: 6, CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc(channelNames[:])},
		},
		StackUse: stackUseSink,
	},
	"send": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "amount", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v)/64 - 1), "" }},
			{Name: "voice", MinValue: 0, MaxValue: 255, CanSet: true, CanModulate: false, DisplayFunc: func(v int) (string, string) {
				if v == 0 {
					return "default", ""
				}
				return strconv.Itoa(v), ""
			}},
			{Name: "target", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "port", MinValue: 0, MaxValue: 7, CanSet: true, CanModulate: false},
			{Name: "sendpop", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse {
			ret := StackUse{Inputs: [][]int{{0}}, Modifies: []bool{true}, NumOutputs: 1}
			if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
				ret = StackUse{Inputs: [][]int{{0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 2}
			}
			if sendpop, ok := u.Parameters["sendpop"]; ok && sendpop == 1 {
				ret.NumOutputs = 0
			}
			return ret
		},
	},
	"envelope": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "attack", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return engineeringTime(math.Pow(2, 24*float64(v)/128) / 44100) }},
			{Name: "decay", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return engineeringTime(math.Pow(2, 24*float64(v)/128) / 44100) }},
			{Name: "sustain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "release", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return engineeringTime(math.Pow(2, 24*float64(v)/128) / 44100) }},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseSource,
	},
	"noise": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "shape", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
		},
		StackUse: stackUseSource,
	},
	"oscillator": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "transpose", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				relvalue := v - 64
				if relvalue%12 == 0 {
					return strconv.Itoa(relvalue / 12), "oct"
				}
				return strconv.Itoa(relvalue), "st"
			}},
			{Name: "detune", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v-64) / 64), "st" }},
			{Name: "phase", MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(float64(v)/128*360, 'f', 1, 64), "°"
			}},
			{Name: "color", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "shape", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "gain", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "frequency", MinValue: 0, MaxValue: -1, CanSet: false, CanModulate: true},
			{Name: "type", MinValue: int(Sine), Default: int(Sine), MaxValue: int(Sample), CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc([]string{"sine", "trisaw", "pulse", "gate", "sample"})},
			{Name: "lfo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "unison", MinValue: 0, MaxValue: 3, CanSet: true, CanModulate: false},
			// bandlimit reduces the aliasing of the sine, trisaw and pulse
			// waveforms with polyBLEP and polyBLAMP, keeping the trisaw's
			// color a sample from 0 and 1 and the sine's in [dt, 1]. The
			// waveshaper still aliases. LFOs ignore it; see
			// OscillatorBandlimited.
			{Name: "bandlimit", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "samplestart", MinValue: 0, MaxValue: 1720329, CanSet: true, CanModulate: false},
			{Name: "loopstart", MinValue: 0, MaxValue: 65535, CanSet: true, CanModulate: false},
			{Name: "looplength", MinValue: 0, MaxValue: 65535, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseSource,
	},
	"bufread": {
		// bufread plays a buffer. With note tracking, note 60 plays it at its
		// original speed; transpose and detune shift the pitch like in the
		// oscillator, and speed multiplies the rate from -1 (backwards) to 1.
		// start, loopstart and looplength are in frames from the oldest valid
		// frame of the buffer; a negative start or loopstart counts back from
		// the newest one when the note was triggered. Modulating them shifts them by the valid length of the buffer
		// times the modulation. start is read when the note is triggered.
		// fade is the length of the crossfade at the end of the loop, and
		// edgefade the length of the fade out near the edges of the valid
		// frames, e.g. the write head of a buffer being written.
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "transpose", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				relvalue := v - 64
				if relvalue%12 == 0 {
					return strconv.Itoa(relvalue / 12), "oct"
				}
				return strconv.Itoa(relvalue), "st"
			}},
			{Name: "detune", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v-64) / 64), "st" }},
			{Name: "gain", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "speed", MinValue: 0, Neutral: 64, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v)/64 - 1), "x" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "notetracking", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "loop", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "start", MinValue: math.MinInt32 + 1, MaxValue: math.MaxInt32, CanSet: true, CanModulate: true, NoTransform: true},
			{Name: "loopstart", MinValue: math.MinInt32 + 1, MaxValue: math.MaxInt32, CanSet: true, CanModulate: true, NoTransform: true},
			{Name: "looplength", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: true, NoTransform: true},
			{Name: "fade", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "edgefade", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseSource,
	},
	"bufwrite": {
		// bufwrite pops a signal and writes it to a buffer without a sample.
		// By default, it writes every frame, whether a note is held or not,
		// wrapping around the end of the buffer and overwriting the oldest
		// frames: a ring buffer. With oneshot, it writes only while its note
		// is held, from the beginning of the buffer when the note starts,
		// until the end of the buffer. bufwrite units writing the same buffer
		// in the same frame, e.g. in the voices of an instrument, mix. The
		// written frame is the old frame times feedback plus the signal. With
		// pop 0, the signal stays on the stack.
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "feedback", MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "oneshot", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "pop", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse {
			ret := stackUseSink(u)
			if u.Parameters["pop"] == 0 { // writes the signal and leaves it on the stack
				ret.NumOutputs = len(ret.Inputs)
			}
			return ret
		},
	},
	"spfft": {
		// spfft pops a signal and analyses it into a spectrum buffer: every
		// size/4 samples, it takes the last size samples, windows them (Hann)
		// and replaces the spectrum with their FFT. A stereo spfft makes a
		// stereo spectrum, which the other units process channel by channel. Other spectral units
		// modify the spectrum, and spifft turns it back into a signal.
		// Spectral units run only in the first voice of their instrument.
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "size", MinValue: 0, Default: SpectrumSizeDefault, MaxValue: SpectrumSizeMax, CanSet: true, CanModulate: false, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(SpectrumSize(v)), "" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseSink,
	},
	"spifft": {
		// spifft pushes the signal of a spectrum buffer: each new spectrum
		// is transformed back (inverse FFT), windowed and overlap-added. The
		// signal comes out about size samples after it went into spfft. A
		// mono spifft averages the channels of a stereo spectrum; a stereo
		// one pushes a mono spectrum to both channels.
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "gain", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(toDecibel(float64(v)/128), 'g', 3, 64), "dB" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseSource,
	},
	"spfilter": {
		// spfilter removes the bins of a spectrum below low and above high,
		// and tilts the rest by up to 12 dB per octave around 1 kHz.
		Params: []UnitParameter{
			{Name: "low", MinValue: 0, Default: 0, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: spectralFrequencyDisplay},
			{Name: "high", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: spectralFrequencyDisplay},
			{Name: "tilt", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat((float64(v)/32-2)*6.0206, 'f', 1, 64), "dB/oct"
			}},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spcompress": {
		// spcompress pulls the magnitudes of a spectrum toward their mean:
		// each bin is scaled by (mean/envelope)^amount, where the envelope is
		// the average magnitude of the bins within width. Amount 1 flattens
		// the envelope, like heavy upward and downward multiband compression;
		// negative amounts exaggerate it. attack and release smooth the
		// envelope of each bin over time, like the level detector of a
		// compressor; 0 follows it instantly.
		Params: []UnitParameter{
			{Name: "amount", MinValue: 0, Neutral: 64, Default: 96, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v)/64 - 1), "" }},
			{Name: "width", MinValue: 0, Default: 16, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(float64(v)*100/128/16, 'g', 3, 64), "%" }},
			{Name: "attack", MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: false, DisplayFunc: spcompressTimeDisplay},
			{Name: "release", MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: false, DisplayFunc: spcompressTimeDisplay},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spblur": {
		// spblur smooths the magnitudes of a spectrum over time, keeping the
		// phases: amount is how much of the previous magnitude stays each
		// frame. While freeze is on (above half), it holds the magnitudes
		// instead, with random phases each frame, for an endless texture.
		Params: []UnitParameter{
			{Name: "amount", MinValue: 0, Default: 96, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "freeze", MinValue: 0, Default: 0, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				if v > 64 {
					return "on", ""
				}
				return "off", ""
			}},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spgate": {
		// spgate removes the bins of a spectrum quieter than threshold,
		// relative to a full scale sine, or with invert, the louder ones.
		Params: []UnitParameter{
			{Name: "threshold", MinValue: 0, Default: 32, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v*96/128 - 96), "dB" }},
			{Name: "invert", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spphase": {
		// spphase changes the phases of a spectrum: disperse rotates the bins
		// by angles growing with the square of their frequency, smearing
		// transients into chirps; random rotates them randomly; robot blends
		// toward phase 0, pitching the sound to the frame rate. amount scales
		// the effect.
		Params: []UnitParameter{
			{Name: "mode", MinValue: 0, MaxValue: 2, CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc(spphaseModeNames[:])},
			{Name: "amount", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spscale": {
		// spscale moves each bin of a spectrum to its frequency times scale,
		// up to an octave up or down, plus shift, up to 1 kHz up or down.
		// Scaling keeps harmonics harmonic; shifting makes them inharmonic.
		Params: []UnitParameter{
			{Name: "scale", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(math.Pow(2, float64(v)/64-1), 'f', 3, 64), "x"
			}},
			{Name: "shift", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa((v - 64) * 1000 / 64), "Hz" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spformant": {
		// spformant moves the envelope of a spectrum, its formants, up to an
		// octave up or down, keeping its fine structure, i.e. the pitch. The
		// envelope is the average magnitude of the bins within width.
		Params: []UnitParameter{
			{Name: "shift", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(float64(v-64)*12/64, 'f', 1, 64), "st" }},
			{Name: "width", MinValue: 0, Default: 16, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(float64(v)*100/128/16, 'g', 3, 64), "%" }},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spcross": {
		// spcross puts the envelope of the source spectrum onto the spectrum:
		// each bin is scaled by (source envelope/envelope)^amount, where an
		// envelope is the average magnitude of the bins within width. Width 0
		// takes the magnitudes of the source and the phases of the spectrum
		// (cross-synthesis); wider, it is a vocoder, the source being the
		// modulator. The source must have the same size.
		Params: []UnitParameter{
			{Name: "amount", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "width", MinValue: 0, Default: 16, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(float64(v)*100/128/32, 'g', 3, 64), "%" }},
			{Name: "source", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spcomb": {
		// spcomb keeps the bins near the harmonics of up to 8 notes, like
		// resonators tuned to a chord: the notes held in the voices of
		// instrument, or if none, the note of its own voice and the notes
		// interval1-3 semitones above it (0 is none). q is how narrow the
		// peaks are; amount blends from the spectrum (0) to only the peaks.
		Params: []UnitParameter{
			{Name: "q", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true},
			{Name: "amount", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
			{Name: "instrument", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "interval1", MinValue: 0, MaxValue: 48, CanSet: true, CanModulate: false, DisplayFunc: intervalDisplay},
			{Name: "interval2", MinValue: 0, MaxValue: 48, CanSet: true, CanModulate: false, DisplayFunc: intervalDisplay},
			{Name: "interval3", MinValue: 0, MaxValue: 48, CanSet: true, CanModulate: false, DisplayFunc: intervalDisplay},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spcopy": {
		// spcopy copies each new spectrum of the source spectrum buffer to
		// its own spectrum buffer, e.g. to process the same spectrum in two
		// different ways.
		Params: []UnitParameter{
			{Name: "source", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
			{Name: "buffer", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse { return StackUse{} },
	},
	"spawn": {
		// spawn triggers notes on the voices of another instrument, taking
		// the released voice that was spawned longest ago. If all the voices
		// are held, it takes the one spawned longest ago with steal on, and
		// skips the spawn otherwise. In rate mode, it spawns at
		// the given rate while its own voice is held; in sync mode likewise,
		// but with the rate in spawns per beat; in edge mode, when its input
		// rises above zero while its own voice is held. The note is
		// transposed from the note of its own voice, or from C-4 without note
		// tracking. The spawned note is released after length, unless it is
		// 0, which holds it until the voice is taken. It pops args values from the stack (below the input in
		// edge mode) and passes them to the spawned voice, where arg units
		// push them. instrument is the index of the instrument plus one; 0
		// means none.
		Params: []UnitParameter{
			{Name: "mode", MinValue: 0, MaxValue: 2, CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc(spawnModeNames[:])},
			{Name: "rate", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(SpawnRateHz(float64(v)/128), 'g', 3, 64), "Hz"
			}},
			{Name: "transpose", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				relvalue := v - 64
				if relvalue%12 == 0 {
					return strconv.Itoa(relvalue / 12), "oct"
				}
				return strconv.Itoa(relvalue), "st"
			}},
			{Name: "length", MinValue: 0, Default: 0, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: spawnLengthDisplay},
			{Name: "notetracking", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "args", MinValue: 0, MaxValue: MaxSpawnArgs, CanSet: true, CanModulate: false},
			{Name: "steal", MinValue: 0, Default: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "instrument", MinValue: 0, MaxValue: math.MaxInt32, CanSet: true, CanModulate: false},
		},
		StackUse: func(u *Unit) StackUse {
			n := u.Parameters["args"]
			if u.Parameters["mode"] == SpawnModeEdge {
				n++ // the input
			}
			ret := StackUse{Inputs: make([][]int, n), Modifies: make([]bool, n)}
			for i := range n {
				ret.Inputs[i] = []int{0}
				ret.Modifies[i] = true
			}
			return ret
		},
	},
	"arg": {
		// arg pushes a value passed by the spawn unit that triggered the
		// voice, or 0.
		Params: []UnitParameter{
			{Name: "index", MinValue: 0, MaxValue: MaxSpawnArgs - 1, CanSet: true, CanModulate: false},
		},
		StackUse: stackUseSource,
	},
	"window": {
		// window pushes a window over the note of its voice: it rises from 0
		// to 1 and falls back to 0 over length, and stays 0 after it. Length 0
		// takes the length of the note from the spawn unit that spawned it;
		// notes without a length get no window (1). shape is how much of the
		// window is spent rising and falling, with smoothstep curves; 0 is a
		// rectangle. Never triggered voices push 0.
		Params: []UnitParameter{
			{Name: "length", MinValue: 0, Default: 0, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: windowLengthDisplay},
			{Name: "shape", MinValue: 0, Default: 128, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.Itoa(v * 100 / 128), "%" }},
		},
		StackUse: stackUseSource,
	},
	"loadval": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "value", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return formatFloat(float64(v)/64 - 1), "" }},
		},
		StackUse: stackUseSource,
	},
	"receive": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "left", MinValue: 0, MaxValue: -1, CanSet: false, CanModulate: true},
			{Name: "right", MinValue: 0, MaxValue: -1, CanSet: false, CanModulate: true},
		},
		StackUse: stackUseSource,
	},
	"in": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, Default: 1, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "channel", MinValue: 0, Default: 2, MaxValue: 6, CanSet: true, CanModulate: false, DisplayFunc: arrDispFunc(channelNames[:])},
		},
		StackUse: stackUseSource,
	},
	"sync": {
		Params:   []UnitParameter{},
		StackUse: func(u *Unit) StackUse { return StackUse{Inputs: [][]int{{0}}, Modifies: []bool{false}, NumOutputs: 1} },
	},
	"belleq": {
		Params: []UnitParameter{
			{Name: "stereo", MinValue: 0, MaxValue: 1, CanSet: true, CanModulate: false},
			{Name: "frequency", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				freq := float64(v) / 128
				return strconv.FormatFloat(44100*2*freq*freq/math.Pi/2, 'f', 0, 64), "Hz"
			}},
			{Name: "bandwidth", MinValue: 0, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) { return strconv.FormatFloat(1/(4*float64(v)/128), 'f', 2, 64), "Q" }},
			{Name: "gain", MinValue: 0, Neutral: 64, Default: 64, MaxValue: 128, CanSet: true, CanModulate: true, DisplayFunc: func(v int) (string, string) {
				return strconv.FormatFloat(40*(float64(v)/64-1), 'f', 2, 64), "dB"
			}},
		},
		StackUse: stackUseEffect,
	},
}

// The size of the spectra of spfft units is 256 * 2^size samples; the size
// of a spectrum buffer comes from the spfft unit writing it, or the spcopy
// unit copying another spectrum to it.
const (
	SpectrumSizeDefault = 2 // 1024
	SpectrumSizeMax     = 5 // 8192
)

// SpcompressTime returns the attack or release time of spcompress, in
// milliseconds, for the parameter value v: 2^(12v/128) - 1, from 0 to about 4
// seconds.
func SpcompressTime(v int) float64 { return math.Exp2(float64(v)*12/128) - 1 }

func spcompressTimeDisplay(v int) (string, string) {
	return strconv.FormatFloat(SpcompressTime(v), 'g', 3, 64), "ms"
}

// The modes of the spphase unit.
const (
	SpphaseDisperse = iota
	SpphaseRandom
	SpphaseRobot
)

var spphaseModeNames = [...]string{"disperse", "random", "robot"}

func intervalDisplay(v int) (string, string) {
	if v == 0 {
		return "none", ""
	}
	return strconv.Itoa(v), "st"
}

// TargetsInstrument reports whether units of the type refer to an instrument
// with their instrument parameter: its index plus one, 0 for none.
func TargetsInstrument(unitType string) bool { return unitType == "spawn" || unitType == "spcomb" }

// SpectralFrequency returns the frequency in Hz of the cutoffs of spfilter:
// 0 is 0 Hz and 1 is 22050 Hz, exponentially over 10 octaves in between.
func SpectralFrequency(v float64) float64 { return 22050 * (math.Pow(2, 10*v) - 1) / 1023 }

func spectralFrequencyDisplay(v int) (string, string) {
	f := SpectralFrequency(float64(v) / 128)
	if f >= 1000 {
		return strconv.FormatFloat(f/1000, 'f', 2, 64), "kHz"
	}
	return strconv.FormatFloat(f, 'f', 0, 64), "Hz"
}

// SpectrumSize returns the size in samples of the spectra of an spfft unit
// with the given size parameter.
func SpectrumSize(size int) int { return 256 << min(max(size, 0), SpectrumSizeMax) }

func stackUseSource(u *Unit) StackUse {
	if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
		return StackUse{Inputs: [][]int{}, Modifies: []bool{true, true}, NumOutputs: 2}
	}
	return StackUse{Inputs: [][]int{}, Modifies: []bool{true}, NumOutputs: 1}
}

func stackUseSink(u *Unit) StackUse {
	if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
		return StackUse{Inputs: [][]int{{0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 0}
	}
	return StackUse{Inputs: [][]int{{0}}, Modifies: []bool{true}, NumOutputs: 0}
}

func stackUseEffect(u *Unit) StackUse {
	if stereo, ok := u.Parameters["stereo"]; ok && stereo == 1 {
		return StackUse{Inputs: [][]int{{0}, {1}}, Modifies: []bool{true, true}, NumOutputs: 2}
	}
	return StackUse{Inputs: [][]int{{0}}, Modifies: []bool{true}, NumOutputs: 1}
}

// addedParameters are parameters added to unit types after songs were saved
// without them, with the values that keep those songs sounding the same. A
// missing parameter is otherwise 0.
var addedParameters = map[string]map[string]int{
	"bufread":    {"speed": 128}, // forwards at the normal speed
	"bufwrite":   {"pop": 1},
	"oscillator": {"bandlimit": 0},            // naive waveforms
	"spcompress": {"attack": 0, "release": 0}, // no smoothing
}

// compile errors if interface is not implemented.
var _ yaml.Unmarshaler = &Unit{}

// UnmarshalYAML fills in parameters added to the unit type after the unit was
// saved; see addedParameters.
func (u *Unit) UnmarshalYAML(value *yaml.Node) error {
	type plain Unit // without the UnmarshalYAML method
	if err := value.Decode((*plain)(u)); err != nil {
		return err
	}
	u.fillAddedParameters()
	return nil
}

// UnmarshalJSON is like UnmarshalYAML.
func (u *Unit) UnmarshalJSON(data []byte) error {
	type plain Unit
	if err := json.Unmarshal(data, (*plain)(u)); err != nil {
		return err
	}
	u.fillAddedParameters()
	return nil
}

func (u *Unit) fillAddedParameters() {
	for name, value := range addedParameters[u.Type] {
		if _, ok := u.Parameters[name]; !ok {
			if u.Parameters == nil {
				u.Parameters = ParamMap{}
			}
			u.Parameters[name] = value
		}
	}
}

// compile errors if interface is not implemented.
var _ yaml.Unmarshaler = &ParamMap{}

func (a *ParamMap) UnmarshalYAML(value *yaml.Node) error {
	var m map[string]int
	if err := value.Decode(&m); err != nil {
		return err
	}
	// Backwards compatibility hack: if the patch was saved with an older
	// version of Sointu, it might have used the negbandpass and neghighpass
	// parameters, which now correspond to having bandpass as value -1 and
	// highpass as value -1.
	if n, ok := m["negbandpass"]; ok {
		m["bandpass"] = m["bandpass"] - n
		delete(m, "negbandpass")
	}
	if n, ok := m["neghighpass"]; ok {
		m["highpass"] = m["highpass"] - n
		delete(m, "neghighpass")
	}
	*a = m
	return nil
}

var channelNames = [...]string{"left", "right", "aux1 left", "aux1 right", "aux2 left", "aux2 right", "aux3 left", "aux3 right"}

// MaxSpawnArgs is the maximum number of values a spawn unit passes to the
// voices it spawns.
const MaxSpawnArgs = 4

// Modes of the spawn unit.
const (
	SpawnModeRate = iota
	SpawnModeEdge
	SpawnModeSync
)

var spawnModeNames = [...]string{"rate", "edge", "sync"}

// SpawnRateHz returns the rate of a spawn unit in Hz for its rate parameter
// (with modulation) scaled to 0-1: 8 Hz at the middle, doubling every 8 steps.
func SpawnRateHz(rate float64) float64 { return math.Pow(2, rate*16-5) }

// SpawnsPerBeat returns the rate of a spawn unit in sync mode, in spawns per
// beat: 1 at the middle, doubling every 8 steps.
func SpawnsPerBeat(rate float64) float64 { return math.Pow(2, rate*16-8) }

// LengthFrames returns the length in frames of a spawn or window unit for its
// length parameter (with modulation) scaled to 0-1: 100 ms at the middle,
// doubling every 8 steps.
func LengthFrames(length float64) float64 { return math.Floor(4410 * math.Pow(2, length*16-8)) }

func spawnLengthDisplay(v int) (string, string) {
	if v == 0 {
		return "hold", ""
	}
	return lengthDisplay(v)
}

func windowLengthDisplay(v int) (string, string) {
	if v == 0 {
		return "note", ""
	}
	return lengthDisplay(v)
}

func lengthDisplay(v int) (string, string) {
	ms := max(LengthFrames(float64(v)/128), 1) / 44.1
	if ms >= 1000 {
		return strconv.FormatFloat(ms/1000, 'g', 3, 64), "s"
	}
	return strconv.FormatFloat(ms, 'g', 3, 64), "ms"
}

func arrDispFunc(arr []string) UnitParameterDisplayFunc {
	return func(v int) (string, string) {
		if v < 0 || v >= len(arr) {
			return "???", ""
		}
		return arr[v], ""
	}
}

func compressorTimeDispFunc(v int) (string, string) {
	alpha := math.Pow(2, -24*float64(v)/128) // alpha is the "smoothing factor" of first order low pass iir
	sec := -1 / (44100 * math.Log(1-alpha))  // from smoothing factor to time constant, https://en.wikipedia.org/wiki/Exponential_smoothing
	return engineeringTime(sec)
}

// ottGainDisplay shows the gain of a band of ott, 2^((v/128-0.5)·8): ±24 dB.
func ottGainDisplay(v int) (string, string) {
	return strconv.FormatFloat(toDecibel(math.Pow(2, (float64(v)/128-0.5)*8)), 'f', 1, 64), "dB"
}

func engineeringTime(sec float64) (string, string) {
	if sec < 1e-3 {
		return fmt.Sprintf("%.2f", sec*1e6), "us"
	} else if sec < 1 {
		return fmt.Sprintf("%.2f", sec*1e3), "ms"
	}
	return fmt.Sprintf("%.2f", sec), "s"
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func toDecibel(amplitude float64) float64 {
	if amplitude <= 0 {
		return math.Inf(-1)
	}
	// Decibels are defined as 20 * log10(amplitude)
	// https://en.wikipedia.org/wiki/Decibel#Sound_pressure
	return 20 * math.Log10(amplitude)
}

// When unit.Type = "oscillator", its unit.Parameter["Type"] tells the type of
// the oscillator. There is five different oscillator types, so these consts
// just enumerate them.
const (
	Sine   = iota
	Trisaw = iota
	Pulse  = iota
	Gate   = iota
	Sample = iota
)

// OscillatorBandlimited reports whether an oscillator unit uses its bandlimit
// parameter: only sine, trisaw and pulse oscillators that are not LFOs do.
func OscillatorBandlimited(u Unit) bool {
	if u.Type != "oscillator" || u.Parameters["bandlimit"] != 1 || u.Parameters["lfo"] == 1 {
		return false
	}
	switch u.Parameters["type"] {
	case Sine, Trisaw, Pulse:
		return true
	}
	return false
}

// UnitNames is a list of all the names of units, sorted
// alphabetically.
var UnitNames []string

func init() {
	UnitNames = make([]string, 0, len(UnitTypes))
	for k := range UnitTypes {
		UnitNames = append(UnitNames, k)
	}
	sort.Strings(UnitNames)
}

// Ports is static map allowing quickly finding the parameters of a unit that
// can be modulated. This is populated based on the UnitTypes list during
// init(). Thus, should be immutable, but Go not supporting that, then this will
// have to suffice: DO NOT EVER CHANGE THIS MAP.
var Ports = make(map[string]([]string))

func init() {
	for name, unitType := range UnitTypes {
		unitPorts := make([]string, 0)
		for _, param := range unitType.Params {
			if param.CanModulate {
				unitPorts = append(unitPorts, param.Name)
			}
		}
		Ports[name] = unitPorts
	}
}

func MakeUnit(unitType string) Unit {
	if ut, ok := UnitTypes[unitType]; ok {
		ret := Unit{
			Type:       unitType,
			Parameters: make(map[string]int),
			VarArgs:    make([]int, len(ut.DefaultVarArgs)),
		}
		copy(ret.VarArgs, ut.DefaultVarArgs)
		for _, p := range ut.Params {
			if p.Default > 0 {
				ret.Parameters[p.Name] = p.Default
			}
		}
		return ret
	}
	return Unit{Parameters: make(map[string]int)}
}

// Copy makes a deep copy of a unit.
func (u *Unit) Copy() Unit {
	ret := *u
	ret.Parameters = make(map[string]int, len(u.Parameters))
	for k, v := range u.Parameters {
		ret.Parameters[k] = v
	}
	ret.VarArgs = make([]int, len(u.VarArgs))
	copy(ret.VarArgs, u.VarArgs)
	return ret
}

func (u *Unit) StackUse() StackUse {
	if u.Disabled {
		return StackUse{}
	}
	if ut, ok := UnitTypes[u.Type]; ok {
		return ut.StackUse(u)
	}
	return StackUse{}
}

// StackChange returns how this unit will affect the signal stack. "pop" and
// "addp" and such will consume the topmost signal, and thus return -1 (or -2,
// if the unit is a stereo unit). On the other hand, "oscillator" and "envelope"
// will produce a signal, and thus return 1 (or 2, if the unit is a stereo
// unit). Effects that just change the topmost signal and will not change the
// number of signals on the stack and thus return 0.
func (u *Unit) StackChange() int {
	s := u.StackUse()
	return s.NumOutputs - len(s.Inputs)
}

// StackNeed returns the number of signals that should be on the stack before
// this unit is executed. Used to prevent stack underflow. Units producing
// signals do not care what is on the stack before and will return 0.
func (u *Unit) StackNeed() int {
	return len(u.StackUse().Inputs)
}

// Copy makes a deep copy of an Instrument
func (instr *Instrument) Copy() Instrument {
	ret := *instr
	ret.Units = make([]Unit, len(instr.Units))
	for i, u := range instr.Units {
		ret.Units[i] = u.Copy()
	}
	return ret
}

// Implement the counter interface
func (i *Instrument) GetNumVoices() int {
	return i.NumVoices
}

func (i *Instrument) SetNumVoices(count int) {
	i.NumVoices = count
}

// Copy makes a deep copy of a Patch.
func (p Patch) Copy() Patch {
	instruments := make([]Instrument, len(p))
	for i, instr := range p {
		instruments[i] = instr.Copy()
	}
	return instruments
}

// NumVoices returns the total number of voices used in the patch; summing the
// voices of every instrument
func (p Patch) NumVoices() int {
	ret := 0
	for _, i := range p {
		ret += i.NumVoices
	}
	return ret
}

// NumDelayLines return the total number of delay lines used in the patch;
// summing the number of delay lines of every delay unit in every instrument
func (p Patch) NumDelayLines() int {
	total := 0
	for _, instr := range p {
		for _, unit := range instr.Units {
			if unit.Type == "delay" {
				total += len(unit.VarArgs) * instr.NumVoices
			}
		}
	}
	return total
}

// NumOtts returns the number of ott states of the patch: the number of ott
// units of every instrument times its number of voices. The synths keep them
// outside the voices, like delay lines, as a unit has room for 8 floats and a
// stereo ott needs 11.
func (p Patch) NumOtts() int {
	total := 0
	for _, instr := range p {
		for _, unit := range instr.Units {
			if unit.Type == "ott" && !unit.Disabled {
				total += instr.NumVoices
			}
		}
	}
	return total
}

// NumSyns return the total number of sync outputs used in the patch; summing
// the number of sync outputs of every sync unit in every instrument
func (p Patch) NumSyncs() int {
	total := 0
	for _, instr := range p {
		for _, unit := range instr.Units {
			if unit.Type == "sync" {
				total += instr.NumVoices
			}
		}
	}
	return total
}

func (p Patch) NumThreads() int {
	numThreads := 1
	for _, instr := range p {
		if l := bits.Len((uint)(instr.ThreadMaskM1 + 1)); l > numThreads {
			numThreads = l
		}
	}
	return numThreads
}

// FirstVoiceForInstrument returns the index of the first voice of given
// instrument. For example, if the Patch has three instruments (0, 1 and 2),
// with 1, 3, 2 voices, respectively, then FirstVoiceForInstrument(0) returns 0,
// FirstVoiceForInstrument(1) returns 1 and FirstVoiceForInstrument(2) returns
// 4. Essentially computes just the cumulative sum.
func (p Patch) FirstVoiceForInstrument(instrIndex int) int {
	if instrIndex < 0 {
		return 0
	}
	instrIndex = min(instrIndex, len(p))
	ret := 0
	for i := 0; i < instrIndex; i++ {
		ret += p[i].NumVoices
	}
	return ret
}

// InstrumentForVoice returns the instrument number for the given voice index.
// For example, if the Patch has three instruments (0, 1 and 2), with 1, 3, 2
// voices, respectively, then InstrumentForVoice(0) returns 0,
// InstrumentForVoice(1) returns 1 and InstrumentForVoice(3) returns 1.
func (p Patch) InstrumentForVoice(voice int) (int, error) {
	if voice < 0 {
		return 0, errors.New("voice cannot be negative")
	}
	for i, instr := range p {
		if voice < instr.NumVoices {
			return i, nil
		}
		voice -= instr.NumVoices
	}
	return 0, errors.New("voice number is beyond the total voices of an instrument")
}

// FindUnit searches the instrument index and unit index for a unit with the
// given id. Two units should never have the same id, but if they do, then the
// first match is returned. Id 0 is interpreted as "no id", thus searching for
// id 0 returns an error. Error is also returned if the searched id is not
// found. FindUnit considers disabled units as non-existent.
func (p Patch) FindUnit(id int) (instrIndex int, unitIndex int, err error) {
	if id == 0 {
		return 0, 0, errors.New("FindUnit called with id 0")
	}
	for i, instr := range p {
		for u, unit := range instr.Units {
			if unit.ID == id && !unit.Disabled {
				return i, u, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("could not find a unit with id %v", id)
}

func FindParamForModulationPort(unitName string, index int) (up UnitParameter, upIndex int, ok bool) {
	unitType, ok := UnitTypes[unitName]
	if !ok {
		return UnitParameter{}, 0, false
	}
	for i, param := range unitType.Params {
		if !param.CanModulate {
			continue
		}
		if index == 0 {
			return param, i, true
		}
		index--
	}
	return UnitParameter{}, 0, false
}
