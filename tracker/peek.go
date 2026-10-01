package tracker

import "github.com/vsariola/sointu"

// Peek returns the Peek view of the model: the units that the selected
// module unit stands for, to show them read-only under the units being
// edited. For a module unit of an instrument, they are the units that the
// synth runs for it, with the values that the module unit gives the bound
// parameters and with the units of the modules that its module uses. For a
// module unit of a module, they are the units of its module as they are.
func (m *Model) Peek() *PeekModel { return (*PeekModel)(m) }

type (
	PeekModel Model

	// PeekUnits and PeekParams are the units and the parameters of the
	// Peek view, like UnitModel and ParamModel are of the units being
	// edited.
	PeekUnits  Model
	PeekParams Model

	// peekData is the units that a module unit stands for, and what is
	// derived from them. It is made again when the module unit or the
	// expansion it was made for is another one.
	peekData struct {
		expansion   *sointu.Expansion
		call        *sointu.Unit
		module      int // index of the module of the module unit
		units       []sointu.Unit
		source      []sointu.ExpandedUnit // where each unit came from; zero if it is a unit of the module as it is
		params      [][]Parameter
		rails       []Rail
		railWidth   int
		paramsWidth int
		cursor      Point
		cursor2     Point
	}
)

// Units and Params return the units and the parameters of the Peek view.
func (m *PeekModel) Units() *PeekUnits   { return (*PeekUnits)(m) }
func (m *PeekModel) Params() *PeekParams { return (*PeekParams)(m) }

// Show returns a Bool telling whether the units that the selected module
// unit stands for are shown.
func (m *PeekModel) Show() Bool { return MakeBool((*peekShow)(m)) }

type peekShow PeekModel

func (m *peekShow) Value() bool       { return !m.peekHidden }
func (m *peekShow) SetValue(val bool) { m.peekHidden = !val }

// Visible reports whether there are units to show: the selected unit is a
// module unit with a module, and Show is on.
func (m *PeekModel) Visible() bool { return (*Model)(m).peekData() != nil }

// Title returns the name of the module of the selected module unit.
func (m *PeekModel) Title() string {
	if d := (*Model)(m).peekData(); d != nil {
		return moduleTitle(&m.d.Song.Modules[d.module])
	}
	return ""
}

// peekData returns the units that the selected module unit stands for, or
// nil if there are none to show.
func (m *Model) peekData() *peekData {
	if m.peekHidden {
		return nil
	}
	call, index, ok := m.selectedModuleUnit()
	if !ok {
		return nil
	}
	d := &m.peeked
	if d.call == call && d.expansion == m.expansion && d.module == index && d.units != nil {
		return d
	}
	*d = peekData{expansion: m.expansion, call: call, module: index, units: []sointu.Unit{}, cursor: d.cursor, cursor2: d.cursor2}
	if !m.editingModule() && m.expansion != nil && m.d.InstrIndex < len(m.expanded) {
		// the units of the synth that came from the module unit
		for _, u := range m.expanded[m.d.InstrIndex].Units {
			if e, ok := m.expansion.Units[u.ID]; ok && e.Call == call.ID && e.Instrument == m.d.InstrIndex && call.ID != 0 {
				d.units = append(d.units, u)
				d.source = append(d.source, e)
			}
		}
	} else {
		for _, u := range m.d.Song.Modules[index].Units {
			d.units = append(d.units, u.Copy())
			d.source = append(d.source, sointu.ExpandedUnit{})
		}
	}
	d.params = make([][]Parameter, len(d.units))
	d.rails = make([]Rail, len(d.units))
	depth := max(m.d.Song.Modules[index].Inputs, 0)
	d.railWidth = depth
	previews := false
	for i := range d.units {
		u := &d.units[i]
		d.params[i] = m.deriveParams(u, nil)
		for j := range d.params[i] {
			d.params[i][j].peek = i + 1
		}
		d.paramsWidth = max(d.paramsWidth, len(d.params[i]))
		_, _, ok := unitBuffer(u)
		previews = previews || ok
		use := m.d.Song.Modules.StackUse(u)
		depth = max(depth-len(use.Inputs), 0)
		d.rails[i] = Rail{PassThrough: depth, StackUse: use, Send: !u.Disabled && u.Type == "send"}
		d.railWidth = max(d.railWidth, depth+max(len(use.Inputs), use.NumOutputs))
		depth += use.NumOutputs
	}
	if previews {
		d.paramsWidth += UnitPreviewCells
	}
	return d
}

// peekBound returns the name of the parameter of a module that the
// parameter of unit i of the Peek view is bound to.
func (m *Model) peekBound(i int, param string) (string, bool) {
	d := &m.peeked
	if i < 0 || i >= len(d.units) {
		return "", false
	}
	module, unit := d.module, &d.units[i]
	if e := d.source[i]; e.Body != 0 {
		// a unit of the synth: the bindings are those of the unit of the
		// module that it came from
		var ok bool
		if module, ok = m.d.Song.Modules.Find(e.Module); !ok {
			return "", false
		}
		unit = nil
		for j := range m.d.Song.Modules[module].Units {
			if u := &m.d.Song.Modules[module].Units[j]; u.ID == e.Body {
				unit = u
			}
		}
		if unit == nil {
			return "", false
		}
	}
	k, ok := unit.Bind[param]
	if !ok {
		return "", false
	}
	if mp, ok := m.d.Song.Modules.Param(module, k); ok {
		return mp.Name, true
	}
	return sointu.ModuleParamName(k), true
}

// Item returns information about unit i of the Peek view.
func (m *PeekUnits) Item(i int) UnitListItem {
	d := (*Model)(m).peekData()
	if d == nil || i < 0 || i >= len(d.units) {
		return UnitListItem{}
	}
	u := &d.units[i]
	return UnitListItem{Type: u.Type, Title: (*Model)(m).unitTitle(u), Comment: u.Comment, Disabled: u.Disabled, Signals: d.rails[i]}
}

