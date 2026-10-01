package sointu_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

func unit(typ string, id int, params map[string]int) sointu.Unit {
	u := sointu.Unit{Type: typ, ID: id, Parameters: sointu.ParamMap{}}
	for k, v := range params {
		u.Parameters[k] = v
	}
	return u
}

func bound(u sointu.Unit, bind map[string]int) sointu.Unit {
	u.Bind = map[string]sointu.Binding{}
	for name, k := range bind {
		u.Bind[name] = sointu.Binding{Param: k}
	}
	return u
}

// scaled binds a parameter of the unit to parameter k of the module, mapped
// onto lo to hi.
func scaled(u sointu.Unit, name string, k, lo, hi int) sointu.Unit {
	if u.Bind == nil {
		u.Bind = map[string]sointu.Binding{}
	}
	u.Bind[name] = sointu.Binding{Param: k, Scaled: true, Min: lo, Max: hi}
	return u
}

func call(id, module int, params ...int) sointu.Unit {
	u := unit("module", id, map[string]int{"module": module})
	for i, v := range params {
		u.Parameters[sointu.ModuleParamName(i+1)] = v
	}
	return u
}

func song(modules sointu.Modules, units ...sointu.Unit) sointu.Song {
	return sointu.Song{BPM: 100, Modules: modules, Patch: sointu.Patch{{NumVoices: 1, Units: units}}}
}

// summary lists the units of an instrument as type:parameter=value..., with
// the given parameters.
func summary(units []sointu.Unit, params ...string) string {
	var sb strings.Builder
	for i, u := range units {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(u.Type)
		for _, p := range params {
			if v, ok := u.Parameters[p]; ok {
				sb.WriteString(":" + p + "=" + itoa(v))
			}
		}
	}
	return sb.String()
}

func itoa(v int) string {
	b, _ := yaml.Marshal(v)
	return strings.TrimSpace(string(b))
}

func noProblems(t *testing.T, exp *sointu.Expansion) {
	t.Helper()
	for _, p := range exp.Problems {
		t.Errorf("problem: %v", p)
	}
}

func TestExpandWithoutModules(t *testing.T) {
	s := song(sointu.Modules{{ID: 1, Units: []sointu.Unit{unit("noise", 0, nil)}}}, unit("noise", 3, nil), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	if !reflect.DeepEqual(got, s) {
		t.Errorf("a song without module units changed: %v", got)
	}
	if &got.Patch[0].Units[0] != &s.Patch[0].Units[0] {
		t.Errorf("a song without module units was copied")
	}
}

func TestExpandBinds(t *testing.T) {
	saw := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "detune"}, {Name: "type"}}, Units: []sointu.Unit{
		bound(unit("oscillator", 0, map[string]int{"detune": 64, "transpose": 70, "type": 1}), map[string]int{"detune": 1, "type": 2}),
		bound(unit("oscillator", 0, map[string]int{"detune": 64, "transpose": 71}), map[string]int{"detune": 1}),
		unit("addp", 0, nil),
		unit("noise", 0, nil), {Type: "noise", Disabled: true, Parameters: sointu.ParamMap{}},
	}}
	s := song(sointu.Modules{saw}, call(0, 1, 10, 2), unit("gain", 0, nil), call(5, 1, 200, 99), sointu.Unit{Type: "module", Disabled: true, Parameters: sointu.ParamMap{"module": 1}}, call(0, 0), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	want := "oscillator:detune=10:transpose=70:type=2 oscillator:detune=10:transpose=71 addp noise gain " +
		"oscillator:detune=128:transpose=70:type=4 oscillator:detune=128:transpose=71 addp noise out" // clamped to the ranges
	if s := summary(got.Patch[0].Units, "detune", "transpose", "type"); s != want {
		t.Errorf("got  %v\nwant %v", s, want)
	}
	if got.Modules != nil {
		t.Errorf("the expanded song has modules")
	}
	for _, u := range got.Patch[0].Units {
		if u.Bind != nil || u.Type == "module" {
			t.Errorf("the expanded song has bindings or module units: %v", u)
		}
	}
	if len(s.Patch[0].Units) != 6 || s.Modules[0].Units[0].Parameters["detune"] != 64 {
		t.Errorf("Expand changed the song")
	}
	if n := s.Modules.NumExpandedUnits(s.Patch[0].Units); n != 10 {
		t.Errorf("NumExpandedUnits: got %v, want 10", n)
	}
}

