package tracker

import (
	"math"
	"slices"

	"github.com/vsariola/sointu"
)

// The positions of a bufread unit that count periods of the note (start, with
// pitch timing) or 48ths of a beat (start, loopstart and looplength, with BPM
// timing) are edited on a grid, like the delay times, unless the unit is set
// free: parts and multiples of a period, or note lengths and bars. Positions
// in frames have no grid. Whether a unit is free is decided as for a delay
// unit (see updateParams) and kept in Model.gridFree.

// bufreadPeriodFractions are the parts of a period on the grid, with their
// names on the knob: -1/n period puts the first peak of a comb filter on the
// n-th harmonic of the note.
var bufreadPeriodFractions = [...]struct {
	periods float64
	name    string
}{
	{1.0 / 4, "1/4"}, {1.0 / 3, "1/3"}, {1.0 / 2, "1/2"}, {2.0 / 3, "2/3"}, {3.0 / 4, "3/4"}, {1.5, "1.5"},
}

// bufreadPeriods are the magnitudes of the pitch grid in periods, ascending:
// the fractions, whole periods to 16, then 1.5·2^k and 2^k.
var bufreadPeriods = func() []float64 {
	var ret []float64
	for _, f := range bufreadPeriodFractions {
		ret = append(ret, f.periods)
	}
	for n := 1; n <= 16; n++ {
		ret = append(ret, float64(n))
	}
	for n := 24; n <= 1<<20; n *= 2 {
		ret = append(ret, float64(n), float64(n*4/3))
	}
	slices.Sort(ret)
	return ret
}()

// bufreadPeriodFrames is a position with pitch timing of the given periods.
func bufreadPeriodFrames(periods float64) int {
	return int(math.Round(periods * bufreadNotePeriod))
}

// bufreadBeatGridMax is the most bars beyond the note lengths on the grid
// with BPM timing.
const bufreadBeatGridMax = 256

// bufreadGrid returns the grid of the position, within its range, if it has
// one.
func bufreadGrid(p *Parameter) (*delayGrid, bool) {
	timing := bufreadTiming(p)
	if timing == 0 || p.vtable == nil {
		return nil, false
	}
	r := p.Range()
	limit := max(r.Max, -r.Min)
	var mags, coarse []int // positive values, ascending
	if timing == 1 {
		for _, periods := range bufreadPeriods {
			v := bufreadPeriodFrames(periods)
			if v > limit {
				break
			}
			mags = append(mags, v)
			if periods == math.Trunc(periods) {
				coarse = append(coarse, v)
			}
		}
	} else {
		for v := 1; v <= delayBPMMax && v <= limit; v++ {
			if _, _, variant, ok := delayNoteLength(v); ok {
				mags = append(mags, v)
				if variant == delayStraight {
					coarse = append(coarse, v)
				}
			}
		}
		for v, n := delayBPMMax+192, 0; v <= limit && n < bufreadBeatGridMax; v, n = v+192, n+1 {
			mags = append(mags, v) // whole bars of four beats
			coarse = append(coarse, v)
		}
	}
	g := &delayGrid{
		values: signedGrid(mags, r),
		coarse: signedGrid(coarse, r),
	}
	if len(g.values) == 0 { // a range shorter than the shortest grid value
		g.values = []int{r.Max}
	}
	if len(g.coarse) == 0 {
		g.coarse = g.values
	}
	g.knots = slices.Clone(g.values)
	if g.knots[0] > r.Min {
		g.knots = slices.Insert(g.knots, 0, r.Min)
	}
	if g.knots[len(g.knots)-1] < r.Max {
		g.knots = append(g.knots, r.Max)
	}
	return g, true
}

// signedGrid returns the values, their negatives and 0, those in the range.
func signedGrid(values []int, r RangeInclusive) []int {
	ret := make([]int, 0, 2*len(values)+1)
	for _, v := range slices.Backward(values) {
		if -v >= r.Min {
			ret = append(ret, -v)
		}
	}
	if r.Min <= 0 && r.Max >= 0 {
		ret = append(ret, 0)
	}
	for _, v := range values {
		if v <= r.Max {
			ret = append(ret, v)
		}
	}
	return ret
}

// bufreadOffGrid tells if a position of the bufread unit that has a grid is
// not on it.
func (m *Model) bufreadOffGrid(unit *sointu.Unit) bool {
	for i, up := range sointu.UnitTypes["bufread"].Params {
		switch up.Name {
		case "start", "loopstart", "looplength":
		default:
			continue
		}
		p := Parameter{m: m, unit: unit, up: &sointu.UnitTypes["bufread"].Params[i], vtable: &bufferFrameParameter{}}
		if g, ok := bufreadGrid(&p); ok && !g.contains(unit.Parameters[up.Name]) {
			return true
		}
	}
	return false
}

// Step moves a position that has a grid by its values: all of them, or whole
// periods, straight notes and bars with large. Free positions and those in
// frames move by single values, or to the next grid value with large.
func (b *bufferFrameParameter) Step(p *Parameter, delta int, large bool) int {
	val := b.Value(p)
	if bufreadTapDelay(p) { // up is a longer delay
		delta = -delta
	}
	if g, ok := bufreadGrid(p); ok && !p.m.gridFree[p.unit.ID] {
		if large {
			return stepOnGrid(g.coarse, val, delta)
		}
		return stepOnGrid(g.values, val, delta)
	}
	if large {
		return b.RoundToGrid(p, val+delta, delta > 0)
	}
	return val + delta
}

func (b *bufferFrameParameter) Position(p *Parameter, value int) float32 {
	var pos float32
	if g, ok := bufreadGrid(p); ok {
		pos = g.position(value)
	} else {
		r := p.Range()
		pos = min(max(float32(value-r.Min)/float32(max(r.Max-r.Min, 1)), 0), 1)
	}
	if bufreadTapDelay(p) { // a longer delay further round
		return 1 - pos
	}
	return pos
}

func (b *bufferFrameParameter) Dragged(p *Parameter, start int, amount float32) int {
	if bufreadTapDelay(p) {
		amount = -amount
	}
	if g, ok := bufreadGrid(p); ok {
		return g.dragged(start, amount, p.m.gridFree[p.unit.ID])
	}
	r := p.Range()
	return int(float32(start) + amount*float32(r.Max-r.Min))
}
