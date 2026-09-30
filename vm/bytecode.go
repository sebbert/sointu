package vm

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/vsariola/sointu"
)

type (
	// Bytecode is the Sointu VM bytecode & data (delay times, sample offsets)
	// which is executed by the synthesizer. It is generated from a Sointu patch.
	Bytecode struct {
		// Opcodes is the bytecode, which is a sequence of opcode bytes, one
		// per unit in the patch. A byte of 0 denotes the end of an instrument,
		// at which point if that instrument has more than one voice, the
		// opcodes are repeated for each voice.
		Opcodes []byte

		// Operands are the operands of the opcodes. When executing the
		// bytecodes, every opcode reads 0 or more operands from it and advances
		// in the sequence.
		Operands []byte

		// DelayTimes is a table of delay times in samples. The delay times are
		// used by the delay units in the patch. The delay unit only stores
		// index and count of delay lines, and the delay times are looked up
		// from this table. This way multiple reverb units do not have to repeat
		// the same delay times.
		DelayTimes []uint16

		// SampleOffsets is a table of sample offsets, which tell where to find
		// a particular sample in the sample data loaded from gm.dls. The sample
		// offsets are used by the oscillator units that are configured to use
		// samples. The unit only stores the index pointing to this table.
		SampleOffsets []SampleOffset

		// BufferRegions is a table of the buffers and regions played by the
		// bufread units. A bufread unit only stores the index pointing to this
		// table.
		BufferRegions []BufferRegion
		// BufreadUnits are the bufread units of the patch, for finding their
		// state in the voices.
		BufreadUnits []BufreadUnit

		// BPM is the tempo the bytecode was made for, e.g. for spawn units
		// in sync mode.
		BPM int

		// PolyphonyBitmask is a rather peculiar bitmask used by Sointu VM to store
		// the information about which voices use which instruments: bit MAXVOICES -
		// n - 1 corresponds to voice n. If the bit 1, the next voice uses the same
		// instrument. If the bit 0, the next voice uses different instrument. For
		// example, if first instrument has 3 voices, second instrument has 2
		// voices, and third instrument four voices, the PolyphonyBitmask is: (MSB)
		// 110101110 (LSB)
		// It is only valid when WideVoices is false.
		PolyphonyBitmask uint32

		// Polyphony has the same information as PolyphonyBitmask, but a byte
		// for each voice: Polyphony[n] is bit n of PolyphonyBitmask. Unlike
		// the bitmask, it works for any number of voices.
		Polyphony []byte

		// WideVoices is true when the patch has more than 32 voices: the
		// addresses of the send units are then 3 bytes instead of 2, with the
		// global flag in bit 23 instead of bit 15, and the players use the
		// Polyphony table instead of the PolyphonyBitmask.
		WideVoices bool

		// NumVoices is the total number of voices in the patch
		NumVoices uint32

		// StereoPan is true when the patch has stereo pan units. The wasm
		// player then pans mono signals with the same code, computing the
		// right channel as (1-p)·s instead of s-p·s, which rounds
		// differently; the Go synth does the same.
		StereoPan bool

		// Spectra are the spectrum buffers of the spectral units, and
		// SpectralUnits the spectral units, in the order of the patch. The
		// operand of a spectral unit is its index in SpectralUnits.
		Spectra       []Spectrum
		SpectralUnits []SpectralUnit

		// Buses are the buffer IDs of the buses of the mc units, and MCUnits
		// the mc units, in the order of the patch. The operand of an mc unit
		// is its index in MCUnits.
		Buses   []int
		MCUnits []MCUnit
	}

	// Spectrum is a spectrum buffer: the ID of the buffer, the base 2
	// logarithm of its size in samples and its number of channels. A
	// spectrum of size n is n complex values for each channel; the spectral
	// units change bins 0 to n/2.
	Spectrum struct {
		BufferID int
		Log2Size int
		Channels int
	}

	// SpectralUnit is a spectral unit: the voice that runs it, the first
	// voice of its instrument, its spectrum and, for spcopy and spcross, the
	// spectrum it reads. Spectrum and Source are indices in Bytecode.Spectra.
	// Channels is 2 for stereo spfft and spifft units, otherwise 1. Smooth is
	// true for spcompress units with a nonzero attack or release, which keep
	// a smoothed envelope for each bin and channel.
	SpectralUnit struct {
		Type             string
		UnitID           int // for UnitSpectrum
		Voice            int
		Spectrum, Source int
		Channels         int
		Smooth           bool
	}

	// SampleOffset is an entry in the sample offset table
	SampleOffset struct {
		Start      uint32 // start offset in words (1 word = 2 bytes)
		LoopStart  uint16 // loop start offset in words, relative to Start
		LoopLength uint16 // loop length in words
	}

	// BufreadUnit is a bufread unit: its voices, the index of its state
	// among the units of the voice, and its buffer region.
	BufreadUnit struct {
		FirstVoice, NumVoices, Unit, Region int
	}

	// BufferRegion is an entry in the buffer region table. Positions are in
	// frames from the oldest valid frame of the buffer; the bufread unit
	// shifts them by its modulations.
	BufferRegion struct {
		BufferID   uint32 // sointu.Buffer.ID
		Start      uint32 // frame where playback starts; as int32, negative counts back from the newest frame
		LoopStart  uint32
		LoopLength uint32
		Fade       uint32 // length of the crossfade at the end of the loop
		EdgeFade   uint32 // length of the fade near the edges of the valid frames
		Flags      uint32 // see BufferRegionNoteTracking and BufferRegionLoop
	}
)

