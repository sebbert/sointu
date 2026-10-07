package compiler_test

import (
	"bytes"
	"compress/gzip"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
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

// TestConvolutionExample checks that examples/convolution.yml has the
// Convolution reverb module preset writing its own response, that its
// reverb is heard after the notes end, and that the wasm player renders it
// like the Go synth.
func TestConvolutionExample(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../examples/convolution.yml")
	if err != nil {
		t.Fatal(err)
	}
	var song sointu.Song
	if err := yaml.Unmarshal(data, &song); err != nil {
		t.Fatal(err)
	}
	preset, err := os.ReadFile("../../tracker/modules/Convolution_reverb.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), string(preset)) {
		t.Errorf("the module of the example is not the module preset Convolution reverb")
	}
	want, err := sointu.Play(vm.GoSynther{}, song, nil)
	if err != nil {
		t.Fatal(err)
	}
	// the last notes end in row 62; the reverb goes on, and has ended by
	// the last pattern
	row := song.SamplesPerRow()
	rms := func(from, to int) float64 {
		sum := 0.0
		for _, s := range want[from*row : to*row] {
			sum += float64(s[0])*float64(s[0]) + float64(s[1])*float64(s[1])
		}
		return 10 * math.Log10(sum/float64(2*(to-from)*row)+1e-30)
	}
	if tail := rms(66, 72); tail < -65 || tail > -45 {
		t.Errorf("the reverb is at %.1f dB half a second after the last note", tail)
	}
	if end := rms(84, 96); end > -100 {
		t.Errorf("the reverb is at %.1f dB in the last rows", end)
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

// TestConvolutionStages renders songs with convolution units in stages:
// the states of the units of a later stage come after those of the stages
// before it, and a unit and the instrument that writes its response are
// not cut apart.
func TestConvolutionStages(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	// a missing buffer shares nothing: every instrument can be a stage
	song := convTestSong(20000, 1, true,
		conv(1, 40, sointu.ParamMap{"buffer": 7, "dry": 100}), conv(0, 66, sointu.ParamMap{"buffer": 7, "dry": 80}), conv(0, 0, sointu.ParamMap{"buffer": 7, "dry": 60}))
	if cuts := testStages(t, node, wat2wasm, song); len(cuts) < 4 {
		t.Errorf("the song without a response can be cut at %v", cuts)
	}
	// the written response binds its readers to the writer: the first
	// instrument, so up to the last reader there is no cut
	song = convTestSong(20000, 1, false, conv(0, 40, sointu.ParamMap{"buffer": 7, "dry": 100}), conv(0, 66, nil), conv(0, 50, sointu.ParamMap{"buffer": 7, "dry": 100}))
	if cuts := testStages(t, node, wat2wasm, song); len(cuts) != 1 || cuts[0] != 3 {
		t.Errorf("the song with a written response can be cut at %v, want [3]", cuts)
	}
}

// wasmSizes returns the bytes of a wasm file, of its data section, and of
// the file compressed with gzip -9.
func wasmSizes(t *testing.T, file string) (total, data, gz int) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for i := 8; i < len(b); {
		id := b[i]
		i++
		size, shift := 0, 0
		for {
			c := b[i]
			i++
			size |= int(c&0x7f) << shift
			shift += 7
			if c < 0x80 {
				break
			}
		}
		if id == 11 {
			data = size
		}
		i += size
	}
	var z bytes.Buffer
	w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
	w.Write(b)
	w.Close()
	return len(b), data, z.Len()
}

// TestConvolutionSizes logs what the unit adds to the wasm player, for each
// of its parts, against the same song without the unit (go test -v).
func TestConvolutionSizes(t *testing.T) {
	t.Parallel()
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	without := func(song sointu.Song) sointu.Song { // the units convolved, not convolved
		for i := 1; i < len(song.Patch); i++ {
			u := song.Patch[i].Units
			song.Patch[i].Units = append(u[:3:3], u[4:]...)
		}
		return song
	}
	sizes := func(song sointu.Song, encoded map[int]compiler.EncodedBuffer) (int, int, int) {
		file, _ := compileWasm(t, wat2wasm, song, encoded, nil)
		return wasmSizes(t, file)
	}
	sample := func(units ...sointu.Unit) (sointu.Song, map[int]compiler.EncodedBuffer) {
		song := convTestSong(20000, 1, false, units...)
		song.Buffers = append(song.Buffers, sointu.Buffer{ID: 2, Name: "sample", Channels: 1, Sample: &sointu.AudioSample{}})
		return song, map[int]compiler.EncodedBuffer{2: {Frames: 3000, Channels: 1}}
	}
	spectral := func(song sointu.Song) sointu.Song { // with spectral units, which have the FFT
		u := song.Patch[1].Units
		song.Buffers = append(song.Buffers, sointu.Buffer{ID: 9, Spectrum: true})
		song.Patch[1].Units = append(u[:len(u)-1:len(u)-1],
			sointu.Unit{Type: "spfft", Parameters: sointu.ParamMap{"stereo": 0, "size": 3, "buffer": 9}},
			sointu.Unit{Type: "spifft", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128, "buffer": 9}}, u[len(u)-1])
		return song
	}
	for _, c := range []struct {
		name string
		with func() (sointu.Song, map[int]compiler.EncodedBuffer)
	}{
		{"written, 64 frames (no FFT)", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 0, nil)), nil
		}},
		{"written, 3 s", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 88, nil)), nil
		}},
		{"written, 3 s, fade", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 88, sointu.ParamMap{"fade": 1})), nil
		}},
		{"written, 3 s, fade, follow", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 88, sointu.ParamMap{"fade": 1, "follow": 2})), nil
		}},
		{"written, 3 s, fade, gain, dry, predelay", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 88, sointu.ParamMap{"fade": 1, "gain": 28, "dry": 64, "predelay": 10})), nil
		}},
		{"written, 3 s, stereo, fade, gain", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 2, false, conv(1, 88, sointu.ParamMap{"fade": 1, "gain": 28})), nil
		}},
		{"two units: fade and not", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return convTestSong(20000, 1, false, conv(0, 88, sointu.ParamMap{"fade": 1}), conv(0, 70, nil)), nil
		}},
		{"sample, 64 frames (no FFT)", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return sample(conv(0, 0, sointu.ParamMap{"buffer": 2}))
		}},
		{"sample, 3 s", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return sample(conv(0, 88, sointu.ParamMap{"buffer": 2}))
		}},
		{"sample and written, 3 s", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return sample(conv(0, 88, sointu.ParamMap{"buffer": 2}), conv(0, 88, nil))
		}},
		{"written, 3 s, in a song with spectral units", func() (sointu.Song, map[int]compiler.EncodedBuffer) {
			return spectral(convTestSong(20000, 1, false, conv(0, 88, nil))), nil
		}},
	} {
		song, encoded := c.with()
		total, data, gz := sizes(song, encoded)
		song, encoded = c.with()
		total0, data0, gz0 := sizes(without(song), encoded)
		t.Logf("%-45s +%4d bytes (code +%4d, data +%3d), gzip +%4d   (%d -> %d, gzip %d -> %d)", c.name, total-total0, total-data-total0+data0, data-data0, gz-gz0, total0, total, gz0, gz)
	}
}

