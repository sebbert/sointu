package vm_test

import (
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// reverbSong is bursts of noise and a saw, panned apart, through a reverb:
// the Reverb module preset that the tracker comes with, or the reverb unit,
// with the 8 parameters given in the order of the module (size, decay,
// highs, lows, predelay, mod, highcut, lowcut). With lfo, a slow oscillator
// modulates mod.
func reverbSong(t testing.TB, unit bool, p [8]int, lfo bool) sointu.Song {
	t.Helper()
	data, err := os.ReadFile("../tracker/modules/Reverb.yml")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Modules sointu.Modules }
	if err := yaml.Unmarshal(data, &file); err != nil || len(file.Modules) != 1 {
		t.Fatalf("reading the preset: %v, %d modules", err, len(file.Modules))
	}
	reverb := sointu.Unit{Type: "module", ID: 1000, Parameters: sointu.ParamMap{"module": file.Modules[0].ID,
		"p1": p[0], "p2": p[1], "p3": p[2], "p4": p[3], "p5": p[4], "p6": p[5], "p7": p[6], "p8": p[7]}}
	port := 5 // of the module parameter mod
	if unit {
		reverb = reverbUnit(sointu.ParamMap{
			"size": p[0], "decay": p[1], "highs": p[2], "lows": p[3], "predelay": p[4], "mod": p[5], "highcut": p[6], "lowcut": p[7]})
		port = 0
	}
	units := []sointu.Unit{
		{Type: "in", Parameters: sointu.ParamMap{"stereo": 1, "channel": 2}},
		reverb,
		{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
	}
	if lfo {
		units = append(units,
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 80, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 100, "target": 1000, "port": port, "sendpop": 1}})
	}
	song := sointu.Song{BPM: 120, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{{60, 1, 0, 1, 1, 1, 1, 1, 64, 1, 0, 1, 1, 1, 1, 1}, make(sointu.Pattern, 16)}},
			{NumVoices: 1, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{{0, 1, 1, 1, 45, 1, 1, 0, 1, 1, 57, 0, 1, 1, 1, 1}, make(sointu.Pattern, 16)}},
			{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}},
		}},
		Patch: sointu.Patch{{Name: "burst", NumVoices: 1, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 50, "sustain": 0, "release": 50, "gain": 128}},
			{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 128}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 40}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 2}},
		}}, {Name: "saw", NumVoices: 1, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 32, "decay": 64, "sustain": 64, "release": 64, "gain": 128}},
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 0, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
			{Type: "pan", Parameters: sointu.ParamMap{"stereo": 0, "panning": 100}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 2}},
		}}, {Name: "reverb", NumVoices: 1, Units: units}},
	}
	if !unit {
		song.Modules = file.Modules
	}
	return song
}

// reverbUnit returns a reverb unit with the given parameters, and the others
// as in the Reverb module.
func reverbUnit(params sointu.ParamMap) sointu.Unit {
	p := sointu.ParamMap{}
	maps.Copy(p, sointu.AddedParameters("reverb"))
	maps.Copy(p, params)
	return sointu.Unit{Type: "reverb", ID: 1000, Parameters: p}
}

// presetSong is the song of reverbSong with the units of an instrument
// preset in place of its reverb instrument.
func presetSong(t testing.TB, file string) sointu.Song {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var instr sointu.Instrument
	if err := yaml.Unmarshal(data, &instr); err != nil {
		t.Fatal(err)
	}
	song := reverbSong(t, true, reverbSettings["defaults"], false)
	song.Patch[2] = instr
	return song
}

