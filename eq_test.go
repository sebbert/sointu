package sointu_test

import (
	"math"
	"math/cmplx"
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

func eqUnit(stereo, gain int, bands ...sointu.EQBand) sointu.Unit {
	return sointu.Unit{Type: "eq", Parameters: sointu.ParamMap{"stereo": stereo, "gain": gain}, Bands: bands}
}

func band(typ string, freq, gain, q float64) sointu.EQBand {
	return sointu.EQBand{Type: typ, Frequency: freq, Gain: gain, Q: q}
}

// eqProbe is a level measured with the Go synth: what the units did to a
// sine of the frequency.
type eqProbe struct{ freq, db float64 }

// measureEQ plays sines from 20 Hz to 20 kHz through the units with the Go
// synth, and returns what they did to the level of each. The instrument is
// a quiet sine, a push, the units and a stereo out: the left channel is the
// sine through the units and the right one the sine itself. Once the units
// have settled, the left channel is the right one times a and the right one
// of the sample before times b, which a least squares fit finds, and with
// the frequency ω of the sine, the level is |a + b·e^(-iω)|.
func measureEQ(t *testing.T, units []sointu.Unit) []eqProbe {
	t.Helper()
	const rows = 6 // of a note: 0.75 s
	var notes []byte
	for n := 8; n <= 127; n += 3 {
		notes = append(notes, byte(n))
	}
	pattern := make([]byte, 0, len(notes)*rows)
	for _, n := range notes {
		pattern = append(pattern, n)
		for range rows - 1 {
			pattern = append(pattern, 1)
		}
	}
	instr := []sointu.Unit{
		{Type: "oscillator", Parameters: sointu.ParamMap{"transpose": 72, "detune": 64, "color": 128, "shape": 64, "gain": 2}},
		{Type: "push", Parameters: sointu.ParamMap{}},
	}
	instr = append(instr, units...)
	instr = append(instr, sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}})
	song := sointu.Song{BPM: 120, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: len(pattern), Length: 1,
			Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern}}}},
		Patch: sointu.Patch{{NumVoices: 1, Units: instr}}}
	audio, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatal(err)
	}
	perRow := song.SamplesPerRow()
	var ret []eqProbe
	for i := range notes {
		seg := audio[i*rows*perRow : (i+1)*rows*perRow]
		seg = seg[len(seg)*2/3:] // settled
		var xx, xc, cc, yx, yc, x1 float64
		for n := 1; n < len(seg)-1; n++ {
			x, d, y := float64(seg[n][1]), float64(seg[n-1][1]), float64(seg[n][0])
			xx, xc, cc, yx, yc = xx+x*x, xc+x*d, cc+d*d, yx+y*x, yc+y*d
			x1 += x * (d + float64(seg[n+1][1]))
		}
		w := math.Acos(x1 / (2 * xx)) // x[n-1] + x[n+1] = 2·cos(ω)·x[n]
		det := xx*cc - xc*xc
		a, b := (yx*cc-yc*xc)/det, (yc*xx-yx*xc)/det
		ret = append(ret, eqProbe{w / (2 * math.Pi) * 44100, 20 * math.Log10(cmplx.Abs(complex(a, 0)+complex(b, 0)*cmplx.Rect(1, -w)))})
	}
	return ret
}

