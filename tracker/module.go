package tracker

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

// Module returns the Module view of the model, containing methods to
// manipulate the modules: reusable blocks of units, which the module units of
// the instruments stand for. On the Modules tab, the unit editor (Model.Unit
// and Model.Params) edits the units of the selected module instead of those
// of the selected instrument.
func (m *Model) Module() *ModuleModel { return (*ModuleModel)(m) }

type ModuleModel Model

// editingModule reports whether the unit editor shows the units of a module:
// on the Modules tab.
func (m *Model) editingModule() bool { return m.d.InstrumentTab == InstrumentModulesTab }

// unitsPtr returns the units being edited: those of the selected module on
// the Modules tab, otherwise those of the selected instrument, or with the
// cursor on an inner unit of a module unit, those of its module (see
// rows.go). It returns nil if there is no such module or instrument.
func (m *Model) unitsPtr() *[]sointu.Unit {
	list, _, _ := m.scope()
	return list
}

func (m *Model) units() []sointu.Unit {
	if p := m.unitsPtr(); p != nil {
		return *p
	}
	return nil
}

// selectedUnit returns the selected unit of the units being edited, or nil.
func (m *Model) selectedUnit() *sointu.Unit {
	if units := m.units(); m.d.UnitIndex >= 0 && m.d.UnitIndex < len(units) {
		return &units[m.d.UnitIndex]
	}
	return nil
}

func moduleTitle(mod *sointu.Module) string {
	if mod.Name != "" {
		return mod.Name
	}
	return "Module " + strconv.Itoa(mod.ID)
}

// unitTitle returns what the unit is shown as: its type, or for a module
// unit, the name of its module.
func (m *Model) unitTitle(u *sointu.Unit) string {
	if u.Type == "module" {
		if i, ok := m.d.Song.Modules.Find(u.Parameters["module"]); ok {
			return moduleTitle(&m.d.Song.Modules[i])
		}
	}
	return u.Type
}

// playerSong returns a copy of the song for the player: with the module
// units replaced by the units of their modules, as the synths need it. It
// warns about what could not be expanded.
func (m *Model) playerSong() sointu.Song {
	song, expansion := m.d.Song.Expand()
	m.expansion, m.expanded = expansion, song.Patch
	if len(expansion.Problems) > 0 {
		m.Alerts().AddNamed("Modules", "Modules: "+expansion.Problems[0].Error(), Error)
	} else {
		m.Alerts().ClearNamed("Modules")
	}
	return song.Copy()
}

// runPatch returns the patch as the synth runs it, with the module units
// replaced by the units of their modules, for checking it.
func (m *Model) runPatch() sointu.Patch {
	if !m.d.Song.HasModules() {
		return m.d.Song.Patch
	}
	song, _ := m.d.Song.Expand()
	return song.Patch
}

// fixModules keeps the modules valid, after every change of the patch: their
// IDs are unique, they have at most MaxModuleParams parameters, and only
// their units have bindings, to parameters that they have and that can be
// bound.
func (m *Model) fixModules() {
	used := map[int]bool{}
	next := 1
	for _, mod := range m.d.Song.Modules {
		next = max(next, mod.ID+1)
	}
	for i := range m.d.Song.Modules {
		mod := &m.d.Song.Modules[i]
		if mod.ID <= 0 || used[mod.ID] {
			mod.ID = next
			next++
		}
		used[mod.ID] = true
		mod.Inputs = max(mod.Inputs, 0)
		if len(mod.Params) > sointu.MaxModuleParams {
			mod.Params = mod.Params[:sointu.MaxModuleParams]
		}
		for j := range mod.Units {
			u := &mod.Units[j]
			for name, b := range u.Bind {
				p, ok := sointu.BindableParam(u.Type, name)
				if b.Param < 1 || b.Param > len(mod.Params) || !sointu.CanBind(u.Type, name) || !ok {
					delete(u.Bind, name)
				} else if b.Scaled && u.Type != "module" && p.MaxValue >= p.MinValue {
					b.Min, b.Max = min(max(b.Min, p.MinValue), p.MaxValue), min(max(b.Max, p.MinValue), p.MaxValue)
					u.Bind[name] = b
				}
			}
			if len(u.Bind) == 0 {
				u.Bind = nil
			}
		}
		// a bound parameter shows the default of the parameter of the
		// module, e.g. in a song written by hand
		for k := range mod.Params {
			if p, ok := m.d.Song.Modules.Param(i, k+1); ok {
				m.setModuleDefault(i, k+1, p.Default)
			}
		}
	}
	for i := range m.d.Song.Patch {
		for j := range m.d.Song.Patch[i].Units {
			m.d.Song.Patch[i].Units[j].Bind = nil
		}
	}
}

// wouldUseItself reports whether a unit of the units being edited using the
// module with the given ID would make a module use itself.
func (m *Model) wouldUseItself(id int) bool {
	module, ok := m.scopeModule()
	return ok && m.usesModule(id, module)
}

// usesModule reports whether the module with the given ID is the module
// with the given index, or uses it.
func (m *Model) usesModule(id, module int) bool {
	edited := m.d.Song.Modules[module].ID
	return id == edited || m.d.Song.Modules.Uses(id, edited)
}

// newModuleUnit returns a module unit for a module that the units being
// edited can use: the selected module, or else the first one, or none.
func (m *Model) newModuleUnit() sointu.Unit {
	mods := m.d.Song.Modules
	if i := m.d.ModuleIndex; i >= 0 && i < len(mods) && !m.wouldUseItself(mods[i].ID) {
		return mods.MakeModuleUnit(i)
	}
	for i := range mods {
		if !m.wouldUseItself(mods[i].ID) {
			return mods.MakeModuleUnit(i)
		}
	}
	return sointu.MakeUnit("module")
}