// TestConvolutionSpeed logs the time that the wasm player under node and
// the Go synth take for a sample of a convolution unit, for responses of
// several lengths: a song of 32 s with the unit against the same song
// without it. With SOINTU_TEST_LONG=1 (go test -v).
func TestConvolutionSpeed(t *testing.T) {
	if !longTests() {
		t.Skip("set SOINTU_TEST_LONG=1 to measure")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	long := func(units ...sointu.Unit) sointu.Song {
		song := convTestSong(530000, 2, false, units...)
		song.Score.Length = 16
		for i := range song.Score.Tracks {
			tr := &song.Score.Tracks[i]
			for len(tr.Order) < 16 {
				tr.Order = append(tr.Order, tr.Order[len(tr.Order)-1])
			}
		}
		return song
	}
	measure := func(song sointu.Song) (wasm, goSynth float64) {
		samples := float64(song.Score.LengthInRows() * song.SamplesPerRow())
		wasm, goSynth = math.Inf(1), math.Inf(1)
		for range 3 {
			start := time.Now()
			renderWasm(t, node, wat2wasm, song, nil, nil, nil)
			wasm = min(wasm, float64(time.Since(start).Nanoseconds())/samples)
			start = time.Now()
			if _, err := sointu.Play(vm.GoSynther{}, song, nil); err != nil {
				t.Fatal(err)
			}
			goSynth = min(goSynth, float64(time.Since(start).Nanoseconds())/samples)
		}
		return
	}
	plain := long(conv(0, 0, nil))
	plain.Patch[1].Units = append(plain.Patch[1].Units[:3:3], plain.Patch[1].Units[4:]...)
	wasm0, go0 := measure(plain)
	t.Logf("%-32s wasm %5.0f ns, Go %5.0f ns for a sample of the song", "without the unit", wasm0, go0)
	for _, c := range []struct {
		name string
		unit sointu.Unit
	}{
		{"64 frames", conv(0, 0, nil)},
		{"512 frames", conv(0, 24, nil)},
		{"4096 frames", conv(0, 48, nil)},
		{"1 s", conv(0, 75, nil)},
		{"3 s", conv(0, 88, nil)},
		{"6 s", conv(0, 96, nil)},
		{"12 s", conv(0, 104, nil)},
		{"3 s, fade", conv(0, 88, sointu.ParamMap{"fade": 1})},
		{"3 s, follow 8", conv(0, 88, sointu.ParamMap{"follow": 3})},
		{"3 s stereo", conv(1, 88, nil)},
		{"12 s stereo, fade, follow 8", conv(1, 104, sointu.ParamMap{"fade": 1, "follow": 3})},
		{"3 s, no buffer (not read again)", conv(0, 88, sointu.ParamMap{"buffer": 7})},
	} {
		wasm, goSynth := measure(long(c.unit))
		t.Logf("%-32s wasm +%5.0f ns, Go +%5.0f ns for a sample: %.2f %% and %.2f %% of real time", c.name, wasm-wasm0, goSynth-go0, (wasm-wasm0)*44100/1e7, (goSynth-go0)*44100/1e7)
	}
}
