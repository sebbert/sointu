package tracker

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// Remote is the model as a remote control reads and changes it: the MCP
// server of tracker/mcp, through which a language model works on the patch.
// It names things instead of pointing at them with the cursor: units by
// their ID, instruments by their index or name, parameters by their name.
//
// Every change is one step of the undo history, moves the cursor to what
// changed and shows an alert. A change that is not valid is refused as a
// whole, and leaves the song as it was.
//
// Like the rest of the model, its methods are called from the goroutine
// that owns the model: see the func() messages of Model.ProcessMsg.
type Remote Model

// Remote returns the model as a remote control reads and changes it.
func (m *Model) Remote() *Remote { return (*Remote)(m) }

type (
	// RemoteUnitEdit changes a unit. Fields left out stay as they are.
	RemoteUnitEdit struct {
		Unit     int                       `json:"unit" jsonschema:"ID of the unit to change"`
		Params   map[string]any            `json:"params,omitempty" jsonschema:"parameter values by name. Numbers within the range of the parameter. A string names a value as the tracker displays it (e.g. type: sine). For a send: target is a unit ID and port the name of the parameter it modulates. For a module unit: module is the name of the module and p1 to p8 (or the names of its parameters) its parameters. delaytime1 and up are the delay times of a delay unit and delaylines their number."`
		Disabled *bool                     `json:"disabled,omitempty" jsonschema:"true disables the unit: it is not played and does not count on the stack"`
		Comment  *string                   `json:"comment,omitempty"`
		Bands    *[]RemoteEQBand           `json:"bands,omitempty" jsonschema:"for an eq unit: all its bands"`
		Bind     map[string]*RemoteBinding `json:"bind,omitempty" jsonschema:"for a unit of a module: binds parameters of the unit, by name, to parameters of the module; null unbinds"`
	}

	// RemoteNewUnit is a unit to add.
	RemoteNewUnit struct {
		Type     string                    `json:"type" jsonschema:"the unit type: see the unit_types tool"`
		Params   map[string]any            `json:"params,omitempty" jsonschema:"parameter values by name, as for edit_units; the others get their defaults. The target of a send can be new:N, the N-th unit of this call, from 0."`
		Disabled bool                      `json:"disabled,omitempty"`
		Comment  string                    `json:"comment,omitempty"`
		Bands    []RemoteEQBand            `json:"bands,omitempty" jsonschema:"for an eq unit: its bands"`
		Bind     map[string]*RemoteBinding `json:"bind,omitempty" jsonschema:"for a unit of a module: see edit_units"`
	}

	// RemoteEQBand is a band of an eq unit.
	RemoteEQBand struct {
		Type      string  `json:"type" jsonschema:"bell, lowcut, highcut, lowcut24, highcut24, ladder, lowshelf, highshelf, notch or bandpass"`
		Frequency float64 `json:"frequency" jsonschema:"in Hz"`
		Gain      float64 `json:"gain,omitempty" jsonschema:"in dB, for bells and shelves"`
		Q         float64 `json:"q,omitempty" jsonschema:"0 or left out is the default of the type"`
		Disabled  bool    `json:"disabled,omitempty"`
	}

	// RemoteBinding binds a parameter of a unit of a module to a parameter of
	// the module.
	RemoteBinding struct {
		Param int  `json:"p" jsonschema:"the parameter of the module, from 1; one more than the module has adds a parameter"`
		Min   *int `json:"min,omitempty" jsonschema:"with max: the module parameter goes from 0 to 128 and is mapped onto min to max of the bound parameter"`
		Max   *int `json:"max,omitempty"`
	}

	// RemoteWhatIf are changes made only to the copy of the song that a
	// render plays.
	RemoteWhatIf struct {
		Edits []RemoteUnitEdit `json:"edits,omitempty" jsonschema:"unit changes, as for edit_units, made only for this render"`
	}

	// RemoteRender is what a render of an instrument needs: see
	// Remote.RenderSource.
	RemoteRender struct {
		Song       sointu.Song // with the module and eq units expanded
		Buffers    map[int]sointu.BufferAudio
		Synther    sointu.Synther
		Instrument int
		Name       string
		FirstVoice int
		NumVoices  int
		Notes      []string // what was changed for the render, to tell
	}

	// unitLoc is where a unit is: in the units of an instrument or of a
	// module, the other being -1.
	unitLoc struct {
		list          *[]sointu.Unit
		index         int
		instr, module int
	}

	// remoteFocus is what a change was about: a unit, by its ID, or else an
	// instrument or a module, by their index, or -1.
	remoteFocus struct {
		unit          int
		instr, module int
	}
)

func (l unitLoc) unit() *sointu.Unit { return &(*l.list)[l.index] }

func (r *Remote) findUnit(id int) (unitLoc, error) {
	m := (*Model)(r)
	if id > 0 {
		for i := range m.d.Song.Patch {
			for j := range m.d.Song.Patch[i].Units {
				if m.d.Song.Patch[i].Units[j].ID == id {
					return unitLoc{&m.d.Song.Patch[i].Units, j, i, -1}, nil
				}
			}
		}
		for i := range m.d.Song.Modules {
			for j := range m.d.Song.Modules[i].Units {
				if m.d.Song.Modules[i].Units[j].ID == id {
					return unitLoc{&m.d.Song.Modules[i].Units, j, -1, i}, nil
				}
			}
		}
	}
	return unitLoc{}, fmt.Errorf("no unit has the ID %d", id)
}