func TestExpandDefaults(t *testing.T) {
	m := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "gain", Default: 100}}, Units: []sointu.Unit{
		bound(unit("gain", 0, map[string]int{"gain": 1}), map[string]int{"gain": 1}),
	}}
	s := song(sointu.Modules{m}, unit("noise", 0, nil), call(0, 1), unit("out", 0, nil)) // p1 not set
	got, exp := s.Expand()
	noProblems(t, exp)
	if s, want := summary(got.Patch[0].Units, "gain"), "noise gain:gain=100 out"; s != want {
		t.Errorf("got %v, want %v", s, want)
	}
	if u := s.Modules.MakeModuleUnit(0); u.Parameters["p1"] != 100 || u.Parameters["module"] != 1 {
		t.Errorf("MakeModuleUnit: %v", u)
	}
}

func TestExpandSends(t *testing.T) {
	// a send in the module to a unit of the module goes to the copy made
	// with it; a send to another unit stays
	m := sointu.Module{ID: 1, Units: []sointu.Unit{
		unit("oscillator", 0, map[string]int{"lfo": 1}),
		unit("send", 0, map[string]int{"target": 10, "port": 1, "sendpop": 1}),
		unit("noise", 0, nil),
		unit("send", 0, map[string]int{"target": 20, "port": 0}),
		unit("filter", 10, nil),
	}}
	s := song(sointu.Modules{m}, call(0, 1), call(0, 1), unit("out", 20, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	if len(u) != 11 {
		t.Fatalf("got %v units: %v", len(u), summary(u))
	}
	for i := 0; i < 2; i++ {
		send, out, filter := u[5*i+1], u[5*i+3], u[5*i+4]
		if filter.ID <= 20 || send.Parameters["target"] != filter.ID || send.Parameters["port"] != 1 {
			t.Errorf("copy %v: send to %v, filter is %v", i, send.Parameters["target"], filter.ID)
		}
		if out.Parameters["target"] != 20 {
			t.Errorf("copy %v: the send to a unit outside the module goes to %v", i, out.Parameters["target"])
		}
		if e := exp.Units[filter.ID]; e.Body != 10 || e.Module != 1 {
			t.Errorf("copy %v: Expansion.Units has %v for the filter", i, e)
		}
	}
	if u[4].ID == u[9].ID {
		t.Errorf("the copies of the filter share the ID %v", u[4].ID)
	}
}

func TestExpandPorts(t *testing.T) {
	// a send to a module unit modulates the parameters bound to the port
	m := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "cutoff"}, {Name: "type"}, {Name: "unused"}}, Units: []sointu.Unit{
		bound(unit("filter", 0, map[string]int{"frequency": 64, "resonance": 64}), map[string]int{"frequency": 1}),
		bound(unit("oscillator", 0, nil), map[string]int{"type": 2, "color": 1}),
	}}
	lfo := unit("oscillator", 0, map[string]int{"lfo": 1})
	s := song(sointu.Modules{m},
		lfo, unit("send", 30, map[string]int{"target": 7, "port": 0, "sendpop": 1, "amount": 96}),
		lfo, unit("send", 0, map[string]int{"target": 7, "port": 0, "amount": 96}), unit("pop", 0, nil),
		lfo, unit("send", 0, map[string]int{"target": 7, "port": 2, "sendpop": 1}), // nothing bound
		lfo, unit("send", 0, map[string]int{"target": 7, "port": 1, "sendpop": 1}), // not modulatable
		unit("noise", 0, nil), call(7, 1, 50, 1), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	want := "oscillator send:sendpop=0:amount=96 send:sendpop=1:amount=96 " +
		"oscillator send:sendpop=0:amount=96 send:sendpop=0:amount=96 pop " +
		"oscillator pop oscillator pop noise filter oscillator out"
	if s := summary(u, "sendpop", "amount"); s != want {
		t.Fatalf("got  %v\nwant %v", s, want)
	}
	filter, osc := u[12], u[13]
	if filter.ID == 0 || osc.ID == 0 || filter.ID == osc.ID {
		t.Fatalf("the modulated units have the IDs %v and %v", filter.ID, osc.ID)
	}
	// frequency is port 0 of the filter, color port 3 of the oscillator
	if p := u[1].Parameters; p["target"] != filter.ID || p["port"] != 0 {
		t.Errorf("the first send goes to %v port %v", p["target"], p["port"])
	}
	if p := u[2].Parameters; p["target"] != osc.ID || p["port"] != 3 {
		t.Errorf("the second send goes to %v port %v", p["target"], p["port"])
	}
	if u[1].ID != 30 || u[2].ID != 0 {
		t.Errorf("the sends have the IDs %v and %v", u[1].ID, u[2].ID)
	}
}

