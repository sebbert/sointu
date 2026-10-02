package compiler_test

import (
	"bytes"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

// soundsetPreset reads an instrument preset like the tracker does: unknown
// fields are an error, as the tracker leaves out a preset that has one.
func soundsetPreset(t *testing.T, file string) (sointu.Instrument, sointu.Modules) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var preset struct {
		sointu.Instrument `yaml:",inline"`
		Modules           sointu.Modules
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&preset); err != nil {
		t.Fatalf("reading the preset: %v", err)
	}
	return preset.Instrument, preset.Modules
}

// soundsetPresetSong is a song that plays a preset: two notes of an
// instrument, slowly for the sounds that rise slowly, or for a Global
// preset, bursts of noise sent to the main output, the reverb send and the
// drum bus on channels 8 and 9, and for Global mastering 2 buses and its
// ducking variant to the delay send too, with the preset as the last
// instrument.
func soundsetPresetSong(instr sointu.Instrument, modules sointu.Modules) sointu.Song {
	song := sointu.Song{BPM: 140, RowsPerBeat: 4, Modules: modules,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{48, 1, 1, 1, 1, 1, 0, 0, 72, 1, 1, 1, 0, 0, 0, 0}}},
		}}}
	if strings.HasPrefix(instr.Name, "Global") {
		instr.NumVoices = 1
		song.Patch = sointu.Patch{{Name: "burst", NumVoices: 1, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 1, "attack": 0, "decay": 60, "sustain": 0, "release": 60, "gain": 128}},
			{Type: "noise", Parameters: sointu.ParamMap{"stereo": 1, "shape": 64, "gain": 128}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}},
			{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 32}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 32, "channel": 2}},
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 8}},
		}}, instr}
		if strings.Contains(instr.Name, "buses") {
			// a copy more of the burst, to the delay send
			units := &song.Patch[0].Units
			*units = slices.Insert(*units, 3, sointu.Unit{Type: "push", Parameters: sointu.ParamMap{"stereo": 1}})
			*units = append(*units, sointu.Unit{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 24, "channel": 6}})
		}
		return song
	}
	if instr.NumVoices == 0 {
		instr.NumVoices = 1
	}
	if strings.Contains(instr.Name, "riser") || strings.Contains(instr.Name, "sweep up") {
		song.BPM = 20 // these take 6 s to rise: a row is 0.75 s
	}
	song.Patch = sointu.Patch{instr}
	return song
}

