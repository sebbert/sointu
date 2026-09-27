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
		buffers    map[int]*synthBuffer
		cpuLoad    sointu.CPULoad
	}

	// GoSynther is a Synther implementation that can converts patches into
	// GoSynths.
	GoSynther struct {
	}
)

const MAX_VOICES = 32
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
	ret := &GoSynth{bytecode: *bytecode, stack: make([]float32, 0, 4), delaylines: make([]delayline, patch.NumDelayLines())}
	ret.state.randSeed = 1
	return ret, nil
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
	needsRefresh := len(bytecode.Opcodes) != len(s.bytecode.Opcodes)
	if !needsRefresh {
		for i, c := range bytecode.Opcodes {
			if s.bytecode.Opcodes[i] != c {
				needsRefresh = true
				break
			}
		}
	}
	s.bytecode = *bytecode
	for len(s.delaylines) < patch.NumDelayLines() {
		s.delaylines = append(s.delaylines, delayline{})
	}
	if needsRefresh {
		for i := range s.state.voices {
			for j := range s.state.voices[i].units {
				s.state.voices[i].units[j] = unit{}
			}
		}
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
				if mask := uint32(1) << uint32(voicesRemaining); s.bytecode.PolyphonyBitmask&mask == mask {
					opcodes, operands = opcodesInstr, operandsInstr
				} else {
					opcodesInstr, operandsInstr = opcodes, operands
				}
				continue
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
					synth.outputs[0] += params[0] * stack[l-1]
					synth.outputs[1] += params[0] * stack[l-2]
					stack = stack[:l-2]
				} else {
					synth.outputs[0] += params[0] * stack[l-1]
					stack = stack[:l-1]
				}
			case opOutaux:
				if stereo {
					synth.outputs[0] += params[0] * stack[l-1]
					synth.outputs[1] += params[0] * stack[l-2]
					synth.outputs[2] += params[1] * stack[l-1]
					synth.outputs[3] += params[1] * stack[l-2]
					stack = stack[:l-2]
				} else {
					synth.outputs[0] += params[0] * stack[l-1]
					synth.outputs[2] += params[1] * stack[l-1]
					stack = stack[:l-1]
				}
			case opAux:
				var channel byte
				channel, operands = operands[0], operands[1:]
				if stereo {
					synth.outputs[channel+1] += params[0] * stack[l-2]
				}
				synth.outputs[channel] += params[0] * stack[l-1]
				stack = stack[:l-channels]
			case opSpeed:
				r := unit.state[0] + float32(math.Exp2(float64(stack[l-1]*2.206896551724138))-1)
				w := int(r+1.5) - 1
				unit.state[0] = r - float32(w)
				renderTime += w
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
					unit.state[0] = envStateRelease // set state to release
				}
				state := unit.state[0]
				level := unit.state[1]
				switch state {
				case envStateAttack:
					level += nonLinearMap(params[0])
					if level >= 1 {
						level = 1
						state = envStateDecay
					}
				case envStateDecay:
					level -= nonLinearMap(params[1])
					if sustain := params[2]; level <= sustain {
						level = sustain
					}
				case envStateRelease:
					level -= nonLinearMap(params[3])
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
				gain := float32(math.Pow(2, float64(params[0]*2-1)*6.643856189774724))
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
				var addrLow, addrHigh byte
				addrLow, addrHigh, operands = operands[0], operands[1], operands[2:]
				addr := (uint16(addrHigh) << 8) + uint16(addrLow)
				targetVoice := voice
				if addr&0x8000 == 0x8000 {
					addr -= 0x8010
					targetVoice = &synth.voices[addr>>10]
				}
				unitIndex := ((addr & 0x01F0) >> 4) - 1
				port := addr & 7
				amount := params[0]*2 - 1
				for i := 0; i < channels; i++ {
					targetVoice.units[unitIndex].ports[int(port)+i] += stack[l-1-i] * amount
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
					low += freq2 * band
					high := stack[l-1-i] - low - res*band
					band += freq2 * high
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
				detuneStereo := params[1]*2 - 1
				unison := flags & 3
				for i := 0; i < channels; i++ {
					detune := detuneStereo
					var output float32
					for j := byte(0); j <= unison; j++ {
						statevar := &unit.state[byte(i)+j*2]
						pitch := float64(64*(params[0]*2-1) + detune)
						if flags&0x8 == 0 { // if lfo is disable, add note to oscillator transpose
							pitch += float64(voice.note)
						}
						pitch *= 0.083333333333 // from semitones to octaves
						omega := math.Exp2(pitch)
						if flags&0x8 == 0 {
							omega *= 0.000092696138 // scaling coefficient to get middle-C where it should be
						} else {
							omega *= 0.000038 //  pretty random scaling constant to get LFOs into reasonable range. Historical reasons, goes all the way back to 4klang
						}
						omega += float64(unit.ports[6]) // add frequency modulation
						var amplitude float32
						phase := float64(*statevar) + omega
						if flags&0x80 == 0x80 { // if this is a sample oscillator
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
							amplitude = float32(int16(binary.LittleEndian.Uint16(su_sample_table[sampleindex*2:]))) / 32767.0
						} else {
							// at this point, the native synth actually uses 80-bit precision, so emulate that as closely as possible by using 64-bit math here
							phase += 1
							phase -= float64(int(phase))
							*statevar = float32(phase)
							phase += float64(params[2])
							phase += 1
							phase -= float64(int(phase)) // this should guaranteee that phase is [0,1), so that the Trisaw should not nan even if color = 1
							color := float64(params[3])
							switch {
							case flags&0x40 == 0x40: // Sine
								if phase < color {
									amplitude = float32(math.Sin(2 * math.Pi * phase / color))
								}
							case flags&0x20 == 0x20: // Trisaw
								if phase >= color { // since phase cannot be 1, if color = 1, then this condition never fires
									phase = 1 - phase
									color = 1 - color
								}
								amplitude = float32(phase/color*2 - 1)
							case flags&0x10 == 0x10: // Pulse
								if phase >= color {
									amplitude = -1
								} else {
									amplitude = 1
								}
							case flags&0x4 == 0x4: // Gate
								maskLow, maskHigh := operandsAtTransform[3], operandsAtTransform[4]
								gateBits := (int(maskHigh) << 8) + int(maskLow)
								amplitude = float32((gateBits >> (int(phase*16+.5) & 15)) & 1)
								g := unit.state[4+i] // warning: still fucks up with unison = 3
								amplitude += 0.99609375 * (g - amplitude)
								unit.state[4+i] = amplitude
							}
						}
						if flags&0x4 == 0 {
							output += waveshape(amplitude, params[4]) * params[5]
						} else {
							output += amplitude * params[5]
						}
						if j < unison {
							params[2] += 0.08333333 // 1/12, add small phase shift so all oscillators don't start in phase
						}
						detune = -detune * 0.5
					}
					stack = append(stack, output)
					detuneStereo = -detuneStereo
				}
				unit.ports[6] = 0
			case opBufwrite:
				index := operands[0]
				operands = operands[1:]
				s.bufwrite(unit, voice, s.bytecode.BufferRegions[index], params[0], stereo, &stack)
			case opSpawn:
				first, count, flags := int(operands[0]), int(operands[1]), operands[2]
				operands = operands[3:]
				s.spawn(unit, voice, first, count, flags, params[0], params[1], params[2], &stack)
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
					output := params[1] * signal // dry output
					for j := byte(0); j < count; j += 2 {
						d, delaylines = &delaylines[0], delaylines[1:]
						delay := float32(s.bytecode.DelayTimes[index]) + unit.ports[4]*32767
						if count&1 == 0 {
							delay /= float32(math.Exp2(float64(voice.note) * 0.083333333333))
						}
						delSignal := d.buffer[t-uint16(delay+0.5)]
						output += delSignal
						d.dampState = damp*d.dampState + (1-damp)*delSignal
						d.buffer[t] = feedback*d.dampState + pregain2*signal
						index++
					}
					d.dcFiltState = output + (0.99609375*d.dcFiltState - d.dcIn)
					d.dcIn = output
					stack[stackIndex] = d.dcFiltState
					stackIndex++
				}
				unit.ports[4] = 0
			case opCompressor:
				signalLevel := stack[l-1] * stack[l-1] // square the signal to get power
				if stereo {
					signalLevel += stack[l-2] * stack[l-2]
				}
				currentLevel := unit.state[0]
				paramIndex := 0 // compressor attacking
				if signalLevel < currentLevel {
					paramIndex = 1 // compressor releasing
				}
				alpha := nonLinearMap(params[paramIndex]) // map attack or release to a smoothing coefficient
				currentLevel += (signalLevel - currentLevel) * alpha
				unit.state[0] = currentLevel
				var gain float32 = 1
				if threshold2 := params[3] * params[3]; currentLevel > threshold2 {
					gain = float32(math.Pow(float64(threshold2/currentLevel), float64(params[4]/2)))
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
				omega0 := 2 * params[0] * params[0]                                // square the omega to have a bit more values mapping to bass frequencies
				alpha := float32(math.Sin(float64(omega0))) * 2 * params[1]        // Q=1/(4*(p/128)) gives a range of Q = 0.25 ... 32
				A := float32(math.Pow(2, float64(params[2]-.5)*6.643856189774724)) // +-40 dB, reusing same constant as dbgain unit
				u, v := alpha*A, alpha/A
				b0, b1, b2 := 1+u, -2*float32(math.Cos(float64(omega0))), 1-u
				a0, a1, a2 := 1+v, b1, 1-v
				for i := range channels { // biquad filter in transposed direct from II (https://en.wikipedia.org/wiki/Digital_biquad_filter)
					x := stack[l-1-i]
					y := (b0*x + unit.state[i]) / a0 // the biquad was not in normalized form, so we need to divide by a0
					unit.state[i] = b1*x - a1*y + unit.state[2+i]
					unit.state[2+i] = b2*x - a2*y
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
	frac += float32((float32(speed*2) - 1) * float32(math.Pow(2, float64(semitones/12))))
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
				hz = float32(float32(math.Pow(2, float64(float32(rate*16)-8))) * (float32(s.bytecode.BPM) / 60))
			} else {
				hz = float32(math.Pow(2, float64(float32(rate*16)-5)))
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
	return float32(math.Floor(float64(4410 * float32(math.Pow(2, float64(float32(length*16)-8))))))
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
	return float32(math.Exp2(float64(-24 * value)))
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

func crush(value, amount float32) float32 {
	n := nonLinearMap(amount)
	return float32(math.Round(float64(value/n)) * float64(n))
}

func waveshape(value, amount float32) float32 {
	absVal := value
	if absVal < 0 {
		absVal = -absVal
	}
	return value * amount / (1 - amount + (2*amount-1)*absVal)
}
