package tracker

import (
	"math"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// The Hardstyle kick presets are complete instruments in DR, end in a
// stereo out, and render a note at G-1 at their mastered level: peaks below
// -1 dB, loud, the same on both sides, silent after the note.
func TestHardstyleKickPresets(t *testing.T) {
	m, _ := newModuleTestModel(t)
	n := 0
	for i := range m.presetData.presets {
		p := &m.presetData.presets[i]
		if p.user || !strings.HasPrefix(p.instr.Name, "Hardstyle kick ") {
			continue
		}
		n++
		name := p.instr.Name
		if p.dir != "DR" {
			t.Errorf("%s is in %s, want DR", name, p.dir)
		}
		last := p.instr.Units[len(p.instr.Units)-1]
		if last.Type != "out" || last.Parameters["stereo"] != 1 {
			t.Errorf("%s ends in %s, want a stereo out", name, last.Type)
		}
		song := sointu.Song{BPM: 150, RowsPerBeat: 4, Patch: sointu.Patch{p.instr.Copy()}, Modules: p.modules.Copy()}
		song, x := song.Expand()
		if len(x.Problems) > 0 {
			t.Errorf("%s: problems expanding it: %v", name, x.Problems)
			continue
		}
		synth, err := vm.GoSynther{}.Synth(song.Patch, song.BPM)
		if err != nil {
			t.Errorf("%s does not compile: %v", name, err)
			continue
		}
		// G-1 held for a beat, then 1.6 s more
		synth.Trigger(0, 43)
		hit := make(sointu.AudioBuffer, 17640)
		if _, _, err := synth.Render(hit, len(hit)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		synth.Release(0)
		after := make(sointu.AudioBuffer, 70560)
		if _, _, err := synth.Render(after, len(after)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var peak, sq float64
		for _, f := range hit {
			if f[0] != f[1] {
				t.Errorf("%s is not mono", name)
				break
			}
			peak = math.Max(peak, math.Abs(float64(f[0])))
			sq += float64(f[0]) * float64(f[0])
		}
		rms := math.Sqrt(sq / float64(len(hit)))
		if peak > 0.89 || peak < 0.8 {
			t.Errorf("%s peaks at %.2f, want 0.8 to 0.89 (-1.9 to -1 dB)", name, peak)
		}
		if rms < 0.25 {
			t.Errorf("%s: RMS of the beat %.2f, want at least 0.25 (-12 dB)", name, rms)
		}
		var tail float64
		for _, f := range after[len(after)-4410:] {
			tail = math.Max(tail, math.Abs(float64(f[0])))
		}
		if tail > 1e-3 || math.IsNaN(tail) {
			t.Errorf("%s still sounds 1.6 s after the note: %g", name, tail)
		}
	}
	if n != 32 {
		t.Errorf("%d Hardstyle kick presets, want 32", n)
	}
}
