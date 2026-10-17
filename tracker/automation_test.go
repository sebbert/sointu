package tracker

import (
	"math"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"gopkg.in/yaml.v3"
)

// automationModel returns a model and a player with a song whose
// instrument puts out the value of a loadval at 0, that is, with a lane on
// it, the value of the lane over 64.
func automationModel(t *testing.T) (m *Model, p *Player, sync func(), render func(n int) []float32) {
	t.Helper()
	broker := NewBroker()
	m = NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	p = NewPlayer(broker, vm.GoSynther{})
	sync = func() {
		for {
			select {
			case msg := <-broker.ToModel:
				m.ProcessMsg(msg)
			default:
				return
			}
		}
	}
	render = func(n int) []float32 {
		out := make(sointu.AudioBuffer, n)
		p.Process(out, NullPlayerProcessContext{})
		sync()
		ret := make([]float32, n)
		for i := range out {
			ret[i] = out[i][0]
		}
		return ret
	}
	func() {
		defer m.change("test", SongChange, MajorChange)()
		m.d.Song = sointu.Song{
			BPM: 120, RowsPerBeat: 4,
			Score: sointu.Score{RowsPerPattern: 16, Length: 2, Tracks: []sointu.Track{{NumVoices: 1, Order: sointu.Order{0, 0}, Patterns: []sointu.Pattern{{1}}}}},
			Patch: sointu.Patch{{Name: "Lead", NumVoices: 1, Units: []sointu.Unit{
				{Type: "loadval", ID: 1, Parameters: sointu.ParamMap{"stereo": 0, "value": 64}},
				{Type: "out", ID: 2, Parameters: sointu.ParamMap{"stereo": 0, "gain": 128}},
			}}},
		}
	}()
	render(64)
	return
}

func TestAutomationBind(t *testing.T) {
	m, _, _, _ := automationModel(t)
	a := m.Automation()
	if a.Automated().Value() {
		t.Fatal("automated without a lane")
	}
	// the parameter under the cursor: value of the loadval
	m.Params().SetCursor(Point{X: 1, Y: 0})
	item := m.Params().Item(m.Params().Table().Cursor())
	if unit, param, ok := a.targetOf(&item); !ok || unit != 1 || param != "value" {
		t.Fatalf("the target under the cursor: %v %v %v (%v)", unit, param, ok, item.Name())
	}
	a.Automated().SetValue(true)
	if l := a.Lanes(); len(l) != 1 || l[0].Slot != 0 || len(l[0].Targets) != 1 || l[0].Targets[0] != (sointu.LaneTarget{Unit: 1, Param: "value", Amount: 1}) {
		t.Fatalf("lanes %v", l)
	}
	if got := a.SlotName(0); got != "Lead: loadval value" {
		t.Errorf("SlotName %q", got)
	}
	item = m.Params().Item(m.Params().Table().Cursor())
	if hint := item.Hint().Label; !strings.HasPrefix(hint, "A1 +0.0: ") {
		t.Errorf("hint %q", hint)
	}
	if err := a.Bind(1, "value", -1, 1); err == nil {
		t.Error("a second lane on a parameter")
	}
	if err := a.Bind(2, "stereo", -1, 1); err == nil {
		t.Error("a lane on a parameter without a port")
	}
	if err := a.Bind(2, "gain", 5, 1); err != nil || len(a.Lanes()) != 2 || a.Lanes()[1].Slot != 5 {
		t.Errorf("binding slot 6: %v, %v", err, a.Lanes())
	}
	if err := a.SetPoints(5, sointu.AutomationCurve{{Row: 0, Value: 1}, {Row: 4, Value: 200}}); err == nil {
		t.Error("a point out of range")
	}
	if err := a.SetPoints(5, sointu.AutomationCurve{{Row: 0, Value: 1}, {Row: 4, Value: -20.5}}); err != nil || len(a.Lanes()[1].Points) != 2 {
		t.Errorf("SetPoints: %v", err)
	}
	m.History().Undo().Do()
	if len(a.Lanes()[1].Points) != 0 {
		t.Error("undo left the points")
	}
	m.History().Redo().Do()
	// the player has the lanes in the automation table of the instrument,
	// not as units
	if ps := m.playerSong(); len(ps.Patch[0].Units) != 2 || len(ps.Patch[0].Automation) != 2 {
		t.Errorf("the player's instrument has %d units and %d targets, want 2 and 2", len(ps.Patch[0].Units), len(ps.Patch[0].Automation))
	}
	// a lane goes with its unit
	func() {
		defer m.change("test", PatchChange, MajorChange)()
		m.d.Song.Patch[0].Units = m.d.Song.Patch[0].Units[:1]
	}()
	if l := a.Lanes(); len(l) != 1 || l[0].Targets[0].Unit != 1 {
		t.Errorf("after deleting the out unit: %v", l)
	}
	a.Automated().SetValue(false)
	if a.Lanes() != nil {
		t.Errorf("after removing the lane: %v", a.Lanes())
	}
}

