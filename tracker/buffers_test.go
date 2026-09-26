package tracker

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/vm"
)

// testWav returns a mono 16-bit 44.1 kHz WAV file with the given samples.
func testWav(samples []int16) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + 2*len(samples)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(44100), uint32(88200), uint16(2), uint16(16)} {
		w(v) // fmt chunk: PCM, mono, 44.1 kHz, 16-bit
	}
	b.WriteString("data")
	w(uint32(2 * len(samples)))
	w(samples)
	return b.Bytes()
}

func newBufferTestModel(t *testing.T) (*Model, *Broker) {
	t.Helper()
	if _, err := ffmpeg.Find(""); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	drainPlayer(broker)
	return m, broker
}

func drainPlayer(broker *Broker) {
	for {
		select {
		case <-broker.ToPlayer:
		default:
			return
		}
	}
}

// processBufferResults passes buffer worker results to the model until the
// buffer is no longer processing.
func processBufferResults(t *testing.T, m *Model, broker *Broker, id int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for m.BufferStatus(id).Processing {
		select {
		case msg := <-broker.ToModel:
			if _, ok := msg.Data.(bufferResult); ok {
				m.ProcessMsg(msg)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for buffer %d", id)
		}
	}
}

// lastBufferAudio returns the last BufferAudioMsg sent to the player.
func lastBufferAudio(t *testing.T, broker *Broker) map[int]sointu.BufferAudio {
	t.Helper()
	var ret map[int]sointu.BufferAudio
	found := false
	for {
		select {
		case msg := <-broker.ToPlayer:
			if a, ok := msg.(BufferAudioMsg); ok {
				ret, found = a.Audio, true
			}
		default:
			if !found {
				t.Fatalf("no buffer audio sent to the player")
			}
			return ret
		}
	}
}

func setBuffers(m *Model, buffers sointu.Buffers) {
	defer m.change("SetBuffers", BufferChange, MajorChange)()
	m.d.Song.Buffers = buffers
}

func TestBufferAudioReachesPlayer(t *testing.T) {
	m, broker := newBufferTestModel(t)
	samples := []int16{0, 16384, -16384, 8192}
	sample := &sointu.AudioSample{Data: testWav(samples), Encoding: &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}}
	setBuffers(m, sointu.Buffers{{ID: 7, Channels: 1, Sample: sample}})
	processBufferResults(t, m, broker, 7)
	if err := m.BufferStatus(7).Err; err != nil {
		t.Fatalf("processing failed: %v", err)
	}
	audio := lastBufferAudio(t, broker)[7]
	want := []float32{0, 0.5, -0.5, 0.25}
	if audio.Channels != 1 || len(audio.Data) != len(want) {
		t.Fatalf("got %+v, want %v", audio, want)
	}
	for i := range want {
		if audio.Data[i] != want[i] {
			t.Errorf("sample %d: got %v, want %v", i, audio.Data[i], want[i])
		}
	}

	setBuffers(m, nil)
	if _, ok := lastBufferAudio(t, broker)[7]; ok {
		t.Errorf("removed buffer still sent to the player")
	}
}

func TestStaleBufferResultIsIgnored(t *testing.T) {
	m, broker := newBufferTestModel(t)
	enc := sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{100}), Encoding: &enc}}})
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{200, 300}), Encoding: &enc}}})
	processBufferResults(t, m, broker, 1)
	if got := m.BufferAudio()[1].Frames(); got != 2 {
		t.Errorf("got %d frames, want the 2 frames of the latest sample", got)
	}
}

func TestUndoRestoresBufferAudio(t *testing.T) {
	m, broker := newBufferTestModel(t)
	enc := sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: testWav([]int16{1, 2, 3}), Encoding: &enc}}})
	processBufferResults(t, m, broker, 1)
	setBuffers(m, nil)
	m.History().Undo().Do()
	processBufferResults(t, m, broker, 1)
	if got := m.BufferAudio()[1].Frames(); got != 3 {
		t.Errorf("after undo, got %d frames, want 3", got)
	}
}