// instrument returns the index of the instrument that ref names: its index,
// from 0, or its name.
func (r *Remote) instrument(ref string) (int, error) {
	patch := r.d.Song.Patch
	ref = strings.TrimSpace(ref)
	if i, err := strconv.Atoi(ref); err == nil {
		if i < 0 || i >= len(patch) {
			return 0, fmt.Errorf("no instrument %d: the song has %d, from 0", i, len(patch))
		}
		return i, nil
	}
	found := -1
	for i := range patch {
		if strings.EqualFold(patch[i].Name, ref) {
			if found >= 0 {
				return 0, fmt.Errorf("several instruments are named %q: use the index", ref)
			}
			found = i
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("no instrument is named %q", ref)
	}
	return found, nil
}

// module returns the index of the module that ref names: its name, or its
// ID.
func (r *Remote) module(ref string) (int, error) {
	mods := r.d.Song.Modules
	ref = strings.TrimSpace(ref)
	for i := range mods {
		if strings.EqualFold(mods[i].Name, ref) {
			return i, nil
		}
	}
	if id, err := strconv.Atoi(ref); err == nil {
		if i, ok := mods.Find(id); ok {
			return i, nil
		}
	}
	return 0, fmt.Errorf("the song has no module %q", ref)
}

// units returns the units that one of the two names: an instrument or a
// module.
func (r *Remote) units(instrument, module string) (unitLoc, error) {
	switch {
	case instrument != "" && module != "":
		return unitLoc{}, errors.New("give an instrument or a module, not both")
	case module != "":
		i, err := r.module(module)
		if err != nil {
			return unitLoc{}, err
		}
		return unitLoc{&r.d.Song.Modules[i].Units, 0, -1, i}, nil
	case instrument != "":
		i, err := r.instrument(instrument)
		if err != nil {
			return unitLoc{}, err
		}
		return unitLoc{&r.d.Song.Patch[i].Units, 0, i, -1}, nil
	}
	return unitLoc{}, errors.New("give an instrument or a module")
}

// unitExcess returns by how many units the instruments exceed what an
// instrument can have, once their module and eq units are expanded.
func (r *Remote) unitExcess() (excess int) {
	for _, instr := range r.d.Song.Patch {
		excess += max(r.d.Song.Modules.NumExpandedUnits(instr.Units)-maxUnits, 0)
	}
	return excess
}

// edit makes a change as one step of the undo history. f changes the song
// and returns what it was about and a summary; if it returns an error, or
// the result is not valid, the song stays as it was.
func (r *Remote) edit(t ChangeType, f func() (remoteFocus, string, error)) (text string, err error) {
	m := (*Model)(r)
	excess := r.unitExcess()
	m.remoteEdits++
	defer r.recoverChange(len(m.undoStack), &err)
	done := m.change("Remote"+strconv.Itoa(m.remoteEdits), t, MajorChange)
	focus, summary, err := f()
	switch {
	case err != nil:
	case m.changeCancel:
		err = errors.New("the tracker refused the change")
	case r.unitExcess() > excess:
		err = fmt.Errorf("an instrument would have more than %d units, counting those that its module and eq units stand for", maxUnits)
	case m.d.Song.BPM <= 0 || m.d.Song.RowsPerBeat <= 0 || m.d.Song.Score.Length <= 0:
		err = errors.New("the song would not be valid")
	}
	if err != nil {
		m.changeCancel = true
		done()
		return "", err
	}
	r.setCursor(focus)
	done()
	m.Alerts().Add("Claude: "+summary, Info)
	return summary + "\n\n" + r.describeFocus(focus), nil
}

// recoverChange is deferred around a change started with the undo history
// at the given length: if the change panics, the song is put back as it was
// and the panic becomes an error, so that a fault in a tool does not take
// the tracker, or the plugin host, down.
func (r *Remote) recoverChange(undoLen int, err *error) {
	p := recover()
	if p == nil {
		return
	}
	m := (*Model)(r)
	if len(m.undoStack) > undoLen && m.changeLevel > 0 {
		m.d = m.undoStack[undoLen]
		m.undoStack = m.undoStack[:undoLen]
	}
	m.changeLevel = 0
	m.updateDeriveData(SongChange)
	*err = fmt.Errorf("the tracker failed, and the change was not made: %v", p)
}

// setCursor moves the cursor of the tracker to what a change was about.
func (r *Remote) setCursor(f remoteFocus) {
	m := (*Model)(r)
	if loc, err := r.findUnit(f.unit); err == nil {
		if loc.instr >= 0 {
			m.d.InstrIndex, m.d.InstrIndex2 = loc.instr, loc.instr
			m.d.InstrumentTab = InstrumentEditorTab
		} else {
			m.d.ModuleIndex = loc.module
			m.d.InstrumentTab = InstrumentModulesTab
		}
		m.leaveModuleUnits()
		m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = loc.index, loc.index, 0
		return
	}
	switch {
	case f.instr >= 0 && f.instr < len(m.d.Song.Patch):
		if m.d.InstrIndex != f.instr || m.editingModule() {
			m.leaveModuleUnits()
			m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
		}
		m.d.InstrIndex, m.d.InstrIndex2 = f.instr, f.instr
		if m.editingModule() {
			m.d.InstrumentTab = InstrumentEditorTab
		}
	case f.module >= 0 && f.module < len(m.d.Song.Modules):
		if m.d.ModuleIndex != f.module || !m.editingModule() {
			m.leaveModuleUnits()
			m.d.UnitIndex, m.d.UnitIndex2, m.d.ParamIndex = 0, 0, 0
		}
		m.d.ModuleIndex = f.module
		m.d.InstrumentTab = InstrumentModulesTab
	}
}

func (r *Remote) describeFocus(f remoteFocus) string {
	if loc, err := r.findUnit(f.unit); err == nil {
		f.instr, f.module = loc.instr, loc.module
	}
	switch {
	case f.instr >= 0 && f.instr < len(r.d.Song.Patch):
		return r.describeInstrument(f.instr, false)
	case f.module >= 0 && f.module < len(r.d.Song.Modules):
		return r.describeModule(f.module, false)
	}
	return r.Song()
}

// Parameters

// remoteParamName returns the name that a remote control sets a parameter
// by: the name it has in the song.
func remoteParamName(p *Parameter) string {
	if p.arg != nil {
		return p.arg.key()
	}
	if _, ok := p.vtable.(*delayTimeParameter); ok {
		return sointu.DelayTimeName(p.index)
	}
	if p.up != nil {
		return p.up.Name
	}
	return p.vtable.Name(p)
}

// remoteLabel returns how the tracker displays a value of a parameter.
func remoteLabel(p *Parameter, v int) string {
	if s, ok := p.vtable.(interface{ StringOf(*Parameter, int) string }); ok {
		return s.StringOf(p, v)
	}
	if _, plain := p.vtable.(*namedParameter); plain && p.up != nil && p.up.DisplayFunc != nil {
		value, _ := p.up.DisplayFunc(v)
		return value
	}
	return ""
}

func remoteInt(name string, value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s: %v is not a whole number", name, v)
		}
		return int(v), nil
	}
	return 0, fmt.Errorf("%s: %v is not a number", name, value)
}