// moduleOf returns the index of the module that the unit is a unit of.
func (m *Model) moduleOf(unit *sointu.Unit) (int, bool) {
	for i := range m.d.Song.Modules {
		units := m.d.Song.Modules[i].Units
		for j := range units {
			if &units[j] == unit {
				return i, true
			}
		}
	}
	return 0, false
}

// syncBoundDefault makes the value of a bound parameter of a unit of a
// module the default of the parameter of the module that it is bound to.
func (m *Model) syncBoundDefault(unit *sointu.Unit, name string) {
	if i, ok := m.moduleOf(unit); ok {
		if b, ok := unit.Bind[name]; ok {
			// with a scaled binding, the default that gives the value
			m.setModuleDefault(i, b.Param, b.Unmap(unit.BoundValue(name)))
		}
	}
}

// setModuleDefault sets the default of parameter k (from 1) of a module.
// The parameters bound to it show the default, so they are set too.
func (m *Model) setModuleDefault(index, k, value int) {
	mod := &m.d.Song.Modules[index]
	if k < 1 || k > len(mod.Params) {
		return
	}
	mod.Params[k-1].Default = value
	for j := range mod.Units {
		u := &mod.Units[j]
		for _, p := range u.BindableParams() {
			b, ok := u.Bind[p.Name]
			if !ok || b.Param != k || !p.CanSet {
				continue
			}
			if u.Type == "module" {
				u.Parameters[p.Name] = b.Map(value)
			} else {
				u.SetBoundValue(p.Name, min(max(b.Map(value), p.MinValue), max(p.MaxValue, p.MinValue)))
			}
		}
	}
}

// moduleUses returns the number of enabled module units using the module
// with the given ID.
func (m *Model) moduleUses(id int) int {
	n := 0
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if u.Type == "module" && !u.Disabled && u.Parameters["module"] == id {
				n++
			}
		}
	}
	return n
}

func (m *Model) newModuleID() int {
	id := 1
	for _, mod := range m.d.Song.Modules {
		id = max(id, mod.ID+1)
	}
	return id
}

