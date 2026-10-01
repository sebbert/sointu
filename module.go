package sointu

import (
	"fmt"
	"strconv"
)

type (
	// Module is a reusable block of units. A unit of the type "module" in an
	// instrument, or in another module, stands for the units of the module:
	// Song.Expand replaces it with a copy of them, so every such unit has
	// its own state, in every voice. The synths and the compiled players
	// only ever see the copies.
	//
	// The units of a module expect Inputs signals on the stack and leave
	// the number Modules.StackUse computes. Their parameters can be bound
	// to the parameters of the module (Unit.Bind), which each module unit
	// sets; a send to a module unit modulates the parameters bound to that
	// port.
	Module struct {
		// ID is used by module units to refer to this module, and stays the
		// same when modules are reordered. ID 0 means no module.
		ID      int
		Name    string `yaml:",omitempty"`
		Comment string `yaml:",omitempty"`
		// Inputs is the number of signals the units of the module expect on
		// the stack.
		Inputs int `yaml:",omitempty"`
		// Params are the parameters of the module, at most MaxModuleParams.
		// Parameter k (from 1) is p<k> of the module units.
		Params []ModuleParam `yaml:",omitempty"`
		Units  []Unit
	}

	// ModuleParam is a parameter of a module. Its range and display are
	// those of the first parameter bound to it, unless overridden.
	ModuleParam struct {
		Name string `yaml:",omitempty"`
		// Default is the value of new module units.
		Default int `yaml:",omitempty"`
		// Min and Max override the range of the parameter, unless both are
		// 0. Values are still clamped to the range of each parameter bound
		// to it.
		Min int `yaml:",omitempty"`
		Max int `yaml:",omitempty"`
		// Display overrides how values are displayed: the type of a unit
		// and the name of its parameter to display the values like, as
		// "type.parameter", e.g. "filter.frequency".
		Display string `yaml:",omitempty"`
	}

	// Modules is the list of modules of a song.
	Modules []Module

	// Expansion tells how Song.Expand expanded a song.
	Expansion struct {
		// Units maps the ID of each unit copied from a module to where it
		// came from.
		Units map[int]ExpandedUnit
		// Buffers maps the ID of each buffer cloned for a module unit to the
		// ID of the buffer it is a clone of.
		Buffers map[int]int
		// Problems are the things that could not be expanded as meant, e.g.
		// modules using themselves. The expanded song leaves them out.
		Problems []error
	}

	// ExpandedUnit is where a unit copied from a module came from: the ID
	// of the module unit of an instrument that it was expanded from (the
	// outermost, if modules use modules; 0 if it has no ID), the instrument,
	// the module it is a unit of, and its ID there (0 if it has none).
	ExpandedUnit struct {
		Call, Instrument, Module, Body int
	}
)

// MaxModuleParams is the number of parameters a module can have: a send
// can address 8 ports.
const MaxModuleParams = 8

// ModuleParamName returns the name of parameter k (from 1) of the module
// units: p1 to p8.
func ModuleParamName(k int) string { return "p" + strconv.Itoa(k) }

// moduleParamIndex returns k (from 1) for the parameter p<k> of a module
// unit, or 0.
func moduleParamIndex(name string) int {
	if len(name) == 2 && name[0] == 'p' && name[1] >= '1' && name[1] <= '0'+MaxModuleParams {
		return int(name[1] - '0')
	}
	return 0
}

// CanBind reports whether a parameter of a unit in a module can be bound to
// a parameter of the module: any parameter that can be set or modulated,
// except those that change how the unit uses the stack or where a send goes.
func CanBind(unitType, param string) bool {
	if param == "stereo" {
		return false
	}
	switch unitType {
	case "spawn":
		if param == "args" || param == "mode" {
			return false
		}
	case "send":
		if param != "amount" {
			return false
		}
	case "module":
		if param == "module" {
			return false
		}
	}
	for _, p := range UnitTypes[unitType].Params {
		if p.Name == param {
			return p.CanSet || p.CanModulate
		}
	}
	return false
}

