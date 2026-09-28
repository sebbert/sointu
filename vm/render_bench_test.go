package vm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// BenchmarkRender renders 2 seconds of the example songs with all their
// voices triggered.
func BenchmarkRender(b *testing.B) {
	files, _ := filepath.Glob(filepath.Join("..", "examples", "patches", "*.yml"))
	var songs []sointu.Song
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		var song sointu.Song
		if err := yaml.Unmarshal(data, &song); err != nil {
			b.Fatal(err)
		}
		songs = append(songs, song)
	}
	out := make(sointu.AudioBuffer, 2*44100)
	b.ResetTimer()
	for range b.N {
		for _, song := range songs {
			synth, err := vm.GoSynther{}.Synth(song.Patch, song.BPM)
			if err != nil {
				b.Fatal(err)
			}
			for v := 0; v < song.Patch.NumVoices(); v++ {
				synth.Trigger(v, 60+byte(v%12))
			}
			if _, _, err := synth.Render(out, len(out)); err != nil {
				b.Fatal(err)
			}
		}
	}
}
