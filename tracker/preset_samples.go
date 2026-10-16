package tracker

import (
	"slices"

	"github.com/vsariola/sointu"
)

// Samples in files, presets and on the clipboard

// presetSamples are the sample buffers that the units of an instrument file,
// a preset or the clipboard read, with the encoding presets of the song that
// their samples use: so that the units play the same in another song. The
// samples are stored as the song stores them, encoded files and all. Written
// buffers, spectra and buses are not among them: the tracker makes those
// for the units (fixConvolutionBuffers, fixSpectrumBuffers, fixBuses).
type presetSamples struct {
	Buffers         sointu.Buffers         `yaml:",omitempty" json:",omitempty"`
	EncodingPresets sointu.EncodingPresets `yaml:",omitempty" json:",omitempty"`
}

// audioBufferUnit reports whether the buffer parameter of a unit type is
// the ID of an audio buffer: one that bufread plays, convolution reads and
// bufwrite writes.
func audioBufferUnit(unitType string) bool {
	return unitType == "bufread" || unitType == "convolution" || unitType == "bufwrite"
}

// audioBufferParams returns the parameters of a unit type that are the IDs
// of audio buffers.
func audioBufferParams(unitType string) []string {
	if audioBufferUnit(unitType) {
		return []string{"buffer"}
	}
	return nil
}

// anyBufferParams returns the parameters of a unit type that are the IDs of
// buffers: audio buffers, spectra or buses.
func anyBufferParams(unitType string) []string {
	return append(audioBufferParams(unitType), unitBufferParams(unitType)...)
}

// bufferPorts returns, for each of the modules by ID, the parameters of the
// module (1 to MaxModuleParams) that are buffers: bound to a parameter of a
// unit that params names, or to such a parameter of a module unit inside.
func bufferPorts(mods sointu.Modules, params func(unitType string) []string) map[int]map[int]bool {
	ports := map[int]map[int]bool{}
	for changed := true; changed; { // until the modules using modules have them too
		changed = false
		for _, mod := range mods {
			for _, u := range mod.Units {
				names := params(u.Type)
				if u.Type == "module" {
					for k := range ports[u.Parameters["module"]] {
						names = append(names, sointu.ModuleParamName(k))
					}
				}
				for _, name := range names {
					b, ok := u.Bind[name]
					if !ok || ports[mod.ID][b.Param] {
						continue
					}
					if ports[mod.ID] == nil {
						ports[mod.ID] = map[int]bool{}
					}
					ports[mod.ID][b.Param] = true
					changed = true
				}
			}
		}
	}
	return ports
}

// rewriteAudioBuffers calls f with every ID of an audio buffer in the units
// and the modules, and sets it to what f returns: the buffer parameters of
// bufread, convolution and bufwrite units, the parameters of module units
// that are bound to them, and the defaults of those parameters of the
// modules. A module unit of a module not among mods is left alone.
func rewriteAudioBuffers(mods sointu.Modules, f func(id int) int, unitLists ...[]sointu.Unit) {
	ports := bufferPorts(mods, audioBufferParams)
	rewrite := func(units []sointu.Unit) {
		for i := range units {
			u := &units[i]
			switch {
			case audioBufferUnit(u.Type):
				if id := u.Parameters["buffer"]; id != 0 {
					u.Parameters["buffer"] = f(id)
				}
			case u.Type == "module":
				for k := range ports[u.Parameters["module"]] {
					if id := u.Parameters[sointu.ModuleParamName(k)]; id != 0 {
						u.Parameters[sointu.ModuleParamName(k)] = f(id)
					}
				}
			}
		}
	}
	for _, units := range unitLists {
		rewrite(units)
	}
	for i := range mods {
		rewrite(mods[i].Units)
		for k := range ports[mods[i].ID] {
			if p := &mods[i].Params; k >= 1 && k <= len(*p) && (*p)[k-1].Default != 0 {
				(*p)[k-1].Default = f((*p)[k-1].Default)
			}
		}
	}
}

