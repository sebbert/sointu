package vm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/vsariola/sointu"
)

// The automation units read their lanes from a table, Bytecode.Automation,
// which the Go synth and the wasm player have byte for byte. A lane is
//
//	value                the value before the first record
//	time, value          a record: the value that is reached that long
//	...                  after the record before it (or the start of the song)
//	0                    the end: the last value stays
//
// with times of 16 bits, in units of automationTick samples and never 0,
// and values in 1/128 steps of a parameter as 16 bits, or, when every value
// of the song is a whole step from -128 to 127, in steps as 8 bits
// (Bytecode.AutomationFine tells which). Between two records the value is
// linear. The operand of a unit is the offset of the first record of its
// lane, 2 bytes.
//
// The state of a unit is the offset of its record from that, and the sample
// of the song at which the record before it was reached. It starts at 0 when
// a note is triggered, and the unit then steps through the records up to the
// time of the song: nothing needs to be kept outside the voices.
//
// The parameters that lanes offset (Instrument.Automation) are in a second
// table, Bytecode.AutomationTargets, which the synths go through every
// sample before the units run. For each lane it has
//
//	offset               of the first record of the lane in Automation, 2 bytes
//	count                the number of its targets, 1 byte
//	address, voices,     for each target: its address as a global send has
//	amount               it, without the flag (addressBytes), the number of
//	                     voices from the voice of the address, 1 byte, and
//	                     the amount as that of a send, 1 byte
//
// and the synths add the value of the lane times the amount to the port of
// the address in each of the voices, as a send does. The state of a lane is
// kept outside the voices: Bytecode.AutomationLanes of them, 8 bytes each.

// automationTick is the unit of the times of the records of a lane, in
// samples.
const automationTick = 16

// automationRamp is the number of samples over which a live value of a
// slot reaches a new value.
const automationRamp = 128

type automationRecord struct {
	time  uint16 // ticks after the record before it
	value int16  // 1/128 steps
}

// automationRecords returns the first value and the records of a lane from
// the VarArgs of its unit: the time in samples and the value in 1/128 steps
// of each point.
func automationRecords(args []int) (first int16, records []automationRecord) {
	prevTick, prevValue := 0, 0
	for i := 0; i+1 < len(args); i += 2 {
		limit := sointu.AutomationRange * sointu.AutomationScale
		tick, value := (max(args[i], 0)+automationTick/2)/automationTick, min(max(args[i+1], -limit), limit)
		if i == 0 {
			first, prevValue = int16(value), value
			if tick == 0 {
				continue
			}
		} else if tick <= prevTick {
			tick = prevTick + 1 // a step, or points closer than a tick
		}
		for tick-prevTick > math.MaxUint16 {
			// a record on the way of one that is too far for 16 bits
			mid := prevValue + int(math.Round(float64(value-prevValue)*math.MaxUint16/float64(tick-prevTick)))
			records = append(records, automationRecord{math.MaxUint16, int16(mid)})
			prevTick, prevValue = prevTick+math.MaxUint16, mid
		}
		records = append(records, automationRecord{uint16(tick - prevTick), int16(value)})
		prevTick, prevValue = tick, value
	}
	return first, records
}

// buildAutomation makes the table of the lanes of the automation units of
// the patch. Units with the same slot and points share a lane.
func (b *bytecodeBuilder) buildAutomation(patch sointu.Patch) error {
	type lane struct {
		slot    int
		first   int16
		records []automationRecord
	}
	var lanes []lane
	var keys []string
	whole := func(v int16) bool {
		return v%sointu.AutomationScale == 0 && v >= math.MinInt8*sointu.AutomationScale && v <= math.MaxInt8*sointu.AutomationScale
	}
	add := func(slot int, args []int) {
		key := automationKey(slot, args)
		if _, ok := b.automationOffsets[key]; ok {
			return
		}
		l := lane{slot: min(max(slot, 0), sointu.AutomationSlots-1)}
		l.first, l.records = automationRecords(args)
		b.AutomationFine = b.AutomationFine || !whole(l.first)
		for _, r := range l.records {
			b.AutomationFine = b.AutomationFine || !whole(r.value)
		}
		lanes, keys = append(lanes, l), append(keys, key)
		b.automationOffsets[key] = 0
	}
	for _, instr := range patch {
		for _, u := range instr.Units {
			if u.Type == "automation" && !u.Disabled {
				add(u.Parameters["slot"], u.VarArgs)
			}
		}
		for _, t := range instr.Automation {
			add(t.Slot, t.Points)
		}
	}
	value := func(v int16) {
		if b.AutomationFine {
			b.Automation = binary.LittleEndian.AppendUint16(b.Automation, uint16(v))
		} else {
			b.Automation = append(b.Automation, byte(v/sointu.AutomationScale))
		}
	}
	for i, l := range lanes {
		value(l.first)
		b.automationOffsets[keys[i]] = len(b.Automation)
		for len(b.AutomationSlot) < len(b.Automation) {
			b.AutomationSlot = append(b.AutomationSlot, 0)
		}
		b.AutomationSlot = append(b.AutomationSlot, byte(l.slot))
		for _, r := range l.records {
			b.Automation = binary.LittleEndian.AppendUint16(b.Automation, r.time)
			value(r.value)
		}
		b.Automation = binary.LittleEndian.AppendUint16(b.Automation, 0)
	}
	if len(b.Automation) > math.MaxUint16 {
		return errors.New("the automation of the patch takes more than 65535 bytes")
	}
	return nil
}

