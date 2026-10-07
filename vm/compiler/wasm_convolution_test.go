package compiler_test

import (
	"math"
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// convTestSong is a song with an instrument that writes a burst of noise
// with an envelope into a buffer, a response, and instruments whose notes
// are convolved with it by the units given: each gets an instrument of
// short notes with the unit after it. With poly, the first of them has
// three voices.
func convTestSong(frames, channels int, poly bool, units ...sointu.Unit) sointu.Song {
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Buffers:     sointu.Buffers{{ID: 1, Name: "response", Channels: channels, Frames: frames}},
		Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{
			// the response is written twice: the second is another one
			{NumVoices: 1, Order: sointu.Order{0, 1}, Patterns: []sointu.Pattern{
				{60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0},
				{1, 1, 72, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1},
			}},
		}},
		Patch: sointu.Patch{
			{Name: "response", NumVoices: 1, Units: []sointu.Unit{
				{Type: "envelope", Parameters: sointu.ParamMap{"stereo": 0, "attack": 0, "decay": 68, "sustain": 0, "release": 64, "gain": 30}},
				{Type: "noise", Parameters: sointu.ParamMap{"stereo": channels - 1, "shape": 64, "gain": 128}},
				{Type: "mulp", Parameters: sointu.ParamMap{"stereo": 0}},
				{Type: "bufwrite", Parameters: sointu.ParamMap{"stereo": channels - 1, "buffer": 1, "oneshot": 1, "pop": 1}},
			}},
		},
	}
	if channels == 2 { // the envelope under both channels of the noise
		u := song.Patch[0].Units
		song.Patch[0].Units = []sointu.Unit{u[0], {Type: "push", Parameters: sointu.ParamMap{"stereo": 0}}, u[1], {Type: "mulp", Parameters: sointu.ParamMap{"stereo": 1}}, u[3]}
	}
	for i, u := range units {
		stereo := u.Parameters["stereo"]
		voices := 1
		pattern := sointu.Pattern{0, 1, 1, 1, 1, 64 + byte(i), 0, 1, 1, 1, 60, 0, 1, 1, 1, 1}
		if poly && i == 0 {
			voices = 3
			pattern = sointu.Pattern{50, 62, 1, 67, 0, 1, 1, 72, 1, 0, 60, 0, 1, 55, 1, 1}
		}
		song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: voices, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{pattern}})
		song.Patch = append(song.Patch, sointu.Instrument{Name: "convolved", NumVoices: voices, Units: []sointu.Unit{
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": stereo, "attack": 32, "decay": 60, "sustain": 40, "release": 60, "gain": 100}},
			{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": stereo, "transpose": 64, "detune": 70, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Trisaw}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": stereo}},
			u,
			{Type: "out", Parameters: sointu.ParamMap{"stereo": stereo, "gain": 128}},
		}})
	}
	return song
}

// conv returns a convolution unit reading buffer 1.
func conv(stereo, length int, more sointu.ParamMap) sointu.Unit {
	p := sointu.ParamMap{"stereo": stereo, "gain": 64, "buffer": 1, "start": 0, "length": length, "predelay": 0, "follow": 0, "fade": 0, "dry": 0}
	for k, v := range more {
		p[k] = v
	}
	return sointu.Unit{Type: "convolution", Parameters: p}
}

