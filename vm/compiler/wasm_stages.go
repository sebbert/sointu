package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// The stages of the wasm player split the rendering of a song over several
// instances of the player, each in its own thread. A stage runs a range of
// consecutive voices. The stages form a pipeline: for every sample, a stage
// starts from what the stages before it left in the memory cells that both
// sides of the cut use (the output and aux ports, and the ports that sends
// from earlier voices modulate), which it reads from a tape, and writes those
// of its own cut to the tape of the next stage. The last stage writes the
// audio. Each cell holds the value it has in the player that runs all voices,
// bit for bit, so the stages render exactly the same song.
//
// A cut between two voices is only possible when nothing flows back from
// the voices after it to the voices before it, as the stages before are
// ahead in time, and when the voices on both sides share nothing but cells
// that are cleared every sample.

// wasmStage is a stage: the voices [First, End), where its code, operands
// and states start, and the cells it reads from and writes to its tapes.
type wasmStage struct {
	First, End      int
	Opcodes         int    // offset of the code of its first voice in su_patch_opcodes
	Operands        int    // offset of the operands of its first voice in su_patch_operands
	DelayLines      int    // delay lines of the voices before it
	Otts            int    // ott states of the voices before it
	Limiters        int    // limiter states of the voices before it
	SeedInit        uint32 // 16007 to the number of noise samples of the voices before it
	SeedStep        uint32 // 16007 to the number of noise samples of the voices of the other stages
	InCells         []uint32
	OutCells        []uint32
	Cost            float64 // estimated share of the rendering time
	InstrumentNames string
}

// wasmStageData is the data of the stages for the wasm player template.
type wasmStageData struct {
	// Stages are the stages of the pipeline, followed by a stage with all
	// voices, which the player has selected when it starts.
	Stages []wasmStage
	// StageCells are the offsets of the taped cells from su_synth, for each
	// cut of the pipeline after each other.
	StageCells []uint32
	// StageTable has 13 i32s for each stage: the offsets of its code and
	// operands, the offset of its first voice in su_voices, the number of
	// voices remaining when it starts and when it ends, the offsets of its
	// first delay line, ott state and limiter state, the initial noise seed
	// and what the seed is multiplied with after each sample, the offsets
	// in StageCells of the cells it reads and of the cells it writes, which
	// end where those of the stage after the next start.
	StageTable []uint32
	// StageTapeCells is the largest number of cells of a cut, and
	// StageRows the most rows a call to r may render: the tapes have room
	// for them.
	StageTapeCells int
	StageRows      int
	// NumStages is the number of stages of the pipeline, 0 without stages.
	NumStages int
}

const wasmStageRecordSize = 13 * 4

// StageTapeBytes is the size of a tape: StageRows rows of StageTapeCells
// cells for each sample.
func (d *wasmStageData) StageTapeBytes(samplesPerRow int) int {
	return d.StageRows * samplesPerRow * d.StageTapeCells * 4
}

// stageUnit is a unit that runs in the players: the instrument, its number
// among the units that run and the number of operations it compiles to (a
// global send to all voices of an instrument is one for each voice).
type stageUnit struct {
	instr, unitNo, ops int
	unit               *sointu.Unit
}

// stageAccess is an access of a cell by a voice: writes add to the cell,
// clears read and zero it, and reads leave it.
type stageAccess struct {
	voice, unitNo int
	clear, read   bool
}

// unitCost is a rough cost of running a unit for a sample in the wasm
// player, in nanoseconds on a 2023 laptop, for balancing the stages: fitted
// to the times of the voices of the example songs, where the bandlimited
// oscillators (for each unison voice and channel), the ladder filters and
// the envelopes take most of the time. The spectral units are guesses.
func unitCost(u *sointu.Unit) float64 {
	p := u.Parameters
	stereo := float64(p["stereo"] & 1)
	switch u.Type {
	case "oscillator":
		c := 12.0
		if sointu.OscillatorBandlimited(*u) {
			c = 46
		}
		return c * float64(p["unison"]+1) * (1 + stereo)
	case "delay":
		return 26 + 12*float64(len(u.VarArgs))
	case "ladder":
		return 83
	case "envelope":
		return 60
	case "filter":
		return 12 + 23*stereo
	case "mcmix", "mcdelay", "mcfilter":
		return 40
	case "spfft", "spifft":
		return 120 * (1 + stereo)
	case "ott":
		return 80 * (1 + stereo)
	case "compressor", "limiter", "softclip", "bufread":
		return 20 * (1 + stereo)
	}
	if len(sointu.SpectrumBufferParams(u.Type)) > 0 {
		return 40
	}
	return 8
}