var eqTestBands = []struct {
	name string
	band sointu.EQBand
}{
	{"bell up", band(sointu.EQBell, 1000, 6, 1)},
	{"bell down narrow", band(sointu.EQBell, 250, -12, 8)},
	{"bell wide low", band(sointu.EQBell, 60, 4, 0.5)},
	{"bell high", band(sointu.EQBell, 9000, 9, 2)},
	{"lowcut", band(sointu.EQLowCut, 80, 0, 1)},
	{"lowcut flat", band(sointu.EQLowCut, 30, 0, 0.71)},
	{"lowcut resonant", band(sointu.EQLowCut, 200, 0, 4)},
	{"lowcut24", band(sointu.EQLowCut24, 40, 0, 0.71)},
	{"lowcut24 resonant", band(sointu.EQLowCut24, 120, 0, 1.5)},
	{"lowcut24 damped", band(sointu.EQLowCut24, 120, 0, 0.5)},
	{"highcut", band(sointu.EQHighCut, 5000, 0, 1)},
	{"highcut flat", band(sointu.EQHighCut, 2000, 0, 0.71)},
	{"highcut top", band(sointu.EQHighCut, 7300, 0, 0.71)},
	{"highcut24", band(sointu.EQHighCut24, 3000, 0, 0.71)},
	{"ladder", band(sointu.EQLadder, 8000, 0, 0.71)},
	{"ladder low", band(sointu.EQLadder, 500, 0, 0.5)},
	{"ladder resonant", band(sointu.EQLadder, 2000, 0, 2)},
	{"lowshelf up", band(sointu.EQLowShelf, 120, 6, 1)},
	{"lowshelf up flat", band(sointu.EQLowShelf, 120, 9, 0.71)},
	{"lowshelf down", band(sointu.EQLowShelf, 200, -6, 1)},
	{"highshelf up", band(sointu.EQHighShelf, 3000, 4, 1)},
	{"highshelf up 12", band(sointu.EQHighShelf, 2000, 12, 1)},
	{"highshelf down", band(sointu.EQHighShelf, 6000, -9, 1)},
	{"highshelf down flat", band(sointu.EQHighShelf, 4000, -3, 0.71)},
	{"notch", band(sointu.EQNotch, 1000, 0, 4)},
	{"bandpass", band(sointu.EQBandPass, 800, 0, 2)},
	{"bandpass wide", band(sointu.EQBandPass, 800, 0, 0.5)},
}

// TestEQResponseMatchesSynth checks the response that EQResponse computes
// for the units of each kind of band, and of all of them in a row, against
// what the Go synth does to sines: what the plot of the eq shows is what is
// heard.
func TestEQResponseMatchesSynth(t *testing.T) {
	check := func(t *testing.T, eq sointu.Unit) {
		c := eq.CompileEQ()
		worst := 0.0
		for _, p := range measureEQ(t, []sointu.Unit{eq}) {
			want := 20 * math.Log10(cmplx.Abs(c.Response(p.freq)))
			if want < -70 && p.db < -70 {
				continue // too quiet to measure
			}
			worst = max(worst, math.Abs(want-p.db))
			if math.Abs(want-p.db) > 0.03 {
				t.Errorf("%.1f Hz: computed %.3f dB, measured %.3f dB", p.freq, want, p.db)
			}
		}
		t.Logf("%d units, largest difference %.4f dB", len(c.Units), worst)
	}
	var all []sointu.EQBand
	for _, test := range eqTestBands {
		all = append(all, test.band)
		t.Run(test.name, func(t *testing.T) { check(t, eqUnit(0, 0, test.band)) })
	}
	t.Run("gain", func(t *testing.T) { check(t, eqUnit(0, -35, band(sointu.EQBell, 500, 3, 1))) })
	t.Run("all", func(t *testing.T) { check(t, eqUnit(0, 20, all[4:12]...)) })
}

func TestEQYAML(t *testing.T) {
	u := eqUnit(1, -15, band(sointu.EQBell, 1000, 3.5, 1.4), sointu.EQBand{Type: sointu.EQLowCut, Frequency: 30, Disabled: true})
	out, err := yaml.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	want := "bands:\n    - {type: bell, frequency: 1000, gain: 3.5, q: 1.4}\n    - {type: lowcut, frequency: 30, disabled: true}\n"
	if !strings.Contains(string(out), want) {
		t.Errorf("the bands are written as\n%s", out)
	}
	var back sointu.Unit
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u, back) {
		t.Errorf("read back %+v, wrote %+v", back, u)
	}
	c := u.Copy()
	c.Bands[0].Gain = 0
	if u.Bands[0].Gain != 3.5 {
		t.Error("the copy shares its bands")
	}
}

