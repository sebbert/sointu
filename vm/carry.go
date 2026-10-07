package vm

import (
	"slices"

	"github.com/vsariola/sointu"
)

// When the patch of a GoSynth changes (GoSynth.Update), the units that are
// still there keep their state, in every voice: the state and the ports of
// the unit, and its delay lines, ott, limiter, reverb and convolution states, which are
// kept in tables in the order the units run. Only the units that are new, or of
// another type than before, start from nothing. So a unit can be added,
// removed, moved or changed while the song plays without the envelopes
// starting anew and the filters and delays falling silent.
//
// Which unit is which is found from the patches (synthLayout):
//
//   - Instruments are matched by the units they share, by ID and type, in
//     their order; an instrument that shares none with any is matched with the
//     one in its place, if the patch has as many instruments as before. The
//     voices of an instrument are matched in their order.
//   - The units of two matched instruments are aligned in their order: units
//     of the same type can be matched, those with the same ID before any
//     other (most units of the tracker have IDs; those that an eq unit stands
//     for have none, and those copied from modules get other ones when units
//     are added). A unit that is left, and has the ID and the type of a unit
//     left in the old instrument, was moved, and is matched too.

type (
	// unitKey tells a unit of a patch from the others, and what it keeps
	// outside the voices.
	unitKey struct {
		id                     int
		typ                    string
		delays, otts, limiters int // in each voice
		reverbs, convs         int
	}

	// instrLayout is an instrument as the synth runs it: its voices, its
	// units without the disabled and empty ones, and slots, the number of
	// such units before each unit of the instrument as the patch has it (and
	// after the last one).
	instrLayout struct {
		voices int
		units  []unitKey
		slots  []int
	}

	synthLayout []instrLayout
)

func newSynthLayout(patch sointu.Patch) synthLayout {
	ret := make(synthLayout, len(patch))
	for i, instr := range patch {
		l := instrLayout{voices: instr.NumVoices, slots: make([]int, 0, len(instr.Units)+1)}
		for _, u := range instr.Units {
			l.slots = append(l.slots, len(l.units))
			if u.Type == "" || u.Disabled {
				continue
			}
			k := unitKey{id: u.ID, typ: u.Type}
			switch u.Type {
			case "delay":
				k.delays = len(u.VarArgs)
			case "ott":
				k.otts = 1
			case "limiter":
				k.limiters = 1
			case "reverb":
				k.reverbs = 1
			case "convolution":
				k.convs = 1
			}
			l.units = append(l.units, k)
		}
		l.slots = append(l.slots, len(l.units))
		ret[i] = l
	}
	return ret
}

// same reports whether the synth keeps the same states in the same places
// for both layouts.
func (a synthLayout) same(b synthLayout) bool {
	return slices.EqualFunc(a, b, func(x, y instrLayout) bool {
		return x.voices == y.voices && slices.EqualFunc(x.units, y.units, func(p, q unitKey) bool {
			p.id, q.id = 0, 0
			return p == q
		})
	})
}

// The scores of matching two units of the same type: with the same ID, and
// without.
const (
	carrySameID   = 4
	carrySameType = 1
)

// alignUnits returns, for each unit of the new instrument, the unit of the
// old one whose state it takes, or -1.
func alignUnits(old, cur []unitKey) []int {
	score := func(a, b unitKey) int {
		switch {
		case a.typ != b.typ:
			return 0
		case a.id != 0 && a.id == b.id:
			return carrySameID
		}
		return carrySameType
	}
	// best[i][j] is the best score of the units from i of old and j of new
	w := len(cur) + 1
	best := make([]int, (len(old)+1)*w)
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(cur) - 1; j >= 0; j-- {
			b := max(best[(i+1)*w+j], best[i*w+j+1])
			if s := score(old[i], cur[j]); s > 0 {
				b = max(b, best[(i+1)*w+j+1]+s)
			}
			best[i*w+j] = b
		}
	}
	ret := make([]int, len(cur))
	for j := range ret {
		ret[j] = -1
	}
	used := make([]bool, len(old))
	for i, j := 0, 0; i < len(old) && j < len(cur); {
		s := score(old[i], cur[j])
		switch {
		case s > 0 && best[i*w+j] == best[(i+1)*w+j+1]+s:
			ret[j], used[i] = i, true
			i, j = i+1, j+1
		case best[i*w+j] == best[(i+1)*w+j]:
			i++
		default:
			j++
		}
	}
	// the units that were moved
	for j, k := range cur {
		if ret[j] >= 0 || k.id == 0 {
			continue
		}
		for i, o := range old {
			if !used[i] && o.id == k.id && o.typ == k.typ {
				ret[j], used[i] = i, true
				break
			}
		}
	}
	return ret
}