func TestExpandNested(t *testing.T) {
	inner := sointu.Module{ID: 1, Inputs: 1, Params: []sointu.ModuleParam{{Name: "gain"}}, Units: []sointu.Unit{
		bound(unit("gain", 0, map[string]int{"gain": 64}), map[string]int{"gain": 1}),
	}}
	outer := sointu.Module{ID: 2, Params: []sointu.ModuleParam{{Name: "a"}, {Name: "level"}}, Units: []sointu.Unit{
		unit("noise", 0, nil),
		bound(call(0, 1, 5), map[string]int{"p1": 2}),
		call(0, 1, 77),
	}}
	lfo := unit("oscillator", 0, map[string]int{"lfo": 1})
	s := song(sointu.Modules{inner, outer}, lfo, unit("send", 0, map[string]int{"target": 9, "port": 1, "sendpop": 1}), call(9, 2, 0, 33), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	if s, want := summary(u, "gain"), "oscillator send noise gain:gain=33 gain:gain=77 out"; s != want {
		t.Fatalf("got  %v\nwant %v", s, want)
	}
	if u[3].ID == 0 || u[1].Parameters["target"] != u[3].ID || u[1].Parameters["port"] != 0 {
		t.Errorf("the send goes to %v port %v, the gain is %v", u[1].Parameters["target"], u[1].Parameters["port"], u[3].ID)
	}
	if n, err := s.Modules.Outputs(1); n != 1 || err != nil {
		t.Errorf("Outputs: %v, %v", n, err)
	}
	if !s.Modules.Uses(2, 1) || s.Modules.Uses(1, 2) {
		t.Errorf("Uses is wrong")
	}
}

func TestExpandProblems(t *testing.T) {
	a := sointu.Module{ID: 1, Name: "a", Units: []sointu.Unit{unit("noise", 40, nil), call(0, 2)}}
	b := sointu.Module{ID: 2, Name: "b", Units: []sointu.Unit{call(0, 1)}}
	c := sointu.Module{ID: 3, Name: "c", Units: []sointu.Unit{bound(unit("noise", 0, nil), map[string]int{"stereo": 1})}}
	for name, s := range map[string]sointu.Song{
		"uses itself":          song(sointu.Modules{a, b}, call(0, 1), unit("out", 0, nil)),
		"missing":              song(nil, call(0, 5)),
		"which is not":         song(sointu.Modules{c}, call(0, 3)),
		"from outside":         song(sointu.Modules{c, a, b}, call(0, 3), unit("send", 0, map[string]int{"target": 40})),
		"does not have":        song(sointu.Modules{c}, call(4, 9)),
		"module b uses itself": song(sointu.Modules{a, b}, call(0, 2)),
	} {
		_, exp := s.Expand()
		found := false
		for _, p := range exp.Problems {
			found = found || strings.Contains(p.Error(), name)
		}
		if !found && name != "missing" {
			t.Errorf("%v: got the problems %v", name, exp.Problems)
		}
	}
	if _, err := (sointu.Modules{a, b}).Outputs(0); err == nil {
		t.Errorf("Outputs of a module using itself did not fail")
	}
}

func TestExpandBuffers(t *testing.T) {
	// buffer 1 belongs to the module, buffer 2 is also used by the
	// instrument, buffer 3 was not created by the tracker
	m := sointu.Module{ID: 1, Units: []sointu.Unit{
		unit("mcspread", 0, map[string]int{"bus": 1}),
		unit("mcsum", 0, map[string]int{"bus": 1}),
		unit("spfft", 0, map[string]int{"buffer": 2}),
		unit("bufread", 0, map[string]int{"buffer": 3}),
		unit("mcloop", 0, map[string]int{"bus": 4}),
	}}
	s := song(sointu.Modules{m}, call(0, 1), call(0, 1), call(0, 1), unit("spifft", 0, map[string]int{"buffer": 2}))
	s.Buffers = sointu.Buffers{{ID: 1, Bus: true, Auto: true}, {ID: 2, Spectrum: true, Auto: true}, {ID: 3, Channels: 1, Frames: 10}, {ID: 4, Bus: true, Auto: true}}
	got, exp := s.Expand()
	noProblems(t, exp)
	want := "mcspread:bus=1 mcsum:bus=1 spfft:buffer=2 bufread:buffer=3 mcloop:bus=4 " +
		"mcspread:bus=5 mcsum:bus=5 spfft:buffer=2 bufread:buffer=3 mcloop:bus=6 " +
		"mcspread:bus=7 mcsum:bus=7 spfft:buffer=2 bufread:buffer=3 mcloop:bus=8 spifft:buffer=2"
	if s := summary(got.Patch[0].Units, "bus", "buffer"); s != want {
		t.Errorf("got  %v\nwant %v", s, want)
	}
	if len(got.Buffers) != 8 || !got.Buffers[4].Bus || got.Buffers[4].ID != 5 || len(s.Buffers) != 4 {
		t.Errorf("buffers: %v", got.Buffers)
	}
	if !reflect.DeepEqual(exp.Buffers, map[int]int{5: 1, 6: 4, 7: 1, 8: 4}) {
		t.Errorf("Expansion.Buffers: %v", exp.Buffers)
	}
}

func TestModuleStackUse(t *testing.T) {
	m := sointu.Modules{
		{ID: 1, Inputs: 1, Units: []sointu.Unit{unit("pan", 0, nil), unit("gain", 0, map[string]int{"stereo": 1})}},
		{ID: 2, Units: []sointu.Unit{unit("addp", 0, nil)}}, // underflows
	}
	c := call(0, 1)
	if use := m.StackUse(&c); len(use.Inputs) != 1 || use.NumOutputs != 2 {
		t.Errorf("stack use of a module unit: %v", use)
	}
	if n, err := m.Outputs(0); n != 2 || err != nil {
		t.Errorf("Outputs: %v, %v", n, err)
	}
	if _, err := m.Outputs(1); err == nil {
		t.Errorf("Outputs of a module that underflows did not fail")
	}
	c = call(0, 2)
	if use := m.StackUse(&c); len(use.Inputs) != 0 || use.NumOutputs != 0 {
		t.Errorf("stack use of a unit of a broken module: %v", use)
	}
	n := unit("noise", 0, nil)
	if use := m.StackUse(&n); use.NumOutputs != 1 {
		t.Errorf("stack use of a noise: %v", use)
	}
}

func TestModuleParam(t *testing.T) {
	m := sointu.Modules{{ID: 1, Params: []sointu.ModuleParam{
		{Name: "cutoff", Default: 300},
		{Name: "wave", Default: 9, Display: "filter.frequency"},
		{},
	}, Units: []sointu.Unit{
		bound(unit("filter", 0, nil), map[string]int{"frequency": 1}),
		bound(unit("oscillator", 0, nil), map[string]int{"type": 2, "transpose": 1}),
	}}}
	p, ok := m.Param(0, 1)
	if !ok || p.Name != "cutoff" || p.MinValue != 0 || p.MaxValue != 128 || p.Default != 128 || p.DisplayFunc == nil || !p.CanModulate {
		t.Errorf("parameter 1: %+v", p)
	}
	if typ, name, _ := m.ParamSource(0, 1); typ != "filter" || name != "frequency" {
		t.Errorf("parameter 1 is like %v.%v", typ, name)
	}
	p, _ = m.Param(0, 2)
	if p.MinValue != 0 || p.MaxValue != 4 || p.Default != 4 || p.CanModulate || p.DisplayFunc == nil {
		t.Errorf("parameter 2: %+v", p)
	}
	if p, _ = m.Param(0, 3); p.Name != "p3" || p.MaxValue != 128 {
		t.Errorf("parameter 3: %+v", p)
	}
	if _, ok := m.Param(0, 4); ok {
		t.Errorf("the module has a parameter 4")
	}
}

func TestModuleYAML(t *testing.T) {
	s := song(sointu.Modules{{ID: 1, Name: "saw", Inputs: 1, Params: []sointu.ModuleParam{{Name: "detune", Default: 32}}, Units: []sointu.Unit{
		bound(unit("oscillator", 2, map[string]int{"detune": 64}), map[string]int{"detune": 1}),
	}}}, call(0, 1, 40))
	out, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "bind: {detune: 1}") {
		t.Errorf("the bindings are not in the flow style:\n%s", out)
	}
	var got sointu.Song
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Modules[0].Params, s.Modules[0].Params) || got.Modules[0].Units[0].Bind["detune"] != (sointu.Binding{Param: 1}) || got.Modules[0].Inputs != 1 {
		t.Errorf("modules after a round trip: %+v", got.Modules)
	}
	// songs without modules are saved as before
	plain := song(nil, unit("noise", 0, nil))
	out, _ = yaml.Marshal(plain)
	if strings.Contains(string(out), "modules") || strings.Contains(string(out), "bind") {
		t.Errorf("a song without modules mentions them:\n%s", out)
	}
}

