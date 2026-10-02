package tracker

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
)

// newEQTestModel returns a model whose instrument is that of
// newModuleTestModel with an eq unit before its out, selected.
func newEQTestModel(t *testing.T) (*Model, *Broker) {
	t.Helper()
	m, broker := newModuleTestModel(t)
	m.Unit().List().SetSelected(5)
	m.Unit().Add(false).Do()
	m.Unit().SetType("eq")
	if !m.EQ().Active() || m.EQ().NumBands() != 0 || m.EQ().Selected() != -1 {
		t.Fatalf("the new eq unit is not selected, or has bands: %+v", m.selectedUnit())
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter out" {
		t.Fatalf("with an eq without bands, the player got: %v", got)
	}
	return m, broker
}

func TestEQModel(t *testing.T) {
	m, broker := newEQTestModel(t)
	eq := m.EQ()
	if count, _ := m.Unit().ExpandedUnits(); count != 7 {
		t.Errorf("an eq without bands counts as units: %d", count)
	}
	// bands become units of the player
	if i := eq.Add(sointu.EQBand{Type: sointu.EQLowCut, Frequency: 80}); i != 0 || eq.Selected() != 0 {
		t.Fatalf("the first band is %d, band %d selected", i, eq.Selected())
	}
	eq.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 1000.123456, Gain: 3.14159, Q: 1.23456})
	eq.Add(sointu.EQBand{Type: sointu.EQHighShelf, Frequency: 2000, Gain: -6})
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter filter belleq push filter invgain addp gain out" {
		t.Errorf("with three bands, the player got: %v", got)
	}
	if count, _ := m.Unit().ExpandedUnits(); count != 14 {
		t.Errorf("the instrument counts as %d units, not 14", count)
	}
	if units, gain, has := eq.Units(); units != 7 || !has || gain > -5.8 || gain < -7.2 {
		t.Errorf("the eq has %d units, a gain unit: %v, of %.2f dB", units, has, gain)
	}
	// what is saved is rounded
	if b, _ := eq.Band(1); b != (sointu.EQBand{Type: sointu.EQBell, Frequency: 1000, Gain: 3.14, Q: 1.23}) {
		t.Errorf("the bell is saved as %+v", b)
	}
	// the values of the units, and what a band costs
	eq.SetSelected(1)
	if got, want := eq.Info(), "990 Hz · 3.12 dB · Q 1.23 · 1 unit"; got != want {
		t.Errorf("the bell is %q, not %q", got, want)
	}
	eq.SetSelected(2)
	if got := eq.Info(); !strings.HasSuffix(got, "· 4 units + gain") || !strings.Contains(got, "-6.0") {
		t.Errorf("the shelf is %q", got)
	}
	// changes are steps of the undo history; those of a gesture are one
	eq.SetSelected(1)
	before, _ := eq.Band(1)
	_, v0, _ := eq.Compiled()
	eq.BeginGesture()
	for i := range 5 {
		b := before
		b.Frequency = 1100 + 100*float64(i)
		b.Gain = float64(i)
		eq.Set(1, b)
	}
	eq.EndGesture()
	if b, _ := eq.Band(1); b.Frequency != 1500 || b.Gain != 4 {
		t.Errorf("after the gesture: %+v", b)
	}
	if _, v, _ := eq.Compiled(); v == v0 {
		t.Error("the eq was not compiled again")
	}
	eq.Step(1, 12, 2, 6, false)
	if b, _ := eq.Band(1); b.Frequency != 3000 || b.Gain != 5 || b.Q != 2.46 {
		t.Errorf("an octave up, 1 dB up, the Q doubled: %+v", b)
	}
	m.History().Undo().Do()
	if b, _ := eq.Band(1); b.Frequency != 1500 || b.Gain != 4 {
		t.Errorf("undoing the step: %+v", b)
	}
	m.History().Undo().Do()
	if b, _ := eq.Band(1); b != before {
		t.Errorf("undoing the gesture: %+v, not %+v", b, before)
	}
	if units := playerPatch(t, broker)[0].Units; units[7].Type != "belleq" || units[7].Parameters["frequency"] != 34 {
		t.Errorf("after the undo, the player has: %+v", units[7])
	}
	m.History().Redo().Do()
	if b, _ := eq.Band(1); b.Frequency != 1500 {
		t.Errorf("redoing the gesture: %+v", b)
	}
	m.History().Undo().Do()
	// the type: a band with the Q of its type gets that of the new one
	eq.SetSelected(0)
	if !eq.Type().SetValue(slices.Index(sointu.EQBandTypes, sointu.EQLowCut24)) {
		t.Fatal("the type was not set")
	}
	if b, _ := eq.Band(0); b.Type != sointu.EQLowCut24 || b.Q != 0.707 {
		t.Errorf("a low cut made one of 24 dB: %+v", b)
	}
	if got := eq.Type().String(); got != "Low cut 24 dB" {
		t.Errorf("the type is shown as %q", got)
	}
	// off: no units
	eq.On().SetValue(false)
	if b, _ := eq.Band(0); !b.Disabled || eq.On().Value() || eq.Info() != "off: no units" {
		t.Errorf("switched off: %+v, %q", b, eq.Info())
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter belleq push filter invgain addp gain out" {
		t.Errorf("with the low cut off, the player got: %v", got)
	}
	// the values as numbers
	eq.SetSelected(1)
	for _, test := range []struct {
		value   String
		text    string
		ok      bool
		reads   string
		checked func(b sointu.EQBand) bool
	}{
		{eq.Frequency(), "1.2k", true, "1200", func(b sointu.EQBand) bool { return b.Frequency == 1200 }},
		{eq.Frequency(), "440 Hz", true, "440", func(b sointu.EQBand) bool { return b.Frequency == 440 }},
		{eq.Frequency(), "abc", false, "440", func(b sointu.EQBand) bool { return b.Frequency == 440 }},
		{eq.Frequency(), "-5", false, "440", func(b sointu.EQBand) bool { return b.Frequency == 440 }},
		{eq.Frequency(), "99999", true, "22000", func(b sointu.EQBand) bool { return b.Frequency == 22000 }},
		{eq.Gain(), "-4,5 dB", true, "-4.5", func(b sointu.EQBand) bool { return b.Gain == -4.5 }},
		{eq.Gain(), "0", true, "0", func(b sointu.EQBand) bool { return b.Gain == 0 }},
		{eq.Gain(), "100", true, "24", func(b sointu.EQBand) bool { return b.Gain == 24 }},
		{eq.Q(), "0.71", true, "0.71", func(b sointu.EQBand) bool { return b.Q == 0.71 }},
		{eq.Q(), "0", false, "0.71", func(b sointu.EQBand) bool { return b.Q == 0.71 }},
	} {
		ok := test.value.SetValue(test.text)
		b, _ := eq.Band(1)
		if ok != test.ok || test.value.Value() != test.reads || !test.checked(b) {
			t.Errorf("setting %q: %v, reads %q, the band is %+v", test.text, ok, test.value.Value(), b)
		}
	}
	eq.SetSelected(0)
	if eq.Gain().Value() != "" || eq.Gain().SetValue("3") {
		t.Error("a low cut has a gain")
	}
	// deleting
	eq.Delete(0)
	if b, _ := eq.Band(0); eq.NumBands() != 2 || b.Type != sointu.EQBell {
		t.Errorf("after deleting the low cut: %d bands, the first %+v", eq.NumBands(), b)
	}
	// only so many bands
	for range 2 * EQMaxBands {
		eq.AddBand().Do()
	}
	if eq.NumBands() != EQMaxBands || eq.AddBand().Enabled() {
		t.Errorf("%d bands", eq.NumBands())
	}
	// another unit selected: nothing to edit
	m.Unit().List().SetSelected(0)
	if eq.Active() || eq.NumBands() != 0 || eq.Add(sointu.EQBand{Frequency: 100}) != -1 || eq.Info() != "" {
		t.Error("an oscillator is edited as an eq unit")
	}
}

