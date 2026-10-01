package tracker

import (
	"fmt"

	"github.com/vsariola/sointu"
)

// fixSpectrumBuffers keeps the spectrum buffers of the song in sync with the
// spectral units, after every change of the patch:
//
//   - units writing a spectrum (spfft, spcopy) without one get a new buffer.
//     Their buffer parameter cannot be set to none, so 0 means a new unit.
//   - units writing a spectrum buffer that is missing, e.g. after pasting them
//     from another song, get it back with the same ID. If the ID is an audio
//     buffer's, they and the units using the spectrum get a new one.
//   - buffers the tracker created for units are deleted when no unit refers
//     to them anymore.
//
// It runs inside the change, so undo restores the buffers with the units.
func (m *Model) fixSpectrumBuffers() {
	remap := map[int]int{} // IDs of audio buffers written as spectra -> new spectrum buffers
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if !sointu.WritesSpectrum(u.Type) {
				continue
			}
			id := u.Parameters["buffer"]
			buf, ok := m.d.Song.Buffers.Find(id)
			switch {
			case id == 0:
				u.Parameters["buffer"] = m.addSpectrumBuffer(0)
			case !ok:
				m.addSpectrumBuffer(id)
			case !buf.Spectrum: // e.g. pasted from a song where the ID was a spectrum
				if _, ok := remap[id]; !ok {
					remap[id] = m.addSpectrumBuffer(0)
				}
			}
		}
	}
	used := map[int]bool{}
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			for _, p := range sointu.SpectrumBufferParams(u.Type) {
				if id, ok := remap[u.Parameters[p]]; ok {
					u.Parameters[p] = id
				}
				used[u.Parameters[p]] = true
			}
		}
	}
	bufs := m.d.Song.Buffers[:0]
	for _, b := range m.d.Song.Buffers {
		if b.Auto && b.Spectrum && !used[b.ID] {
			continue
		}
		bufs = append(bufs, b)
	}
	m.d.Song.Buffers = bufs
	m.d.BufferIndex = clamp(m.d.BufferIndex, 0, len(m.d.Song.Buffers)-1)
	m.warnSpectral()
}

// warnSpectral warns about spectral units in instruments with more than one
// voice, which run only in the first voice, and about spectrum buffers
// written by more than one unit.
func (m *Model) warnSpectral() {
	writers := map[int]int{}
	for i, instr := range m.runPatch() {
		for _, u := range instr.Units {
			if u.Disabled || len(sointu.SpectrumBufferParams(u.Type)) == 0 {
				continue
			}
			if instr.NumVoices > 1 {
				m.Alerts().AddNamed("SpectralVoices", fmt.Sprintf("Instrument %d '%s' has %d voices, but its spectral units run only in the first one", i+1, instr.Name, instr.NumVoices), Warning)
				return
			}
			if sointu.WritesSpectrum(u.Type) {
				if writers[u.Parameters["buffer"]]++; writers[u.Parameters["buffer"]] > 1 {
					m.Alerts().AddNamed("SpectralVoices", "A spectrum buffer is written by more than one spfft or spcopy unit; only the last one is heard", Warning)
					return
				}
			}
		}
	}
	m.Alerts().ClearNamed("SpectralVoices")
}

// addSpectrumBuffer adds an automatically created spectrum buffer with the
// given ID, or a new ID if 0, and returns the ID.
func (m *Model) addSpectrumBuffer(id int) int {
	if id == 0 {
		id = 1
		for _, b := range m.d.Song.Buffers {
			id = max(id, b.ID+1)
		}
	}
	m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: id, Name: fmt.Sprintf("Spectrum %d", id), Spectrum: true, Auto: true})
	return id
}

// spectrumSize returns the size and channels of the spectrum buffer with the
// given ID: those of the spfft unit writing it, or of the spectrum an spcopy
// unit copies to it, and whether it is written at all.
func (m *Model) spectrumSize(id int) (size, channels int, ok bool) {
	for range 8 { // copies of copies
		found := false
		for units := range m.d.Song.UnitLists() {
			for _, u := range units {
				if u.Disabled || u.Parameters["buffer"] != id {
					continue
				}
				switch u.Type {
				case "spfft":
					return sointu.SpectrumSize(u.Parameters["size"]), 1 + u.Parameters["stereo"]&1, true
				case "spcopy":
					if !found {
						id, found = u.Parameters["source"], true
					}
				}
			}
		}
		if !found {
			break
		}
	}
	return sointu.SpectrumSize(sointu.SpectrumSizeDefault), 1, false
}

// defaultSpectrumBuffer returns the spectrum buffer a new spectral unit reading
// a spectrum at the given position should use: the spectrum written last
// before it in its instrument, or else the first spectrum buffer of the song,
// or 0.
func (m *Model) defaultSpectrumBuffer(units []sointu.Unit, unitIndex int) int {
	for i := min(unitIndex, len(units)) - 1; i >= 0; i-- {
		if sointu.WritesSpectrum(units[i].Type) {
			return units[i].Parameters["buffer"]
		}
	}
	for _, b := range m.d.Song.Buffers {
		if b.Spectrum {
			return b.ID
		}
	}
	return 0
}

// spectrumBufferUsed reports whether a unit refers to the spectrum buffer
// with the given ID.
func (m *Model) spectrumBufferUsed(id int) bool {
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			for _, p := range sointu.SpectrumBufferParams(u.Type) {
				if u.Parameters[p] == id {
					return true
				}
			}
		}
	}
	return false
}
