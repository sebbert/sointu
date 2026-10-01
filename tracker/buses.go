package tracker

import (
	"fmt"

	"github.com/vsariola/sointu"
)

// fixBuses keeps the buses of the song in sync with the mc units, after every
// change of the patch, like fixSpectrumBuffers:
//
//   - units starting a chain (mcspread) without a bus get a new one. Their
//     bus parameter cannot be set to none, so 0 means a new unit.
//   - units referring to a bus that is missing, e.g. after pasting them from
//     another song or loading a preset, get it back with the same ID. If the
//     ID is another kind of buffer's, they get a new bus.
//   - buses the tracker created for units are deleted when no unit refers to
//     them anymore.
//
// It runs inside the change, so undo restores the buses with the units.
func (m *Model) fixBuses() {
	remap := map[int]int{} // IDs of other buffers used as buses -> new buses
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if sointu.BusParams(u.Type) == nil {
				continue
			}
			id := u.Parameters["bus"]
			buf, ok := m.d.Song.Buffers.Find(id)
			switch {
			case id == 0:
				if sointu.WritesBus(u.Type) {
					u.Parameters["bus"] = m.addBus(0)
				}
			case !ok:
				m.addBus(id)
			case !buf.Bus:
				if _, ok := remap[id]; !ok {
					remap[id] = m.addBus(0)
				}
			}
		}
	}
	used := map[int]bool{}
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			for _, p := range sointu.BusParams(u.Type) {
				if id, ok := remap[u.Parameters[p]]; ok {
					u.Parameters[p] = id
				}
				used[u.Parameters[p]] = true
			}
		}
	}
	bufs := m.d.Song.Buffers[:0]
	for _, b := range m.d.Song.Buffers {
		if b.Auto && b.Bus && !used[b.ID] {
			continue
		}
		bufs = append(bufs, b)
	}
	m.d.Song.Buffers = bufs
	m.d.BufferIndex = clamp(m.d.BufferIndex, 0, len(m.d.Song.Buffers)-1)
	m.warnBuses()
}

// assignBuses moves the mc units of an instrument being loaded, e.g. from a
// preset, to buses of their own: bus IDs that the song already uses for a
// buffer become new IDs, the same for all the units sharing one. fixBuses
// then creates the buses.
func (m *Model) assignBuses(units []sointu.Unit) {
	next := 1
	for _, b := range m.d.Song.Buffers {
		next = max(next, b.ID+1)
	}
	rewrites := map[int]int{}
	for i := range units {
		for _, p := range sointu.BusParams(units[i].Type) {
			id := units[i].Parameters[p]
			if _, ok := m.d.Song.Buffers.Find(id); id == 0 || !ok {
				continue
			}
			if _, ok := rewrites[id]; !ok {
				rewrites[id] = next
				next++
			}
			units[i].Parameters[p] = rewrites[id]
		}
	}
}

// warnBuses warns about mc units in instruments with more than one voice,
// which run only in the first one.
func (m *Model) warnBuses() {
	for i, instr := range m.runPatch() {
		if instr.NumVoices <= 1 {
			continue
		}
		for _, u := range instr.Units {
			if !u.Disabled && sointu.BusParams(u.Type) != nil {
				m.Alerts().AddNamed("MCVoices", fmt.Sprintf("Instrument %d '%s' has %d voices, but its mc units run only in the first one", i+1, instr.Name, instr.NumVoices), Warning)
				return
			}
		}
	}
	m.Alerts().ClearNamed("MCVoices")
}

// addBus adds an automatically created bus with the given ID, or a new ID if
// 0, and returns the ID.
func (m *Model) addBus(id int) int {
	if id == 0 {
		id = 1
		for _, b := range m.d.Song.Buffers {
			id = max(id, b.ID+1)
		}
	}
	m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: id, Name: fmt.Sprintf("Bus %d", id), Channels: sointu.MCChannels, Bus: true, Auto: true})
	return id
}

// defaultBus returns the bus a new mc unit at the given position should use:
// the bus of the mc unit before it in its instrument, or else the first bus
// of the song, or 0.
func (m *Model) defaultBus(units []sointu.Unit, unitIndex int) int {
	for i := min(unitIndex, len(units)) - 1; i >= 0; i-- {
		if sointu.BusParams(units[i].Type) != nil && units[i].Parameters["bus"] != 0 {
			return units[i].Parameters["bus"]
		}
	}
	for _, b := range m.d.Song.Buffers {
		if b.Bus {
			return b.ID
		}
	}
	return 0
}

// busUsed reports whether an mc unit refers to the bus with the given ID.
func (m *Model) busUsed(id int) bool {
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			for _, p := range sointu.BusParams(u.Type) {
				if u.Parameters[p] == id {
					return true
				}
			}
		}
	}
	return false
}

// Levels returns the peak level of each channel of the bus after unit i of
// the selected instrument, an mc unit, in the last report of the player, or
// nil. While it is being called, the player keeps reporting them.
func (m *UnitModel) Levels(i int) []float32 {
	_, id, _ := (*Model)(m).rowUnit(i)
	if id == 0 {
		return nil
	}
	mags, _ := (*Model)(m).spectrumOf(SpectrumSource{Unit: id, Levels: true})
	return mags
}