// TestReverbUnitRendersLikeThePresets checks that the presets Reverb unit
// Room, Hall, Ambient and Plate, a reverb unit each, render exactly what the
// presets Reverb FDN Room, Hall, Ambient and Plate render, chains of mc units; and
// that the unit renders the chain of examples/reverb.yml.
func TestReverbUnitRendersLikeThePresets(t *testing.T) {
	compare := func(name string, unit, mc sointu.Song) {
		t.Helper()
		want, err := sointu.Play(vm.GoSynther{}, mc, nil)
		if err != nil {
			t.Fatalf("%s: the mc units: %v", name, err)
		}
		got, err := sointu.Play(vm.GoSynther{}, unit, nil)
		if err != nil {
			t.Fatalf("%s: the unit: %v", name, err)
		}
		peak, differing := 0.0, 0
		for i := range want {
			for c := range 2 {
				peak = max(peak, math.Abs(float64(want[i][c])))
				if got[i][c] != want[i][c] {
					differing++
				}
			}
		}
		if peak < 0.01 || len(got) != len(want) || differing > 0 {
			t.Errorf("%s: %d samples of the unit differ from the mc units' (peak %v, %d and %d frames)", name, differing, peak, len(got), len(want))
		}
	}
	for _, name := range []string{"Room", "Hall", "Ambient", "Plate"} {
		compare(name, presetSong(t, "../tracker/presets/UTIL/Reverb_unit_"+name+".yml"), presetSong(t, "../tracker/presets/UTIL/Reverb_FDN_"+name+".yml"))
	}
	// examples/reverb.yml: the chain of the Reverb module without the high
	// cut, with a predelay of 20 ms and a network of 150 ms
	data, err := os.ReadFile("../examples/reverb.yml")
	if err != nil {
		t.Fatal(err)
	}
	var example sointu.Song
	if err := yaml.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	mc := reverbSong(t, true, reverbSettings["defaults"], false)
	mc.Patch[2] = example.Patch[len(example.Patch)-1]
	unit := reverbSong(t, true, reverbSettings["defaults"], false)
	unit.Patch[2].Units[1] = reverbUnit(sointu.ParamMap{"size": 64, "decay": 90, "highs": 48, "lows": 72, "mod": 24, "lowcut": 56,
		"network": 1500, "pretime": 200, "bypass": sointu.ReverbBypassHighcut})
	compare("examples/reverb.yml", unit, mc)
}

// withTheModule replaces the reverb units among the units, which must have
// the parameters that the Reverb module fixes at their defaults, with module
// units of the module with the ID given: the unit's size, decay, highs, lows,
// predelay, mod, highcut and lowcut as the parameters of the module, and
// their bindings as bindings of those. It returns how many it replaced.
func withTheModule(t testing.TB, units []sointu.Unit, module int) (n int) {
	t.Helper()
	shared := []string{"size", "decay", "highs", "lows", "predelay", "mod", "highcut", "lowcut"}
	for i := range units {
		u := &units[i]
		if u.Type != "reverb" {
			continue
		}
		n++
		c := sointu.Unit{Type: "module", ID: u.ID, Parameters: sointu.ParamMap{"module": module}}
		for _, p := range sointu.UnitTypes["reverb"].Params {
			k := slices.Index(shared, p.Name)
			if k < 0 {
				if u.Parameters[p.Name] != p.Default {
					t.Fatalf("a reverb unit with %s %d: the Reverb module has %d", p.Name, u.Parameters[p.Name], p.Default)
				}
				continue
			}
			name := "p" + strconv.Itoa(k+1)
			c.Parameters[name] = u.Parameters[p.Name]
			if b, ok := u.Bind[p.Name]; ok {
				if c.Bind == nil {
					c.Bind = map[string]sointu.Binding{}
				}
				c.Bind[name] = b
			}
		}
		*u = c
	}
	return n
}