// paramOrder sorts the parameters that decide which others a unit has, and
// what their values mean, before those.
func paramOrder(name string) int {
	return slices.Index([]string{"port", "delaylines", "stereo", "type", "target", "module"}, name)
}

// setParams sets parameters of a unit by their names. newIDs are the IDs of
// the units added in the same call, for send targets given as new:N.
func (r *Remote) setParams(unit *sointu.Unit, params map[string]any, newIDs []int) error {
	m := (*Model)(r)
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if a, b := paramOrder(names[i]), paramOrder(names[j]); a != b {
			return a > b
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		value := params[name]
		text, isText := value.(string)
		switch {
		case unit.Type == "send" && name == "target":
			id := 0
			if n, ok := strings.CutPrefix(text, "new:"); isText && ok {
				i, err := strconv.Atoi(n)
				if err != nil || i < 0 || i >= len(newIDs) {
					return fmt.Errorf("target %q: this call adds %d units, from new:0", text, len(newIDs))
				}
				id = newIDs[i]
			} else {
				var err error
				if id, err = remoteInt(name, value); err != nil {
					return err
				}
				if _, err := r.findUnit(id); err != nil && id != 0 {
					return fmt.Errorf("target: %w", err)
				}
			}
			unit.Parameters["target"] = id
			continue
		case unit.Type == "send" && name == "port":
			port, err := r.sendPort(unit, value)
			if err != nil {
				return err
			}
			unit.Parameters["port"] = port
			continue
		case unit.Type == "module" && name == "module":
			index := -1
			if isText {
				i, err := r.module(text)
				if err != nil {
					return err
				}
				index = i
			} else {
				id, err := remoteInt(name, value)
				if err != nil {
					return err
				}
				i, ok := m.d.Song.Modules.Find(id)
				if !ok {
					return fmt.Errorf("the song has no module with the ID %d", id)
				}
				index = i
			}
			id := m.d.Song.Modules[index].ID
			if inside, ok := m.moduleOf(unit); ok && m.usesModule(id, inside) {
				return fmt.Errorf("the module %q would use itself", m.d.Song.Modules[inside].Name)
			}
			unit.Parameters["module"] = id
			continue
		}
		derived := m.deriveParams(unit, nil)
		var p *Parameter
		for i := range derived {
			if q := &derived[i]; q.vtable != nil && !q.trackerSetting() && q.Type() != NoParameter &&
				(remoteParamName(q) == name || strings.EqualFold(q.Name(), name)) {
				p = q
				break
			}
		}
		if p == nil {
			// a parameter that the tracker does not show now, like the
			// sample of an oscillator of another type
			up, ok := sointu.BindableParam(unit.Type, name)
			if i := slices.IndexFunc(sointu.UnitTypes[unit.Type].Params, func(up sointu.UnitParameter) bool { return up.Name == name }); !ok || i < 0 || !up.CanSet || unit.Type == "module" {
				var valid []string
				for i := range derived {
					if q := &derived[i]; q.vtable != nil && !q.trackerSetting() && q.Type() != NoParameter {
						valid = append(valid, remoteParamName(q))
					}
				}
				return fmt.Errorf("a %s unit has no parameter %q that can be set; it has: %s", unit.Type, name, strings.Join(valid, ", "))
			}
			v, err := remoteInt(name, value)
			if err != nil {
				return err
			}
			if v < up.MinValue || v > up.MaxValue {
				return fmt.Errorf("%s of a %s unit is %d to %d, not %d", name, unit.Type, up.MinValue, up.MaxValue, v)
			}
			unit.Parameters[name] = v
			continue
		}
		rng := p.Range()
		v := 0
		if isText {
			found := false
			for c := rng.Min; c <= rng.Max && c-rng.Min < 1024 && !found; c++ {
				if label := remoteLabel(p, c); label != "" && strings.EqualFold(label, strings.TrimSpace(text)) {
					v, found = c, true
				}
			}
			if !found {
				return fmt.Errorf("%s of a %s unit has no value displayed as %q; it is %d to %d", name, unit.Type, text, rng.Min, rng.Max)
			}
		} else {
			var err error
			if v, err = remoteInt(name, value); err != nil {
				return err
			}
		}
		if v < rng.Min || v > rng.Max {
			return fmt.Errorf("%s of a %s unit is %d to %d, not %d", name, unit.Type, rng.Min, rng.Max, v)
		}
		if v != p.Value() && !p.SetValue(v) {
			return fmt.Errorf("%s of the %s unit %d cannot be set to %d", name, unit.Type, unit.ID, v)
		}
	}
	return nil
}