func unitTypes(units []sointu.Unit) string {
	var types []string
	for _, u := range units {
		types = append(types, u.Type)
	}
	return strings.Join(types, " ")
}

// TestEQUnits checks which units each kind of band stands for, and that
// bands that are off or do nothing stand for none.
func TestEQUnits(t *testing.T) {
	tests := []struct {
		band  sointu.EQBand
		units string
	}{
		{band(sointu.EQBell, 1000, 6, 1), "belleq"},
		{band(sointu.EQBell, 1000, 0, 1), ""},
		{band(sointu.EQBell, 1000, 0.2, 1), ""}, // the gain of belleq has steps of 0.625 dB
		{sointu.EQBand{Type: sointu.EQBell, Frequency: 1000, Gain: 6, Disabled: true}, ""},
		{band(sointu.EQLowCut, 80, 0, 1), "filter"},
		{band(sointu.EQLowCut, 80, 0, 3), "filter"},
		{band(sointu.EQLowCut, 80, 0, 0.71), "filter belleq"},
		{band(sointu.EQHighCut, 2000, 0, 1), "filter"},
		{band(sointu.EQHighCut, 2000, 0, 0.71), "filter belleq"},
		{band(sointu.EQLowCut24, 80, 0, 0.71), "filter belleq filter"},
		{band(sointu.EQLowCut24, 80, 0, 1.4), "filter filter"},
		{band(sointu.EQLowCut24, 80, 0, 0.4), "filter belleq filter belleq"},
		{band(sointu.EQHighCut24, 2000, 0, 0.71), "filter belleq filter"},
		{band(sointu.EQLadder, 8000, 0, 0.5), "ladder"},
		{band(sointu.EQLadder, 8000, 0, 0.71), "ladder invgain"}, // the resonance lowers the level
		{band(sointu.EQLowShelf, 100, 6, 1), "push filter gain addp"},
		{band(sointu.EQLowShelf, 100, 12, 1), "push filter invgain addp"},
		{band(sointu.EQLowShelf, 100, 6, 0.71), "push filter gain addp belleq"},
		{band(sointu.EQLowShelf, 100, -6, 1), "push filter gain addp gain"},
		{band(sointu.EQLowShelf, 100, 0, 1), ""},
		{band(sointu.EQHighShelf, 2000, 6, 1), "push filter gain addp"},
		{band(sointu.EQHighShelf, 2000, -6, 1), "push filter invgain addp gain"},
		{band(sointu.EQNotch, 1000, 0, 4), "filter"},
		{band(sointu.EQBandPass, 1000, 0, 1), "filter"},
		{band(sointu.EQBandPass, 1000, 0, 4), "filter gain"},
		{sointu.EQBand{Type: "nonsense", Frequency: 1000, Gain: 6}, "belleq"},
	}
	for _, test := range tests {
		for stereo := range 2 {
			u := eqUnit(stereo, 0, test.band)
			c := u.CompileEQ()
			if got := unitTypes(c.Units); got != test.units {
				t.Errorf("%+v stands for %q, not %q", test.band, got, test.units)
			}
			if n := u.NumEQUnits(); n != len(c.Units) {
				t.Errorf("%+v: NumEQUnits is %d for %d units", test.band, n, len(c.Units))
			}
			for _, x := range c.Units {
				if x.Parameters["stereo"] != stereo {
					t.Errorf("%+v, stereo %d: the %s unit has stereo %d", test.band, stereo, x.Type, x.Parameters["stereo"])
				}
				for _, p := range sointu.UnitTypes[x.Type].Params {
					if v, ok := x.Parameters[p.Name]; !ok || v < p.MinValue || v > p.MaxValue {
						t.Errorf("%+v: %s of the %s unit is %d (set: %v)", test.band, p.Name, x.Type, v, ok)
					}
				}
				if x.Type == "ladder" != c.Bands[0].GoWasmOnly && x.Type != "invgain" {
					t.Errorf("%+v: GoWasmOnly is %v with a %s unit", test.band, c.Bands[0].GoWasmOnly, x.Type)
				}
			}
		}
	}
	// the gain of the eq and what the bands leave to it are one unit
	u := eqUnit(1, -30, band(sointu.EQBandPass, 1000, 0, 2), band(sointu.EQLowShelf, 100, -6, 1), band(sointu.EQBell, 3000, 3, 1))
	c := u.CompileEQ()
	if got, want := unitTypes(c.Units), "filter push filter gain addp belleq gain"; got != want {
		t.Errorf("three bands and a gain stand for %q, not %q", got, want)
	}
	if want := math.Pow(10, -3.0/20) / 2 / 2; math.Abs(c.Gain/want-1) > 0.01 || math.Abs(c.ActualGain/want-1) > 0.02 {
		t.Errorf("the gain unit should have the gain %.4f: it should have %.4f and has %.4f", want, c.Gain, c.ActualGain)
	}
	if n := len(c.Bands[0].Units) + len(c.Bands[1].Units) + len(c.Bands[2].Units) + len(c.GainUnits); n != len(c.Units) {
		t.Errorf("the bands and the gain have %d units of %d", n, len(c.Units))
	}
	// the units are the caller's to change: compiled bands are kept, and
	// must not change with them
	c.Units[0].Parameters["frequency"] = 1
	if again := u.CompileEQ(); again.Units[0].Parameters["frequency"] == 1 {
		t.Error("changing the units of a compiled eq changed what it compiles to")
	}
	// a gain of the eq alone
	u = eqUnit(0, 60)
	if got := unitTypes(u.CompileEQ().Units); got != "invgain" {
		t.Errorf("a gain of 6 dB stands for %q", got)
	}
}

