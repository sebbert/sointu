package tracker

import (
	"strings"
	"testing"

	"github.com/vsariola/sointu"
)

func moduleAlerts(m *Model) (ret []string) {
	for _, a := range m.Alerts().Iterate {
		ret = append(ret, a.Message)
	}
	return ret
}

// TestMakeModuleRepointsSends checks that a send to a unit that becomes a
// unit of a new module goes to the module unit instead.
func TestMakeModuleRepointsSends(t *testing.T) {
	m, broker := newModuleTestModel(t)
	frequency := sointu.PortOf("filter", "frequency")
	func() {
		// the LFO modulates the frequency of the filter, and so does a send
		// of a second instrument
		defer m.change("Test", SongChange, MajorChange)()
		send := &m.d.Song.Patch[0].Units[1]
		send.Parameters["target"], send.Parameters["port"] = 6, frequency
		other := sointu.MakeUnit("send")
		other.ID = 20
		other.Parameters["target"], other.Parameters["port"] = 6, frequency
		osc := sointu.MakeUnit("oscillator")
		osc.ID = 21
		m.d.Song.Patch = append(m.d.Song.Patch, sointu.Instrument{Name: "mod", NumVoices: 1, Units: []sointu.Unit{osc, other}})
	}()
	makeTestModule(t, m)
	send, call, mod := m.d.Song.Patch[0].Units[1], m.d.Song.Patch[0].Units[2], &m.d.Song.Modules[0]
	if send.Parameters["target"] != call.ID || send.Parameters["port"] != 0 {
		t.Fatalf("the send goes to %v port %v, the module unit is %v", send.Parameters["target"], send.Parameters["port"], call.ID)
	}
	if other := m.d.Song.Patch[1].Units[1]; other.Parameters["target"] != call.ID || other.Parameters["port"] != 0 {
		t.Errorf("the send of the other instrument goes to %v port %v", other.Parameters["target"], other.Parameters["port"])
	}
	if len(mod.Params) != 1 || mod.Params[0].Name != "frequency" || mod.Params[0].Default != 40 || mod.Units[3].Bind["frequency"].Param != 1 || call.Parameters["p1"] != 40 {
		t.Fatalf("the parameters of the module: %+v, bindings %v, module unit %v", mod.Params, mod.Units[3].Bind, call.Parameters)
	}
	patch := playerPatch(t, broker)
	filter := patch[0].Units[5]
	if got := patch[0].Units[1]; filter.Type != "filter" || got.Parameters["target"] != filter.ID || got.Parameters["port"] != frequency || filter.Parameters["frequency"] != 40 {
		t.Errorf("the player got a send to %v port %v; the filter is %v", got.Parameters["target"], got.Parameters["port"], filter.ID)
	}
	if got := patch[1].Units[1]; got.Parameters["target"] != filter.ID || got.Parameters["port"] != frequency {
		t.Errorf("the player got a send of the other instrument to %v port %v", got.Parameters["target"], got.Parameters["port"])
	}
	for _, a := range m.Alerts().Iterate {
		if a.Name == "Modules" {
			t.Errorf("alert: %v", a.Message)
		}
	}
	m.History().Undo().Do()
	if send := m.d.Song.Patch[0].Units[1]; send.Parameters["target"] != 6 || send.Parameters["port"] != frequency || len(m.d.Song.Modules) != 0 {
		t.Errorf("after undo the send goes to %v port %v", send.Parameters["target"], send.Parameters["port"])
	}
}

// TestMakeModuleStereoSendAndLimit checks a stereo send, which needs two
// parameters of the module next to each other, and what happens when the
// module has no parameters left.
func TestMakeModuleStereoSendAndLimit(t *testing.T) {
	m, broker := newModuleTestModel(t)
	frequency := sointu.PortOf("filter", "frequency")
	sendTo := func(id, target, port int, stereo bool) sointu.Unit {
		u := sointu.MakeUnit("send")
		u.ID = id
		u.Parameters["target"], u.Parameters["port"] = target, port
		if stereo {
			u.Parameters["stereo"] = 1
		}
		return u
	}
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		var sends []sointu.Unit
		osc := sointu.MakeUnit("oscillator")
		osc.ID = 30
		sends = append(sends, osc)
		// the resonance, then in stereo the frequency and the resonance,
		// then the first seven ports of the envelope and the oscillator
		sends = append(sends, sendTo(31, 6, frequency+1, false), sendTo(32, 6, frequency, true))
		for i := range 4 {
			sends = append(sends, sendTo(40+i, 3, i, false))
		}
		for i := range 3 {
			sends = append(sends, sendTo(50+i, 4, i, false))
		}
		m.d.Song.Patch = append(m.d.Song.Patch, sointu.Instrument{Name: "mod", NumVoices: 1, Units: sends})
	}()
	makeTestModule(t, m)
	call, mod := m.d.Song.Patch[0].Units[2], &m.d.Song.Modules[0]
	sends := m.d.Song.Patch[1].Units
	if len(mod.Params) != sointu.MaxModuleParams {
		t.Fatalf("the module has %v parameters: %+v", len(mod.Params), mod.Params)
	}
	// the stereo send first: the frequency and the resonance next to each other
	if f, r := mod.Units[3].Bind["frequency"].Param, mod.Units[3].Bind["resonance"].Param; f != 1 || r != 2 {
		t.Errorf("the frequency and the resonance are parameters %v and %v", f, r)
	}
	if s := sends[2]; s.Parameters["target"] != call.ID || s.Parameters["port"] != 0 {
		t.Errorf("the stereo send goes to %v port %v", s.Parameters["target"], s.Parameters["port"])
	}
	if s := sends[1]; s.Parameters["target"] != call.ID || s.Parameters["port"] != 1 {
		t.Errorf("the send to the resonance goes to %v port %v", s.Parameters["target"], s.Parameters["port"])
	}
	// six more fit; the last one stays, and the user is told
	for i := 3; i < 9; i++ {
		if s := sends[i]; s.Parameters["target"] != call.ID || s.Parameters["port"] != i-1 {
			t.Errorf("send %v goes to %v port %v", i, s.Parameters["target"], s.Parameters["port"])
		}
	}
	if s := sends[9]; s.Parameters["target"] != 4 || s.Parameters["port"] != 2 {
		t.Errorf("the last send goes to %v port %v", s.Parameters["target"], s.Parameters["port"])
	}
	if alerts := strings.Join(moduleAlerts(m), "\n"); !strings.Contains(alerts, "send #9 of instrument mod") {
		t.Errorf("the alerts do not name the send:\n%v", alerts)
	}
	// played, the stereo send is one stereo send to the filter
	patch := playerPatch(t, broker)
	filter := patch[0].Units[5]
	if s := patch[1].Units[2]; s.Parameters["target"] != filter.ID || s.Parameters["port"] != frequency || s.Parameters["stereo"] != 1 || len(patch[1].Units) != 10 {
		t.Errorf("the player got %+v, and %v units; the filter is %v", s, len(patch[1].Units), filter.ID)
	}
}

