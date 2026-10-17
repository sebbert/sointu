package tracker

import (
	"math"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// The Hard techno screech presets are complete instruments in LEAD, end in
// a stereo outaux, and render a note at C-4 below -3 dB, with the side under
// the mid, no offset, and silent 3 s after the note.
func TestHardTechnoScreechPresets(t *testing.T) {
	m, _ := newModuleTestModel(t)
	n := 0
	for i := range m.presetData.presets {
		p := &m.presetData.presets[i]
		if p.user || !strings.HasPrefix(p.instr.Name, "Hard techno screech") {
			continue
		}
		n++
		name := p.instr.Name
		if p.dir != "LEAD" {
			t.Errorf("%s is in %s, want LEAD", name, p.dir)
		}
		last := p.instr.Units[len(p.instr.Units)-1]
		if last.Type != "outaux" || last.Parameters["stereo"] != 1 {
			t.Errorf("%s ends in %s, want a stereo outaux", name, last.Type)
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
		// C-4 held for a beat, then 3 s more
		synth.Trigger(0, 72)
		hit := make(sointu.AudioBuffer, 17640)
		if _, _, err := synth.Render(hit, len(hit)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		synth.Release(0)
		after := make(sointu.AudioBuffer, 3*44100)
		if _, _, err := synth.Render(after, len(after)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var peak, mid, side, sum float64
		for _, f := range hit {
			l, r := float64(f[0]), float64(f[1])
			peak = math.Max(peak, math.Max(math.Abs(l), math.Abs(r)))
			mid += (l + r) * (l + r) / 4
			side += (l - r) * (l - r) / 4
			sum += l + r
		}
		if peak > 0.71 || peak < 0.1 || math.IsNaN(peak) {
			t.Errorf("%s peaks at %.2f, want 0.1 to 0.71 (-20 to -3 dB)", name, peak)
		}
		if side > mid/2 {
			t.Errorf("%s: the side is %.1f dB under the mid, want at least 3", name, 10*math.Log10(mid/side))
		}
		if dc := sum / float64(2*len(hit)); math.Abs(dc) > 1e-3 {
			t.Errorf("%s has an offset of %g", name, dc)
		}
		var tail float64
		for _, f := range after[len(after)-4410:] {
			tail = math.Max(tail, math.Max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))))
		}
		if tail > 1e-3 || math.IsNaN(tail) {
			t.Errorf("%s still sounds 3 s after the note: %g", name, tail)
		}
	}
	if n != 11 {
		t.Errorf("%d Hard techno screech presets, want 11", n)
	}
}