func TestExpandPortOnlyBinding(t *testing.T) {
	// the inputs of a receive can only be modulated: bound to a parameter of
	// the module, a send to the module unit reaches the receive
	m := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "in"}}, Units: []sointu.Unit{
		bound(unit("receive", 0, nil), map[string]int{"left": 1}),
	}}
	s := song(sointu.Modules{m}, unit("noise", 0, nil), unit("send", 0, map[string]int{"target": 3, "port": 0, "sendpop": 1}), call(3, 1), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	if s := summary(u); s != "noise send receive out" {
		t.Fatalf("got %v", s)
	}
	if u[2].ID == 0 || u[1].Parameters["target"] != u[2].ID || u[1].Parameters["port"] != 0 {
		t.Errorf("the send goes to %v port %v, the receive is %v", u[1].Parameters["target"], u[1].Parameters["port"], u[2].ID)
	}
	if _, ok := u[2].Parameters["left"]; ok {
		t.Errorf("the receive got a value for its input")
	}
	if p, _ := s.Modules.Param(0, 1); p.CanSet || !p.CanModulate {
		t.Errorf("the parameter of the module: %+v", p)
	}
}

func TestExpandStereoSend(t *testing.T) {
	// a stereo send to a module unit: the top signal to the parameters of
	// the port, the one below to those of the next port
	m := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "a"}, {Name: "b"}}, Units: []sointu.Unit{
		bound(unit("filter", 0, nil), map[string]int{"frequency": 1, "resonance": 2}),
		bound(unit("gain", 0, nil), map[string]int{"gain": 2}),
	}}
	lfo := unit("oscillator", 0, map[string]int{"lfo": 1})
	s := song(sointu.Modules{m}, lfo, lfo, unit("send", 0, map[string]int{"target": 7, "port": 0, "stereo": 1, "sendpop": 1, "amount": 90}),
		unit("noise", 0, nil), call(7, 1), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	// the frequency and the resonance of the filter are ports next to each
	// other: a stereo send; the gain gets the signal below on its own
	want := "oscillator oscillator send:stereo=1:sendpop=0:amount=90 xch send:stereo=0:sendpop=0:amount=90 xch pop:stereo=1 noise filter gain out"
	if s := summary(u, "stereo", "sendpop", "amount"); s != want {
		t.Fatalf("got  %v\nwant %v", s, want)
	}
	filter, gain := u[8], u[9]
	if p := u[2].Parameters; p["target"] != filter.ID || p["port"] != 0 {
		t.Errorf("the stereo send goes to %v port %v", p["target"], p["port"])
	}
	if p := u[4].Parameters; p["target"] != gain.ID || p["port"] != 0 {
		t.Errorf("the send of the signal below goes to %v port %v", p["target"], p["port"])
	}
}

