package compiler_test

import (
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// The module presets Ducker, Sidechain, Ping pong delay, Ducking reverb and
// Ducking delay that the tracker comes with: what they do, measured on what
// the Go synth renders, and that the wasm player renders them like it.

// modulePreset reads a module preset that the tracker comes with: the
// module, last, and before it the modules that it uses.
func modulePreset(t *testing.T, name string) sointu.Modules {
	t.Helper()
	data, err := os.ReadFile("../../tracker/modules/" + name + ".yml")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Modules sointu.Modules }
	if err := yaml.Unmarshal(data, &file); err != nil || len(file.Modules) == 0 {
		t.Fatalf("reading the preset %s: %v, %d modules", name, err, len(file.Modules))
	}
	return file.Modules
}

// duckingSong returns a song of 120 beats per minute and 4 rows per beat
// (5512.5 samples per row), with one track of one voice for each instrument,
// playing the pattern of 16 rows given for it and then silence up to length
// patterns.
func duckingSong(length int, patterns []sointu.Pattern, patch sointu.Patch, modules sointu.Modules) sointu.Song {
	order := make(sointu.Order, length)
	for i := 1; i < length; i++ {
		order[i] = 1
	}
	tracks := make([]sointu.Track, len(patterns))
	for i, p := range patterns {
		if p == nil {
			p = make(sointu.Pattern, 16)
		}
		tracks[i] = sointu.Track{NumVoices: 1, Order: order, Patterns: []sointu.Pattern{p, make(sointu.Pattern, 16)}}
	}
	return sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 16, Length: length, Tracks: tracks}, Patch: patch, Modules: modules}
}

func playGo(t *testing.T, song sointu.Song) sointu.AudioBuffer {
	t.Helper()
	if _, x := song.Expand(); len(x.Problems) > 0 {
		t.Fatalf("problems expanding the song: %v", x.Problems)
	}
	buffer, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}
	return buffer
}

// level returns the root mean square of both channels of the frames.
func level(frames sointu.AudioBuffer) float64 {
	sum := 0.0
	for _, f := range frames {
		sum += float64(f[0])*float64(f[0]) + float64(f[1])*float64(f[1])
	}
	return math.Sqrt(sum / float64(2*max(len(frames), 1)))
}

func moduleCall(id int, params sointu.ParamMap) sointu.Unit {
	p := sointu.ParamMap{"module": id}
	for k, v := range params {
		p[k] = v
	}
	return sointu.Unit{Type: "module", ID: 1000, Parameters: p}
}

// A constant 1 on aux 4/5, which the instrument after it reads, sends through
// the units and out: the output is the gain of the units.
func gainSong(pattern sointu.Pattern, modules sointu.Modules, units ...sointu.Unit) sointu.Song {
	kick := []sointu.Unit{{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 4}}}
	kick = append(kick, units...)
	kick = append(kick, sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}})
	return duckingSong(1, []sointu.Pattern{nil, pattern}, sointu.Patch{
		{Name: "bus", NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 1, "value": 128}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 4}},
		}},
		{Name: "kick", NumVoices: 1, Units: kick},
	}, modules)
}

const duckingRow = 5512.5 // samples per row of duckingSong

