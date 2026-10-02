package compiler_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm/compiler"
	"gopkg.in/yaml.v3"
)

// progressiveWasmRunner renders a song with the player that renders at
// instantiation and with the progressive player in parts, once for each split
// in SPLITS (an array of arrays: the rows of each call to r, repeated until
// the song ends), and fails if a render in parts differs in any byte. With
// UNEVEN, only the whole song is compared: the speed unit changes the lengths
// of the rows. Buffer audio is given like to bufreadWasmRunner.
// Usage: node runner.js oneshot.wasm progressive.wasm audio0 audio1 ...
const progressiveWasmRunner = `
const fs = require('fs');
const [oneshotFile, progressiveFile, ...bufferFiles] = process.argv.slice(2);
(async () => {
  const audio = bufferFiles.map(f => { const raw = fs.readFileSync(f); return new Float32Array(raw.buffer, raw.byteOffset, raw.byteLength / 4); });
  const channels = JSON.parse(process.env.CHANNELS);
  const imports = { m: Math, s: { b: (i, f, c) => audio[i][f * channels[i] + c] ?? 0 } };
  const one = (await WebAssembly.instantiate(await WebAssembly.compile(fs.readFileSync(oneshotFile)), imports)).exports;
  const want = Buffer.from(one.m.buffer, one.s.value, one.l.value);
  const module = await WebAssembly.compile(fs.readFileSync(progressiveFile));
  const rows = +process.env.ROWS;
  let failed = false;
  for (const split of JSON.parse(process.env.SPLITS)) {
    const { exports } = await WebAssembly.instantiate(module, imports);
    const out = () => Buffer.from(exports.m.buffer, exports.s.value, exports.l.value);
    if (exports.l.value !== one.l.value) throw new Error('the output lengths differ');
    if (out().some(v => v)) throw new Error('the progressive player rendered at instantiation');
    const rowBytes = one.l.value / rows, zero = Buffer.alloc(rowBytes);
    for (let done = 0, i = 0; done < rows; i++) {
      const n = Math.min(split[i % split.length], rows - done);
      exports.r(n);
      // a call renders its rows, and nothing after them
      const from = rowBytes * done, to = rowBytes * (done + n);
      if (!process.env.UNEVEN && (!out().subarray(from, to).equals(want.subarray(from, to)) || !out().subarray(to, to + rowBytes).equals(zero.subarray(0, Math.min(rowBytes, one.l.value - to))))) {
        console.error('split ' + JSON.stringify(split) + ': rows ' + done + ' to ' + (done + n) + ' differ');
        failed = true;
        break;
      }
      done += n;
    }
    // and the rows rendered before stay as they were
    if (!failed && !out().equals(want)) {
      console.error('split ' + JSON.stringify(split) + ': the song differs');
      failed = true;
    }
    // the sync values, in songs that have them
    if (one.y && !Buffer.from(exports.m.buffer, exports.y.value, exports.z.value).equals(Buffer.from(one.m.buffer, one.y.value, one.z.value))) {
      console.error('split ' + JSON.stringify(split) + ': the sync values differ');
      failed = true;
    }
  }
  if (!want.some(v => v)) throw new Error('the song is silent');
  process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
`

// progressiveSplits are the splits tried for a song: row by row, uneven parts
// that cross the pattern boundaries, the patterns, and all at once; for the
// songs that take seconds to render, only the uneven parts.
func progressiveSplits(song sointu.Song) string {
	if song.Score.LengthInRows()*song.SamplesPerRow() > 5*44100 {
		return "[[3,1,7]]"
	}
	return fmt.Sprintf("[[1],[3,1,7],[%d],[%d]]", song.Score.RowsPerPattern, song.Score.LengthInRows())
}

func runProgressive(t *testing.T, node, wat2wasm string, song sointu.Song, encoded map[int]compiler.EncodedBuffer, runnerArgs, channels []string, configure func(*compiler.Compiler)) {
	t.Helper()
	oneshot, _ := compileWasm(t, wat2wasm, song, encoded, func(c *compiler.Compiler) {
		if configure != nil {
			configure(c)
		}
	})
	progressive, _ := compileWasm(t, wat2wasm, song, encoded, func(c *compiler.Compiler) {
		if configure != nil {
			configure(c)
		}
		c.Progressive = true
	})
	runner := filepath.Join(t.TempDir(), "runner.js")
	os.WriteFile(runner, []byte(progressiveWasmRunner), 0o644)
	var audioFiles []string
	for i := 1; i < len(runnerArgs); i += 2 {
		audioFiles = append(audioFiles, runnerArgs[i])
	}
	cmd := exec.Command(node, append([]string{runner, oneshot, progressive}, audioFiles...)...)
	cmd.Env = append(os.Environ(),
		"CHANNELS=["+strings.Join(channels, ",")+"]",
		fmt.Sprintf("ROWS=%d", song.Score.LengthInRows()),
		"SPLITS="+progressiveSplits(song))
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "speed" {
				cmd.Env = append(cmd.Env, "UNEVEN=1")
			}
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("rendering in parts failed: %v\n%s", err, out)
	}
}

// TestProgressiveWasmMatchesOneShot renders the regression test songs and the
// example songs (of the long ones, the first 2 seconds, or the first 10 with
// SOINTU_TEST_LONG=1) with the progressive
// wasm player, in parts of different sizes: each must give exactly the bytes
// of the player that renders at instantiation, which other tests compare
// with the Go synth.
func TestProgressiveWasmMatchesOneShot(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	files, err := filepath.Glob(filepath.Join("..", "..", "tests", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	examples, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.yml"))
	for _, f := range append(files, examples...) {
		name := strings.TrimSuffix(filepath.Base(f), ".yml")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if strings.Contains(name, "sample") {
				t.Skip("samples (gm.dls) are not supported by the wasm player")
			}
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var song sointu.Song
			if err := yaml.Unmarshal(data, &song); err != nil {
				t.Fatal(err)
			}
			if ffmpeg.NeedsFFmpeg(&song) {
				t.Skip("the song has samples; TestProgressiveWasmWithSamples covers them")
			}
			if name == "soundset" && !longTests() {
				t.Skip("long: set SOINTU_TEST_LONG=1 to render it")
			}
			// of the long songs, the patterns of the first 2 seconds, or
			// of the first 10 with SOINTU_TEST_LONG=1
			seconds := 2
			if longTests() {
				seconds = 10
			}
			if patterns := seconds * 44100 / (song.SamplesPerRow() * song.Score.RowsPerPattern); song.Score.Length > max(patterns, 1) {
				song.Score.Length = max(patterns, 1)
			}
			runProgressive(t, node, wat2wasm, song, nil, nil, nil, nil)
			if name == "test_chords" {
				// 16 bit output
				runProgressive(t, node, wat2wasm, song, nil, nil, nil, func(c *compiler.Compiler) { c.Output16Bit = true })
			}
		})
	}
}

// TestProgressiveWasmWithSamples renders a song with samples in parts: the
// first call fills the buffers from the host.
func TestProgressiveWasmWithSamples(t *testing.T) {
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
	song := bufreadTestSong()
	encoded, _, runnerArgs, channels := encodeTestBuffers(t, ff, song)
	runProgressive(t, node, wat2wasm, song, encoded, runnerArgs, channels, nil)
}