func TestExpandDelayTimes(t *testing.T) {
	// the delay times of a delay unit can be bound
	delay := bound(sointu.Unit{Type: "delay", Parameters: sointu.ParamMap{"notetracking": 2}, VarArgs: []int{24, 48}}, map[string]int{"delaytime2": 1})
	m := sointu.Module{ID: 1, Inputs: 1, Params: []sointu.ModuleParam{{Name: "time", Default: 48}}, Units: []sointu.Unit{delay}}
	s := song(sointu.Modules{m}, unit("noise", 0, nil), call(0, 1, 36), call(0, 1), call(0, 1, 100000), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	if len(u) != 5 || !reflect.DeepEqual(u[1].VarArgs, []int{24, 36}) || !reflect.DeepEqual(u[2].VarArgs, []int{24, 48}) || !reflect.DeepEqual(u[3].VarArgs, []int{24, 65535}) {
		t.Errorf("the delay times: %v %v %v", u[1].VarArgs, u[2].VarArgs, u[3].VarArgs)
	}
	if !reflect.DeepEqual(s.Modules[0].Units[0].VarArgs, []int{24, 48}) {
		t.Errorf("Expand changed the module")
	}
	p, _ := s.Modules.Param(0, 1)
	if typ, name, _ := s.Modules.ParamSource(0, 1); typ != "delay" || name != "delaytime2" || p.MinValue != 1 || p.MaxValue != 65535 || p.CanModulate {
		t.Errorf("the parameter: like %v.%v, %+v", typ, name, p)
	}
	if !sointu.CanBind("delay", "delaytime3") || sointu.CanBind("filter", "delaytime1") || sointu.CanBind("delay", "delaytime0") {
		t.Errorf("CanBind is wrong for delay times")
	}
}

func TestExpandStereoSendToPair(t *testing.T) {
	// two ports of the module bound to the left and right of a receive: a
	// stereo send to the module unit stays one stereo send
	m := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "left"}, {Name: "right"}}, Units: []sointu.Unit{
		bound(unit("receive", 0, map[string]int{"stereo": 1}), map[string]int{"left": 1, "right": 2}),
	}}
	lfo := unit("oscillator", 0, map[string]int{"lfo": 1})
	send := unit("send", 6, map[string]int{"target": 7, "port": 0, "stereo": 1, "sendpop": 1, "amount": 90})
	s := song(sointu.Modules{m}, lfo, lfo, send, call(7, 1), unit("out", 0, map[string]int{"stereo": 1}))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	if s, want := summary(u, "stereo", "sendpop", "amount"), "oscillator oscillator send:stereo=1:sendpop=1:amount=90 receive:stereo=1 out:stereo=1"; s != want {
		t.Fatalf("got  %v\nwant %v", s, want)
	}
	if p := u[2].Parameters; p["target"] != u[3].ID || p["port"] != 0 || u[2].ID != 6 {
		t.Errorf("the send %v goes to %v port %v, the receive is %v", u[2].ID, p["target"], p["port"], u[3].ID)
	}
	// with another parameter bound to the first port, that one gets a mono send
	m.Units = append(m.Units, bound(unit("gain", 0, nil), map[string]int{"gain": 1}))
	s = song(sointu.Modules{m}, lfo, lfo, send, call(7, 1), unit("out", 0, map[string]int{"stereo": 1}))
	got, exp = s.Expand()
	noProblems(t, exp)
	if s, want := summary(got.Patch[0].Units, "stereo", "sendpop"), "oscillator oscillator send:stereo=1:sendpop=0 send:stereo=0:sendpop=0 pop:stereo=1 receive:stereo=1 gain out:stereo=1"; s != want {
		t.Errorf("got  %v\nwant %v", s, want)
	}
}

