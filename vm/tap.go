package vm

import "github.com/vsariola/sointu"

// tap is a place where the GoSynth records the signal: see sointu.Tapper.
type tap struct {
	point sointu.TapPoint
	// the voices and the unit, among the units that the voices run, before
	// which the signal is taken; the voices are none if the patch has no
	// such place
	firstVoice, endVoice, slot int
	sum                        [2]float32 // of the voices, in the frame being rendered
	frames                     sointu.AudioBuffer
}

// maxTapFrames is how many frames a tap keeps if nobody takes them.
const maxTapFrames = 1 << 16

// SetTaps implements sointu.Tapper.
func (s *GoSynth) SetTaps(points []sointu.TapPoint) {
	if len(points) == 0 {
		s.taps = nil
		return
	}
	taps := make([]tap, len(points))
	for i, p := range points {
		taps[i].point = p
		// a tap that goes on keeps what it has recorded
		for j := range s.taps {
			if s.taps[j].point == p && s.taps[j].frames != nil {
				taps[i].frames, s.taps[j].frames = s.taps[j].frames, nil
				break
			}
		}
	}
	s.taps = taps
	s.resolveTaps()
}

// resolveTaps finds the voices and the unit of each tap in the patch.
func (s *GoSynth) resolveTaps() {
	for i := range s.taps {
		t := &s.taps[i]
		t.firstVoice, t.endVoice, t.slot = 0, 0, 0
		p := t.point
		if p.Instrument < 0 || p.Instrument >= len(s.layout) {
			continue
		}
		first := 0
		for _, instr := range s.layout[:p.Instrument] {
			first += instr.voices
		}
		instr := s.layout[p.Instrument]
		if p.Unit < 0 || p.Unit >= len(instr.slots) || p.Voice > instr.voices {
			continue
		}
		t.slot = instr.slots[p.Unit]
		t.firstVoice, t.endVoice = first, first+instr.voices
		if p.Voice > 0 {
			t.firstVoice, t.endVoice = first+p.Voice-1, first+p.Voice
		}
	}
}

// tapUnit is called before a voice runs a unit, while there are taps: the
// taps at that place take the two signals on top of the stack.
func (s *GoSynth) tapUnit(voice, slot int, stack []float32) {
	for i := range s.taps {
		if t := &s.taps[i]; slot == t.slot && voice >= t.firstVoice && voice < t.endVoice {
			l := len(stack) // at least 4: see Render
			t.sum[0] += stack[l-1]
			t.sum[1] += stack[l-2]
		}
	}
}

// tapFrame is called after every frame, while there are taps.
func (s *GoSynth) tapFrame() {
	for i := range s.taps {
		t := &s.taps[i]
		if len(t.frames) >= maxTapFrames {
			t.frames = t.frames[:0] // nobody takes them
		}
		t.frames = append(t.frames, t.sum)
		t.sum = [2]float32{}
	}
}

// Tapped implements sointu.Tapper.
func (s *GoSynth) Tapped(i int, dst sointu.AudioBuffer) sointu.AudioBuffer {
	if i < 0 || i >= len(s.taps) {
		return dst
	}
	t := &s.taps[i]
	dst = append(dst, t.frames...)
	t.frames = t.frames[:0]
	return dst
}

// multithreadTap is a tap of a MultithreadSynth: the synth that has the
// instrument, and the number of the tap there, or -1.
type multithreadTap struct{ synth, tap int }

// SetTaps implements sointu.Tapper: each tap is that of the first synth
// whose thread runs the instrument.
func (s *MultithreadSynth) SetTaps(points []sointu.TapPoint) {
	s.tapPoints = append(s.tapPoints[:0], points...)
	s.setTaps()
}

func (s *MultithreadSynth) setTaps() {
	s.taps = s.taps[:0]
	perSynth := make([][]sointu.TapPoint, len(s.synths))
	for _, p := range s.tapPoints {
		t := multithreadTap{-1, -1}
		if p.Instrument >= 0 && p.Instrument < len(s.instruments) {
			if at := s.instruments[p.Instrument]; at[0] >= 0 && at[0] < len(s.synths) {
				t = multithreadTap{at[0], len(perSynth[at[0]])}
				p.Instrument = at[1]
				perSynth[at[0]] = append(perSynth[at[0]], p)
			}
		}
		s.taps = append(s.taps, t)
	}
	for i, synth := range s.synths {
		if t, ok := synth.(sointu.Tapper); ok {
			t.SetTaps(perSynth[i])
		}
	}
}

// Tapped implements sointu.Tapper.
func (s *MultithreadSynth) Tapped(i int, dst sointu.AudioBuffer) sointu.AudioBuffer {
	if i < 0 || i >= len(s.taps) || s.taps[i].synth < 0 || s.taps[i].synth >= len(s.synths) {
		return dst
	}
	if t, ok := s.synths[s.taps[i].synth].(sointu.Tapper); ok {
		return t.Tapped(s.taps[i].tap, dst)
	}
	return dst
}

// threadInstruments returns, for each instrument of a patch, the first
// thread that runs it and its index among the instruments of that thread,
// as splitPatchByCores splits them, or -1.
func threadInstruments(patch sointu.Patch) [][2]int {
	ret := make([][2]int, len(patch))
	var count [MAX_THREADS]int
	for i, instr := range patch {
		ret[i] = [2]int{-1, -1}
		for c := range MAX_THREADS {
			if (instr.ThreadMaskM1+1)&(1<<c) != 0 {
				if ret[i][0] < 0 {
					ret[i] = [2]int{c, count[c]}
				}
				count[c]++
			}
		}
	}
	return ret
}