// TestSoundsetPresets checks every preset of the sound set (Club ...) and the
// Global presets with the drum bus, Global mastering 2 buses and Global
// mastering 2 buses ducking among them: the
// preset loads, stays within the 63
// units of an instrument, compiles for wasm, and the Go synth renders sound
// from it without NaN; with node and wat2wasm, the wasm player renders
// exactly the same.
func TestSoundsetPresets(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../tracker/presets/*/Club_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 21 {
		t.Errorf("%d Club presets, want 21", len(files))
	}
	files = append(files, "../../tracker/presets/UTIL/Global_mastering_2_drumbus.yml", "../../tracker/presets/UTIL/Global_mastering_2_drumbus_reverb.yml",
		"../../tracker/presets/UTIL/Global_mastering_2_buses.yml", "../../tracker/presets/UTIL/Global_mastering_2_buses_ducking.yml")
	node, nodeErr := exec.LookPath("node")
	wat2wasm, watErr := exec.LookPath("wat2wasm")
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".yml")
		t.Run(name, func(t *testing.T) {
			instr, modules := soundsetPreset(t, file)
			instr.Name = strings.ReplaceAll(name, "_", " ")
			song := soundsetPresetSong(instr, modules)
			expanded, x := song.Expand()
			if len(x.Problems) > 0 {
				t.Fatalf("problems expanding the preset: %v", x.Problems)
			}
			if n := len(expanded.Patch[len(expanded.Patch)-1].Units); n > 63 {
				t.Errorf("the preset has %d units, more than the 63 of an instrument", n)
			}
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := com.Song(&song); err != nil {
				t.Fatalf("compiling for wasm failed: %v", err)
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			peak := 0.0
			for i, frame := range want {
				for _, v := range frame {
					if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
						t.Fatalf("frame %d is %v", i, v)
					}
					peak = max(peak, math.Abs(float64(v)))
				}
			}
			// the quietest, the closed hat, peaks at 0.18; nothing may be
			// far above full scale either
			if peak < 0.1 || peak > 2 {
				t.Errorf("the preset peaks at %v", peak)
			}
			if nodeErr != nil || watErr != nil {
				return
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}

// TestSoundsetExamplesWasmMatchGoSynth renders the example songs of the
// sound set in both synths: examples/soundset.yml plays every sound in
// turn, examples/soundset_loop.yml a loop of kit, bass and lead through the
// drum bus and the master chain. The first takes more than a minute, and
// only runs with SOINTU_TEST_LONG=1; of the loop, without it, the first two
// patterns.
func TestSoundsetExamplesWasmMatchGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, name := range []string{"soundset_loop", "soundset"} {
		t.Run(name, func(t *testing.T) {
			if name == "soundset" && !longTests() {
				t.Skip("long: set SOINTU_TEST_LONG=1 to render it")
			}
			data, err := os.ReadFile("../../examples/" + name + ".yml")
			if err != nil {
				t.Fatal(err)
			}
			var song sointu.Song
			if err := yaml.Unmarshal(data, &song); err != nil {
				t.Fatal(err)
			}
			if !longTests() {
				song.Score.Length = min(song.Score.Length, 2)
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			for i, frame := range want {
				if v := float64(frame[0] + frame[1]); math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("frame %d is %v", i, frame)
				}
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}

// mapChannel returns the song with the aux and in units of one channel
// moved to another.
func mapChannel(song sointu.Song, from, to int) sointu.Song {
	song = song.Copy()
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if (u.Type == "aux" || u.Type == "in") && u.Parameters["channel"] == from {
				u.Parameters["channel"] = to
			}
		}
	}
	return song
}

// TestDrumBusChannel checks that the drum bus of the Global presets is on
// channels 8 and 9, where it does what it did on 6 and 7: the Go synth
// renders the same with the bus and the drums moved there. On 8 and 9 the
// presets are for the Go synth and the wasm player only, as their comments
// say.
func TestDrumBusChannel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Global mastering 2 drumbus", "Global mastering 2 drumbus reverb", "Global mastering 2 buses", "Global mastering 2 buses ducking"} {
		instr, modules := soundsetPreset(t, "../../tracker/presets/UTIL/"+strings.ReplaceAll(name, " ", "_")+".yml")
		if !strings.Contains(instr.Units[0].Comment, "channels above 7: Go synth and wasm player only") {
			t.Errorf("%s: the comment does not say that the channels above 7 are for the Go synth and the wasm player only", name)
		}
		song := soundsetPresetSong(instr, modules)
		expanded, _ := song.Expand()
		if got := expanded.Patch.MaxChannel(); got != 9 {
			t.Errorf("%s: the highest channel is %d, want 9", name, got)
		}
		delay := mapChannel(song, 6, 10) // the delay send, out of the way
		old := mapChannel(delay, 8, 6)
		expanded, _ = old.Expand()
		if got := expanded.Patch.MaxChannel(); !strings.Contains(name, "buses") && got != 7 {
			t.Errorf("%s with the bus on 6: the highest channel is %d, want 7", name, got)
		}
		want, got := playGo(t, old), playGo(t, delay)
		if !slices.Equal(want, got) {
			t.Errorf("%s: the drum bus on channels 8 and 9 renders differently from the one on 6 and 7", name)
		}
		// the bus is heard: not with the drums sent elsewhere
		elsewhere := song.Copy()
		elsewhere.Patch = append(mapChannel(sointu.Song{Patch: song.Patch[:1]}, 8, 12).Patch, elsewhere.Patch[1:]...)
		if slices.Equal(playGo(t, elsewhere), playGo(t, song)) {
			t.Errorf("%s: the drum bus is not heard", name)
		}
	}
}

// TestBusesDuckingPreset checks, in the Go synth, that Global mastering 2
// buses ducking is Global mastering 2 ducking with the drum bus of Global
// mastering 2 buses: with nothing sent to the drum bus it renders what the
// first renders, and with only the drum bus sent to, what the second does.
// TestSoundsetPresets renders it in the wasm player.
func TestBusesDuckingPreset(t *testing.T) {
	t.Parallel()
	preset := func(name string) sointu.Song {
		instr, modules := soundsetPreset(t, "../../tracker/presets/UTIL/"+strings.ReplaceAll(name, " ", "_")+".yml")
		instr.Name = "Global mastering 2 buses" // the song for the presets with every bus
		return soundsetPresetSong(instr, modules)
	}
	// the units of the burst that send to a bus, with their gain 0, unless
	// keep says otherwise
	mute := func(song sointu.Song, keep func(channel int) bool) sointu.Song {
		song = song.Copy()
		for _, u := range song.Patch[0].Units {
			if (u.Type == "aux" || u.Type == "out") && !keep(u.Parameters["channel"]) {
				u.Parameters["gain"] = 0
			}
		}
		return song
	}
	both := preset("Global mastering 2 buses ducking")
	if units := both.Patch[1].Units; !strings.Contains(units[0].Comment, "channels above 7: Go synth and wasm player only") {
		t.Errorf("the comment does not say that the channels above 7 are for the Go synth and the wasm player only")
	}
	all := playGo(t, both)
	for _, tc := range []struct {
		like string
		keep func(channel int) bool
	}{
		{"Global mastering 2 ducking", func(c int) bool { return c != 8 }},
		{"Global mastering 2 buses", func(c int) bool { return c == 8 }},
	} {
		want, got := playGo(t, mute(preset(tc.like), tc.keep)), playGo(t, mute(both, tc.keep))
		if level(want) < 1e-3 || slices.Equal(got, all) {
			t.Errorf("like %s: level %v, and the sends that are muted change nothing: %v", tc.like, level(want), slices.Equal(got, all))
		}
		if !slices.Equal(want, got) {
			t.Errorf("the preset does not render like %s", tc.like)
		}
	}
}

// TestBusesExample renders examples/buses.yml in both synths: the reverb
// send on aux 2/3, the bus that the Kick ducker ducks on 4/5, the delay send
// on 6/7 and the drum bus on 8/9 in one song, through the preset Global
// mastering 2 buses. Each of the four buses is heard: without what is sent
// to it, the song renders differently.
func TestBusesExample(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../examples/buses.yml")
	if err != nil {
		t.Fatal(err)
	}
	var song sointu.Song
	if err := yaml.Unmarshal(data, &song); err != nil {
		t.Fatal(err)
	}
	// the last instrument is the preset, apart from the IDs
	preset, _ := soundsetPreset(t, "../../tracker/presets/UTIL/Global_mastering_2_buses.yml")
	global := song.Patch[len(song.Patch)-1]
	if global.Name != preset.Name || len(global.Units) != len(preset.Units) {
		t.Fatalf("the last instrument is %s with %d units, the preset %s has %d", global.Name, len(global.Units), preset.Name, len(preset.Units))
	}
	for i, u := range global.Units {
		p := preset.Units[i].Copy()
		if u.Type == "module" {
			p.Parameters["module"] = u.Parameters["module"]
		}
		if u.Type != p.Type || !maps.Equal(u.Parameters, p.Parameters) {
			t.Errorf("unit %d of the last instrument is %s %v, of the preset %s %v", i, u.Type, u.Parameters, p.Type, p.Parameters)
		}
	}
	want := playGo(t, song)
	for _, channel := range []int{2, 4, 6, 8} {
		muted := song.Copy()
		sends := 0
		for _, instr := range muted.Patch {
			for _, u := range instr.Units {
				switch {
				case u.Type == "aux" && u.Parameters["channel"] == channel:
					u.Parameters["gain"] = 0
					sends++
				case u.Type == "outaux" && channel == 2:
					u.Parameters["auxgain"] = 0
					sends++
				}
			}
		}
		if sends == 0 || slices.Equal(playGo(t, muted), want) {
			t.Errorf("channel %d: %d units send to it, and the song is the same without them", channel, sends)
		}
	}
	for i, f := range want {
		if v := float64(f[0] + f[1]); math.IsNaN(v) || math.Abs(float64(f[0])) > 1 || math.Abs(float64(f[1])) > 1 {
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