const (
	// BufferRegionNoteTracking is set in BufferRegion.Flags when the pitch
	// of the bufread unit follows the note.
	BufferRegionNoteTracking = 1
	// BufferRegionLoop is set in BufferRegion.Flags when the bufread unit
	// loops. A loop of zero length does not loop.
	BufferRegionLoop = 2
	// BufferRegionRing is set in BufferRegion.Flags when the bufwrite unit
	// writes continuously, wrapping around the end of the buffer, instead of
	// once while its note is held.
	BufferRegionRing = 4
	// BufferRegionWrite is set in BufferRegion.Flags for bufwrite units.
	BufferRegionWrite = 8
	// BufferRegionNoPop is set in BufferRegion.Flags for bufwrite units that
	// leave the signal on the stack.
	BufferRegionNoPop = 16
)

type bytecodeBuilder struct {
	sampleOffsetMap map[SampleOffset]int
	bufferRegionMap map[BufferRegion]int
	spectrumSizes   map[int]Spectrum // spectrum buffer ID -> its size and channels
	globalAddrs     map[int]int
	globalFixups    map[int]([]int)
	localAddrs      map[int]int
	localFixups     map[int]([]int)
	voiceNo         int
	delayIndices    [][]int
	unitNo          int
	featureSet      FeatureSet
	Bytecode
}