// Copy makes a deep copy of a module.
func (m *Module) Copy() Module {
	ret := *m
	if m.Params != nil {
		ret.Params = append([]ModuleParam{}, m.Params...)
	}
	ret.Units = make([]Unit, len(m.Units))
	for i, u := range m.Units {
		ret.Units[i] = u.Copy()
	}
	return ret
}

// Copy makes a deep copy of the modules.
func (m Modules) Copy() Modules {
	if m == nil {
		return nil
	}
	ret := make(Modules, len(m))
	for i := range m {
		ret[i] = m[i].Copy()
	}
	return ret
}

// Find returns the index of the module with the given ID.
func (m Modules) Find(id int) (int, bool) {
	if id != 0 {
		for i := range m {
			if m[i].ID == id {
				return i, true
			}
		}
	}
	return 0, false
}

// Uses reports whether the units of module a use module b, directly or
// through other modules.
func (m Modules) Uses(a, b int) bool {
	return m.uses(a, b, map[int]bool{})
}

func (m Modules) uses(a, b int, seen map[int]bool) bool {
	i, ok := m.Find(a)
	if !ok || seen[a] {
		return false
	}
	seen[a] = true
	for _, u := range m[i].Units {
		if u.Type != "module" || u.Disabled {
			continue
		}
		if id := u.Parameters["module"]; id == b || m.uses(id, b, seen) {
			return true
		}
	}
	return false
}

// StackUse returns how a unit uses the stack, like Unit.StackUse, but also
// for module units: they take the inputs of their module and leave its
// outputs, every output depending on every input. Module units without a
// module, or with one whose units underflow the stack or use the module
// itself, use nothing.
func (m Modules) StackUse(u *Unit) StackUse {
	ret, _ := m.stackUse(u, nil)
	return ret
}

func (m Modules) stackUse(u *Unit, path []int) (StackUse, error) {
	if u.Type != "module" || u.Disabled {
		return u.StackUse(), nil
	}
	i, ok := m.Find(u.Parameters["module"])
	if !ok {
		return StackUse{}, nil
	}
	outputs, err := m.outputs(i, path)
	if err != nil {
		return StackUse{}, err
	}
	ret := StackUse{Inputs: make([][]int, max(m[i].Inputs, 0)), Modifies: make([]bool, outputs), NumOutputs: outputs}
	all := make([]int, outputs)
	for j := range all {
		all[j] = j
		ret.Modifies[j] = true
	}
	for j := range ret.Inputs {
		ret.Inputs[j] = all
	}
	return ret, nil
}

// Outputs returns the number of signals the units of the module with the
// given index leave on the stack, given its inputs. It fails if they take
// more signals than there are, or if the module uses itself.
func (m Modules) Outputs(index int) (int, error) {
	return m.outputs(index, nil)
}

func (m Modules) outputs(index int, path []int) (int, error) {
	mod := &m[index]
	for _, id := range path {
		if id == mod.ID {
			return 0, fmt.Errorf("module %v uses itself", mod.title())
		}
	}
	path = append(path, mod.ID)
	depth := max(mod.Inputs, 0)
	for j := range mod.Units {
		use, err := m.stackUse(&mod.Units[j], path)
		if err != nil {
			return 0, err
		}
		if len(use.Inputs) > depth {
			return 0, fmt.Errorf("unit %d / %s of module %v needs %d inputs, but got only %d", j, mod.Units[j].Type, mod.title(), len(use.Inputs), depth)
		}
		depth += use.NumOutputs - len(use.Inputs)
	}
	return depth, nil
}

func (m *Module) title() string {
	if m.Name != "" {
		return m.Name
	}
	return strconv.Itoa(m.ID)
}

// ParamSource returns the type of the unit and the parameter that parameter
// k (from 1) of the module with the given index takes its range and display
// from: the first parameter bound to it, through modules used by the
// module. ok is false if nothing is bound to it.
func (m Modules) ParamSource(index, k int) (unitType, param string, ok bool) {
	return m.paramSource(index, k, 0)
}

