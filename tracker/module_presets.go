package tracker

import (
	"embed"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

// Module presets are modules saved as files in the modules directory of the
// user's sointu configuration directory, next to the instrument presets:
// each file has a module, last, and before it the modules that it uses. The
// tracker also comes with module presets of its own, the files in modules/,
// which cannot be deleted; a preset of the user with the same name is used
// instead of one of those.

//go:embed modules/*.yml
var builtinModulePresets embed.FS

type modulePreset struct {
	name    string
	file    string // empty for a preset that the tracker comes with
	modules sointu.Modules
}

// parseModulePreset reads a module preset from the contents of its file.
func parseModulePreset(fileName string, data []byte) (modulePreset, bool) {
	var file struct{ Modules sointu.Modules }
	if yaml.Unmarshal(data, &file) != nil || len(file.Modules) == 0 {
		return modulePreset{}, false
	}
	name := filenameToInstrumentName(strings.TrimSuffix(fileName, ".yml"))
	return modulePreset{name: name, modules: file.Modules}, true
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

// loadModulePresets reads the module presets: those of the user from their
// directory, in the order of their names, and after them those that the
// tracker comes with.
func (m *Model) loadModulePresets() {
	m.modulePresets = m.modulePresets[:0]
	m.userModulePresets = 0
	if dir, ok := m.modulePresetDir(); ok {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if p, ok := parseModulePreset(e.Name(), data); ok {
				p.file = filepath.Join(dir, e.Name())
				m.modulePresets = append(m.modulePresets, p)
			}
		}
		sort.Slice(m.modulePresets, func(i, j int) bool { return m.modulePresets[i].name < m.modulePresets[j].name })
		m.userModulePresets = len(m.modulePresets)
	}
	entries, _ := builtinModulePresets.ReadDir("modules") // in the order of the file names
	for _, e := range entries {
		data, err := builtinModulePresets.ReadFile("modules/" + e.Name())
		if err != nil {
			continue
		}
		p, ok := parseModulePreset(e.Name(), data)
		if !ok || slices.ContainsFunc(m.modulePresets[:m.userModulePresets], func(u modulePreset) bool { return u.name == p.name }) {
			continue
		}
		m.modulePresets = append(m.modulePresets, p)
	}
}

// SavePreset returns an Action to save the selected module as a module
// preset, with the modules that it uses. If there is a preset with the same
// name, it shows a dialog asking whether to save over it, like saving an
// instrument preset does.
func (m *ModuleModel) SavePreset() Action { return MakeAction((*saveModulePreset)(m)) }

type saveModulePreset ModuleModel

// file returns the file of the preset of the selected module, and its name.
func (m *saveModulePreset) file() (path, name string, ok bool) {
	mod := (*ModuleModel)(m).selected()
	if mod == nil {
		return "", "", false
	}
	dir, ok := (*Model)(m).modulePresetDir()
	name = instrumentNameToFilename(moduleTitle(mod))
	if !ok || name == "" {
		return "", "", false
	}
	return filepath.Join(dir, name+".yml"), filenameToInstrumentName(name), true
}
func (m *saveModulePreset) Enabled() bool { return (*ModuleModel)(m).selected() != nil }
func (m *saveModulePreset) Do() {
	path, name, ok := m.file()
	if !ok {
		(*Model)(m).Alerts().Add("The module preset could not be saved", Error)
		return
	}
	if _, err := os.Stat(path); err == nil {
		m.modulePresetAsked = name
		m.dialog = OverwriteModulePresetDialog
		return
	}
	(*ModuleModel)(m).OverwritePreset().Do()
}

// OverwritePreset returns an Action to save the selected module as a module
// preset, replacing a preset with the same name.
func (m *ModuleModel) OverwritePreset() Action { return MakeAction((*overwriteModulePreset)(m)) }

type overwriteModulePreset ModuleModel

func (m *overwriteModulePreset) Enabled() bool { return (*ModuleModel)(m).selected() != nil }
func (m *overwriteModulePreset) Do() {
	model := (*Model)(m)
	m.dialog = NoDialog
	mod := (*ModuleModel)(m).selected()
	path, name, ok := (*saveModulePreset)(m).file()
	if !ok {
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
		if err = os.MkdirAll(filepath.Dir(path), 0755); err == nil {
			err = os.WriteFile(path, data, 0644)
		}
	}
	if err != nil {
		model.Alerts().Add("The module preset could not be saved: "+err.Error(), Error)
		return
	}
	model.Alerts().Add("Module saved as the preset "+name, Info)
	model.loadModulePresets()
}

// AskedPreset returns the name of the module preset that the dialog asks
// about: the one to delete, or to save over.
func (m *ModuleModel) AskedPreset() string { return m.modulePresetAsked }

// DeletePresets returns an Int of the module presets of the user, to choose
// one to delete: setting it shows a dialog asking whether to delete the
// preset, like deleting an instrument preset does. Its value is -1: none is
// the current one.
func (m *ModuleModel) DeletePresets() Int { return MakeInt((*modulePresetDeletion)(m)) }

type modulePresetDeletion ModuleModel

func (v *modulePresetDeletion) Value() int { return -1 }
func (v *modulePresetDeletion) Range() RangeInclusive {
	return RangeInclusive{0, v.userModulePresets - 1}
}
func (v *modulePresetDeletion) StringOf(i int) string {
	if i < 0 || i >= v.userModulePresets {
		return ""
	}
	return "Delete " + v.modulePresets[i].name
}
func (v *modulePresetDeletion) SetValue(i int) bool {
	if i < 0 || i >= v.userModulePresets {
		return false
	}
	v.modulePresetAsked = v.modulePresets[i].name
	v.dialog = DeleteModulePresetDialog
	return true
}

// ConfirmDeletePreset returns an Action to delete the module preset that
// DeletePresets asked about.
func (m *ModuleModel) ConfirmDeletePreset() Action { return MakeAction((*deleteModulePreset)(m)) }

type deleteModulePreset ModuleModel

func (m *deleteModulePreset) preset() (modulePreset, bool) {
	for _, p := range m.modulePresets[:m.userModulePresets] {
		if p.name == m.modulePresetAsked {
			return p, true
		}
	}
	return modulePreset{}, false
}
func (m *deleteModulePreset) Enabled() bool {
	_, ok := m.preset()
	return ok && m.dialog == DeleteModulePresetDialog
}
func (m *deleteModulePreset) Do() {
	m.dialog = NoDialog
	p, _ := m.preset()
	if err := os.Remove(p.file); err != nil {
		(*Model)(m).Alerts().Add("The module preset could not be deleted: "+err.Error(), Error)
	}
	(*Model)(m).loadModulePresets()
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
		if m.editingModule() {
			m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
			m.leaveModuleUnits()
		}
	}
	return true
}