// sendPort returns the port of the target of a send that value names: the
// name of the parameter that the send modulates, or the number of the port.
func (r *Remote) sendPort(send *sointu.Unit, value any) (int, error) {
	m := (*Model)(r)
	var ports []string
	target := "nothing"
	if loc, err := r.findUnit(send.Parameters["target"]); err == nil {
		t := loc.unit()
		target = "a " + t.Type + " unit"
		if t.Type == "module" {
			if i, ok := m.d.Song.Modules.Find(t.Parameters["module"]); ok {
				for k := range m.d.Song.Modules[i].Params {
					ports = append(ports, sointu.ModuleParamName(k+1))
					if text, ok := value.(string); ok && strings.EqualFold(text, m.d.Song.Modules[i].Params[k].Name) {
						return k, nil
					}
				}
			}
		} else {
			ports = sointu.Ports[t.Type]
		}
	}
	if text, ok := value.(string); ok {
		if i := slices.Index(ports, text); i >= 0 {
			return i, nil
		}
		return 0, fmt.Errorf("port %q: the target of the send, %s, has the ports: %s (set target in the same call, or first)", text, target, strings.Join(ports, ", "))
	}
	port, err := remoteInt("port", value)
	if err != nil {
		return 0, err
	}
	if port < 0 || port > 7 || len(ports) > 0 && port >= len(ports) {
		return 0, fmt.Errorf("port %d: the target of the send, %s, has the ports: %s", port, target, strings.Join(ports, ", "))
	}
	return port, nil
}

func remoteBands(bands []RemoteEQBand) ([]sointu.EQBand, error) {
	if len(bands) > EQMaxBands {
		return nil, fmt.Errorf("an eq unit has at most %d bands", EQMaxBands)
	}
	ret := make([]sointu.EQBand, len(bands))
	for i, b := range bands {
		if !slices.Contains(sointu.EQBandTypes, b.Type) {
			return nil, fmt.Errorf("band %d: no band type %q; the types are: %s", i, b.Type, strings.Join(sointu.EQBandTypes, ", "))
		}
		if b.Frequency <= 0 {
			return nil, fmt.Errorf("band %d: a frequency in Hz is needed", i)
		}
		ret[i] = roundEQ(sointu.EQBand{Type: b.Type, Frequency: b.Frequency, Gain: b.Gain, Q: b.Q, Disabled: b.Disabled})
	}
	return ret, nil
}

// bind binds parameters of a unit of a module to parameters of the module,
// or unbinds them.
func (r *Remote) bind(loc unitLoc, bind map[string]*RemoteBinding) error {
	m := (*Model)(r)
	if len(bind) == 0 {
		return nil
	}
	if loc.module < 0 {
		return errors.New("only the units of a module can bind parameters: the unit is in an instrument")
	}
	names := make([]string, 0, len(bind))
	for name := range bind {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b := bind[name]
		unit := loc.unit()
		if b == nil || b.Param <= 0 {
			delete(unit.Bind, name)
			continue
		}
		up, ok := sointu.BindableParam(unit.Type, name)
		if _, isDelayTime := delayTimeNumber(name); unit.Type == "delay" && isDelayTime {
			ok = true
		}
		if !ok || !sointu.CanBind(unit.Type, name) {
			return fmt.Errorf("%s of a %s unit cannot be bound", name, unit.Type)
		}
		mod := &m.d.Song.Modules[loc.module]
		k := b.Param
		switch {
		case k == len(mod.Params)+1 && k <= sointu.MaxModuleParams:
			mod.Params = append(mod.Params, sointu.ModuleParam{Name: name})
		case k > len(mod.Params):
			return fmt.Errorf("the module %q has %d parameters, of at most %d: p is 1 to %d", mod.Name, len(mod.Params), sointu.MaxModuleParams, min(len(mod.Params)+1, sointu.MaxModuleParams))
		}
		m.bindParam(loc.module, unit, name, k)
		if b.Min != nil || b.Max != nil {
			if b.Min == nil || b.Max == nil {
				return fmt.Errorf("bind %s: give both min and max, or neither", name)
			}
			if unit.Type != "module" && up.MaxValue >= up.MinValue && (*b.Min < up.MinValue || *b.Min > up.MaxValue || *b.Max < up.MinValue || *b.Max > up.MaxValue) {
				return fmt.Errorf("bind %s: min and max are within %d to %d", name, up.MinValue, up.MaxValue)
			}
			unit.Bind[name] = sointu.Binding{Param: k, Scaled: true, Min: *b.Min, Max: *b.Max}
			m.setModuleDefault(loc.module, k, mod.Params[k-1].Default)
		}
	}
	return nil
}

// delayTimeNumber returns i for the name of delay time i, from 0.
func delayTimeNumber(name string) (int, bool) {
	n, ok := strings.CutPrefix(name, "delaytime")
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(n)
	return i - 1, err == nil && i >= 1
}

// editUnit makes the changes of e to the unit it names.
func (r *Remote) editUnit(e RemoteUnitEdit, newIDs []int) error {
	loc, err := r.findUnit(e.Unit)
	if err != nil {
		return err
	}
	unit := loc.unit()
	if unit.Parameters == nil {
		unit.Parameters = sointu.ParamMap{}
	}
	if e.Bands != nil {
		if unit.Type != "eq" {
			return fmt.Errorf("unit %d is a %s unit: only eq units have bands", e.Unit, unit.Type)
		}
		bands, err := remoteBands(*e.Bands)
		if err != nil {
			return err
		}
		unit.Bands = bands
	}
	if err := r.setParams(unit, e.Params, newIDs); err != nil {
		return fmt.Errorf("unit %d: %w", e.Unit, err)
	}
	if e.Disabled != nil {
		unit.Disabled = *e.Disabled
	}
	if e.Comment != nil {
		unit.Comment = *e.Comment
	}
	if err := r.bind(loc, e.Bind); err != nil {
		return fmt.Errorf("unit %d: %w", e.Unit, err)
	}
	return nil
}

