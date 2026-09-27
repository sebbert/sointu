package vm

import (
	"maps"
	"math"
	"math/bits"
	"runtime"
	"sync"

	"github.com/vsariola/sointu"
)

type (
	MultithreadSynth struct {
		voiceMapping voiceMapping
		synths       []sointu.Synth
		commands     chan<- multithreadSynthCommand // maxtime
		results      <-chan multithreadSynthResult  // rendered buffer
		pool         sync.Pool
		synther      sointu.Synther
		buffers      map[int]sointu.BufferAudio
	}

	MultithreadSynther struct {
		synther sointu.Synther
		name    string
	}

	voiceMapping [MAX_THREADS][MAX_VOICES]int

	multithreadSynthCommand struct {
		thread  int
		samples int
		time    int
	}

	multithreadSynthResult struct {
		buffer      *sointu.AudioBuffer
		samples     int
		time        int
		renderError error
	}
)

const MAX_THREADS = 4

func MakeMultithreadSynther(synther sointu.Synther) MultithreadSynther {
	return MultithreadSynther{synther: synther, name: "Multithread " + synther.Name()}
}

func (s MultithreadSynther) Name() string                 { return s.name }
func (s MultithreadSynther) SupportsMultithreading() bool { return true }

func (s MultithreadSynther) Synth(patch sointu.Patch, bpm int) (sointu.Synth, error) {
	patches, voiceMapping := splitPatchByCores(patch)
	synths := make([]sointu.Synth, 0, len(patches))
	for _, p := range patches {
		synth, err := s.synther.Synth(p, bpm)
		if err != nil {
			return nil, err
		}
		synths = append(synths, synth)
	}
	ret := &MultithreadSynth{
		synths:       synths,
		voiceMapping: voiceMapping,
		pool:         sync.Pool{New: func() any { ret := make(sointu.AudioBuffer, 0, 8096); return &ret }},
	}
	ret.startProcesses()
	ret.synther = s.synther
	return ret, nil
}

func (s *MultithreadSynth) Update(patch sointu.Patch, bpm int) error {
	patches, voiceMapping := splitPatchByCores(patch)
	if s.voiceMapping != voiceMapping {
		s.voiceMapping = voiceMapping
		if s.buffers != nil {
			maps.Copy(s.buffers, s.WrittenBuffers()) // the new synths continue writing
		}
		s.closeSynths()
	}
	for i, p := range patches {
		if len(s.synths) <= i {
			synth, err := s.synther.Synth(p, bpm)
			if err != nil {
				s.closeSynths()
				return err
			}
			if b, ok := synth.(sointu.BufferSetter); ok && s.buffers != nil {
				b.SetBuffers(s.buffers)
			}
			s.synths = append(s.synths, synth)
		} else {
			if err := s.synths[i].Update(p, bpm); err != nil {
				s.closeSynths()
				return err
			}
		}
	}
	return nil
}

func (s *MultithreadSynth) startProcesses() {
	maxProcs := runtime.GOMAXPROCS(0)
	cmdChan := make(chan multithreadSynthCommand, maxProcs)
	s.commands = cmdChan
	resultsChan := make(chan multithreadSynthResult, maxProcs)
	s.results = resultsChan
	for i := 0; i < maxProcs; i++ {
		go func(commandCh <-chan multithreadSynthCommand, resultCh chan<- multithreadSynthResult) {
			for cmd := range commandCh {
				buffer := s.pool.Get().(*sointu.AudioBuffer)
				*buffer = append(*buffer, make(sointu.AudioBuffer, cmd.samples)...)
				samples, time, renderError := s.synths[cmd.thread].Render(*buffer, cmd.time)
				resultCh <- multithreadSynthResult{buffer: buffer, samples: samples, time: time, renderError: renderError}
			}
		}(cmdChan, resultsChan)
	}
}

func (s *MultithreadSynth) Close() {
	close(s.commands)
	s.closeSynths()
}

func (s *MultithreadSynth) closeSynths() {
	for _, synth := range s.synths {
		synth.Close()
	}
	s.synths = s.synths[:0]
}

// SetBuffers sets the buffers of the synths of each thread.
func (s *MultithreadSynth) SetBuffers(buffers map[int]sointu.BufferAudio) {
	s.buffers = maps.Clone(buffers)
	for _, synth := range s.synths {
		if b, ok := synth.(sointu.BufferSetter); ok {
			b.SetBuffers(buffers)
		}
	}
}

// WrittenBuffers returns the writable buffers with what has been written to
// them. The synth of each thread keeps its own state of the buffers, and only
// the thread writing to a buffer changes it, so the state that differs from
// the one set is taken.
func (s *MultithreadSynth) WrittenBuffers() map[int]sointu.BufferAudio {
	ret := map[int]sointu.BufferAudio{}
	for _, synth := range s.synths {
		w, ok := synth.(sointu.BufferWriter)
		if !ok {
			continue
		}
		for id, b := range w.WrittenBuffers() {
			set := s.buffers[id]
			if _, ok := ret[id]; !ok || b.Head != set.Head || b.Filled != set.Filled {
				ret[id] = b
			}
		}
	}
	return ret
}

