package tracker

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/vsariola/sointu"
)

// The changes to the song between two Remote calls: mostly made by the user
// in the tracker, which a language model cannot see.

// maxChangeLines is how many changes Changes lists before it summarizes.
const maxChangeLines = 60

// Changes tells what changed in the song since the previous Remote call, or
// "" when nothing did.
func (r *Remote) Changes() string {
	m := (*Model)(r)
	if m.remoteSeen == nil {
		return ""
	}
	now := m.d.Song.Copy()
	if reflect.DeepEqual(*m.remoteSeen, now) {
		return ""
	}
	lines := songChanges(m.remoteSeen, &now)
	if len(lines) == 0 {
		lines = []string{"(details not tracked)"}
	}
	if len(lines) > maxChangeLines {
		n := len(lines) - maxChangeLines
		lines = append(lines[:maxChangeLines], fmt.Sprintf("... and %d more: read the song again", n))
	}
	return strings.Join(lines, "\n")
}

func songChanges(old, now *sointu.Song) []string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	if old.BPM != now.BPM || old.RowsPerBeat != now.RowsPerBeat {
		add("tempo: %d BPM, %d rows per beat (was %d, %d)", now.BPM, now.RowsPerBeat, old.BPM, old.RowsPerBeat)
	}
	lines = append(lines, scoreChanges(&old.Score, &now.Score)...)
	// instruments: matched by the IDs of their units, else by name
	match := matchInstruments(old.Patch, now.Patch)
	matched := map[int]bool{}
	for j := range now.Patch {
		if i, ok := match[j]; ok {
			matched[i] = true
		}
	}
	for i, instr := range old.Patch {
		if !matched[i] {
			add("deleted instrument %q (was %d)", instr.Name, i)
		}
	}
	var order []int // the old indices of the instruments kept, in their new order
	for j := range now.Patch {
		instr := &now.Patch[j]
		i, ok := match[j]
		if !ok {
			add("added instrument %d %q: %d units", j, instr.Name, len(instr.Units))
			continue
		}
		order = append(order, i)
		was := &old.Patch[i]
		name := fmt.Sprintf("instrument %d %q", j, instr.Name)
		if was.Name != instr.Name {
			add("%s: renamed from %q", name, was.Name)
		}
		if max(was.NumVoices, 1) != max(instr.NumVoices, 1) {
			add("%s: %d voices (was %d)", name, max(instr.NumVoices, 1), max(was.NumVoices, 1))
		}
		if was.Mute != instr.Mute {
			add("%s: muted %v", name, instr.Mute)
		}
		if was.Comment != instr.Comment {
			add("%s: comment changed", name)
		}
		if was.MIDI != instr.MIDI {
			add("%s: MIDI %+v (was %+v)", name, instr.MIDI, was.MIDI)
		}
		if was.ThreadMaskM1 != instr.ThreadMaskM1 {
			add("%s: threads changed", name)
		}
		for _, l := range unitChanges(was.Units, instr.Units) {
			add("%s: %s", name, l)
		}
	}
	if !slices.IsSorted(order) {
		names := make([]string, len(now.Patch))
		for j := range now.Patch {
			names[j] = fmt.Sprintf("%d %q", j, now.Patch[j].Name)
		}
		add("instruments reordered: now %s", strings.Join(names, ", "))
	}
	// modules: matched by ID
	oldMods := map[int]*sointu.Module{}
	for k := range old.Modules {
		oldMods[old.Modules[k].ID] = &old.Modules[k]
	}
	seen := map[int]bool{}
	for k := range now.Modules {
		mod := &now.Modules[k]
		seen[mod.ID] = true
		was, ok := oldMods[mod.ID]
		if !ok {
			add("added module %q: %d units", mod.Name, len(mod.Units))
			continue
		}
		name := fmt.Sprintf("module %q", mod.Name)
		if was.Name != mod.Name {
			add("%s: renamed from %q", name, was.Name)
		}
		if was.Inputs != mod.Inputs {
			add("%s: %d inputs (was %d)", name, mod.Inputs, was.Inputs)
		}
		if was.Comment != mod.Comment {
			add("%s: comment changed", name)
		}
		if !slices.Equal(was.Params, mod.Params) {
			add("%s: parameters %s (were %s)", name, moduleParams(mod.Params), moduleParams(was.Params))
		}
		for _, l := range unitChanges(was.Units, mod.Units) {
			add("%s: %s", name, l)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(oldMods)) {
		if !seen[id] {
			add("deleted module %q", oldMods[id].Name)
		}
	}
	lines = append(lines, bufferChanges(old.Buffers, now.Buffers)...)
	if !reflect.DeepEqual(old.EncodingPresets, now.EncodingPresets) {
		add("the sample encoding presets changed")
	}
	return lines
}