func TestAutomationLiveAndRecording(t *testing.T) {
	m, p, sync, render := automationModel(t)
	a := m.Automation()
	if err := a.Bind(1, "value", 3, 1); err != nil {
		t.Fatal(err)
	}
	render(64)
	src := new(int)
	// a live value takes effect at its frame, in a short ramp
	start := p.frame
	p.Automate(start+100, 3, 32, src)
	out := render(400)
	if out[99] != 0 || out[101] <= 0 || out[100+128] != 0.5 || out[399] != 0.5 {
		t.Errorf("live: %v %v %v %v", out[99], out[101], out[228], out[399])
	}
	if got := a.Live(3); got != 32 {
		t.Errorf("Live(3) = %v", got)
	}
	// record: the slot is at 32 when the first note starts the recording,
	// goes down to -16 over 22050 frames from 11025 on, stays, and steps to
	// 8 at 66150; slot 10, bound to nothing, moves too
	m.Play().IsRecording().SetValue(true)
	render(64)
	t0 := p.frame
	type value struct {
		frame int64
		slot  int
		value float32
	}
	var values []value
	for f := 0; f <= 22050; f += 441 {
		values = append(values, value{t0 + 11025 + int64(f), 3, 32 - 48*float32(f)/22050})
	}
	values = append(values, value{t0 + 66150, 3, 8}, value{t0 + 44100, 10, -5})
	p.events = append(p.events, NoteEvent{Timestamp: t0, Channel: 0, Note: 60, On: true, Source: src})
	for range 80 { // a host gives the values of a block before the block
		for _, v := range values {
			if v.frame >= p.frame && v.frame < p.frame+1024 {
				p.Automate(v.frame, v.slot, v.value, src)
			}
		}
		render(1024)
	}
	p.events = append(p.events, NoteEvent{Timestamp: p.frame, Channel: 0, Note: 60, On: false, Source: src})
	render(64)
	m.Play().IsRecording().SetValue(false)
	render(64)
	sync()
	lanes := a.Lanes()
	if len(lanes) != 2 || lanes[0].Slot != 3 || lanes[0].Targets[0].Unit != 1 || lanes[1].Slot != 10 || len(lanes[1].Targets) != 0 {
		t.Fatalf("lanes after recording: %+v", lanes)
	}
	rows := func(frames float64) float64 { return frames / 44100 / 60 * 120 * 4 }
	c := lanes[0].Points
	if len(c) < 5 || len(c) > 7 {
		t.Errorf("%d points: %v", len(c), c)
	}
	for _, tc := range []struct{ frame, want float64 }{{0, 32}, {11025, 32}, {22050, 8}, {33075, -16}, {50000, -16}, {66149, -16}, {66151, 8}, {80000, 8}} {
		if got := c.Value(rows(tc.frame)); math.Abs(got-tc.want) > 0.15 {
			t.Errorf("frame %v: %v, want %v", tc.frame, got, tc.want)
		}
	}
	if c := lanes[1].Points; c.Value(rows(44000)) != 0 || c.Value(rows(44200)) != -5 {
		t.Errorf("the lane of slot 11: %v", c)
	}
	// played, the lane is heard instead of the live value, from the row
	// the song is played from
	m.Play().FromBeginning().Do()
	out = render(40000)
	spr := float64(m.d.Song.SamplesPerRow())
	for _, at := range []int{0, 5000, 11025, 22050, 33075, 39999} {
		want := c.Value(float64(at)/spr) / 64
		if got := float64(out[at]); math.Abs(got-want) > 0.01 {
			t.Errorf("playing, frame %d: %v, want %v", at, got, want)
		}
	}
	m.Play().Stop().Do()
	render(64)
	if out = render(300); out[299] != 8.0/64 {
		t.Errorf("stopped: %v, want the live value", out[299])
	}
	// a change of the tempo moves the points with the rows
	m.Song().BPM().SetValue(60)
	m.Play().FromBeginning().Do()
	out = render(60000)
	if got, want := float64(out[44100]), c.Value(float64(44100)/float64(m.d.Song.SamplesPerRow()))/64; math.Abs(got-want) > 0.01 {
		t.Errorf("at 60 BPM, frame 44100: %v, want %v", got, want)
	}
	// the live values are saved with the state of a plugin and restored
	state := m.History().MarshalRecovery()
	m2, p2, _, render2 := automationModel(t)
	m2.History().UnmarshalRecovery(state)
	render2(64)
	if out := render2(300); out[299] != 8.0/64 || p2.live[3] != 8 || m2.Automation().HostValues()[3] != 8 {
		t.Errorf("restored: %v, live %v", out[299], p2.live[3])
	}
}

