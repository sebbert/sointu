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
	return newPresetWatchModelWithSource(t, "")
}

// newPresetWatchModelWithSource is newPresetWatchModel with the presets that
// the tracker comes with read from source.
func newPresetWatchModelWithSource(t *testing.T, source string) (*Model, *Broker, string) {
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
	m.presetData.source = source
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

func builtinPreset(m *Model, name string) (preset, bool) {
	i := slices.IndexFunc(m.presetData.presets, func(p preset) bool { return !p.user && p.instr.Name == name })
	if i < 0 {
		return preset{}, false
	}
	return m.presetData.presets[i], true
}

func TestPresetWatchBuiltinPresetsFromSource(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "presets", "BA", "Source_bass.yml"), instrumentPresetFile(t, "first"), 1)
	ducker, err := builtinModulePresets.ReadFile("modules/Ducker.yml")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "modules", "Source_module.yml"), ducker, 1)
	m, broker, root := newPresetWatchModelWithSource(t, source)
	// read from the source, not the binary
	if _, ok := builtinPreset(m, "Source bass"); !ok || len(m.presetData.presets) != 1 {
		t.Fatalf("got %d presets, want Source bass alone", len(m.presetData.presets))
	}
	if len(m.modulePresets) != 1 || m.modulePresets[0].name != "Source module" || m.modulePresets[0].file != "" {
		t.Fatalf("module presets %v, want Source module, built-in", m.modulePresets)
	}
	// added and changed in the source
	writeFile(t, filepath.Join(source, "presets", "BA", "Source_bass.yml"), instrumentPresetFile(t, "second"), 2)
	writeFile(t, filepath.Join(source, "presets", "LEAD", "Source_lead.yml"), instrumentPresetFile(t, ""), 2)
	waitFor(t, m, broker, "the presets of the source", func() bool {
		p, _ := builtinPreset(m, "Source bass")
		_, ok := builtinPreset(m, "Source lead")
		return ok && p.instr.Comment == "second"
	})
	writeFile(t, filepath.Join(source, "modules", "Another_module.yml"), ducker, 2)
	waitFor(t, m, broker, "the module preset of the source", func() bool { return len(m.modulePresets) == 2 })
	// a preset of the user is listed before the built-in ones
	writeFile(t, filepath.Join(root, "modules", "User_module.yml"), ducker, 3)
	waitFor(t, m, broker, "the module preset of the user", func() bool { return m.userModulePresets == 1 })
	if m.modulePresets[0].name != "User module" || m.modulePresets[1].name != "Another module" {
		t.Errorf("module presets %v, want User module, then those of the source", m.modulePresets)
	}
	// built-in presets cannot be deleted
	m.Preset().SearchTerm().SetValue("source bass")
	if m.Preset().Delete().Enabled() {
		t.Error("a built-in preset from the source can be deleted")
	}
}

func TestPresetSourceFallsBackToTheBinary(t *testing.T) {
	embedded := presetData{source: ""}
	embedded.load()
	missing := presetData{source: filepath.Join(t.TempDir(), "gone")}
	missing.load()
	if len(missing.presets) == 0 || len(missing.presets) != len(embedded.presets) {
		t.Errorf("with the source missing, %d presets, want the %d embedded", len(missing.presets), len(embedded.presets))
	}
	mods, _ := readModulePresets("", false, filepath.Join(t.TempDir(), "gone"))
	embeddedMods, _ := readModulePresets("", false, "")
	if len(mods) == 0 || len(mods) != len(embeddedMods) {
		t.Errorf("with the source missing, %d module presets, want the %d embedded", len(mods), len(embeddedMods))
	}
}

func TestPresetSource(t *testing.T) {
	t.Setenv(PresetSourceEnv, "off")
	if s := presetSource(); s != "" {
		t.Errorf("off: got %q", s)
	}
	t.Setenv(PresetSourceEnv, "/some/where")
	if s := presetSource(); s != "/some/where" {
		t.Errorf("a path: got %q", s)
	}
	os.Unsetenv(PresetSourceEnv)
	s := presetSource()
	if info, err := os.Stat(filepath.Join(s, "presets")); err != nil || !info.IsDir() {
		t.Errorf("unset: got %q, want the tracker folder of the source tree", s)
	}
	// the source tree reads as the binary does
	src := presetData{source: s}
	src.load()
	embedded := presetData{}
	embedded.load()
	if len(src.presets) != len(embedded.presets) {
		t.Errorf("%d presets from the source tree, %d embedded", len(src.presets), len(embedded.presets))
	}
}
