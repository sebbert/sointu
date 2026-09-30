package vm

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

// mcSong is a song with one instrument, which gets an impulse at the start of
// the song, the first row of its only pattern, and runs the units after it
// for rows rows.
func mcSong(rows int, units ...sointu.Unit) sointu.Song {
	pattern := make(sointu.Pattern, rows)
	pattern[0] = 60
	for i := 1; i < rows; i++ {
		pattern[i] = 1
	}
	impulse := sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 0, "sustain": 0, "release": 0, "gain": 128}}
	return sointu.Song{BPM: 120, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: rows, Length: 1, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern}}}},
		Patch: sointu.Patch{{Name: "mc", NumVoices: 1, Units: append([]sointu.Unit{impulse}, units...)}},
	}
}

func mcUnit(typ string, params sointu.ParamMap) sointu.Unit {
	u := sointu.MakeUnit(typ)
	u.Parameters["bus"] = 1
	for k, v := range params {
		u.Parameters[k] = v
	}
	return u
}

func renderMC(t *testing.T, song sointu.Song) sointu.AudioBuffer {
	t.Helper()
	buf, err := sointu.Play(GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}
	for i, f := range buf {
		for _, x := range f {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				t.Fatalf("frame %d is %v", i, f)
			}
		}
	}
	return buf
}

func TestMCSpreadSumPassesThrough(t *testing.T) {
	for stereo := 0; stereo <= 1; stereo++ {
		song := mcSong(2,
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine}},
			sointu.Unit{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			mcUnit("mcspread", nil),
			mcUnit("mcsum", sointu.ParamMap{"stereo": stereo}),
			sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": stereo, "gain": 128}})
		// compare with the same without the mc units
		ref := song
		ref.Patch = song.Patch.Copy()
		units := ref.Patch[0].Units
		ref.Patch[0].Units = units[:3:3]
		if stereo == 1 { // mono in, the same on both channels out
			ref.Patch[0].Units = append(ref.Patch[0].Units, sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 0}})
		}
		ref.Patch[0].Units = append(ref.Patch[0].Units, units[5])
		want, got := renderMC(t, ref), renderMC(t, song)
		for i := range want {
			if math.Abs(float64(got[i][0]-want[i][0])) > 1e-6 || math.Abs(float64(got[i][1]-want[i][1])) > 1e-6 {
				t.Fatalf("stereo %d, frame %d: got %v, want %v", stereo, i, got[i], want[i])
			}
		}
	}
}

func TestMCMixPreservesEnergy(t *testing.T) {
	x := [sointu.MCChannels]float32{0.3, -0.7, 0.1, 0.9, -0.2, 0.5, 0.05, -0.4}
	energy := func(v [sointu.MCChannels]float32) (e float64) {
		for _, a := range v {
			e += float64(a) * float64(a)
		}
		return e
	}
	for typ := range byte(3) {
		for seed := range 4 {
			y := x
			mcmix(&y, typ, newMCShuffle(seed))
			if e, want := energy(y), energy(x); math.Abs(e-want) > 1e-5 {
				t.Errorf("type %d, seed %d: energy %v, want %v", typ, seed, e, want)
			}
		}
	}
	// a shuffle is a permutation
	s := newMCShuffle(7)
	seen := map[int]bool{}
	for _, c := range s.Source {
		seen[c] = true
	}
	if len(seen) != sointu.MCChannels {
		t.Errorf("shuffle sources %v are not a permutation", s.Source)
	}
}

func TestMCDelayLengths(t *testing.T) {
	d := newMCDelay(sointu.ParamMap{"size": 1000, "spread": 64, "seed": 3})
	for c, l := range d.Lengths {
		if l < 4410/2 || l > 4410 {
			t.Errorf("length %d is %v, want between 2205 and 4410", c, l)
		}
		for k := range c {
			if d.Lengths[k] == l {
				t.Errorf("lengths %d and %d are both %v", k, c, l)
			}
		}
	}
	if other := newMCDelay(sointu.ParamMap{"size": 1000, "spread": 64, "seed": 4}); other.Lengths == d.Lengths {
		t.Errorf("seeds 3 and 4 give the same lengths")
	}
}

// fdn is a feedback delay network of one mcdelay with the given parameters
// and a Householder mix, with the tail summed to stereo.
func fdn(delay sointu.ParamMap) []sointu.Unit {
	return []sointu.Unit{
		mcUnit("mcspread", nil),
		mcUnit("mcloop", nil),
		mcUnit("mcdelay", delay),
		mcUnit("mcsum", nil),
		mcUnit("mcmix", sointu.ParamMap{"type": sointu.MCMixHouseholder}),
		mcUnit("mcloopend", nil),
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}
}

