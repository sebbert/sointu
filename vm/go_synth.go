package vm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/vsariola/sointu"
)

//go:generate go run generate/generate.go

type (
	// GoSynth is a pure-Go bytecode interpreter for the Sointu VM bytecode. It
	// can only simulate bytecode compiled for AllFeatures, as the opcodes hard
	// coded in it for speed. If you are interested exactly how opcodes / units
	// work, studying GoSynth.Render is a good place to start.
	//
	// Internally, it uses software stack with practically no limitations in the
	// number of signals, so be warned that if you compose patches for it, they
	// might not work with the x87 implementation, as it has only 8-level stack.
	GoSynth struct {
		bytecode   Bytecode
		stack      []float32
		state      synthState
		delaylines []delayline
		otts       []ottState
		limiters   []limiterState
		reverbs    []reverbState
		buffers    map[int]*synthBuffer
		spectra    []spectrum
		spectral   []spectralState // states of the spectral units
		buses      []mcBus
		mc         []mcState // states of the mc units
		scratch    []float32
		cpuLoad    sointu.CPULoad
		layout     synthLayout // of the patch, for the states of its units: see carry.go
		taps       []tap       // where the signal is recorded: see tap.go; nil if nowhere
	}

	// GoSynther is a Synther implementation that can converts patches into
	// GoSynths.
	GoSynther struct {
	}
)

// MAX_VOICES is the maximum number of voices in a patch. Patches with more
// than MAX_VOICES_NARROW voices use wider send addresses and do not compile to
// the x86 players.
const (
	MAX_VOICES        = 255
	MAX_VOICES_NARROW = 32
)
const MAX_UNITS = 63

type (
	unit struct {
		state [8]float32
		ports [8]float32
	}

	voice struct {
		note     byte
		sustain  bool
		spawned  uint32                       // global time + 1 when a spawn unit last triggered the voice, 0 if never
		release  uint32                       // global time when to release a spawned note, 0 if never
		length   uint32                       // length of a spawned note in frames, 0 if none
		released uint32                       // global time when the note was released
		args     [sointu.MaxSpawnArgs]float32 // values passed by the spawn unit
		units    [MAX_UNITS]unit
	}

	synthState struct {
		outputs    [8]float32
		randSeed   uint32
		globalTime uint32
		voices     [MAX_VOICES]voice
	}

	delayline struct {
		buffer      [65536]float32
		dampState   float32
		dcIn        float32
		dcFiltState float32
	}
)

const (
	envStateAttack = iota
	envStateDecay
	envStateSustain
	envStateRelease
)

var su_sample_table [3440660]byte

func init() {
	var f *os.File
	var err error
	if f, err = os.Open("gm.dls"); err == nil { // try to open from current directory first
		goto success
	}
	if f, err = os.Open(filepath.Join(os.Getenv("SystemRoot"), "system32", "drivers", "gm.dls")); err == nil {
		goto success
	}
	if f, err = os.Open(filepath.Join(os.Getenv("SystemRoot"), "SysWOW64", "drivers", "gm.dls")); err == nil {
		goto success
	}
	return
success:
	defer f.Close()
	// read file, ignoring errors
	f.Read(su_sample_table[:])
}

func (s GoSynther) Name() string                 { return "Go" }
func (s GoSynther) SupportsMultithreading() bool { return false }

func (s GoSynther) Synth(patch sointu.Patch, bpm int) (sointu.Synth, error) {
	bytecode, err := NewBytecode(patch, AllFeatures{}, bpm)
	if err != nil {
		return nil, fmt.Errorf("error compiling %v", err)
	}
	ret := &GoSynth{bytecode: *bytecode, stack: make([]float32, 0, 4), delaylines: make([]delayline, patch.NumDelayLines()), otts: make([]ottState, patch.NumOtts()), limiters: make([]limiterState, patch.NumLimiters()), reverbs: make([]reverbState, patch.NumReverbs())}
	ret.state.randSeed = 1
	ret.layout = newSynthLayout(patch)
	ret.setSpectra(nil)
	ret.setMC(nil)
	return ret, nil
}

// envelopeStep moves the level of an envelope stage from start to end by one
// sample. delta is the rate of the linear envelope, positive when rising.
// With a curve c > 0 (12·curve², from 0 to 12 when not modulated), the level
// instead follows a one-pole filter toward a target beyond end:
//
//	target = end + (end - start)/(2^c - 1)
//	level += (target - level)·(1 - 2^(-c·|delta|/|end - start|))
//
// The distance to the target shrinks by 2^(-c) from start to end, as the
// target is 2^c/(2^c - 1) of the stage away at the start and 1/(2^c - 1) at
// the end. It shrinks by 2^(-c·|delta|/|end - start|) per sample, so the
// stage takes |end - start|/|delta| samples, as long as the linear stage.
// The larger c, the closer the target and the more curved the stage; as c
// goes to 0, the target goes to infinity and the stage becomes linear, which
// it is below c = 2^-20. 2^x - 1 is computed with exp2m1f, as for slow stages
// 1 - 2^(-x) is too small for 1 - exp2f(-x). The wasm player computes the
// same, operation by operation.
func envelopeStep(level, delta, start, end, curve float32) float32 {
	if curve < 0x1p-20 {
		return level + delta
	}
	target := float32((end-start)/exp2m1f(curve)) + end
	span := float32(math.Abs(float64(end - start)))
	return level + float32((level-target)*exp2m1f(-curve*float32(math.Abs(float64(delta)))/span))
}

func (s *GoSynth) Trigger(voiceIndex int, note byte) {
	s.state.voices[voiceIndex] = voice{}
	s.state.voices[voiceIndex].note = note
	s.state.voices[voiceIndex].sustain = true
}

func (s *GoSynth) Release(voiceIndex int) {
	if v := &s.state.voices[voiceIndex]; v.sustain {
		v.sustain, v.released = false, s.state.globalTime
	}
}

func (s *GoSynth) Close() {}

// synthBuffer is a buffer in the synth. The valid frames are the filled
// frames before head, wrapping around the end of the buffer.
type synthBuffer struct {
	audio        sointu.BufferAudio
	head, filled uint32
	written      uint32 // global time + 1 of the frame written last, for mixing writers
}

