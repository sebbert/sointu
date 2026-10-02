package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

// TestRuntimeModule checks the JavaScript module that the compiler writes
// with JS, for each way the song changes it: it is a module that parses, with
// only the parts the song needs.
func TestRuntimeModule(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	plain := stageTestSong(
		[]sointu.Unit{{Type: "oscillator", Parameters: sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "phase": 0, "color": 128, "shape": 64, "gain": 100, "type": sointu.Trisaw}}, {Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}}},
		[]sointu.Unit{{Type: "noise", Parameters: sointu.ParamMap{"stereo": 0, "shape": 64, "gain": 60}}, {Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 64}}})
	sampled := bufreadTestSong()
	sampled.Buffers[0].Sample.Encoding = &sointu.Encoding{Format: "flac"}
	encoded := map[int]compiler.EncodedBuffer{1: {Encoded: []byte("one"), Frames: 10, Channels: 1, Format: "flac"}, 2: {Encoded: []byte("two"), Frames: 10, Channels: 2}}
	for _, tc := range []struct {
		name      string
		song      sointu.Song
		configure func(*compiler.Compiler)
		has, not  []string // in the module
		files     []string
	}{
		{"plain", plain, func(c *compiler.Compiler) {}, []string{"new Worker(", "Float32Array(m.buffer"}, []string{"MessageChannel", "decodeAudioData", "sampleFiles", "Int16Array", "m: Math", "tape"}, nil},
		{"stages", plain, func(c *compiler.Compiler) { c.Stages = 2 }, []string{"MessageChannel", "g(stage)", "tape"}, []string{"decodeAudioData"}, nil},
		{"16 bit", plain, func(c *compiler.Compiler) { c.Output16Bit = true }, []string{"Int16Array(m.buffer", "/ 32767"}, []string{"Float32Array(m.buffer"}, nil},
		{"math imports", plain, func(c *compiler.Compiler) { c.MathImports = true }, []string{"{ m: Math }"}, nil, nil},
		{"samples", sampled, func(c *compiler.Compiler) {}, []string{`customSections(module, "sointu.buffer")`, "decodeAudioData", "s: { b:"}, []string{"sampleFiles"}, nil},
		{"separate samples", sampled, func(c *compiler.Compiler) { c.SeparateSamples = true }, []string{"sampleFiles.map", "decodeAudioData", "load = (wasm, sampleFiles, runway"}, []string{"customSections"}, []string{".0.flac", ".1.bin"}},
		{"separate samples in stages", sampled, func(c *compiler.Compiler) { c.SeparateSamples, c.Stages = true, 2 }, []string{"sampleFiles.map", "MessageChannel", "channel.port1, samples]"}, nil, []string{".0.flac", ".1.bin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			com.JS = true
			com.Buffers = encoded
			tc.configure(com)
			files, _, err := com.Song(&tc.song)
			if err != nil {
				t.Fatal(err)
			}
			js, types := files[".js"], files[".d.ts"]
			for _, s := range tc.has {
				if !strings.Contains(js, s) {
					t.Errorf("the module does not have %q", s)
				}
			}
			for _, s := range tc.not {
				if strings.Contains(js, s) {
					t.Errorf("the module has %q", s)
				}
			}
			if strings.Contains(js, "sampleFiles") != strings.Contains(types, "sampleFiles") {
				t.Errorf("the types and the module differ in the sample files")
			}
			for _, ext := range tc.files {
				if files[ext] == "" {
					t.Errorf("no file %v", ext)
				}
			}
			if strings.Contains(files[".wat"], "(@custom") != (len(tc.song.Buffers) > 0 && !com.SeparateSamples) {
				t.Errorf("the samples are in the wrong place")
			}
			if !strings.Contains(files[".wat"], `(export "r")`) || strings.Contains(files[".wat"], "(start") {
				t.Errorf("the player is not progressive")
			}
			file := filepath.Join(t.TempDir(), "song.mjs")
			os.WriteFile(file, []byte(js), 0o644)
			if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
				t.Errorf("the module does not parse: %v\n%s", err, out)
			}
		})
	}
	t.Run("speed", func(t *testing.T) {
		song := stageTestSong([]sointu.Unit{{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 70}}, {Type: "speed", Parameters: sointu.ParamMap{}}})
		com, _ := compiler.New("", "wasm", false, false)
		com.JS = true
		if _, _, err := com.Song(&song); err == nil || !strings.Contains(err.Error(), "speed") {
			t.Errorf("a song with the speed unit got a module: %v", err)
		}
	})
	t.Run("x86", func(t *testing.T) {
		com, _ := compiler.New("windows", "amd64", false, false)
		com.JS = true
		if _, _, err := com.Song(&plain); err == nil {
			t.Errorf("an x86 player got a module")
		}
	})
}

