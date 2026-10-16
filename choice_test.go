package sointu

import "testing"

// TestEveryChoiceIsAChoice checks that the parameters whose values have names
// (an arrDispFunc, which names the values below 0 "???") are
// choices, and that every choice names all its values.
func TestEveryChoiceIsAChoice(t *testing.T) {
	for typ, ut := range UnitTypes {
		for _, p := range ut.Params {
			named := p.DisplayFunc != nil
			if named {
				s, _ := p.DisplayFunc(p.MinValue - 1)
				named = s == "???"
			}
			if named != p.Choice {
				t.Errorf("%s.%s: named values %v, Choice %v", typ, p.Name, named, p.Choice)
			}
			for v := p.MinValue; p.Choice && v <= p.MaxValue; v++ {
				if s, unit := p.DisplayFunc(v); s == "???" || unit != "" {
					t.Errorf("%s.%s: value %d is %q %q", typ, p.Name, v, s, unit)
				}
			}
		}
	}
}

// TestModuleChoiceParam checks that a module parameter is a choice when the
// first parameter bound to it is, unless the binding is scaled, also through
// a module unit; and with a display override only within its range.
func TestModuleChoiceParam(t *testing.T) {
	osc := func(b Binding) Unit {
		u := MakeUnit("oscillator")
		u.ID = 10
		u.Bind = map[string]Binding{"type": b}
		return u
	}
	call := MakeUnit("module")
	call.ID = 20
	call.Parameters["module"] = 1
	call.Bind = map[string]Binding{"p1": {Param: 1}}
	mods := Modules{
		{ID: 1, Params: []ModuleParam{{Name: "shape"}}, Units: []Unit{osc(Binding{Param: 1})}},
		{ID: 2, Params: []ModuleParam{{Name: "shape"}}, Units: []Unit{osc(Binding{Param: 1, Scaled: true, Min: 0, Max: 4})}},
		{ID: 3, Params: []ModuleParam{{Name: "shape"}}, Units: []Unit{call}},
		{ID: 4, Params: []ModuleParam{{Name: "plain", Display: "oscillator.type"}}},
	}
	for i, want := range []bool{true, false, true, false} {
		p, ok := mods.Param(i, 1)
		if !ok || p.Choice != want {
			t.Errorf("module %d: ok %v, Choice %v, want %v", mods[i].ID, ok, p.Choice, want)
		}
	}
	if p, _ := mods.Param(2, 1); p.MaxValue != int(Sample) || p.DisplayFunc == nil {
		t.Errorf("through a module unit: range %d..%d", p.MinValue, p.MaxValue)
	}
}