// TestEQShapes checks the bands against the filters they are meant to be:
// where the units can do them, and how far off they are where they cannot.
func TestEQShapes(t *testing.T) {
	// the level of s²+as+b over s²+cs+d at s = i·w, in dB
	biquad := func(n2, n1, n0, d1 float64) func(w float64) float64 {
		return func(w float64) float64 {
			return 20 * math.Log10(cmplx.Abs(complex(n0-n2*w*w, n1*w)/complex(1-w*w, d1*w)))
		}
	}
	butter4 := func(highpass bool) func(w float64) float64 {
		return func(w float64) float64 {
			if highpass {
				w = 1 / w
			}
			return -10 * math.Log10(1+math.Pow(w, 8))
		}
	}
	tests := []struct {
		name     string
		band     sointu.EQBand
		ideal    func(w float64) float64 // w is the frequency over that of the band, as its units have it
		from, to float64                 // Hz
		within   float64                 // dB
	}{
		{"lowcut Q 1", band(sointu.EQLowCut, 100, 0, 1), biquad(1, 0, 0, 1), 20, 16000, 0.15},
		{"lowcut Q 0.71", band(sointu.EQLowCut, 100, 0, 0.7071), biquad(1, 0, 0, math.Sqrt2), 20, 16000, 0.5},
		{"lowcut24", band(sointu.EQLowCut24, 100, 0, 0.7071), butter4(true), 40, 16000, 0.6},
		{"highcut Q 1", band(sointu.EQHighCut, 1000, 0, 1), biquad(0, 0, 1, 1), 20, 4000, 1},
		{"highcut Q 0.71", band(sointu.EQHighCut, 1000, 0, 0.7071), biquad(0, 0, 1, math.Sqrt2), 20, 4000, 1},
		{"highcut24", band(sointu.EQHighCut24, 1000, 0, 0.7071), butter4(false), 20, 3000, 1.5},
		{"notch", band(sointu.EQNotch, 500, 0, 2), biquad(1, 0, 1, 0.5), 20, 16000, 0.5},
		{"bandpass", band(sointu.EQBandPass, 500, 0, 2), biquad(0, 0.5, 0, 0.5), 20, 4000, 0.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := eqUnit(0, 0, test.band)
			c := u.CompileEQ()
			worst, at := 0.0, 0.0
			for f := test.from; f <= test.to; f *= 1.05 {
				got := 20 * math.Log10(cmplx.Abs(c.Response(f)))
				want := test.ideal(f / c.Bands[0].Actual.Frequency)
				if want < -50 {
					continue
				}
				if d := math.Abs(got - want); d > worst {
					worst, at = d, f
				}
			}
			t.Logf("%d units, at most %.2f dB off, at %.0f Hz", len(c.Units), worst, at)
			if worst > test.within {
				t.Errorf("%.2f dB off at %.0f Hz", worst, at)
			}
		})
	}
	// the shelves: the gain at both ends, the middle at the frequency, and
	// how far the level goes beyond the shelf
	for _, test := range []struct {
		band      sointu.EQBand
		overshoot float64
	}{
		{band(sointu.EQLowShelf, 100, 6, 1), 1.6},
		{band(sointu.EQLowShelf, 100, 6, 0.7071), 0.3},
		{band(sointu.EQLowShelf, 100, -6, 1), 1.6},
		{band(sointu.EQLowShelf, 300, 12, 1), 2.5},
		{band(sointu.EQHighShelf, 2000, 6, 1), 0.7},
		{band(sointu.EQHighShelf, 2000, -6, 1), 2},
		{band(sointu.EQHighShelf, 1500, 12, 1), 1.2},
	} {
		u := eqUnit(0, 0, test.band)
		c := u.CompileEQ()
		db := func(f float64) float64 { return 20 * math.Log10(cmplx.Abs(c.Response(f))) }
		low, high := db(10), db(16000)
		flat, shelf := high, low
		if test.band.Type == sointu.EQHighShelf {
			flat, shelf = low, high
		}
		if math.Abs(flat) > 0.25 || math.Abs(shelf-test.band.Gain) > 0.25 {
			t.Errorf("%+v: %.2f dB at 10 Hz and %.2f dB at 16 kHz", test.band, low, high)
		}
		if mid := db(test.band.Frequency); math.Abs(mid-test.band.Gain/2) > 0.5 {
			t.Errorf("%+v: %.2f dB at its frequency", test.band, mid)
		}
		lo, hi := math.Min(0, test.band.Gain), math.Max(0, test.band.Gain)
		over := 0.0
		for f := 20.0; f <= 16000; f *= 1.05 {
			over = max(over, db(f)-hi, lo-db(f))
		}
		t.Logf("%+v: %d units, %.2f dB beyond the shelf", test.band, len(c.Units), over)
		if over > test.overshoot {
			t.Errorf("%+v: %.2f dB beyond the shelf", test.band, over)
		}
	}
}

