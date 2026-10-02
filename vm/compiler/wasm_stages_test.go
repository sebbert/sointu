package compiler_test

import (
	"encoding/json"
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

// stagesWasmRunner renders a song with the player that renders at
// instantiation and with the stages of a staged player, one after the other
// like the pipeline of workers does: every stage reads the tape that the
// stage before it wrote, chunk by chunk. The audio of the last stage must be
// the bytes of the one-shot render, and so must the audio of the staged
// player when no stage is selected (checked with WHOLE). LAYOUT is the
// compiler.WasmLayout.
// Usage: node runner.js oneshot.wasm staged.wasm
const stagesWasmRunner = `
const fs = require('fs');
const [oneshotFile, stagedFile] = process.argv.slice(2);
(async () => {
  const L = JSON.parse(process.env.LAYOUT);
  const imports = { m: Math, s: { b: () => 0 } };
  let want; // rendered once for all the plans of a song
  if (fs.existsSync(oneshotFile + '.raw')) {
    want = fs.readFileSync(oneshotFile + '.raw');
  } else {
    const one = (await WebAssembly.instantiate(await WebAssembly.compile(fs.readFileSync(oneshotFile)), imports)).exports;
    want = Buffer.from(one.m.buffer, one.s.value, one.l.value);
    fs.writeFileSync(oneshotFile + '.raw', want);
    if (one.y) fs.writeFileSync(oneshotFile + '.sync', Buffer.from(one.m.buffer, one.y.value, one.z.value));
  }
  // the sync values, in songs that have them: a stage gets those of the
  // stages before it for the chunk, and adds its own
  const syncBytes = L.SyncValues * L.SyncTicks * 4;
  const wantSync = syncBytes ? fs.readFileSync(oneshotFile + '.sync') : null;
  const syncs = e => Buffer.from(e.m.buffer, L.Sync, syncBytes);
  const syncRange = (row, n) => [Math.ceil(row * L.RowSamples / 256) * L.SyncValues * 4, Math.ceil((row + n) * L.RowSamples / 256) * L.SyncValues * 4];
  if (!want.some(v => v)) throw new Error('the song is silent');
  const module = await WebAssembly.compile(fs.readFileSync(stagedFile));
  const audio = e => Buffer.from(e.m.buffer, L.Output, L.OutputBytes);
  let exports;
  if (process.env.WHOLE) { // all voices in one instance
    ({ exports } = await WebAssembly.instantiate(module, imports));
    for (let row = 0; row < L.Rows; row += L.ChunkRows) exports.r(Math.min(L.ChunkRows, L.Rows - row));
    if (!audio(exports).equals(want)) throw new Error('the staged player differs without a stage selected');
    if (syncBytes && !syncs(exports).equals(wantSync)) throw new Error('the sync values of the staged player differ without a stage selected');
  }
  // the pipeline
  let tapes = [];
  for (let s = 0; s < L.Stages; s++) {
    ({ exports } = await WebAssembly.instantiate(module, imports));
    exports.g(s);
    const next = [];
    for (let row = 0, c = 0; row < L.Rows; row += L.ChunkRows, c++) {
      const n = Math.min(L.ChunkRows, L.Rows - row);
      const [from, to] = syncRange(row, n);
      if (s > 0) new Uint8Array(exports.m.buffer, L.TapeIn).set(tapes[c][0]);
      if (s > 0) new Uint8Array(exports.m.buffer, L.Sync + from).set(tapes[c][1]);
      exports.r(n);
      if (s < L.Stages - 1) next.push([new Uint8Array(exports.m.buffer, L.TapeOut, n * L.RowSamples * L.StageCells[s + 1] * 4).slice(), new Uint8Array(exports.m.buffer, L.Sync + from, to - from).slice()]);
    }
    if (s < L.Stages - 1 && audio(exports).some(v => v)) throw new Error('stage ' + s + ' wrote audio');
    tapes = next;
  }
  const got = audio(exports);
  if (!got.equals(want)) {
    let i = 0;
    while (got[i] === want[i]) i++;
    throw new Error('the pipeline differs from byte ' + i + ' (frame ' + (i >> 3) + ')');
  }
  if (syncBytes && !syncs(exports).equals(wantSync)) throw new Error('the sync values of the pipeline differ');
  if (syncBytes && !wantSync.some(v => v)) throw new Error('the sync values are all zero');
})().catch(e => { console.error(e.message); process.exit(1); });
`

// renderStages compiles the song with the stages set by configure and
// renders them. It returns the layout, or nil with the error of the compiler
// if the song cannot be cut that way.
func renderStages(t *testing.T, node, wat2wasm, oneshot string, song sointu.Song, configure func(*compiler.Compiler)) (*compiler.WasmLayout, error) {
	t.Helper()
	com, err := compiler.New("", "wasm", false, false)
	if err != nil {
		t.Fatal(err)
	}
	com.Progressive = true
	configure(com)
	files, _, err := com.Song(&song)
	if err != nil {
		return nil, err
	}
	dir := t.TempDir()
	watFile, wasmFile := filepath.Join(dir, "song.wat"), filepath.Join(dir, "song.wasm")
	os.WriteFile(watFile, []byte(files[".wat"]), 0o644)
	if out, err := exec.Command(wat2wasm, wat2wasmArgs(wat2wasm, "-o", wasmFile, watFile)...).CombinedOutput(); err != nil {
		t.Fatalf("wat2wasm failed: %v\n%s", err, out)
	}
	runner := filepath.Join(dir, "runner.js")
	os.WriteFile(runner, []byte(stagesWasmRunner), 0o644)
	layout, _ := json.Marshal(com.Layout)
	cmd := exec.Command(node, runner, oneshot, wasmFile)
	cmd.Env = append(os.Environ(), "LAYOUT="+string(layout))
	if com.Stages > 0 {
		cmd.Env = append(cmd.Env, "WHOLE=1")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("stages %v: %v\n%s", com.Layout.StageVoices, err, out)
	}
	return com.Layout, nil
}

// testStages renders the song cut before every voice where it can be cut,
// and in pipelines of 2 to 4 balanced stages. It returns the cuts.
//
// Without SOINTU_TEST_LONG=1, of a song with more than 6 possible cuts only
// 4 are rendered, spread over the voices, and the pipelines.
func testStages(t *testing.T, node, wat2wasm string, song sointu.Song) (cuts []int) {
	t.Helper()
	oneshot, _ := compileWasm(t, wat2wasm, song, nil, nil)
	for b := 1; b < song.Patch.NumVoices(); b++ {
		com, _ := compiler.New("", "wasm", false, false)
		com.Progressive, com.StageCuts = true, []int{b}
		if _, _, err := com.Song(&song); err == nil {
			cuts = append(cuts, b)
		} else if !strings.Contains(err.Error(), "cannot cut") {
			t.Fatalf("cut before voice %d: %v", b, err)
		}
	}
	for i, b := range cuts {
		if !longTests() && len(cuts) > 6 && i%((len(cuts)+3)/4) != 0 {
			continue
		}
		if _, err := renderStages(t, node, wat2wasm, oneshot, song, func(c *compiler.Compiler) { c.StageCuts = []int{b} }); err != nil {
			t.Fatalf("cut before voice %d: %v", b, err)
		}
	}
	for n := 2; n <= 4; n++ {
		layout, err := renderStages(t, node, wat2wasm, oneshot, song, func(c *compiler.Compiler) { c.Stages = n; c.ChunkRows = n })
		if err != nil {
			t.Fatalf("%d stages: %v", n, err)
		}
		want := min(n, len(cuts)+1)
		if want == 1 {
			want = 0 // a song that cannot be cut has no stages
		}
		if layout.Stages != want {
			t.Errorf("asked for %d stages with %d possible cuts: got %d stages, want %d", n, len(cuts), layout.Stages, want)
		}
	}
	return
}

// longTests reports whether the tests render the long songs, and every cut
// of the songs with many voices: with SOINTU_TEST_LONG=1, and without
// -short. The default run covers every feature with short songs.
func longTests() bool { return os.Getenv("SOINTU_TEST_LONG") == "1" && !testing.Short() }

// TestStagedWasmMatchesOneShot renders the regression test songs and the
// example songs in stages, with every cut the compiler allows (of the songs
// with many voices, four of them unless SOINTU_TEST_LONG=1): the pipeline
// must give exactly the bytes of the player that renders at instantiation.
// The long songs are cut to their first second.
func TestStagedWasmMatchesOneShot(t *testing.T) {
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
	total := 0
	t.Run("songs", func(t *testing.T) {
		for _, f := range append(files, examples...) {
			name := strings.TrimSuffix(filepath.Base(f), ".yml")
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if strings.Contains(name, "sample") || name == "test_sync" || name == "test_speed" {
					t.Skip("not supported by the wasm player, or not in stages")
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
					t.Skip("the song has samples")
				}
				if name == "soundset" && !longTests() {
					t.Skip("long: set SOINTU_TEST_LONG=1 to render it")
				}
				// at most a second, from the start of the song
				if rows := 44100 / song.SamplesPerRow(); song.Score.LengthInRows() > rows {
					song.Score.Length = max(1, min(song.Score.Length, rows/song.Score.RowsPerPattern))
					song.Score.RowsPerPattern = min(song.Score.RowsPerPattern, rows)
				}
				if song.Patch.NumVoices() < 2 {
					return
				}
				cuts := testStages(t, node, wat2wasm, song)
				t.Logf("%d voices, cuts before %v", song.Patch.NumVoices(), cuts)
				total += len(cuts)
			})
		}
	})
	if total < 20 {
		t.Errorf("only %d cuts were tested", total)
	}
}