func (s *GoSynth) SetBuffers(buffers map[int]sointu.BufferAudio) {
	s.buffers = make(map[int]*synthBuffer, len(buffers))
	for id, b := range buffers {
		frames := b.Frames()
		if b.Writable {
			s.buffers[id] = &synthBuffer{audio: b, head: uint32(min(max(b.Head, 0), frames)), filled: uint32(min(max(b.Filled, 0), frames))}
		} else {
			s.buffers[id] = &synthBuffer{audio: b, filled: uint32(frames)}
		}
	}
}

func (s *GoSynth) Playheads(dst []sointu.Playhead) []sointu.Playhead {
	for _, b := range s.bytecode.BufreadUnits {
		r := s.bytecode.BufferRegions[b.Region]
		buf := s.buffers[int(r.BufferID)]
		if buf == nil || buf.audio.Frames() == 0 {
			continue
		}
		capacity := uint32(buf.audio.Frames())
		for v := b.FirstVoice; v < b.FirstVoice+b.NumVoices && v < len(s.state.voices); v++ {
			voice := &s.state.voices[v]
			u := &voice.units[b.Unit]
			released := 0
			if !voice.sustain {
				released = int(s.state.globalTime-voice.released) + 1
			}
			if voice.note == 0 || released > sointu.MaxPlayheadRelease || math.Float32bits(u.state[3]) == 0 {
				continue
			}
			pos, base := int32(math.Float32bits(u.state[0])), math.Float32bits(u.state[2])
			if pos < 0 || pos >= int32(capacity) {
				continue // outside the buffer
			}
			dst = append(dst, sointu.Playhead{BufferID: int(r.BufferID), Frame: int((base + uint32(pos)) % capacity), Released: released})
		}
	}
	return dst
}

func (s *GoSynth) WrittenBuffers() map[int]sointu.BufferAudio {
	ret := map[int]sointu.BufferAudio{}
	for id, b := range s.buffers {
		if b.audio.Writable {
			a := b.audio
			a.Head, a.Filled = int(b.head), int(b.filled)
			ret[id] = a
		}
	}
	return ret
}

func (s *GoSynth) CPULoad(loads []sointu.CPULoad) int {
	if len(loads) < 1 {
		return 0
	}
	loads[0] = s.cpuLoad
	return 1
}

func (s *GoSynth) Update(patch sointu.Patch, bpm int) error {
	bytecode, err := NewBytecode(patch, AllFeatures{}, bpm)
	if err != nil {
		return fmt.Errorf("error compiling %v", err)
	}
	old := s.bytecode
	s.bytecode = *bytecode
	s.setSpectra(&old)
	s.setMC(&old)
	for len(s.delaylines) < patch.NumDelayLines() {
		s.delaylines = append(s.delaylines, delayline{})
	}
	for len(s.otts) < patch.NumOtts() {
		s.otts = append(s.otts, ottState{})
	}
	for len(s.limiters) < patch.NumLimiters() {
		s.limiters = append(s.limiters, limiterState{})
	}
	for len(s.reverbs) < patch.NumReverbs() {
		s.reverbs = append(s.reverbs, reverbState{})
	}
	// the units that are still there keep their state
	if layout := newSynthLayout(patch); !layout.same(s.layout) {
		s.carryState(s.layout, layout)
		s.layout = layout
	} else {
		s.layout = layout
	}
	if s.taps != nil {
		s.resolveTaps()
	}
	return nil
}

