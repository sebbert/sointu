package gioui

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

// TestReload checks what the tracker of a plugin of the dev loader does when
// a newer build is installed: the menu item is enabled, and with auto
// reload the reload is asked for at once, once.
func TestReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("AppData", home)
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	tr := NewTracker(model)
	if tr.CanReload() || tr.AutoReload().Enabled() {
		t.Fatal("a tracker without the loader offers to reload")
	}
	asked := 0
	tr.SetReloader(func() { asked++ })
	if !tr.CanReload() || tr.ReloadPlugin().Enabled() {
		t.Fatal("reloading is offered before a newer build is installed, or not offered")
	}
	tr.ReloadAvailable("v1-20261018")
	if asked != 0 || !tr.ReloadPlugin().Enabled() {
		t.Fatalf("a new build: asked %d times, menu item enabled %v", asked, tr.ReloadPlugin().Enabled())
	}
	tr.AutoReload().SetValue(true)
	if asked != 1 || tr.ReloadPlugin().Enabled() {
		t.Fatalf("auto reload on: asked %d times, menu item enabled %v", asked, tr.ReloadPlugin().Enabled())
	}
	tr.ReloadAvailable("v1-20261019")
	if asked != 1 {
		t.Fatalf("asked again while the reload is under way: %d", asked)
	}
}