func moduleParams(params []sointu.ModuleParam) string {
	names := make([]string, len(params))
	for k, p := range params {
		names[k] = fmt.Sprintf("p%d %s=%d", k+1, p.Name, p.Default)
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// scoreChanges tells which tracks of the score changed.
func scoreChanges(old, now *sointu.Score) []string {
	var lines []string
	if old.Length != now.Length || old.RowsPerPattern != now.RowsPerPattern {
		lines = append(lines, fmt.Sprintf("score: %d patterns of %d rows (was %d of %d)", now.Length, now.RowsPerPattern, old.Length, old.RowsPerPattern))
	}
	if len(old.Tracks) != len(now.Tracks) {
		lines = append(lines, fmt.Sprintf("score: %d tracks (was %d)", len(now.Tracks), len(old.Tracks)))
		return lines
	}
	var changed []string
	for t := range now.Tracks {
		if !reflect.DeepEqual(old.Tracks[t], now.Tracks[t]) {
			changed = append(changed, strconv.Itoa(t))
		}
	}
	if len(changed) > 0 {
		lines = append(lines, "score: notes or patterns of tracks "+strings.Join(changed, ", ")+" changed")
	}
	return lines
}

// bufferChanges tells the buffers added, deleted and changed, by ID.
func bufferChanges(old, now sointu.Buffers) []string {
	var lines []string
	byID := map[int]*sointu.Buffer{}
	for k := range old {
		byID[old[k].ID] = &old[k]
	}
	seen := map[int]bool{}
	for k := range now {
		b := &now[k]
		seen[b.ID] = true
		was, ok := byID[b.ID]
		switch {
		case !ok:
			lines = append(lines, fmt.Sprintf("added buffer %d %q", b.ID, b.Name))
		case !reflect.DeepEqual(*was, *b):
			s := fmt.Sprintf("buffer %d %q changed", b.ID, b.Name)
			if was.Name != b.Name {
				s += fmt.Sprintf(" (was named %q)", was.Name)
			}
			lines = append(lines, s)
		}
	}
	for k := range old {
		if !seen[old[k].ID] {
			lines = append(lines, fmt.Sprintf("deleted buffer %d %q", old[k].ID, old[k].Name))
		}
	}
	return lines
}

// matchInstruments returns, for each instrument of now, the index of the
// same instrument in old: the one with most unit IDs in common, else the one
// of the same name.
func matchInstruments(old, now sointu.Patch) map[int]int {
	owner := map[int]int{}
	for i, instr := range old {
		for _, u := range instr.Units {
			if u.ID != 0 {
				owner[u.ID] = i
			}
		}
	}
	ret := map[int]int{}
	taken := map[int]bool{}
	for j, instr := range now {
		votes := map[int]int{}
		for _, u := range instr.Units {
			if i, ok := owner[u.ID]; ok && u.ID != 0 {
				votes[i]++
			}
		}
		best, bestVotes := -1, 0
		for i, v := range votes {
			if !taken[i] && (v > bestVotes || v == bestVotes && i < best) {
				best, bestVotes = i, v
			}
		}
		if best >= 0 {
			ret[j], taken[best] = best, true
		}
	}
	for j, instr := range now {
		if _, ok := ret[j]; ok {
			continue
		}
		for i := range old {
			if !taken[i] && old[i].Name == instr.Name {
				ret[j], taken[i] = i, true
				break
			}
		}
	}
	return ret
}

// unitChanges tells how a list of units changed, matching units by ID.
func unitChanges(old, now []sointu.Unit) []string {
	var lines []string
	add := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	byID := map[int]*sointu.Unit{}
	var oldOrder []int
	for k := range old {
		if old[k].ID != 0 {
			byID[old[k].ID] = &old[k]
			oldOrder = append(oldOrder, old[k].ID)
		}
	}
	kept := map[int]bool{}
	var newOrder []int
	for k := range now {
		u := &now[k]
		was, ok := byID[u.ID]
		if u.ID == 0 || !ok {
			add("added unit #%d %s", u.ID, unitTypeName(u))
			continue
		}
		kept[u.ID] = true
		newOrder = append(newOrder, u.ID)
		name := fmt.Sprintf("unit #%d %s", u.ID, unitTypeName(u))
		if was.Type != u.Type {
			add("%s: was a %s", name, unitTypeName(was))
			continue
		}
		if was.Disabled != u.Disabled {
			if u.Disabled {
				add("%s: disabled", name)
			} else {
				add("%s: enabled", name)
			}
		}
		var params []string
		for _, p := range slices.Sorted(maps.Keys(u.Parameters)) {
			if v, w := u.Parameters[p], was.Parameters[p]; v != w {
				params = append(params, fmt.Sprintf("%s=%s (was %s)", p, paramValue(u.Type, p, v), paramValue(u.Type, p, w)))
			}
		}
		for p, w := range was.Parameters {
			if _, ok := u.Parameters[p]; !ok && w != 0 {
				params = append(params, fmt.Sprintf("%s=0 (was %d)", p, w))
			}
		}
		if len(params) > 0 {
			add("%s: %s", name, strings.Join(params, ", "))
		}
		if !slices.Equal(was.VarArgs, u.VarArgs) {
			add("%s: varargs (delay times) %v (was %v)", name, u.VarArgs, was.VarArgs)
		}
		if !reflect.DeepEqual(was.Bind, u.Bind) {
			add("%s: bindings changed", name)
		}
		if was.Comment != u.Comment {
			add("%s: comment %q", name, u.Comment)
		}
	}
	for _, id := range oldOrder {
		if !kept[id] {
			add("deleted unit #%d %s", id, unitTypeName(byID[id]))
		}
	}
	var keptOrder []int
	for _, id := range oldOrder {
		if kept[id] {
			keptOrder = append(keptOrder, id)
		}
	}
	if !slices.Equal(keptOrder, newOrder) {
		add("units reordered")
	}
	return lines
}

func unitTypeName(u *sointu.Unit) string {
	if u.Type == "" {
		return "(empty)"
	}
	return u.Type
}

// paramValue is a parameter value with what the tracker displays for it.
func paramValue(unitType, param string, v int) string {
	s := strconv.Itoa(v)
	if p, ok := sointu.BindableParam(unitType, param); ok && p.DisplayFunc != nil {
		if text, unit := p.DisplayFunc(v); text != s {
			s += "(" + strings.TrimSpace(text+" "+unit) + ")"
		}
	}
	return s
}
