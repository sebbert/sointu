package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// TestIDCollisionKeepsSendTargets checks that when units of an instrument
// and of a module (or of two instruments) have the same IDs, e.g. in a song
// put together from parts, the units that get new IDs keep their sends: a
// send still goes to the unit of its own instrument or module that it went
// to.
func TestIDCollisionKeepsSendTargets(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	unit := func(id int, typ string, params sointu.ParamMap) sointu.Unit {
		u := sointu.MakeUnit(typ)
		u.ID = id
		for k, v := range params {
			u.Parameters[k] = v
		}
		return u
	}
	// the same IDs in the first instrument, the second one and the module:
	// an LFO (1) sent (2) to a unit (3) of its own
	lfo := sointu.ParamMap{"lfo": 1}
	send := sointu.ParamMap{"target": 3, "port": 0, "sendpop": 1}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{
			{Name: "first", NumVoices: 1, Units: []sointu.Unit{
				unit(1, "oscillator", lfo), unit(2, "send", send), unit(3, "oscillator", nil), unit(4, "out", sointu.ParamMap{"stereo": 0}),
				unit(5, "loadval", nil), unit(6, "module", sointu.ParamMap{"module": 1}), unit(7, "out", sointu.ParamMap{"stereo": 0}),
			}},
			{Name: "second", NumVoices: 1, Units: []sointu.Unit{
				unit(1, "oscillator", lfo), unit(2, "send", send), unit(3, "envelope", nil), unit(4, "out", sointu.ParamMap{"stereo": 0}),
				// a send to a unit that only the first instrument has
				unit(8, "oscillator", lfo), unit(9, "send", sointu.ParamMap{"target": 5, "port": 0, "sendpop": 1}),
			}},
		}
		m.d.Song.Modules = sointu.Modules{{ID: 1, Name: "wobble", Inputs: 1, Units: []sointu.Unit{
			unit(1, "oscillator", lfo), unit(2, "send", send), unit(3, "filter", nil),
			// two units of the module with the same ID: the send means the first
			unit(3, "gain", nil),
		}}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 1}}
	}()
	ids := map[int]string{}
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if u.ID == 0 || ids[u.ID] != "" {
				t.Fatalf("after the fix, two units have the ID %d: %s and %s", u.ID, ids[u.ID], u.Type)
			}
			ids[u.ID] = u.Type
		}
	}
	check := func(name string, units []sointu.Unit, send, target int, targetType string) {
		t.Helper()
		if got := units[send].Parameters["target"]; got != units[target].ID || units[target].Type != targetType {
			t.Errorf("%s: the send goes to unit %d (%s), not to the %s of its own, which has the ID %d", name, got, ids[got], targetType, units[target].ID)
		}
	}
	check("first instrument", m.d.Song.Patch[0].Units, 1, 2, "oscillator")
	check("second instrument", m.d.Song.Patch[1].Units, 1, 2, "envelope")
	check("module", m.d.Song.Modules[0].Units, 1, 2, "filter")
	if got := m.d.Song.Patch[1].Units[5].Parameters["target"]; got != m.d.Song.Patch[0].Units[4].ID || got != 5 {
		t.Errorf("the send to a unit of the other instrument goes to unit %d (%s)", got, ids[got])
	}
	// and the player gets them so
	patch := playerPatch(t, broker)
	if got := unitTypes(patch[0].Units); got != "oscillator send oscillator out loadval oscillator send filter gain out" {
		t.Fatalf("the player got: %v", got)
	}
	check("module unit, as played", patch[0].Units, 6, 7, "filter")
}
