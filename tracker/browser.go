package tracker

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

type (
	// BrowserModel is the sample browser: a tree of the folders and audio
	// files under a root folder, whose files play as they are selected and
	// load into buffers.
	BrowserModel Model

	browserState struct {
		root     string
		rootErr  error // why the root could not be read
		entries  []BrowserEntry
		expanded map[string]bool
		selected int
		// lastFolder is the folder of the file last loaded into a buffer,
		// from the browser, the file dialog or a drop
		lastFolder string
		// decode decodes a file for its preview; tests replace it
		decode func(ctx context.Context, path string) (sointu.BufferAudio, error)
		cancel context.CancelFunc // of the running decode
		gen    int                // counts the decodes, to tell a stale result
		// previewing is the file whose preview is decoding or playing
		previewing string
		status     string // what the preview of the selected file is doing
		// manual turns off playing a file when it is selected: it plays by
		// PlaySelected only
		manual bool
		cache      []browserDecoded
		// watched is read by the watcher, which runs while there is a root
		watched     atomic.Pointer[browserWatch]
		watchCancel context.CancelFunc
	}

	browserDecoded struct {
		path  string
		stamp fileStamp // the file as it was decoded
		audio sointu.BufferAudio
	}

	// fileStamp tells whether a file or a folder has changed.
	fileStamp struct {
		modTime time.Time
		size    int64
	}

	// browserWatch is what the watcher of the folders compares: the stamps
	// of the folders shown, as they were read.
	browserWatch map[string]fileStamp

	// BrowserEntry is a row of the sample browser: a folder or an audio
	// file, indented by its depth below the root.
	BrowserEntry struct {
		Path     string
		Name     string
		Depth    int
		Dir      bool
		Expanded bool
	}

	// BrowserRoot is a folder offered as the root of the sample browser.
	BrowserRoot struct {
		Name, Path string
	}
)

const (
	// browserPreviewSeconds is the most of a file that a preview plays.
	browserPreviewSeconds = 30
	// browserCacheSize is the number of decoded previews kept, so that
	// going back and forth among files does not decode them again.
	browserCacheSize = 8
)

// Browser returns the sample browser.
func (m *Model) Browser() *BrowserModel { return (*BrowserModel)(m) }

func (m *BrowserModel) state() *browserState { return &m.browser }

// Root returns the folder that the browser shows.
func (m *BrowserModel) Root() string { return m.browser.root }

// RootErr returns why the root could not be read, or nil.
func (m *BrowserModel) RootErr() error { return m.browser.rootErr }

// SetRoot shows the folder dir, with its subfolders collapsed and the first
// row selected, which does not play. The preview stops.
func (m *BrowserModel) SetRoot(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	b := m.state()
	b.root, b.expanded, b.selected, b.cache = dir, nil, 0, nil
	m.refresh()
	m.Stop()
}

// LastFolder returns the folder of the file last loaded into a buffer.
func (m *BrowserModel) LastFolder() string { return m.browser.lastFolder }

// SetLastFolder sets the folder of the file last loaded, e.g. from the
// preferences.
func (m *BrowserModel) SetLastFolder(dir string) { m.browser.lastFolder = dir }

// Roots returns the folders offered as roots: the home folder, the last
// folder and the folder of the song, those that are known, each once.
func (m *BrowserModel) Roots() []BrowserRoot {
	var roots []BrowserRoot
	add := func(name, path string) {
		if path == "" || slices.ContainsFunc(roots, func(r BrowserRoot) bool { return r.Path == path }) {
			return
		}
		roots = append(roots, BrowserRoot{name, path})
	}
	home, _ := os.UserHomeDir()
	add("Home", home)
	add("Last folder", m.browser.lastFolder)
	if m.d.FilePath != "" {
		add("Song folder", filepath.Dir(m.d.FilePath))
	}
	return roots
}