func (s *GoSynth) Render(buffer sointu.AudioBuffer, maxtime int) (samples int, renderTime int, renderError error) {
	startTime := time.Now()
	defer func() { s.cpuLoad.Update(time.Since(startTime), int64(samples)) }()

	defer func() {
		if err := recover(); err != nil {
			renderError = fmt.Errorf("render panicced: %v", err)
		}
	}()
	var params [8]float32
	stack := s.stack[:]
	stack = append(stack, []float32{0, 0, 0, 0}...)
	synth := &s.state
	for renderTime < maxtime && len(buffer) > 0 {
		opcodesInstr := s.bytecode.Opcodes
		operandsInstr := s.bytecode.Operands
		opcodes, operands := opcodesInstr, operandsInstr
		delaylines := s.delaylines
		otts := s.otts
		limiters := s.limiters
		reverbs := s.reverbs
		voicesRemaining := s.bytecode.NumVoices
		voices := s.state.voices[:]
		units := voices[0].units[:]
		for voicesRemaining > 0 {
			op := opcodes[0]
			opcodes = opcodes[1:]
			channels := int((op & 1) + 1)
			stereo := channels == 2
			opNoStereo := (op & 0xFE) >> 1
			if opNoStereo == 0 {
				voicesRemaining--
				if voicesRemaining > 0 {
					voices = voices[1:]
					units = voices[0].units[:]
				}
				if voicesRemaining > 0 && s.bytecode.Polyphony[voicesRemaining] == 1 {
					opcodes, operands = opcodesInstr, operandsInstr
				} else {
					opcodesInstr, operandsInstr = opcodes, operands
				}
				continue
			}
			if s.taps != nil {
				s.tapUnit(int(s.bytecode.NumVoices-voicesRemaining), MAX_UNITS-len(units), stack)
			}
			tcount := transformCounts[opNoStereo-1]
			if len(operands) < tcount {
				return samples, renderTime, errors.New("operand stream ended prematurely")
			}
			voice := &voices[0]
			unit := &units[0]
			operandsAtTransform := operands
			for i := 0; i < tcount; i++ {
				params[i] = float32(operands[0])/128.0 + unit.ports[i]
				unit.ports[i] = 0
				operands = operands[1:]
			}
			l := len(stack)
			switch opNoStereo {
			case opAdd:
				if stereo {
					stack[l-1] += stack[l-3]
					stack[l-2] += stack[l-4]
				} else {
					stack[l-1] += stack[l-2]
				}
			case opAddp:
				if stereo {
					stack[l-3] += stack[l-1]
					stack[l-4] += stack[l-2]
					stack = stack[:l-2]
				} else {
					stack[l-2] += stack[l-1]
					stack = stack[:l-1]
				}
			case opMul:
				if stereo {
					stack[l-1] *= stack[l-3]
					stack[l-2] *= stack[l-4]
				} else {
					stack[l-1] *= stack[l-2]
				}
			case opMulp:
				if stereo {
					stack[l-3] *= stack[l-1]
					stack[l-4] *= stack[l-2]
					stack = stack[:l-2]
				} else {
					stack[l-2] *= stack[l-1]
					stack = stack[:l-1]
				}
			case opXch:
				if stereo {
					stack[l-3], stack[l-1] = stack[l-1], stack[l-3]
					stack[l-4], stack[l-2] = stack[l-2], stack[l-4]
				} else {
					stack[l-2], stack[l-1] = stack[l-1], stack[l-2]
				}
			case opPush:
				if stereo {
					stack = append(stack, stack[l-2])
				}
				stack = append(stack, stack[l-1])
			case opPop:
				if stereo {
					stack = stack[:l-2]
				} else {
					stack = stack[:l-1]
				}
			case opDistort:
				amount := params[0]
				if stereo {
					stack[l-2] = waveshape(stack[l-2], amount)
				}
				stack[l-1] = waveshape(stack[l-1], amount)
			case opLoadval:
				val := params[0]*2 - 1
				if stereo {
					stack = append(stack, val)
				}
				stack = append(stack, val)
			case opOut:
				if stereo {
					synth.outputs[0] += float32(stack[l-1] * params[0])
					synth.outputs[1] += float32(stack[l-2] * params[0])
					stack = stack[:l-2]
				} else {
					synth.outputs[0] += float32(stack[l-1] * params[0])
					stack = stack[:l-1]
				}
			case opOutaux:
				if stereo {
					synth.outputs[0] += float32(stack[l-1] * params[0])
					synth.outputs[1] += float32(stack[l-2] * params[0])
					synth.outputs[2] += float32(stack[l-1] * params[1])
					synth.outputs[3] += float32(stack[l-2] * params[1])
					stack = stack[:l-2]
				} else {
					synth.outputs[0] += float32(stack[l-1] * params[0])
					synth.outputs[2] += float32(stack[l-1] * params[1])
					stack = stack[:l-1]
				}
			case opAux:
				var channel byte
				channel, operands = operands[0], operands[1:]
				if stereo {
					synth.outputs[channel+1] += float32(stack[l-2] * params[0])
				}
				synth.outputs[channel] += float32(stack[l-1] * params[0])
				stack = stack[:l-channels]
			case opSpeed:
				// like the wasm player, which truncates the time step; the x86
				// players round it
				r := unit.state[0] + (exp2f(stack[l-1]*2.206896551724138) - 1)
				w := int32(r)
				unit.state[0] = r - float32(w)
				renderTime += int(w)
				stack = stack[:l-1]
			case opIn:
				var channel byte
				channel, operands = operands[0], operands[1:]
				if stereo {
					stack = append(stack, synth.outputs[channel+1])
					synth.outputs[channel+1] = 0
				}
				stack = append(stack, synth.outputs[channel])
				synth.outputs[channel] = 0
			case opEnvelope:
				if !voices[0].sustain {
					if unit.state[0] != envStateRelease {
						unit.state[2] = unit.state[1] // the level where the release starts
					}
					unit.state[0] = envStateRelease // set state to release
				}
				state := unit.state[0]
				level := unit.state[1]
				// like the wasm player: the rate is that of the parameter of
				// the state, and decay ends in the sustain state, which holds
				// the level
				delta := nonLinearMap(params[int(state)])
				curve := params[5] * params[5] * 12
				switch state {
				case envStateAttack:
					level = envelopeStep(level, delta, 0, 1, curve)
					if level >= 1 {
						level = 1
						state = envStateDecay
					}
				case envStateDecay:
					sustain := params[2]
					level = envelopeStep(level, -delta, 1, sustain, curve)
					if level <= sustain {
						level = sustain
						state = envStateSustain
					}
				case envStateRelease:
					level = envelopeStep(level, -delta, unit.state[2], 0, curve)
					if level <= 0 {
						level = 0
					}
				}
				unit.state[0] = state
				unit.state[1] = level
				output := level * params[4]
				stack = append(stack, output)
				if stereo {
					stack = append(stack, output)
				}
			case opNoise:
				if stereo {
					value := waveshape(synth.rand(), params[0]) * params[1]
					stack = append(stack, value)
				}
				value := waveshape(synth.rand(), params[0]) * params[1]
				stack = append(stack, value)
			case opGain:
				if stereo {
					stack[l-2] *= params[0]
				}
				stack[l-1] *= params[0]
			case opInvgain:
				if stereo {
					stack[l-2] /= params[0]
				}
				stack[l-1] /= params[0]
			case opDbgain:
				gain := exp2f(float32(params[0]-0.5) * 13.287712379549449)
				if stereo {
					stack[l-2] *= gain
				}
				stack[l-1] *= gain
			case opClip:
				if stereo {
					stack[l-2] = clip(stack[l-2])
				}
				stack[l-1] = clip(stack[l-1])
			case opCrush:
				if stereo {
					stack[l-2] = crush(stack[l-2], params[0])
				}
				stack[l-1] = crush(stack[l-1], params[0])
			case opHold:
				freq2 := params[0] * params[0]
				for i := 0; i < channels; i++ {
					phase := unit.state[i] - freq2
					if phase <= 0 {
						unit.state[2+i] = stack[l-1-i]
						phase += 1.0
					}
					stack[l-1-i] = unit.state[2+i]
					unit.state[i] = phase
				}
			case opSend:
				addr, globalFlag := int(operands[0])|int(operands[1])<<8, 0x8000
				operands = operands[2:]
				if s.bytecode.WideVoices {
					addr, globalFlag = addr|int(operands[0])<<16, 0x800000
					operands = operands[1:]
				}
				targetVoice := voice
				if addr&globalFlag == globalFlag {
					addr -= globalFlag + 0x10
					targetVoice = &synth.voices[addr>>10]
				}
				unitIndex := ((addr & 0x03F0) >> 4) - 1
				port := addr & 7
				amount := params[0]*2 - 1
				for i := 0; i < channels; i++ {
					targetVoice.units[unitIndex].ports[int(port)+i] += float32(stack[l-1-i] * amount)
				}
				if addr&0x8 == 0x8 {
					stack = stack[:l-channels]
				}
			case opReceive:
				if stereo {
					stack = append(stack, unit.ports[1])
					unit.ports[1] = 0
				}
				stack = append(stack, unit.ports[0])
				unit.ports[0] = 0
			case opLoadnote:
				noteFloat := float32(voice.note)/64 - 1
				stack = append(stack, noteFloat)
				if stereo {
					stack = append(stack, noteFloat)
				}
			case opPan:
				if !stereo && !s.bytecode.StereoPan { // like the mono only pan of the wasm player
					x := stack[l-1]
					stack[l-1] = x * params[0]
					stack = append(stack, x-float32(x*params[0]))
					break
				}
				if !stereo {
					stack = append(stack, stack[l-1])
					l++
				}
				stack[l-2] *= params[0]
				stack[l-1] *= 1 - params[0]
			case opFilter:
				freq2 := params[0] * params[0]
				res := params[1]
				var flags byte
				flags, operands = operands[0], operands[1:]
				for i := 0; i < channels; i++ {
					low, band := unit.state[0+i], unit.state[2+i]
					low += float32(freq2 * band)
					high := stack[l-1-i] - low - float32(res*band)
					band += float32(freq2 * high)
					unit.state[0+i], unit.state[2+i] = low, band
					var output float32
					if flags&0x40 == 0x40 {
						output += low
					}
					if flags&0x20 == 0x20 {
						output += band
					}
					if flags&0x10 == 0x10 {
						output += high
					}
					if flags&0x08 == 0x08 {
						output -= band
					}
					if flags&0x04 == 0x04 {
						output -= high
					}
					stack[l-1-i] = output
				}
			case opOscillator:
				var flags byte
				flags, operands = operands[0], operands[1:]
				if flags&0x80 == 0x80 { // sample oscillators exist only in the x86 players
					s.sampleOscillator(unit, voice, flags, operandsAtTransform, &params, channels, &stack)
					unit.ports[6] = 0
					break
				}
				// like the wasm player, operation by operation
				freqMod := unit.ports[6]
				unit.ports[6] = 0
				detuneStereo := float32(params[1]*2) - 1
				unison := flags & 3
				// gate (0x04) with a waveform bit means bandlimited
				gate := flags&0x74 == 0x04
				bandlimit := flags&0x04 == 0x04 && !gate
				// the phase parameter of the previous sample is in port 7,
				// which the oscillator has no input for
				var dPhase float32
				if bandlimit {
					dPhase = params[2] - unit.ports[7]
					unit.ports[7] = params[2]
				}
				for i := 0; i < channels; i++ {
					detune := detuneStereo
					var output float32
					for j := byte(0); j <= unison; j++ {
						k := i + 2*int(j) // the state of this oscillator
						pitch := float32(float32(params[0]*2)-1)/0.015625 + detune
						if flags&0x8 == 0 { // if lfo is disabled, add note to oscillator transpose
							pitch += float32(voice.note)
						}
						omega := exp2f(pitch * 0.0833333) // semitones to octaves
						// float32() rounds products before they are added, as Go
						// may otherwise fuse them into multiply-adds, even
						// across statements
						if flags&0x8 == 0 {
							omega = float32(omega * 0.000092696138) // scaling coefficient to get middle-C where it should be
						} else {
							omega = float32(omega * 0.000038) // pretty random scaling constant to get LFOs into reasonable range. Historical reasons, goes all the way back to 4klang
						}
						advance := omega + freqMod
						phase := advance + unit.state[k]
						phase -= floor32(phase)
						unit.state[k] = phase
						phase += params[2]
						phase -= floor32(phase)
						color := params[3]
						var dt float32 // the phase advance of this sample, 0 when not bandlimited
						if bandlimit {
							dt = min(max(abs32(advance+dPhase), minBandlimitDt), 0.5)
						}
						var amplitude float32
						switch {
						case flags&0x40 == 0x40: // Sine
							amplitude = oscillatorSine(phase, color, dt)
						case flags&0x20 == 0x20: // Trisaw
							amplitude = oscillatorTrisaw(phase, color, dt)
						case flags&0x10 == 0x10: // Pulse
							amplitude = oscillatorPulse(phase, color, dt)
						case gate:
							gateBits := int32(operandsAtTransform[4])<<8 | int32(operandsAtTransform[3])
							x := float32(gateBits >> (int32(float32(phase*16)+0.5) & 15) & 1)
							// the smoothed gate is 4 floats after the phase,
							// which with stereo unison runs into the ports
							g := unitFloat(unit, 4+k)
							amplitude = float32(float32(*g-x)*0.99609375) + x
							*g = amplitude
						}
						if !gate {
							amplitude = waveshape(amplitude, params[4])
						}
						output += float32(amplitude * params[5])
						if j < unison {
							params[2] += 0.08333333 // 1/12, add small phase shift so all oscillators don't start in phase
						}
						detune = float32(-detune * 0.5)
					}
					stack = append(stack, output)
					detuneStereo = -detuneStereo
				}
			case opBufwrite:
				index := operands[0]
				operands = operands[1:]
				s.bufwrite(unit, voice, s.bytecode.BufferRegions[index], params[0], stereo, &stack)
			case opSpawn:
				first, count, flags := int(operands[0]), int(operands[1]), operands[2]
				operands = operands[3:]
				s.spawn(unit, voice, first, count, flags, params[0], params[1], params[2], &stack)
			case opSpfft:
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					in := [2]float32{stack[l-1], 0}
					if stereo {
						in[1] = stack[l-2]
					}
					s.spfft(index, in)
					s.tapSpectrum(index)
				}
				operands = operands[1:]
				stack = stack[:l-channels]
			case opSpifft:
				var out [2]float32
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					out = s.spifft(index, params[0])
					s.tapSpectrum(index)
				}
				operands = operands[1:]
				if stereo {
					stack = append(stack, out[1])
				}
				stack = append(stack, out[0])
			case opSpcomb:
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					s.spcomb(index, voice.note, params[0], params[1], operands[1:6])
					s.tapSpectrum(index)
				}
				operands = operands[6:]
			case opSpgate, opSpphase:
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					if opNoStereo == opSpgate {
						s.spgate(index, params[0], operands[1] != 0)
					} else {
						s.spphase(index, operands[1], params[0])
					}
					s.tapSpectrum(index)
				}
				operands = operands[2:]
			case opSpcompress:
				// with the stereo bit, the attack and release follow the index
				var attack, release byte
				if stereo {
					attack, release = operands[1], operands[2]
				}
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					s.spcompress(index, params[0], params[1], attack, release)
					s.tapSpectrum(index)
				}
				operands = operands[1+2*(op&1):]
			case opSpfilter, opSpblur, opSpscale, opSpformant, opSpcross:
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					switch opNoStereo {
					case opSpcross:
						s.spcross(index, params[0], params[1])
					case opSpscale:
						s.spscale(index, params[0], params[1])
					case opSpformant:
						s.spformant(index, params[0], params[1])
					case opSpfilter:
						s.spfilter(index, params[0], params[1], params[2])
					case opSpblur:
						s.spblur(index, params[0], params[1])
					}
					s.tapSpectrum(index)
				}
				operands = operands[1:]
			case opSpcopy:
				if index := int(operands[0]); s.bytecode.SpectralUnits[index].Voice == int(s.bytecode.NumVoices-voicesRemaining) {
					s.spcopy(index)
					s.tapSpectrum(index)
				}
				operands = operands[1:]
			case opWindow:
				stack = append(stack, window(unit, voice, params[0], params[1]))
			case opArg:
				stack = append(stack, voice.args[operands[0]])
				operands = operands[1:]
			case opBufread:
				var index byte
				index, operands = operands[0], operands[1:]
				s.bufread(unit, voice, s.bytecode.BufferRegions[index], params[0], params[1], params[2], params[3], stereo, &stack)
				unit.ports[4], unit.ports[5], unit.ports[6] = 0, 0, 0
			case opDelay:
				pregain2 := params[0] * params[0]
				damp := params[3]
				feedback := params[2]
				var index, count byte
				index, count, operands = operands[0], operands[1], operands[2:]
				t := uint16(s.state.globalTime)
				stackIndex := l - channels
				for i := 0; i < channels; i++ {
					var d *delayline
					signal := stack[stackIndex]
					output := float32(params[1] * signal) // dry output
					for j := byte(0); j < count; j += 2 {
						d, delaylines = &delaylines[0], delaylines[1:]
						// like the wasm player, operation by operation
						delay := float32(s.bytecode.DelayTimes[index]) + float32(unit.ports[4]*32767)
						if count&1 == 0 {
							delay /= exp2f(float32(voice.note) * 0.08333333)
						}
						delSignal := d.buffer[t-uint16(delay+0.5)]
						output += delSignal
						d.dampState = float32((d.dampState-delSignal)*damp) + delSignal
						d.buffer[t] = float32(feedback*d.dampState) + float32(pregain2*signal)
						index++
					}
					d.dcFiltState = output + (float32(0.99609375*d.dcFiltState) - d.dcIn)
					d.dcIn = output
					stack[stackIndex] = d.dcFiltState
					stackIndex++
				}
				unit.ports[4] = 0
			case opMcspread, opMcsum, opMcdelay, opMcmix, opMcloop, opMcloopend, opMcfilter:
				operands = s.runMC(opNoStereo, stereo, operands, &params, int(s.bytecode.NumVoices-voicesRemaining), voice.note, &stack)
			case opOtt:
				ott(&otts[0], &params, channels, stack)
				otts = otts[1:]
			case opSoftclip:
				var oversample byte
				oversample, operands = operands[0], operands[1:]
				drive := float32(1 + float32(7*params[0]))
				for i := range channels {
					x := float32(stack[l-1-i] * drive)
					if oversample != 0 {
						stack[l-1-i] = softclipOversampled(unit.state[4*i:4*i+4], x, params[1])
					} else {
						stack[l-1-i] = softclip(x, params[1])
					}
				}
			case opWidth:
				stack[l-1], stack[l-2] = width(&unit.state, stack[l-1], stack[l-2], params[0], params[1])
			case opLadder:
				for i := range channels {
					stack[l-1-i] = ladder(unit.state[4*i:4*i+4], stack[l-1-i], params[0], params[1], params[2])
				}
			case opLimiter:
				var lookahead byte
				lookahead, operands = operands[0], operands[1:]
				limiter(&limiters[0], &params, int(lookahead)*4, channels, stack)
				limiters = limiters[1:]
			case opReverb:
				var index byte
				index, operands = operands[0], operands[1:]
				reverb(&s.bytecode.Reverbs[index], &reverbs[0], &params, stack)
				reverbs = reverbs[1:]
			case opCompressor:
				signalLevel := float32(stack[l-1] * stack[l-1]) // square the signal to get power
				if stereo {
					signalLevel += float32(stack[l-2] * stack[l-2])
				}
				currentLevel := unit.state[0]
				paramIndex := 0 // compressor attacking
				if signalLevel < currentLevel {
					paramIndex = 1 // compressor releasing
				}
				alpha := nonLinearMap(params[paramIndex]) // map attack or release to a smoothing coefficient
				currentLevel += float32((signalLevel - currentLevel) * alpha)
				unit.state[0] = currentLevel
				var gain float32 = 1
				if threshold2 := params[3] * params[3]; currentLevel > threshold2 {
					gain = powf(threshold2/currentLevel, params[4]*0.5)
				}
				gain /= params[2] // apply inverse gain
				stack = append(stack, gain)
				if stereo {
					stack = append(stack, gain)
				}
			case opBelleq:
				// Bell-shaped peaking filter equations based on https://shepazu.github.io/Audio-EQ-Cookbook/audio-eq-cookbook.html:
				//   alpha = sin(omega0)/(2*Q) where omega0 determines the angular frequency of the peak and Q is the Q-factor
				//   A = sqrt(10^(dBgain/20)) = 10^(dBgain/40) where dbGain determines the gain at the peak
				//   b0 = 1 + alpha*A, b1 = -2*cos(omega0), b2 = 1 - alpha*A,
				//   a0 = 1 + alpha/A, a1 = -2*cos(omega0), a2 = 1 - alpha/A are the biquad filter coefficients
				// like the wasm player, operation by operation: the state is
				// s1, s2, at 4 floats further for the right channel, and
				// cos(omega0) is computed as sqrt(1-sin(omega0)²)
				sinw := sinTurns(float32(params[0]*params[0]) * 0.31830987) // omega0 = 2f² in turns
				alpha := sinw * float32(params[1]*2)                        // Q=1/(4*(p/128)) gives a range of Q = 0.25 ... 32
				A := exp2f(float32(params[2]-0.5) * 6.643856189774724)      // +-40 dB, reusing same constant as dbgain unit
				u, v := A*alpha, alpha/A
				cosw := sqrt32(1 - float32(sinw*sinw))
				for i := range channels {
					st := unit.state[4*i : 4*i+2]
					x := stack[l-1-i]
					y := float32(float32(x+float32(u*x))+st[0]) / (v + 1)
					st[0] = float32(float32(float32(y-x)*cosw)*2) + st[1]
					st[1] = float32(x-y) + float32(float32(v*y)-float32(u*x))
					stack[l-1-i] = y
				}
			case opSync:
				break
			default:
				return samples, renderTime, errors.New("invalid / unimplemented opcode")
			}
			units = units[1:]
		}
		if len(stack) < 4 {
			return samples, renderTime, errors.New("stack underflow")
		}
		if len(stack) > 4 {
			return samples, renderTime, errors.New("stack not empty")
		}
		if s.taps != nil {
			s.tapFrame()
		}
		buffer[0][0], buffer[0][1] = synth.outputs[0], synth.outputs[1]
		synth.outputs[0] = 0
		synth.outputs[1] = 0
		buffer = buffer[1:]
		samples++
		renderTime++
		s.state.globalTime++
	}
	s.stack = stack[:0]
	return samples, renderTime, nil
}