// samplesUsedBy returns copies of the sample buffers of the song that the
// units and the modules (those they use, see modulesUsedBy) refer to, and
// the encoding presets of their samples.
func (m *Model) samplesUsedBy(mods sointu.Modules, unitLists ...[]sointu.Unit) presetSamples {
	var ret presetSamples
	ids := map[int]bool{}
	// f does not change the units: they are the song's
	rewriteAudioBuffers(mods, func(id int) int { ids[id] = true; return id }, unitLists...)
	for _, buf := range m.d.Song.Buffers {
		if !ids[buf.ID] || buf.Sample == nil {
			continue
		}
		ret.Buffers = append(ret.Buffers, buf)
		s := buf.Sample
		if _, done := ret.EncodingPresets.Find(s.Preset); s.Encoding != nil || done {
			continue
		}
		if p, ok := m.d.Song.EncodingPresets.Find(s.Preset); ok {
			ret.EncodingPresets = append(ret.EncodingPresets, p)
		}
	}
	ret.Buffers = ret.Buffers.Copy()
	ret.EncodingPresets = ret.EncodingPresets.Copy()
	return ret
}

// importSamples adds the sample buffers that came with units from a file, a
// preset or the clipboard to the song, before the modules are imported
// (importModules), and points the units and the modules at them. It returns
// a copy of mods with the new IDs; the units are changed in place. Each
// buffer gets an ID that neither the song nor the units use; a buffer of
// the song with the same sample, channels and slices is used instead of a
// new one. A sample using an encoding preset gets the preset added to the
// song, if the song has none of the name, or its encoding as its own, if
// the song's preset of the name encodes differently.
func (m *Model) importSamples(samples presetSamples, mods sointu.Modules, unitLists ...[]sointu.Unit) sointu.Modules {
	mods = mods.Copy()
	if len(samples.Buffers) == 0 {
		return mods
	}
	next := 1
	for _, buf := range m.d.Song.Buffers {
		next = max(next, buf.ID+1)
	}
	// nor the IDs of buffers, spectra and buses that the units refer to
	ports := bufferPorts(mods, anyBufferParams)
	referenced := func(units []sointu.Unit) {
		for _, u := range units {
			for _, name := range anyBufferParams(u.Type) {
				next = max(next, u.Parameters[name]+1)
			}
			if u.Type == "module" {
				for k := range ports[u.Parameters["module"]] {
					next = max(next, u.Parameters[sointu.ModuleParamName(k)]+1)
				}
			}
		}
	}
	for _, units := range unitLists {
		referenced(units)
	}
	for _, mod := range mods {
		referenced(mod.Units)
		for k := range ports[mod.ID] {
			if k >= 1 && k <= len(mod.Params) {
				next = max(next, mod.Params[k-1].Default+1)
			}
		}
	}
	newIDs := map[int]int{}
	for _, buf := range samples.Buffers.Copy() {
		if buf.Sample == nil || buf.ID == 0 {
			continue
		}
		if i := slices.IndexFunc(m.d.Song.Buffers, func(b sointu.Buffer) bool {
			return b.Sample != nil && b.Sample.Data.Equal(buf.Sample.Data) && b.Channels == buf.Channels && slices.Equal(b.Slices, buf.Slices)
		}); i >= 0 {
			newIDs[buf.ID] = m.d.Song.Buffers[i].ID
			continue
		}
		newIDs[buf.ID] = next
		buf.ID, buf.Auto = next, false
		next++
		m.importEncoding(buf.Sample, samples.EncodingPresets)
		m.d.Song.Buffers = append(m.d.Song.Buffers, buf)
		m.changeType |= BufferChange
	}
	rewriteAudioBuffers(mods, func(id int) int {
		if n, ok := newIDs[id]; ok {
			return n
		}
		return id
	}, unitLists...)
	return mods
}

// importEncoding makes a sample from another song use an encoding of this
// song that encodes it as there: see importSamples.
func (m *Model) importEncoding(s *sointu.AudioSample, presets sointu.EncodingPresets) {
	if s.Encoding != nil {
		return
	}
	p, known := presets.Find(s.Preset)
	if !known {
		return // a file without its presets: the name is all there is
	}
	if len(m.d.Song.EncodingPresets) == 0 {
		m.d.Song.EncodingPresets = m.defaultPresets.Copy() // as the first sample imported gets
	}
	if q, ok := m.d.Song.EncodingPresets.Find(s.Preset); !ok {
		m.d.Song.EncodingPresets = append(m.d.Song.EncodingPresets, sointu.EncodingPreset{Name: p.Name, Encoding: p.Encoding.Copy()})
	} else if !q.Encoding.Equal(p.Encoding) {
		e := p.Encoding.Copy()
		s.Encoding, s.Preset = &e, ""
	}
}
