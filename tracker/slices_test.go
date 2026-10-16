package tracker

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vsariola/sointu"
)

func TestSliceMarkersAreUndoableAndReachPlayer(t *testing.T) {
	m, broker := newBufferTestModel(t)
	samples := make([]int16, 1000)
	sample := &sointu.AudioSample{Data: testWav(samples), Encoding: &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}}
	setBuffers(m, sointu.Buffers{{ID: 3, Channels: 1, Sample: sample}})
	processBufferResults(t, m, broker, 3)
	drainPlayer(broker)
	b := m.Buffer()
	b.AddSlice(500)
	b.AddSlice(200)
	b.AddSlice(200)  // a duplicate
	b.AddSlice(5000) // beyond the end
	if got := b.Slices(); !slices.Equal(got, []int{200, 500}) {
		t.Fatalf("got markers %v, want [200 500]", got)
	}
	if got := lastBufferAudio(t, broker)[3].Slices; !slices.Equal(got, []int{200, 500}) {
		t.Errorf("the player got markers %v", got)
	}
	b.MoveSlice(0, 700)
	if got := b.Slices(); !slices.Equal(got, []int{500, 700}) {
		t.Fatalf("after moving, got %v", got)
	}
	b.DeleteSlice(1)
	if got := b.Slices(); !slices.Equal(got, []int{500}) {
		t.Fatalf("after deleting, got %v", got)
	}
	m.History().Undo().Do()
	if got := b.Slices(); !slices.Equal(got, []int{500, 700}) {
		t.Fatalf("after undo, got %v", got)
	}
	b.SliceCount().SetValue(4)
	b.SliceEqually().Do()
	if got := b.Slices(); !slices.Equal(got, []int{250, 500, 750}) {
		t.Fatalf("sliced equally: %v", got)
	}
	b.ClearSlices().Do()
	if got := b.Slices(); got != nil {
		t.Fatalf("cleared: %v", got)
	}
}

func TestDetectOnsets(t *testing.T) {
	// decaying noise bursts at these frames, on a quiet hum
	hits := []int{5000, 16000, 20000, 33000}
	n := 44100
	audio := make([]float32, n)
	seed := uint32(1)
	for i := range audio {
		audio[i] = 0.001 * float32(math.Sin(float64(i)*0.01))
		for _, h := range hits {
			if i >= h {
				seed = seed*1664525 + 1013904223
				audio[i] += float32(int32(seed)) / (1 << 31) * float32(math.Exp(-float64(i-h)/1500)) * 0.5
			}
		}
	}
	got := detectOnsets(func(i int) float32 { return audio[i] }, n, 50)
	if len(got) != len(hits) {
		t.Fatalf("got %v, want near %v", got, hits)
	}
	for i, h := range hits {
		if got[i] > h || got[i] < h-64 {
			t.Errorf("onset %d at %d, want just before %d", i, got[i], h)
		}
	}
	if got := detectOnsets(func(int) float32 { return 0 }, n, 100); got != nil {
		t.Errorf("silence has onsets: %v", got)
	}
}

func TestApplySampleEdit(t *testing.T) {
	a := sointu.BufferAudio{Channels: 1, Data: []float32{0, 0, 1, 2, 3, 4, 5, 0}}
	check := func(e sampleEdit, want []float32, markers, wantMarkers []int) {
		t.Helper()
		out, move, err := applySampleEdit(a, e)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(out.Data, want) {
			t.Errorf("%+v: got %v, want %v", e, out.Data, want)
		}
		if got := moveSlices(markers, move, out.Frames()); !slices.Equal(got, wantMarkers) {
			t.Errorf("%+v: markers %v, want %v", e, got, wantMarkers)
		}
	}
	check(sampleEdit{kind: editCrop, start: 2, end: 5}, []float32{1, 2, 3}, []int{1, 3, 4, 6}, []int{1, 2})
	check(sampleEdit{kind: editDelete, start: 2, end: 5}, []float32{0, 0, 4, 5, 0}, []int{1, 3, 6}, []int{1, 3})
	check(sampleEdit{kind: editTrimSilence, threshold: 0.5}, []float32{1, 2, 3, 4, 5}, []int{3}, []int{1})
	check(sampleEdit{kind: editFadeIn, length: 4, curve: 1}, []float32{0, 0, 0.5, 1.5, 3, 4, 5, 0}, []int{3}, []int{3})
	check(sampleEdit{kind: editFadeOut, length: 4, curve: 2}, []float32{0, 0, 1, 2, 3 * 0.5625, 4 * 0.25, 5 * 0.0625, 0}, nil, nil)
	if _, _, err := applySampleEdit(sointu.BufferAudio{Channels: 1, Data: make([]float32, 8)}, sampleEdit{kind: editTrimSilence, threshold: 0.1}); err != errSilent {
		t.Errorf("trimming silence: %v", err)
	}
}

