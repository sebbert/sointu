package tracker

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/vsariola/sointu"
)

// The delay times of a delay unit are edited on a grid of the values a user
// wants, unless the unit is set free: note lengths when the delay follows the
// tempo, semitones when it follows the note and whole milliseconds when it is
// fixed. The values stored in the unit are the same either way: 1/48 beats
// when following the tempo, samples otherwise.
//
// A unit starts free if it has a delay time that is not on the grid, so that
// a step does not move the time to the grid: this is decided once, when the
// unit is first seen (loaded with a song or a preset, pasted or added) and
// when a reverb preset sets its times. After that only the user changes it.

const (
	delayFixed = iota // the values of the notetracking parameter of a delay
	delayPitch
	delayBPM

	delayBPMMax     = 576   // the longest delay time following the tempo, 12 beats
	delaySamplesMax = 65535 // the longest delay line, and the longest time otherwise
)

// delayGrid is the grid of the delay times for one value of notetracking.
type delayGrid struct {
	values []int // the grid, ascending
	coarse []int // the values a large step moves between, a subset of the grid
	// knots are the grid and the ends of the range of the delay time. The
	// scale of the knob is linear between them, each a step apart.
	knots []int
}

var delayGrids [3]delayGrid // by notetracking

func init() {
	fixed := &delayGrids[delayFixed]
	for ms := 1; ms*441/10 <= delaySamplesMax; ms++ {
		v := (ms*441 + 5) / 10 // 44.1 samples per millisecond
		fixed.values = append(fixed.values, v)
		if ms%10 == 0 {
			fixed.coarse = append(fixed.coarse, v)
		}
	}
	pitch := &delayGrids[delayPitch]
	for st := -30; st <= 30; st++ { // as delayNoteTrackGrid
		v := int(math.Exp2(float64(st)/12)*10787 + 0.5)
		pitch.values = append(pitch.values, v)
		if st%12 == 0 {
			pitch.coarse = append(pitch.coarse, v)
		}
	}
	bpm := &delayGrids[delayBPM]
	for v := 1; v <= delayBPMMax; v++ {
		if _, _, variant, ok := delayNoteLength(v); ok {
			bpm.values = append(bpm.values, v)
			if variant == delayStraight {
				bpm.coarse = append(bpm.coarse, v)
			}
		}
	}
	for i, r := range [...]int{delaySamplesMax, delaySamplesMax, delayBPMMax} {
		g := &delayGrids[i]
		g.knots = slices.Clone(g.values)
		if g.knots[0] > 1 {
			g.knots = slices.Insert(g.knots, 0, 1)
		}
		if g.knots[len(g.knots)-1] < r {
			g.knots = append(g.knots, r)
		}
	}
}

// delayGridOf returns the grid of the delay times of the unit of the
// parameter.
func delayGridOf(p *Parameter) *delayGrid { return delayGridOfUnit(p.unit) }

func delayGridOfUnit(unit *sointu.Unit) *delayGrid {
	switch unit.Parameters["notetracking"] {
	case delayPitch:
		return &delayGrids[delayPitch]
	case delayBPM:
		return &delayGrids[delayBPM]
	}
	return &delayGrids[delayFixed]
}

// delayOffGrid tells if any delay time of the delay unit is not on its grid.
func delayOffGrid(unit *sointu.Unit) bool {
	g := delayGridOfUnit(unit)
	return slices.ContainsFunc(unit.VarArgs, func(v int) bool { return !g.contains(v) })
}

// setDelayFree sets if the delay times of the delay unit are edited freely.
func (m *Model) setDelayFree(unit *sointu.Unit, free bool) {
	if m.delayFree == nil {
		m.delayFree = make(map[int]bool)
	}
	m.delayFree[unit.ID] = free
}

func (g *delayGrid) contains(value int) bool {
	_, ok := slices.BinarySearch(g.values, value)
	return ok
}

// stepOnGrid returns the grid value delta steps from the value. A value that
// is not on the grid takes one step to reach the nearest grid value in that
// direction. At the end of the grid, and beyond it, the value stays.
func stepOnGrid(grid []int, value, delta int) int {
	i, found := slices.BinarySearch(grid, value) // grid[i] is the first at least value
	switch {
	case delta > 0:
		if !found {
			i--
		}
		return max(value, grid[min(i+delta, len(grid)-1)])
	case delta < 0:
		return min(value, grid[max(i+delta, 0)])
	}
	return value
}

// position returns where the value lies on the scale of the knob, from 0 to
// 1: values between two knots lie between them.
func (g *delayGrid) position(value int) float32 {
	k := g.knots
	value = min(max(value, k[0]), k[len(k)-1])
	i, found := slices.BinarySearch(k, value)
	if found {
		return float32(i) / float32(len(k)-1)
	}
	return (float32(i-1) + float32(value-k[i-1])/float32(k[i]-k[i-1])) / float32(len(k)-1)
}