func (m Modules) paramSource(index, k, depth int) (string, string, bool) {
	if depth > len(m) {
		return "", "", false // modules using themselves
	}
	for _, u := range m[index].Units {
		if u.Disabled {
			continue
		}
		ut, ok := UnitTypes[u.Type]
		if !ok {
			continue
		}
		for _, p := range ut.Params { // in the order of the parameters
			if u.Bind[p.Name] != k || !CanBind(u.Type, p.Name) {
				continue
			}
			if u.Type != "module" {
				return u.Type, p.Name, true
			}
			if i, ok := m.Find(u.Parameters["module"]); ok {
				if t, n, ok := m.paramSource(i, moduleParamIndex(p.Name), depth+1); ok {
					return t, n, true
				}
			}
		}
	}
	return "", "", false
}

// Param returns the parameter k (from 1) of the module with the given
// index as a unit parameter: its name, its range, its default and how its
// values are displayed. They come from the first parameter bound to it,
// unless the module overrides them. ok is false if the module has no such
// parameter.
func (m Modules) Param(index, k int) (ret UnitParameter, ok bool) {
	if index < 0 || index >= len(m) || k < 1 || k > len(m[index].Params) || k > MaxModuleParams {
		return UnitParameter{}, false
	}
	mp := m[index].Params[k-1]
	ret = UnitParameter{Name: mp.Name, MinValue: 0, MaxValue: 128, CanSet: true, CanModulate: true}
	if t, n, ok := m.ParamSource(index, k); ok {
		for _, p := range UnitTypes[t].Params {
			if p.Name == n {
				ret.MinValue, ret.MaxValue, ret.Neutral, ret.DisplayFunc = p.MinValue, p.MaxValue, p.Neutral, p.DisplayFunc
				ret.CanSet, ret.CanModulate = p.CanSet, p.CanModulate
			}
		}
	}
	if mp.Min != 0 || mp.Max != 0 {
		ret.MinValue, ret.MaxValue = mp.Min, max(mp.Min, mp.Max)
	}
	if t, n, ok := splitDisplay(mp.Display); ok {
		for _, p := range UnitTypes[t].Params {
			if p.Name == n {
				ret.DisplayFunc = p.DisplayFunc
			}
		}
	}
	ret.Default = min(max(mp.Default, ret.MinValue), max(ret.MaxValue, ret.MinValue))
	if ret.Name == "" {
		ret.Name = ModuleParamName(k)
	}
	return ret, true
}