func TestExpandScaledBindings(t *testing.T) {
	// one parameter of the module moves the cutoff from 40 to 100 and the
	// resonance from 80 down to 16, and a parameter of a module in it
	inner := sointu.Module{ID: 1, Inputs: 1, Params: []sointu.ModuleParam{{Name: "gain"}}, Units: []sointu.Unit{
		scaled(unit("gain", 0, nil), "gain", 1, 0, 64),
	}}
	m := sointu.Module{ID: 2, Inputs: 1, Params: []sointu.ModuleParam{{Name: "bright", Default: 32}}, Units: []sointu.Unit{
		scaled(scaled(unit("filter", 0, nil), "frequency", 1, 40, 100), "resonance", 1, 80, 16),
		scaled(call(0, 1), "p1", 1, 128, 0),
	}}
	lfo := unit("oscillator", 0, map[string]int{"lfo": 1})
	s := song(sointu.Modules{inner, m},
		lfo, unit("send", 0, map[string]int{"target": 9, "port": 0, "sendpop": 1, "amount": 128}),
		unit("noise", 0, nil), call(9, 2, 64), call(0, 2), call(0, 2, 128), call(0, 2, 500), unit("out", 0, nil))
	got, exp := s.Expand()
	noProblems(t, exp)
	u := got.Patch[0].Units
	want := "oscillator send:amount=94 send:amount=32 send:amount=32 noise " +
		"filter:frequency=70:resonance=48 gain:gain=32 " + // at 64: half way; the gain 128 to 0, then 0 to 64
		"filter:frequency=55:resonance=64 gain:gain=48 " + // the default, 32
		"filter:frequency=100:resonance=16 gain:gain=0 " +
		"filter:frequency=100:resonance=16 gain:gain=0 out" // clamped to 128
	if s := summary(u, "amount", "frequency", "resonance", "gain"); s != want {
		t.Fatalf("got  %v\nwant %v", s, want)
	}
	// the sends: to the frequency by 60/128, the resonance by -64/128 and
	// the gain by -1 times 64/128 of the amount of 1
	filter, gain := u[5], u[6]
	for i, wantTarget := range []struct{ id, port int }{{filter.ID, 0}, {filter.ID, 1}, {gain.ID, 0}} {
		if p := u[1+i].Parameters; p["target"] != wantTarget.id || p["port"] != wantTarget.port || (p["sendpop"] == 1) != (i == 2) {
			t.Errorf("send %v goes to %v port %v, popping %v", i, p["target"], p["port"], p["sendpop"])
		}
	}
	// the parameter of the module is 0 to 128, shown as the cutoff
	p, _ := s.Modules.Param(1, 1)
	if p.MinValue != 0 || p.MaxValue != 128 || !p.CanModulate || p.DisplayFunc == nil {
		t.Fatalf("the parameter of the module: %+v", p)
	}
	a, _ := p.DisplayFunc(64)
	b, _ := sointu.UnitTypes["filter"].Params[1].DisplayFunc(70)
	if sointu.UnitTypes["filter"].Params[1].Name != "frequency" || a != b {
		t.Errorf("64 is shown as %q, want %q", a, b)
	}
	// Binding maps and back
	bd := sointu.Binding{Param: 1, Scaled: true, Min: 80, Max: 16}
	if bd.Map(0) != 80 || bd.Map(128) != 16 || bd.Map(64) != 48 || bd.Unmap(48) != 64 || bd.Unmap(200) != 0 || bd.Scale() != -0.5 {
		t.Errorf("the binding maps 0, 64, 128 to %v, %v, %v, 48 back to %v, scale %v", bd.Map(0), bd.Map(64), bd.Map(128), bd.Unmap(48), bd.Scale())
	}
	// in YAML
	out, err := yaml.Marshal(s.Modules[1].Units[0])
	if err != nil || !strings.Contains(string(out), "frequency: {p: 1, min: 40, max: 100}") {
		t.Errorf("the scaled binding in YAML: %s (%v)", out, err)
	}
	var back sointu.Unit
	if err := yaml.Unmarshal(out, &back); err != nil || !reflect.DeepEqual(back.Bind, s.Modules[1].Units[0].Bind) {
		t.Errorf("the bindings after a round trip: %+v (%v)", back.Bind, err)
	}
}