func TestSplitJoinArgs(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"-c:a libopus -b:a 32k", []string{"-c:a", "libopus", "-b:a", "32k"}},
		{`  -af "volume=0.5, atempo=2"  `, []string{"-af", "volume=0.5, atempo=2"}},
		{`-metadata title='it'\''s' ""`, []string{"-metadata", "title=it's", ""}},
		{`a\ b`, []string{"a b"}},
		{"", nil},
	} {
		got, err := SplitArgs(tc.in)
		if err != nil || !slicesEqual(got, tc.want) {
			t.Errorf("SplitArgs(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
		back, err := SplitArgs(JoinArgs(tc.want))
		if err != nil || !slicesEqual(back, tc.want) {
			t.Errorf("JoinArgs(%q) = %q does not split back", tc.want, JoinArgs(tc.want))
		}
	}
	if _, err := SplitArgs(`"unterminated`); err == nil {
		t.Errorf("expected an error for an unterminated quote")
	}
}

func TestBufreadBufferParameter(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Buffers = sointu.Buffers{{ID: 3, Name: "kick", Channels: 1}, {ID: 5, Name: "snare", Channels: 1}}
		m.d.Song.Patch[0].Units = []sointu.Unit{{Type: "bufread", ID: 100, Parameters: sointu.ParamMap{"buffer": 5, "notetracking": 1}}}
		m.d.UnitIndex = 0
	}()
	var p Parameter
	for _, q := range m.derived.patch[0].params[0] {
		if q.Name() == "buffer" {
			p = q
		}
	}
	if p.Type() != ChoiceParameter {
		t.Fatalf("buffer parameter has type %v, want ChoiceParameter", p.Type())
	}
	if p.Value() != 2 || p.Hint().Label != "snare" {
		t.Errorf("got value %d (%q), want 2 (snare)", p.Value(), p.Hint().Label)
	}
	i := p.Int()
	names := []string{}
	for v := i.Range().Min; v <= i.Range().Max; v++ {
		names = append(names, i.StringOf(v))
	}
	if !slicesEqual(names, []string{"none", "kick", "snare"}) {
		t.Errorf("got choices %q", names)
	}
	p.SetValue(1)
	if got := m.d.Song.Patch[0].Units[0].Parameters["buffer"]; got != 3 {
		t.Errorf("choosing kick stored buffer ID %d, want 3", got)
	}
	// deleting the buffer leaves the unit pointing at a missing buffer
	m.d.BufferIndex = 0
	m.Buffer().Delete().Do()
	if p.Hint().Label != "missing" {
		t.Errorf("after deleting the buffer, got %q, want missing", p.Hint().Label)
	}
	// loop points are hidden unless looping
	for _, q := range m.derived.patch[0].params[0] {
		if q.Name() == "loopstart" {
			t.Errorf("loopstart shown although loop is off")
		}
	}
}

func newPresetTestModel(t *testing.T) *Model {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.EncodingPresets = sointu.EncodingPresets{
			{Name: "Opus", Encoding: sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus"}}},
			{Name: "FLAC", Encoding: sointu.Encoding{Format: "flac"}},
		}
		m.d.Song.Buffers = sointu.Buffers{
			{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: sointu.Blob{1}, Preset: "Opus"}},
			{ID: 2, Channels: 1, Sample: &sointu.AudioSample{Data: sointu.Blob{2}, Preset: "Opus"}},
		}
	}()
	return m
}

func encodingOf(t *testing.T, m *Model, i int) sointu.Encoding {
	t.Helper()
	e, err := m.d.Song.SampleEncoding(m.d.Song.Buffers[i].Sample)
	if err != nil {
		t.Fatalf("SampleEncoding: %v", err)
	}
	return e
}

func TestEditingPresetChangesAllItsSamples(t *testing.T) {
	m := newPresetTestModel(t)
	if m.Buffer().PresetUsers() != 2 {
		t.Errorf("got %d users, want 2", m.Buffer().PresetUsers())
	}
	m.Buffer().Args().SetValue("-c:a libopus -b:a 24k")
	for i := range 2 {
		if got := encodingOf(t, m, i).Args; !slicesEqual(got, []string{"-c:a", "libopus", "-b:a", "24k"}) {
			t.Errorf("buffer %d: got args %q", i, got)
		}
	}
	m.History().Undo().Do()
	if got := encodingOf(t, m, 1).Args; len(got) != 2 {
		t.Errorf("undo did not restore the preset: %q", got)
	}
}