// bufread pushes the next frame of a buffer region on the stack. Positions
// are in frames from the oldest valid frame of the buffer at the time the note
// was triggered. unit.state holds, as bits, the integer part of the position
// (signed) in state[0], the fraction in state[1], the oldest valid frame at
// the trigger in state[2], 1 in state[3] once playback has started, 1 in
// state[4] once the position has been in the loop and the filled length at the
// trigger in state[5]; all are zeroed when a note is triggered. The modulations of start, loop start and loop length are in
// unit.ports[4:7]. Voices that have never been triggered (note 0) are silent.
// Matches $su_op_bufread in the wasm player.
func (s *GoSynth) bufread(unit *unit, voice *voice, r BufferRegion, transpose, detune, gain, speed float32, stereo bool, stack *[]float32) {
	buf := s.buffers[int(r.BufferID)]
	if voice.note == 0 || buf == nil || buf.audio.Frames() == 0 {
		if stereo {
			*stack = append(*stack, 0)
		}
		*stack = append(*stack, 0)
		return
	}
	capacity := uint32(buf.audio.Frames())
	filled := int32(buf.filled)
	oldest := (buf.head + capacity - buf.filled) % capacity
	pos, frac := int32(math.Float32bits(unit.state[0])), unit.state[1]
	base := math.Float32bits(unit.state[2])
	inLoop := math.Float32bits(unit.state[4]) != 0
	if math.Float32bits(unit.state[3]) == 0 {
		base = oldest
		unit.state[5] = math.Float32frombits(uint32(filled)) // for negative loop starts
		start := int32(r.Start)
		if start < 0 {
			start += filled // from the newest frame
		}
		pos = bufreadFrames(start, unit.ports[4], filled, int32(capacity))
		// keep the fraction, so that small changes of the modulation do not
		// jump whole frames
		offset := float32(unit.ports[4] * float32(filled)) // rounded, not fused with the subtraction
		frac = offset - float32(math.Floor(float64(offset)))
	}
	next := pos + 1
	var loopStart, loopLength, loopEnd, fade int32
	if r.Flags&BufferRegionLoop != 0 {
		ls := int32(r.LoopStart)
		if ls < 0 {
			ls += int32(math.Float32bits(unit.state[5])) // from the newest frame at the trigger
		}
		loopStart = bufreadFrames(ls, unit.ports[5], filled, int32(capacity))
		loopLength = bufreadFrames(int32(r.LoopLength), unit.ports[6], filled, int32(capacity))
		loopEnd = loopStart + loopLength
		fade = min(int32(r.Fade), loopStart, loopLength)
		if loopLength > 0 {
			if pos >= loopEnd {
				pos = loopStart + (pos-loopStart)%loopLength
			} else if inLoop && pos < loopStart { // backwards past the loop start
				pos = loopEnd - 1 - (loopStart-1-pos)%loopLength
			}
			inLoop = inLoop || pos >= loopStart
			next = pos + 1
			if next >= loopEnd {
				next = loopStart
			}
		}
	}
	sample := func(frame int32, channel int) float32 {
		if frame < 0 || frame >= int32(capacity) {
			return 0
		}
		abs := (base + uint32(frame)) % capacity
		if (abs+capacity-oldest)%capacity >= uint32(filled) {
			return 0
		}
		a := buf.audio
		return a.Data[int(abs)*a.Channels+min(channel, a.Channels-1)]
	}
	// fade out near the edges of the valid frames
	if r.EdgeFade > 0 && pos >= 0 && pos < int32(capacity) {
		if rel := ((base+uint32(pos))%capacity + capacity - oldest) % capacity; rel < uint32(filled) {
			d := min(rel, uint32(filled)-1-rel)
			gain = float32(gain * min(float32(d)/float32(r.EdgeFade), 1))
		}
	}
	read := func(channel int) float32 {
		// the explicit conversions round the products, so that the compiler
		// does not fuse them into multiply-adds, which the wasm player does
		// not do
		a, b := sample(pos, channel), sample(next, channel)
		v := a + float32((b-a)*frac)
		if loopLength > 0 && fade > 0 && pos >= loopEnd-fade {
			// crossfade to the frames before the loop start, which the
			// loop start continues
			j := pos - loopLength
			c, d := sample(j, channel), sample(j+1, channel)
			w := (float32(pos-(loopEnd-fade)) + frac) / float32(fade)
			v += float32((c + float32((d-c)*frac) - v) * w)
		}
		return v * gain
	}
	if stereo {
		*stack = append(*stack, read(1), read(0)) // the left channel is on top
	} else if buf.audio.Channels == 2 {
		*stack = append(*stack, (read(0)+read(1))*0.5)
	} else {
		*stack = append(*stack, read(0))
	}
	semitones := float32(64*(float32(transpose*2)-1)) + (float32(detune*2) - 1)
	if r.Flags&BufferRegionNoteTracking != 0 {
		semitones += float32(voice.note) - 60
	}
	// computed like the wasm player, which uses JavaScript's Math.pow
	frac += float32((float32(speed*2) - 1) * exp2f(semitones/12))
	whole := float32(math.Floor(float64(frac)))
	unit.state[0] = math.Float32frombits(uint32(pos + int32(whole)))
	unit.state[1] = frac - whole
	unit.state[2] = math.Float32frombits(base)
	unit.state[3] = math.Float32frombits(1)
	if inLoop {
		unit.state[4] = math.Float32frombits(1)
	}
}