// Entries returns the rows of the browser.
func (m *BrowserModel) Entries() []BrowserEntry { return m.browser.entries }

// Status returns what the preview of the selected file is doing: decoding,
// or why it failed.
func (m *BrowserModel) Status() string { return m.browser.status }

// Selected returns the selected row, and false if there are none.
func (m *BrowserModel) Selected() (BrowserEntry, bool) {
	b := m.state()
	if b.selected < 0 || b.selected >= len(b.entries) {
		return BrowserEntry{}, false
	}
	return b.entries[b.selected], true
}

// List returns a List of the rows, for moving through them. Selecting a file
// previews it.
func (m *BrowserModel) List() List { return MakeList((*browserList)(m)) }

type browserList BrowserModel

func (v *browserList) Count() int        { return len(v.browser.entries) }
func (v *browserList) Selected() int     { return v.browser.selected }
func (v *browserList) Selected2() int    { return v.browser.selected }
func (v *browserList) SetSelected2(int)  {}
func (v *browserList) SetSelected(i int) { (*BrowserModel)(v).selectIndex(i) }

func (m *BrowserModel) selectIndex(i int) {
	if i == m.browser.selected {
		return
	}
	m.browser.selected = i
	m.selectionChanged()
}

// isAudioFile reports whether the loader reads the file, by its extension.
func isAudioFile(name string) bool {
	return slices.Contains(AudioFileExtensions, strings.ToLower(filepath.Ext(name)))
}

// refresh reads the folders again: the root, and the expanded folders under
// it. The selection stays on the same path if it is still there.
func (m *BrowserModel) refresh() {
	b := m.state()
	selected := ""
	if e, ok := m.Selected(); ok {
		selected = e.Path
	}
	b.entries = b.entries[:0]
	b.rootErr = nil
	if b.root == "" {
		return
	}
	if err := m.appendDir(b.root, 0); err != nil {
		b.rootErr = err
	}
	if i := slices.IndexFunc(b.entries, func(e BrowserEntry) bool { return e.Path == selected }); i >= 0 {
		b.selected = i
	}
	b.selected = min(max(b.selected, 0), len(b.entries)-1)
	m.watch()
}

// browserWatchInterval is how often the watcher looks at the folders shown.
var browserWatchInterval = time.Second

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{info.ModTime(), info.Size()}
}

// watch gives the watcher the folders shown, the root and the expanded
// folders, as they are now, and starts it if it is not running. The watcher
// stats them every browserWatchInterval, on a goroutine, and when one has
// changed (a file was added, removed or renamed in it) the browser reads
// the folders again.
func (m *BrowserModel) watch() {
	b := m.state()
	w := browserWatch{b.root: stampOf(b.root)}
	for _, e := range b.entries {
		if e.Dir && e.Expanded {
			w[e.Path] = stampOf(e.Path)
		}
	}
	b.watched.Store(&w)
	if b.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.watchCancel = cancel
	go func() {
		ticker := time.NewTicker(browserWatchInterval)
		defer ticker.Stop()
		var told *browserWatch // a change is told once, until the folders are read again
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			w := b.watched.Load()
			if w == told {
				continue
			}
			for dir, stamp := range *w {
				if stampOf(dir) != stamp {
					told = w
					TrySend(m.broker.ToModel, MsgToModel{Data: func() {
						if b.watched.Load() == w {
							m.refresh()
						}
					}})
					break
				}
			}
		}
	}()
}

// stopWatching stops the watcher.
func (m *BrowserModel) stopWatching() {
	if m.browser.watchCancel != nil {
		m.browser.watchCancel()
		m.browser.watchCancel = nil
	}
}

