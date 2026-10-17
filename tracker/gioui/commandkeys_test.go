package gioui

import (
	"testing"

	"gioui.org/io/key"
)

// The tracker claims the command keys of its shortcuts from the menu of a
// host, and leaves it the others, e.g. Cmd+' to cycle windows.
func TestCommandKeysClaimed(t *testing.T) {
	press := func(name key.Name, mods key.Modifiers) key.Event {
		return key.Event{Name: name, Modifiers: mods, State: key.Press}
	}
	for _, e := range []key.Event{press("Z", key.ModShortcut), press("C", key.ModShortcut), press("V", key.ModShortcut)} {
		if _, ok := keyBindingMap[e]; !ok {
			t.Errorf("%v is not claimed", e)
		}
	}
	for _, e := range []key.Event{press("'", key.ModShortcut), press("Q", key.ModShortcut), press("H", key.ModShortcut)} {
		if _, ok := keyBindingMap[e]; ok {
			t.Errorf("%v is claimed", e)
		}
	}
}