// dragged returns the value that dragging the knob from the value start, by
// amount of its scale, leads to: the nearest grid value, or any value when
// free. A drag of less than half a step leaves the value alone, so that a
// value that is not on the grid does not move to it by a mere click.
func (g *delayGrid) dragged(start int, amount float32, free bool) int {
	k := g.knots
	n := float32(len(k) - 1)
	if amount == 0 || !free && amount*n < 0.5 && amount*n > -0.5 {
		return start
	}
	x := min(max((g.position(start)+amount)*n, 0), n)
	if free {
		i := min(int(x), len(k)-2)
		return k[i] + int(math.Round(float64(x-float32(i))*float64(k[i+1]-k[i])))
	}
	return min(max(k[int(x+0.5)], g.values[0]), g.values[len(g.values)-1])
}

type delayVariant int

const (
	delayStraight delayVariant = iota
	delayDotted
	delayTriplet
)

// delayNoteLength returns the note length num/den of a delay time following
// the tempo, a beat being a quarter note of 48, and whether it is one: 3·2^k
// are the straight notes from 1/64, 9·2^k the dotted ones from 1/32 and 2^k
// the triplets from 1/128.
func delayNoteLength(value int) (num, den int, variant delayVariant, ok bool) {
	if value < 1 || value > delayBPMMax {
		return 0, 0, 0, false
	}
	num = 1
	for value&1 == 0 {
		value >>= 1
		num <<= 1
	}
	switch value {
	case 1:
		den, variant = 128, delayTriplet
	case 3:
		den, variant = 64, delayStraight
	case 9:
		den, variant = 32, delayDotted
	default:
		return 0, 0, 0, false
	}
	for num > 1 && den > 1 {
		num >>= 1
		den >>= 1
	}
	return num, den, variant, true
}

// delayNoteName returns the name of a delay time following the tempo, if it is
// a note length: short for the knob (1/8D, 1/8T) or long for the hint.
func delayNoteName(value int, long bool) (string, bool) {
	num, den, variant, ok := delayNoteLength(value)
	if !ok {
		return "", false
	}
	suffix := [...]string{"", "D", "T"}
	if long {
		suffix = [...]string{"", " dotted", " triplet"}
	}
	return fmt.Sprintf("%d/%d%s", num, den, suffix[variant]), true
}

// delaySemitones returns the pitch of a delay time following the note, in
// semitones relative to the note, and whether it is a whole number of them.
func delaySemitones(value int) (semitones float64, whole bool) {
	if i, ok := slices.BinarySearch(delayNoteTrackGrid, value); ok {
		return float64(len(delayNoteTrackGrid)/2 - i), true
	}
	return -math.Log2(float64(value)/10787) * 12, false
}