// newModuleName returns the name with a number added, if needed, so that no
// module has it.
func (m *Model) newModuleName(name string) string {
	taken := func(n string) bool {
		for _, mod := range m.d.Song.Modules {
			if mod.Name == n {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s %d", name, i); !taken(n) {
			return n
		}
	}
}

// addModule adds a new module after the selected one, selects it and returns
// its index.
func (m *Model) addModule() int {
	mod := sointu.Module{ID: m.newModuleID(), Name: m.newModuleName("Module"), Units: []sointu.Unit{}}
	i := min(max(m.d.ModuleIndex+1, 0), len(m.d.Song.Modules))
	m.d.Song.Modules, _ = Insert(m.d.Song.Modules, i, mod)
	m.d.ModuleIndex = i
	m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
	if m.editingModule() {
		m.leaveModuleUnits()
	}
	return i
}

// Modules in files and on the clipboard

type (
	// unitClipboard is units on the clipboard, with the modules they use
	unitClipboard struct {
		Units   []sointu.Unit
		Modules sointu.Modules `yaml:",omitempty"`
	}

	// instrumentFile is an instrument in a file or a preset, with the
	// modules that its units use
	instrumentFile struct {
		sointu.Instrument `yaml:",inline"`
		Modules           sointu.Modules `yaml:",omitempty" json:",omitempty"`
	}
)

// withoutBindings returns copies of the units without their bindings to the
// parameters of a module.
func withoutBindings(units []sointu.Unit) []sointu.Unit {
	ret := make([]sointu.Unit, len(units))
	for i := range units {
		ret[i] = units[i].Copy()
		ret[i].Bind = nil
	}
	return ret
}

// modulesUsedBy returns copies of the modules that the units use, directly
// or through other modules.
func (m *Model) modulesUsedBy(unitLists ...[]sointu.Unit) sointu.Modules {
	var ret sointu.Modules
	seen := map[int]bool{}
	var visit func(units []sointu.Unit)
	visit = func(units []sointu.Unit) {
		for _, u := range units {
			if u.Type != "module" {
				continue
			}
			i, ok := m.d.Song.Modules.Find(u.Parameters["module"])
			if !ok || seen[m.d.Song.Modules[i].ID] {
				continue
			}
			seen[m.d.Song.Modules[i].ID] = true
			visit(m.d.Song.Modules[i].Units) // the modules it uses come first
			ret = append(ret, m.d.Song.Modules[i].Copy())
		}
	}
	for _, units := range unitLists {
		visit(units)
	}
	return ret
}

// moduleKey returns a text that two modules share if they are the same
// apart from their IDs, the IDs of their units and the IDs of their buffers.
func moduleKey(mod *sointu.Module) string {
	c := mod.Copy()
	c.ID = 0
	for i := range c.Units {
		c.Units[i].Unfolded = false // a hint to the tracker, not a difference
	}
	ids := map[int]int{}
	for i := range c.Units {
		if id := c.Units[i].ID; id != 0 {
			ids[id] = i + 1
			c.Units[i].ID = i + 1
		}
	}
	buffers := map[int]int{}
	for i := range c.Units {
		u := &c.Units[i]
		if u.Type == "send" {
			u.Parameters["target"] = ids[u.Parameters["target"]] // 0 if outside the module
		}
		if u.Type != "module" {
			// a parameter that a unit does not have is 0: reading a unit
			// from a file or the clipboard adds some of them
			for name, value := range u.Parameters {
				if value == 0 {
					delete(u.Parameters, name)
				}
			}
		}
		for _, name := range unitBufferParams(u.Type) {
			id := u.Parameters[name]
			if _, ok := buffers[id]; !ok && id != 0 {
				buffers[id] = len(buffers) + 1
			}
			u.Parameters[name] = buffers[id]
		}
	}
	key, _ := yaml.Marshal(c)
	return string(key)
}

// unitBufferParams returns the names of the parameters of a unit type that
// are the IDs of spectrum buffers or buses, which the tracker creates for
// the units.
func unitBufferParams(unitType string) []string {
	if p := sointu.SpectrumBufferParams(unitType); p != nil {
		return p
	}
	return sointu.BusParams(unitType)
}

// importModules adds the modules that came with units from a file, a preset
// or the clipboard to the song, and makes the module units among the units
// use them. A module that the song already has, with the same name and the
// same content, is not added again: the units use the one of the song.
// Another module with a name that is taken gets a number added to its name.
// It returns the IDs that the modules have in the song, by their IDs.
func (m *Model) importModules(mods sointu.Modules, unitLists ...[]sointu.Unit) (newIDs map[int]int) {
	newIDs = map[int]int{}
	if len(mods) == 0 {
		return newIDs
	}
	mods = mods.Copy()
	imported := map[int]bool{}
	for _, mod := range mods {
		imported[mod.ID] = true
	}
	remap := func(units []sointu.Unit) (ready bool) {
		for _, u := range units {
			if _, ok := newIDs[u.Parameters["module"]]; u.Type == "module" && imported[u.Parameters["module"]] && !ok {
				return false // uses a module not added yet
			}
		}
		for i := range units {
			if id, ok := newIDs[units[i].Parameters["module"]]; ok && units[i].Type == "module" {
				units[i].Parameters["module"] = id
			}
		}
		return true
	}
	done := make([]bool, len(mods))
	for range mods { // the modules that a module uses first
		for i := range mods {
			if done[i] || !remap(mods[i].Units) {
				continue
			}
			done[i] = true
			oldID := mods[i].ID
			key := moduleKey(&mods[i])
			found := false
			for _, existing := range m.d.Song.Modules {
				if existing.Name == mods[i].Name && moduleKey(&existing) == key {
					newIDs[oldID], found = existing.ID, true
					break
				}
			}
			if found {
				continue
			}
			mods[i].ID = m.newModuleID()
			mods[i].Name = m.newModuleName(mods[i].Name)
			m.assignUnitIDs(mods[i].Units)
			m.assignBuses(mods[i].Units)
			newIDs[oldID] = mods[i].ID
			m.d.Song.Modules = append(m.d.Song.Modules, mods[i])
		}
	}
	for _, units := range unitLists {
		for i := range units {
			if units[i].Type != "module" {
				continue
			}
			if id, ok := newIDs[units[i].Parameters["module"]]; ok {
				units[i].Parameters["module"] = id
			} else if imported[units[i].Parameters["module"]] {
				units[i].Parameters["module"] = 0 // modules using themselves
			}
		}
	}
	return newIDs
}

// Editing reports whether the unit editor shows the units of a module, on
// the Modules tab, instead of those of the selected instrument.
func (m *ModuleModel) Editing() bool { return (*Model)(m).editingModule() }

// Item returns the name of the module with the given index and the number
// of module units using it.
func (m *ModuleModel) Item(i int) (name string, uses int, ok bool) {
	if i < 0 || i >= len(m.d.Song.Modules) {
		return "", 0, false
	}
	return moduleTitle(&m.d.Song.Modules[i]), (*Model)(m).moduleUses(m.d.Song.Modules[i].ID), true
}

func (m *ModuleModel) selected() *sointu.Module {
	if i := m.d.ModuleIndex; i >= 0 && i < len(m.d.Song.Modules) {
		return &m.d.Song.Modules[i]
	}
	return nil
}

// HeardIn returns the name of the instrument that plays when notes are
// played while editing a module, the selected instrument, and whether it
// uses the selected module at all.
func (m *ModuleModel) HeardIn() (name string, uses bool) {
	mod := m.selected()
	if mod == nil || m.d.InstrIndex < 0 || m.d.InstrIndex >= len(m.d.Song.Patch) {
		return "", false
	}
	instr := m.d.Song.Patch[m.d.InstrIndex]
	for _, used := range (*Model)(m).modulesUsedBy(instr.Units) {
		uses = uses || used.ID == mod.ID
	}
	return instr.Name, uses
}

// Add returns an Action to add a new module.
func (m *ModuleModel) Add() Action { return MakeAction((*addModuleAction)(m)) }

type addModuleAction ModuleModel

func (m *addModuleAction) Do() {
	defer (*Model)(m).change("AddModule", PatchChange, MajorChange)()
	(*Model)(m).addModule()
}

// Delete returns an Action to delete the selected module. Module units
// using it are left without a module.
func (m *ModuleModel) Delete() Action { return MakeAction((*deleteModuleAction)(m)) }

type deleteModuleAction ModuleModel

func (m *deleteModuleAction) Enabled() bool { return len(m.d.Song.Modules) > 0 }
func (m *deleteModuleAction) Do()           { (*ModuleModel)(m).List().DeleteElements(false) }

// List returns a List of the modules of the song.
func (m *ModuleModel) List() List { return List{(*moduleList)(m)} }

type moduleList ModuleModel

func (v *moduleList) Count() int             { return len(v.d.Song.Modules) }
func (v *moduleList) Selected() int          { return v.d.ModuleIndex }
func (v *moduleList) Selected2() int         { return v.d.ModuleIndex }
func (v *moduleList) SetSelected2(value int) {}
func (v *moduleList) SetSelected(value int) {
	if v.d.ModuleIndex == value {
		return
	}
	if (*Model)(v).editingModule() {
		// the unit editor shows the units of the module
		v.d.UnitIndex, v.d.UnitIndex2, v.d.ParamIndex = 0, 0, 0
		(*Model)(v).leaveModuleUnits()
		v.d.UnitSearching = false
		v.d.UnitSearchString = ""
	}
	v.d.ModuleIndex = value
}
func (v *moduleList) Move(r Range, delta int) (ok bool) {
	mods := v.d.Song.Modules
	for i, j := range r.Swaps(delta) {
		mods[i], mods[j] = mods[j], mods[i]
	}
	return true
}
func (v *moduleList) Delete(r Range) (ok bool) {
	mods := v.d.Song.Modules
	for _, mod := range mods[r.Start:r.End] {
		for units := range v.d.Song.UnitLists() {
			for i := range units {
				if units[i].Type == "module" && units[i].Parameters["module"] == mod.ID {
					units[i].Parameters["module"] = 0
				}
			}
		}
	}
	v.d.Song.Modules = append(mods[:r.Start], mods[r.End:]...)
	return true
}
func (v *moduleList) Change(n string, severity ChangeSeverity) func() {
	return (*Model)(v).change("ModuleList."+n, PatchChange, severity)
}
func (v *moduleList) Cancel() { v.changeCancel = true }
func (v *moduleList) Marshal(r Range) ([]byte, error) {
	mods := v.d.Song.Modules[r.Start:r.End]
	// with the modules they use, first
	var all sointu.Modules
	for _, mod := range mods {
		for _, used := range (*Model)(v).modulesUsedBy(mod.Units) {
			if _, ok := all.Find(used.ID); !ok {
				all = append(all, used)
			}
		}
	}
	for _, mod := range mods {
		if _, ok := all.Find(mod.ID); !ok {
			all = append(all, mod.Copy())
		}
	}
	return yaml.Marshal(struct{ Modules sointu.Modules }{all})
}
func (v *moduleList) Unmarshal(data []byte) (r Range, err error) {
	var pasted struct{ Modules sointu.Modules }
	if err := yaml.Unmarshal(data, &pasted); err != nil {
		return Range{}, fmt.Errorf("moduleList.Unmarshal: %v", err)
	}
	if len(pasted.Modules) == 0 {
		return Range{}, errors.New("moduleList.Unmarshal: no modules")
	}
	n := len(v.d.Song.Modules)
	(*Model)(v).importModules(pasted.Modules)
	if len(v.d.Song.Modules) == n {
		return Range{}, errors.New("moduleList.Unmarshal: the song already has the modules")
	}
	return Range{len(v.d.Song.Modules) - 1, len(v.d.Song.Modules)}, nil
}

// Name returns a String representing the name of the selected module.
func (m *ModuleModel) Name() String { return MakeString((*moduleName)(m)) }

type moduleName ModuleModel

func (v *moduleName) Value() string {
	if mod := (*ModuleModel)(v).selected(); mod != nil {
		return mod.Name
	}
	return ""
}
func (v *moduleName) SetValue(value string) bool {
	mod := (*ModuleModel)(v).selected()
	if mod == nil {
		return false
	}
	defer (*Model)(v).change("ModuleName", PatchChange, MinorChange)()
	mod.Name = value
	return true
}

// Comment returns a String representing the comment of the selected module.
func (m *ModuleModel) Comment() String { return MakeString((*moduleComment)(m)) }

type moduleComment ModuleModel

func (v *moduleComment) Value() string {
	if mod := (*ModuleModel)(v).selected(); mod != nil {
		return mod.Comment
	}
	return ""
}
func (v *moduleComment) SetValue(value string) bool {
	mod := (*ModuleModel)(v).selected()
	if mod == nil {
		return false
	}
	defer (*Model)(v).change("ModuleComment", PatchChange, MinorChange)()
	mod.Comment = value
	return true
}

// Inputs returns an Int representing the number of signals that the units
// of the selected module expect on the stack.
func (m *ModuleModel) Inputs() Int { return MakeInt((*moduleInputs)(m)) }

type moduleInputs ModuleModel

func (v *moduleInputs) Value() int {
	if mod := (*ModuleModel)(v).selected(); mod != nil {
		return min(max(mod.Inputs, 0), v.Range().Max)
	}
	return 0
}
func (v *moduleInputs) SetValue(value int) bool {
	mod := (*ModuleModel)(v).selected()
	if mod == nil {
		return false
	}
	defer (*Model)(v).change("ModuleInputs", PatchChange, MinorChange)()
	mod.Inputs = value
	return true
}
func (v *moduleInputs) Range() RangeInclusive { return RangeInclusive{0, 8} }

// Outputs returns the number of signals that the units of the selected
// module leave on the stack, or why they cannot.
func (m *ModuleModel) Outputs() (int, error) {
	if m.selected() == nil {
		return 0, nil
	}
	return m.d.Song.Modules.Outputs(m.d.ModuleIndex)
}

// NumParams returns the number of parameters of the selected module.
func (m *ModuleModel) NumParams() int {
	if mod := m.selected(); mod != nil {
		return min(len(mod.Params), sointu.MaxModuleParams)
	}
	return 0
}

// AddParam returns an Action to add a parameter to the selected module.
func (m *ModuleModel) AddParam() Action { return MakeAction((*addModuleParam)(m)) }

type addModuleParam ModuleModel

func (m *addModuleParam) Enabled() bool {
	mod := (*ModuleModel)(m).selected()
	return mod != nil && len(mod.Params) < sointu.MaxModuleParams
}
func (m *addModuleParam) Do() {
	defer (*Model)(m).change("AddModuleParam", PatchChange, MajorChange)()
	mod := (*ModuleModel)(m).selected()
	mod.Params = append(mod.Params, sointu.ModuleParam{Name: sointu.ModuleParamName(len(mod.Params) + 1)})
}

// DeleteParam returns an Action to delete parameter k (from 1) of the
// selected module. The parameters after it move down by one, in the
// bindings, in the module units using the module and in the sends to them.
func (m *ModuleModel) DeleteParam(k int) Action {
	return MakeAction(deleteModuleParam{k: k, ModuleModel: m})
}

type deleteModuleParam struct {
	k int
	*ModuleModel
}

func (m deleteModuleParam) Enabled() bool {
	mod := m.selected()
	return mod != nil && m.k >= 1 && m.k <= len(mod.Params)
}
func (m deleteModuleParam) Do() {
	defer (*Model)(m.ModuleModel).change("DeleteModuleParam", PatchChange, MajorChange)()
	mod := m.selected()
	mod.Params = append(mod.Params[:m.k-1], mod.Params[m.k:]...)
	for i := range mod.Units {
		for name, b := range mod.Units[i].Bind {
			switch {
			case b.Param == m.k:
				delete(mod.Units[i].Bind, name)
			case b.Param > m.k:
				b.Param--
				mod.Units[i].Bind[name] = b
			}
		}
	}
	calls := map[int]bool{}
	for units := range m.d.Song.UnitLists() {
		for i := range units {
			u := &units[i]
			if u.Type != "module" || u.Parameters["module"] != mod.ID {
				continue
			}
			calls[u.ID] = true
			for k := m.k; k <= sointu.MaxModuleParams; k++ {
				name, next := sointu.ModuleParamName(k), sointu.ModuleParamName(k+1)
				if v, ok := u.Parameters[next]; ok && k < sointu.MaxModuleParams {
					u.Parameters[name] = v
				} else {
					delete(u.Parameters, name)
				}
				if b, ok := u.Bind[next]; ok && k < sointu.MaxModuleParams {
					u.Bind[name] = b
				} else {
					delete(u.Bind, name)
				}
			}
		}
	}
	for units := range m.d.Song.UnitLists() {
		for i := range units {
			u := &units[i]
			if u.Type != "send" || !calls[u.Parameters["target"]] {
				continue
			}
			switch port := u.Parameters["port"]; {
			case port == m.k-1:
				u.Parameters["target"] = 0
			case port > m.k-1:
				u.Parameters["port"] = port - 1
			}
		}
	}
}

// Param returns parameter k (from 1) of the selected module as a unit
// parameter: its name, range and display.
func (m *ModuleModel) Param(k int) (sointu.UnitParameter, bool) {
	if m.selected() == nil {
		return sointu.UnitParameter{}, false
	}
	return m.d.Song.Modules.Param(m.d.ModuleIndex, k)
}

// ParamSource returns the type of the unit and the name of the parameter
// that parameter k (from 1) of the selected module takes its range and
// display from: the first parameter bound to it.
func (m *ModuleModel) ParamSource(k int) (unitType, param string, ok bool) {
	if m.selected() == nil {
		return "", "", false
	}
	return m.d.Song.Modules.ParamSource(m.d.ModuleIndex, k)
}

// ParamName returns a String representing the name of parameter k (from 1)
// of the selected module.
func (m *ModuleModel) ParamName(k int) String { return MakeString(moduleParamName{k, m}) }

type moduleParamName struct {
	k int
	*ModuleModel
}

func (v moduleParamName) param() *sointu.ModuleParam {
	if mod := v.selected(); mod != nil && v.k >= 1 && v.k <= len(mod.Params) {
		return &mod.Params[v.k-1]
	}
	return nil
}
func (v moduleParamName) Value() string {
	if p := v.param(); p != nil {
		return p.Name
	}
	return ""
}
func (v moduleParamName) SetValue(value string) bool {
	p := v.param()
	if p == nil {
		return false
	}
	defer (*Model)(v.ModuleModel).change("ModuleParamName", PatchChange, MinorChange)()
	p.Name = value
	return true
}

// ParamDefault returns an Int representing the default of parameter k (from
// 1) of the selected module: the value that new module units get, and that
// the parameters bound to it show.
func (m *ModuleModel) ParamDefault(k int) Int { return MakeInt(moduleParamDefault{k, m}) }

type moduleParamDefault struct {
	k int
	*ModuleModel
}

func (v moduleParamDefault) Value() int {
	p, _ := v.Param(v.k)
	return p.Default
}
func (v moduleParamDefault) SetValue(value int) bool {
	if _, ok := v.Param(v.k); !ok {
		return false
	}
	defer (*Model)(v.ModuleModel).change("ModuleParamDefault", PatchChange, MinorChange)()
	(*Model)(v.ModuleModel).setModuleDefault(v.d.ModuleIndex, v.k, value)
	return true
}
func (v moduleParamDefault) Range() RangeInclusive {
	p, _ := v.Param(v.k)
	return RangeInclusive{p.MinValue, max(p.MaxValue, p.MinValue)}
}
func (v moduleParamDefault) StringOf(value int) string {
	if p, ok := v.Param(v.k); ok && p.DisplayFunc != nil {
		s, unit := p.DisplayFunc(value)
		return s + " " + unit
	}
	return strconv.Itoa(value)
}

// BindingAt returns an Int representing the value that the parameter under
// the cursor of the unit editor, bound to parameter k (from 1) of the
// selected module, gets when a module unit sets the module parameter to 0, or
// with top, to 128: the Min and Max of a scaled sointu.Binding. Set to the
// whole range of the bound parameter, the binding is not scaled: the bound
// parameter gets the value of the module unit as it is.
//
// Changing the first binding of a module parameter changes what the values
// of the module parameter mean: its default and the values of the module
// units using the module are changed to give the bound parameter the values
// nearest to those it had.
func (m *ModuleModel) BindingAt(k int, top bool) Int { return MakeInt(moduleBindingAt{k, top, m}) }

type moduleBindingAt struct {
	k   int
	top bool
	*ModuleModel
}

// binding returns the binding of the parameter under the cursor to
// parameter k, the unit and the bound parameter.
func (v moduleBindingAt) binding() (b sointu.Binding, unit *sointu.Unit, p sointu.UnitParameter, ok bool) {
	unit, name, ok := moduleParamBound{v.k, v.ModuleModel}.cursor()
	if !ok {
		return b, nil, p, false
	}
	b, ok = unit.Bind[name]
	if !ok || b.Param != v.k {
		return b, nil, p, false
	}
	p, ok = sointu.BindableParam(unit.Type, name)
	if unit.Type == "module" { // a parameter of a module unit: the range of the parameter of its module
		p = sointu.UnitParameter{Name: name, MinValue: 0, MaxValue: 128, CanSet: true}
		if i, found := v.d.Song.Modules.Find(unit.Parameters["module"]); found {
			if mp, found := v.d.Song.Modules.Param(i, moduleParamNumber(name)); found {
				p.MinValue, p.MaxValue = mp.MinValue, mp.MaxValue
			}
		}
	}
	return b, unit, p, ok && p.MaxValue >= p.MinValue
}
func (v moduleBindingAt) Value() int {
	b, _, p, ok := v.binding()
	switch {
	case !ok:
		return 0
	case b.Scaled && v.top:
		return b.Max
	case b.Scaled:
		return b.Min
	case v.top:
		return p.MaxValue
	}
	return p.MinValue
}
func (v moduleBindingAt) Range() RangeInclusive {
	_, _, p, ok := v.binding()
	if !ok {
		return RangeInclusive{}
	}
	return RangeInclusive{p.MinValue, p.MaxValue}
}
func (v moduleBindingAt) SetValue(value int) bool {
	old, unit, p, ok := v.binding()
	if !ok {
		return false
	}
	m := (*Model)(v.ModuleModel)
	defer m.change("ModuleBindingAt", PatchChange, MinorChange)()
	b := sointu.Binding{Param: v.k, Scaled: true, Min: p.MinValue, Max: p.MaxValue}
	if old.Scaled {
		b.Min, b.Max = old.Min, old.Max
	}
	if v.top {
		b.Max = value
	} else {
		b.Min = value
	}
	if b.Min == p.MinValue && b.Max == p.MaxValue {
		b = sointu.Binding{Param: v.k} // the whole range: the value as it is
	}
	_, name, _ := moduleParamBound{v.k, v.ModuleModel}.cursor()
	source, sourceName, _ := m.d.Song.Modules.ParamSourceUnit(m.d.ModuleIndex, v.k)
	first := source == unit && sourceName == name
	unit.Bind[name] = b
	mod := v.selected()
	if !first {
		m.setModuleDefault(m.d.ModuleIndex, v.k, mod.Params[v.k-1].Default)
		return true
	}
	// the values of the module parameter mean something else now
	convert := func(value int) int { return b.Unmap(old.Map(value)) }
	for units := range m.d.Song.UnitLists() {
		for i := range units {
			u := &units[i]
			key := sointu.ModuleParamName(v.k)
			if value, ok := u.Parameters[key]; ok && u.Type == "module" && u.Parameters["module"] == mod.ID {
				u.Parameters[key] = convert(value)
			}
		}
	}
	m.setModuleDefault(m.d.ModuleIndex, v.k, convert(mod.Params[v.k-1].Default))
	return true
}

// moduleParamNumber returns k for the name p<k> of a parameter of a module
// unit, or 0.
func moduleParamNumber(name string) int {
	for k := 1; k <= sointu.MaxModuleParams; k++ {
		if sointu.ModuleParamName(k) == name {
			return k
		}
	}
	return 0
}

// ParamBound returns a Bool telling whether the parameter under the cursor
// of the unit editor is bound to parameter k (from 1) of the selected
// module. Setting it binds the parameter, so that every module unit using
// the module sets it; clearing it unbinds it.
func (m *ModuleModel) ParamBound(k int) Bool { return MakeBool(moduleParamBound{k, m}) }

type moduleParamBound struct {
	k int
	*ModuleModel
}

// cursor returns the unit and the name of the parameter under the cursor,
// if it can be bound.
func (v moduleParamBound) cursor() (*sointu.Unit, string, bool) {
	if _, _, depth := (*Model)(v.ModuleModel).scope(); !v.Editing() || v.selected() == nil || depth > 0 {
		return nil, "", false // an inner unit is not a unit of the selected module
	}
	p := (*Model)(v.ModuleModel).Params().Item((*Model)(v.ModuleModel).Params().Cursor())
	unit, name, ok := p.bindTarget()
	if !ok || !sointu.CanBind(unit.Type, name) {
		return nil, "", false
	}
	return unit, name, true
}
func (v moduleParamBound) Enabled() bool {
	_, _, ok := v.cursor()
	return ok && v.k >= 1 && v.k <= v.NumParams()
}
func (v moduleParamBound) Value() bool {
	unit, name, ok := v.cursor()
	if !ok {
		return false
	}
	b, bound := unit.Bind[name]
	return bound && b.Param == v.k
}
func (v moduleParamBound) SetValue(val bool) {
	unit, name, ok := v.cursor()
	if !ok {
		return
	}
	m := (*Model)(v.ModuleModel)
	defer m.change("BindModuleParam", PatchChange, MajorChange)()
	if !val {
		delete(unit.Bind, name)
		return
	}
	m.bindParam(m.d.ModuleIndex, unit, name, v.k)
}

// bindParam binds what the name stands for (see sointu.Unit.BoundValue) of a
// unit of the module with the given index to parameter k (from 1) of the
// module.
func (m *Model) bindParam(module int, unit *sointu.Unit, name string, k int) {
	_, _, hadSource := m.d.Song.Modules.ParamSource(module, k)
	if unit.Bind == nil {
		unit.Bind = map[string]sointu.Binding{}
	}
	unit.Bind[name] = sointu.Binding{Param: k}
	if hadSource {
		m.setModuleDefault(module, k, m.d.Song.Modules[module].Params[k-1].Default)
		return
	}
	// the first parameter bound to it: its value becomes the default, and
	// its range the range
	value := unit.BoundValue(name)
	if _, set := unit.Parameters[name]; !set && unit.Type == "module" {
		// a module unit that does not set the parameter of its module has
		// the default of that module
		if i, ok := m.d.Song.Modules.Find(unit.Parameters["module"]); ok {
			if mp, ok := m.d.Song.Modules.Param(i, moduleParamNumber(name)); ok {
				value = mp.Default
			}
		}
	}
	m.setModuleDefault(module, k, value)
}

// Binding returns an Int representing the parameter of the module that the
// parameter under the cursor of the unit editor is bound to, while the units
// of a module are being edited, also as inner units of a module unit: 0 for
// none, and one more than the module has parameters for a new parameter,
// named after the parameter under the cursor. Setting it binds the
// parameter, so that every module unit using the module sets it, or unbinds
// it.
func (m *ParamModel) Binding() Int { return MakeInt((*paramBinding)(m)) }

type paramBinding ParamModel

// target returns the index of the module being edited, the unit and what of
// it is under the cursor, if it can be bound.
func (v *paramBinding) target() (module int, unit *sointu.Unit, name string, ok bool) {
	m := (*Model)(v)
	module, ok = m.scopeModule()
	if !ok {
		return 0, nil, "", false
	}
	p := (*ParamModel)(v).Item((*ParamModel)(v).Cursor())
	unit, name, ok = p.bindTarget()
	if !ok || !sointu.CanBind(unit.Type, name) {
		return 0, nil, "", false
	}
	if owner, found := m.moduleOf(unit); !found || owner != module {
		return 0, nil, "", false
	}
	return module, unit, name, true
}
func (v *paramBinding) Enabled() bool {
	_, _, _, ok := v.target()
	return ok
}
func (v *paramBinding) Value() int {
	module, unit, name, ok := v.target()
	if !ok {
		return 0
	}
	if b, bound := unit.Bind[name]; bound && b.Param >= 1 && b.Param <= len(v.d.Song.Modules[module].Params) {
		return b.Param
	}
	return 0
}
func (v *paramBinding) Range() RangeInclusive {
	module, _, _, ok := v.target()
	if !ok {
		return RangeInclusive{}
	}
	return RangeInclusive{0, min(len(v.d.Song.Modules[module].Params)+1, sointu.MaxModuleParams)}
}
func (v *paramBinding) StringOf(k int) string {
	module, _, _, ok := v.target()
	switch {
	case !ok || k <= 0:
		return "not bound"
	case k > len(v.d.Song.Modules[module].Params):
		return "new parameter"
	}
	mp, _ := v.d.Song.Modules.Param(module, k)
	return mp.Name
}
func (v *paramBinding) SetValue(k int) bool {
	module, unit, name, ok := v.target()
	if !ok {
		return false
	}
	m := (*Model)(v)
	defer m.change("BindModuleParam", PatchChange, MajorChange)()
	if k <= 0 {
		delete(unit.Bind, name)
		return true
	}
	mod := &v.d.Song.Modules[module]
	if k > len(mod.Params) {
		p := (*ParamModel)(v).Item((*ParamModel)(v).Cursor())
		mod.Params = append(mod.Params, sointu.ModuleParam{Name: p.vtable.Name(&p)})
		k = len(mod.Params)
	}
	m.bindParam(module, unit, name, k)
	return true
}

// Actions on the units that involve modules

// MakeModule returns an Action to make a module of the selected units: they
// become the units of a new module, and a module unit using it takes their
// place.
func (m *UnitModel) MakeModule() Action { return MakeAction((*makeModule)(m)) }

type makeModule UnitModel

func (m *makeModule) Enabled() bool { return len((*Model)(m).units()) > 0 }
func (m *makeModule) Do() {
	model := (*Model)(m)
	defer model.change("MakeModule", PatchChange, MajorChange)()
	list := model.unitsPtr()
	r := model.unitRange()
	if list == nil || r.Len() <= 0 {
		m.changeCancel = true
		return
	}
	units := make([]sointu.Unit, r.Len())
	for i := range units {
		units[i] = (*list)[r.Start+i].Copy()
		units[i].Bind = nil
	}
	// the inputs are the signals that the units take from below what they
	// push themselves
	depth, inputs := 0, 0
	for i := range units {
		use := m.d.Song.Modules.StackUse(&units[i])
		depth -= len(use.Inputs)
		inputs = max(inputs, -depth)
		depth += use.NumOutputs
	}
	mod := sointu.Module{ID: model.newModuleID(), Name: model.newModuleName("Module"), Inputs: inputs, Units: units}
	call := sointu.MakeUnit("module")
	call.Parameters["module"] = mod.ID
	call.ID = model.maxID() + 1
	rest := append([]sointu.Unit{call}, (*list)[r.End:]...)
	*list = append((*list)[:r.Start:r.Start], rest...)
	m.d.Song.Modules = append(m.d.Song.Modules, mod)
	m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = r.Start, r.Start, 0
}

// selectedModuleUnit returns the selected unit and the index of its module,
// if it is a module unit with a module.
func (m *Model) selectedModuleUnit() (*sointu.Unit, int, bool) {
	u := m.selectedUnit()
	if u == nil || u.Type != "module" {
		return nil, 0, false
	}
	i, ok := m.d.Song.Modules.Find(u.Parameters["module"])
	return u, i, ok
}

// InlineModule returns an Action to replace the selected module unit with
// copies of the units of its module, with the values the module unit gives
// their bound parameters. Sends to the module unit are left without a
// target.
func (m *UnitModel) InlineModule() Action { return MakeAction((*inlineModule)(m)) }

type inlineModule UnitModel

func (m *inlineModule) Enabled() bool {
	_, _, ok := (*Model)(m).selectedModuleUnit()
	return ok
}
func (m *inlineModule) Do() {
	model := (*Model)(m)
	defer model.change("InlineModule", PatchChange, MajorChange)()
	call, index, _ := model.selectedModuleUnit()
	_, inModule := model.scopeModule()
	mod := &m.d.Song.Modules[index]
	units := make([]sointu.Unit, 0, len(mod.Units))
	for _, u := range mod.Units {
		c := u.Copy()
		for name, b := range u.Bind {
			k := b.Param
			delete(c.Bind, name)
			p, ok := sointu.BindableParam(u.Type, name)
			if !ok {
				continue
			}
			if outer, ok := call.Bind[sointu.ModuleParamName(k)]; ok && inModule {
				// bound to the module being edited instead, through both
				// bindings
				through := sointu.Binding{Param: outer.Param}
				if b.Scaled || outer.Scaled {
					through.Scaled = true
					if lo, hi := 0, 128; outer.Scaled {
						through.Min, through.Max = b.Map(outer.Map(lo)), b.Map(outer.Map(hi))
					} else {
						through.Min, through.Max = b.Min, b.Max
					}
				}
				c.Bind[name] = through
			}
			if !p.CanSet {
				continue
			}
			v, ok := call.Parameters[sointu.ModuleParamName(k)]
			if !ok {
				if mp, ok := m.d.Song.Modules.Param(index, k); ok {
					v = mp.Default
				}
			}
			v = b.Map(v)
			if u.Type != "module" {
				v = min(max(v, p.MinValue), max(p.MaxValue, p.MinValue))
			}
			c.SetBoundValue(name, v)
		}
		if len(c.Bind) == 0 {
			c.Bind = nil
		}
		units = append(units, c)
	}
	model.assignUnitIDs(units) // also moves the sends between them
	model.assignBuses(units)
	list := model.unitsPtr()
	i := m.d.UnitIndex
	rest := append(units, (*list)[i+1:]...)
	*list = append((*list)[:i:i], rest...)
	m.d.UnitIndex2 = i + max(len(units)-1, 0)
}

// UniqueModule returns an Action to give the selected module unit a copy of
// its module of its own, so that changing it does not change the other
// module units using the module.
func (m *UnitModel) UniqueModule() Action { return MakeAction((*uniqueModule)(m)) }

type uniqueModule UnitModel

func (m *uniqueModule) Enabled() bool {
	_, _, ok := (*Model)(m).selectedModuleUnit()
	return ok
}
func (m *uniqueModule) Do() {
	model := (*Model)(m)
	defer model.change("UniqueModule", PatchChange, MajorChange)()
	call, index, _ := model.selectedModuleUnit()
	mod := m.d.Song.Modules[index].Copy()
	mod.ID = model.newModuleID()
	mod.Name = model.newModuleName(mod.Name)
	model.assignUnitIDs(mod.Units)
	model.assignBuses(mod.Units)
	call.Parameters["module"] = mod.ID
	m.d.Song.Modules = append(m.d.Song.Modules, mod)
}

// OpenModule returns an Action to show the module of the selected module
// unit on the Modules tab, or with the cursor on an inner unit that is no
// module unit, the module that the cursor is in.
func (m *UnitModel) OpenModule() Action { return MakeAction((*openModule)(m)) }

type openModule UnitModel

func (m *openModule) module() (int, bool) {
	if _, index, ok := (*Model)(m).selectedModuleUnit(); ok {
		return index, true
	}
	_, module, depth := (*Model)(m).scope()
	return module, depth > 0
}
func (m *openModule) Enabled() bool {
	_, ok := m.module()
	return ok
}
func (m *openModule) Do() {
	index, _ := m.module()
	m.d.InstrumentTab = InstrumentModulesTab
	m.d.ModuleIndex = index
	m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
	(*Model)(m).leaveModuleUnits()
	m.d.UnitSearching = false
}

// ExpandedUnits returns the number of units of the selected instrument once
// its module units are replaced by the units of their modules, and the most
// an instrument can have.
func (m *UnitModel) ExpandedUnits() (count, limit int) {
	if i := m.d.InstrIndex; i >= 0 && i < len(m.derived.patch) {
		count = m.derived.patch[i].expandedUnits
	}
	return count, maxUnits
}

// maxUnits is the number of units an instrument can have (vm.MAX_UNITS).
const maxUnits = 63
