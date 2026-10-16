package tracker

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// sampleTestModel returns a model whose song has the buffers and one
// instrument of the units.
func sampleTestModel(t *testing.T, buffers sointu.Buffers, presets sointu.EncodingPresets, mods sointu.Modules, units ...sointu.Unit) *Model {
	t.Helper()
	m := NewModel(NewBroker(), []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{{Name: "sampler", NumVoices: 1, Units: units}}
		m.d.Song.Modules = mods
		m.d.Song.Buffers = buffers
		m.d.Song.EncodingPresets = presets
		m.d.InstrIndex = 0
	}()
	return m
}

func sampleUnit(typ string, id int, params sointu.ParamMap) sointu.Unit {
	u := sointu.MakeUnit(typ)
	u.ID = id
	for k, v := range params {
		u.Parameters[k] = v
	}
	return u
}

func sample(data string, preset string) *sointu.AudioSample {
	return &sointu.AudioSample{FileName: data + ".flac", Data: sointu.Blob(data), Preset: preset}
}

type closer struct{ *bytes.Buffer }

func (closer) Close() error { return nil }

// writeInstrument writes the selected instrument as a file does.
func writeInstrument(t *testing.T, m *Model) []byte {
	t.Helper()
	var b bytes.Buffer
	if !m.Instrument().Write(closer{&b}) {
		t.Fatal("the instrument was not written")
	}
	return b.Bytes()
}

func readInstrument(t *testing.T, m *Model, data []byte) {
	t.Helper()
	if !m.Instrument().Read(io.NopCloser(bytes.NewReader(data))) {
		t.Fatal("the instrument was not read")
	}
}

func readsBuffer(m *Model, typ string) int {
	for _, u := range m.d.Song.Patch[m.d.InstrIndex].Units {
		if u.Type == typ {
			return u.Parameters["buffer"]
		}
	}
	return -1
}

// TestInstrumentCarriesSamples checks that an instrument file has the
// samples its units play, with their encoding preset, and that reading it
// into another song adds them with free IDs, uses a buffer that the song
// has with the same sample, and leaves a written buffer to the tracker.
func TestInstrumentCarriesSamples(t *testing.T) {
	kick := sointu.Buffer{ID: 1, Name: "kick", Channels: 1, Sample: sample("kick", "Small"), Slices: []int{100, 200}}
	rec := sointu.Buffer{ID: 2, Name: "rec", Channels: 1, Frames: 44100}
	src := sampleTestModel(t, sointu.Buffers{kick, rec},
		sointu.EncodingPresets{{Name: "Small", Encoding: sointu.Encoding{Format: "ogg", Args: []string{"-b:a", "32k"}}}}, nil,
		sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 1}),
		sampleUnit("bufwrite", 2, sointu.ParamMap{"buffer": 2}))
	data := writeInstrument(t, src)
	var file instrumentFile
	if err := yaml.Unmarshal(data, &file); err != nil || len(file.Buffers) != 1 || file.Buffers[0].Name != "kick" || len(file.EncodingPresets) != 1 {
		t.Fatalf("the file has %+v (%v), want the kick and its preset", file.presetSamples, err)
	}

	// a song with other buffers at the IDs: the kick gets a new one
	dst := sampleTestModel(t, sointu.Buffers{{ID: 1, Name: "snare", Channels: 1, Sample: sample("snare", "")}}, nil, nil)
	readInstrument(t, dst, data)
	id := readsBuffer(dst, "bufread")
	buf, ok := dst.d.Song.Buffers.Find(id)
	if !ok || id <= 2 || buf.Name != "kick" || !slices.Equal(buf.Slices, kick.Slices) || buf.Sample.Preset != "Small" {
		t.Fatalf("bufread reads %d: %+v, want the kick at a free ID", id, buf)
	}
	if p, ok := dst.d.Song.EncodingPresets.Find("Small"); !ok || p.Encoding.Format != "ogg" {
		t.Errorf("the preset of the kick did not come: %+v", dst.d.Song.EncodingPresets)
	}
	if got := readsBuffer(dst, "bufwrite"); got != 2 {
		t.Errorf("bufwrite writes %d, want the written buffer 2 left as it was", got)
	}
	n := len(dst.d.Song.Buffers)
	readInstrument(t, dst, data) // the kick is there now
	if readsBuffer(dst, "bufread") != id || len(dst.d.Song.Buffers) != n {
		t.Errorf("reading again reads %d with %d buffers, want the kick at %d reused", readsBuffer(dst, "bufread"), len(dst.d.Song.Buffers), id)
	}
	dst.History().Undo().Do()
	dst.History().Undo().Do()
	if len(dst.d.Song.Buffers) != 1 {
		t.Errorf("undo left %d buffers", len(dst.d.Song.Buffers))
	}
}

