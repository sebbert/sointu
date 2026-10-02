package compiler_test

import (
	"encoding/binary"
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

// syncWasmRunner writes the sync values of a wasm player.
// Usage: node runner.js player.wasm sync.raw
const syncWasmRunner = `
const fs = require('fs');
const [wasmFile, outFile] = process.argv.slice(2);
(async () => {
  const { instance: { exports } } = await WebAssembly.instantiate(fs.readFileSync(wasmFile), { m: Math });
  fs.writeFileSync(outFile, Buffer.from(exports.m.buffer, exports.y.value, exports.z.value));
})().catch(e => { console.error(e); process.exit(1); });
`

func floats(raw []byte) []float32 {
	ret := make([]float32, len(raw)/4)
	for i := range ret {
		ret[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return ret
}

// TestSyncWasmMatchesGoSynth checks the sync values of the wasm player: the
// signals at the sync units, every 256th sample, are exactly those the Go
// synth records, and with RowSync the row comes first. They also agree with
// the sync buffer of the x86 player in tests/expected_output, which rounds
// differently. A player of a song without sync units has none of it.
func TestSyncWasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	data, err := os.ReadFile("../../tests/test_sync.yml")
	if err != nil {
		t.Fatal(err)
	}
	var song sointu.Song
	if err := yaml.Unmarshal(data, &song); err != nil {
		t.Fatal(err)
	}
	var want []float32
	if _, err := sointu.Play(vm.GoSynther{Syncs: &want}, song, nil); err != nil {
		t.Fatal(err)
	}
	ticks := (song.Score.LengthInRows()*song.SamplesPerRow() + 255) >> 8
	if len(want) != 2*ticks {
		t.Fatalf("the Go synth recorded %d sync values, want %d", len(want), 2*ticks)
	}
	x86 := floats(func() []byte { b, _ := os.ReadFile("../../tests/expected_output/test_sync_syncbuf.raw"); return b }())
	for _, rowSync := range []bool{false, true} {
		name := map[bool]string{false: "units", true: "units and row"}[rowSync]
		t.Run(name, func(t *testing.T) {
			wasmFile, files := compileWasm(t, wat2wasm, song, nil, func(c *compiler.Compiler) { c.RowSync = rowSync })
			if !strings.Contains(files[".wat"], "$su_op_sync") || !strings.Contains(files[".wat"], `(export "y")`) {
				t.Fatalf("the player has no sync unit")
			}
			dir := t.TempDir()
			runner, outFile := filepath.Join(dir, "runner.js"), filepath.Join(dir, "sync.raw")
			os.WriteFile(runner, []byte(syncWasmRunner), 0o644)
			if out, err := exec.Command(node, runner, wasmFile, outFile).CombinedOutput(); err != nil {
				t.Fatalf("running the wasm player failed: %v\n%s", err, out)
			}
			raw, _ := os.ReadFile(outFile)
			got := floats(raw)
			n := 2
			if rowSync {
				n = 3
			}
			if len(got) != n*ticks {
				t.Fatalf("the wasm player has %d sync values, want %d", len(got), n*ticks)
			}
			peak := float32(0)
			for tick := 0; tick < ticks; tick++ {
				values := got[tick*n : tick*n+n]
				if rowSync {
					sample := tick * 256
					row := float32(sample%song.SamplesPerRow())/float32(song.SamplesPerRow()) + float32(sample/song.SamplesPerRow())
					if values[0] != row {
						t.Fatalf("tick %d: the row is %v, want %v", tick, values[0], row)
					}
					for i, v := range values {
						if x := x86[tick*3+i]; math.Abs(float64(v-x)) > 1e-4 {
							t.Fatalf("tick %d value %d: wasm %v, x86 %v", tick, i, v, x)
						}
					}
					values = values[1:]
				}
				for i, v := range values {
					if v != want[tick*2+i] {
						t.Fatalf("tick %d sync %d: wasm %v, Go %v", tick, i, v, want[tick*2+i])
					}
					peak = max(peak, v)
				}
			}
			if peak < 0.5 {
				t.Errorf("the sync values peak at %v: the test is not testing anything", peak)
			}
		})
	}
	t.Run("no sync units", func(t *testing.T) {
		song := song.Copy()
		song.Patch[0].Units[1].Disabled, song.Patch[0].Units[3].Disabled = true, true
		com, _ := compiler.New("", "wasm", false, false)
		files, _, err := com.Song(&song)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"su_op_sync", "syncBufPtr", `(export "y")`} {
			if strings.Contains(files[".wat"], s) {
				t.Errorf("the player of a song without sync units has %q", s)
			}
		}
	})
}
