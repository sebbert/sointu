package tracker

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// newPresetWatchModel returns a model whose presets of the user are in a
// folder of its own, watched every 10 ms, and that folder.
func newPresetWatchModel(t *testing.T) (*Model, *Broker, string) {
	t.Helper()
	old := presetWatchInterval
	presetWatchInterval = 10 * time.Millisecond
	t.Cleanup(func() { presetWatchInterval = old })
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	drainPlayer(broker)
	m.stopWatchingPresets() // of the user's own folders
	root := t.TempDir()
	m.presetData.userRoot = root
	m.modulePresetPath = filepath.Join(root, "modules")
	m.encodingPresetPath = filepath.Join(root, "encoding-presets.yml")
	m.watchPresets()
	m.Preset().reload()
	m.loadModulePresets()
	return m, broker, root
}

// waitFor passes the messages to the model until cond holds.
func waitFor(t *testing.T, m *Model, broker *Broker, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case msg := <-broker.ToModel:
			m.ProcessMsg(msg)
		case <-broker.ToPlayer:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// writeFile writes a file, making its folder, with a time of change of its
// own, so that a change is seen however coarse the clock of the file system.
func writeFile(t *testing.T, path string, data []byte, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	tm := time.Date(2020, 1, 1, 0, 0, n, 0, time.UTC)
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
}

func instrumentPresetFile(t *testing.T, comment string) []byte {
	t.Helper()
	instr := defaultInstrument.Copy()
	instr.Comment = comment
	data, err := yaml.Marshal(&instrumentFile{Instrument: instr})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func userPreset(m *Model, name string) (preset, bool) {
	i := slices.IndexFunc(m.presetData.presets, func(p preset) bool { return p.user && p.instr.Name == name })
	if i < 0 {
		return preset{}, false
	}
	return m.presetData.presets[i], true
}

func selectedPresetName(m *Model) string {
	name, _, _ := m.Preset().SearchResult(m.Preset().SearchResultList().Selected())
	return name
}

func TestPresetWatchAddsChangesAndDeletesInstrumentPresets(t *testing.T) {
	m, broker, root := newPresetWatchModel(t)
	path := filepath.Join(root, "presets", "MINE", "Watched_lead.yml")
	writeFile(t, path, instrumentPresetFile(t, "first"), 1)
	waitFor(t, m, broker, "the added preset", func() bool { _, ok := userPreset(m, "Watched lead"); return ok })
	if p, _ := userPreset(m, "Watched lead"); p.dir != "MINE" || p.instr.Comment != "first" {
		t.Errorf("got dir %q comment %q, want MINE and first", p.dir, p.instr.Comment)
	}
	if !slices.Contains(m.presetData.dirs, "MINE") {
		t.Errorf("the folder MINE is not in the folders: %v", m.presetData.dirs)
	}
	writeFile(t, path, instrumentPresetFile(t, "second"), 2)
	waitFor(t, m, broker, "the changed preset", func() bool { p, _ := userPreset(m, "Watched lead"); return p.instr.Comment == "second" })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, broker, "the deleted preset", func() bool { _, ok := userPreset(m, "Watched lead"); return !ok })
}

func TestPresetWatchKeepsTheSearchAndTheSelection(t *testing.T) {
	m, broker, root := newPresetWatchModel(t)
	dir := filepath.Join(root, "presets", "MINE")
	writeFile(t, filepath.Join(dir, "Watch_b.yml"), instrumentPresetFile(t, ""), 1)
	writeFile(t, filepath.Join(dir, "Watch_c.yml"), instrumentPresetFile(t, ""), 1)
	waitFor(t, m, broker, "the presets", func() bool { _, ok := userPreset(m, "Watch c"); return ok })
	m.Preset().SearchTerm().SetValue("t:u watch")
	results := m.Preset().SearchResultList()
	results.SetSelected(1)
	if got := selectedPresetName(m); got != "Watch c" {
		t.Fatalf("selected %q, want Watch c", got)
	}
	// a preset sorted before the selected one
	writeFile(t, filepath.Join(dir, "Watch_a.yml"), instrumentPresetFile(t, ""), 2)
	waitFor(t, m, broker, "the added preset", func() bool { return results.Count() == 3 })
	if got := selectedPresetName(m); got != "Watch c" {
		t.Errorf("after an add, selected %q, want Watch c", got)
	}
	if got := m.Preset().SearchTerm().Value(); got != "t:u watch" {
		t.Errorf("search %q, want t:u watch", got)
	}
	if !m.Preset().UserFilter().Value() {
		t.Error("the filter of the user's presets is off")
	}
	// the selected one deleted: the selection stays at its index
	if err := os.Remove(filepath.Join(dir, "Watch_c.yml")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, broker, "the deleted preset", func() bool { return results.Count() == 2 })
	if got := selectedPresetName(m); got != "Watch b" {
		t.Errorf("after deleting the selected preset, selected %q, want Watch b", got)
	}
}

func TestPresetWatchModulePresets(t *testing.T) {
	m, broker, root := newPresetWatchModel(t)
	data, err := builtinModulePresets.ReadFile("modules/Ducker.yml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "modules", "Watched_module.yml")
	writeFile(t, path, data, 1)
	names := func() []string {
		var ret []string
		for _, p := range m.modulePresets[:m.userModulePresets] {
			ret = append(ret, p.name)
		}
		return ret
	}
	waitFor(t, m, broker, "the added module preset", func() bool { return slices.Equal(names(), []string{"Watched module"}) })
	if c := m.Module().Presets(); c.StringOf(0) != "Watched module" {
		t.Errorf("the menu has %q first, want Watched module", c.StringOf(0))
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, broker, "the deleted module preset", func() bool { return len(names()) == 0 })
}

func TestPresetWatchEncodingPresets(t *testing.T) {
	m, broker, root := newPresetWatchModel(t)
	path := filepath.Join(root, "encoding-presets.yml")
	has := func(name string) bool {
		return slices.ContainsFunc(m.defaultPresets, func(p sointu.EncodingPreset) bool { return p.Name == name })
	}
	writeFile(t, path, []byte("- name: Watched\n  format: wav\n"), 1)
	waitFor(t, m, broker, "the added encoding preset", func() bool { return has("Watched") })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, broker, "the deleted encoding preset", func() bool { return !has("Watched") })
}

func TestStampTree(t *testing.T) {
	root := t.TempDir()
	if stampTree(filepath.Join(root, "missing")) != 0 || stampTree("") != 0 {
		t.Error("a missing path does not stamp to 0")
	}
	empty := stampTree(root)
	if empty == 0 {
		t.Error("an empty folder stamps to 0")
	}
	writeFile(t, filepath.Join(root, "a", "b.yml"), []byte("x"), 1)
	added := stampTree(root)
	writeFile(t, filepath.Join(root, "a", "b.yml"), []byte("x"), 2)
	touched := stampTree(root)
	writeFile(t, filepath.Join(root, "a", "b.yml"), []byte("xy"), 2)
	grown := stampTree(root)
	if err := os.Rename(filepath.Join(root, "a", "b.yml"), filepath.Join(root, "a", "c.yml")); err != nil {
		t.Fatal(err)
	}
	renamed := stampTree(root)
	all := []uint64{empty, added, touched, grown, renamed}
	for i := range all {
		for j := range i {
			if all[i] == all[j] {
				t.Errorf("stamps %d and %d are the same", j, i)
			}
		}
	}
	if stampTree(root) != renamed {
		t.Error("the stamp changes with nothing changed")
	}
}