func NewBytecode(patch sointu.Patch, featureSet FeatureSet, bpm int) (*Bytecode, error) {
	if patch.NumVoices() > MAX_VOICES {
		return nil, fmt.Errorf("Sointu does not support more than %v concurrent voices; patch uses %v", MAX_VOICES, patch.NumVoices())
	}
	b := newBytecodeBuilder(patch, bpm)
	b.featureSet = featureSet
	for instrIndex, instr := range patch {
		if instr.NumVoices < 1 {
			return nil, errors.New("Each instrument must have at least 1 voice")
		}
		for unitIndex, unit := range instr.Units {
			if unit.Type == "" || unit.Disabled { // empty units are just ignored & skipped
				continue
			}
			opcode, ok := featureSet.Opcode(unit.Type)
			if !ok {
				return nil, fmt.Errorf(`VM is not configured to support unit type "%v"`, unit.Type)
			}
			if unit.ID != 0 {
				b.idLabel(unit.ID)
			}
			p := unit.Parameters
			if unit.Type == "pan" && p["stereo"] == 1 {
				b.StereoPan = true
			}
			switch unit.Type {
			case "oscillator":
				color := p["color"]
				if unit.Parameters["type"] == 4 {
					color = b.getSampleIndex(unit)
					if color > 255 {
						return nil, errors.New("Patch uses over 256 samples")
					}
				}
				flags := 0
				switch p["type"] {
				case sointu.Sine:
					flags = 0x40
				case sointu.Trisaw:
					flags = 0x20
				case sointu.Pulse:
					flags = 0x10
				case sointu.Gate:
					flags = 0x04
				case sointu.Sample:
					flags = 0x80
				}
				if p["lfo"] == 1 {
					flags += 0x08
				}
				if sointu.OscillatorBandlimited(unit) {
					// the type bits are one-hot: gate (0x04) together with a
					// waveform bit means a bandlimited waveform
					flags += 0x04
				}
				flags += p["unison"]
				b.op(opcode + p["stereo"])
				b.operand(p["transpose"], p["detune"], p["phase"], color, p["shape"], p["gain"], flags)
			case "delay":
				count := len(unit.VarArgs)
				if unit.Parameters["stereo"] == 1 {
					count /= 2
				}
				if count == 0 {
					continue // skip encoding delays without any delay lines
				}
				countTrack := count*2 - 1 + (unit.Parameters["notetracking"] & 1) // 1 means no note tracking and 1 delay, 2 means notetracking with 1 delay, 3 means no note tracking and 2 delays etc.
				b.op(opcode + p["stereo"])
				b.defOperands(unit)
				b.operand(b.delayIndices[instrIndex][unitIndex], countTrack)
			case "spfft", "spifft", "spcopy", "spfilter", "spcompress", "spblur", "spgate", "spphase", "spscale", "spformant", "spcross", "spcomb":
				if len(b.SpectralUnits) > 255 {
					return nil, errors.New("Patch uses over 256 spectral units")
				}
				u := SpectralUnit{Type: unit.Type, UnitID: unit.ID, Voice: patch.FirstVoiceForInstrument(instrIndex), Spectrum: b.spectrumIndex(p["buffer"]), Source: -1, Channels: 1}
				stereo := 0
				if unit.Type == "spfft" || unit.Type == "spifft" {
					stereo = p["stereo"] & 1
					u.Channels += stereo
				}
				if unit.Type == "spcopy" || unit.Type == "spcross" {
					u.Source = b.spectrumIndex(p["source"])
				}
				// spcompress does not use the stereo bit: it marks the
				// smoothing, whose attack and release follow the index
				attack, release := min(max(p["attack"], 0), 128), min(max(p["release"], 0), 128)
				if unit.Type == "spcompress" && (attack != 0 || release != 0) {
					u.Smooth = true
					stereo = 1
				}
				b.op(opcode + stereo)
				b.defOperands(unit)
				b.operand(len(b.SpectralUnits))
				switch unit.Type {
				case "spgate":
					b.operand(p["invert"] & 1)
				case "spphase":
					b.operand(min(max(p["mode"], 0), 2))
				case "spcompress":
					if u.Smooth {
						b.operand(attack, release)
					}
				case "spcomb":
					// the voices of the instrument whose notes to use, and
					// the intervals
					first, count := 0, 0
					if t := p["instrument"] - 1; t >= 0 && t < len(patch) {
						first, count = patch.FirstVoiceForInstrument(t), patch[t].NumVoices
					}
					b.operand(first, count, p["interval1"], p["interval2"], p["interval3"])
				}
				b.SpectralUnits = append(b.SpectralUnits, u)
			case "mcspread", "mcsum", "mcdelay", "mcmix", "mcloop", "mcloopend", "mcfilter":
				// operands: the index in MCUnits, and for mcspread add, for
				// mcmix the type, for mcfilter highpass and for mcdelay the
				// flags
				if len(b.MCUnits) > 255 {
					return nil, errors.New("Patch uses over 256 mc units")
				}
				u := MCUnit{Type: unit.Type, UnitID: unit.ID, Voice: patch.FirstVoiceForInstrument(instrIndex), Bus: b.busIndex(p["bus"])}
				b.op(opcode + p["stereo"]&1)
				b.defOperands(unit)
				b.operand(len(b.MCUnits))
				switch unit.Type {
				case "mcspread":
					b.operand(p["add"] & 1)
				case "mcmix":
					typ := min(max(p["type"], 0), sointu.MCMixShuffle)
					if typ == sointu.MCMixShuffle {
						u.Shuffle = newMCShuffle(p["seed"])
					}
					b.operand(typ)
				case "mcfilter":
					b.operand(p["type"] & 1)
				case "mcdelay":
					u.Delay = newMCDelay(p)
					flags := 0
					if p["notetracking"] == 1 {
						flags |= MCDelayNoteTracking
					}
					if p["allpass"] == 1 {
						flags |= MCDelayAllpass
					}
					b.operand(flags)
				}
				b.MCUnits = append(b.MCUnits, u)
			case "bufread", "bufwrite":
				index := b.getBufferRegionIndex(unit)
				if index > 255 {
					return nil, errors.New("Patch uses over 256 different buffer regions")
				}
				if unit.Type == "bufread" {
					b.BufreadUnits = append(b.BufreadUnits, BufreadUnit{FirstVoice: patch.FirstVoiceForInstrument(instrIndex), NumVoices: instr.NumVoices, Unit: b.unitNo, Region: index})
				}
				b.op(opcode + p["stereo"])
				b.defOperands(unit)
				b.operand(index)
			case "spawn":
				// operands: first voice and number of voices of the target
				// instrument, and flags: bit 0 = edge mode, bit 1 = note
				// tracking, bits 2-4 = number of arguments, bit 5 = sync mode,
				// bit 6 = steal held voices
				first, count := 0, 0
				if t := p["instrument"] - 1; t >= 0 && t < len(patch) {
					first, count = patch.FirstVoiceForInstrument(t), patch[t].NumVoices
				}
				args := min(max(p["args"], 0), sointu.MaxSpawnArgs)
				b.op(opcode)
				b.defOperands(unit)
				flags := (p["notetracking"]&1)<<1 + args<<2 + (p["steal"]&1)<<6
				switch p["mode"] {
				case sointu.SpawnModeEdge:
					flags |= 1
				case sointu.SpawnModeSync:
					flags |= 32
				}
				b.operand(first, count, flags)
			case "window":
				b.op(opcode)
				b.defOperands(unit)
			case "arg":
				b.op(opcode)
				b.operand(min(max(p["index"], 0), sointu.MaxSpawnArgs-1))
			case "aux", "in":
				b.op(opcode + p["stereo"])
				b.defOperands(unit)
				b.operand(unit.Parameters["channel"])
			case "filter":
				flags := 0
				if unit.Parameters["lowpass"] == 1 {
					flags += 0x40
				}
				if unit.Parameters["bandpass"] == 1 {
					flags += 0x20
				}
				if unit.Parameters["highpass"] == 1 {
					flags += 0x10
				}
				if unit.Parameters["bandpass"] == -1 {
					flags += 0x08
				}
				if unit.Parameters["highpass"] == -1 {
					flags += 0x04
				}
				b.op(opcode + p["stereo"])
				b.defOperands(unit)
				b.operand(flags)
			case "send":
				targetID := unit.Parameters["target"]
				targetInstrIndex, _, err := patch.FindUnit(targetID)
				targetVoice := unit.Parameters["voice"]
				addr := unit.Parameters["port"] & 7
				if err == nil {
					// local send is only possible if targetVoice is "auto" (0) and
					// the targeted unit is in the same instrument as send
					if targetInstrIndex == instrIndex && targetVoice == 0 {
						if unit.Parameters["sendpop"] == 1 {
							addr += 0x8
						}
						b.op(opcode + p["stereo"])
						b.defOperands(unit)
						b.localIDRef(targetID, addr)
					} else {
						addr += b.globalFlag()
						voiceStart := 0
						voiceEnd := patch[targetInstrIndex].NumVoices
						if targetVoice > 0 { // "all" (0) means for global send that it targets all voices of that instrument
							voiceStart = targetVoice - 1
							voiceEnd = targetVoice
						}
						addr += voiceStart * 0x400
						for i := voiceStart; i < voiceEnd; i++ {
							b.op(opcode + p["stereo"])
							b.defOperands(unit)
							if i == voiceEnd-1 && unit.Parameters["sendpop"] == 1 {
								addr += 0x8 // when making multi unit send, only the last one should have POP bit set if popping
							}
							b.globalIDRef(targetID, addr)
							addr += 0x400
						}
					}
				} else {
					// if no target will be found, the send will trash some of
					// the last values of the last port of the last voice, which
					// is unlikely to cause issues. We still honor the POP bit.
					// For 32 voices, this is 0xFFF7.
					addr = b.globalFlag() | (MAX_VOICES_NARROW*1024-1)&^0x8
					if b.WideVoices {
						addr = b.globalFlag() | (int(b.NumVoices)*1024-1)&^0x8
					}
					if unit.Parameters["sendpop"] == 1 {
						addr |= 0x8
					}
					b.op(opcode + p["stereo"])
					b.defOperands(unit)
					b.address(addr)
				}
			default:
				b.op(opcode + p["stereo"])
				b.defOperands(unit)
			}
			if b.unitNo > 63 {
				return nil, fmt.Errorf(`Instrument %v has over 63 units`, instrIndex)
			}
		}
		b.opFinish(instr)
	}
	return &b.Bytecode, nil
}