// alignInstruments returns, for each instrument of the new layout, the
// instrument of the old one that it was, or -1.
func alignInstruments(old, cur synthLayout) []int {
	type idType struct {
		id  int
		typ string
	}
	sets := make([]map[idType]bool, len(old))
	for i, instr := range old {
		sets[i] = map[idType]bool{}
		for _, u := range instr.units {
			if u.id != 0 {
				sets[i][idType{u.id, u.typ}] = true
			}
		}
	}
	// shared[i*w+j] is the number of units that old i and new j share
	w := len(cur) + 1
	shared := make([]int, (len(old)+1)*w)
	for i := range old {
		for j, instr := range cur {
			for _, u := range instr.units {
				if u.id != 0 && sets[i][idType{u.id, u.typ}] {
					shared[i*w+j]++
				}
			}
		}
	}
	best := make([]int, (len(old)+1)*w)
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(cur) - 1; j >= 0; j-- {
			b := max(best[(i+1)*w+j], best[i*w+j+1])
			if s := shared[i*w+j]; s > 0 {
				b = max(b, best[(i+1)*w+j+1]+s)
			}
			best[i*w+j] = b
		}
	}
	ret := make([]int, len(cur))
	for j := range ret {
		ret[j] = -1
	}
	used := make([]bool, len(old))
	for i, j := 0, 0; i < len(old) && j < len(cur); {
		s := shared[i*w+j]
		switch {
		case s > 0 && best[i*w+j] == best[(i+1)*w+j+1]+s:
			ret[j], used[i] = i, true
			i, j = i+1, j+1
		case best[i*w+j] == best[(i+1)*w+j]:
			i++
		default:
			j++
		}
	}
	// those that share nothing with any, e.g. as their units have no IDs:
	// the one in the same place
	if len(old) == len(cur) {
		for j := range ret {
			if ret[j] < 0 && !used[j] {
				ret[j], used[j] = j, true
			}
		}
	}
	return ret
}

// carryTable returns a table of states, kept in the order the units run,
// with the states of the units where the new layout has them. counts tells
// how many states a unit has in each voice; from tells, for each new voice
// and unit, the old voice and unit, or -1. If nothing moves, the table is
// returned as it is, at least as long as the new layout needs it.
func carryTable[T any](table []T, old, cur synthLayout, count func(unitKey) int, from func(voice, unit int) (int, int)) []T {
	// where the states of each old voice and unit start
	type place struct{ voice, unit int }
	type states struct{ start, count int }
	starts := map[place]states{}
	n, voice := 0, 0
	for _, instr := range old {
		for range instr.voices {
			for u, k := range instr.units {
				if c := count(k); c > 0 {
					starts[place{voice, u}] = states{n, c}
					n += c
				}
			}
			voice++
		}
	}
	// and of each new one, with the old state it takes
	type move struct{ to, from, count int }
	var moves []move
	total, moved := 0, false
	voice = 0
	for _, instr := range cur {
		for range instr.voices {
			for u, k := range instr.units {
				c := count(k)
				if c == 0 {
					continue
				}
				m := move{to: total, from: -1, count: c}
				if ov, ou := from(voice, u); ov >= 0 {
					// with as many states as before: e.g. the delay lines
					// of a delay unit that has as many as it had
					if st, ok := starts[place{ov, ou}]; ok && st.count == c && st.start+c <= len(table) {
						m.from = st.start
					}
				}
				moved = moved || m.from != m.to
				moves = append(moves, m)
				total += c
			}
			voice++
		}
	}
	if !moved {
		for len(table) < total {
			var zero T
			table = append(table, zero)
		}
		return table
	}
	ret := make([]T, max(total, len(table)))
	for _, m := range moves {
		if m.from >= 0 {
			copy(ret[m.to:m.to+m.count], table[m.from:m.from+m.count])
		}
	}
	return ret
}

// carryState puts the states of the units where the new layout has them,
// after the patch changed.
func (s *GoSynth) carryState(old, cur synthLayout) {
	instruments := alignInstruments(old, cur)
	oldFirst := make([]int, len(old)) // the first voice of each old instrument
	n := 0
	for i, instr := range old {
		oldFirst[i] = n
		n += instr.voices
	}
	// for each new voice, the old voice, and for each of its units, the old
	// unit
	type source struct {
		voice int
		units []int
	}
	var sources []source
	for j, instr := range cur {
		var units []int
		i := instruments[j]
		if i >= 0 {
			units = alignUnits(old[i].units, instr.units)
		}
		for v := range instr.voices {
			src := source{voice: -1}
			if i >= 0 && v < old[i].voices && oldFirst[i]+v < MAX_VOICES {
				src = source{oldFirst[i] + v, units}
			}
			sources = append(sources, src)
		}
	}
	if len(sources) > MAX_VOICES {
		sources = sources[:MAX_VOICES]
	}
	before := s.state.voices
	for v := range s.state.voices {
		if v >= len(sources) || sources[v].voice < 0 {
			s.state.voices[v] = voice{}
			continue
		}
		src := &before[sources[v].voice]
		dst := &s.state.voices[v]
		*dst = *src
		dst.units = [MAX_UNITS]unit{}
		for u, from := range sources[v].units {
			if u < MAX_UNITS && from >= 0 && from < MAX_UNITS {
				dst.units[u] = src.units[from]
			}
		}
	}
	from := func(voice, unit int) (int, int) {
		if voice >= len(sources) || sources[voice].voice < 0 {
			return -1, -1
		}
		return sources[voice].voice, sources[voice].units[unit]
	}
	s.delaylines = carryTable(s.delaylines, old, cur, func(k unitKey) int { return k.delays }, from)
	s.otts = carryTable(s.otts, old, cur, func(k unitKey) int { return k.otts }, from)
	s.limiters = carryTable(s.limiters, old, cur, func(k unitKey) int { return k.limiters }, from)
	s.reverbs = carryTable(s.reverbs, old, cur, func(k unitKey) int { return k.reverbs }, from)
	s.convs = carryTable(s.convs, old, cur, func(k unitKey) int { return k.convs }, from)
}
