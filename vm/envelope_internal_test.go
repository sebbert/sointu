package vm

import (
	"math"
	"testing"

	"github.com/vsariola/sointu"
)

// renderEnvelope renders n samples of an envelope with gain 1, triggered at
// sample 0 and released at sample releaseAt.
func renderEnvelope(t *testing.T, attack, decay, sustain, release, curve, releaseAt, n int) []float32 {
	t.Helper()
	patch := sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
		{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": attack, "decay": decay, "sustain": sustain, "release": release, "gain": 128, "curve": curve}},
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
	}}}
	synth, err := GoSynther{}.Synth(patch, 120)
	if err != nil {
		t.Fatal(err)
	}
	synth.Trigger(0, 60)
	buffer := make(sointu.AudioBuffer, n)
	for pos := 0; pos < n; {
		end := n
		if pos < releaseAt {
			end = min(releaseAt, n)
		}
		if pos == releaseAt {
			synth.Release(0)
		}
		samples, _, err := synth.Render(buffer[pos:end], end-pos)
		if err != nil {
			t.Fatal(err)
		}
		pos += samples
	}
	ret := make([]float32, n)
	for i := range buffer {
		ret[i] = buffer[i][0]
	}
	return ret
}

// linearEnvelope is the envelope before the curve parameter.
func linearEnvelope(attack, decay, sustain, release, releaseAt, n int) []float32 {
	params := []float32{float32(attack) / 128, float32(decay) / 128, float32(sustain) / 128, float32(release) / 128}
	state, level := envStateAttack, float32(0)
	ret := make([]float32, n)
	for i := range ret {
		if i >= releaseAt {
			state = envStateRelease
		}
		delta := nonLinearMap(params[state])
		switch state {
		case envStateAttack:
			level += delta
			if level >= 1 {
				level, state = 1, envStateDecay
			}
		case envStateDecay:
			level -= delta
			if level <= params[2] {
				level, state = params[2], envStateSustain
			}
		case envStateRelease:
			level -= delta
			if level <= 0 {
				level = 0
			}
		}
		ret[i] = level
	}
	return ret
}

func TestEnvelopeCurveZeroIsLinear(t *testing.T) {
	for _, c := range []struct{ attack, decay, sustain, release, releaseAt int }{
		{40, 48, 64, 48, 1000}, // through every stage
		{60, 48, 64, 50, 1000}, // released in the attack
		{30, 60, 20, 44, 500},  // released in the decay
		{0, 0, 128, 128, 100},  // instant attack and decay, sustain 1
		{16, 16, 0, 16, 300},
	} {
		got := renderEnvelope(t, c.attack, c.decay, c.sustain, c.release, 0, c.releaseAt, 4000)
		want := linearEnvelope(c.attack, c.decay, c.sustain, c.release, c.releaseAt, 4000)
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%+v: sample %v is %v, want %v", c, i, got[i], want[i])
				break
			}
		}
	}
}

// firstAt returns the index of the first sample from start whose value
// satisfies f, or -1.
func firstAt(x []float32, start int, f func(float32) bool) int {
	for i := start; i < len(x); i++ {
		if f(x[i]) {
			return i
		}
	}
	return -1
}

