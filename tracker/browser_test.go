package tracker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// browserTree makes a folder of audio files, other files and folders to
// browse, and returns its path.
func browserTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"b.wav", "A.flac", "notes.txt", ".hidden.wav", "kicks/k1.wav", "kicks/sub/s.wav", "Snares/.keep"} {
		path := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, testWav(make([]int16, 4410)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// newBrowserTestModel returns a model whose browser decodes a file to two
// frames holding its number in the calls of decode, and the calls.
func newBrowserTestModel(t *testing.T) (*Model, *Broker, *[]string) {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	drainPlayer(broker)
	calls := new([]string)
	m.browser.decode = func(ctx context.Context, path string) (sointu.BufferAudio, error) {
		*calls = append(*calls, filepath.Base(path))
		v := float32(len(*calls))
		return sointu.BufferAudio{Channels: 1, Data: []float32{v, v}}, nil
	}
	return m, broker, calls
}

func browserNames(m *Model) []string {
	var names []string
	for _, e := range m.Browser().Entries() {
		names = append(names, e.Name)
	}
	return names
}

// nextPreview passes the messages to the model until the player is sent a
// preview, and returns it.
func nextPreview(t *testing.T, m *Model, broker *Broker) PreviewMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-broker.ToModel:
			m.ProcessMsg(msg)
		case msg := <-broker.ToPlayer:
			if p, ok := msg.(PreviewMsg); ok {
				return p
			}
		case <-deadline:
			t.Fatal("no preview sent to the player")
		}
	}
}

func TestBrowserListsFoldersAndAudioFiles(t *testing.T) {
	m, _, _ := newBrowserTestModel(t)
	m.Browser().SetRoot(browserTree(t))
	if got, want := browserNames(m), []string{"kicks", "Snares", "A.flac", "b.wav"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBrowserNavigation(t *testing.T) {
	m, _, _ := newBrowserTestModel(t)
	root := browserTree(t)
	b := m.Browser()
	b.SetRoot(root)
	b.Right() // expands kicks
	if got, want := browserNames(m), []string{"kicks", "sub", "k1.wav", "Snares", "A.flac", "b.wav"}; !slices.Equal(got, want) {
		t.Fatalf("after Right: got %v, want %v", got, want)
	}
	b.Right() // into kicks
	b.Right() // expands sub
	b.Right() // into sub
	if e, _ := b.Selected(); e.Name != "s.wav" || e.Depth != 2 {
		t.Fatalf("got %+v, want s.wav at depth 2", e)
	}
	b.Left() // to sub
	b.Left() // collapses sub
	b.Left() // to kicks
	if e, _ := b.Selected(); e.Name != "kicks" || !e.Expanded {
		t.Fatalf("got %+v, want kicks expanded", e)
	}
	b.Left() // collapses kicks, and what was expanded inside
	b.Right()
	if got := browserNames(m); slices.Contains(got, "s.wav") {
		t.Errorf("sub stayed expanded inside kicks: %v", got)
	}
	b.Left()
	b.Left() // at the top: the parent becomes the root
	if b.Root() != filepath.Dir(root) {
		t.Fatalf("root %s, want %s", b.Root(), filepath.Dir(root))
	}
	if e, _ := b.Selected(); e.Path != root || !e.Expanded {
		t.Errorf("got %+v, want the old root selected and expanded", e)
	}
}

func TestBrowserPreviewsSelectedFile(t *testing.T) {
	m, broker, calls := newBrowserTestModel(t)
	b := m.Browser()
	b.SetRoot(browserTree(t))
	b.List().SetSelected(2) // A.flac
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 2 || p.Audio.Data[0] != 1 {
		t.Fatalf("got %+v, want the decoded A.flac", p.Audio)
	}
	b.List().SetSelected(3) // b.wav
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("the preview of A.flac was not stopped first: %+v", p.Audio)
	}
	if p := nextPreview(t, m, broker); p.Audio.Data[0] != 2 {
		t.Fatalf("got %+v, want the decoded b.wav", p.Audio)
	}
	b.List().SetSelected(2) // decoded lately: plays without a decode
	nextPreview(t, m, broker)
	if p := nextPreview(t, m, broker); p.Audio.Data[0] != 1 || len(*calls) != 2 {
		t.Fatalf("got %+v after %v, want A.flac from the cache", p.Audio, *calls)
	}
	b.List().SetSelected(0) // a folder stops the preview
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("selecting a folder did not stop the preview: %+v", p.Audio)
	}
	b.List().SetSelected(3)
	nextPreview(t, m, broker)
	b.Stop()
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("Stop did not stop the preview: %+v", p.Audio)
	}
}