// appendDir appends the rows of the folders and audio files in dir, folders
// first, each sorted by name, and those of the expanded folders below them.
// Hidden files are left out.
func (m *BrowserModel) appendDir(dir string, depth int) error {
	b := m.state()
	list, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var entries []BrowserEntry
	for _, d := range list {
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		isDir := d.IsDir()
		if d.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(path); err == nil {
				isDir = info.IsDir()
			}
		}
		if !isDir && !isAudioFile(name) {
			continue
		}
		entries = append(entries, BrowserEntry{Path: path, Name: name, Depth: depth, Dir: isDir, Expanded: isDir && b.expanded[path]})
	}
	slices.SortStableFunc(entries, func(a, c BrowserEntry) int {
		if a.Dir != c.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(c.Name))
	})
	for _, e := range entries {
		b.entries = append(b.entries, e)
		if e.Expanded {
			m.appendDir(e.Path, depth+1) // an unreadable folder shows empty
		}
	}
	return nil
}

// Refresh returns an Action to read the folders again, e.g. after files
// were added to them.
func (m *BrowserModel) Refresh() Action { return MakeAction((*browserRefresh)(m)) }

type browserRefresh BrowserModel

func (m *browserRefresh) Enabled() bool { return m.browser.root != "" }
func (m *browserRefresh) Do() {
	m.browser.cache = nil
	(*BrowserModel)(m).refresh()
}

// Right expands the selected folder, or goes into it if it is expanded.
func (m *BrowserModel) Right() {
	e, ok := m.Selected()
	if !ok || !e.Dir {
		return
	}
	if !e.Expanded {
		m.setExpanded(e.Path, true)
		return
	}
	if next := m.browser.selected + 1; next < len(m.browser.entries) && m.browser.entries[next].Depth > e.Depth {
		m.selectIndex(next)
	}
}

// Left collapses the selected folder if it is expanded, or else goes to the
// folder the selection is in: its row, or at the top, the parent of the root,
// which becomes the root.
func (m *BrowserModel) Left() {
	e, ok := m.Selected()
	if ok && e.Dir && e.Expanded {
		m.setExpanded(e.Path, false)
		return
	}
	if ok && e.Depth > 0 {
		for i := m.browser.selected - 1; i >= 0; i-- {
			if m.browser.entries[i].Depth < e.Depth {
				m.selectIndex(i)
				return
			}
		}
	}
	m.Up()
}

// Up makes the parent of the root the root, with the old root selected and
// expanded.
func (m *BrowserModel) Up() {
	b := m.state()
	parent := filepath.Dir(b.root)
	if b.root == "" || parent == b.root {
		return
	}
	old := b.root
	m.SetRoot(parent)
	m.setExpanded(old, true)
	if i := slices.IndexFunc(b.entries, func(e BrowserEntry) bool { return e.Path == old }); i >= 0 {
		m.selectIndex(i)
	}
}

// Toggle expands or collapses the folder of row i.
func (m *BrowserModel) Toggle(i int) {
	if i >= 0 && i < len(m.browser.entries) && m.browser.entries[i].Dir {
		m.setExpanded(m.browser.entries[i].Path, !m.browser.entries[i].Expanded)
	}
}

func (m *BrowserModel) setExpanded(path string, expanded bool) {
	b := m.state()
	if b.expanded == nil {
		b.expanded = map[string]bool{}
	}
	if expanded {
		b.expanded[path] = true
	} else {
		// what was expanded inside it is collapsed with it
		for p := range b.expanded {
			if p == path || strings.HasPrefix(p, path+string(filepath.Separator)) {
				delete(b.expanded, p)
			}
		}
	}
	m.refresh()
}

// Enter loads the selected file into the selected buffer, as Load; on a
// folder it expands or collapses it.
func (m *BrowserModel) Enter() {
	e, ok := m.Selected()
	if !ok {
		return
	}
	if e.Dir {
		m.Toggle(m.browser.selected)
		return
	}
	m.Load()
}

