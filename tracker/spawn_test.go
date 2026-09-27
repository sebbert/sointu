package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// newSpawnTestModel returns a model with instruments a, spawner and target,
// where the spawner spawns target, and the track and instrument lists
// unlinked.
func newSpawnTestModel(t *testing.T) *Model {
	t.Helper()
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{
			{Name: "a", NumVoices: 1, Units: []sointu.Unit{{ID: 1, Type: "loadnote", Parameters: sointu.ParamMap{}}}},
			{Name: "spawner", NumVoices: 1, Units: []sointu.Unit{{ID: 2, Type: "spawn", Parameters: sointu.ParamMap{"instrument": 3}}}},
			{Name: "target", NumVoices: 2, Units: []sointu.Unit{{ID: 3, Type: "loadnote", Parameters: sointu.ParamMap{}}}},
		}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 4}}
	}()
	drainPlayer(broker)
	return m
}

func spawnTarget(t *testing.T, m *Model) int {
	t.Helper()
	for _, instr := range m.d.Song.Patch {
		for _, u := range instr.Units {
			if u.Type == "spawn" {
				return u.Parameters["instrument"]
			}
		}
	}
	t.Fatal("no spawn unit")
	return 0
}

func TestSpawnTargetFollowsInstruments(t *testing.T) {
	m := newSpawnTestModel(t)
	instruments := m.Instrument().List()

	instruments.SetSelected(0)
	instruments.SetSelected2(0)
	instruments.DeleteElements(false) // delete a
	if got := spawnTarget(t, m); got != 2 {
		t.Errorf("after deleting an instrument before the target: target %d, want 2", got)
	}

	instruments.SetSelected(1)
	instruments.SetSelected2(1)
	instruments.MoveElements(-1) // move target before spawner
	if got := spawnTarget(t, m); got != 1 || m.d.Song.Patch[0].Name != "target" {
		t.Errorf("after moving the target up: target %d, want 1", got)
	}

	m.d.InstrIndex = 0
	m.Instrument().Add().Do() // add before the target
	if got, name := spawnTarget(t, m), m.d.Song.Patch[spawnTarget(t, m)-1].Name; name != "target" {
		t.Errorf("after adding an instrument: target %d is %q, want target", got, name)
	}

	i := spawnTarget(t, m) - 1
	m.d.InstrIndex = i
	m.Instrument().Split().Do() // the target stays in the first half
	if got := spawnTarget(t, m); got != i+1 || m.d.Song.Patch[i+1].Name == "target" {
		t.Errorf("after splitting the target: target %d, want %d", got, i+1)
	}

	m.History().Undo().Do()
	if got := spawnTarget(t, m); got != i+1 {
		t.Errorf("after undo: target %d, want %d", got, i+1)
	}

	instruments.SetSelected(i)
	instruments.SetSelected2(i)
	instruments.DeleteElements(false) // delete the target
	if got := spawnTarget(t, m); got != 0 {
		t.Errorf("after deleting the target: target %d, want 0", got)
	}
}

func TestSpawnTargetParameter(t *testing.T) {
	m := newSpawnTestModel(t)
	var p Parameter
	for _, q := range m.derived.patch[1].params[0] {
		if q.Name() == "instrument" {
			p = q
		}
	}
	if p.Type() != ChoiceParameter {
		t.Fatalf("instrument parameter has type %v, want ChoiceParameter", p.Type())
	}
	i := p.Int()
	names := []string{}
	for v := i.Range().Min; v <= i.Range().Max; v++ {
		names = append(names, i.StringOf(v))
	}
	if !slicesEqual(names, []string{"none", "1: a", "2: spawner", "3: target"}) {
		t.Errorf("got choices %q", names)
	}
}

func TestUnitsWithoutIDsGetIDsQuietly(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	defer m.Close()
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Patch = sointu.Patch{{NumVoices: 1, Units: []sointu.Unit{
			{Type: "loadnote", Parameters: sointu.ParamMap{}},
			{ID: 5, Type: "loadnote", Parameters: sointu.ParamMap{}},
			{Type: "loadnote", Parameters: sointu.ParamMap{}},
		}}}
	}()
	seen := map[int]bool{}
	for _, u := range m.d.Song.Patch[0].Units {
		if u.ID == 0 || seen[u.ID] {
			t.Errorf("unit IDs not fixed: %v", m.d.Song.Patch[0].Units)
		}
		seen[u.ID] = true
	}
	if m.d.Song.Patch[0].Units[1].ID != 5 {
		t.Error("an existing ID was changed")
	}
	for _, a := range m.alerts {
		if a.Name == "IDCollision" {
			t.Error("warned about units without IDs")
		}
	}
}