// Units

// EditUnits changes units: their parameters, whether they are disabled,
// their comments, the bands of eq units and the bindings of units of
// modules.
func (r *Remote) EditUnits(edits []RemoteUnitEdit) (string, error) {
	if len(edits) == 0 {
		return "", errors.New("no edits given")
	}
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		for _, e := range edits {
			if err := r.editUnit(e, nil); err != nil {
				return remoteFocus{}, "", err
			}
		}
		last := edits[len(edits)-1].Unit
		summary := fmt.Sprintf("changed %d units", len(edits))
		if len(edits) == 1 {
			summary = "changed " + r.unitName(last)
		}
		return remoteFocus{unit: last, instr: -1, module: -1}, summary, nil
	})
}

// unitName names a unit for a summary: its type, its ID and where it is.
func (r *Remote) unitName(id int) string {
	loc, err := r.findUnit(id)
	if err != nil {
		return fmt.Sprintf("unit %d", id)
	}
	return fmt.Sprintf("%s #%d of %s", loc.unit().Type, id, r.scopeName(loc))
}

func (r *Remote) scopeName(loc unitLoc) string {
	if loc.module >= 0 {
		return fmt.Sprintf("module %q", r.d.Song.Modules[loc.module].Name)
	}
	return fmt.Sprintf("instrument %d %q", loc.instr, r.d.Song.Patch[loc.instr].Name)
}

// place returns where units go: after or before the unit with the given ID,
// or with neither, at the end of the units of the instrument or the module.
func (r *Remote) place(instrument, module string, after, before int) (unitLoc, error) {
	switch {
	case after != 0 && before != 0:
		return unitLoc{}, errors.New("give after or before, not both")
	case after != 0:
		loc, err := r.findUnit(after)
		loc.index++
		return loc, err
	case before != 0:
		return r.findUnit(before)
	}
	loc, err := r.units(instrument, module)
	if err != nil {
		return loc, fmt.Errorf("where to: give after or before (a unit ID), or an instrument or a module to add at the end of: %w", err)
	}
	loc.index = len(*loc.list)
	return loc, nil
}

// AddUnits adds units after or before the unit with the given ID, or at the
// end of the units of an instrument or a module.
func (r *Remote) AddUnits(instrument, module string, after, before int, units []RemoteNewUnit) (string, error) {
	if len(units) == 0 {
		return "", errors.New("no units given")
	}
	m := (*Model)(r)
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		loc, err := r.place(instrument, module, after, before)
		if err != nil {
			return remoteFocus{}, "", err
		}
		added := make([]sointu.Unit, len(units))
		for i, n := range units {
			if _, ok := sointu.UnitTypes[n.Type]; !ok {
				return remoteFocus{}, "", fmt.Errorf("no unit type %q: see the unit_types tool", n.Type)
			}
			u := sointu.MakeUnit(n.Type)
			// like a unit added in the tracker: a spectral unit reads the
			// spectrum written last before it, an mc unit uses the bus of
			// the one before it, and writers get new ones when the change
			// is done
			for j, name := range sointu.SpectrumBufferParams(u.Type) {
				if j > 0 || !sointu.WritesSpectrum(u.Type) {
					u.Parameters[name] = m.defaultSpectrumBuffer(*loc.list, loc.index)
				}
			}
			for _, name := range sointu.BusParams(u.Type) {
				if !sointu.WritesBus(u.Type) {
					u.Parameters[name] = m.defaultBus(*loc.list, loc.index)
				}
			}
			u.Disabled, u.Comment = n.Disabled, n.Comment
			added[i] = u
		}
		m.assignUnitIDs(added)
		ids := make([]int, len(added))
		for i := range added {
			ids[i] = added[i].ID
		}
		list, ok := Insert(*loc.list, loc.index, added...)
		if !ok {
			return remoteFocus{}, "", errors.New("could not insert the units")
		}
		*loc.list = list
		for i, n := range units {
			var bands *[]RemoteEQBand
			if n.Bands != nil {
				bands = &n.Bands
			}
			if n.Type == "module" && n.Params["module"] == nil {
				return remoteFocus{}, "", errors.New("a module unit needs the parameter module: the name of a module of the song (see add_module)")
			}
			if err := r.editUnit(RemoteUnitEdit{Unit: ids[i], Params: n.Params, Bands: bands, Bind: n.Bind}, ids); err != nil {
				return remoteFocus{}, "", fmt.Errorf("unit new:%d (%s): %w", i, n.Type, err)
			}
		}
		types := make([]string, len(units))
		for i := range units {
			types[i] = fmt.Sprintf("%s #%d", units[i].Type, ids[i])
		}
		return remoteFocus{unit: ids[len(ids)-1], instr: -1, module: -1}, fmt.Sprintf("added %s to %s", strings.Join(types, ", "), r.scopeName(loc)), nil
	})
}

// DeleteUnits deletes the units with the given IDs.
func (r *Remote) DeleteUnits(ids []int) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("no units given")
	}
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		var names []string
		for _, id := range ids {
			loc, err := r.findUnit(id)
			if err != nil {
				return focus, "", err
			}
			if loc.instr >= 0 && len(*loc.list) == 1 {
				return focus, "", fmt.Errorf("unit %d is the last unit of %s: an instrument keeps at least one unit (delete the instrument instead)", id, r.scopeName(loc))
			}
			names = append(names, r.unitName(id))
			focus.instr, focus.module = loc.instr, loc.module
			*loc.list = slices.Delete(*loc.list, loc.index, loc.index+1)
			if n := len(*loc.list); n > 0 {
				focus.unit = (*loc.list)[min(loc.index, n-1)].ID
			}
		}
		return focus, "deleted " + strings.Join(names, ", "), nil
	})
}

