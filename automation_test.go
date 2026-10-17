package sointu_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

func automationSong() sointu.Song {
	return sointu.Song{
		BPM: 100, RowsPerBeat: 4,
		Score: sointu.Score{RowsPerPattern: 16, Length: 1, Tracks: []sointu.Track{{NumVoices: 1}}},
		Modules: sointu.Modules{{ID: 1, Name: "m", Inputs: 1, Params: []sointu.ModuleParam{{Name: "f", Default: 64}}, Units: []sointu.Unit{
			{Type: "filter", ID: 5, Parameters: sointu.ParamMap{"frequency": 64, "resonance": 64, "lowpass": 1}, Bind: map[string]sointu.Binding{"frequency": {Param: 1}}},
			{Type: "automation", Parameters: sointu.ParamMap{"slot": 9}},
			{Type: "pop", Parameters: sointu.ParamMap{}},
		}}},
		Patch: sointu.Patch{{Name: "a", NumVoices: 1, Units: []sointu.Unit{
			{Type: "oscillator", ID: 1, Parameters: sointu.ParamMap{"transpose": 64, "detune": 64, "gain": 128}},
			{Type: "module", ID: 2, Parameters: sointu.ParamMap{"module": 1}},
			{Type: "out", ID: 3, Parameters: sointu.ParamMap{"gain": 64}},
		}}},
		Automation: sointu.Automation{
			{Slot: 2, Targets: []sointu.LaneTarget{{Unit: 1, Param: "detune", Amount: 1}}, Points: sointu.AutomationCurve{{0, 1.5}, {4, -200}}},
			{Slot: 4, Targets: []sointu.LaneTarget{{Unit: 2, Param: "p1", Amount: 1}}, Points: sointu.AutomationCurve{{1, 3}}},
			{Slot: 9, Points: sointu.AutomationCurve{{2, 7}}},
			{Slot: 10, Targets: []sointu.LaneTarget{{Unit: 3, Param: "gain", Amount: 1}}},
		},
	}
}

func TestAutomationExpand(t *testing.T) {
	song := automationSong()
	before, _ := json.Marshal(song)
	if !song.NeedsExpand() {
		t.Fatal("NeedsExpand is false")
	}
	x, exp := song.Expand()
	if len(exp.Problems) > 0 {
		t.Fatal(exp.Problems)
	}
	if after, _ := json.Marshal(song); string(after) != string(before) {
		t.Error("Expand changed the song")
	}
	if x.Automation != nil {
		t.Error("the expanded song has automation")
	}
	var got []string
	for _, u := range x.Patch[0].Units {
		got = append(got, u.Type)
	}
	// the targets of the lanes are no units
	want := []string{"oscillator", "filter", "automation", "pop", "out"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units %v, want %v", got, want)
	}
	u := x.Patch[0].Units
	spr := song.SamplesPerRow()
	a := x.Patch[0].Automation
	if len(a) != 3 {
		t.Fatalf("the automation table: %v", a)
	}
	if !reflect.DeepEqual(a[0], sointu.AutomationTarget{Slot: 2, Unit: 1, Port: sointu.PortOf("oscillator", "detune"), Amount: 128, Points: []int{0, 192, 4 * spr, -128 * 128}}) {
		t.Errorf("the target of slot 3: %v", a[0])
	}
	// the target of the module unit is the filter of its copy
	if a[1].Slot != 4 || a[1].Unit != u[1].ID || a[1].Port != 0 || !reflect.DeepEqual(a[1].Points, []int{spr, 3 * 128}) {
		t.Errorf("the target of the module unit: %v, the filter #%d", a[1], u[1].ID)
	}
	// the unit placed by hand in the module has the lane of its slot
	if !reflect.DeepEqual(u[2].VarArgs, []int{2 * spr, 7 * 128}) {
		t.Errorf("the unit of the module: %v", u[2].VarArgs)
	}
	if a[2].Slot != 10 || a[2].Unit != 3 || len(a[2].Points) != 0 {
		t.Errorf("a lane without points: %v", a[2])
	}
	if sp, ok := exp.Spans[2]; !ok || sp.First != 1 || sp.Count != 3 {
		t.Errorf("the span of the module unit: %v", sp)
	}
	if a := song.Automation.Active(); len(a) != 3 || a[2].Slot != 9 {
		t.Errorf("Active: %v", a)
	}
}

