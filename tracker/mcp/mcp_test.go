package mcp

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// StartTestHost runs a model, as the tracker would, and a Host for it that
// listens, with the user's configuration directory in a directory of the
// test. It returns the Host and its Info.
func startTestHost(t *testing.T) (*Host, Info) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { // the goroutine of the GUI
		defer close(finished)
		for {
			select {
			case msg := <-broker.ToModel:
				model.ProcessMsg(msg)
			case <-broker.ToPlayer:
			case <-done:
				return
			}
		}
	}()
	host := NewHost(model, "sointu-test")
	if err := host.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		host.Close()
		close(done)
		<-finished
		model.Close()
	})
	instances, err := Instances()
	if err != nil || len(instances) != 1 {
		t.Fatalf("the host is not listed: %v %v", instances, err)
	}
	return host, instances[0]
}

func callTool(t *testing.T, info Info, tool string, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return Call(info.Socket, tool, raw)
}

func TestHostOverSocket(t *testing.T) {
	host, info := startTestHost(t)
	if info.ID != host.id || len(info.Instruments) != 2 || info.Instruments[0] != "Instr" {
		t.Errorf("the info of the host is not complete: %+v", info)
	}
	if fi, err := os.Stat(info.Socket); err != nil || fi.Mode().Perm()&0077 != 0 {
		t.Errorf("the socket is not the user's only: %v %v", fi.Mode(), err)
	}
	song, err := callTool(t, info, "get_song", struct{}{})
	if err != nil || !strings.Contains(song, `0 "Instr"`) {
		t.Fatalf("get_song: %v\n%s", err, song)
	}
	text, err := callTool(t, info, "edit_units", map[string]any{"edits": []map[string]any{{"unit": 1, "params": map[string]any{"attack": 3}}}})
	if err != nil || !strings.Contains(text, "attack=3(") {
		t.Fatalf("edit_units: %v\n%s", err, text)
	}
	if _, err := callTool(t, info, "edit_units", map[string]any{"edit": []any{}}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("a misspelled argument was not refused: %v", err)
	}
	if _, err := callTool(t, info, "no_such_tool", struct{}{}); err == nil {
		t.Error("a tool that is not there was called")
	}
	if _, err := callTool(t, info, "undo", struct{}{}); err != nil {
		t.Errorf("undo: %v", err)
	}
	text, err = callTool(t, info, "get_instrument", map[string]any{"instrument": "0"})
	if err != nil || !strings.Contains(text, "attack=64(") {
		t.Errorf("undo did not bring the attack back: %v\n%s", err, text)
	}
	text, err = callTool(t, info, "render_note", map[string]any{"instrument": "Instr", "notes": []int{69}, "wav": true})
	if err != nil || !strings.Contains(text, "pitch 220.0 Hz (A3)") {
		t.Fatalf("render_note: %v\n%s", err, text)
	}
	_, path, _ := strings.Cut(text, "\nwav: ")
	if fi, err := os.Stat(path); err != nil || fi.Size() < 44100*8 {
		t.Errorf("the wav file was not written: %v", err)
	}
	os.Remove(path)
	if text, err = callTool(t, info, "play_note", map[string]any{"instrument": "0", "duration_ms": 20}); err != nil || !strings.Contains(text, "playing note 60") {
		t.Errorf("play_note: %v %s", err, text)
	}
	// turned off, the host is gone
	host.SetEnabled(false)
	if instances, _ := Instances(); len(instances) != 0 {
		t.Errorf("the host is still listed when turned off: %v", instances)
	}
	if _, err := os.Stat(info.Socket); !os.IsNotExist(err) {
		t.Error("the socket is still there")
	}
}

func TestStaleHostFilesAreRemoved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0700)
	file := filepath.Join(dir, "sointu-gone-1-1.json")
	os.WriteFile(file, []byte(`{"id":"sointu-gone-1-1","pid":999999999,"socket":"/nonexistent.sock"}`), 0600)
	instances, err := Instances()
	if err != nil || len(instances) != 0 {
		t.Errorf("a host that is gone was listed: %v %v", instances, err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the file of a host that is gone was not removed")
	}
	if _, err := Choose(instances, ""); err == nil || !strings.Contains(err.Error(), "Enable MCP") {
		t.Errorf("without hosts, the user is not told how to turn it on: %v", err)
	}
}

func TestMeasure(t *testing.T) {
	// a sine of 110 Hz at -6 dBFS for 0.5 s, then 0.5 s of silence
	buf := make(sointu.AudioBuffer, 44100)
	for i := 0; i < 22050; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*110*float64(i)/44100))
		buf[i] = [2]float32{v, v}
	}
	m := Measure(buf, 22050)
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s is %v, not %v", name, got, want)
		}
	}
	near("peak", m.Peak, -6.02, 0.05)
	near("rms", m.RMS, -9.03-3.01, 0.1) // half of the time silent
	near("crest", m.Crest, 6.02, 0.1)
	near("correlation", m.Correlation, 1, 1e-6)
	near("pitch", m.Pitch, 110, 0.5)
	if m.SideToMid > -100 {
		t.Errorf("side/mid of a mono signal is %v", m.SideToMid)
	}
	loudest := m.Bands[0]
	for _, b := range m.Bands {
		if b.Level > loudest.Level {
			loudest = b
		}
	}
	if loudest.Center != 125 {
		t.Errorf("the loudest band is %v Hz, not 125", loudest.Center)
	}
	total := 0.0
	for _, b := range m.Bands {
		total += math.Pow(10, b.Level/10)
	}
	near("the bands added up", 10*math.Log10(total), m.RMS, 0.2)
	near("the tail", m.TailTo60, 0, 0.011)
	if s := m.String(); !strings.Contains(s, "pitch 110.0 Hz (A2)") || !strings.Contains(s, "125:-") {
		t.Errorf("the measurements do not read as expected:\n%s", s)
	}
	if !Measure(make(sointu.AudioBuffer, 1000), -1).Silent {
		t.Error("silence is not silent")
	}
	// noise has no pitch
	noise := make(sointu.AudioBuffer, 8192)
	x := uint32(1)
	for i := range noise {
		x = x*1664525 + 1013904223
		v := float32(x)/float32(1<<32) - 0.5
		noise[i] = [2]float32{v, -v}
	}
	if m := Measure(noise, -1); m.Pitch != 0 || math.Abs(m.Correlation+1) > 1e-6 {
		t.Errorf("noise has the pitch %v and the correlation %v", m.Pitch, m.Correlation)
	}
}