// TestEQSaved checks that the bands are saved and loaded with the song, the
// recovery file, the instrument file (which presets are) and the clipboard,
// and that a song without eq units is written as before.
func TestEQSaved(t *testing.T) {
	m, _ := newEQTestModel(t)
	var plain bytes.Buffer
	m.Song().Write(nopWriteCloser{&plain})
	if strings.Contains(plain.String(), "bands") {
		t.Errorf("an eq without bands is written with bands:\n%s", plain.String())
	}
	bands := []sointu.EQBand{
		{Type: sointu.EQLowCut24, Frequency: 35, Q: 0.707},
		{Type: sointu.EQBell, Frequency: 250, Gain: -4.5, Q: 2},
		{Type: sointu.EQHighShelf, Frequency: 3000, Gain: 3, Q: 1, Disabled: true},
	}
	for _, b := range bands {
		m.EQ().Add(b)
	}
	check := func(name string, m *Model, instr, unit int) {
		t.Helper()
		if instr >= len(m.d.Song.Patch) || unit >= len(m.d.Song.Patch[instr].Units) {
			t.Fatalf("%s: no unit %d of instrument %d", name, unit, instr)
		}
		if u := m.d.Song.Patch[instr].Units[unit]; u.Type != "eq" || !slices.Equal(u.Bands, bands) {
			t.Errorf("%s: the unit is %+v", name, u)
		}
	}
	check("the song", m, 0, 6)
	// the song
	var file bytes.Buffer
	m.Song().Write(nopWriteCloser{&file})
	if !strings.Contains(file.String(), "- {type: bell, frequency: 250, gain: -4.5, q: 2}") {
		t.Errorf("the song is written as:\n%s", file.String())
	}
	other, broker := newModuleTestModel(t)
	other.Song().Read(io.NopCloser(bytes.NewReader(file.Bytes())))
	check("the song read", other, 0, 6)
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter filter belleq filter belleq out" {
		t.Errorf("of the song read, the player got: %v", got)
	}
	// the recovery file
	recovery := m.History().MarshalRecovery()
	other, _ = newModuleTestModel(t)
	other.History().UnmarshalRecovery(recovery)
	check("the recovery file", other, 0, 6)
	// the instrument file
	file.Reset()
	if !m.Instrument().Write(nopWriteCloser{&file}) {
		t.Fatal("writing the instrument failed")
	}
	other, _ = newModuleTestModel(t)
	if !other.Instrument().Read(io.NopCloser(bytes.NewReader(file.Bytes()))) {
		t.Fatal("reading the instrument failed")
	}
	check("the instrument file", other, 0, 6)
	// the clipboard
	m.Unit().List().SetSelected(6)
	m.Unit().List().SetSelected2(6)
	clip, ok := m.Unit().List().CopyElements()
	if !ok {
		t.Fatal("copying the unit failed")
	}
	other, _ = newModuleTestModel(t)
	other.Unit().List().SetSelected(0)
	other.Unit().List().SetSelected2(0)
	if !other.Unit().List().PasteElements(clip) {
		t.Fatal("pasting the unit failed")
	}
	check("the clipboard", other, 0, 0)
	// in a module: the units of the eq are units of every module unit
	m.Unit().List().SetSelected(5)
	m.Unit().List().SetSelected2(6)
	m.Unit().MakeModule().Do()
	if len(m.d.Song.Modules) != 1 || unitTypes(m.d.Song.Modules[0].Units) != "filter eq" || !slices.Equal(m.d.Song.Modules[0].Units[1].Bands, bands) {
		t.Fatalf("the module is %+v", m.d.Song.Modules)
	}
	if count, _ := m.Unit().ExpandedUnits(); count != 11 {
		t.Errorf("the instrument counts as %d units, not 11", count)
	}
}

