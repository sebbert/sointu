package compiler_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// bufreadWasmRunner instantiates a wasm player with the buffer audio given as
// raw float32 files, checks the custom sections and writes the rendered
// output. Usage: node runner.js player.wasm output.raw encoded0 audio0 ...
const bufreadWasmRunner = `
const fs = require('fs');
const [wasmFile, outFile, ...bufferFiles] = process.argv.slice(2);
(async () => {
  const module = await WebAssembly.compile(fs.readFileSync(wasmFile));
  const sections = WebAssembly.Module.customSections(module, 'sointu.buffer');
  const audio = [];
  for (let i = 0; i < bufferFiles.length; i += 2) {
    const encoded = fs.readFileSync(bufferFiles[i]);
    const section = Buffer.from(sections[i / 2]);
    if (!section.equals(encoded)) throw new Error('custom section ' + i / 2 + ' does not match the encoded sample');
    const raw = fs.readFileSync(bufferFiles[i + 1]);
    audio.push(new Float32Array(raw.buffer, raw.byteOffset, raw.byteLength / 4));
  }
  if (sections.length !== audio.length) throw new Error('got ' + sections.length + ' custom sections, want ' + audio.length);
  const channels = JSON.parse(process.env.CHANNELS);
  const { exports } = await WebAssembly.instantiate(module, {
    m: Math,
    s: { b: (i, f, c) => audio[i][f * channels[i] + c] ?? 0 },
  });
  fs.writeFileSync(outFile, Buffer.from(exports.m.buffer, exports.s.value, exports.l.value));
})().catch(e => { console.error(e); process.exit(1); });
`

func testWav(channels, frames int, f func(frame, channel int) float64) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + frames*channels*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(channels), uint32(44100), uint32(44100 * channels * 2), uint16(channels * 2), uint16(16)} {
		w(v)
	}
	b.WriteString("data")
	w(uint32(frames * channels * 2))
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			w(int16(f(i, c) * 16000))
		}
	}
	return b.Bytes()
}

