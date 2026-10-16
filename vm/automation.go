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
	for _, instr := range patch {
		for _, u := range instr.Units {
			if u.Type != "automation" || u.Disabled {
				continue
			}
			key := automationKey(u)
			if _, ok := b.automationOffsets[key]; ok {
				continue
			}
			l := lane{slot: min(max(u.Parameters["slot"], 0), sointu.AutomationSlots-1)}
			l.first, l.records = automationRecords(u.VarArgs)
			b.AutomationFine = b.AutomationFine || !whole(l.first)
			for _, r := range l.records {
				b.AutomationFine = b.AutomationFine || !whole(r.value)
			}
			lanes, keys = append(lanes, l), append(keys, key)
			b.automationOffsets[key] = 0
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

func automationKey(u sointu.Unit) string {
	return fmt.Sprint(u.Parameters["slot"], u.VarArgs)
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
	b := &s.bytecode
	if !s.songPlaying {
		return s.live[b.AutomationSlot[lane]].value * (1.0 / 128)
	}
	vs := b.AutomationValueBytes()
	offset, since := math.Float32bits(u.state[0]), math.Float32bits(u.state[1])
	if math.Float32bits(u.state[2]) != s.automationEpoch || since > s.songTime {
		// the table changed, or the song went back: from the first record.
		// Neither happens in the wasm player.
		offset, since = 0, 0
		u.state[2] = math.Float32frombits(s.automationEpoch)
	}
	p, t := lane+int(offset), s.songTime-since
	var dt uint32
	for {
		dt = uint32(binary.LittleEndian.Uint16(b.Automation[p:])) * automationTick
		if t <= dt-1 { // also at the end, where dt is 0
			break
		}
		t -= dt
		p += 2 + vs
	}
	u.state[0], u.state[1] = math.Float32frombits(uint32(p-lane)), math.Float32frombits(s.songTime-t)
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