func (m *PeekUnits) RailWidth() int {
	if d := (*Model)(m).peekData(); d != nil {
		return d.railWidth
	}
	return 0
}

// Buffer, Bus, Spectrum and Levels are like those of UnitModel: the buffer
// or bus of unit i of the Peek view, and what the synth last had in them.
// Only the units of a module unit of an instrument are in the synth.
func (m *PeekUnits) Buffer(i int) (id int, spectrum, ok bool) {
	d := (*Model)(m).peekData()
	if d == nil || i < 0 || i >= len(d.units) {
		return 0, false, false
	}
	return unitBuffer(&d.units[i])
}

func (m *PeekUnits) Bus(i int) (id int, ok bool) {
	d := (*Model)(m).peekData()
	if d == nil || i < 0 || i >= len(d.units) || sointu.BusParams(d.units[i].Type) == nil {
		return 0, false
	}
	return d.units[i].Parameters["bus"], true
}

func (m *PeekUnits) playedID(i int) int {
	d := (*Model)(m).peekData()
	if d == nil || i < 0 || i >= len(d.units) || d.source[i].Body == 0 {
		return 0
	}
	return d.units[i].ID
}

func (m *PeekUnits) Spectrum(i int) ([]float32, int) {
	id := m.playedID(i)
	if id == 0 {
		return nil, 0
	}
	return (*Model)(m).spectrumOf(SpectrumSource{Unit: id})
}

func (m *PeekUnits) Levels(i int) []float32 {
	id := m.playedID(i)
	if id == 0 {
		return nil
	}
	mags, _ := (*Model)(m).spectrumOf(SpectrumSource{Unit: id, Levels: true})
	return mags
}

// List returns the units of the Peek view as a List. They cannot be changed.
func (m *PeekUnits) List() List { return List{(*peekUnitList)(m)} }

type peekUnitList PeekUnits

func (v *peekUnitList) Count() int {
	if d := (*Model)(v).peekData(); d != nil {
		return len(d.units)
	}
	return 0
}
func (v *peekUnitList) Selected() int          { return v.peeked.cursor.Y }
func (v *peekUnitList) Selected2() int         { return v.peeked.cursor2.Y }
func (v *peekUnitList) SetSelected(value int)  { v.peeked.cursor.Y = value }
func (v *peekUnitList) SetSelected2(value int) { v.peeked.cursor2.Y = value }

// Columns returns the columns of the parameters of the Peek view as a List.
func (m *PeekParams) Columns() List { return List{(*peekColumns)(m)} }

type peekColumns PeekParams

func (v *peekColumns) Count() int             { return (*PeekParams)(v).Width() }
func (v *peekColumns) Selected() int          { return v.peeked.cursor.X }
func (v *peekColumns) Selected2() int         { return v.peeked.cursor.X }
func (v *peekColumns) SetSelected(value int)  { v.peeked.cursor.X = value }
func (v *peekColumns) SetSelected2(value int) {}

// Table returns the parameters of the Peek view as a Table. It has a cursor
// of its own, and its parameters cannot be changed.
func (m *PeekParams) Table() Table { return Table{m} }

func (m *PeekParams) Cursor() Point  { return m.clamp(m.peeked.cursor) }
func (m *PeekParams) Cursor2() Point { return m.clamp(m.peeked.cursor2) }
func (m *PeekParams) clamp(p Point) Point {
	return Point{max(min(p.X, m.Width()-1), 0), max(min(p.Y, m.Height()-1), 0)}
}
func (m *PeekParams) SetCursor(p Point) {
	m.peeked.cursor = m.clamp(p)
}
func (m *PeekParams) SetCursor2(p Point) {
	m.peeked.cursor2 = m.clamp(p)
}
func (m *PeekParams) Width() int {
	if d := (*Model)(m).peekData(); d != nil {
		return d.paramsWidth + 1 // like ParamModel.Width
	}
	return 0
}
func (m *PeekParams) Height() int {
	if d := (*Model)(m).peekData(); d != nil {
		return len(d.units)
	}
	return 0
}
func (m *PeekParams) RowWidth(y int) int {
	if d := (*Model)(m).peekData(); d != nil && y >= 0 && y < len(d.params) {
		return len(d.params[y])
	}
	return 0
}
func (m *PeekParams) MoveCursor(dx, dy int) (ok bool) {
	p := m.Cursor()
	p.X += dx
	p.Y += dy
	m.SetCursor(p)
	return p == m.Cursor()
}

// Item returns the parameter at the given point of the Peek view. It cannot
// be changed.
func (m *PeekParams) Item(p Point) Parameter {
	d := (*Model)(m).peekData()
	if d == nil || p.Y < 0 || p.Y >= len(d.params) || p.X < 0 || p.X >= len(d.params[p.Y]) {
		return Parameter{}
	}
	return d.params[p.Y][p.X]
}

// the parameters of the Peek view cannot be changed
func (m *PeekParams) clear(p Point)                                 {}
func (m *PeekParams) set(p Point, value int)                        {}
func (m *PeekParams) add(rect Rect, delta int, largeStep bool) bool { return false }
func (m *PeekParams) marshal(rect Rect) (data []byte, ok bool)      { return nil, false }
func (m *PeekParams) unmarshalAtCursor(data []byte) bool            { return false }
func (m *PeekParams) unmarshalRange(rect Rect, data []byte) bool    { return false }
func (m *PeekParams) change(kind string, severity ChangeSeverity) func() {
	return func() {}
}
func (m *PeekParams) cancel() {}
