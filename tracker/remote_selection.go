package tracker

import (
	"fmt"
	"strconv"
	"strings"
)

var tabNames = [NumInstrumentTabs]string{"Editor", "Presets", "Comment", "Buffers", "Modules"}

// Selection tells what the user has open and selected in the tracker: the
// tab of the instrument panel, the instrument, the module on the Modules
// tab, the buffer on the Buffers tab, the units and the parameter under the
// cursor of the unit editor. With full, also whether the song plays, and the
// cursor in the score.
func (r *Remote) Selection(full bool) string {
	m := (*Model)(r)
	d := &m.d
	parts := []string{}
	tab := "Editor"
	if t := int(d.InstrumentTab); t >= 0 && t < len(tabNames) {
		tab = tabNames[t]
	}
	parts = append(parts, "tab "+tab)
	if i := d.InstrIndex; i >= 0 && i < len(d.Song.Patch) {
		s := fmt.Sprintf("instrument %d %q", i, d.Song.Patch[i].Name)
		if j := d.InstrIndex2; j != i && j >= 0 && j < len(d.Song.Patch) {
			s += fmt.Sprintf(" (instruments %d to %d selected)", min(i, j), max(i, j))
		}
		parts = append(parts, s)
	}
	switch d.InstrumentTab {
	case InstrumentModulesTab:
		if i := d.ModuleIndex; i >= 0 && i < len(d.Song.Modules) {
			parts = append(parts, fmt.Sprintf("module %q", d.Song.Modules[i].Name))
		}
	case InstrumentBuffersTab:
		if buf := m.Buffer().selected(); buf != nil {
			parts = append(parts, fmt.Sprintf("buffer %d %q", buf.ID, buf.Name))
		}
	}
	if d.InstrumentTab == InstrumentEditorTab || d.InstrumentTab == InstrumentModulesTab {
		parts = append(parts, r.unitSelection()...)
	}
	text := "selection: " + strings.Join(parts, " | ")
	if !full {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if m.playing {
		pos := m.playerStatus.SongPos
		fmt.Fprintf(&b, "\nplaying: pattern %d (order row), row %d", pos.OrderRow, pos.PatternRow)
	} else {
		b.WriteString("\nnot playing")
	}
	if t := d.Cursor.Track; t >= 0 && t < len(d.Song.Score.Tracks) {
		fmt.Fprintf(&b, "\nscore cursor: track %d, order row %d, row %d", t, d.Cursor.OrderRow, d.Cursor.PatternRow)
	}
	return b.String()
}

// unitSelection tells the units selected in the unit editor, the module
// units that the cursor is inside, and the parameter under the cursor.
func (r *Remote) unitSelection() []string {
	m := (*Model)(r)
	d := &m.d
	var parts []string
	list, _, depth := m.scope()
	if list == nil || len(*list) == 0 {
		return nil
	}
	if depth > 0 {
		ids := make([]string, depth)
		for i, id := range d.UnitPath[:depth] {
			ids[i] = "#" + strconv.Itoa(id)
		}
		parts = append(parts, "inside module unit "+strings.Join(ids, " > "))
	}
	units := *list
	a, b := min(d.UnitIndex, d.UnitIndex2), max(d.UnitIndex, d.UnitIndex2)
	if a < 0 || a >= len(units) {
		return parts
	}
	b = min(b, len(units)-1)
	u := &units[d.UnitIndex]
	if d.UnitIndex < 0 || d.UnitIndex >= len(units) {
		u = &units[a]
	}
	s := fmt.Sprintf("unit #%d %s", u.ID, u.Type)
	if b > a {
		ids := make([]string, 0, b-a+1)
		for _, v := range units[a : b+1] {
			ids = append(ids, "#"+strconv.Itoa(v.ID))
		}
		s += " (units " + strings.Join(ids, " ") + " selected)"
	}
	parts = append(parts, s)
	params := m.Params()
	if p := params.Item(params.Cursor()); p.vtable != nil && p.unit == u && !p.trackerSetting() && p.Type() != NoParameter {
		value := p.Value()
		s := fmt.Sprintf("parameter %s=%d", remoteParamName(&p), value)
		if hint := strings.TrimSpace(p.Hint().Label); hint != "" && hint != strconv.Itoa(value) {
			s += "(" + hint + ")"
		}
		parts = append(parts, s)
	}
	return parts
}