// TestSampleEncodingConflict checks that a sample whose preset the song has
// with another encoding gets the encoding as its own.
func TestSampleEncodingConflict(t *testing.T) {
	src := sampleTestModel(t, sointu.Buffers{{ID: 1, Channels: 1, Sample: sample("kick", "Small")}},
		sointu.EncodingPresets{{Name: "Small", Encoding: sointu.Encoding{Format: "ogg"}}}, nil,
		sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 1}))
	dst := sampleTestModel(t, nil, sointu.EncodingPresets{{Name: "Small", Encoding: sointu.Encoding{Format: "mp3"}}}, nil)
	readInstrument(t, dst, writeInstrument(t, src))
	buf, _ := dst.d.Song.Buffers.Find(readsBuffer(dst, "bufread"))
	if buf.Sample == nil || buf.Sample.Encoding == nil || buf.Sample.Encoding.Format != "ogg" || buf.Sample.Preset != "" {
		t.Errorf("the sample is %+v, want the ogg encoding of its own", buf.Sample)
	}
}

// TestOldInstrumentFile checks that a file without samples reads as before.
func TestOldInstrumentFile(t *testing.T) {
	dst := sampleTestModel(t, sointu.Buffers{{ID: 3, Channels: 1, Sample: sample("snare", "")}}, nil, nil)
	readInstrument(t, dst, []byte("units:\n  - type: bufread\n    parameters: {buffer: 3, stereo: 0}\n"))
	if got := readsBuffer(dst, "bufread"); got != 3 || len(dst.d.Song.Buffers) != 1 {
		t.Errorf("bufread reads %d with %d buffers, want 3 as in the file", got, len(dst.d.Song.Buffers))
	}
}

// TestPastedModuleUnitCarriesSample checks a sample that a module plays
// through a parameter of the module: the module unit's value and the
// default of the parameter point at the new buffer.
func TestPastedModuleUnitCarriesSample(t *testing.T) {
	read := sampleUnit("bufread", 10, sointu.ParamMap{"buffer": 0})
	read.Bind = map[string]sointu.Binding{"buffer": {Param: 1}}
	mod := sointu.Module{ID: 1, Name: "player", Params: []sointu.ModuleParam{{Name: "sample", Default: 4}}, Units: []sointu.Unit{read}}
	call := sampleUnit("module", 1, sointu.ParamMap{"module": 1, "p1": 4})
	src := sampleTestModel(t, sointu.Buffers{{ID: 4, Name: "pad", Channels: 2, Sample: sample("pad", "")}}, nil, sointu.Modules{mod}, call)
	data, err := src.Unit().List().CopyElements()
	if !err {
		t.Fatal("the unit was not copied")
	}
	dst := sampleTestModel(t, sointu.Buffers{{ID: 4, Name: "other", Channels: 1, Sample: sample("other", "")}}, nil, nil,
		sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 4}))
	if !dst.Unit().List().PasteElements(data) {
		t.Fatal("the unit was not pasted")
	}
	var pasted sointu.Unit
	for _, u := range dst.d.Song.Patch[0].Units {
		if u.Type == "module" {
			pasted = u
		}
	}
	buf, ok := dst.d.Song.Buffers.Find(pasted.Parameters["p1"])
	if !ok || buf.Name != "pad" {
		t.Fatalf("the module unit plays %d: %+v, want the pad", pasted.Parameters["p1"], buf)
	}
	i, _ := dst.d.Song.Modules.Find(pasted.Parameters["module"])
	if d := dst.d.Song.Modules[i].Params[0].Default; d != buf.ID {
		t.Errorf("the default of the parameter is %d, want %d", d, buf.ID)
	}
	if readsBuffer(dst, "bufread") != 4 {
		t.Errorf("the song's own bufread was changed")
	}
}

