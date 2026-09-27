package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

func TestHostSavesState(t *testing.T) {
	m := NewModel(NewBroker(), []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	changes := 0
	m.SetHostSavesState(func() { changes++ })
	m.Song().BPM().SetValue(123)
	if changes != 1 {
		t.Errorf("after an edit: %d changes, want 1", changes)
	}
	if m.Song().ChangedSinceSave() {
		t.Error("song shown as unsaved although the host saves it")
	}
	m.History().Undo().Do()
	m.History().Redo().Do()
	if changes != 3 {
		t.Errorf("after undo and redo: %d changes, want 3", changes)
	}
	m.History().UnmarshalRecovery(m.History().MarshalRecovery())
	if changes != 3 {
		t.Errorf("loading the state from the host is a change")
	}
}