// TestInlineModuleRepointsSends checks that a send to a module unit goes to
// what its port modulated when the units of the module take its place.
func TestInlineModuleRepointsSends(t *testing.T) {
	m, broker := newModuleTestModel(t)
	makeTestModule(t, m)
	m.Unit().OpenModule().Do()
	m.Module().AddParam().Do()
	bindParam(t, m, 3, "frequency", 1) // the filter, at 40
	bindParam(t, m, 3, "resonance", 1)
	bindParam(t, m, 1, "detune", 1)
	m.Module().BindingAt(1, false).SetValue(100) // the detune from 100 down to 36: half as much, the other way
	m.Module().BindingAt(1, true).SetValue(36)
	m.Module().AddParam().Do() // nothing is bound to the second one
	m.Instrument().Tab().SetValue(int(InstrumentEditorTab))
	m.Unit().List().SetSelected(2)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		units := m.d.Song.Patch[0].Units
		send := &units[1]
		send.Parameters["target"], send.Parameters["port"], send.Parameters["amount"] = units[2].ID, 0, 96
		// another send, to the port that modulates nothing
		other := send.Copy()
		other.ID = 40
		other.Parameters["port"], other.Parameters["sendpop"] = 1, 0
		m.d.Song.Patch[0].Units = append([]sointu.Unit{units[0], other}, units[1:]...)
	}()
	m.Unit().List().SetSelected(3)
	m.Unit().List().SetSelected2(3)
	want := playerPatch(t, broker)
	if got := unitTypes(want[0].Units); got != "oscillator send send send envelope oscillator mulp filter out" {
		t.Fatalf("before, the player got: %v", got)
	}
	m.Unit().InlineModule().Do()
	units := m.d.Song.Patch[0].Units
	if got := unitTypes(units); got != "oscillator send send send send envelope oscillator mulp filter out" {
		t.Fatalf("after replacing the module unit: %v", got)
	}
	if m.d.UnitIndex != 5 || m.d.UnitIndex2 != 8 {
		t.Errorf("units %v to %v are selected", m.d.UnitIndex, m.d.UnitIndex2)
	}
	if other := units[1]; other.ID != 40 || other.Parameters["target"] != 0 {
		t.Errorf("the send to the port that modulated nothing: %+v", other)
	}
	osc, filter := units[6], units[8]
	// in the order of the units of the module: the detune, by half and the
	// other way, then the frequency and the resonance; only the last pops
	for i, s := range []struct{ target, port, amount, pop int }{
		{osc.ID, sointu.PortOf("oscillator", "detune"), 48, 0},
		{filter.ID, sointu.PortOf("filter", "frequency"), 96, 0},
		{filter.ID, sointu.PortOf("filter", "resonance"), 96, 1},
	} {
		got := units[2+i].Parameters
		if got["target"] != s.target || got["port"] != s.port || got["amount"] != s.amount || got["sendpop"] != s.pop {
			t.Errorf("send %v: %v, want %+v", i, got, s)
		}
	}
	seen := map[int]bool{}
	for _, u := range units {
		if u.ID == 0 || seen[u.ID] {
			t.Errorf("the ID %v is missing or not unique", u.ID)
		}
		seen[u.ID] = true
	}
	// the player gets what it got before, but for the send without a target
	got := playerPatch(t, broker)
	if len(got[0].Units) != len(want[0].Units)+1 {
		t.Fatalf("the player got %v units, before %v", len(got[0].Units), len(want[0].Units))
	}
	for i, w := range want[0].Units {
		g := got[0].Units[i+1]
		if i == 0 {
			g = got[0].Units[0]
		}
		if g.Type != w.Type || g.Type == "send" && (g.Parameters["port"] != w.Parameters["port"] || g.Parameters["amount"] != w.Parameters["amount"] || g.Parameters["sendpop"] != w.Parameters["sendpop"]) {
			t.Errorf("unit %v: the player got %v %v, before %v %v", i, g.Type, g.Parameters, w.Type, w.Parameters)
		}
	}
	m.History().Undo().Do()
	if got := unitTypes(m.d.Song.Patch[0].Units); got != "oscillator send send module out" {
		t.Errorf("after undo: %v", got)
	}
}