// decayRate returns the slope of the energy of the signal between frames
// from and to, in dB per second, from a least squares fit of the energy of
// windows of 50 ms.
func decayRate(buf sointu.AudioBuffer, from, to int) float64 {
	const w = 2205
	var sx, sy, sxx, sxy, n float64
	for i := from; i+w <= to; i += w {
		e := 0.0
		for _, f := range buf[i : i+w] {
			e += float64(f[0])*float64(f[0]) + float64(f[1])*float64(f[1])
		}
		x, y := float64(i)/44100, 10*math.Log10(e)
		sx, sy, sxx, sxy, n = sx+x, sy+y, sxx+x*x, sxy+x*y, n+1
	}
	return (n*sxy - sx*sy) / (n*sxx - sx*sx)
}

func TestMCDecayTime(t *testing.T) {
	// decay 64 is 1 second: -60 dB per second; with allpasses roughly
	for _, allpass := range []int{0, 1} {
		buf := renderMC(t, mcSong(16, fdn(sointu.ParamMap{"size": 800, "spread": 64, "seed": 1, "decay": 64, "hfdecay": 128, "allpass": allpass})...))
		if rate := decayRate(buf, 44100/4, 44100*3/2); math.Abs(rate+60) > float64(3+6*allpass) {
			t.Errorf("allpass %d: decays at %.1f dB/s, want -60", allpass, rate)
		}
	}
	// decay 80 is 2 seconds
	buf := renderMC(t, mcSong(16, fdn(sointu.ParamMap{"size": 800, "spread": 64, "seed": 1, "decay": 80, "hfdecay": 128})...))
	if rate := decayRate(buf, 44100/4, 44100*3/2); math.Abs(rate+30) > 4 {
		t.Errorf("decays at %.1f dB/s, want -30", rate)
	}
}

func TestMCDecayOffSustains(t *testing.T) {
	// without decay, the loop loses nothing, and with modulation only a
	// little, from the linear interpolation
	for _, c := range []struct {
		moddepth int
		rate     float64
	}{{0, 0}, {20, -3}} {
		buf := renderMC(t, mcSong(16, fdn(sointu.ParamMap{"size": 300, "spread": 64, "seed": 1, "moddepth": c.moddepth})...))
		if rate := decayRate(buf, 44100/4, 44100*3/2); math.Abs(rate-c.rate) > 1 {
			t.Errorf("moddepth %d: decays at %.1f dB/s, want %.0f", c.moddepth, rate, c.rate)
		}
	}
}

func TestMCHighsDecayFaster(t *testing.T) {
	// energy above 3 kHz relative to the whole, early and late in the tail
	highRatio := func(buf sointu.AudioBuffer, from int) float64 {
		var all, high float64
		var prev float32
		for _, f := range buf[from : from+4410] {
			all += float64(f[0]) * float64(f[0])
			d := float64(f[0] - prev) // a crude high-pass
			high += d * d
			prev = f[0]
		}
		return high / all
	}
	buf := renderMC(t, mcSong(16, fdn(sointu.ParamMap{"size": 600, "spread": 64, "seed": 2, "decay": 80, "hfdecay": 32})...))
	if early, late := highRatio(buf, 4410), highRatio(buf, 44100); late > early*0.5 {
		t.Errorf("high frequencies %.3g early and %.3g late, want them to fade", early, late)
	}
}

func TestMCRunsInFirstVoiceOnly(t *testing.T) {
	// the note of the first row plays in the second voice, whose mc units
	// do nothing: mcspread pops and mcsum pushes 0; the note of the second
	// row plays in the first voice
	song := mcSong(2, mcUnit("mcspread", nil), mcUnit("mcsum", sointu.ParamMap{"stereo": 0}), sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}})
	song.Patch[0].NumVoices = 2
	song.Score.Tracks[0].NumVoices = 2
	song.Score.Tracks[0].Patterns[0][1] = 60
	buf := renderMC(t, song)
	row := song.SamplesPerRow()
	for i, f := range buf {
		if want := float32(0); i == row {
			want = 1
			if f[0] != want {
				t.Fatalf("frame %d: %v, want %v", i, f[0], want)
			}
		} else if i < row && f[0] != 0 {
			t.Fatalf("frame %d: %v, want 0", i, f[0])
		}
	}
}

func TestMCUnitLevels(t *testing.T) {
	song := mcSong(2, fdn(sointu.ParamMap{"size": 300, "decay": 64})...)
	song.Patch[0].Units[3].ID = 42 // the mcdelay
	synth, err := GoSynther{}.Synth(song.Patch, song.BPM)
	if err != nil {
		t.Fatal(err)
	}
	synth.Trigger(0, 60)
	buf := make(sointu.AudioBuffer, 44100/10)
	if _, _, err := synth.Render(buf, len(buf)); err != nil {
		t.Fatal(err)
	}
	levels := synth.(*GoSynth).UnitLevels(42, nil)
	if len(levels) != sointu.MCChannels {
		t.Fatalf("got %d levels, want 8", len(levels))
	}
	for c, l := range levels {
		if l <= 0 {
			t.Errorf("channel %d has level %v", c, l)
		}
	}
	if again := synth.(*GoSynth).UnitLevels(42, nil); again[0] != 0 {
		t.Errorf("levels not reset after reporting: %v", again)
	}
}