// stageTestSong is a song of instruments that play a chord of the given
// units, each a voice.
func stageTestSong(instruments ...[]sointu.Unit) sointu.Song {
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 8, Length: 2}}
	for i, units := range instruments {
		song.Patch = append(song.Patch, sointu.Instrument{Name: fmt.Sprint("i", i), NumVoices: 1, Units: units})
		song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: 1, Order: sointu.Order{0, 0},
			Patterns: []sointu.Pattern{{byte(60 + 3*i), 1, 1, 0, byte(67 + i), 1, 0, byte(55 + 2*i)}}})
	}
	return song
}

// TestStageCuts checks where songs can be cut, for the ways voices depend
// on each other, and that the cuts allowed render exactly.
func TestStageCuts(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	p := func(kv ...any) sointu.ParamMap {
		m := sointu.ParamMap{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1].(int)
		}
		return m
	}
	env := sointu.Unit{Type: "envelope", Parameters: p("stereo", 0, "attack", 40, "decay", 64, "sustain", 64, "release", 64, "gain", 128)}
	osc := func(id int) sointu.Unit {
		return sointu.Unit{ID: id, Type: "oscillator", Parameters: p("stereo", 0, "transpose", 64, "detune", 64, "phase", 0, "color", 128, "shape", 64, "gain", 100, "type", sointu.Trisaw)}
	}
	noise := sointu.Unit{Type: "noise", Parameters: p("stereo", 0, "shape", 64, "gain", 60)}
	stereoNoise := sointu.Unit{Type: "noise", Parameters: p("stereo", 1, "shape", 64, "gain", 60)}
	mulp := sointu.Unit{Type: "mulp", Parameters: p("stereo", 0)}
	sync := sointu.Unit{Type: "sync", Parameters: p()}
	addp := sointu.Unit{Type: "addp", Parameters: p("stereo", 0)}
	out := sointu.Unit{Type: "out", Parameters: p("stereo", 0, "gain", 64)}
	stereoOut := sointu.Unit{Type: "out", Parameters: p("stereo", 1, "gain", 64)}
	aux := func(ch int) sointu.Unit {
		return sointu.Unit{Type: "aux", Parameters: p("stereo", 1, "gain", 100, "channel", ch)}
	}
	in := func(ch int) sointu.Unit { return sointu.Unit{Type: "in", Parameters: p("stereo", 1, "channel", ch)} }
	pan := sointu.Unit{Type: "pan", Parameters: p("stereo", 0, "panning", 40)}
	send := func(target, port, voice int) sointu.Unit {
		return sointu.Unit{Type: "send", Parameters: p("stereo", 0, "amount", 96, "target", target, "port", port, "voice", voice, "sendpop", 0)}
	}
	delay := sointu.Unit{Type: "delay", Parameters: p("stereo", 0, "pregain", 64, "dry", 100, "feedback", 80, "damp", 64, "notetracking", 0), VarArgs: []int{3000, 4411}}
	reverb := func(id, size int) sointu.Unit {
		return sointu.Unit{ID: id, Type: "reverb", Parameters: reverbParams(sointu.ParamMap{"size": size, "decay": 70, "highs": 60, "lows": 70, "predelay": 20, "mod": 30, "highcut": 100, "lowcut": 40})}
	}
	voice := []sointu.Unit{env, osc(0), mulp, out}
	noisy := []sointu.Unit{env, noise, mulp, delay, out}
	for _, tc := range []struct {
		name string
		song sointu.Song
		cuts []int
	}{
		{"independent voices", stageTestSong(voice, noisy, voice, noisy), []int{1, 2, 3}},
		{"noise on both sides, mono and stereo", stageTestSong(noisy, []sointu.Unit{env, stereoNoise, mulp, mulp, out}, noisy, []sointu.Unit{stereoNoise, stereoOut}), []int{1, 2, 3}},
		{"aux read by the last voice", stageTestSong(
			[]sointu.Unit{env, osc(0), mulp, pan, aux(2)}, noisy, []sointu.Unit{env, noise, mulp, pan, aux(2)},
			[]sointu.Unit{in(2), delay, stereoOut}), []int{1, 2, 3}},
		{"aux read by an earlier voice", stageTestSong(
			voice, []sointu.Unit{in(4), stereoOut}, noisy, []sointu.Unit{env, osc(0), mulp, pan, aux(4)}, voice), []int{1, 4}},
		{"aux that nothing reads", stageTestSong(
			[]sointu.Unit{env, osc(0), mulp, pan, aux(6), env, osc(0), mulp, out}, voice, []sointu.Unit{env, osc(0), mulp, pan, aux(6), env, noise, mulp, out}, voice), []int{3}},
		{"main output read and written again", stageTestSong(
			voice, noisy, []sointu.Unit{in(0), delay, stereoOut}, voice), []int{1, 2, 3}},
		{"send to a later voice", stageTestSong(
			[]sointu.Unit{env, send(7, 1, 0), osc(0), mulp, out}, voice, []sointu.Unit{env, osc(7), mulp, out}, voice), []int{1, 2, 3}},
		{"send to an earlier voice", stageTestSong(
			voice, []sointu.Unit{env, osc(7), mulp, out}, voice, []sointu.Unit{env, send(7, 1, 0), osc(0), mulp, out}, voice), []int{1, 4}},
		{"sends from both sides", stageTestSong(
			[]sointu.Unit{env, send(7, 1, 0), osc(0), mulp, out}, []sointu.Unit{env, osc(7), mulp, out}, voice, []sointu.Unit{env, send(7, 1, 0), osc(0), mulp, out}), []int{}},
		{"send to a port that is not cleared", stageTestSong(
			[]sointu.Unit{env, send(7, 7, 0), osc(0), mulp, out}, voice, []sointu.Unit{env, osc(7), mulp, out}, voice), []int{3}},
		{"sync units on both sides", stageTestSong(
			[]sointu.Unit{env, sync, osc(0), mulp, out}, []sointu.Unit{env, noise, mulp, sync, out}, voice, []sointu.Unit{env, sync, osc(0), sync, mulp, out}), []int{1, 2, 3}},
		{"reverb units on both sides", stageTestSong(
			[]sointu.Unit{env, osc(0), mulp, pan, reverb(0, 20), stereoOut}, voice,
			[]sointu.Unit{env, noise, mulp, pan, reverb(0, 40), reverb(0, 10), stereoOut}, []sointu.Unit{env, osc(0), mulp, pan, reverb(0, 20), stereoOut}), []int{1, 2, 3}},
		{"send to a reverb of a later voice", stageTestSong(
			[]sointu.Unit{env, send(9, 1, 0), osc(0), mulp, out}, voice, []sointu.Unit{env, noise, mulp, pan, reverb(9, 30), stereoOut}, voice), []int{1, 2, 3}},
		{"signal passed on the stack", stageTestSong(
			[]sointu.Unit{env, osc(0), mulp}, voice, []sointu.Unit{env, osc(0), mulp, addp, out}, voice), []int{3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := testStages(t, node, wat2wasm, tc.song); fmt.Sprint(got) != fmt.Sprint(tc.cuts) {
				t.Errorf("cuts before voices %v, want %v", got, tc.cuts)
			}
		})
	}
	t.Run("polyphony", func(t *testing.T) {
		// two instruments of three voices, the first sending to one voice of
		// the second: cuts inside the instruments too
		song := stageTestSong([]sointu.Unit{env, send(7, 1, 2), noise, mulp, sync, delay, out}, []sointu.Unit{env, sync, osc(7), mulp, delay, out})
		for i := range song.Patch {
			song.Patch[i].NumVoices = 3
			song.Score.Tracks[i].NumVoices = 3
			song.Score.Tracks[i].Patterns = []sointu.Pattern{{60, 64, 67, 1, 72, 0, 62, 65}}
		}
		if got := testStages(t, node, wat2wasm, song); fmt.Sprint(got) != "[1 2 3 4 5]" {
			t.Errorf("cuts before voices %v", got)
		}
	})
	t.Run("reverb polyphony", func(t *testing.T) {
		// an instrument of three voices, each with a reverb of its own, and
		// one after it: cuts inside the instrument too
		song := stageTestSong([]sointu.Unit{env, noise, mulp, pan, reverb(0, 20), stereoOut}, []sointu.Unit{env, osc(0), mulp, pan, reverb(0, 50), stereoOut})
		song.Patch[0].NumVoices, song.Score.Tracks[0].NumVoices = 3, 3
		song.Score.Tracks[0].Patterns = []sointu.Pattern{{60, 64, 67, 1, 72, 0, 62, 65}}
		if got := testStages(t, node, wat2wasm, song); fmt.Sprint(got) != "[1 2 3]" {
			t.Errorf("cuts before voices %v", got)
		}
	})
	t.Run("row sync", func(t *testing.T) {
		song := stageTestSong([]sointu.Unit{env, sync, osc(0), mulp, out}, noisy, []sointu.Unit{env, osc(0), sync, mulp, out})
		oneshot, _ := compileWasm(t, wat2wasm, song, nil, func(c *compiler.Compiler) { c.RowSync = true })
		layout, err := renderStages(t, node, wat2wasm, oneshot, song, func(c *compiler.Compiler) { c.Stages, c.RowSync, c.ChunkRows = 3, true, 3 })
		if err != nil || layout.Stages != 3 || layout.SyncValues != 3 {
			t.Errorf("3 stages with the row and 2 sync units: %v, %+v", err, layout)
		}
	})
	t.Run("speed", func(t *testing.T) {
		song := stageTestSong(voice, []sointu.Unit{{Type: "loadval", Parameters: p("stereo", 0, "value", 70)}, {Type: "speed", Parameters: p()}})
		com, _ := compiler.New("", "wasm", false, false)
		com.Progressive, com.Stages = true, 2
		if _, _, err := com.Song(&song); err == nil || !strings.Contains(err.Error(), "speed") {
			t.Errorf("a song with the speed unit compiled in stages: %v", err)
		}
	})
}
