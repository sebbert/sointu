package tracker

import (
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

// TestEveryChoiceIsAMenu checks that the choices of every unit type are
// menus in the unit editor, named as the unit type displays them.
func TestEveryChoiceIsAMenu(t *testing.T) {
	for typ, ut := range sointu.UnitTypes {
		for i := range ut.Params {
			up := &ut.Params[i]
			if !up.Choice {
				continue
			}
			u := sointu.MakeUnit(typ)
			p := Parameter{unit: &u, up: up, vtable: &namedParameter{}}
			if p.Type() != ChoiceParameter {
				t.Errorf("%s.%s has type %v, want ChoiceParameter", typ, up.Name, p.Type())
			}
			want, _ := up.DisplayFunc(up.MaxValue)
			menu := p.Int()
			if got := menu.StringOf(up.MaxValue); got != want {
				t.Errorf("%s.%s: the menu names %d %q, want %q", typ, up.Name, up.MaxValue, got, want)
			}
		}
	}
}

// TestModuleChoiceIsAMenu checks that a parameter of a module unit is a menu
// when the module binds a choice to it, also through a module unit of another
// module, and that the default of the module parameter is named.
func TestModuleChoiceIsAMenu(t *testing.T) {
	broker := NewBroker()
	m := NewModel(broker, []sointu.Synther{vm.GoSynther{}}, NullMIDIContext{}, "")
	t.Cleanup(m.Close)
	osc := sointu.MakeUnit("oscillator")
	osc.ID = 10
	osc.Bind = map[string]sointu.Binding{"type": {Param: 1}}
	inner := sointu.MakeUnit("module")
	inner.ID = 11
	inner.Parameters["module"] = 1
	inner.Bind = map[string]sointu.Binding{"p1": {Param: 1}}
	outer := sointu.MakeUnit("module")
	outer.ID = 1
	outer.Parameters["module"] = 2
	outer.Parameters["p1"] = int(sointu.Pulse)
	func() {
		defer m.change("Test", SongChange, MajorChange)()
		m.d.Song.Modules = sointu.Modules{
			{ID: 1, Name: "Shape", Params: []sointu.ModuleParam{{Name: "shape", Default: int(sointu.Trisaw)}}, Units: []sointu.Unit{osc}},
			{ID: 2, Name: "Outer", Params: []sointu.ModuleParam{{Name: "shape", Default: int(sointu.Trisaw)}}, Units: []sointu.Unit{inner}},
		}
		m.d.Song.Patch = sointu.Patch{{Name: "lead", NumVoices: 1, Units: []sointu.Unit{outer, {Type: "out", ID: 2, Parameters: sointu.ParamMap{"stereo": 0}}}}}
		m.d.Song.Score.Tracks = []sointu.Track{{NumVoices: 1}}
	}()
	var shape Parameter
	for x := 0; x < m.Params().RowWidth(0); x++ {
		if p := m.Params().Item(Point{x, 0}); p.Name() == "shape" {
			shape = p
		}
	}
	if shape.Type() != ChoiceParameter {
		t.Fatalf("the shape of the module unit has type %v, want ChoiceParameter", shape.Type())
	}
	menu := shape.Int()
	if label, _ := shape.ChoiceLabel(); label != "pulse" || menu.StringOf(int(sointu.Sample)) != "sample" {
		t.Errorf("the shape is labelled %q, the menu names %q", label, menu.StringOf(int(sointu.Sample)))
	}
	if !menu.SetValue(int(sointu.Gate)) || m.d.Song.Patch[0].Units[0].Parameters["p1"] != int(sointu.Gate) {
		t.Errorf("the module unit has p1 %v, want %v", m.d.Song.Patch[0].Units[0].Parameters["p1"], sointu.Gate)
	}
	for i := range m.d.Song.Modules {
		m.d.ModuleIndex = i
		def := m.Module().ParamDefault(1)
		if p, _ := m.Module().Param(1); !p.Choice || def.String() != "trisaw" {
			t.Errorf("module %d: Choice %v, default %q", i+1, p.Choice, def.String())
		}
	}
}