var heldFromRow2 = sointu.Pattern{0, 0, 60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

// TestDuckerGain checks the gain of the Ducker module: 1 until the note of
// its instrument, then down to 1 - depth within the attack of 1 ms, without
// a step, and back to 1 in the release time, sooner with a curve.
func TestDuckerGain(t *testing.T) {
	t.Parallel()
	mods := modulePreset(t, "Ducker")
	render := func(params sointu.ParamMap) []float32 {
		buffer := playGo(t, gainSong(heldFromRow2, mods, moduleCall(mods[0].ID, params)))
		gain := make([]float32, len(buffer))
		for i, f := range buffer {
			if f[0] != f[1] {
				t.Fatalf("frame %d: left %v, right %v", i, f[0], f[1])
			}
			gain[i] = f[0]
		}
		return gain
	}
	start := int(2*duckingRow) - 1 // the row starts a sample early or on time
	at := func(ms float64) int { return start + int(ms*44.1) }
	for _, c := range []struct {
		name          string
		params        sointu.ParamMap
		low           float64 // the lowest gain
		after50, back float64 // the gain 50 ms after the note, and when it is back within 0.1 %, in ms
	}{
		{"defaults", nil, 0, 0.24, 204},
		{"depth 64", sointu.ParamMap{"p1": 64}, 0.5, 0.62, 204},
		{"release 64", sointu.ParamMap{"p2": 64}, 0, 0.53, 94},
		{"curve 96", sointu.ParamMap{"p3": 96}, 0, 0.69, 204},
	} {
		gain := render(c.params)
		for i := 0; i < start; i++ {
			if gain[i] != 1 {
				t.Fatalf("%s: gain %v at frame %d, before the note at %d", c.name, gain[i], i, start)
			}
		}
		low, step := 1.0, 0.0
		for i := start; i < len(gain); i++ {
			low = min(low, float64(gain[i]))
			step = max(step, math.Abs(float64(gain[i]-gain[i-1])))
		}
		if math.Abs(low-c.low) > 1e-3 || math.Abs(float64(gain[at(1.5)])-c.low) > 0.02 {
			t.Errorf("%s: lowest gain %.4f, %.4f after 1.5 ms, want %.4f", c.name, low, gain[at(1.5)], c.low)
		}
		// the attack is 49 samples: a linear one steps by depth/49, a curved one by more at first
		if limit := 0.021 * (1 - c.low) * 5; step > limit || (c.name != "curve 96" && step > 0.021*(1-c.low)) {
			t.Errorf("%s: the gain steps by %.4f", c.name, step)
		}
		if g := float64(gain[at(50)]); math.Abs(g-c.after50) > 0.02 {
			t.Errorf("%s: gain %.3f after 50 ms, want %.2f", c.name, g, c.after50)
		}
		if a, b := gain[at(c.back-8)], gain[at(c.back+2)]; a >= 0.999 || b < 0.999 {
			t.Errorf("%s: gain %.4f at %.0f ms and %.4f at %.0f ms, want it back in between", c.name, a, c.back-8, b, c.back+2)
		}
	}
}

// TestDuckerOrder checks the limit of the routing: the instrument that ducks
// the bus has to come after the instruments that send to it. Before them, it
// reads what they sent in the sample before.
func TestDuckerOrder(t *testing.T) {
	t.Parallel()
	mods := modulePreset(t, "Ducker")
	song := func(kickFirst bool) sointu.Song {
		bus := sointu.Instrument{Name: "bus", NumVoices: 1, Units: []sointu.Unit{
			{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 64}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 4}},
		}}
		kick := sointu.Instrument{Name: "kick", NumVoices: 1, Units: []sointu.Unit{
			{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 4}},
			moduleCall(mods[0].ID, nil),
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
		}}
		if kickFirst {
			return duckingSong(1, []sointu.Pattern{heldFromRow2, nil}, sointu.Patch{kick, bus}, mods)
		}
		return duckingSong(1, []sointu.Pattern{nil, heldFromRow2}, sointu.Patch{bus, kick}, mods)
	}
	after, before := playGo(t, song(false)), playGo(t, song(true))
	// before the note the gain is 1: the same noise, a sample late
	for i := 1; i < int(2*duckingRow)-1; i++ {
		if before[i] != after[i-1] {
			t.Fatalf("frame %d: %v with the kick first, %v a sample earlier with the kick last", i, before[i], after[i-1])
		}
	}
	if before[0] != [2]float32{} || after[0] == [2]float32{} {
		t.Errorf("first frame: %v with the kick first, %v with the kick last", before[0], after[0])
	}
}