// convCases are songs with convolution units, each using some parts of the
// unit, and the code that the player has for them and for no others.
var convCases = []struct {
	name  string
	song  func() sointu.Song
	parts string // of fft, scan, follow, fade, nofade, predelay, gain, dry, stereo
}{
	{"head", func() sointu.Song { return convTestSong(20000, 1, false, conv(0, 0, nil)) }, "scan"},
	{"level 0", func() sointu.Song { return convTestSong(20000, 1, false, conv(0, 20, nil)) }, "fft scan nofade"},
	{"level 1", func() sointu.Song { return convTestSong(20000, 1, false, conv(0, 40, nil)) }, "fft scan nofade"},
	{"level 2", func() sointu.Song { return convTestSong(20000, 1, false, conv(0, 66, nil)) }, "fft scan nofade"},
	{"fade", func() sointu.Song { return convTestSong(20000, 1, false, conv(0, 66, sointu.ParamMap{"fade": 1})) }, "fft scan fade"},
	{"fade and not", func() sointu.Song {
		return convTestSong(20000, 1, false, conv(0, 66, sointu.ParamMap{"fade": 1}), conv(0, 50, nil))
	}, "fft scan fade nofade"},
	{"follow", func() sointu.Song {
		return convTestSong(20000, 1, false, conv(0, 66, sointu.ParamMap{"follow": 2}), conv(0, 30, sointu.ParamMap{"follow": 3, "fade": 1}), conv(0, 62, nil))
	}, "fft scan follow fade nofade"},
	{"predelay, gain, dry", func() sointu.Song {
		return convTestSong(20000, 1, false, conv(0, 60, sointu.ParamMap{"predelay": 127, "gain": 50, "dry": 40}), conv(0, 0, sointu.ParamMap{"predelay": 3}))
	}, "fft scan nofade predelay gain dry"},
	{"stereo", func() sointu.Song {
		return convTestSong(20000, 2, false, conv(1, 66, sointu.ParamMap{"start": 100}), conv(0, 66, nil))
	}, "fft scan nofade stereo"},
	{"stereo unit, mono buffer", func() sointu.Song { return convTestSong(20000, 1, false, conv(1, 60, sointu.ParamMap{"gain": 60})) }, "fft scan nofade stereo gain"},
	{"voices", func() sointu.Song {
		return convTestSong(9000, 2, true, conv(1, 58, sointu.ParamMap{"fade": 1}), conv(1, 70, sointu.ParamMap{"start": 3000, "fade": 1}))
	}, "fft scan fade stereo"},
	{"modulated", func() sointu.Song {
		song := convTestSong(20000, 1, false, conv(0, 64, nil))
		u := song.Patch[1].Units
		u[3].ID = 10
		song.Patch[1].Units = append(u[:3:3],
			sointu.Unit{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 80, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 80, "target": 10, "port": 0, "sendpop": 0}},
			sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 90, "target": 10, "port": 1, "sendpop": 1}},
			u[3], u[4])
		return song
	}, "fft scan nofade gain dry"},
	{"missing buffer", func() sointu.Song {
		return convTestSong(20000, 1, false, conv(0, 60, sointu.ParamMap{"buffer": 7, "dry": 100}), conv(0, 0, nil))
	}, "fft scan nofade dry"},
}

// convMarkers are texts that the player has when it has a part of the unit.
var convMarkers = map[string]string{
	"fft": "$convBack", "scan": ";; the head follows the buffer", "follow": "loop $scan", "predelay": "(local.set $at (i32.add",
	"gain": "$gain", "dry": "(f32.ne (call $input", "stereo": "br_if $channels", "load": "(if (i32.eqz (local.get $n)) (then",
}

func checkConvParts(t *testing.T, name, wat, parts string) {
	t.Helper()
	i := strings.Index(wat, "$convRead")
	if i < 0 {
		t.Fatalf("%s: the player has no convolution", name)
	}
	code := wat[i:]
	code = code[:strings.Index(code, "$input returns")]
	has := map[string]bool{}
	for _, p := range strings.Fields(parts) {
		has[p] = true
	}
	for part, marker := range convMarkers {
		if got := strings.Contains(code, marker); got != has[part] {
			t.Errorf("%s: the player has %s: %v, want %v", name, part, got, has[part])
		}
	}
	// the fades: a ramp, the whole change at once, or one of them by the unit
	ramp, both := strings.Contains(code, "(f32.const 0) (f32.div (local.get $scale)"), strings.Contains(code, "(select (f32.const 0) (local.get $scale)")
	if ramp != (has["fade"] && !has["nofade"]) || both != (has["fade"] && has["nofade"]) {
		t.Errorf("%s: the player has the ramp: %v, both: %v", name, ramp, both)
	}
}

func TestConvolutionWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, c := range convCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			song := c.song()
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			_, files := compileWasm(t, wat2wasm, song, nil, nil)
			checkConvParts(t, c.name, files[".wat"], c.parts)
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}

// TestConvolutionSampleWasmMatchesGoSynth convolves with the responses of
// samples, which are read once, at the first sample: a mono and a stereo
// one, alone and next to a written response.
func TestConvolutionSampleWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	ff, err := ffmpeg.Find("")
	if err != nil {
		t.Skipf("ffmpeg not found: %v", err)
	}
	burst := func(i, c int) float64 {
		return math.Sin(float64(i*i%977+c*31)) * math.Exp(-float64(i)/3000) * 0.3
	}
	for _, c := range []struct {
		name  string
		parts string
		units []sointu.Unit
	}{
		{"head", "load", []sointu.Unit{conv(0, 0, sointu.ParamMap{"buffer": 2})}},
		{"samples", "fft load stereo", []sointu.Unit{conv(0, 66, sointu.ParamMap{"buffer": 2}), conv(1, 60, sointu.ParamMap{"buffer": 3, "start": 50}), conv(1, 70, sointu.ParamMap{"buffer": 2})}},
		// a sample is not read again, so only the other unit counts for the fade
		{"sample and written", "fft load scan fade", []sointu.Unit{conv(0, 66, sointu.ParamMap{"buffer": 3}), conv(0, 62, sointu.ParamMap{"fade": 1})}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			song := convTestSong(20000, 1, false, c.units...)
			flac := &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
			samples := sointu.Buffers{
				{ID: 2, Name: "mono", Channels: 1, Sample: &sointu.AudioSample{Data: testWav(1, 12000, burst), Encoding: flac}},
				{ID: 3, Name: "stereo", Channels: 2, Sample: &sointu.AudioSample{Data: testWav(2, 9000, burst), Encoding: flac}},
			}
			played := sointu.Song{Buffers: nil}
			for _, b := range samples { // those that the units use, as the compiler takes them
				for _, u := range c.units {
					if u.Parameters["buffer"] == b.ID && len(played.Buffers) < 2 && (len(played.Buffers) == 0 || played.Buffers[0].ID != b.ID) {
						played.Buffers = append(played.Buffers, b)
					}
				}
			}
			played.EncodingPresets = song.EncodingPresets
			encoded, audio, runnerArgs, channels := encodeTestBuffers(t, ff, played)
			song.Buffers = append(song.Buffers, played.Buffers...)
			want, err := sointu.PlayWithBuffers(vm.GoSynther{}, song, audio, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			_, files := compileWasm(t, wat2wasm, song, encoded, nil)
			checkConvParts(t, c.name, files[".wat"], c.parts)
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, encoded, runnerArgs, channels))
		})
	}
}

// TestConvolutionOnlyWhenUsed checks that a song without convolution units
// compiles to a player that has nothing of them, and that x86 refuses them.
func TestConvolutionOnlyWhenUsed(t *testing.T) {
	t.Parallel()
	song := convTestSong(20000, 1, false, conv(0, 66, nil))
	for _, arch := range []string{"386", "amd64"} {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		alone := song // without the instrument that writes the buffer, which x86 has not either
		alone.Patch, alone.Score.Tracks = song.Patch[1:], song.Score.Tracks[1:]
		if _, _, err := com.Song(&alone); err == nil || !strings.Contains(err.Error(), "convolution") {
			t.Errorf("compiling convolution for %v: %v, want an error naming the unit", arch, err)
		}
	}
	song.Patch[1].Units = append(song.Patch[1].Units[:3:3], song.Patch[1].Units[4:]...)
	com, _ := compiler.New("linux", "wasm", false, false)
	files, _, err := com.Song(&song)
	if err != nil {
		t.Fatal(err)
	}
	if wat := strings.ToLower(files[".wat"]); strings.Contains(wat, "$conv") || strings.Contains(wat, "convolution") || strings.Contains(wat, "$fft") {
		t.Errorf("a song without convolution units compiles to a player that mentions them")
	}
}
