package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// TestWasmMatchesGoSynth renders the regression test songs with the Go synth
// and the wasm player, which should give exactly the same audio.
func TestWasmMatchesGoSynth(t *testing.T) {
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
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".yml")
		t.Run(name, func(t *testing.T) {
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
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			got := renderWasm(t, node, wat2wasm, song, nil, nil, nil)
			// the wasm player renders the nominal length of the song, which
			// differs with the speed unit
			if len(got) != 2*len(want) && name != "test_speed" {
				t.Fatalf("wasm rendered %d frames, Go %d", len(got)/2, len(want))
			}
			for i, frame := range want[:min(len(want), len(got)/2)] {
				for c := range 2 {
					if got[2*i+c] != frame[c] {
						t.Fatalf("frame %d channel %d: wasm %v, Go %v", i, c, got[2*i+c], frame[c])
					}
				}
			}
		})
	}
}