func newBytecodeBuilder(patch sointu.Patch, bpm int) *bytecodeBuilder {
	var polyphonyBitmask uint32 = 0
	numVoices := patch.NumVoices()
	polyphony := make([]byte, numVoices)
	voice := 0
	for _, instr := range patch {
		for j := 0; j < instr.NumVoices-1; j++ {
			polyphonyBitmask = (polyphonyBitmask << 1) + 1 // for each instrument, NumVoices - 1 bits are ones
			polyphony[numVoices-1-voice] = 1
			voice++
		}
		polyphonyBitmask <<= 1 // ...and the last bit is zero, to denote "change instrument"
		voice++
	}
	delayTimesInt, delayIndices := constructDelayTimeTable(patch, bpm)
	delayTimesU16 := make([]uint16, len(delayTimesInt))
	for i, d := range delayTimesInt {
		delayTimesU16[i] = uint16(d)
	}
	c := bytecodeBuilder{
		spectrumSizes:   spectrumSizes(patch),
		Bytecode:        Bytecode{PolyphonyBitmask: polyphonyBitmask, Polyphony: polyphony, WideVoices: numVoices > MAX_VOICES_NARROW, NumVoices: uint32(numVoices), DelayTimes: delayTimesU16, BPM: bpm},
		sampleOffsetMap: map[SampleOffset]int{},
		bufferRegionMap: map[BufferRegion]int{},
		globalAddrs:     map[int]int{},
		globalFixups:    map[int]([]int){},
		localAddrs:      map[int]int{},
		localFixups:     map[int]([]int){},
		delayIndices:    delayIndices}
	return &c
}