// TestSidechainGain checks the Sidechain module, the ducking by a compressor:
// the gain is 1 until the key, a decaying sine, plays, goes down by more than
// 12 dB while it is loud and comes back as it fades.
func TestSidechainGain(t *testing.T) {
	t.Parallel()
	mods := modulePreset(t, "Sidechain")
	buffer := playGo(t, gainSong(sointu.Pattern{0, 0, 43, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, mods,
		sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 70, "sustain": 0, "release": 70, "gain": 128}},
		sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine}},
		sointu.Unit{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
		moduleCall(mods[0].ID, nil),
	))
	start := int(2*duckingRow) - 1
	low, step := float32(1), 0.0
	for i, f := range buffer {
		if f[0] != f[1] || f[0] > 1 || (i < start && f[0] != 1) {
			t.Fatalf("frame %d: %v", i, f)
		}
		low = min(low, f[0])
		if i > 0 {
			step = max(step, math.Abs(float64(f[0]-buffer[i-1][0])))
		}
	}
	if g := buffer[start+int(0.05*44100)][0]; low > 0.2 || g > 0.25 || step > 0.1 {
		t.Errorf("lowest gain %.3f, %.3f after 50 ms, largest step %.3f", low, g, step)
	}
	if g := buffer[start+int(0.6*44100)][0]; g != 1 {
		t.Errorf("gain %v 600 ms after the key started, want 1", g)
	}
}

// fxSong plays a signal into aux 2/3 and has a second instrument send that
// through a module unit and out: wet only, so the output is what the module
// makes of it.
func fxSong(length int, pattern sointu.Pattern, source []sointu.Unit, modules sointu.Modules, call sointu.Unit) sointu.Song {
	source = append(source[:len(source):len(source)],
		sointu.Unit{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 64}},
		sointu.Unit{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 2}})
	return duckingSong(length, []sointu.Pattern{pattern, nil}, sointu.Patch{
		{Name: "source", NumVoices: 1, Units: source},
		{Name: "fx", NumVoices: 1, Units: []sointu.Unit{
			{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}},
			call,
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
		}},
	}, modules)
}

// TestPingPongDelay checks the repeats of the Ping pong delay module for a
// click: at multiples of the delay time, a dotted eighth, on the left and the
// right in turn, nothing on the other side, each lower than the one before
// and with less of its level in the highs.
func TestPingPongDelay(t *testing.T) {
	t.Parallel()
	mods := modulePreset(t, "Ping_pong_delay")
	click := []sointu.Unit{{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 16, "sustain": 0, "release": 16, "gain": 128}}}
	for _, right := range []bool{false, true} {
		params := sointu.ParamMap{}
		if right {
			params["p5"] = 128 // pan: the first repeat on the right
		}
		buffer := playGo(t, fxSong(2, sointu.Pattern{60, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, click, mods, moduleCall(mods[0].ID, params)))
		const step = 44100 * 60 * 36 / 48 / 120 // 36/48 of a beat at 120 beats per minute
		if l := level(buffer[:step-2]); l != 0 {
			t.Errorf("level %v before the first repeat: the module is not wet only", l)
		}
		last, lastHighs := math.Inf(1), math.Inf(1)
		for k := 1; k <= 5; k++ {
			frames := buffer[k*step-2 : k*step+step/2]
			side := (k + 1) % 2 // 0 is left
			if right {
				side = k % 2
			}
			var on, off, highs float64
			for i, f := range frames {
				on += float64(f[side]) * float64(f[side])
				off += float64(f[1-side]) * float64(f[1-side])
				if i > 0 { // the difference of samples next to each other: the highs
					d := float64(f[side] - frames[i-1][side])
					highs += d * d
				}
			}
			if on == 0 || off > on*1e-20 || on >= last*0.5 || highs/on >= lastHighs { // the filters leave denormals on the other side
				t.Errorf("right first %v, repeat %d: energy %g on its side (%g before), %g on the other, highs %g of it (%g before)", right, k, on, last, off, highs/on, lastHighs)
			}
			last, lastHighs = on, highs/on
		}
	}
}

