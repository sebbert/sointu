package tracker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

// Module presets are modules saved as files in the modules directory of the
// user's sointu configuration directory, next to the instrument presets:
// each file has a module, last, and before it the modules that it uses.

type modulePreset struct {
	name    string
	modules sointu.Modules
}

// modulePresetDir returns the directory of the module presets.
func (m *Model) modulePresetDir() (string, bool) {
	if m.modulePresetPath != "" {
		return m.modulePresetPath, true
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(configDir, "sointu", "modules"), true
}

// loadModulePresets reads the module presets from their directory.
func (m *Model) loadModulePresets() {
	m.modulePresets = m.modulePresets[:0]
	dir, ok := m.modulePresetDir()
	if !ok {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var file struct{ Modules sointu.Modules }
		if yaml.Unmarshal(data, &file) != nil || len(file.Modules) == 0 {
			continue
		}
		name := filenameToInstrumentName(strings.TrimSuffix(e.Name(), ".yml"))
		m.modulePresets = append(m.modulePresets, modulePreset{name: name, modules: file.Modules})
	}
	sort.Slice(m.modulePresets, func(i, j int) bool { return m.modulePresets[i].name < m.modulePresets[j].name })
}

// SavePreset returns an Action to save the selected module as a module
// preset, with the modules that it uses. A preset with the same name is
// replaced.
func (m *ModuleModel) SavePreset() Action { return MakeAction((*saveModulePreset)(m)) }

type saveModulePreset ModuleModel

func (m *saveModulePreset) Enabled() bool { return (*ModuleModel)(m).selected() != nil }
func (m *saveModulePreset) Do() {
	model := (*Model)(m)
	mod := (*ModuleModel)(m).selected()
	dir, ok := model.modulePresetDir()
	name := instrumentNameToFilename(moduleTitle(mod))
	if !ok || name == "" {
		model.Alerts().Add("The module preset could not be saved", Error)
		return
	}
	mods := append(model.modulesUsedBy(mod.Units), mod.Copy())
	for i := range mods {
		for j := range mods[i].Units {
			mods[i].Units[j].Unfolded = false
		}
	}
	data, err := yaml.Marshal(struct{ Modules sointu.Modules }{mods})
	if err == nil {
		if err = os.MkdirAll(dir, 0755); err == nil {
			err = os.WriteFile(filepath.Join(dir, name+".yml"), data, 0644)
		}
	}
	if err != nil {
		model.Alerts().Add("The module preset could not be saved: "+err.Error(), Error)
		return
	}
	model.Alerts().Add("Module saved as the preset "+filenameToInstrumentName(name), Info)
	model.loadModulePresets()
}

// Presets returns an Int of the module presets, to choose one from: setting
// it adds the module of the preset to the song, with the modules that it
// uses, and selects it. A module that the song already has, with the same
// name and content, is not added again. Its value is -1: none is the
// current one.
func (m *ModuleModel) Presets() Int { return MakeInt((*modulePresetChoice)(m)) }

type modulePresetChoice ModuleModel

func (v *modulePresetChoice) Value() int { return -1 }
func (v *modulePresetChoice) Range() RangeInclusive {
	return RangeInclusive{0, len(v.modulePresets) - 1}
}
func (v *modulePresetChoice) StringOf(i int) string {
	if i < 0 || i >= len(v.modulePresets) {
		return ""
	}
	return v.modulePresets[i].name
}
func (v *modulePresetChoice) SetValue(i int) bool {
	if i < 0 || i >= len(v.modulePresets) {
		return false
	}
	m := (*Model)(v)
	defer m.change("LoadModulePreset", PatchChange, MajorChange)()
	mods := v.modulePresets[i].modules
	ids := m.importModules(mods)
	if index, ok := m.d.Song.Modules.Find(ids[mods[len(mods)-1].ID]); ok {
		m.d.ModuleIndex = index
		m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
	}
	return true
}