// trimFloat formats x with at most the given number of decimals.
func trimFloat(x float64, decimals int) string {
	s := strconv.FormatFloat(x, 'f', decimals, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func signed(s string) string {
	if s == "0" || strings.HasPrefix(s, "-") {
		return s
	}
	return "+" + s
}

func plural(amount, unit string) string {
	if amount == "1" {
		return amount + " " + unit
	}
	return amount + " " + unit + "s"
}

// delayTimeParameter methods for the grid. See params.go for the rest of the
// vtable.

func (d *delayTimeParameter) free(p *Parameter) bool { return p.m.delayFree[p.unit.ID] }

// Type makes the delay time a choice among the note lengths when it follows
// the tempo, unless it is free. Otherwise it is a knob, which moves over the
// semitones or the milliseconds of the grid.
func (d *delayTimeParameter) Type(p *Parameter) ParameterType {
	if !d.free(p) && p.unit.Parameters["notetracking"] == delayBPM {
		return ChoiceParameter
	}
	return IntegerParameter
}

func (d *delayTimeParameter) Choices(p *Parameter) IntValue { return delayTimeChoice{*p} }

// delayTimeChoice is a delay time following the tempo as an index of the
// note lengths, from the longest to the shortest. A time that is not on the
// grid is none of the choices.
type delayTimeChoice struct{ p Parameter }

// gridIndex returns the index in the grid of the choice, and back.
func (v delayTimeChoice) gridIndex(choice int) int {
	return len(delayGridOf(&v.p).values) - 1 - choice
}

func (v delayTimeChoice) Value() int {
	if i, ok := slices.BinarySearch(delayGridOf(&v.p).values, v.p.Value()); ok {
		return v.gridIndex(i)
	}
	return -1
}
func (v delayTimeChoice) SetValue(choice int) bool {
	return v.p.SetValue(delayGridOf(&v.p).values[v.gridIndex(choice)])
}
func (v delayTimeChoice) Range() RangeInclusive {
	return RangeInclusive{Min: 0, Max: len(delayGridOf(&v.p).values) - 1}
}
func (v delayTimeChoice) StringOf(choice int) string {
	g := delayGridOf(&v.p)
	if choice < 0 || choice >= len(g.values) {
		return ""
	}
	name, _ := delayNoteName(g.values[v.gridIndex(choice)], true)
	return name
}

// Step moves the delay time by grid values: all of them, or the straight
// notes, octaves or 10 ms with large. Free delay times move by single values,
// or to the next grid value with large.
func (d *delayTimeParameter) Step(p *Parameter, delta int, large bool) int {
	val := d.Value(p)
	g := delayGridOf(p)
	switch {
	case !d.free(p) && large:
		return stepOnGrid(g.coarse, val, delta)
	case !d.free(p):
		return stepOnGrid(g.values, val, delta)
	case large:
		return d.RoundToGrid(p, val+delta, delta > 0)
	}
	return val + delta
}

func (d *delayTimeParameter) Position(p *Parameter, value int) float32 {
	return delayGridOf(p).position(value)
}

func (d *delayTimeParameter) Dragged(p *Parameter, start int, amount float32) int {
	return delayGridOf(p).dragged(start, amount, d.free(p))
}

// Label is the text on the knob: the stored value when free. Otherwise it is
// in the units of the grid: a note length, milliseconds or semitones. Values
// that are not on the grid show as what they are, the stored value or a
// decimal fraction.
func (d *delayTimeParameter) Label(p *Parameter) string {
	val := d.Value(p)
	if d.free(p) {
		return strconv.Itoa(val)
	}
	switch p.unit.Parameters["notetracking"] {
	case delayBPM:
		if name, ok := delayNoteName(val, false); ok {
			return name
		}
		return strconv.Itoa(val)
	case delayPitch:
		if st, whole := delaySemitones(val); whole {
			return signed(strconv.Itoa(int(st)))
		} else {
			return fmt.Sprintf("%+.1f", st)
		}
	}
	if delayGridOf(p).contains(val) {
		return strconv.Itoa((val*10 + 220) / 441)
	}
	decimals := 1
	if val < 441 { // below 10 ms
		decimals = 2
	}
	return strconv.FormatFloat(float64(val)/44.1, 'f', decimals, 64)
}

func (d *delayTimeParameter) Hint(p *Parameter) ParameterHint {
	val := d.Value(p)
	song := &p.m.d.Song
	var text string
	switch p.unit.Parameters["notetracking"] {
	case delayBPM:
		name, ok := delayNoteName(val, true)
		if !ok {
			name = fmt.Sprintf("%d/48 beat", val)
		}
		text = fmt.Sprintf("%s: %s, %s", name,
			plural(trimFloat(float64(val)/48, 3), "beat"),
			plural(trimFloat(float64(val*song.RowsPerBeat)/48, 3), "row"))
		if song.BPM > 0 {
			samples := 44100 * 60 * val / 48 / song.BPM // as the synth computes it
			if samples > delaySamplesMax {
				text += fmt.Sprintf(", %s ms (the longest delay)", trimFloat(delaySamplesMax/44.1, 0))
			} else {
				text += fmt.Sprintf(", %s ms", trimFloat(float64(samples)/44.1, 1))
			}
		}
	case delayPitch:
		st, whole := delaySemitones(val)
		decimals := 3
		if whole {
			decimals = 0
		}
		text = fmt.Sprintf("%s st (%d)", signed(trimFloat(st, decimals)), val)
	default:
		text = fmt.Sprintf("%s ms, %s", trimFloat(float64(val)/44.1, 3), plural(strconv.Itoa(val), "sample"))
		if spr := song.SamplesPerRow(); spr > 0 {
			text += ", " + plural(trimFloat(float64(val)/float64(spr), 3), "row")
		}
	}
	if p.unit.Parameters["stereo"] == 1 {
		if p.index < len(p.unit.VarArgs)/2 {
			text += " R"
		} else {
			text += " L"
		}
	}
	return ParameterHint{text, true}
}

// delayFreeParameter vtable: whether the delay times of a delay unit can take
// any value, instead of the values of their grid. It is a setting of the
// tracker, not of the song: the times are stored the same either way. See
// updateParams for how it starts.

func (d *delayFreeParameter) Value(p *Parameter) int {
	if p.m.delayFree[p.unit.ID] {
		return 1
	}
	return 0
}
func (d *delayFreeParameter) SetValue(p *Parameter, v int) bool {
	p.m.setDelayFree(p.unit, v == 1)
	return true
}
func (d *delayFreeParameter) Range(p *Parameter) RangeInclusive {
	return RangeInclusive{Min: 0, Max: 1}
}
func (d *delayFreeParameter) Type(p *Parameter) ParameterType                { return BoolParameter }
func (d *delayFreeParameter) Name(p *Parameter) string                       { return "free" }
func (d *delayFreeParameter) RoundToGrid(p *Parameter, val int, up bool) int { return val }
func (d *delayFreeParameter) Reset(p *Parameter)                             { d.SetValue(p, 0) }
func (d *delayFreeParameter) Hint(p *Parameter) ParameterHint {
	if d.Value(p) == 1 {
		return ParameterHint{"any value", true}
	}
	switch p.unit.Parameters["notetracking"] {
	case delayPitch:
		return ParameterHint{"semitones", true}
	case delayBPM:
		return ParameterHint{"note lengths", true}
	}
	return ParameterHint{"milliseconds", true}
}