// TestSwitchedPresetsRenderAsBefore checks the presets and the module preset
// that had the Reverb module before they got a reverb unit: each renders
// exactly what it renders with the module preset Reverb in place of its
// reverb unit, which is what it was. The parameters of the Ducking reverb
// module, which are bound to those of the unit, are set too.
func TestSwitchedPresetsRenderAsBefore(t *testing.T) {
	read := func(file string, v any) {
		t.Helper()
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, v); err != nil {
			t.Fatal(err)
		}
	}
	var reverb struct{ Modules sointu.Modules }
	read("../tracker/modules/Reverb.yml", &reverb)
	if len(reverb.Modules) != 1 {
		t.Fatalf("the module preset Reverb has %d modules", len(reverb.Modules))
	}
	// before returns the song with the Reverb module in place of every
	// reverb unit, of its last instrument and of its modules
	before := func(name string, song sointu.Song, want int) sointu.Song {
		t.Helper()
		old := song.Copy()
		mod := reverb.Modules[0].Copy()
		mod.ID = 1000
		n := withTheModule(t, old.Patch[len(old.Patch)-1].Units, mod.ID)
		for i := range old.Modules {
			n += withTheModule(t, old.Modules[i].Units, mod.ID)
		}
		old.Modules = append(old.Modules, mod)
		if n != want {
			t.Fatalf("%s has %d reverb units, want %d", name, n, want)
		}
		return old
	}
	compare := func(name string, song sointu.Song, reverbs int) {
		t.Helper()
		got, err := sointu.Play(vm.GoSynther{}, song, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := sointu.Play(vm.GoSynther{}, before(name, song, reverbs), nil)
		if err != nil {
			t.Fatalf("%s with the module: %v", name, err)
		}
		peak, differing := 0.0, 0
		for i := range want {
			for c := range 2 {
				peak = max(peak, math.Abs(float64(want[i][c])))
				if got[i][c] != want[i][c] {
					differing++
				}
			}
		}
		if peak < 0.01 || len(got) != len(want) || differing > 0 {
			t.Errorf("%s: %d samples differ from those with the Reverb module (peak %v, %d and %d frames)", name, differing, peak, len(got), len(want))
		}
	}
	for _, name := range []string{"Global reverb", "Global mastering reverb", "Global mastering 2 reverb", "Global mastering 2 drumbus reverb", "Global ducking reverb", "Global mastering 2 ducking"} {
		var preset struct {
			sointu.Instrument `yaml:",inline"`
			Modules           sointu.Modules
		}
		read("../tracker/presets/UTIL/"+strings.ReplaceAll(name, " ", "_")+".yml", &preset)
		song := reverbSong(t, true, reverbSettings["defaults"], false)
		song.Patch[2], song.Modules = preset.Instrument, preset.Modules
		for _, u := range preset.Units {
			if len(sointu.BusParams(u.Type)) > 0 {
				t.Errorf("%s has the mc unit %s", name, u.Type)
			}
		}
		compare(name, song, 1)
		// the parameters of the module unit of the Ducking reverb module
		for i := range song.Patch[2].Units {
			u := &song.Patch[2].Units[i]
			if j, ok := song.Modules.Find(u.Parameters["module"]); u.Type == "module" && ok && song.Modules[j].Name == "Ducking reverb" {
				c := song.Copy()
				maps.Copy(c.Patch[2].Units[i].Parameters, sointu.ParamMap{"p1": 20, "p2": 70, "p3": 100, "p4": 40, "p5": 90, "p6": 60, "p7": 50, "p8": 20})
				compare(name+", parameters set", c, 1)
			}
		}
	}
	// the module preset itself, with the defaults and with every parameter set
	var ducking struct{ Modules sointu.Modules }
	read("../tracker/modules/Ducking_reverb.yml", &ducking)
	if len(ducking.Modules) != 1 {
		t.Fatalf("the module preset Ducking reverb has %d modules, want 1", len(ducking.Modules))
	}
	for name, params := range map[string]sointu.ParamMap{
		"Ducking reverb":                 {"module": ducking.Modules[0].ID},
		"Ducking reverb, parameters set": {"module": ducking.Modules[0].ID, "p1": 110, "p2": 40, "p3": 10, "p4": 120, "p5": 0, "p6": 128, "p7": 80, "p8": 30},
	} {
		song := reverbSong(t, true, reverbSettings["defaults"], false)
		song.Patch[2].Units[1] = sointu.Unit{Type: "module", ID: 1000, Parameters: params}
		song.Modules = ducking.Modules
		compare(name, song, 1)
	}
	// the examples of the sound set, whole songs
	for _, file := range []string{"soundset_loop.yml"} {
		var song sointu.Song
		read("../examples/"+file, &song)
		compare(file, song, 1)
	}
}