// MoveUnits moves the units with the given IDs, in the order given, to
// after or before another unit, or to the end of the units of an instrument
// or a module. They keep their IDs, so sends to them still reach them.
func (r *Remote) MoveUnits(ids []int, instrument, module string, after, before int) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("no units given")
	}
	if slices.Contains(ids, after) || slices.Contains(ids, before) {
		return "", errors.New("after or before is one of the units to move")
	}
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{unit: ids[len(ids)-1], instr: -1, module: -1}
		moved := make([]sointu.Unit, len(ids))
		for i, id := range ids {
			loc, err := r.findUnit(id)
			if err != nil {
				return focus, "", err
			}
			moved[i] = loc.unit().Copy()
			*loc.list = slices.Delete(*loc.list, loc.index, loc.index+1)
		}
		loc, err := r.place(instrument, module, after, before)
		if err != nil {
			return focus, "", err
		}
		list, ok := Insert(*loc.list, loc.index, moved...)
		if !ok {
			return focus, "", errors.New("could not insert the units")
		}
		*loc.list = list
		for i := range r.d.Song.Patch {
			if len(r.d.Song.Patch[i].Units) == 0 {
				return focus, "", fmt.Errorf("instrument %d %q would have no units left", i, r.d.Song.Patch[i].Name)
			}
		}
		return focus, fmt.Sprintf("moved %d units to %s", len(ids), r.scopeName(loc)), nil
	})
}

// Instruments

// findPreset returns the instrument preset with the given name, or
// directory/name.
func (r *Remote) findPreset(name string) (preset, error) {
	name = strings.TrimSpace(name)
	var found []preset
	for _, p := range r.presetData.presets {
		if strings.EqualFold(p.instr.Name, name) || strings.EqualFold(p.dir+"/"+p.instr.Name, name) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return preset{}, fmt.Errorf("no instrument preset %q: see the presets tool", name)
	case 1:
		return found[0], nil
	}
	dirs := make([]string, len(found))
	for i, p := range found {
		dirs[i] = p.dir + "/" + p.instr.Name
	}
	return preset{}, fmt.Errorf("several presets are named %q: %s", name, strings.Join(dirs, ", "))
}

// loadPreset gives an instrument the units, the name and the comment of a
// preset, like choosing it in the tracker.
func (r *Remote) loadPreset(instr *sointu.Instrument, name string) error {
	m := (*Model)(r)
	p, err := r.findPreset(name)
	if err != nil {
		return err
	}
	loaded := p.instr.Copy()
	m.importModules(p.modules, loaded.Units)
	m.assignUnitIDs(loaded.Units)
	m.assignBuses(loaded.Units)
	instr.Name, instr.Comment, instr.Units = loaded.Name, loaded.Comment, loaded.Units
	return nil
}

// AddInstrument adds an instrument after the last one: the default
// instrument of the tracker, or a preset.
func (r *Remote) AddInstrument(name, presetName string, voices int) (string, error) {
	m := (*Model)(r)
	return r.edit(SongChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		instr := defaultInstrument.Copy()
		for i := range instr.Units {
			instr.Units[i].ID = 0 // new IDs, also for a second one
		}
		if presetName != "" {
			if err := r.loadPreset(&instr, presetName); err != nil {
				return focus, "", err
			}
		}
		if name != "" {
			instr.Name = name
		}
		instr.NumVoices = max(voices, 1)
		if instr.NumVoices > m.remainingVoices(true, m.linkInstrTrack) {
			return focus, "", fmt.Errorf("the song has room for %d more voices", m.remainingVoices(true, m.linkInstrTrack))
		}
		instrRange, _, ok := m.addVoices(m.d.Song.Patch.NumVoices(), sointu.Patch{instr}, []sointu.Track{{NumVoices: instr.NumVoices}}, true, m.linkInstrTrack)
		if !ok {
			return focus, "", errors.New("the tracker could not add the instrument")
		}
		focus.instr = instrRange.Start
		return focus, fmt.Sprintf("added instrument %d %q", focus.instr, instr.Name), nil
	})
}

// DeleteInstrument deletes an instrument.
func (r *Remote) DeleteInstrument(ref string) (string, error) {
	m := (*Model)(r)
	return r.edit(SongChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		i, err := r.instrument(ref)
		if err != nil {
			return focus, "", err
		}
		name := m.d.Song.Patch[i].Name
		if !(*instrumentList)(m).Delete(Range{i, i + 1}) {
			return focus, "", errors.New("the tracker could not delete the instrument")
		}
		focus.instr = min(i, len(m.d.Song.Patch)-1)
		return focus, fmt.Sprintf("deleted instrument %d %q", i, name), nil
	})
}

// EditInstrument changes an instrument: its name, its comment, its number
// of voices, whether it is muted, and with a preset, its units.
func (r *Remote) EditInstrument(ref string, name, comment *string, voices *int, mute *bool, solo *bool, presetName string) (string, error) {
	m := (*Model)(r)
	return r.edit(SongChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		i, err := r.instrument(ref)
		if err != nil {
			return focus, "", err
		}
		focus.instr = i
		instr := &m.d.Song.Patch[i]
		if presetName != "" {
			if err := r.loadPreset(instr, presetName); err != nil {
				return focus, "", err
			}
		}
		if name != nil {
			instr.Name = *name
		}
		if comment != nil {
			instr.Comment = *comment
		}
		if mute != nil {
			instr.Mute = *mute
		}
		if solo != nil {
			for j := range m.d.Song.Patch {
				m.d.Song.Patch[j].Mute = *solo && j != i
			}
		}
		if voices != nil && *voices != instr.NumVoices {
			m.d.InstrIndex, m.d.InstrIndex2 = i, i
			v := m.Instrument().Voices()
			if rng := v.Range(); *voices < rng.Min || *voices > rng.Max {
				return focus, "", fmt.Errorf("the instrument can have %d to %d voices", rng.Min, rng.Max)
			}
			if !v.SetValue(*voices) {
				return focus, "", errors.New("the tracker could not change the number of voices")
			}
		}
		return focus, fmt.Sprintf("changed instrument %d %q", i, m.d.Song.Patch[i].Name), nil
	})
}