// TestEQRows checks that each eq unit of the unit editor has its own bands
// and selected band, by its row, and that it folds and unfolds like a
// module unit, which is saved and undone without adding rows.
func TestEQRows(t *testing.T) {
	m, _ := newEQTestModel(t)
	first := m.EQ().Row()
	m.Unit().Add(false).Do()
	m.Unit().SetType("eq")
	second := m.EQ().Row()
	if second != first+1 || !m.EQAt(first).Active() || !m.EQAt(second).Active() || m.EQAt(first-1).Active() {
		t.Fatalf("the eq units are on rows %d and %d", first, second)
	}
	a, b := m.EQAt(first), m.EQAt(second)
	a.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 100, Gain: 3})
	b.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 200, Gain: 3})
	b.Add(sointu.EQBand{Type: sointu.EQBell, Frequency: 400, Gain: 3})
	b.SetSelected(0)
	a.SetSelected(0)
	b.SetSelected(1)
	if a.NumBands() != 1 || b.NumBands() != 2 || a.Selected() != 0 || b.Selected() != 1 {
		t.Fatalf("bands %d and %d, selected %d and %d", a.NumBands(), b.NumBands(), a.Selected(), b.Selected())
	}
	if m.EQ().Row() != second || m.EQ().Selected() != 1 {
		t.Errorf("the selected eq unit is on row %d with band %d selected", m.EQ().Row(), m.EQ().Selected())
	}
	// a drag of the first while the second is selected is one step
	n := len(m.undoStack)
	a.BeginGesture()
	a.Step(0, 1, 0, 0, false)
	a.Step(0, 1, 0, 0, false)
	a.EndGesture()
	if len(m.undoStack) != n+1 {
		t.Errorf("a gesture on the first eq unit is %d steps", len(m.undoStack)-n)
	}
	if band, _ := b.Band(1); band.Frequency != 400 {
		t.Errorf("the gesture on the first eq unit changed the second: %+v", band)
	}
	// unfolded, like a module unit
	rows := m.Unit().List().Count()
	unfold := m.Unit().Unfold()
	if !unfold.Enabled() || unfold.Value() || m.Unit().EQExpanded(second) {
		t.Fatalf("Unfold of a folded eq unit: enabled %v, value %v", unfold.Enabled(), unfold.Value())
	}
	unfold.SetValue(true)
	if !unfold.Value() || !m.Unit().EQExpanded(second) || m.Unit().EQExpanded(first) || m.Unit().List().Count() != rows {
		t.Errorf("unfolded: %v, rows %d, was %d", m.Unit().EQExpanded(second), m.Unit().List().Count(), rows)
	}
	if item := m.Unit().Item(second); !item.EQ || !item.Unfolded || item.Module {
		t.Errorf("the item of the unfolded eq unit: %+v", item)
	}
	m.Unit().ToggleUnfold(first).Do()
	if !m.Unit().EQExpanded(first) || !m.Unit().EQExpanded(second) {
		t.Error("the two eq units are not unfolded at once")
	}
	var buf bytes.Buffer
	m.Song().Write(nopWriteCloser{&buf})
	if c := strings.Count(buf.String(), "unfolded: true"); c != 2 {
		t.Errorf("the song saves %d unfolded units", c)
	}
	// folding and unfolding in a row is one step, as for module units
	m.History().Undo().Do()
	if m.Unit().EQExpanded(first) || m.Unit().EQExpanded(second) {
		t.Error("undoing the unfolding left the eq units unfolded")
	}
	unfold.Toggle()
	if !m.Unit().EQExpanded(second) || m.Unit().EQExpanded(first) {
		t.Error("toggling did not unfold the selected eq unit")
	}
}