// TestDuckingReverbAndDelay checks the modules Ducking reverb and Ducking
// delay against what they are made of, the reverb unit and the Ping pong
// delay module, for a held saw note: the wet signal is lower by more than
// 6 dB while the note plays, and exactly the same once the gain is back.
func TestDuckingReverbAndDelay(t *testing.T) {
	t.Parallel()
	saw := []sointu.Unit{
		{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 96, "release": 56, "gain": 128}},
		{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 0, "shape": 64, "gain": 64, "type": sointu.Trisaw}},
		{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
	}
	held := sointu.Pattern{60, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0} // a second
	for _, name := range []string{"Ducking_reverb", "Ducking_delay"} {
		mods := modulePreset(t, name)
		// without ducking: the module that it uses, or the reverb unit in
		// it, with its parameters as the defaults of the module give them
		var dry sointu.Unit
		switch {
		case name == "Ducking_delay" && len(mods) == 2:
			dry = moduleCall(mods[0].ID, nil)
		case name == "Ducking_reverb" && len(mods) == 1:
			for _, u := range mods[0].Units {
				if u.Type == "reverb" {
					dry = u.Copy()
					dry.Bind = nil
				}
			}
			if dry.Type == "" {
				t.Fatalf("%s has no reverb unit", name)
			}
		default:
			t.Fatalf("%s: %d modules", name, len(mods))
		}
		ducking := mods[len(mods)-1]
		plain := playGo(t, fxSong(2, held, saw, mods, dry))
		ducked := playGo(t, fxSong(2, held, saw, mods, moduleCall(ducking.ID, nil)))
		// from 0.5 s: the first repeat of the delay comes after 0.375 s
		if a, b := level(plain[22050:44100]), level(ducked[22050:44100]); a == 0 || b == 0 || b > a/2 {
			t.Errorf("%s: level %.4f while the note plays, %.4f without ducking", name, b, a)
		}
		from := 44100 + 44100/2 // half a second after the note
		if a := level(plain[from:]); a == 0 {
			t.Errorf("%s: nothing left after the note", name)
		}
		for i := from; i < len(plain); i++ {
			if plain[i] != ducked[i] {
				t.Fatalf("%s: frame %d is %v, %v without ducking: the gain is not back", name, i, ducked[i], plain[i])
			}
		}
		// duck 0 is no ducking at all
		off := playGo(t, fxSong(2, held, saw, mods, moduleCall(ducking.ID, sointu.ParamMap{"p6": 0})))
		for i := range plain {
			if plain[i] != off[i] {
				t.Fatalf("%s with duck 0: frame %d is %v, %v without ducking", name, i, off[i], plain[i])
			}
		}
	}
}

// TestDuckingModulePresetsWasmMatchesGoSynth renders each of the module
// presets in both synths, fed by bursts of noise, in an instrument that
// plays notes itself: with the defaults of its parameters, and with all of
// them set.
func TestDuckingModulePresetsWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, c := range []struct {
		name string
		set  sointu.ParamMap
	}{
		{"Ducker", sointu.ParamMap{"p1": 100, "p2": 60, "p3": 80}},
		{"Sidechain", sointu.ParamMap{"p1": 30, "p2": 90, "p3": 20, "p4": 70}},
		{"Ping_pong_delay", sointu.ParamMap{"p1": 12, "p2": 128, "p3": 40, "p4": 90, "p5": 100}},
		{"Ducking_reverb", sointu.ParamMap{"p1": 20, "p2": 70, "p3": 100, "p4": 40, "p5": 20, "p6": 128, "p7": 70, "p8": 20}},
		{"Ducking_delay", sointu.ParamMap{"p1": 24, "p2": 100, "p3": 100, "p4": 10, "p5": 128, "p6": 60, "p7": 50, "p8": 30}},
	} {
		mods := modulePreset(t, c.name)
		mod := mods[len(mods)-1]
		for name, params := range map[string]sointu.ParamMap{"defaults": nil, "all set": c.set} {
			t.Run(c.name+" "+name, func(t *testing.T) {
				fx := []sointu.Unit{{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}}}
				if mod.Inputs == 3 { // a key on top of the signal
					fx = append(fx,
						sointu.Unit{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 64, "sustain": 0, "release": 64, "gain": 128}},
						sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 40, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine}},
						sointu.Unit{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}})
				}
				fx = append(fx, moduleCall(mod.ID, params), sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}})
				song := duckingSong(2, []sointu.Pattern{{60, 1, 1, 1, 0, 1, 1, 1, 64, 1, 0, 1, 1, 1, 1, 1}, {0, 0, 60, 1, 1, 1, 60, 1, 0, 0, 0, 0, 62, 0, 0, 0}}, sointu.Patch{
					{Name: "burst", NumVoices: 1, Units: []sointu.Unit{
						{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
						{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
						{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
						{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 40}},
						{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 2}},
					}},
					{Name: "fx", NumVoices: 1, Units: fx},
				}, mods)
				want := playGo(t, song)
				if level(want) == 0 {
					t.Fatal("the module is silent")
				}
				compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
			})
		}
	}
}