func (s *MultithreadSynth) Playheads(dst []sointu.Playhead) []sointu.Playhead {
	for _, synth := range s.synths {
		if p, ok := synth.(sointu.PlayheadReporter); ok {
			dst = p.Playheads(dst)
		}
	}
	return dst
}

func (s *MultithreadSynth) Trigger(voiceIndex int, note byte) {
	for i, synth := range s.synths {
		if ind := s.voiceMapping[i][voiceIndex]; ind >= 0 {
			synth.Trigger(ind, note)
		}
	}
}

func (s *MultithreadSynth) Release(voiceIndex int) {
	for i, synth := range s.synths {
		if ind := s.voiceMapping[i][voiceIndex]; ind >= 0 {
			synth.Release(ind)
		}
	}
}

func (s *MultithreadSynth) CPULoad(loads []sointu.CPULoad) (elems int) {
	for _, synth := range s.synths {
		n := synth.CPULoad(loads)
		elems += n
		loads = loads[n:]
		if len(loads) <= 0 {
			return
		}
	}
	return
}

func (s *MultithreadSynth) Render(buffer sointu.AudioBuffer, maxtime int) (samples int, time int, renderError error) {
	count := len(s.synths)
	for i := 0; i < count; i++ {
		s.commands <- multithreadSynthCommand{thread: i, samples: len(buffer), time: maxtime}
	}
	clear(buffer)
	samples = math.MaxInt
	time = math.MaxInt
	for i := 0; i < count; i++ {
		// We mix the results as they come, but the order doesn't matter. This
		// leads to slight indeterminism in the results, because the order of
		// floating point additions can change the least significant bits.
		result := <-s.results
		if result.renderError != nil && renderError == nil {
			renderError = result.renderError
		}
		samples = min(samples, result.samples)
		time = min(time, result.time)
		for j := 0; j < samples; j++ {
			buffer[j][0] += (*result.buffer)[j][0]
			buffer[j][1] += (*result.buffer)[j][1]
		}
		*result.buffer = (*result.buffer)[:0]
		s.pool.Put(result.buffer)
	}
	return
}

func splitPatchByCores(patch sointu.Patch) ([]sointu.Patch, voiceMapping) {
	cores := 1
	for _, instr := range patch {
		cores = max(bits.Len((uint)(instr.ThreadMaskM1+1)), cores)
	}
	cores = min(cores, MAX_THREADS)
	ret := make([]sointu.Patch, cores)
	for c := 0; c < cores; c++ {
		ret[c] = make(sointu.Patch, 0, len(patch))
	}
	var voicemapping [MAX_THREADS][MAX_VOICES]int
	for c := 0; c < MAX_THREADS; c++ {
		for j := 0; j < MAX_VOICES; j++ {
			voicemapping[c][j] = -1
		}
	}
	for c := range cores {
		coreVoice := 0
		curVoice := 0
		coreIndex := make([]int, len(patch)) // instrument index in the core's patch + 1, 0 if not in it
		for i, instr := range patch {
			if (instr.ThreadMaskM1+1)&(1<<c) != 0 {
				coreIndex[i] = len(ret[c]) + 1
				ret[c] = append(ret[c], instr)
			}
		}
		remapSpawnTargets(ret[c], coreIndex)
		for _, instr := range patch {
			mask := instr.ThreadMaskM1 + 1
			if mask&(1<<c) != 0 {
				for j := 0; j < instr.NumVoices; j++ {
					if coreVoice+j >= MAX_VOICES {
						break
					}
					voicemapping[c][curVoice+j] = coreVoice + j
				}
				coreVoice += instr.NumVoices
			}
			curVoice += instr.NumVoices
		}
	}
	return ret, voicemapping
}

// remapSpawnTargets changes the targets of the spawn units in patch, which
// are instrument indices + 1, with indexMap. The instruments with spawn units
// are copied, so that the original patch is not changed. Spawn units whose
// target is not in the patch spawn nothing.
func remapSpawnTargets(patch sointu.Patch, indexMap []int) {
	for i, instr := range patch {
		copied := false
		for j, u := range instr.Units {
			if u.Type != "spawn" {
				continue
			}
			if !copied {
				patch[i] = instr.Copy()
				copied = true
			}
			target := 0
			if t := u.Parameters["instrument"] - 1; t >= 0 && t < len(indexMap) {
				target = indexMap[t]
			}
			patch[i].Units[j].Parameters["instrument"] = target
		}
	}
}