// TestPresetFileFormats checks that the samples go through YAML as the
// presets are read, with known fields only, and JSON.
func TestPresetFileFormats(t *testing.T) {
	file := instrumentFile{Instrument: sointu.Instrument{Units: []sointu.Unit{sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 1})}}}
	file.Buffers = sointu.Buffers{{ID: 1, Channels: 1, Sample: sample("kick", "")}}
	data, _ := yaml.Marshal(&file)
	var back instrumentFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&back); err != nil || len(back.Buffers) != 1 || string(back.Buffers[0].Sample.Data) != "kick" {
		t.Errorf("YAML: %v, %+v", err, back.presetSamples)
	}
	data, _ = json.Marshal(file)
	back = instrumentFile{}
	if err := json.Unmarshal(data, &back); err != nil || len(back.Buffers) != 1 || string(back.Buffers[0].Sample.Data) != "kick" {
		t.Errorf("JSON: %v, %+v", err, back.presetSamples)
	}
}

// TestCopiedInstrumentCarriesSample copies an instrument from one song to
// another.
func TestCopiedInstrumentCarriesSample(t *testing.T) {
	src := sampleTestModel(t, sointu.Buffers{{ID: 1, Name: "kick", Channels: 1, Sample: sample("kick", "")}}, nil, nil,
		sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 1}))
	data, ok := src.Instrument().List().CopyElements()
	if !ok {
		t.Fatal("the instrument was not copied")
	}
	dst := sampleTestModel(t, sointu.Buffers{{ID: 1, Name: "snare", Channels: 1, Sample: sample("snare", "")}}, nil, nil,
		sampleUnit("bufread", 1, sointu.ParamMap{"buffer": 1}))
	if !dst.Instrument().List().PasteElements(data) {
		t.Fatal("the instrument was not pasted")
	}
	found := false
	for _, instr := range dst.d.Song.Patch {
		for _, u := range instr.Units {
			if buf, ok := dst.d.Song.Buffers.Find(u.Parameters["buffer"]); ok && buf.Name == "kick" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no pasted unit reads the kick: %+v", dst.d.Song.Buffers)
	}
}

// TestModulePresetCarriesSample saves a module that plays a sample as a
// module preset and adds it to another song.
func TestModulePresetCarriesSample(t *testing.T) {
	mod := sointu.Module{ID: 1, Name: "kicker", Units: []sointu.Unit{sampleUnit("bufread", 10, sointu.ParamMap{"buffer": 2})}}
	src := sampleTestModel(t, sointu.Buffers{{ID: 2, Name: "kick", Channels: 1, Sample: sample("kick", "")}}, nil, sointu.Modules{mod})
	dir := t.TempDir()
	src.modulePresetPath = dir
	src.d.ModuleIndex = 0
	src.Module().SavePreset().Do()
	dst := sampleTestModel(t, sointu.Buffers{{ID: 2, Name: "snare", Channels: 1, Sample: sample("snare", "")}}, nil, nil)
	dst.modulePresetPath = dir
	dst.loadModulePresets()
	presets := dst.Module().Presets()
	for i := presets.Range().Min; i <= presets.Range().Max; i++ {
		if presets.StringOf(i) == "kicker" {
			presets.SetValue(i)
		}
	}
	i := slices.IndexFunc(dst.d.Song.Modules, func(m sointu.Module) bool { return m.Name == "kicker" })
	if i < 0 {
		t.Fatal("the module preset was not added")
	}
	buf, ok := dst.d.Song.Buffers.Find(dst.d.Song.Modules[i].Units[0].Parameters["buffer"])
	if !ok || buf.Name != "kick" {
		t.Errorf("the module plays %+v, want the kick", buf)
	}
}