// reverbSettings are values for the 8 parameters of the reverb: the defaults,
// the ends of their ranges, and some in between.
var reverbSettings = map[string][8]int{
	"defaults":  {64, 90, 48, 72, 13, 24, 98, 56},
	"all set":   {20, 70, 100, 40, 60, 80, 60, 20},
	"smallest":  {0, 40, 0, 0, 0, 0, 0, 0},
	"largest":   {128, 128, 128, 128, 128, 128, 128, 128},
	"hold":      {90, 0, 64, 64, 30, 10, 128, 0},
	"no mod":    {64, 100, 30, 100, 0, 0, 110, 90},
	"dark room": {33, 64, 20, 50, 5, 40, 77, 33},
}

// TestReverbUnitRendersLikeTheModule checks that the reverb unit renders
// exactly what the Reverb module preset renders with the same values of its
// parameters, also when mod is modulated.
func TestReverbUnitRendersLikeTheModule(t *testing.T) {
	for name, p := range reverbSettings {
		for _, lfo := range []bool{false, true} {
			module, err := sointu.Play(vm.GoSynther{}, reverbSong(t, false, p, lfo), nil)
			if err != nil {
				t.Fatalf("%s: the module: %v", name, err)
			}
			unit, err := sointu.Play(vm.GoSynther{}, reverbSong(t, true, p, lfo), nil)
			if err != nil {
				t.Fatalf("%s: the unit: %v", name, err)
			}
			if len(unit) != len(module) {
				t.Fatalf("%s: the unit rendered %d frames, the module %d", name, len(unit), len(module))
			}
			peak, differing := 0.0, 0
			for i := range module {
				for c := range 2 {
					peak = max(peak, math.Abs(float64(module[i][c])))
					if unit[i][c] != module[i][c] {
						differing++
					}
				}
			}
			if peak < 0.01 {
				t.Errorf("%s: the reverb is silent (peak %v)", name, peak)
			}
			if differing > 0 {
				t.Errorf("%s, lfo %v: %d samples of the unit differ from the module's", name, lfo, differing)
			}
		}
	}
}

// TestReverbUnitDecays checks that the reverb of the unit dies away in about
// its decay time, and that decay 0 holds the sound.
func TestReverbUnitDecays(t *testing.T) {
	level := func(buf sointu.AudioBuffer, from, to float64) float64 { // in dB, between two times in seconds
		sum, n := 0.0, 0
		for _, v := range buf[int(from*44100):int(to*44100)] {
			sum += float64(v[0]*v[0] + v[1]*v[1])
			n++
		}
		return 10 * math.Log10(sum/float64(n)+1e-30)
	}
	// 4 s of song; the last note ends after 1.4 s
	short, err := sointu.Play(vm.GoSynther{}, reverbSong(t, true, [8]int{64, 64, 64, 64, 0, 24, 128, 0}, false), nil) // 1 s
	if err != nil {
		t.Fatal(err)
	}
	if d := level(short, 2.0, 2.5) - level(short, 3.0, 3.5); d < 45 || d > 75 {
		t.Errorf("decay of 1 s: the level falls by %.1f dB in a second, want about 60", d)
	}
	hold, err := sointu.Play(vm.GoSynther{}, reverbSong(t, true, [8]int{64, 0, 64, 64, 0, 0, 128, 0}, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := level(hold, 2.0, 2.5) - level(hold, 3.0, 3.5); math.Abs(d) > 1 {
		t.Errorf("decay 0: the level changes by %.1f dB in a second, want it to hold", d)
	}
}

// BenchmarkReverb renders 4 seconds of noise and a saw through the Reverb
// module preset and through the reverb unit, with the default parameters,
// and of the same song without a reverb.
func BenchmarkReverb(b *testing.B) {
	p := reverbSettings["defaults"]
	dry := reverbSong(b, true, p, false)
	dry.Patch[2].Units = append(dry.Patch[2].Units[:1:1], dry.Patch[2].Units[2:]...)
	for _, c := range []struct {
		name string
		song sointu.Song
	}{{"module", reverbSong(b, false, p, false)}, {"unit", reverbSong(b, true, p, false)}, {"none", dry}} {
		b.Run(c.name, func(b *testing.B) {
			for range b.N {
				buf, err := sointu.Play(vm.GoSynther{}, c.song, nil)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(buf)), "ns/sample")
			}
		})
	}
}