func TestBufreadWasmMatchesGoSynth(t *testing.T) {
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
	sine := func(freq float64) func(int, int) float64 {
		return func(i, c int) float64 { return math.Sin(2*math.Pi*freq*float64(i+c*7)/44100) * (1 - float64(i)/20000) }
	}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		EncodingPresets: sointu.EncodingPresets{
			{Name: "Opus", Encoding: sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus", "-b:a", "64k"}}},
		},
		Buffers: sointu.Buffers{
			{ID: 1, Name: "mono", Channels: 1, Sample: &sointu.AudioSample{Data: testWav(1, 20000, sine(440)), Encoding: &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}}},
			{ID: 2, Name: "stereo", Channels: 2, Sample: &sointu.AudioSample{Data: testWav(2, 20000, sine(300)), Preset: "Opus"}},
		},
		Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 72, 1, 55, 1}}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 64, 1, 1, 1, 1, 0}}}, // silent until triggered
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1}}},
			{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 67, 1, 60, 1, 53, 1}}},
		}},
		Patch: sointu.Patch{
			{Name: "mono", NumVoices: 1, Units: []sointu.Unit{
				{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 70, "gain": 100, "buffer": 1, "notetracking": 1}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "stereo loop", NumVoices: 1, Units: []sointu.Unit{
				{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 70, "detune": 64, "gain": 128, "buffer": 2, "notetracking": 1, "start": 500, "loop": 1, "loopstart": 1000, "looplength": 3000}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
			{Name: "missing", NumVoices: 1, Units: []sointu.Unit{
				{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "buffer": 99, "notetracking": 0}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}},
			{Name: "modulated loop", NumVoices: 1, Units: []sointu.Unit{
				// start modulated by an LFO, loop start and loop length by
				// constants, with a crossfade. Positions are whole frames, so
				// the tiny differences between the Go and wasm oscillators
				// would make modulated loop points differ by a frame now and
				// then.
				{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 70, "detune": 64, "phase": 0, "color": 64, "shape": 64, "gain": 128, "type": sointu.Sine, "lfo": 1}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 90, "target": 100, "port": 3, "sendpop": 1}},
				{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 90}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 70, "target": 100, "port": 4, "sendpop": 0}},
				{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": 40, "target": 100, "port": 5, "sendpop": 1}},
				{ID: 100, Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 128, "buffer": 1, "notetracking": 1, "start": 100, "loop": 1, "loopstart": 2000, "looplength": 4000, "fade": 1500}},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
			}},
		},
	}

	dir := t.TempDir()
	cache := ffmpeg.NewCache(ff, "")
	encoded := map[int]compiler.EncodedBuffer{}
	audio := map[int]sointu.BufferAudio{}
	var runnerArgs []string
	var channels []string
	for _, buf := range song.Buffers {
		enc, err := song.SampleEncoding(buf.Sample)
		if err != nil {
			t.Fatal(err)
		}
		r, err := cache.Get(buf.Sample.Data, enc, buf.Channels)
		if err != nil {
			t.Skipf("encoding %s failed (encoder missing?): %v", buf.Name, err)
		}
		encoded[buf.ID] = compiler.EncodedBuffer{Encoded: r.Encoded, Frames: r.Audio.Frames(), Channels: buf.Channels}
		audio[buf.ID] = r.Audio
		encFile, audioFile := filepath.Join(dir, buf.Name+".enc"), filepath.Join(dir, buf.Name+".f32")
		os.WriteFile(encFile, r.Encoded, 0o644)
		raw := make([]byte, 4*len(r.Audio.Data))
		for i, v := range r.Audio.Data {
			binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
		}
		os.WriteFile(audioFile, raw, 0o644)
		runnerArgs = append(runnerArgs, encFile, audioFile)
		channels = append(channels, string(rune('0'+buf.Channels)))
	}

	want, err := sointu.PlayWithBuffers(vm.GoSynther{}, song, audio, nil)
	if err != nil {
		t.Fatalf("Go synth failed: %v", err)
	}

	com, err := compiler.New("", "wasm", false, false)
	if err != nil {
		t.Fatal(err)
	}
	com.Buffers = encoded
	files, _, err := com.Song(&song)
	if err != nil {
		t.Fatalf("compiling failed: %v", err)
	}
	watFile, wasmFile := filepath.Join(dir, "song.wat"), filepath.Join(dir, "song.wasm")
	os.WriteFile(watFile, []byte(files[".wat"]), 0o644)
	if out, err := exec.Command(wat2wasm, "--enable-annotations", "-o", wasmFile, watFile).CombinedOutput(); err != nil {
		t.Fatalf("wat2wasm failed: %v\n%s", err, out)
	}
	runner, outFile := filepath.Join(dir, "runner.js"), filepath.Join(dir, "out.raw")
	os.WriteFile(runner, []byte(bufreadWasmRunner), 0o644)
	cmd := exec.Command(node, append([]string{runner, wasmFile, outFile}, runnerArgs...)...)
	cmd.Env = append(os.Environ(), "CHANNELS=["+strings.Join(channels, ",")+"]")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running the wasm player failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]float32, len(raw)/4)
	for i := range got {
		got[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	if len(got) != 2*len(want) {
		t.Fatalf("wasm rendered %d frames, Go %d", len(got)/2, len(want))
	}
	maxDiff, maxAbs := 0.0, 0.0
	for i, frame := range want {
		for c := range 2 {
			maxDiff = max(maxDiff, math.Abs(float64(got[2*i+c]-frame[c])))
			maxAbs = max(maxAbs, math.Abs(float64(frame[c])))
		}
	}
	if maxAbs < 0.1 {
		t.Fatalf("the song is almost silent (peak %v); the test is not testing anything", maxAbs)
	}
	if maxDiff > 1e-5 {
		t.Errorf("wasm and Go outputs differ by up to %v (peak %v)", maxDiff, maxAbs)
	}
	t.Logf("peak %v, max difference %v", maxAbs, maxDiff)
}

// TestBufreadWasmInBrowser renders songs with samples in headless Chrome,
// decoded by the browser like the example loader does, and compares them
// with the Go synth playing the samples as decoded by ffmpeg. FLAC is
// lossless at 44.1 kHz, so only the browser's int16 to float conversion
// differs; Opus decodes at 48 kHz, and the browser and ffmpeg resample it
// differently. Launching Chrome may trigger OS permission prompts, so the
// test only runs with SOINTU_TEST_BROWSER=1.
func TestBufreadWasmInBrowser(t *testing.T) {
	if os.Getenv("SOINTU_TEST_BROWSER") != "1" {
		t.Skip("set SOINTU_TEST_BROWSER=1 to run the wasm player in headless Chrome")
	}
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
	_, self, _, _ := runtime.Caller(0)
	renderer := filepath.Join(filepath.Dir(self), "..", "..", "tests", "wasm_browser_renderer.mjs")
	for _, tc := range []struct {
		name      string
		encoding  sointu.Encoding
		tolerance float64
	}{
		{"flac", sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}, 1e-4},
		// Opus pads the start with 312 samples at 48 kHz, a fractional number
		// of frames at 44.1 kHz, which ffmpeg and browsers trim and resample
		// slightly differently: expect differences of a few percent
		{"opus", sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus", "-b:a", "96k"}}, 0.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := tc.encoding
			song := sointu.Song{
				BPM: 120, RowsPerBeat: 4,
				Buffers: sointu.Buffers{
					{ID: 1, Name: "mono", Channels: 1, Sample: &sointu.AudioSample{Data: testWav(1, 20000, func(i, c int) float64 { return math.Sin(2 * math.Pi * 440 * float64(i) / 44100) }), Encoding: &enc}},
					{ID: 2, Name: "stereo", Channels: 2, Sample: &sointu.AudioSample{Data: testWav(2, 20000, func(i, c int) float64 { return math.Sin(2*math.Pi*300*float64(i+7*c)/44100) * 0.8 }), Encoding: &enc}},
				},
				Score: sointu.Score{RowsPerPattern: 8, Length: 1, Tracks: []sointu.Track{
					{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{60, 1, 1, 1, 72, 1, 55, 1}}},
					{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{64, 1, 1, 1, 1, 1, 1, 0}}},
				}},
				Patch: sointu.Patch{
					{NumVoices: 1, Units: []sointu.Unit{
						{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 64, "buffer": 1, "notetracking": 1}},
						{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
					}},
					{NumVoices: 1, Units: []sointu.Unit{
						{Type: "bufread", Parameters: sointu.ParamMap{"stereo": 1, "transpose": 64, "detune": 64, "gain": 64, "buffer": 2, "notetracking": 1, "loop": 1, "loopstart": 1000, "looplength": 5000}},
						{Type: "out", Parameters: sointu.ParamMap{"stereo": 1, "gain": 128}},
					}},
				},
			}
			dir := t.TempDir()
			cache := ffmpeg.NewCache(ff, "")
			encoded := map[int]compiler.EncodedBuffer{}
			audio := map[int]sointu.BufferAudio{}
			for _, buf := range song.Buffers {
				r, err := cache.Get(buf.Sample.Data, *buf.Sample.Encoding, buf.Channels)
				if err != nil {
					t.Skipf("encoding failed (encoder missing?): %v", err)
				}
				encoded[buf.ID] = compiler.EncodedBuffer{Encoded: r.Encoded, Frames: r.Audio.Frames(), Channels: buf.Channels}
				audio[buf.ID] = r.Audio
			}
			want, err := sointu.PlayWithBuffers(vm.GoSynther{}, song, audio, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw := make([]byte, 8*len(want))
			for i, fr := range want {
				binary.LittleEndian.PutUint32(raw[8*i:], math.Float32bits(fr[0]))
				binary.LittleEndian.PutUint32(raw[8*i+4:], math.Float32bits(fr[1]))
			}
			expected := filepath.Join(dir, "expected.raw")
			os.WriteFile(expected, raw, 0o644)
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			com.Buffers = encoded
			files, _, err := com.Song(&song)
			if err != nil {
				t.Fatal(err)
			}
			watFile, wasmFile := filepath.Join(dir, "song.wat"), filepath.Join(dir, "song.wasm")
			os.WriteFile(watFile, []byte(files[".wat"]), 0o644)
			if out, err := exec.Command(wat2wasm, "--enable-annotations", "-o", wasmFile, watFile).CombinedOutput(); err != nil {
				t.Fatalf("wat2wasm failed: %v\n%s", err, out)
			}
			out, err := exec.Command(node, renderer, wasmFile, expected, "--tolerance", fmt.Sprint(tc.tolerance)).CombinedOutput()
			t.Logf("%s", out)
			if err != nil {
				t.Errorf("browser rendering differs or failed: %v", err)
			}
		})
	}
}