func TestAutomationProblems(t *testing.T) {
	for name, lane := range map[string]sointu.Lane{
		"a missing unit":             {Slot: 0, Targets: []sointu.LaneTarget{{Unit: 99, Param: "gain", Amount: 1}}},
		"a parameter without port":   {Slot: 0, Targets: []sointu.LaneTarget{{Unit: 1, Param: "type", Amount: 1}}},
		"a slot that does not exist": {Slot: sointu.AutomationSlots, Targets: []sointu.LaneTarget{{Unit: 1, Param: "detune", Amount: 1}}},
		"a slot with a lane":         {Slot: 2, Targets: []sointu.LaneTarget{{Unit: 3, Param: "gain", Amount: 1}}},
	} {
		song := automationSong()
		song.Automation = append(song.Automation, lane)
		if _, exp := song.Expand(); len(exp.Problems) != 1 {
			t.Errorf("%s: problems %v", name, exp.Problems)
		}
	}
}

func TestAutomationSaved(t *testing.T) {
	song := automationSong()
	song.Automation[1].Unipolar = true
	y, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML sointu.Song
	if err := yaml.Unmarshal(y, &fromYAML); err != nil {
		t.Fatalf("%v\n%s", err, y)
	}
	if !reflect.DeepEqual(fromYAML.Automation, song.Automation) {
		t.Errorf("YAML: %v\n%s", fromYAML.Automation, y)
	}
	j, err := json.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON sointu.Song
	if err := json.Unmarshal(j, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromJSON.Automation, song.Automation) {
		t.Errorf("JSON: %v\n%s", fromJSON.Automation, j)
	}
	// a song without automation is saved as before
	song.Automation, song.Modules = nil, nil
	y, _ = yaml.Marshal(song)
	j, _ = json.Marshal(song)
	if strings.Contains(strings.ToLower(string(y)+string(j)), "automation") {
		t.Errorf("a song without automation mentions it")
	}
}

func TestReduceAutomation(t *testing.T) {
	// a ramp sampled every tenth of a row with a little noise, a step, a
	// plateau and a curve
	var points sointu.AutomationCurve
	for i := 0; i <= 100; i++ {
		points = append(points, sointu.AutomationPoint{Row: float64(i) / 10, Value: float64(i)/2 + 0.2*math.Sin(float64(i))})
	}
	points = append(points, sointu.AutomationPoint{Row: 10, Value: -20})
	for i := 1; i <= 50; i++ {
		points = append(points, sointu.AutomationPoint{Row: 10 + float64(i)/10, Value: -20})
	}
	for i := 1; i <= 200; i++ {
		x := float64(i) / 200
		points = append(points, sointu.AutomationPoint{Row: 15 + 10*x, Value: -20 + 100*x*x})
	}
	reduced := sointu.ReduceAutomation(points, 0.5)
	if len(reduced) < 8 || len(reduced) > 20 {
		t.Errorf("%d points left of %d", len(reduced), len(points))
	}
	for _, p := range points {
		if p.Row == 10 {
			continue // the step
		}
		if d := math.Abs(reduced.Value(p.Row) - p.Value); d > 0.5+1e-9 {
			t.Errorf("row %v: %v off", p.Row, d)
		}
	}
	if reduced[0] != points[0] || reduced[len(reduced)-1] != points[len(points)-1] {
		t.Error("the ends are not kept")
	}
	if got := (sointu.AutomationCurve{{1, 2}, {3, 6}}); got.Value(0) != 2 || got.Value(2) != 4 || got.Value(9) != 6 || (sointu.AutomationCurve{}).Value(1) != 0 {
		t.Error("Value")
	}
}

