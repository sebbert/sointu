package compiler_test

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
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
// drum bus, with the preset as the last instrument.
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
			{Type: "aux", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128, "channel": 6}},
		}}, instr}
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
// Global presets with the drum bus: the preset loads, stays within the 63
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
	files = append(files, "../../tracker/presets/UTIL/Global_mastering_2_drumbus.yml", "../../tracker/presets/UTIL/Global_mastering_2_drumbus_reverb.yml")
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