// TestRuntimeInBrowser plays songs with the JavaScript module in headless
// Chrome, and compares what the audio context plays, recorded by an
// AudioWorklet, with the player that renders at instantiation: it must be
// the same samples, without a gap, on a clock that never goes back; in
// workers, in a pipeline of workers, on the main thread, and with audio that
// arrives late, where the song has to wait. The songs play in real time, and
// launching Chrome may trigger OS permission prompts, so the test only runs
// with SOINTU_TEST_BROWSER=1.
func TestRuntimeInBrowser(t *testing.T) {
	if os.Getenv("SOINTU_TEST_BROWSER") != "1" {
		t.Skip("set SOINTU_TEST_BROWSER=1 to run the JavaScript module in headless Chrome")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	_, self, _, _ := runtime.Caller(0)
	harness := filepath.Join(filepath.Dir(self), "..", "..", "tests", "wasm_runtime_browser.mjs")
	data, err := os.ReadFile("../../examples/soundset_loop.yml")
	if err != nil {
		t.Fatal(err)
	}
	var loop sointu.Song
	if err := yaml.Unmarshal(data, &loop); err != nil {
		t.Fatal(err)
	}
	loop.Score.Length = 3 // 5 s
	sampled := bufreadTestSong()
	var encoded map[int]compiler.EncodedBuffer
	if ff, err := ffmpeg.Find(""); err == nil {
		encoded, _, _, _ = encodeTestBuffers(t, ff, sampled)
		for id, e := range encoded {
			e.Format = map[int]string{1: "flac", 2: "ogg"}[id]
			encoded[id] = e
		}
	}
	for _, tc := range []struct {
		name      string
		song      sointu.Song
		scenario  string
		configure func(*compiler.Compiler)
	}{
		{"worker", loop, "play", func(c *compiler.Compiler) {}},
		{"main thread", loop, "noworker", func(c *compiler.Compiler) {}},
		{"late audio", loop, "stall", func(c *compiler.Compiler) {}},
		{"stages", loop, "play", func(c *compiler.Compiler) { c.Stages = 4 }},
		{"stages on the main thread", loop, "noworker", func(c *compiler.Compiler) { c.Stages = 4 }},
		{"stages with late audio", loop, "stall", func(c *compiler.Compiler) { c.Stages = 3 }},
		{"16 bit", loop, "play", func(c *compiler.Compiler) { c.Output16Bit = true }},
		{"samples", sampled, "play", func(c *compiler.Compiler) {}},
		{"samples on the main thread", sampled, "noworker", func(c *compiler.Compiler) {}},
		{"separate samples", sampled, "play", func(c *compiler.Compiler) { c.SeparateSamples = true }},
		{"separate samples in stages", sampled, "play", func(c *compiler.Compiler) { c.SeparateSamples, c.Stages = true, 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffers map[int]compiler.EncodedBuffer
			if len(tc.song.Buffers) > 0 {
				if encoded == nil {
					t.Skip("ffmpeg not found")
				}
				buffers = encoded
			}
			dir := t.TempDir()
			for name, js := range map[string]bool{"oneshot": false, "song": true} {
				com, err := compiler.New("", "wasm", false, false)
				if err != nil {
					t.Fatal(err)
				}
				com.Buffers = buffers
				tc.configure(com)
				com.JS = js
				if !js {
					com.Stages = 0
				}
				files, _, err := com.Song(&tc.song)
				if err != nil {
					t.Fatal(err)
				}
				for ext, content := range files {
					os.WriteFile(filepath.Join(dir, name+ext), []byte(content), 0o644)
				}
				if out, err := exec.Command(wat2wasm, wat2wasmArgs(wat2wasm, "-o", filepath.Join(dir, name+".wasm"), filepath.Join(dir, name+".wat"))...).CombinedOutput(); err != nil {
					t.Fatalf("wat2wasm failed: %v\n%s", err, out)
				}
			}
			out, err := exec.Command(node, harness, dir, "--scenario", tc.scenario).CombinedOutput()
			t.Logf("%s", out)
			if err != nil && strings.Contains(string(out), `"playing caught up with rendering"`) && !strings.Contains(string(out), "differs") {
				// a busy machine: once more, rendering everything first
				out, err = exec.Command(node, harness, dir, "--scenario", tc.scenario, "--runway", "100").CombinedOutput()
				t.Logf("again, with all rendered before the start:\n%s", out)
			}
			if err != nil {
				t.Errorf("the played audio differs, or playing failed: %v", err)
			}
		})
	}
}