// TestDuckingExampleWasmMatchesGoSynth renders examples/ducking.yml, a bass
// and a pad on a bus that the preset Kick ducker ducks, in both synths, and
// checks on the way that the bus is ducked: the first pattern has no kick.
func TestDuckingExampleWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../examples/ducking.yml")
	if err != nil {
		t.Fatal(err)
	}
	var song sointu.Song
	if err := yaml.Unmarshal(data, &song); err != nil {
		t.Fatal(err)
	}
	want := playGo(t, song)
	// the kick must be the last instrument: after those on the bus
	if last := song.Patch[len(song.Patch)-1]; last.Name != "kick" {
		t.Errorf("the last instrument is %s", last.Name)
	}
	pattern := 16 * 44100 * 60 / (song.BPM * song.RowsPerBeat)
	beat := pattern / 4
	if len(want) < 2*pattern {
		t.Fatalf("%d frames", len(want))
	}
	// the 20 ms from 5 ms after a beat: the bus alone in the first pattern,
	// the kick over the ducked bus in the second; and the last 100 ms of a
	// beat, where the kick is over and the bus back
	quiet, loud := want[beat+220:beat+1102], want[pattern+beat+220:pattern+beat+1102]
	if a, b := level(quiet), level(loud); a == 0 || b == 0 {
		t.Errorf("level %v without the kick, %v with it", a, b)
	}
	a, b := level(want[2*beat-4410:2*beat]), level(want[pattern+2*beat-4410:pattern+2*beat])
	if math.Abs(a-b) > 0.02*a {
		t.Errorf("level %.4f at the end of a beat without the kick, %.4f with it: the bus is not back", a, b)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}

// TestDuckingGlobalPresetWasmMatchesGoSynth renders the preset Global
// mastering 2 ducking, the Ducking delay and the Ducking reverb before Global
// mastering 2, in both synths: bursts of noise sent to aux 2/3 and aux 6/7.
func TestDuckingGlobalPresetWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../tracker/presets/UTIL/Global_mastering_2_ducking.yml")
	if err != nil {
		t.Fatal(err)
	}
	var preset struct {
		sointu.Instrument `yaml:",inline"`
		Modules           sointu.Modules
	}
	if err := yaml.Unmarshal(data, &preset); err != nil || len(preset.Modules) != 3 {
		t.Fatalf("reading the preset: %v, %d modules", err, len(preset.Modules))
	}
	song := duckingSong(2, []sointu.Pattern{{60, 1, 1, 1, 0, 1, 1, 1, 64, 1, 0, 1, 1, 1, 1, 1}, nil}, sointu.Patch{
		{Name: "burst", NumVoices: 1, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
			{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 40}},
			{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64, "channel": 2}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 64, "channel": 6}},
		}},
		preset.Instrument,
	}, preset.Modules)
	want := playGo(t, song)
	// the repeats and the reverb are there after the bursts, and nothing
	// exceeds full scale after the mastering
	if l := level(want[len(want)/2+11025 : len(want)/2+44100]); l < 1e-4 {
		t.Errorf("level %v in the second pattern, where only the delay and the reverb play", l)
	}
	for i, f := range want {
		if math.Abs(float64(f[0])) > 1 || math.Abs(float64(f[1])) > 1 {
			t.Fatalf("frame %d: %v", i, f)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
}