// TestEQShelfRange checks that a shelf beyond what the filter unit reaches
// is the nearest shelf that it can do, with the gain asked for.
func TestEQShelfRange(t *testing.T) {
	for _, b := range []sointu.EQBand{
		band(sointu.EQHighShelf, 22000, 12, 1), band(sointu.EQHighShelf, 22000, -6, 1),
		band(sointu.EQLowShelf, 22000, 6, 1), band(sointu.EQLowShelf, 10, 6, 1), band(sointu.EQHighShelf, 10, -6, 1),
	} {
		u := eqUnit(0, 0, b)
		a := u.CompileEQ().Bands[0].Actual
		lo, hi := 12.0, 25.0
		if b.Frequency > 1000 {
			lo, hi = 6000, 9000
		}
		if a.Frequency < lo || a.Frequency > hi || math.Abs(a.Gain-b.Gain) > 0.3 {
			t.Errorf("%+v is the shelf %+v", b, a)
		}
	}
}

// TestEQShelfQ checks that shelves are where they were put, with the gain
// asked for, whatever their Q: unless they are high up and so damped that
// they have not ended at 16 kHz.
func TestEQShelfQ(t *testing.T) {
	for _, typ := range []string{sointu.EQLowShelf, sointu.EQHighShelf} {
		for _, f := range []float64{60, 180, 600, 2000} {
			for _, g := range []float64{-12, -3, 3, 9} {
				for _, q := range []float64{0.4, 0.5, 0.71, 1, 2, 4} {
					if f >= 2000 && q < 0.7 {
						continue // so damped that it has not ended at 16 kHz
					}
					b := band(typ, f, g, q)
					u := eqUnit(0, 0, b)
					a := u.CompileEQ().Bands[0].Actual
					if r := a.Frequency / f; r < 0.8 || r > 1.25 || math.Abs(a.Gain-g) > 0.3 {
						t.Errorf("%+v is the shelf %+v", b, a)
					}
				}
			}
		}
	}
}