// op adds a command to the bytecode, and increments the unit number
func (b *bytecodeBuilder) op(opcode int) {
	b.Opcodes = append(b.Opcodes, byte(opcode))
	b.unitNo++
}

// opFinish adds a command to the bytecode that marks the end of an instrument, resets the unit number and increments the voice number
// local addresses are forgotten when instrument ends
func (b *bytecodeBuilder) opFinish(instr sointu.Instrument) {
	b.Opcodes = append(b.Opcodes, 0)
	b.unitNo = 0
	b.voiceNo += instr.NumVoices
	b.localAddrs = map[int]int{}
	b.localFixups = map[int]([]int){}
}

// operand appends operands to the operand stream
func (b *bytecodeBuilder) operand(operands ...int) {
	for _, v := range operands {
		b.Operands = append(b.Operands, byte(v))
	}
}

// defOperands appends the operands to the stream for the parameters that can
// be modulated and set, as many as the feature set transforms: it leaves out
// the optional parameters that the song does not use.
func (b *bytecodeBuilder) defOperands(unit sointu.Unit) {
	count := b.featureSet.TransformCount(unit.Type)
	for _, v := range sointu.UnitTypes[unit.Type].Params {
		if v.CanModulate && v.CanSet && !v.NoTransform && count > 0 {
			b.Operands = append(b.Operands, byte(unit.Parameters[v.Name]))
			count--
		}
	}
}

