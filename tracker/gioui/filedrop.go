package gioui

import (
	"os"

	"gioui.org/app"
	"gioui.org/io/key"
	"github.com/vsariola/sointu/tracker"
)

// dropFiles takes files dropped onto the window from the system (only macOS
// sends them, see app.FileDropEvent): audio files become new buffers, in
// order, or with Alt held, the first replaces the sample of the selected
// buffer. A folder dropped alone is shown in the sample browser. Either
// shows the Buffers tab, wherever the drop was.
func (t *Tracker) dropFiles(e app.FileDropEvent) {
	var files []string
	folder := ""
	for _, p := range e.Paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			if folder == "" {
				folder = p
			}
			continue
		}
		files = append(files, p)
	}
	if len(files) == 0 && folder == "" {
		return
	}
	t.Instrument().Tab().SetValue(int(tracker.InstrumentBuffersTab))
	if len(files) == 0 {
		if !t.ShowBrowser().Value() {
			t.ShowBrowser().SetValue(true)
		}
		t.Browser().SetRoot(folder)
		return
	}
	b := t.Buffer()
	replace := e.Modifiers.Contain(key.ModAlt) && b.HasSelection() && !b.IsSpectrum() && !b.IsBus()
	b.ImportFiles(files, replace)
}