// automationKey is what tells lanes apart: the slot and the points.
func automationKey(slot int, args []int) string {
	return fmt.Sprint(slot, args)
}

// buildAutomationTargets makes the table of the parameters that the lanes
// offset, from the automation tables of the instruments, once the addresses
// of all units are known: see the top of this file. Targets whose unit is
// not played (disabled) are left out, and lanes without targets.
func (b *bytecodeBuilder) buildAutomationTargets(patch sointu.Patch) {
	type target struct{ addr, voices, amount int }
	var order []string
	targets := map[string][]target{}
	for _, instr := range patch {
		for _, t := range instr.Automation {
			addr, ok := b.globalAddrs[t.Unit]
			if !ok || t.Port < 0 || t.Port > 7 {
				continue
			}
			key := automationKey(t.Slot, t.Points)
			if _, ok := targets[key]; !ok {
				order = append(order, key)
			}
			// the unit is one of the instrument: its address is that in
			// the first voice of the instrument
			targets[key] = append(targets[key], target{addr + t.Port, instr.NumVoices, min(max(t.Amount, 0), 128)})
		}
	}
	for _, key := range order {
		for len(targets[key]) > 0 {
			ts := targets[key][:min(len(targets[key]), 255)]
			targets[key] = targets[key][len(ts):]
			offset := b.automationOffsets[key]
			b.AutomationTargets = append(b.AutomationTargets, byte(offset), byte(offset>>8), byte(len(ts)))
			for _, t := range ts {
				for i := 0; i < b.addressBytes(); i++ {
					b.AutomationTargets = append(b.AutomationTargets, byte(t.addr>>(8*i)))
				}
				b.AutomationTargets = append(b.AutomationTargets, byte(t.voices), byte(t.amount))
			}
			b.AutomationLanes++
		}
	}
}

// AutomationValueBytes returns the number of bytes of a value in the
// automation table.
func (b *Bytecode) AutomationValueBytes() int {
	if b.AutomationFine {
		return 2
	}
	return 1
}

// automationValue returns the value at the offset of the table, in its
// units.
func (b *Bytecode) automationValue(at int) int32 {
	if b.AutomationFine {
		return int32(int16(binary.LittleEndian.Uint16(b.Automation[at:])))
	}
	return int32(int8(b.Automation[at]))
}

// automation returns what an automation unit pushes: the value of its lane
// at the time of the song, or the live value of its slot when the song is
// not playing, in steps over 128. The wasm player computes the first, in
// the same operations.
func (s *GoSynth) automation(u *unit, lane int) float32 {
	if !s.songPlaying {
		return s.live[s.bytecode.AutomationSlot[lane]].value * (1.0 / 128)
	}
	offset, since := math.Float32bits(u.state[0]), math.Float32bits(u.state[1])
	if math.Float32bits(u.state[2]) != s.automationEpoch || since > s.songTime {
		// the table changed, or the song went back: from the first record.
		// Neither happens in the wasm player.
		offset, since = 0, 0
		u.state[2] = math.Float32frombits(s.automationEpoch)
	}
	v := s.laneValue(lane, &offset, &since)
	u.state[0], u.state[1] = math.Float32frombits(offset), math.Float32frombits(since)
	return v
}