// spawn implements the spawn unit. unit.state[0] is the time until the next
// spawn in rate and sync modes, in frames, and unit.state[1] the previous
// input in edge mode. Matches $su_op_spawn in the wasm player.
func (s *GoSynth) spawn(unit *unit, own *voice, first, count int, flags byte, rate, transpose, length float32, stack *[]float32) {
	for i := first; i < first+count; i++ { // release the notes that have lasted their length
		if v := &s.state.voices[i]; v.release != 0 && s.state.globalTime >= v.release {
			v.sustain, v.release, v.released = false, 0, s.state.globalTime
		}
	}
	held := own.note != 0 && own.sustain
	fire := false
	if flags&1 != 0 { // edge mode
		l := len(*stack)
		in := (*stack)[l-1]
		*stack = (*stack)[:l-1]
		fire = held && unit.state[1] <= 0 && in > 0
		unit.state[1] = in
	} else if held {
		// counting down whole frames is exact, so spawns do not drift; the
		// period is read at each spawn
		if unit.state[0] <= 0 {
			fire = true
			// computed like the wasm player, which uses JavaScript's Math.pow
			var hz float32
			if flags&32 != 0 { // sync: spawns per beat
				hz = float32(exp2f(float32(rate*16)-8) * (float32(s.bytecode.BPM) / 60))
			} else {
				hz = exp2f(float32(rate*16) - 5)
			}
			unit.state[0] += 44100 / hz
		}
		unit.state[0] -= 1
	}
	nargs := int(flags>>2) & 7
	l := len(*stack)
	args := (*stack)[l-nargs:]
	*stack = (*stack)[:l-nargs]
	if !fire || count == 0 {
		return
	}
	base := float32(60)
	if flags&2 != 0 {
		base = float32(own.note)
	}
	n := base + float32((float32(transpose*2)-1)*64) + 0.5 // no multiply-adds, like wasm
	n = max(min(n, 127), 1)
	// the released voice spawned longest ago, or the held one with steal
	target, busy := -1, -1
	for i := first; i < first+count; i++ {
		v := &s.state.voices[i]
		if !v.sustain {
			if target < 0 || v.spawned < s.state.voices[target].spawned {
				target = i
			}
		} else if busy < 0 || v.spawned < s.state.voices[busy].spawned {
			busy = i
		}
	}
	if target < 0 {
		if flags&64 == 0 {
			return // all voices held
		}
		target = busy
	}
	v := &s.state.voices[target]
	*v = voice{note: byte(math.Floor(float64(n))), sustain: true, spawned: s.state.globalTime + 1}
	if length > 0 {
		v.length = uint32(max(lengthFrames(length), 1))
		v.release = s.state.globalTime + v.length
	}
	copy(v.args[:], args)
}