// localIDRef adds a reference to a local id label to the value stream; if the targeted ID has not been seen yet, it is added to the fixup list
func (b *bytecodeBuilder) localIDRef(id int, addr int) {
	if v, ok := b.localAddrs[id]; ok {
		addr += v
	} else {
		b.localFixups[id] = append(b.localFixups[id], len(b.Operands))
	}
	b.address(addr)
}

// globalIDRef adds a reference to a global id label to the value stream; if the targeted ID has not been seen yet, it is added to the fixup list
func (b *bytecodeBuilder) globalIDRef(id int, addr int) {
	if v, ok := b.globalAddrs[id]; ok {
		addr += v
	} else {
		b.globalFixups[id] = append(b.globalFixups[id], len(b.Operands))
	}
	b.address(addr)
}

// globalFlag is the bit of a send address that marks it global.
func (b *bytecodeBuilder) globalFlag() int {
	if b.WideVoices {
		return 0x800000
	}
	return 0x8000
}

// addressBytes is the number of bytes in a send address.
func (b *bytecodeBuilder) addressBytes() int {
	if b.WideVoices {
		return 3
	}
	return 2
}

// address appends a send address to the operand stream.
func (b *bytecodeBuilder) address(addr int) {
	for i := 0; i < b.addressBytes(); i++ {
		b.Operands = append(b.Operands, byte(addr>>(8*i)))
	}
}

// idLabel adds a label to the value stream for the given id; all earlier references to the id are fixed up
func (b *bytecodeBuilder) idLabel(id int) {
	localAddr := (b.unitNo + 1) << 4
	b.fixUp(b.localFixups[id], localAddr)
	b.localFixups[id] = nil
	b.localAddrs[id] = localAddr
	globalAddr := localAddr + 16 + b.voiceNo*1024
	b.fixUp(b.globalFixups[id], globalAddr)
	b.globalFixups[id] = nil
	b.globalAddrs[id] = globalAddr
}

// fixUp fixes up the references to the given id with the given delta
func (b *bytecodeBuilder) fixUp(positions []int, delta int) {
	n := b.addressBytes()
	for _, pos := range positions {
		addr := 0
		for i := 0; i < n; i++ {
			addr |= int(b.Operands[pos+i]) << (8 * i)
		}
		addr += delta
		for i := 0; i < n; i++ {
			b.Operands[pos+i] = byte(addr >> (8 * i))
		}
	}
}