// Modules

// AddModule adds a module to the song: a module preset, with the modules
// that it uses, or an empty module.
func (r *Remote) AddModule(name, presetName string, inputs int) (string, error) {
	m := (*Model)(r)
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		if presetName != "" {
			i := slices.IndexFunc(m.modulePresets, func(p modulePreset) bool { return strings.EqualFold(p.name, strings.TrimSpace(presetName)) })
			if i < 0 {
				return focus, "", fmt.Errorf("no module preset %q: see the presets tool", presetName)
			}
			mods := m.modulePresets[i].modules
			ids := m.importModules(mods)
			index, ok := m.d.Song.Modules.Find(ids[mods[len(mods)-1].ID])
			if !ok {
				return focus, "", errors.New("the tracker could not add the module preset")
			}
			focus.module = index
			if name != "" {
				m.d.Song.Modules[index].Name = name
			}
			return focus, fmt.Sprintf("added the module %q", m.d.Song.Modules[index].Name), nil
		}
		if inputs < 0 || inputs > 8 {
			return focus, "", errors.New("a module has 0 to 8 inputs")
		}
		m.d.ModuleIndex = len(m.d.Song.Modules) - 1
		index := m.addModule()
		mod := &m.d.Song.Modules[index]
		if name != "" {
			mod.Name = ""
			mod.Name = m.newModuleName(name)
		}
		mod.Inputs = inputs
		focus.module = index
		return focus, fmt.Sprintf("added the module %q", mod.Name), nil
	})
}

// RemoteModuleParam sets a parameter of a module.
type RemoteModuleParam struct {
	Name    *string `json:"name,omitempty"`
	Default *int    `json:"default,omitempty" jsonschema:"the value of new module units, within the range of the parameter bound first"`
	Display *string `json:"display,omitempty" jsonschema:"displays the values like a parameter of a unit type, as type.parameter, e.g. filter.frequency"`
}

// EditModule changes a module: its name, its comment, the number of its
// inputs and its parameters. params are its parameters from the first:
// those beyond what it has are added. deleteParams are the numbers, from 1,
// of parameters to delete after that.
func (r *Remote) EditModule(ref string, name, comment *string, inputs *int, params []RemoteModuleParam, deleteParams []int) (string, error) {
	m := (*Model)(r)
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		index, err := r.module(ref)
		if err != nil {
			return focus, "", err
		}
		focus.module = index
		mod := &m.d.Song.Modules[index]
		if name != nil && *name != mod.Name {
			mod.Name = ""
			mod.Name = m.newModuleName(*name)
		}
		if comment != nil {
			mod.Comment = *comment
		}
		if inputs != nil {
			if *inputs < 0 || *inputs > 8 {
				return focus, "", errors.New("a module has 0 to 8 inputs")
			}
			mod.Inputs = *inputs
		}
		if len(params) > sointu.MaxModuleParams {
			return focus, "", fmt.Errorf("a module has at most %d parameters", sointu.MaxModuleParams)
		}
		for k, p := range params {
			if k >= len(mod.Params) {
				mod.Params = append(mod.Params, sointu.ModuleParam{Name: sointu.ModuleParamName(k + 1)})
			}
			if p.Name != nil {
				mod.Params[k].Name = *p.Name
			}
			if p.Display != nil {
				mod.Params[k].Display = *p.Display
			}
			if p.Default != nil {
				if up, ok := m.d.Song.Modules.Param(index, k+1); ok && up.MaxValue >= up.MinValue && (*p.Default < up.MinValue || *p.Default > up.MaxValue) {
					return focus, "", fmt.Errorf("parameter %d of the module is %d to %d, not %d", k+1, up.MinValue, up.MaxValue, *p.Default)
				}
				m.setModuleDefault(index, k+1, *p.Default)
			}
		}
		deleteParams = slices.Clone(deleteParams)
		sort.Sort(sort.Reverse(sort.IntSlice(deleteParams)))
		for _, k := range slices.Compact(deleteParams) {
			m.d.ModuleIndex = index
			if a := m.Module().DeleteParam(k); a.Enabled() {
				a.Do()
			} else {
				return focus, "", fmt.Errorf("the module has no parameter %d", k)
			}
		}
		return focus, fmt.Sprintf("changed the module %q", m.d.Song.Modules[index].Name), nil
	})
}

// DeleteModule deletes a module. The module units using it are left without
// a module.
func (r *Remote) DeleteModule(ref string) (string, error) {
	m := (*Model)(r)
	return r.edit(PatchChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		index, err := r.module(ref)
		if err != nil {
			return focus, "", err
		}
		name, uses := m.d.Song.Modules[index].Name, m.moduleUses(m.d.Song.Modules[index].ID)
		(*moduleList)(m).Delete(Range{index, index + 1})
		summary := fmt.Sprintf("deleted the module %q", name)
		if uses > 0 {
			summary += fmt.Sprintf("; %d module units used it and now have no module", uses)
		}
		return focus, summary, nil
	})
}

// Song

// SetBPM sets the tempo of the song. In a plugin the song follows the tempo
// of the host again when that changes.
func (r *Remote) SetBPM(bpm int) (string, error) {
	return r.edit(BPMChange, func() (remoteFocus, string, error) {
		if bpm < 1 || bpm > 999 {
			return remoteFocus{}, "", errors.New("the tempo is 1 to 999 BPM")
		}
		r.d.Song.BPM = bpm
		return remoteFocus{instr: -1, module: -1}, fmt.Sprintf("set the tempo to %d BPM", bpm), nil
	})
}