func TestRemoteAutomation(t *testing.T) {
	m, _, _, _ := automationModel(t)
	r := (*Remote)(m)
	undo := len(m.undoStack)
	text, err := r.Call(func() (string, error) { return r.Automate(1, "value", 0, false, nil, nil, nil) })
	if err != nil || !strings.Contains(text, "slot A1, named \"A1 Lead: loadval value\" in the host, bipolar (the host moves it from -128 to 128 steps), offsets #1 value") {
		t.Fatalf("automate: %v\n%s", err, text)
	}
	if len(m.undoStack) != undo+1 {
		t.Errorf("automate took %d undo steps", len(m.undoStack)-undo)
	}
	if text, err = r.SetAutomation(1, [][2]float64{{0, 0}, {8, 12.5}, {8, -3}}); err != nil || !strings.Contains(text, "3 points, rows 0 to 8, -3 to +12.5 steps") {
		t.Fatalf("set_automation: %v\n%s", err, text)
	}
	if text := r.Song(); !strings.Contains(text, "A1 offsets #1 value (Lead: loadval value), 3 points") {
		t.Errorf("get_song:\n%s", text)
	}
	if text, _ := r.Instrument("0", false); !strings.Contains(text, "value=64(A1 +0.0: ") {
		t.Errorf("get_instrument:\n%s", text)
	}
	if text, err := r.Automation(1); err != nil || !strings.Contains(text, "[0, 0] [8, 12.5] [8, -3]") {
		t.Errorf("get_automation: %v\n%s", err, text)
	}
	// refused without changing the song or the history
	undo = len(m.undoStack)
	for name, call := range map[string]func() (string, error){
		"a parameter without a port": func() (string, error) { return r.Automate(2, "stereo", 0, false, nil, nil, nil) },
		"a missing unit":             func() (string, error) { return r.Automate(77, "gain", 0, false, nil, nil, nil) },
		"a lane that is there":       func() (string, error) { return r.Automate(1, "value", 0, false, nil, nil, nil) },
		"a slot that is not":         func() (string, error) { return r.Automate(2, "gain", 65, false, nil, nil, nil) },
		"points out of order":        func() (string, error) { return r.SetAutomation(1, [][2]float64{{4, 0}, {2, 1}}) },
		"a missing lane":             func() (string, error) { return r.Automate(2, "gain", 0, true, nil, nil, nil) },
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if len(m.undoStack) != undo || len(m.d.Song.Automation) != 1 || len(m.d.Song.Automation[0].Points) != 3 {
		t.Errorf("refused calls changed the song: %v", m.d.Song.Automation)
	}
	// unipolar: with a new lane, and for the lane that is there
	yes, no := true, false
	if text, err := r.Automate(2, "gain", 4, false, nil, nil, &yes); err != nil || !strings.Contains(text, "unipolar (the host moves it from 0 to 128 steps)") {
		t.Errorf("a new unipolar lane: %v\n%s", err, text)
	}
	if text := r.Song(); !strings.Contains(text, "A4 offsets #2 gain") || !strings.Contains(text, ", unipolar") {
		t.Errorf("get_song does not tell the lane is unipolar:\n%s", text)
	}
	before := m.d.Song.Copy()
	if text, err := r.Automate(2, "gain", 0, false, nil, nil, &no); err != nil || !strings.Contains(text, "bipolar") || m.d.Song.Automation[1].Unipolar {
		t.Errorf("making the lane bipolar: %v\n%s", err, text)
	}
	if lines := strings.Join(automationChanges(before.Automation, m.d.Song.Automation), "\n"); !strings.Contains(lines, "slot A4 is bipolar") {
		t.Errorf("changes: %s", lines)
	}
	r.Automate(2, "gain", 0, true, nil, nil, nil)
	// what the user changed
	before = m.d.Song.Copy()
	m.Automation().Bind(2, "gain", 9, 1)
	if lines := strings.Join(automationChanges(before.Automation, m.d.Song.Automation), "\n"); !strings.Contains(lines, "slot A10 added (#2 gain") {
		t.Errorf("changes: %s", lines)
	}
	if _, err := r.Automate(1, "value", 0, true, nil, nil, nil); err != nil || len(m.d.Song.Automation) != 1 {
		t.Errorf("removing: %v, %v", err, m.d.Song.Automation)
	}
}

func TestAutomationNames(t *testing.T) {
	m, _, _, _ := automationModel(t)
	a := m.Automation()
	m.Params().SetCursor(Point{X: 1, Y: 0})
	if a.Name().SetValue("x") {
		t.Error("named a parameter without a lane")
	}
	if err := a.Bind(1, "value", 2, 1); err != nil {
		t.Fatal(err)
	}
	if got := a.HostNames()[2]; got != "A3 Lead: loadval value" {
		t.Errorf("the host's name: %q", got)
	}
	undo := len(m.undoStack)
	if !a.Name().SetValue("  Bass\n cutoff ") || a.Name().Value() != "Bass cutoff" {
		t.Errorf("the name: %q", a.Name().Value())
	}
	if got, def := a.HostNames()[2], a.DefaultSlotName(2); got != "Bass cutoff" || def != "Lead: loadval value" || a.SlotName(2) != "Bass cutoff" {
		t.Errorf("named: host %q, default %q", got, def)
	}
	if len(m.undoStack) != undo+1 {
		t.Errorf("naming took %d undo steps", len(m.undoStack)-undo)
	}
	if err := a.Rename(2, strings.Repeat("x", MaxAutomationName+1)); err == nil {
		t.Error("a name that is too long")
	}
	if err := a.Rename(7, "x"); err == nil {
		t.Error("named a slot without a lane")
	}
	// the name is saved, and survives a recording
	data, err := yaml.Marshal(m.d.Song)
	if err != nil || !strings.Contains(string(data), "name: Bass cutoff") {
		t.Errorf("saved: %v\n%s", err, data)
	}
	rec := Recording{BPM: 120, State: RecordingFinished}
	if lanes := rec.Lanes(m.d.Song.Automation, 4); len(lanes) != 1 || lanes[0].Name != "Bass cutoff" {
		t.Errorf("after a recording: %v", lanes)
	}
	// through the remote tools: naming a lane that is there, and a new one
	r := m.Remote()
	name := "Cutoff"
	if text, err := r.Automate(1, "value", 0, false, &name, nil, nil); err != nil || !strings.Contains(text, `A3 is named "Cutoff"`) {
		t.Errorf("renaming: %v\n%s", err, text)
	}
	name = "Level"
	if text, err := r.Automate(2, "gain", 0, false, &name, nil, nil); err != nil || !strings.Contains(text, `named "Level"`) || a.HostNames()[0] != "Level" {
		t.Errorf("binding with a name: %v\n%s", err, text)
	}
	if text := r.Song(); !strings.Contains(text, `A3 "Cutoff" offsets #1 value`) {
		t.Errorf("get_song:\n%s", text)
	}
	name = ""
	if _, err := r.Automate(1, "value", 0, false, &name, nil, nil); err != nil || a.HostNames()[2] != "A3 Lead: loadval value" {
		t.Errorf("clearing the name: %v, %q", err, a.HostNames()[2])
	}
}

// TestAutomationMacro gives a lane two targets, one of them with a negative
// amount, and moves a target between lanes.
func TestAutomationMacro(t *testing.T) {
	m, p, _, render := automationModel(t)
	a := m.Automation()
	if err := a.Bind(1, "value", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Bind(2, "gain", 0, -0.5); err != nil {
		t.Fatal(err)
	}
	if l := a.Lanes(); len(l) != 1 || len(l[0].Targets) != 2 || l[0].Targets[1] != (sointu.LaneTarget{Unit: 2, Param: "gain", Amount: -0.5}) {
		t.Fatalf("lanes %v", l)
	}
	if got := a.DefaultSlotName(0); got != "Lead: loadval value +1" {
		t.Errorf("DefaultSlotName %q", got)
	}
	// 32 steps: the loadval at 0.5, the gain of the out 16 steps down
	render(64)
	p.Automate(p.frame, 0, 32, new(int))
	if out := render(400); math.Abs(float64(out[399])-0.5*112/128) > 1e-6 {
		t.Errorf("live: %v, want %v", out[399], 0.5*112/128)
	}
	// the amount and the slot of the parameter under the cursor
	m.Params().SetCursor(Point{X: 1, Y: 0})
	if v := a.Amount().Value(); v != 64 {
		t.Errorf("Amount %d, want 64", v)
	}
	a.Amount().SetValue(32)
	if l := a.Lanes(); l[0].Targets[0].Amount != 0.5 {
		t.Errorf("after setting the amount: %v", l)
	}
	if !a.SlotNumber().SetValue(5) {
		t.Fatal("moving to slot 5")
	}
	if l := a.Lanes(); len(l) != 2 || len(l[0].Targets) != 1 || l[1].Slot != 4 || l[1].Targets[0] != (sointu.LaneTarget{Unit: 1, Param: "value", Amount: 0.5}) {
		t.Errorf("after moving to slot 5: %v", l)
	}
	// alone in its lane, the target takes the lane along
	a.SetPoints(4, sointu.AutomationCurve{{Row: 0, Value: 3}})
	if !a.SlotNumber().SetValue(7) {
		t.Fatal("moving to slot 7")
	}
	if l := a.Lanes(); len(l) != 2 || l[1].Slot != 6 || len(l[1].Points) != 1 {
		t.Errorf("after moving the lane to slot 7: %v", l)
	}
	// back into the lane of slot 1, and out again
	if err := a.Bind(1, "value", 0, 1); err != nil || len(a.Lanes()) != 1 || len(a.Lanes()[0].Targets) != 2 {
		t.Errorf("back to slot 1: %v %v", err, a.Lanes())
	}
	if !a.Unbind(2, "gain") || len(a.Lanes()) != 1 || a.Lanes()[0].Targets[0].Unit != 1 {
		t.Errorf("after unbinding the gain: %v", a.Lanes())
	}
	// through the remote tools, with an amount
	r := m.Remote()
	amount := -0.25
	if text, err := r.Automate(2, "gain", 1, false, nil, &amount, nil); err != nil || !strings.Contains(text, "#2 gain times -0.25") {
		t.Errorf("automate with an amount: %v\n%s", err, text)
	}
	if text, _ := r.Instrument("0", false); !strings.Contains(text, "gain=128(A1×-0.25 ") {
		t.Errorf("get_instrument:\n%s", text)
	}
	amount = 2
	if _, err := r.Automate(2, "gain", 1, false, nil, &amount, nil); err == nil {
		t.Error("an amount of 2")
	}
}

// TestAutomationActions checks the actions of the context menu of a
// parameter and the amount of the small knob.
func TestAutomationActions(t *testing.T) {
	m, _, _, _ := automationModel(t)
	a := m.Automation()
	m.Params().SetCursor(Point{X: 1, Y: 0})
	value := m.Params().Item(m.Params().Table().Cursor())
	m.Params().SetCursor(Point{X: 1, Y: 1})
	gain := m.Params().Item(m.Params().Table().Cursor())
	if !a.NewLane(&value).Enabled() || a.RemoveLane(&value).Enabled() || a.JoinLane(&value, 0).Enabled() {
		t.Fatal("the actions of a parameter without a lane")
	}
	a.NewLane(&value).Do()
	if a.NewLane(&value).Enabled() || !a.RemoveLane(&value).Enabled() || a.JoinLane(&value, 0).Enabled() || !a.JoinLane(&gain, 0).Enabled() {
		t.Error("the actions after a new lane")
	}
	a.JoinLane(&gain, 0).Do()
	if l := a.Lanes(); len(l) != 1 || len(l[0].Targets) != 2 {
		t.Fatalf("after joining: %v", l)
	}
	undo := len(m.undoStack)
	a.SetAmountOf(&gain, 0.3)
	a.SetAmountOf(&gain, -0.5)
	if got, _ := a.AmountOf(&gain); got != -0.5 || len(m.undoStack) > undo+1 {
		t.Errorf("amount %v, %d undo steps", got, len(m.undoStack)-undo)
	}
	// the lane is bipolar; the action toggles it, in one undo step each
	if a.HostUnipolar()[0] || !a.TogglePolarity(&gain).Enabled() {
		t.Fatal("a new lane is unipolar, or its polarity cannot change")
	}
	undo = len(m.undoStack)
	a.TogglePolarity(&gain).Do()
	if !a.Lanes()[0].Unipolar || !a.HostUnipolar()[0] || len(m.undoStack) != undo+1 {
		t.Errorf("toggled once: %v, %d undo steps", a.Lanes()[0], len(m.undoStack)-undo)
	}
	a.TogglePolarity(&value).Do() // the other target of the same lane
	if a.Lanes()[0].Unipolar {
		t.Error("toggled twice, the lane is unipolar")
	}
	if err := a.SetUnipolar(5, true); err == nil {
		t.Error("a slot without a lane was made unipolar")
	}
	a.RemoveLane(&value).Do()
	if l := a.Lanes(); len(l) != 1 || l[0].Targets[0].Unit != 2 {
		t.Errorf("after removing the value: %v", l)
	}
	if a.TogglePolarity(&value).Enabled() {
		t.Error("the polarity of a parameter without a lane")
	}
}
