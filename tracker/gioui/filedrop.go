package gioui

import (
	"os"
	"slices"

	"gioui.org/app"
	"gioui.org/io/event"
	"github.com/vsariola/sointu/tracker"
)

// dropFiles takes files dropped onto the window from the system (only macOS
// sends them, see app.FileDropEvent). Audio files dropped onto the
// properties of the Buffers tab replace the sample of the selected buffer
// with the first, and the rest become new buffers; dropped anywhere else,
// e.g. onto the list of the buffers, they all become new buffers, in order.
// A folder dropped alone is shown in the sample browser. Either shows the
// Buffers tab.
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
	onProps := t.Instrument().Tab().Value() == int(tracker.InstrumentBuffersTab) &&
		slices.Contains(e.Targets, event.Tag(&t.PatchPanel.instrBuffers.dropReplace))
	t.Instrument().Tab().SetValue(int(tracker.InstrumentBuffersTab))
	if len(files) == 0 {
		if !t.ShowBrowser().Value() {
			t.ShowBrowser().SetValue(true)
		}
		t.Browser().SetRoot(folder)
		return
	}
	b := t.Buffer()
	replace := onProps && b.HasSelection() && !b.IsSpectrum() && !b.IsBus()
	b.ImportFiles(files, replace)
}