// TestEQExpand checks that Song.Expand replaces the eq units of instruments
// and modules with their units, and leaves songs without them alone.
func TestEQExpand(t *testing.T) {
	osc := unit("oscillator", 1, map[string]int{"gain": 64})
	out := unit("out", 2, map[string]int{"stereo": 0, "gain": 64})
	eq := eqUnit(0, 0, band(sointu.EQLowCut, 80, 0, 1), band(sointu.EQBell, 1000, 6, 1))
	eq.ID = 3
	song := sointu.Song{BPM: 100, RowsPerBeat: 4, Patch: sointu.Patch{
		{NumVoices: 1, Units: []sointu.Unit{osc, eq, out}},
		{NumVoices: 1, Units: []sointu.Unit{osc, out}},
	}}
	if !song.NeedsExpand() || !song.HasEQs() {
		t.Fatal("a song with an eq unit needs no expanding")
	}
	expanded, exp := song.Expand()
	if len(exp.Problems) > 0 {
		t.Fatal(exp.Problems)
	}
	if got := unitTypes(expanded.Patch[0].Units); got != "oscillator filter belleq out" {
		t.Errorf("the instrument expands to %q", got)
	}
	if got := unitTypes(song.Patch[0].Units); got != "oscillator eq out" {
		t.Errorf("expanding changed the song: %q", got)
	}
	if expanded.NeedsExpand() {
		t.Error("the expanded song needs expanding")
	}
	// off, flat and disabled: nothing
	for i, u := range []sointu.Unit{
		eqUnit(0, 0),
		eqUnit(0, 0, band(sointu.EQBell, 1000, 0, 1), sointu.EQBand{Type: sointu.EQLowCut, Frequency: 80, Disabled: true}),
		{Type: "eq", Parameters: sointu.ParamMap{"stereo": 0}, Bands: eq.Bands, Disabled: true},
	} {
		s := sointu.Song{BPM: 100, RowsPerBeat: 4, Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{osc, u, out}}}}
		e, _ := s.Expand()
		if got := unitTypes(e.Patch[0].Units); got != "oscillator out" {
			t.Errorf("%d: an eq that does nothing expands to %q", i, got)
		}
	}
	// in a module, used twice
	mod := sointu.Module{ID: 1, Name: "tone", Inputs: 1, Units: []sointu.Unit{eq}}
	call := unit("module", 4, map[string]int{"module": 1})
	call2 := unit("module", 5, map[string]int{"module": 1})
	s := sointu.Song{BPM: 100, RowsPerBeat: 4, Modules: sointu.Modules{mod},
		Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{osc, call, call2, out}}}}
	e, exp := s.Expand()
	if len(exp.Problems) > 0 {
		t.Fatal(exp.Problems)
	}
	if got := unitTypes(e.Patch[0].Units); got != "oscillator filter belleq filter belleq out" {
		t.Errorf("an eq in a module expands to %q", got)
	}
	if got := unitTypes(s.Modules[0].Units); got != "eq" {
		t.Errorf("expanding changed the module: %q", got)
	}
	if n := s.Modules.NumExpandedUnits(s.Patch[0].Units); n != 6 {
		t.Errorf("the instrument counts as %d units, not 6", n)
	}
	if sointu.CanBind("eq", "gain") {
		t.Error("a parameter of an eq unit can be bound")
	}
	// a song without eq units is returned as it is
	plain := sointu.Song{BPM: 100, RowsPerBeat: 4, Patch: sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{osc, out}}}}
	e, _ = plain.Expand()
	if &e.Patch[0] != &plain.Patch[0] {
		t.Error("a song without eq units was copied")
	}
}