// Load loads the selected file into the selected buffer, replacing its
// sample as Replace does, or into a new buffer if there is none or it is a
// spectrum or a bus. It is undone as one step.
func (m *BrowserModel) Load() {
	e, ok := m.Selected()
	if !ok || e.Dir {
		return
	}
	(*Model)(m).Buffer().ImportFiles([]string{e.Path}, m.canReplace())
}

// canReplace reports whether the selected buffer can take a sample in place
// of what it has.
func (m *BrowserModel) canReplace() bool {
	bm := (*Model)(m).Buffer()
	return bm.HasSelection() && !bm.IsSpectrum() && !bm.IsBus()
}

// Stop stops the preview.
func (m *BrowserModel) Stop() {
	m.cancelDecode()
	m.stopPreview()
	m.browser.status = ""
}

// selectionChanged previews the selected file, or stops the preview if a
// folder or nothing is selected, or AutoPreview is off.
func (m *BrowserModel) selectionChanged() {
	e, ok := m.Selected()
	if !ok || e.Dir || m.browser.manual {
		m.Stop()
		return
	}
	m.preview(e.Path)
}

// AutoPreview returns a Bool for playing a file when it is selected. When it
// is off, a file plays by PlaySelected only; turning it off stops the
// preview.
func (m *BrowserModel) AutoPreview() Bool { return MakeBool((*browserAutoPreview)(m)) }

type browserAutoPreview BrowserModel

func (v *browserAutoPreview) Value() bool { return !v.browser.manual }
func (v *browserAutoPreview) SetValue(value bool) {
	v.browser.manual = !value
	if !value {
		(*BrowserModel)(v).Stop()
	}
}

// PlaySelected returns an Action to play the selected file from its start,
// whether AutoPreview is on or not.
func (m *BrowserModel) PlaySelected() Action { return MakeAction((*browserPlay)(m)) }

type browserPlay BrowserModel

func (m *browserPlay) Enabled() bool {
	e, ok := (*BrowserModel)(m).Selected()
	return ok && !e.Dir
}
func (m *browserPlay) Do() {
	if e, ok := (*BrowserModel)(m).Selected(); ok && !e.Dir {
		(*BrowserModel)(m).preview(e.Path)
	}
}

func (m *BrowserModel) cancelDecode() {
	if m.browser.cancel != nil {
		m.browser.cancel()
		m.browser.cancel = nil
	}
}

// stopPreview stops a preview that the browser started; a preview of a
// buffer from the Buffers tab plays on.
func (m *BrowserModel) stopPreview() {
	if m.browser.previewing != "" {
		m.browser.previewing = ""
		TrySend(m.broker.ToPlayer, any(PreviewMsg{}))
		m.playerStatus.Previewing = false
	}
}

func (m *BrowserModel) play(audio sointu.BufferAudio) {
	TrySend(m.broker.ToPlayer, any(PreviewMsg{Audio: audio}))
	m.playerStatus.Previewing = audio.Frames() > 0 // until the player reports back
}