// laneValue returns the value of the lane at the offset of the table at the
// time of the song, in steps over 128. offset and since are the state of
// the reader: the offset of its record from the first one, and the sample
// at which the record before it was reached; it moves them on.
func (s *GoSynth) laneValue(lane int, offset, since *uint32) float32 {
	b := &s.bytecode
	vs := b.AutomationValueBytes()
	p, t := lane+int(*offset), s.songTime-*since
	var dt uint32
	for {
		dt = uint32(binary.LittleEndian.Uint16(b.Automation[p:])) * automationTick
		if t <= dt-1 { // also at the end, where dt is 0
			break
		}
		t -= dt
		p += 2 + vs
	}
	*offset, *since = uint32(p-lane), s.songTime-t
	v0 := b.automationValue(p - vs)
	x := float32(v0)
	if dt != 0 {
		x += float32(float32(b.automationValue(p+2)-v0) * float32(float32(t)/float32(dt)))
	}
	if b.AutomationFine {
		return x * (1.0 / 16384)
	}
	return x * (1.0 / 128)
}

type (
	// automationLane is a lane of Bytecode.AutomationTargets, as the Go
	// synth applies it: the offset of its first record in the table of the
	// lanes, its targets, and its state (see laneValue), with the
	// automationEpoch that it is of.
	automationLane struct {
		lane                 int
		targets              []automationTarget
		offset, since, epoch uint32
	}

	// automationTarget is the port of a unit in voices of the synth, and
	// the amount that it gets of the lane, as a send has it: from -1 to 1.
	automationTarget struct {
		voice, voices, unit, port int
		amount                    float32
	}
)

// loadAutomationTargets reads the table of the targets of the lanes from
// the bytecode: see the top of this file.
func (s *GoSynth) loadAutomationTargets() {
	b := &s.bytecode
	s.automationLanes = nil
	ab := 2
	if b.WideVoices {
		ab = 3
	}
	t := b.AutomationTargets
	for len(t) >= 3 {
		l := automationLane{lane: int(t[0]) | int(t[1])<<8}
		n := int(t[2])
		t = t[3:]
		for ; n > 0 && len(t) >= ab+2; n-- {
			addr := 0
			for i := 0; i < ab; i++ {
				addr |= int(t[i]) << (8 * i)
			}
			addr -= 0x10
			at := automationTarget{
				voice:  addr >> 10,
				voices: int(t[ab]),
				unit:   ((addr & 0x3F0) >> 4) - 1,
				port:   addr & 7,
				amount: float32(t[ab+1])/64 - 1, // as a send has it: exactly amount/128·2 - 1
			}
			t = t[ab+2:]
			if at.unit < 0 || at.unit >= MAX_UNITS || at.voice < 0 || at.voice+at.voices > MAX_VOICES {
				continue
			}
			l.targets = append(l.targets, at)
		}
		s.automationLanes = append(s.automationLanes, l)
	}
}

// applyAutomation adds the value of each lane, times the amount of each of
// its targets, to their ports, as sends before the units would: once a
// sample, before the units run.
func (s *GoSynth) applyAutomation() {
	for i := range s.automationLanes {
		l := &s.automationLanes[i]
		var v float32
		if !s.songPlaying {
			v = s.live[s.bytecode.AutomationSlot[l.lane]].value * (1.0 / 128)
		} else {
			if l.epoch != s.automationEpoch || l.since > s.songTime {
				l.offset, l.since, l.epoch = 0, 0, s.automationEpoch
			}
			v = s.laneValue(l.lane, &l.offset, &l.since)
		}
		for _, t := range l.targets {
			f := float32(v * t.amount)
			for k := t.voice; k < t.voice+t.voices; k++ {
				s.state.voices[k].units[t.unit].ports[t.port] += f
			}
		}
	}
}

// automationLive is the live value of a slot, in steps of a parameter: it
// moves to target in a ramp.
type automationLive struct {
	value, target, step float32
	left                int
}

// SetSongTime tells the synth the sample of the song that it renders next,
// which is where its automation units read their lanes; a negative sample,
// that the song is not playing: the units then push the live values of
// their slots. A new synth is at sample 0.
func (s *GoSynth) SetSongTime(sample int) {
	s.songPlaying = sample >= 0
	if s.songPlaying {
		s.songTime = uint32(sample)
	}
}

// SetAutomation sets the live value of a slot, in steps of a parameter. The
// value is reached in a short ramp.
func (s *GoSynth) SetAutomation(slot int, value float32) {
	if slot < 0 || slot >= len(s.live) {
		return
	}
	l := &s.live[slot]
	l.target, l.left = value, automationRamp
	l.step = (value - l.value) / automationRamp
	s.liveRamps = true
}

// stepAutomation moves the time of the song and the live values by a
// sample.
func (s *GoSynth) stepAutomation() {
	s.songTime++
	if !s.liveRamps {
		return
	}
	s.liveRamps = false
	for i := range s.live {
		l := &s.live[i]
		if l.left == 0 {
			continue
		}
		if l.left--; l.left == 0 {
			l.value = l.target
		} else {
			l.value += l.step
			s.liveRamps = true
		}
	}
}
