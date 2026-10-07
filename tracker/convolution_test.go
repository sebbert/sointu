package tracker

import (
	"strings"
	"testing"

	"github.com/vsariola/sointu"
)

func convolutionAlert(m *Model) string {
	for _, a := range m.Alerts().Iterate {
		if a.Name == "Convolution" && a.Duration > 0 { // a cleared alert has none
			return a.Message
		}
	}
	return ""
}

func responseBuffer(m *Model) (sointu.Buffer, bool) {
	for _, b := range m.d.Song.Buffers {
		if b.Auto && b.IsAudio() {
			return b, true
		}
	}
	return sointu.Buffer{}, false
}

// TestConvolutionPresetBuffer checks that the module preset Convolution
// reverb comes with the buffer of its response: as long as its longest
// decay, stereo, with audio for the synth; with an ID of its own when the
// song has a buffer with the ID of the preset; deleted with the module, and
// back with undo.
func TestConvolutionPresetBuffer(t *testing.T) {
	m, _ := newModuleTestModel(t)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: 1, Name: "taken", Channels: 1, Frames: 100})
	}()
	m.modulePresetPath = t.TempDir()
	m.loadModulePresets()
	m.Instrument().Tab().SetValue(int(InstrumentModulesTab))
	presets := m.Module().Presets()
	found := false
	for i := presets.Range().Min; i <= presets.Range().Max; i++ {
		if presets.StringOf(i) == "Convolution reverb" {
			found = presets.SetValue(i)
		}
	}
	if !found {
		t.Fatal("no module preset Convolution reverb")
	}
	buf, ok := responseBuffer(m)
	if !ok {
		t.Fatalf("the song has no buffer for the response: %+v", m.d.Song.Buffers)
	}
	if buf.ID == 1 || buf.Channels != 2 || buf.Frames != sointu.ConvolutionLength(97) || !buf.Writable() {
		t.Errorf("the buffer of the response is %+v", buf)
	}
	mod := m.d.Song.Modules[len(m.d.Song.Modules)-1]
	for _, u := range mod.Units {
		if (u.Type == "bufwrite" || u.Type == "convolution") && u.Parameters["buffer"] != buf.ID {
			t.Errorf("the %s unit of the module has the buffer %d, the response is %d", u.Type, u.Parameters["buffer"], buf.ID)
		}
	}
	if audio := m.BufferAudio()[buf.ID]; audio.Frames() != buf.Frames || audio.Channels != 2 || !audio.Writable {
		t.Errorf("the audio of the response has %d frames of %d channels", audio.Frames(), audio.Channels)
	}
	if msg := convolutionAlert(m); msg != "" {
		t.Errorf("alert: %s", msg)
	}
	m.Module().Delete().Do()
	if _, ok := responseBuffer(m); ok {
		t.Errorf("the buffer of the response is still there without the module")
	}
	if _, ok := m.d.Song.Buffers.Find(1); !ok {
		t.Errorf("the buffer of the song is gone")
	}
	m.History().Undo().Do()
	if _, ok := responseBuffer(m); !ok {
		t.Errorf("undo does not bring the buffer of the response back")
	}
}

// TestConvolutionWarnings checks the alerts for a convolution unit without
// a buffer and for units that take too much time, and what the parameters
// of the unit show.
func TestConvolutionWarnings(t *testing.T) {
	m, _ := newModuleTestModel(t)
	units := m.d.Song.Patch[0].Units
	set := func(voices int, params sointu.ParamMap) {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Buffers = sointu.Buffers{{ID: 3, Name: "written", Channels: 2, Frames: 44100}}
		u := sointu.MakeUnit("convolution")
		for k, v := range params {
			u.Parameters[k] = v
		}
		m.d.Song.Patch[0].NumVoices = voices
		m.d.Song.Patch[0].Units = append([]sointu.Unit{u}, units...)
	}
	set(1, nil)
	if msg := convolutionAlert(m); !strings.Contains(msg, "without a buffer") {
		t.Errorf("a unit without a buffer: alert %q", msg)
	}
	set(1, sointu.ParamMap{"buffer": 3, "stereo": 1, "length": 104, "follow": 3})
	if msg := convolutionAlert(m); msg != "" {
		t.Errorf("one long stereo unit: alert %q", msg)
	}
	set(8, sointu.ParamMap{"buffer": 3, "stereo": 1, "length": 104, "follow": 3})
	if msg := convolutionAlert(m); !strings.Contains(msg, "% of a processor core") {
		t.Errorf("eight voices of it: alert %q", msg)
	}
	set(1, sointu.ParamMap{"buffer": 3, "length": 96, "start": 4410})
	hints := map[string]string{}
	m.d.InstrIndex, m.d.UnitIndex = 0, 0
	for _, p := range m.deriveParams(&m.d.Song.Patch[0].Units[0], nil) {
		hints[p.Name()] = p.Hint().Label
	}
	if got := hints["length"]; got != "5.94 s (262144), the buffer has 0.90 s" {
		t.Errorf("the hint of length: %q", got)
	}
	if got := hints["start"]; got != "0.10 s (4410)" {
		t.Errorf("the hint of start: %q", got)
	}
	if _, ok := hints["follow"]; !ok {
		t.Errorf("no follow for a written buffer: %v", hints)
	}
}
