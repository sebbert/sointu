package tracker

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// The Horror presets: every one is a complete instrument in the directory
// of its kind, expands, encodes and renders a held note without NaNs and
// below full scale, the same at velocity 127, and quieter at 64; with SOINTU_TEST_LONG=1 also that it stops within 20 s
// of the note (the hall of an impact lasts 11 s). Presets that share a module share
// it exactly, so that a song with several of them gets the module once;
// every parameter of each module is bound to a unit.
func TestHorrorPresets(t *testing.T) {
	m, _ := newModuleTestModel(t)
	dirs := map[string]int{}
	modules := map[string]string{}
	n := 0
	for i := range m.presetData.presets {
		p := &m.presetData.presets[i]
		if p.user || !(strings.HasPrefix(p.instr.Name, "Horror ") || strings.HasPrefix(p.instr.Name, "Global horror space")) {
			continue
		}
		n++
		name := p.instr.Name
		dirs[p.dir]++
		for j := range p.modules {
			mod := &p.modules[j]
			key := moduleKey(mod)
			if k, ok := modules[mod.Name]; ok && k != key {
				t.Errorf("%s: its module %s differs from the module of that name in another Horror preset", name, mod.Name)
			}
			modules[mod.Name] = key
			bound := map[int]bool{}
			for _, u := range mod.Units {
				for _, b := range u.Bind {
					bound[b.Param] = true
				}
			}
			if len(bound) != len(mod.Params) {
				t.Errorf("%s: %d of the %d parameters of %s are bound", name, len(bound), len(mod.Params), mod.Name)
			}
		}
		instr := p.instr.Copy()
		instr.NumVoices = 1 // the note plays in the first voice; the others would only cost time
		song := sointu.Song{BPM: 120, RowsPerBeat: 4, Patch: sointu.Patch{instr}, Modules: p.modules.Copy()}
		song, x := song.Expand()
		if len(x.Problems) > 0 {
			t.Errorf("%s: problems expanding it: %v", name, x.Problems)
			continue
		}
		if len(song.Patch[0].Units) > 63 {
			t.Errorf("%s: %d units", name, len(song.Patch[0].Units))
		}
		synth, err := vm.GoSynther{}.Synth(song.Patch, song.BPM)
		if err != nil {
			t.Errorf("%s does not compile: %v", name, err)
			continue
		}
		if p.dir == "UTIL" {
			continue // a bus: nothing to play
		}
		// C-2, held 2 s, then the release
		long := os.Getenv("SOINTU_TEST_LONG") == "1"
		tailSeconds := 2
		if long {
			tailSeconds = 20
		}
		synth.Trigger(0, 48)
		held := make(sointu.AudioBuffer, 2*44100)
		if _, _, err := synth.Render(held, len(held)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		synth.Release(0)
		after := make(sointu.AudioBuffer, tailSeconds*44100)
		if _, _, err := synth.Render(after, len(after)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var peak float64
		for _, f := range append(held, after...) {
			for _, v := range f {
				if math.IsNaN(float64(v)) {
					t.Fatalf("%s renders NaN", name)
				}
				peak = math.Max(peak, math.Abs(float64(v)))
			}
		}
		if peak >= 1 || peak < 0.001 { // the long risers are still quiet after 2 s
			t.Errorf("%s peaks at %.3f, want between -60 and 0 dB", name, peak)
		}
		// velocity 127 is the note as calibrated, velocity 64 about 6 dB
		// quieter
		peakAt := func(velocity byte) float64 {
			synth, _ := vm.GoSynther{}.Synth(song.Patch, song.BPM)
			synth.(sointu.VelocityTriggerer).TriggerVelocity(0, 48, velocity)
			buf := make(sointu.AudioBuffer, 2*44100)
			synth.Render(buf, len(buf))
			var p float64
			for _, f := range buf {
				p = math.Max(p, math.Max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))))
			}
			return p
		}
		if full := peakAt(127); full != func() float64 {
			var p float64
			for _, f := range held {
				p = math.Max(p, math.Max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))))
			}
			return p
		}() {
			t.Errorf("%s: velocity 127 is not the note without velocity", name)
		} else if db := 20 * math.Log10(peakAt(64)/full); db > -4 || db < -12 {
			t.Errorf("%s: velocity 64 is %.1f dB from 127, want -4 to -12", name, db)
		}
		if !long {
			continue
		}
		var tail float64
		for _, f := range after[len(after)-4410:] {
			tail = math.Max(tail, math.Max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))))
		}
		if tail > 1e-3 {
			t.Errorf("%s still sounds 20 s after the note: %g", name, tail)
		}
	}
	want := map[string]int{"PAD": 13, "ST": 16, "FX": 38, "KEYS": 9, "DR": 4, "UTIL": 2}
	for d, k := range want {
		if dirs[d] != k {
			t.Errorf("%d Horror presets in %s, want %d", dirs[d], d, k)
		}
	}
	if n != 82 {
		t.Errorf("%d Horror presets, want 82", n)
	}
}