func TestCustomEncodingOverridesPreset(t *testing.T) {
	m := newPresetTestModel(t)
	custom := m.Buffer().Preset().Range().Max
	m.Buffer().Preset().SetValue(custom)
	if !m.Buffer().IsCustom() || m.Buffer().Preset().Value() != custom {
		t.Fatalf("choosing Custom did not stick: value %d", m.Buffer().Preset().Value())
	}
	if got := encodingOf(t, m, 0); got.Format != "ogg" {
		t.Errorf("custom encoding should start from the preset, got %+v", got)
	}
	m.Buffer().Format().SetValue("flac")
	if encodingOf(t, m, 0).Format != "flac" || encodingOf(t, m, 1).Format != "ogg" {
		t.Errorf("editing the custom encoding changed other samples or the preset")
	}
	m.Buffer().Preset().SetValue(1) // FLAC preset
	if m.Buffer().IsCustom() || m.d.Song.Buffers[0].Sample.Preset != "FLAC" {
		t.Errorf("choosing a preset did not drop the custom encoding")
	}
}

func TestRenameAndDeletePreset(t *testing.T) {
	m := newPresetTestModel(t)
	if m.Buffer().PresetName().SetValue("FLAC") {
		t.Errorf("renaming to an existing name should fail")
	}
	m.Buffer().PresetName().SetValue("Small")
	for i := range 2 {
		if p := m.d.Song.Buffers[i].Sample.Preset; p != "Small" {
			t.Errorf("buffer %d still uses %q", i, p)
		}
	}
	m.Buffer().DeletePreset().Do()
	if _, ok := m.d.Song.EncodingPresets.Find("Small"); ok {
		t.Errorf("preset not deleted")
	}
	for i := range 2 {
		if !m.d.Song.Buffers[i].Sample.Encoding.Equal(sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus"}}) {
			t.Errorf("buffer %d did not keep the deleted preset's encoding", i)
		}
	}
}

func TestNewPreset(t *testing.T) {
	m := newPresetTestModel(t)
	m.Buffer().NewPreset().Do()
	if got := m.d.Song.Buffers[0].Sample.Preset; got != "Opus 2" {
		t.Fatalf("new preset is %q, want Opus 2", got)
	}
	m.Buffer().Args().SetValue("-c:a libopus -b:a 96k")
	if len(encodingOf(t, m, 1).Args) != 2 {
		t.Errorf("editing the new preset changed the other sample's preset")
	}
}

func TestSongWithoutPresetsGetsDefaults(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	m.defaultPresets = sointu.EncodingPresets{
		{Name: "Opus", Encoding: sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus"}}},
	}
	func() {
		defer m.change("LoadSong", SongChange, MajorChange)()
		m.d.Song.EncodingPresets = nil
		m.d.Song.Buffers = sointu.Buffers{
			{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: sointu.Blob{1}, Encoding: &sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus"}}}},
			{ID: 2, Channels: 1, Sample: &sointu.AudioSample{Data: sointu.Blob{2}, Encoding: &sointu.Encoding{Format: "flac"}}},
		}
	}()
	if _, ok := m.d.Song.EncodingPresets.Find("Opus"); !ok {
		t.Fatalf("default presets not added: %+v", m.d.Song.EncodingPresets)
	}
	if s := m.d.Song.Buffers[0].Sample; s.Encoding != nil || s.Preset != "Opus" {
		t.Errorf("matching encoding not linked to its preset: %+v", s)
	}
	if s := m.d.Song.Buffers[1].Sample; s.Encoding == nil {
		t.Errorf("non-matching encoding should stay custom")
	}
}

func TestSongWithoutSamplesGetsNoPresets(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	if len(m.d.Song.EncodingPresets) != 0 {
		t.Errorf("a song without samples got presets")
	}
}

func TestFormatChoice(t *testing.T) {
	m := newPresetTestModel(t)
	fc := m.Buffer().FormatChoice()
	if fc.String() != "Ogg" || m.Buffer().IsCustomFormat() {
		t.Errorf("got %q, want Ogg", fc.String())
	}
	fc.SetValue(3) // FLAC
	if got := encodingOf(t, m, 0).Format; got != "flac" {
		t.Errorf("format is %q, want flac", got)
	}
	fc.SetValue(fc.Range().Max) // Custom
	if !m.Buffer().IsCustomFormat() || encodingOf(t, m, 0).Format != "flac" {
		t.Errorf("choosing Custom should keep the format and show the text field")
	}
	m.Buffer().Format().SetValue("matroska")
	if got := encodingOf(t, m, 0).Format; got != "matroska" || fc.String() != "Custom" {
		t.Errorf("got %q (%s), want matroska (Custom)", got, fc.String())
	}
	fc.SetValue(0)
	if got := encodingOf(t, m, 0).Format; got != "" || m.Buffer().IsCustomFormat() {
		t.Errorf("Keep original: got format %q", got)
	}
}