// lengthFrames returns the length in frames of a spawn or window unit, like
// sointu.LengthFrames, computed like the wasm player.
func lengthFrames(length float32) float32 {
	return floor32(4410 * exp2f(float32(length*16)-8))
}

// window implements the window unit. unit.state[0] is the number of frames
// since the note was triggered, as bits. Matches $su_op_window in the wasm
// player.
func window(unit *unit, voice *voice, length, shape float32) float32 {
	if voice.note == 0 {
		return 0
	}
	age := math.Float32bits(unit.state[0])
	unit.state[0] = math.Float32frombits(age + 1)
	frames := float32(voice.length) // the length of the spawned note
	if length > 0 {
		frames = max(lengthFrames(length), 1)
	}
	if frames == 0 {
		return 1 // a note without a length
	}
	t := float32(age) / frames
	if t >= 1 {
		return 0
	}
	half := min(max(shape, 0), 1) * 0.5 // the rising and falling part, each
	d := min(t, 1-t)
	if d >= half {
		return 1
	}
	x := d / half
	return float32(x*x) * (3 - float32(2*x)) // no multiply-adds, like wasm
}

// bufwrite pops a frame from the stack and writes it to a writable buffer:
// every frame, wrapping around, or with one shot while the note is held, from
// the beginning when the note starts. Writers writing the same buffer in the
// same frame, e.g. the voices of a polyphonic instrument, mix: the first one
// writes the frame and advances the head, and the others add to the frame.
// unit.state[0] is 1 (as bits) once the note has started a one shot recording.
// Matches $su_op_bufwrite in the wasm player.
func (s *GoSynth) bufwrite(unit *unit, voice *voice, r BufferRegion, feedback float32, stereo bool, stack *[]float32) {
	l := len(*stack)
	left, right := (*stack)[l-1], (*stack)[l-1] // the left channel is on top
	if stereo {
		right = (*stack)[l-2]
	}
	if r.Flags&BufferRegionNoPop == 0 {
		if stereo {
			*stack = (*stack)[:l-2]
		} else {
			*stack = (*stack)[:l-1]
		}
	}
	buf := s.buffers[int(r.BufferID)]
	if buf == nil || !buf.audio.Writable || buf.audio.Frames() == 0 {
		return
	}
	capacity := uint32(buf.audio.Frames())
	ring := r.Flags&BufferRegionRing != 0
	if !ring { // one shot: while the note is held, from the beginning
		if voice.note == 0 || !voice.sustain {
			return
		}
		if math.Float32bits(unit.state[0]) == 0 {
			unit.state[0] = math.Float32frombits(1)
			buf.head, buf.filled, buf.written = 0, 0, 0
		}
	}
	a := buf.audio
	now := s.state.globalTime + 1
	if buf.written == now { // another writer wrote this frame already: mix
		i := int((buf.head+capacity-1)%capacity) * a.Channels
		if a.Channels == 2 {
			a.Data[i] += left
			a.Data[i+1] += right
		} else {
			a.Data[i] += float32((left + right) * 0.5)
		}
		return
	}
	if buf.head >= capacity {
		return // a recording that reached the end
	}
	i := int(buf.head) * a.Channels
	// no multiply-adds, like wasm; without feedback, the old frame is not
	// read at all, so that e.g. a NaN in it does not stay forever
	if a.Channels == 2 {
		if feedback != 0 {
			left += float32(a.Data[i] * feedback)
			right += float32(a.Data[i+1] * feedback)
		}
		a.Data[i], a.Data[i+1] = left, right
	} else {
		v := float32((left + right) * 0.5)
		if feedback != 0 {
			v += float32(a.Data[i] * feedback)
		}
		a.Data[i] = v
	}
	buf.written = now
	buf.head++
	if ring {
		buf.head %= capacity
		buf.filled = min(buf.filled+1, capacity)
	} else {
		buf.filled = buf.head
	}
}