func TestBrowserCancelsStaleDecode(t *testing.T) {
	m, broker, _ := newBrowserTestModel(t)
	cancelled := make(chan string, 4)
	release := make(chan struct{})
	m.browser.decode = func(ctx context.Context, path string) (sointu.BufferAudio, error) {
		if filepath.Base(path) == "A.flac" {
			select {
			case <-ctx.Done():
				cancelled <- filepath.Base(path)
				return sointu.BufferAudio{}, ctx.Err()
			case <-release:
			}
		}
		return sointu.BufferAudio{Channels: 1, Data: []float32{3}}, nil
	}
	b := m.Browser()
	b.SetRoot(browserTree(t))
	b.List().SetSelected(2) // A.flac, which blocks
	gen := m.browser.gen
	b.List().SetSelected(3) // b.wav
	select {
	case name := <-cancelled:
		if name != "A.flac" {
			t.Fatalf("cancelled %s", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the decode of A.flac was not cancelled")
	}
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("the preview of A.flac was not stopped first: %+v", p.Audio)
	}
	if p := nextPreview(t, m, broker); p.Audio.Data[0] != 3 {
		t.Fatalf("got %+v, want b.wav", p.Audio)
	}
	// a result of the old decode that comes late is not played
	b.decoded(gen, filepath.Join(b.Root(), "A.flac"), fileStamp{}, sointu.BufferAudio{Channels: 1, Data: []float32{9}}, nil)
	select {
	case msg := <-broker.ToPlayer:
		if p, ok := msg.(PreviewMsg); ok {
			t.Fatalf("a stale decode was played: %+v", p.Audio)
		}
	default:
	}
}

func TestBrowserLoadsIntoSelectedBuffer(t *testing.T) {
	m, broker := newBufferTestModel(t)
	b := m.Browser()
	m.browser.decode = func(ctx context.Context, path string) (sointu.BufferAudio, error) {
		return sointu.BufferAudio{}, nil
	}
	root := browserTree(t)
	b.SetRoot(root)
	waitBuffers := func(n int, file string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			if bufs := m.d.Song.Buffers; len(bufs) == n && bufs[m.d.BufferIndex].Sample != nil && bufs[m.d.BufferIndex].Sample.FileName == file {
				return
			}
			select {
			case msg := <-broker.ToModel:
				m.ProcessMsg(msg)
			case <-deadline:
				m.Alerts().Iterate(func(_ int, a Alert) bool { t.Log(a.Message); return true })
				t.Fatalf("timed out waiting for %d buffers with %s selected", n, file)
			}
		}
	}
	b.List().SetSelected(3) // b.wav: no buffers, so a new one
	b.Enter()
	waitBuffers(1, "b.wav")
	if m.d.Song.Buffers[0].Name != "b" {
		t.Errorf("buffer named %q, want b", m.d.Song.Buffers[0].Name)
	}
	b.List().SetSelected(2) // A.flac replaces it
	b.Enter()
	waitBuffers(1, "A.flac")
	m.History().Undo().Do()
	if s := m.d.Song.Buffers[0].Sample; s == nil || s.FileName != "b.wav" {
		t.Errorf("undo left %+v, want b.wav back", s)
	}
	if b.LastFolder() != root {
		t.Errorf("last folder %q, want %q", b.LastFolder(), root)
	}
	// several files add several buffers, in order; other files are left out
	m.Buffer().ImportFiles([]string{filepath.Join(root, "notes.txt"), filepath.Join(root, "kicks/k1.wav"), filepath.Join(root, "kicks/sub/s.wav")}, false)
	waitBuffers(3, "s.wav")
	if got := m.d.Song.Buffers[1].Sample.FileName; got != "k1.wav" {
		t.Errorf("second buffer %s, want k1.wav", got)
	}
}

func TestBrowserWithoutAutoPreview(t *testing.T) {
	m, broker, calls := newBrowserTestModel(t)
	b := m.Browser()
	b.SetRoot(browserTree(t))
	b.List().SetSelected(2) // A.flac
	nextPreview(t, m, broker)
	b.AutoPreview().SetValue(false)
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("turning AutoPreview off did not stop the preview: %+v", p.Audio)
	}
	b.List().SetSelected(3) // b.wav: not played
	if len(*calls) != 1 || m.browser.previewing != "" {
		t.Fatalf("b.wav was decoded or played without AutoPreview: %v", *calls)
	}
	b.PlaySelected().Do()
	if p := nextPreview(t, m, broker); p.Audio.Data[0] != 2 {
		t.Fatalf("got %+v, want b.wav played by PlaySelected", p.Audio)
	}
	b.List().SetSelected(2) // leaving the file stops it
	if p := nextPreview(t, m, broker); p.Audio.Frames() != 0 {
		t.Fatalf("leaving b.wav did not stop its preview: %+v", p.Audio)
	}
}

func TestBrowserWatchesFolders(t *testing.T) {
	browserWatchInterval = 10 * time.Millisecond
	t.Cleanup(func() { browserWatchInterval = time.Second })
	m, broker, _ := newBrowserTestModel(t)
	b := m.Browser()
	root := browserTree(t)
	b.SetRoot(root)
	b.Right() // expands kicks
	waitFor := func(name string, want bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for slices.Contains(browserNames(m), name) != want {
			select {
			case msg := <-broker.ToModel:
				m.ProcessMsg(msg)
			case <-deadline:
				t.Fatalf("%s shown: %v, want %v: %v", name, !want, want, browserNames(m))
			}
		}
	}
	os.WriteFile(filepath.Join(root, "new.wav"), nil, 0o644)
	waitFor("new.wav", true)
	os.WriteFile(filepath.Join(root, "kicks", "k2.wav"), nil, 0o644)
	waitFor("k2.wav", true)
	os.Remove(filepath.Join(root, "b.wav"))
	waitFor("b.wav", false)
}

func TestBrowserDecodesChangedFileAgain(t *testing.T) {
	m, broker, calls := newBrowserTestModel(t)
	b := m.Browser()
	root := browserTree(t)
	b.SetRoot(root)
	b.List().SetSelected(2) // A.flac
	nextPreview(t, m, broker)
	b.List().SetSelected(3)
	nextPreview(t, m, broker)
	nextPreview(t, m, broker)
	os.WriteFile(filepath.Join(root, "A.flac"), make([]byte, 7), 0o644)
	b.List().SetSelected(2)
	nextPreview(t, m, broker)
	nextPreview(t, m, broker)
	if len(*calls) != 3 {
		t.Errorf("decodes %v, want A.flac decoded again after it changed", *calls)
	}
}