// preview plays a file: at once if it was decoded lately, or else when it
// is, on a goroutine. A decode still running for another file is cancelled.
func (m *BrowserModel) preview(path string) {
	b := m.state()
	m.cancelDecode()
	m.stopPreview()
	b.previewing = path
	stamp := stampOf(path)
	if i := slices.IndexFunc(b.cache, func(d browserDecoded) bool { return d.path == path && d.stamp == stamp }); i >= 0 {
		b.status = ""
		m.play(b.cache[i].audio)
		return
	}
	b.status = "decoding..."
	decode := b.decode
	if decode == nil {
		decode = decodePreview
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.gen++
	gen := b.gen
	go func() {
		audio, err := decode(ctx, path)
		if ctx.Err() != nil {
			return // cancelled: another file is selected, or the preview stopped
		}
		TrySend(m.broker.ToModel, MsgToModel{Data: func() { m.decoded(gen, path, stamp, audio, err) }})
	}()
}

// decoded takes the result of a decode, unless another one was started since.
func (m *BrowserModel) decoded(gen int, path string, stamp fileStamp, audio sointu.BufferAudio, err error) {
	b := m.state()
	if gen != b.gen || b.previewing != path {
		return
	}
	b.cancel = nil
	if err != nil {
		b.status = err.Error()
		b.previewing = ""
		return
	}
	b.status = ""
	b.cache = slices.DeleteFunc(b.cache, func(d browserDecoded) bool { return d.path == path })
	b.cache = append(b.cache, browserDecoded{path, stamp, audio})
	if len(b.cache) > browserCacheSize {
		b.cache = b.cache[1:]
	}
	if audio.Frames() == 0 {
		b.status = "no audio"
		b.previewing = ""
		return
	}
	m.play(audio)
}

// decodePreview decodes the first browserPreviewSeconds of a file in stereo
// with ffmpeg, which is killed when ctx is cancelled.
func decodePreview(ctx context.Context, path string) (sointu.BufferAudio, error) {
	f, err := ffmpeg.Find("")
	if err != nil {
		return sointu.BufferAudio{}, err
	}
	f.Context = ctx
	return f.DecodeFile(path, 2, browserPreviewSeconds)
}

// ImportFiles imports audio files as new buffers, in order, on a goroutine,
// each an undo step of its own. With replace, the first file replaces the
// sample of the buffer selected now, as Import does, and the rest are added.
// Paths that are not audio files are left out, with an alert.
func (m *BufferModel) ImportFiles(paths []string, replace bool) {
	var files, skipped []string
	for _, p := range paths {
		if isAudioFile(p) {
			files = append(files, p)
		} else {
			skipped = append(skipped, filepath.Base(p))
		}
	}
	if len(skipped) > 0 {
		(*Model)(m).Alerts().Add(fmt.Sprintf("Not an audio file: %s", strings.Join(skipped, ", ")), Warning)
	}
	if len(files) == 0 {
		return
	}
	target := 0
	if buf := m.selected(); replace && buf != nil {
		target = buf.ID
	}
	m.browser.lastFolder = filepath.Dir(files[len(files)-1])
	if len(files) == 1 {
		(*Model)(m).Alerts().Add(fmt.Sprintf("Importing %s...", filepath.Base(files[0])), Info)
	} else {
		(*Model)(m).Alerts().Add(fmt.Sprintf("Importing %d files...", len(files)), Info)
	}
	go func() {
		f, findErr := ffmpeg.Find("")
		for i, path := range files {
			name := filepath.Base(path)
			var stored []byte
			var channels int
			err := findErr
			if err == nil {
				var data []byte
				if data, err = os.ReadFile(path); err == nil {
					stored, channels, err = f.Import(data)
				}
			}
			id := 0
			if i == 0 {
				id = target
			}
			TrySend(m.broker.ToModel, MsgToModel{Data: func() {
				if err != nil {
					(*Model)(m).Alerts().Add(fmt.Sprintf("Error importing %s: %v", name, err), Error)
					return
				}
				m.addImported(name, stored, min(max(channels, 1), 2), id)
			}})
			if findErr != nil {
				return // without ffmpeg, the others fail the same
			}
		}
	}()
}

// ShowRoot returns an Action to make path the root of the browser.
func (m *BrowserModel) ShowRoot(path string) Action {
	return MakeAction(browserShowRoot{m, path})
}

type browserShowRoot struct {
	m    *BrowserModel
	path string
}

func (a browserShowRoot) Do() { a.m.SetRoot(a.path) }

// Parent returns an Action to make the parent of the root the root, as Up.
func (m *BrowserModel) Parent() Action { return MakeAction((*browserParent)(m)) }

type browserParent BrowserModel

func (m *browserParent) Enabled() bool {
	return m.browser.root != "" && filepath.Dir(m.browser.root) != m.browser.root
}
func (m *browserParent) Do() { (*BrowserModel)(m).Up() }