func splitDisplay(s string) (unitType, param string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// MakeModuleUnit returns a new module unit for the module with the given
// index, with the default values of its parameters.
func (m Modules) MakeModuleUnit(index int) Unit {
	u := MakeUnit("module")
	u.Parameters["module"] = m[index].ID
	for k := 1; k <= len(m[index].Params) && k <= MaxModuleParams; k++ {
		if p, ok := m.Param(index, k); ok {
			u.Parameters[ModuleParamName(k)] = p.Default
		}
	}
	return u
}

// bufferParams returns the names of the parameters of a unit type that are
// the IDs of buffers.
func bufferParams(unitType string) []string {
	switch unitType {
	case "bufread", "bufwrite":
		return []string{"buffer"}
	}
	if p := SpectrumBufferParams(unitType); p != nil {
		return p
	}
	return BusParams(unitType)
}

// HasModules reports whether the song has module units to expand.
func (s *Song) HasModules() bool {
	for _, instr := range s.Patch {
		for _, u := range instr.Units {
			if u.Type == "module" {
				return true
			}
		}
	}
	return false
}

// Expand returns the song with every module unit replaced by a copy of the
// units of its module, as the synths and the compiler need it: without
// modules. A song without module units is returned as it is, sharing its
// data with s.
//
//   - The parameters bound to the parameters of a module (Unit.Bind) get the
//     values of the module unit, clamped to their ranges.
//   - The copies get new IDs, above every ID of the song. Sends in a module
//     to units of the module go to the copies made with them. Sends to other
//     units stay as they are.
//   - A mono send to a module unit modulates the parameters bound to the
//     module parameter of that port: it becomes a send to each of them, only
//     the last one popping. Without any, a popping send becomes a pop.
//   - Buffers that the tracker created (Buffer.Auto) and that only the units
//     of one module use belong to that module: its first module unit uses
//     them, and every further one gets clones of them.
//   - Disabled units, and module units without a module, are left out.
//
// What cannot be expanded is left out and reported in Expansion.Problems:
// modules using themselves, bindings that are not allowed, stereo sends to
// module units, and sends from outside a module to one of its units, as
// they are ambiguous.
func (s *Song) Expand() (Song, *Expansion) {
	exp := &Expansion{}
	if !s.HasModules() {
		return *s, exp
	}
	e := expander{song: s, exp: exp, ports: map[int]*[MaxModuleParams][]portTarget{}, bodyIDs: map[int]int{}}
	exp.Units, exp.Buffers = map[int]ExpandedUnit{}, map[int]int{}
	for _, instr := range s.Patch {
		for _, u := range instr.Units {
			e.nextID = max(e.nextID, u.ID)
		}
	}
	for _, m := range s.Modules {
		for _, u := range m.Units {
			e.nextID = max(e.nextID, u.ID)
			if u.ID != 0 {
				e.bodyIDs[u.ID] = m.ID
			}
		}
	}
	for _, b := range s.Buffers {
		e.nextBuffer = max(e.nextBuffer, b.ID)
	}
	e.findOwnedBuffers()
	ret := *s
	ret.Modules = nil
	ret.Buffers = s.Buffers[:len(s.Buffers):len(s.Buffers)] // clones are appended to a copy
	e.buffers = &ret.Buffers
	ret.Patch = make(Patch, len(s.Patch))
	for i, instr := range s.Patch {
		ret.Patch[i] = instr
		units := make([]Unit, 0, len(instr.Units))
		for _, u := range instr.Units {
			if u.Type == "module" {
				e.call, e.instr = u.ID, i
				units = e.instantiate(units, &u, u.ID, nil)
			} else {
				units = append(units, u.Copy())
			}
		}
		ret.Patch[i].Units = units
	}
	// the sends to module units, and to units of modules from outside
	for i := range ret.Patch {
		units := ret.Patch[i].Units
		var out []Unit
		for j, u := range units {
			if u.Type != "send" || u.Disabled {
				if out != nil {
					out = append(out, u)
				}
				continue
			}
			target := u.Parameters["target"]
			ports, isCall := e.ports[target]
			if !isCall {
				if m, ok := e.bodyIDs[target]; ok {
					e.problem("a send in instrument %d / %s targets a unit of module %v from outside it: send to a module unit instead", i, ret.Patch[i].Name, m)
				}
				if out != nil {
					out = append(out, u)
				}
				continue
			}
			if out == nil {
				out = append(make([]Unit, 0, len(units)), units[:j]...)
			}
			var targets []portTarget
			if u.Parameters["stereo"]&1 == 1 {
				// the parameters bound to two ports need not be next to each
				// other, as a stereo send needs them
				e.problem("a stereo send in instrument %d / %s targets a module unit: use two mono sends", i, ret.Patch[i].Name)
			} else if port := u.Parameters["port"]; port >= 0 && port < MaxModuleParams {
				targets = ports[port]
			}
			pop := u.Parameters["sendpop"] == 1
			for k, t := range targets {
				c := u.Copy()
				if k > 0 {
					c.ID = 0
				}
				c.Parameters["target"], c.Parameters["port"] = t.unit, t.port
				c.Parameters["sendpop"] = 0
				if pop && k == len(targets)-1 {
					c.Parameters["sendpop"] = 1
				}
				out = append(out, c)
			}
			if len(targets) == 0 && pop {
				p := MakeUnit("pop")
				p.Parameters["stereo"] = u.Parameters["stereo"] & 1
				out = append(out, p)
			}
		}
		if out != nil {
			ret.Patch[i].Units = out
		}
	}
	return ret, exp
}

type (
	expander struct {
		song       *Song
		exp        *Expansion
		buffers    *Buffers
		nextID     int
		nextBuffer int
		// ports are the parameters that each module unit's ports modulate,
		// by the ID of the module unit (of its copy, for module units in
		// modules)
		ports map[int]*[MaxModuleParams][]portTarget
		// bodyIDs maps the IDs of the units of the modules to their module
		bodyIDs map[int]int
		// owned maps the IDs of the buffers that belong to a module to the
		// ID of the module, and used tells which of them a module unit
		// already uses
		owned map[int]int
		used  map[int]bool
		// the module unit of the instrument being expanded, and the
		// instrument
		call, instr int
	}

	// portTarget is a modulated parameter: the ID of the unit and the
	// number of the port
	portTarget struct{ unit, port int }
)

func (e *expander) problem(format string, args ...any) {
	e.exp.Problems = append(e.exp.Problems, fmt.Errorf(format, args...))
}

// findOwnedBuffers finds the buffers that belong to a module: the ones the
// tracker created that only units of that module use.
func (e *expander) findOwnedBuffers() {
	const shared = -1
	users := map[int]int{} // buffer ID -> module ID, 0 for instruments
	note := func(units []Unit, module int) {
		for _, u := range units {
			for _, name := range bufferParams(u.Type) {
				id := u.Parameters[name]
				if _, bound := u.Bind[name]; bound || id == 0 {
					continue
				}
				if m, ok := users[id]; ok && m != module {
					users[id] = shared
				} else if !ok {
					users[id] = module
				}
			}
		}
	}
	for _, instr := range e.song.Patch {
		note(instr.Units, 0)
	}
	for _, m := range e.song.Modules {
		note(m.Units, m.ID)
	}
	e.owned, e.used = map[int]int{}, map[int]bool{}
	for _, b := range e.song.Buffers {
		if m := users[b.ID]; b.Auto && m > 0 {
			e.owned[b.ID] = m
		}
	}
}

// instantiate appends the units of the module of the module unit call to
// units. id is the ID that sends to the module unit use, and path the
// modules being expanded.
func (e *expander) instantiate(units []Unit, call *Unit, id int, path []int) []Unit {
	if call.Disabled {
		return units
	}
	index, ok := e.song.Modules.Find(call.Parameters["module"])
	if !ok {
		if call.Parameters["module"] != 0 {
			e.problem("a module unit uses module %d, which the song does not have", call.Parameters["module"])
		}
		return units
	}
	mod := &e.song.Modules[index]
	for _, m := range path {
		if m == mod.ID {
			e.problem("module %v uses itself", mod.title())
			return units
		}
	}
	path = append(path, mod.ID)
	var ports *[MaxModuleParams][]portTarget
	if id != 0 {
		ports = new([MaxModuleParams][]portTarget)
		e.ports[id] = ports
	}
	// the buffers of the module: the first module unit to use them takes
	// the buffers themselves, the others clones
	bufs := map[int]int{}
	for _, u := range mod.Units {
		if u.Disabled {
			continue
		}
		for _, name := range bufferParams(u.Type) {
			b := u.Parameters[name]
			if _, done := bufs[b]; done || e.owned[b] != mod.ID {
				continue
			}
			if _, bound := u.Bind[name]; bound {
				continue
			}
			if !e.used[b] {
				e.used[b], bufs[b] = true, b
				continue
			}
			e.nextBuffer++
			bufs[b] = e.nextBuffer
			e.exp.Buffers[e.nextBuffer] = b
			if orig, ok := e.song.Buffers.Find(b); ok {
				orig.ID = e.nextBuffer
				*e.buffers = append(*e.buffers, orig)
			}
		}
	}
	// new IDs for the units
	ids := map[int]int{}
	newID := func(u *Unit) int {
		e.nextID++
		e.exp.Units[e.nextID] = ExpandedUnit{Call: e.call, Instrument: e.instr, Module: mod.ID, Body: u.ID}
		return e.nextID
	}
	for i := range mod.Units {
		if u := &mod.Units[i]; u.ID != 0 && !u.Disabled {
			if _, ok := ids[u.ID]; !ok {
				ids[u.ID] = newID(u)
			}
		}
	}
	for i := range mod.Units {
		u := &mod.Units[i]
		if u.Disabled {
			continue
		}
		c := u.Copy()
		c.Bind = nil
		if c.ID != 0 {
			c.ID = ids[c.ID]
		}
		for _, name := range bufferParams(c.Type) {
			if b, ok := bufs[c.Parameters[name]]; ok {
				if _, bound := u.Bind[name]; !bound {
					c.Parameters[name] = b
				}
			}
		}
		if c.Type == "send" {
			if t, ok := ids[c.Parameters["target"]]; ok {
				c.Parameters["target"] = t
			}
		}
		// the values of the bound parameters, in the order of the
		// parameters
		var bound []UnitParameter
		var boundTo []int
		for _, p := range UnitTypes[c.Type].Params {
			k, ok := u.Bind[p.Name]
			if !ok {
				continue
			}
			if !CanBind(c.Type, p.Name) || k < 1 || k > MaxModuleParams {
				e.problem("module %v binds %s of a %s unit to parameter %d, which is not possible", mod.title(), p.Name, c.Type, k)
				continue
			}
			bound, boundTo = append(bound, p), append(boundTo, k)
			if !p.CanSet {
				continue // only a port
			}
			v, ok := call.Parameters[ModuleParamName(k)]
			if !ok && k <= len(mod.Params) {
				if mp, ok := e.song.Modules.Param(index, k); ok {
					v = mp.Default
				}
			}
			if c.Type != "module" {
				v = min(max(v, p.MinValue), max(p.MaxValue, p.MinValue))
			}
			c.Parameters[p.Name] = v
		}
		if c.Type == "module" {
			// the ports of the inner module unit become ports of this one
			inner := c.ID
			if inner == 0 && len(bound) > 0 && ports != nil {
				e.nextID++ // only to find its ports below
				inner = e.nextID
			}
			units = e.instantiate(units, &c, inner, path)
			if ports != nil {
				if innerPorts := e.ports[inner]; innerPorts != nil {
					for j, p := range bound {
						ports[boundTo[j]-1] = append(ports[boundTo[j]-1], innerPorts[moduleParamIndex(p.Name)-1]...)
					}
				}
			}
			continue
		}
		if ports != nil {
			for j, p := range bound {
				if !p.CanModulate {
					continue
				}
				if c.ID == 0 {
					c.ID = newID(u) // so that sends can find it
				}
				port := 0
				for _, q := range UnitTypes[c.Type].Params {
					if q.Name == p.Name {
						break
					}
					if q.CanModulate {
						port++
					}
				}
				ports[boundTo[j]-1] = append(ports[boundTo[j]-1], portTarget{c.ID, port})
			}
		}
		units = append(units, c)
	}
	return units
}

// NumExpandedUnits returns the number of units that the units have once
// their module units are expanded, without the sends added for sends to
// module units: what counts towards the units an instrument can have.
func (m Modules) NumExpandedUnits(units []Unit) int {
	return m.numExpanded(units, 0)
}

func (m Modules) numExpanded(units []Unit, depth int) int {
	n := 0
	for _, u := range units {
		switch {
		case u.Disabled || u.Type == "":
		case u.Type != "module":
			n++
		default:
			if i, ok := m.Find(u.Parameters["module"]); ok && depth <= len(m) {
				n += m.numExpanded(m[i].Units, depth+1)
			}
		}
	}
	return n
}