// bufreadFrames returns a position of a bufread unit: frames shifted by the
// modulation times the filled length of the buffer, clamped to the capacity.
func bufreadFrames(frames int32, modulation float32, filled, capacity int32) int32 {
	offset := float32(math.Floor(float64(modulation * float32(filled))))
	offset = max(min(offset, 1<<30), -(1 << 30))
	return int32(max(min(int64(frames)+int64(offset), int64(capacity)), 0))
}

func (s *synthState) rand() float32 {
	s.randSeed *= 16007
	return float32(int32(s.randSeed)) / -2147483648.0
}

func nonLinearMap(value float32) float32 {
	return exp2f(-24 * value)
}

func clip(value float32) float32 {
	if value < -1 {
		return -1
	}
	if value > 1 {
		return 1
	}
	return value
}

// crush rounds halves to even, like f32.nearest of the wasm player and the
// x87 of the x86 players.
func crush(value, amount float32) float32 {
	n := nonLinearMap(amount)
	return float32(math.RoundToEven(float64(value/n)) * float64(n))
}

// waveshape is the waveshaper of the wasm and x86 players, operation by
// operation: value·(amount/(1+((2·amount-1)·|value|-amount))), value clipped
// to [-1, 1] first.
func waveshape(value, amount float32) float32 {
	value = clip(value)
	absVal := value
	if absVal < 0 {
		absVal = -absVal
	}
	return value * (amount / (1 + (float32((amount+amount-1)*absVal) - amount)))
}