// TestModulePortsAndSendToPorts checks what the tracker uses to re-point the
// sends to a module unit when the units of its module take its place.
func TestModulePortsAndSendToPorts(t *testing.T) {
	mod := sointu.Module{ID: 1, Params: []sointu.ModuleParam{{Name: "a"}, {Name: "b"}}, Units: []sointu.Unit{
		{Type: "filter", ID: 1, Parameters: sointu.ParamMap{"frequency": 40}, Bind: map[string]sointu.Binding{
			"frequency": {Param: 1}, "resonance": {Param: 1, Scaled: true, Min: 0, Max: 64}}},
		{Type: "module", ID: 2, Parameters: sointu.ParamMap{"module": 9}, Bind: map[string]sointu.Binding{"p2": {Param: 1}}},
		{Type: "oscillator", ID: 3, Disabled: true, Bind: map[string]sointu.Binding{"detune": {Param: 1}}},
	}}
	ports := mod.Ports([]int{11, 12, 13})
	frequency, resonance := sointu.PortOf("filter", "frequency"), sointu.PortOf("filter", "resonance")
	want := []sointu.PortTarget{{Unit: 11, Port: frequency, Scale: 1}, {Unit: 11, Port: resonance, Scale: 0.5}, {Unit: 12, Port: 1, Scale: 1}}
	if !reflect.DeepEqual(ports[0], want) || ports[1] != nil {
		t.Fatalf("the ports are %+v, want %+v", ports, want)
	}
	send := sointu.MakeUnit("send")
	send.ID = 5
	send.Parameters["amount"], send.Parameters["sendpop"], send.Parameters["port"] = 96, 1, 0
	out := sointu.SendToPorts(send, &ports)
	if len(out) != 3 {
		t.Fatalf("the send became %v units", len(out))
	}
	for i, w := range []struct{ id, target, port, amount, pop int }{
		{5, 11, frequency, 96, 0}, {0, 11, resonance, 80, 0}, {0, 12, 1, 96, 1},
	} {
		p := out[i].Parameters
		if out[i].Type != "send" || out[i].ID != w.id || p["target"] != w.target || p["port"] != w.port || p["amount"] != w.amount || p["sendpop"] != w.pop {
			t.Errorf("unit %v: %+v, want %+v", i, out[i], w)
		}
	}
	// a send to a port that modulates nothing: a pop if it pops
	send.Parameters["port"] = 1
	if out := sointu.SendToPorts(send, &ports); len(out) != 1 || out[0].Type != "pop" {
		t.Errorf("a popping send to nothing became %+v", out)
	}
}