// stageUnits lists the units of the patch that run in the players, numbered
// like vm.NewBytecode numbers them.
func stageUnits(patch sointu.Patch) (units []stageUnit, byID map[int]stageUnit) {
	byID = map[int]stageUnit{}
	for i, instr := range patch {
		unitNo := 0
		for j := range instr.Units {
			u := &instr.Units[j]
			if u.Type == "" || u.Disabled {
				continue
			}
			su := stageUnit{instr: i, unitNo: unitNo, ops: 1, unit: u}
			switch u.Type {
			case "delay":
				count := len(u.VarArgs)
				if u.Parameters["stereo"] == 1 {
					count /= 2
				}
				if count == 0 {
					continue
				}
			case "send":
				if t, _, err := patch.FindUnit(u.Parameters["target"]); err == nil && !(t == i && u.Parameters["voice"] == 0) && u.Parameters["voice"] == 0 {
					su.ops = patch[t].NumVoices
				}
			}
			if u.ID != 0 {
				byID[u.ID] = su
			}
			units = append(units, su)
			unitNo += su.ops
		}
	}
	return
}

// wasmStages plans a pipeline of up to numStages stages for the song, or
// with the given cuts (numbers of the first voices of the stages after the
// first) if there are any. It returns the data for the player and a
// description of the plan. The patch is expanded: it has no module units.
func wasmStages(song *sointu.Song, features vm.FeatureSet, numStages int, cuts []int, rows int) (ret wasmStageData, report []string, err error) {
	if numStages < 2 && len(cuts) == 0 {
		return ret, nil, nil
	}
	patch := song.Patch
	numVoices := patch.NumVoices()
	units, byID := stageUnits(patch)
	firstVoice := make([]int, len(patch)+1)
	for i, instr := range patch {
		firstVoice[i+1] = firstVoice[i] + instr.NumVoices
	}
	// cells are offsets from su_synth: the 8 global ports at 32, the voices
	// at 64
	accesses := map[uint32][]stageAccess{}
	access := func(cell uint32, a stageAccess) { accesses[cell] = append(accesses[cell], a) }
	port := func(channel int) uint32 { return uint32(32 + 4*(channel&7)) }
	// forbidden[b] is why there can be no cut before voice b
	forbidden := make([]string, numVoices+1)
	bind := func(why string, lo, hi int) { // the voices lo to hi share state
		for b := lo + 1; b <= hi; b++ {
			if forbidden[b] == "" {
				forbidden[b] = why
			}
		}
	}
	type span struct{ lo, hi int }
	shared := map[string]*span{} // voices that use a buffer, a spectrum or a bus
	share := func(key string, lo, hi int) {
		if s, ok := shared[key]; ok {
			s.lo, s.hi = min(s.lo, lo), max(s.hi, hi)
		} else {
			shared[key] = &span{lo, hi}
		}
	}
	noise := make([]int, len(patch))      // noise samples of a voice of the instrument
	delayLines := make([]int, len(patch)) // delay lines of a voice
	otts := make([]int, len(patch))
	limiters := make([]int, len(patch))
	cost := make([]float64, len(patch))
	stack := make([]int, len(patch))
	for _, su := range units {
		u, p := su.unit, su.unit.Parameters
		lo, hi := firstVoice[su.instr], firstVoice[su.instr+1]-1
		stereo := p["stereo"] & 1
		cost[su.instr] += unitCost(u) * float64(su.ops)
		stack[su.instr] += u.StackChange()
		target := func() (int, int) { // the voices of the instrument parameter
			if t := p["instrument"] - 1; t >= 0 && t < len(patch) {
				return firstVoice[t], firstVoice[t+1] - 1
			}
			return lo, hi
		}
		switch u.Type {
		case "speed":
			return ret, nil, fmt.Errorf("songs with the speed unit cannot be rendered in stages")
		case "noise":
			noise[su.instr] += 1 + stereo
		case "delay":
			n := len(u.VarArgs)
			if stereo == 1 {
				n = n / 2 * 2
			}
			delayLines[su.instr] += n
		case "ott":
			otts[su.instr]++
		case "limiter":
			limiters[su.instr]++
		case "spawn", "spcomb":
			tlo, thi := target()
			bind(fmt.Sprintf("%s of %q and its instrument", u.Type, patch[su.instr].Name), min(lo, tlo), max(hi, thi))
		case "bufread", "bufwrite":
			if buf, found := song.Buffers.Find(p["buffer"]); found && buf.Writable() {
				share(fmt.Sprintf("buffer %q", buf.Name), lo, hi)
			}
		}
		for _, name := range sointu.SpectrumBufferParams(u.Type) {
			share(fmt.Sprintf("spectrum %d", p[name]), lo, hi)
		}
		for _, name := range sointu.BusParams(u.Type) {
			share(fmt.Sprintf("bus %d", p[name]), lo, hi)
		}
		for v := lo; v <= hi; v++ {
			a := stageAccess{voice: v, unitNo: su.unitNo}
			switch u.Type {
			case "out":
				access(port(0), a)
				if stereo == 1 {
					access(port(1), a)
				}
			case "outaux":
				for ch := 0; ch <= stereo; ch++ {
					access(port(ch), a)
					access(port(ch+2), a)
				}
			case "aux":
				for ch := 0; ch <= stereo; ch++ {
					access(port(p["channel"]+ch), a)
				}
			case "in":
				a.clear = true
				for ch := 0; ch <= stereo; ch++ {
					access(port(p["channel"]+ch), a)
				}
			case "send":
				t, ok := byID[p["target"]]
				if !ok || (t.instr == su.instr && p["voice"] == 0) {
					continue // no target, or a send within the voice
				}
				tlo, thi := firstVoice[t.instr], firstVoice[t.instr+1]-1
				if p["voice"] > 0 {
					tlo += p["voice"] - 1
					thi = tlo
				}
				for tv := tlo; tv <= thi; tv++ {
					for ch := 0; ch <= stereo; ch++ {
						n := p["port"]&7 + ch
						cell := uint32(64 + tv*4096 + (t.unitNo+1)*64 + 32 + 4*n)
						access(cell, a)
						// the unit clears the ports of its transformed
						// parameters when it reads them, and receive its
						// two; of the others, nothing is known
						cleared := n < features.TransformCount(t.unit.Type) || t.unit.Type == "receive" && n < 2
						access(cell, stageAccess{voice: tv, unitNo: t.unitNo, clear: cleared, read: !cleared})
					}
				}
			}
		}
	}
	for key, s := range shared {
		bind(key, s.lo, s.hi)
	}
	depth := 0
	for i, instr := range patch {
		for v := 0; v < instr.NumVoices; v++ {
			depth += stack[i]
			if depth != 0 {
				bind(fmt.Sprintf("signals left on the stack by %q", instr.Name), firstVoice[i]+v, firstVoice[i]+v+1)
			}
		}
	}
	// A cell is clean when the last access of a sample clears it: it is 0
	// when the next sample starts, whatever stage runs the voice that
	// cleared it. The left and right outputs are cleared after the last
	// voice. The voices of any other cell depend on each other from one
	// sample to the next.
	type liveCell struct {
		cell     uint32
		min, max int
	}
	var clean []liveCell
	for cell, list := range accesses {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].voice != list[j].voice {
				return list[i].voice < list[j].voice
			}
			return list[i].unitNo < list[j].unitNo
		})
		lo, hi := list[0].voice, list[len(list)-1].voice
		switch {
		case cell == port(0) || cell == port(1):
			clean = append(clean, liveCell{cell, lo, numVoices})
		case list[len(list)-1].clear:
			clean = append(clean, liveCell{cell, lo, hi})
		default:
			what := "a send"
			if cell < 64 {
				what = fmt.Sprintf("aux channel %d", (cell-32)/4)
			}
			bind(what+" that reaches the next sample", lo, hi)
		}
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].cell < clean[j].cell })
	cellsAt := func(b int) (cells []uint32) { // the cells used on both sides of the cut before voice b
		for _, c := range clean {
			if c.min < b && b <= c.max {
				cells = append(cells, c.cell)
			}
		}
		return
	}
	voiceInstr := make([]int, numVoices)
	voiceCost := make([]float64, numVoices+1) // cumulative
	for i := range patch {
		for v := firstVoice[i]; v < firstVoice[i+1]; v++ {
			voiceInstr[v] = i
			voiceCost[v+1] = voiceCost[v] + cost[i]
		}
	}
	total := voiceCost[numVoices]
	if len(cuts) > 0 {
		sort.Ints(cuts)
		for i, b := range cuts {
			if b <= 0 || b >= numVoices || i > 0 && cuts[i-1] == b {
				return ret, nil, fmt.Errorf("cannot cut before voice %d: the cuts are voices from 1 to %d, each once", b, numVoices-1)
			}
			if forbidden[b] != "" {
				return ret, nil, fmt.Errorf("cannot cut before voice %d (%q): %s", b, patch[voiceInstr[b]].Name, forbidden[b])
			}
		}
	} else {
		var legal []int
		for b := 1; b < numVoices; b++ {
			if forbidden[b] == "" {
				legal = append(legal, b)
			}
		}
		cuts = balanceStages(legal, voiceCost, numStages)
		if len(cuts)+1 < numStages {
			report = append(report, fmt.Sprintf("the song can be cut into %d stages only, not %d", len(cuts)+1, numStages))
		}
	}
	// the stages of the pipeline, and the stage of all voices
	bounds := append(append([]int{0}, cuts...), numVoices)
	pow := func(n int) uint32 {
		r := uint32(1)
		for ; n > 0; n-- {
			r *= 16007
		}
		return r
	}
	sum := func(per []int, upTo int) (n int) { // of the voices before upTo
		for v := 0; v < upTo; v++ {
			n += per[voiceInstr[v]]
		}
		return
	}
	starts := make([][2]int, len(patch)) // offsets of the code and operands of the instruments
	for i := range patch {
		b, bcErr := vm.NewBytecode(patch[:i], features, song.BPM)
		if bcErr != nil {
			return ret, nil, bcErr
		}
		starts[i] = [2]int{len(b.Opcodes), len(b.Operands)}
	}
	stage := func(first, end int) wasmStage {
		s := wasmStage{First: first, End: end,
			Opcodes: starts[voiceInstr[first]][0], Operands: starts[voiceInstr[first]][1],
			DelayLines: sum(delayLines, first), Otts: sum(otts, first), Limiters: sum(limiters, first),
			SeedInit: pow(sum(noise, first)), SeedStep: pow(sum(noise, numVoices) - sum(noise, end) + sum(noise, first)),
			Cost: (voiceCost[end] - voiceCost[first]) / total,
		}
		var names []string
		for i := voiceInstr[first]; i <= voiceInstr[end-1]; i++ {
			names = append(names, patch[i].Name)
		}
		s.InstrumentNames = strings.Join(names, ", ")
		return s
	}
	offsets := []uint32{}
	for i := 0; i+1 < len(bounds); i++ {
		s := stage(bounds[i], bounds[i+1])
		if i > 0 {
			s.InCells = cellsAt(bounds[i])
		}
		if i+2 < len(bounds) {
			s.OutCells = cellsAt(bounds[i+1])
		}
		offsets = append(offsets, uint32(4*len(ret.StageCells)))
		ret.StageCells = append(ret.StageCells, s.InCells...)
		ret.StageTapeCells = max(ret.StageTapeCells, len(s.InCells))
		ret.Stages = append(ret.Stages, s)
		report = append(report, fmt.Sprintf("stage %d: voices %d to %d (%s), about %.0f%% of the work, reads %d cells for each sample",
			i, s.First, s.End-1, s.InstrumentNames, 100*s.Cost, len(s.InCells)))
	}
	ret.NumStages = len(ret.Stages)
	end := uint32(4 * len(ret.StageCells))
	offsets = append(offsets, end, end, end) // the last stage writes no cells; the stage of all voices reads and writes none
	ret.Stages = append(ret.Stages, stage(0, numVoices))
	for i, s := range ret.Stages {
		ret.StageTable = append(ret.StageTable,
			uint32(s.Opcodes), uint32(s.Operands), uint32(s.First*4096), uint32(numVoices-s.First), uint32(numVoices-s.End),
			uint32(s.DelayLines*262156), uint32(s.Otts*44), uint32(s.Limiters*4112), s.SeedInit, s.SeedStep,
			offsets[i], offsets[i+1], offsets[i+2])
	}
	ret.StageRows = rows
	return ret, report, nil
}

// balanceStages picks up to n-1 of the legal cuts so that the most
// expensive stage is as cheap as possible. cost is cumulative over voices.
func balanceStages(legal []int, cost []float64, n int) []int {
	numVoices := len(cost) - 1
	points := append(append([]int{0}, legal...), numVoices)
	n = min(n, len(points)-1)
	// best[k][j]: the lowest cost of the most expensive of k stages that
	// end at points[j]
	best := make([][]float64, n+1)
	from := make([][]int, n+1)
	for k := range best {
		best[k] = make([]float64, len(points))
		from[k] = make([]int, len(points))
		for j := range best[k] {
			best[k][j] = -1
		}
	}
	best[0][0] = 0
	for k := 1; k <= n; k++ {
		for j := k; j < len(points); j++ {
			for i := k - 1; i < j; i++ {
				if best[k-1][i] < 0 {
					continue
				}
				c := max(best[k-1][i], cost[points[j]]-cost[points[i]])
				if best[k][j] < 0 || c < best[k][j] {
					best[k][j], from[k][j] = c, i
				}
			}
		}
	}
	var cuts []int
	for k, j := n, len(points)-1; k > 1; k-- {
		j = from[k][j]
		cuts = append([]int{points[j]}, cuts...)
	}
	return cuts
}