// TestAutomationTargets checks a lane with several targets and amounts,
// through a scaled binding of a module.
func TestAutomationTargets(t *testing.T) {
	song := automationSong()
	song.Modules[0].Units[0].Bind = map[string]sointu.Binding{"frequency": {Param: 1, Scaled: true, Min: 64, Max: 96}}
	song.Automation = sointu.Automation{{Slot: 0, Targets: []sointu.LaneTarget{
		{Unit: 1, Param: "detune", Amount: 1},
		{Unit: 3, Param: "gain", Amount: -0.5},
		{Unit: 2, Param: "p1", Amount: 1},
	}, Points: sointu.AutomationCurve{{0, 10}}}}
	x, exp := song.Expand()
	if len(exp.Problems) > 0 {
		t.Fatal(exp.Problems)
	}
	a := x.Patch[0].Automation
	if len(a) != 3 {
		t.Fatalf("the automation table: %v", a)
	}
	amounts := map[int]int{}
	for _, at := range a {
		amounts[at.Unit] = at.Amount
	}
	// -0.5 is 32 as the amount of a send, and the binding scales by a
	// quarter: 64 + 64/4
	if amounts[1] != 128 || amounts[3] != 32 || amounts[x.Patch[0].Units[1].ID] != 80 {
		t.Errorf("the amounts: %v", amounts)
	}
}

// TestAutomationLegacy reads a lane as songs had them before lanes had
// targets, and saves the amount only when it is not 1.
func TestAutomationLegacy(t *testing.T) {
	var song sointu.Song
	if err := yaml.Unmarshal([]byte("automation:\n  - slot: 3\n    unit: 12\n    param: gain\n    points: [[0, 1]]\n"), &song); err != nil {
		t.Fatal(err)
	}
	want := sointu.Automation{{Slot: 3, Targets: []sointu.LaneTarget{{Unit: 12, Param: "gain", Amount: 1}}, Points: sointu.AutomationCurve{{0, 1}}}}
	if !reflect.DeepEqual(song.Automation, want) {
		t.Errorf("YAML: %v", song.Automation)
	}
	var fromJSON sointu.Song
	if err := json.Unmarshal([]byte(`{"Automation":[{"Slot":3,"Unit":12,"Param":"gain","Points":[[0,1]]}]}`), &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromJSON.Automation, want) {
		t.Errorf("JSON: %v", fromJSON.Automation)
	}
	song.Automation[0].Targets = append(song.Automation[0].Targets, sointu.LaneTarget{Unit: 13, Param: "decay", Amount: -0.25})
	y, _ := yaml.Marshal(song.Automation)
	if strings.Count(string(y), "amount") != 1 || !strings.Contains(string(y), "{unit: 13, param: decay, amount: -0.25}") {
		t.Errorf("saved as\n%s", y)
	}
}

// TestHostSteps checks how a slot maps the range of the host to steps: a
// bipolar one as it is, a unipolar one from 0 at the bottom.
func TestHostSteps(t *testing.T) {
	for _, c := range []struct {
		host, steps float64
		unipolar    bool
	}{{-128, -128, false}, {0, 0, false}, {128, 128, false}, {-128, 0, true}, {0, 64, true}, {128, 128, true}} {
		if got := sointu.HostSteps(c.host, c.unipolar); got != c.steps {
			t.Errorf("HostSteps(%v, %v) = %v, want %v", c.host, c.unipolar, got, c.steps)
		}
		if got := sointu.HostValue(c.steps, c.unipolar); got != c.host {
			t.Errorf("HostValue(%v, %v) = %v, want %v", c.steps, c.unipolar, got, c.host)
		}
	}
}