func TestSampleEditMovesMarkers(t *testing.T) {
	m, broker := newBufferTestModel(t)
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = int16(i * 16)
	}
	sample := &sointu.AudioSample{Data: testWav(samples), Preset: ""}
	sample.Encoding = &sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	setBuffers(m, sointu.Buffers{{ID: 1, Channels: 1, Sample: sample, Slices: []int{100, 300, 600}}})
	processBufferResults(t, m, broker, 1)
	b := m.Buffer()
	b.SetSelection(200, 700)
	if !b.Crop().Enabled() {
		t.Fatal("crop is not enabled")
	}
	b.Crop().Do()
	deadline := time.After(10 * time.Second)
	for b.IsEditing() {
		select {
		case msg := <-broker.ToModel:
			m.ProcessMsg(msg)
		case <-deadline:
			t.Fatal("timed out")
		}
	}
	if got := b.Slices(); !slices.Equal(got, []int{100, 400}) {
		t.Errorf("markers after crop: %v", got)
	}
	processBufferResults(t, m, broker, 1)
	audio, _, _ := b.Waveform()
	if audio.Frames() != 500 || math.Abs(float64(audio.Data[0]-float32(200*16)/32768)) > 1e-4 {
		t.Errorf("cropped audio: %d frames, first %v", audio.Frames(), audio.Data[:min(1, len(audio.Data))])
	}
	m.History().Undo().Do()
	if got := b.Slices(); !slices.Equal(got, []int{100, 300, 600}) {
		t.Errorf("markers after undo: %v", got)
	}
}

// The MCP server sets slice markers, lists them, and shows the slice
// parameters of a bufread unit only with slices on.
func TestRemoteSlices(t *testing.T) {
	m, r := newRemoteTestModel(t)
	setBuffers(m, sointu.Buffers{{ID: 4, Name: "rec", Channels: 1, Frames: 8000}})
	m.syncBuffers()
	remoteStep(t, m, func() (string, error) { return r.SetSlices(4, nil, 4, 0, false) })
	if got := m.d.Song.Buffers[0].Slices; !slices.Equal(got, []int{2000, 4000, 6000}) {
		t.Fatalf("equal slices: %v", got)
	}
	remoteStep(t, m, func() (string, error) { return r.SetSlices(4, []int{3000, 100}, 0, 0, false) })
	if got := m.d.Song.Buffers[0].Slices; !slices.Equal(got, []int{100, 3000}) {
		t.Fatalf("slices by frames: %v", got)
	}
	if !strings.Contains(r.Song(), "3 slices, markers at frames 100 3000") {
		t.Errorf("get_song does not list the slices:\n%s", r.Song())
	}
	if _, err := r.SetSlices(99, nil, 4, 0, false); err == nil {
		t.Error("slices of a missing buffer")
	}
	first := m.d.Song.Patch[0].Units[0].ID
	remoteStep(t, m, func() (string, error) {
		return r.AddUnits("", "", 0, first, []RemoteNewUnit{{Type: "bufread", Params: map[string]any{"buffer": 1, "loop": 1}}})
	})
	shown := func() map[string]Parameter {
		ret := map[string]Parameter{}
		for x := 0; x < m.Params().RowWidth(0); x++ {
			if p := m.Params().Item(Point{x, 0}); p.up != nil {
				ret[p.up.Name] = p
			}
		}
		return ret
	}
	s := shown()
	if _, ok := s["slice"]; ok {
		t.Error("the slice is shown with slices off")
	}
	func() {
		defer m.change("test", PatchChange, MajorChange)()
		m.d.Song.Patch[0].Units[0].Parameters["slices"] = 2
		m.d.Song.Patch[0].Units[0].Parameters["looplength"] = 64
	}()
	s = shown()
	for _, name := range []string{"slice", "sliceend"} {
		if _, ok := s[name]; !ok {
			t.Errorf("%s is not shown with slices note", name)
		}
	}
	if _, ok := s["notetracking"]; ok {
		t.Error("notetracking is shown with slices note")
	}
	if p := s["looplength"]; p.Label() != "50%" {
		t.Errorf("the loop length of a sliced unit: %q", p.Label())
	}
}

// An imported WAV file with cue points gets them as slice markers, in the
// one undo step of the import.
func TestImportCuePoints(t *testing.T) {
	m, broker := newBufferTestModel(t)
	wav := testWav(make([]int16, 2000))
	cue := []byte("cue ")
	cue = binary.LittleEndian.AppendUint32(cue, 4+2*24)
	cue = binary.LittleEndian.AppendUint32(cue, 2)
	for i, f := range []uint32{500, 1500} {
		for _, v := range []uint32{uint32(i + 1), f, 0x61746164, 0, 0, f} {
			cue = binary.LittleEndian.AppendUint32(cue, v)
		}
	}
	wav = append(wav, cue...)
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	undos := len(m.undoStack)
	m.Buffer().Import(io.NopCloser(bytes.NewReader(wav)), false)
	deadline := time.After(10 * time.Second)
	for len(m.d.Song.Buffers) == 0 {
		select {
		case msg := <-broker.ToModel:
			m.ProcessMsg(msg)
		case <-deadline:
			t.Fatal("timed out")
		}
	}
	if got := m.d.Song.Buffers[0].Slices; !slices.Equal(got, []int{500, 1500}) {
		t.Errorf("the imported markers: %v", got)
	}
	if len(m.undoStack) != undos+1 {
		t.Errorf("the import made %d undo steps", len(m.undoStack)-undos)
	}
}