// getSampleIndex returns the index of the sample in the sample offset table; if the sample has not been seen yet, it is added to the table
func (b *bytecodeBuilder) getSampleIndex(unit sointu.Unit) int {
	s := SampleOffset{Start: uint32(unit.Parameters["samplestart"]), LoopStart: uint16(unit.Parameters["loopstart"]), LoopLength: uint16(unit.Parameters["looplength"])}
	if s.LoopLength == 0 {
		// hacky quick fix: looplength 0 causes div by zero so avoid crashing
		s.LoopLength = 1
	}
	index, ok := b.sampleOffsetMap[s]
	if !ok {
		index = len(b.SampleOffsets)
		b.sampleOffsetMap[s] = index
		b.SampleOffsets = append(b.SampleOffsets, s)
	}
	return index
}

// spectrumIndex returns the index of the spectrum buffer with the given ID in
// Bytecode.Spectra, adding it if needed.
func (b *bytecodeBuilder) spectrumIndex(id int) int {
	for i, s := range b.Spectra {
		if s.BufferID == id {
			return i
		}
	}
	sp, ok := b.spectrumSizes[id]
	if !ok {
		sp = Spectrum{Log2Size: spectrumLog2Size(sointu.SpectrumSizeDefault), Channels: 1}
	}
	sp.BufferID = id
	b.Spectra = append(b.Spectra, sp)
	return len(b.Spectra) - 1
}

func spectrumLog2Size(size int) int { return bits.Len(uint(sointu.SpectrumSize(size))) - 1 }

// spectrumSizes returns the sizes, as base 2 logarithms, and the channels of
// the spectrum buffers written by the spfft units of the patch, and copied to
// by spcopy units.
func spectrumSizes(patch sointu.Patch) map[int]Spectrum {
	ret := map[int]Spectrum{}
	var copies [][2]int // source, destination
	for _, instr := range patch {
		for _, u := range instr.Units {
			if u.Disabled {
				continue
			}
			switch u.Type {
			case "spfft":
				if _, ok := ret[u.Parameters["buffer"]]; !ok {
					ret[u.Parameters["buffer"]] = Spectrum{Log2Size: spectrumLog2Size(u.Parameters["size"]), Channels: 1 + u.Parameters["stereo"]&1}
				}
			case "spcopy":
				copies = append(copies, [2]int{u.Parameters["source"], u.Parameters["buffer"]})
			}
		}
	}
	for range copies { // copies of copies
		for _, c := range copies {
			if _, ok := ret[c[1]]; !ok {
				if size, ok := ret[c[0]]; ok {
					ret[c[1]] = size
				}
			}
		}
	}
	return ret
}

// getBufferRegionIndex returns the index of the region played by a bufread unit
// in the buffer region table, adding it to the table if it is not there yet.
func (b *bytecodeBuilder) getBufferRegionIndex(unit sointu.Unit) int {
	p := unit.Parameters
	r := BufferRegion{BufferID: uint32(p["buffer"]), Start: uint32(int32(p["start"])), EdgeFade: uint32(max(p["edgefade"], 0))}
	// loop points as int32 too; a negative loop start counts from the end
	if p["loop"] == 1 {
		r.LoopStart, r.LoopLength, r.Fade = uint32(int32(p["loopstart"])), uint32(p["looplength"]), uint32(p["fade"])
		r.Flags |= BufferRegionLoop
	}
	if unit.Type == "bufwrite" {
		r = BufferRegion{BufferID: uint32(p["buffer"]), Flags: BufferRegionWrite}
		if p["oneshot"] == 0 {
			r.Flags |= BufferRegionRing
		}
		if p["pop"] == 0 {
			r.Flags |= BufferRegionNoPop
		}
	} else if p["notetracking"] == 1 {
		r.Flags |= BufferRegionNoteTracking
	}
	index, ok := b.bufferRegionMap[r]
	if !ok {
		index = len(b.BufferRegions)
		b.bufferRegionMap[r] = index
		b.BufferRegions = append(b.BufferRegions, r)
	}
	return index
}