func TestEnvelopeCurveStages(t *testing.T) {
	// attack 40: 2^7.5 = 181 samples, decay 48 to sustain 0.5: 256
	// samples, release 48 from 0.5: 256 samples
	const attack, decay, sustain, release, releaseAt = 40, 48, 64, 48, 1000
	type shape struct{ attack, decay, release float32 } // levels half way through each stage
	var prev shape
	for _, curve := range []int{0, 1, 16, 32, 64, 96, 128} {
		x := renderEnvelope(t, attack, decay, sustain, release, curve, releaseAt, 2000)
		attackEnd := firstAt(x, 0, func(v float32) bool { return v == 1 })
		decayEnd := firstAt(x, attackEnd, func(v float32) bool { return v == 0.5 })
		releaseEnd := firstAt(x, releaseAt, func(v float32) bool { return v == 0 })
		// stages reach their end values and last about as long as linear ones
		for _, s := range []struct {
			name       string
			start, end int
			want       float64
		}{{"attack", -1, attackEnd, 181.02}, {"decay", attackEnd, decayEnd, 256}, {"release", releaseAt - 1, releaseEnd, 256}} {
			if s.end < 0 {
				t.Errorf("curve %v: the %v does not end", curve, s.name)
				continue
			}
			if d := float64(s.end - s.start); d < s.want*0.99 || d > s.want*1.01+1 {
				t.Errorf("curve %v: the %v takes %v samples, want about %v", curve, s.name, d, s.want)
			}
		}
		if x[releaseAt-1] != 0.5 {
			t.Errorf("curve %v: the sustain is %v, want 0.5", curve, x[releaseAt-1])
		}
		// the shape: fast then slow; more so as the curve grows
		sh := shape{x[90], x[attackEnd+128], x[releaseAt+127]}
		if curve == 0 {
			if sh.attack < 0.49 || sh.attack > 0.51 || sh.decay < 0.74 || sh.decay > 0.76 || sh.release < 0.24 || sh.release > 0.26 {
				t.Errorf("curve 0: half way levels %+v, want linear", sh)
			}
		} else if sh.attack <= prev.attack || sh.decay >= prev.decay || sh.release >= prev.release {
			t.Errorf("curve %v: half way levels %+v do not bend more than %+v", curve, sh, prev)
		}
		prev = sh
	}
	if prev.attack < 0.9 || prev.decay > 0.55 || prev.release > 0.05 {
		t.Errorf("curve 128: half way levels %+v, want strongly exponential", prev)
	}
}

func TestEnvelopeCurveReleaseEarly(t *testing.T) {
	// released in the attack and in the decay, the release starts from the
	// level reached and takes about as long as a linear one from there
	for _, releaseAt := range []int{60, 300} {
		for _, curve := range []int{0, 64, 128} {
			x := renderEnvelope(t, 40, 48, 64, 48, curve, releaseAt, 2000)
			start := x[releaseAt-1]
			if x[releaseAt] >= start {
				t.Errorf("curve %v, released at %v: the release does not fall from %v", curve, releaseAt, start)
			}
			end := firstAt(x, releaseAt, func(v float32) bool { return v == 0 })
			want := float64(start) * 512
			if d := float64(end - releaseAt + 1); end < 0 || d < want*0.99 || d > want*1.01+1 {
				t.Errorf("curve %v, released at %v from %v: the release takes %v samples, want about %v", curve, releaseAt, start, d, want)
			}
		}
	}
}

func TestExp2m1f(t *testing.T) {
	for _, y := range []float32{-126, -30, -3.7, -1, -0.5, -0.1, -1e-3, -1e-7, -1e-20, 0, 1e-20, 1e-7, 1e-3, 0.1, 0.5, 0.75, 1, 3.3, 12, 40} {
		want := math.Expm1(float64(y) * math.Ln2)
		// relative to 2^y - 1, or, for large y, like exp2f, relative to 2^y
		got := float64(exp2m1f(y))
		if d := math.Abs(got - want); d > 3e-7*math.Abs(want) && d > 2e-7*(want+1) {
			t.Errorf("exp2m1f(%v) = %v, want %v", y, got, want)
		}
	}
}

func TestEnvelopeCurveSlowStages(t *testing.T) {
	// attack 128 takes 2^24 samples; with any curve it moves from the
	// start, at about 2^-24·(1+1/(2^c-1))·c·ln 2 per sample
	for _, curve := range []int{1, 64, 128} {
		x := renderEnvelope(t, 128, 128, 64, 128, curve, 1<<20, 10000)
		c := 12 * math.Pow(float64(curve)/128, 2)
		want := 10000 * math.Pow(2, -24) * (1 + 1/math.Expm1(c*math.Ln2)) * c * math.Ln2
		if got := float64(x[9999]); math.Abs(got-want) > want*0.01 {
			t.Errorf("curve %v: attack 128 is at %v after 10000 samples, want %v", curve, got, want)
		}
	}
}
