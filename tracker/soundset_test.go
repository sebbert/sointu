package tracker

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// soundsetPresets returns the presets of the sound set that the tracker
// comes with, by name: the Club presets and the Global presets with the
// drum bus, Global mastering 2 buses and its ducking variant among them.
func soundsetPresets(t *testing.T) map[string]*preset {
	t.Helper()
	m, _ := newModuleTestModel(t)
	ret := map[string]*preset{}
	for i := range m.presetData.presets {
		p := &m.presetData.presets[i]
		if !p.user && (strings.HasPrefix(p.instr.Name, "Club ") || strings.HasPrefix(p.instr.Name, "Global mastering 2 drumbus") || strings.HasPrefix(p.instr.Name, "Global mastering 2 buses")) {
			ret[p.instr.Name] = p
		}
	}
	return ret
}

// The tracker lists every preset of the sound set, in the directory of its
// kind, and each is a complete instrument: it expands and encodes. The
// drums end in an out unit, which their comments tell to make an aux unit
// to route them to the drum bus, on channels 8 and 9; so routed, they still
// encode.
func TestSoundsetPresets(t *testing.T) {
	presets := soundsetPresets(t)
	want := map[string]string{
		"Club 909 kick": "DR", "Club kick hard": "DR", "Club 909 snare": "DR", "Club 909 clap": "DR", "Club 909 hat closed": "DR",
		"Club 909 hat open": "DR", "Club 909 ride": "DR", "Club 909 crash": "DR", "Club 909 tom": "DR",
		"Club sub bass": "BA", "Club acid bass": "BA", "Club reese bass": "BA", "Club dist bass": "BA",
		"Club supersaw lead": "LEAD", "Club hoover": "LEAD", "Club supersaw pad": "PAD", "Club supersaw pluck": "PL",
		"Club riser": "FX", "Club noise sweep up": "FX", "Club downlifter": "FX", "Club impact": "FX",
		"Global mastering 2 drumbus": "UTIL", "Global mastering 2 drumbus reverb": "UTIL", "Global mastering 2 buses": "UTIL",
		"Global mastering 2 buses ducking": "UTIL",
	}
	for name := range presets {
		if _, ok := want[name]; !ok {
			t.Errorf("a preset %s that the test does not know", name)
		}
	}
	for name, dir := range want {
		p, ok := presets[name]
		if !ok {
			t.Errorf("no preset %s", name)
			continue
		}
		if p.dir != dir {
			t.Errorf("the preset %s is in %s, want %s", name, p.dir, dir)
		}
		encode := func(instr sointu.Instrument) {
			t.Helper()
			if instr.NumVoices == 0 {
				instr.NumVoices = 1
			}
			song := sointu.Song{BPM: 120, RowsPerBeat: 4, Patch: sointu.Patch{instr}, Modules: p.modules.Copy()}
			song, x := song.Expand()
			if len(x.Problems) > 0 {
				t.Errorf("the preset %s: problems expanding it: %v", name, x.Problems)
			}
			if _, err := vm.NewBytecode(song.Patch, vm.AllFeatures{}, song.BPM); err != nil {
				t.Errorf("the preset %s does not encode: %v", name, err)
			}
		}
		encode(p.instr.Copy())
		last := p.instr.Units[len(p.instr.Units)-1]
		switch dir {
		case "DR":
			if last.Type != "out" || last.Parameters["stereo"] != 1 {
				t.Errorf("the preset %s ends in %s, want a stereo out", name, last.Type)
			}
			if !strings.Contains(p.instr.Comment, "aux, channel 8") {
				t.Errorf("the comment of the preset %s does not tell how to route it to the drum bus", name)
			}
			bus := p.instr.Copy()
			bus.Units[len(bus.Units)-1] = sointu.Unit{Type: "aux", ID: last.ID, Parameters: sointu.ParamMap{"stereo": 1, "gain": last.Parameters["gain"], "channel": 8}}
			encode(bus)
		case "UTIL":
			// the bus is read and sent to the mix before the mix is read
			bus, mix := -1, -1
			for i, u := range p.instr.Units {
				if u.Type == "in" && u.Parameters["channel"] == 8 {
					bus = i
				}
				if u.Type == "in" && u.Parameters["channel"] == 0 {
					mix = i
				}
			}
			if bus < 0 || mix < bus || last.Type != "out" {
				t.Errorf("the preset %s: the bus is read by unit %d, the mix by unit %d, and it ends in %s", name, bus, mix, last.Type)
			}
		}
	}
}

// The instruments of the example songs of the sound set are the presets:
// the same units with the same values, apart from the IDs, and the drums
// of the loop sent to the drum bus as the presets tell.
func TestSoundsetExamplesUseThePresets(t *testing.T) {
	presets := soundsetPresets(t)
	m, _ := newModuleTestModel(t)
	for i := range m.presetData.presets {
		if p := &m.presetData.presets[i]; !p.user && p.instr.Name == "Global reverb" {
			presets[p.instr.Name] = p
		}
	}
	for _, file := range []string{"soundset.yml", "soundset_loop.yml"} {
		data, err := os.ReadFile("../examples/" + file)
		if err != nil {
			t.Fatal(err)
		}
		var song sointu.Song
		if err := yaml.Unmarshal(data, &song); err != nil {
			t.Fatal(err)
		}
		bare := func(units []sointu.Unit, bus bool) []sointu.Unit {
			ret := make([]sointu.Unit, 0, len(units))
			for i, u := range units {
				if u.Type == "" {
					continue
				}
				c := sointu.Unit{Type: u.Type, Parameters: sointu.ParamMap{}, VarArgs: u.VarArgs}
				for k, v := range u.Parameters {
					c.Parameters[k] = v
				}
				delete(c.Parameters, "target")
				if bus && i == len(units)-1 && c.Type == "out" {
					c.Type, c.Parameters["channel"] = "aux", 8
				}
				ret = append(ret, c)
			}
			return ret
		}
		for _, instr := range song.Patch {
			p, ok := presets[instr.Name]
			if !ok {
				t.Errorf("%s: no preset %s", file, instr.Name)
				continue
			}
			bus := file == "soundset_loop.yml" && p.dir == "DR"
			if got, want := bare(instr.Units, false), bare(p.instr.Units, bus); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: the instrument %s differs from the preset:\n%v\n%v", file, instr.Name, got, want)
			}
		}
	}
}