// floor32 returns the largest integer at most x.
func floor32(x float32) float32 { return float32(math.Floor(float64(x))) }

func abs32(x float32) float32 { return math.Float32frombits(math.Float32bits(x) &^ (1 << 31)) }

// minBandlimitDt is the smallest phase advance the bandlimited oscillators
// use, which keeps their corrections finite when the oscillator stops.
const minBandlimitDt = 9.5367431640625e-7 // 2^-20

// The oscillator waveforms, as in the wasm player. With dt > 0, the phase
// advance of the sample, they are bandlimited: polyBLEP smooths the jumps of
// the pulse, polyBLAMP the corners of the trisaw and of the sine with color
// < 1. Their color is then kept where the corrections stay bounded: at least
// dt from 0 and 1 for the trisaw (a saw's jump becomes a ramp of one
// sample), at least dt and at most 1 for the sine, from 0 to 1 for the pulse.

func oscillatorSine(phase, color, dt float32) float32 {
	var h float32
	if dt > 0 {
		color = min(max(color, dt), 1)
		// the slope changes by ±2π/color at 0 and color
		h = float32(float32(dt*1.0471976)/color) * float32(polyBLAMP(phase, dt)-polyBLAMP(wrap(phase-color), dt))
	}
	var amplitude float32
	if phase < color {
		amplitude = sinTurns(phase / color)
	}
	return amplitude + h
}

func oscillatorTrisaw(phase, color, dt float32) float32 {
	var h float32
	if dt > 0 {
		color = min(max(color, dt), 1-dt)
		// the slope changes by ±2/(color·(1-color)) at 0 and color
		h = float32(float32(dt*0.33333334)/float32(color*(1-color))) * float32(polyBLAMP(phase, dt)-polyBLAMP(wrap(phase-color), dt))
	}
	if phase >= color {
		phase = 1 - phase
		color = 1 - color
	}
	return float32(float32(float32(phase/color)*2)-1) + h
}

func oscillatorPulse(phase, color, dt float32) float32 {
	var amplitude float32 = 1
	if dt > 0 {
		color = min(max(color, 0), 1)
	}
	if phase >= color {
		amplitude = -1
	}
	if dt > 0 {
		amplitude = (amplitude + polyBLEP(phase, dt)) - polyBLEP(wrap(phase-color), dt)
	}
	return amplitude
}

func wrap(x float32) float32 { return x - floor32(x) }

// polyWindow is 1 - |d|/dt within dt of a discontinuity at phase 0, where d
// is the distance to it, and 0 further away.
func polyWindow(t, dt float32) float32 { return max(1-min(t, 1-t)/dt, 0) }

// polyBLEP is the correction for a step of +2 at phase 0: -(1-d/dt)² after
// it and (1-d/dt)² before.
func polyBLEP(t, dt float32) float32 {
	y := polyWindow(t, dt)
	if t < 0.5 {
		return -float32(y * y)
	}
	return float32(y * y)
}

// polyBLAMP is 6/dt times the correction for a corner at phase 0 where the
// slope increases by 1: (1-|d|/dt)³.
func polyBLAMP(t, dt float32) float32 {
	y := polyWindow(t, dt)
	return float32(float32(y*y) * y)
}

// unitFloat returns the kth float of the unit: its state, then its ports.
func unitFloat(u *unit, k int) *float32 {
	if k < len(u.state) {
		return &u.state[k]
	}
	return &u.ports[k-len(u.state)]
}

// sampleOscillator is the oscillator playing a sample of gm.dls, as in the x86
// players, with the phase in float64 like their 80-bit x87 math.
func (s *GoSynth) sampleOscillator(unit *unit, voice *voice, flags byte, operandsAtTransform []byte, params *[8]float32, channels int, stack *[]float32) {
	detuneStereo := params[1]*2 - 1
	unison := flags & 3
	for i := 0; i < channels; i++ {
		detune := detuneStereo
		var output float32
		for j := byte(0); j <= unison; j++ {
			statevar := &unit.state[byte(i)+j*2]
			pitch := float64(64*(params[0]*2-1) + detune)
			if flags&0x8 == 0 {
				pitch += float64(voice.note)
			}
			pitch *= 0.083333333333
			omega := math.Exp2(pitch)
			if flags&0x8 == 0 {
				omega *= 0.000092696138
			} else {
				omega *= 0.000038
			}
			omega += float64(unit.ports[6])
			phase := float64(*statevar) + omega
			*statevar = float32(phase)
			phase += float64(params[2])
			sampleno := operandsAtTransform[3] // reuse color as the sample number
			sampleoffset := s.bytecode.SampleOffsets[sampleno]
			sampleindex := int(phase*84.28074964676522 + 0.5)
			loopstart := int(sampleoffset.LoopStart)
			if sampleindex >= loopstart {
				sampleindex -= loopstart
				sampleindex %= int(sampleoffset.LoopLength)
				sampleindex += loopstart
			}
			sampleindex += int(sampleoffset.Start)
			amplitude := float32(int16(binary.LittleEndian.Uint16(su_sample_table[sampleindex*2:]))) / 32767.0
			output += waveshape(amplitude, params[4]) * params[5]
			if j < unison {
				params[2] += 0.08333333
			}
			detune = -detune * 0.5
		}
		*stack = append(*stack, output)
		detuneStereo = -detuneStereo
	}
}