// Undo undoes the last steps of the undo history, also those made in the
// tracker by hand, and Redo redoes them.
func (r *Remote) Undo(steps int) (string, error) { return r.history(steps, false) }
func (r *Remote) Redo(steps int) (string, error) { return r.history(steps, true) }

func (r *Remote) history(steps int, redo bool) (string, error) {
	m := (*Model)(r)
	action, word, past := m.History().Undo(), "undo", "undid"
	if redo {
		action, word, past = m.History().Redo(), "redo", "redid"
	}
	done := 0
	for ; done < max(steps, 1) && action.Enabled(); done++ {
		action.Do()
	}
	if done == 0 {
		return "", errors.New("nothing to " + word)
	}
	summary := fmt.Sprintf("%s %d steps", past, done)
	m.Alerts().Add("Claude: "+summary, Info)
	return summary + "\n\n" + r.Song(), nil
}

// Rendering and playing

// RenderSource returns what rendering notes of an instrument needs: a copy
// of the song as the synths play it, with the changes of whatIf made to the
// copy only. With dry, only what the instrument itself puts out is heard:
// the outputs of the other instruments are silenced, and if the instrument
// only writes to aux channels, those go to the output.
func (r *Remote) RenderSource(ref string, dry bool, whatIf *RemoteWhatIf) (ret RemoteRender, err error) {
	m := (*Model)(r)
	index, err := r.instrument(ref)
	if err != nil {
		return RemoteRender{}, err
	}
	var song sointu.Song
	defer r.recoverChange(len(m.undoStack), &err)
	// the changes are made like any others, and cancelled: the model, the
	// undo history and the player never see them
	done := m.change("RemoteWhatIf", PatchChange, MajorChange)
	if whatIf != nil {
		for _, e := range whatIf.Edits {
			if err = r.editUnit(e, nil); err != nil {
				break
			}
		}
	}
	if err == nil {
		m.fixIDCollisions()
		m.fixUnitParams()
		m.fixModules()
		m.fixSpectrumBuffers()
		m.fixBuses()
		known, _ := m.d.Song.WithoutUnknownUnits()
		expanded, expansion := known.Expand()
		if len(expansion.Problems) > 0 {
			err = fmt.Errorf("the modules cannot be expanded: %w", errors.Join(expansion.Problems...))
		}
		song = expanded.Copy()
	}
	m.changeCancel = true
	done()
	if err != nil {
		return RemoteRender{}, err
	}
	ret = RemoteRender{
		Song:       song,
		Buffers:    m.BufferAudio(),
		Synther:    vm.GoSynther{},
		Instrument: index,
		Name:       song.Patch[index].Name,
		FirstVoice: song.Patch.FirstVoiceForInstrument(index),
		NumVoices:  max(song.Patch[index].NumVoices, 1),
	}
	if dry {
		ret.Notes = dryRouting(song.Patch, index)
	}
	return ret, nil
}

// dryRouting changes a patch so that only what one instrument itself puts
// out is heard, and returns what it did.
func dryRouting(patch sointu.Patch, index int) (notes []string) {
	main := func(u *sointu.Unit) bool {
		return !u.Disabled && (u.Type == "out" || u.Type == "outaux" || u.Type == "aux" && u.Parameters["channel"] < 2)
	}
	for i := range patch {
		if i == index {
			continue
		}
		for j := range patch[i].Units {
			if u := &patch[i].Units[j]; main(u) {
				if u.Type == "outaux" {
					u.Parameters["outgain"] = 0
				} else {
					u.Parameters["gain"] = 0
				}
			}
		}
	}
	units := patch[index].Units
	if slices.ContainsFunc(units, func(u sointu.Unit) bool { return main(&u) }) {
		return nil
	}
	moved := 0
	for j := range units {
		if u := &units[j]; u.Type == "aux" && !u.Disabled {
			u.Parameters["channel"] = 0
			moved++
		}
	}
	if moved > 0 {
		return []string{"the instrument has no out or outaux unit: its aux units were sent to the main output for this dry render"}
	}
	return []string{"the instrument has no out, outaux or aux unit: nothing reaches the output (use output: master if it sends to another instrument)"}
}

// remoteNotes is the source of the note events of PlayNote.
var remoteNotes = new(struct{ _ int })

// PlayNote plays a note of an instrument in the running synth, so that the
// user hears it, for the given time.
func (r *Remote) PlayNote(ref string, note int, duration time.Duration) (string, error) {
	m := (*Model)(r)
	index, err := r.instrument(ref)
	if err != nil {
		return "", err
	}
	if note < 2 || note > 255 {
		return "", errors.New("a note is 2 to 255: 60 is C4")
	}
	duration = min(max(duration, 10*time.Millisecond), 30*time.Second)
	frames := func() int64 { return time.Now().UnixMicro() * 441 / 10000 } // the clock of the source, in frames
	broker := m.broker
	if !TrySend(broker.ToPlayer, any(&NoteEvent{Timestamp: frames(), Channel: index, Note: byte(note), On: true, Source: remoteNotes})) {
		return "", errors.New("the player is busy")
	}
	time.AfterFunc(duration, func() {
		TrySend(broker.ToPlayer, any(&NoteEvent{Timestamp: frames(), Channel: index, Note: byte(note), On: false, Source: remoteNotes}))
	})
	summary := fmt.Sprintf("playing note %d on instrument %d %q for %.2f s", note, index, m.d.Song.Patch[index].Name, duration.Seconds())
	if m.d.Song.Patch[index].Mute {
		summary += " (the instrument is muted: nothing will be heard)"
	}
	return summary, nil
}
