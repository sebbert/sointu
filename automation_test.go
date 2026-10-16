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
			{Slot: 2, Unit: 1, Param: "detune", Points: sointu.AutomationCurve{{0, 1.5}, {4, -200}}},
			{Slot: 4, Unit: 2, Param: "p1", Points: sointu.AutomationCurve{{1, 3}}},
			{Slot: 9, Points: sointu.AutomationCurve{{2, 7}}},
			{Slot: 10, Unit: 3, Param: "gain"},
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
	want := []string{"automation", "send", "oscillator", "automation", "send", "filter", "automation", "pop", "automation", "send", "out"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units %v, want %v", got, want)
	}
	u := x.Patch[0].Units
	spr := song.SamplesPerRow()
	if !reflect.DeepEqual(u[0].VarArgs, []int{0, 192, 4 * spr, -128 * 128}) || u[0].Parameters["slot"] != 2 {
		t.Errorf("the lane of slot 3: %v", u[0])
	}
	if s := u[1].Parameters; s["target"] != 1 || s["port"] != sointu.PortOf("oscillator", "detune") || s["amount"] != 128 || s["sendpop"] != 1 {
		t.Errorf("the send to the oscillator: %v", s)
	}
	// the send to the module unit is a send to the filter of its copy
	if s := u[4].Parameters; s["target"] != u[5].ID || s["port"] != 0 || !reflect.DeepEqual(u[3].VarArgs, []int{spr, 3 * 128}) {
		t.Errorf("the send to the module unit: %v, lane %v", s, u[3].VarArgs)
	}
	// the unit placed by hand in the module has the lane of its slot
	if !reflect.DeepEqual(u[6].VarArgs, []int{2 * spr, 7 * 128}) {
		t.Errorf("the unit of the module: %v", u[6].VarArgs)
	}
	if len(u[8].VarArgs) != 0 {
		t.Errorf("a lane without points: %v", u[8].VarArgs)
	}
	if a := song.Automation.Active(); len(a) != 3 || a[2].Slot != 9 {
		t.Errorf("Active: %v", a)
	}
}

func TestAutomationProblems(t *testing.T) {
	for name, lane := range map[string]sointu.Lane{
		"a missing unit":             {Slot: 0, Unit: 99, Param: "gain"},
		"a parameter without port":   {Slot: 0, Unit: 1, Param: "type"},
		"a slot that does not exist": {Slot: sointu.AutomationSlots, Unit: 1, Param: "detune"},
		"a slot with a lane":         {Slot: 2, Unit: 3, Param: "gain"},
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
