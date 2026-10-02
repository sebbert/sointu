package tracker

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

// TestUnknownUnitsInTracker checks that the tracker keeps a unit of a type
// that it does not have, as a song of a newer version may have, with its
// parameters, warns about it, plays the song without it, and saves it.
func TestUnknownUnitsInTracker(t *testing.T) {
	m, broker := newModuleTestModel(t)
	if alerts := alertTexts(m); len(alerts) != 0 {
		t.Fatalf("alerts before: %q", alerts)
	}
	var file bytes.Buffer
	m.Song().Write(nopWriteCloser{&file})
	var song sointu.Song
	if err := yaml.Unmarshal(file.Bytes(), &song); err != nil {
		t.Fatal(err)
	}
	units := song.Patch[0].Units
	future := sointu.Unit{Type: "futureunit", ID: 50, Parameters: sointu.ParamMap{"stereo": 0, "amount": 5}, Comment: "from the future"}
	song.Patch[0].Units = append(append(append([]sointu.Unit{}, units[:6]...), future), units[6:]...)
	data, err := yaml.Marshal(song)
	if err != nil {
		t.Fatal(err)
	}
	drainPlayer(broker)
	m.Song().Read(io.NopCloser(bytes.NewReader(data)))
	kept := m.d.Song.Patch[0].Units[6]
	if kept.Type != "futureunit" || kept.Parameters["amount"] != 5 || kept.Comment != "from the future" {
		t.Fatalf("after loading, the unit is %+v", kept)
	}
	alerts := strings.Join(alertTexts(m), "\n")
	if !strings.Contains(alerts, `"futureunit"`) || !strings.Contains(alerts, "lead") || !strings.Contains(alerts, "not played") {
		t.Errorf("the alerts are %q", alerts)
	}
	if strings.Contains(alerts, "invalid parameters") {
		t.Errorf("the parameters of the unknown unit were taken for invalid: %q", alerts)
	}
	if got := unitTypes(playerPatch(t, broker)[0].Units); got != "oscillator send envelope oscillator mulp filter out" {
		t.Errorf("the player got: %v", got)
	}
	// a change elsewhere leaves it, and its parameters, as they are
	m.Unit().List().SetSelected(0)
	m.Unit().Comment().SetValue("lfo")
	if kept := m.d.Song.Patch[0].Units[6]; kept.Type != "futureunit" || kept.Parameters["amount"] != 5 {
		t.Errorf("after a change, the unit is %+v", kept)
	}
	file.Reset()
	m.Song().Write(nopWriteCloser{&file})
	if !strings.Contains(file.String(), "type: futureunit") || !strings.Contains(file.String(), "amount: 5") {
		t.Errorf("the song is saved without the unit:\n%s", file.String())
	}
	// without it, no warning
	m.Unit().List().SetSelected(6)
	m.Unit().List().SetSelected2(6)
	m.Unit().Delete().Do()
	if alerts := strings.Join(alertTexts(m), "\n"); strings.Contains(alerts, "futureunit") {
		t.Errorf("after deleting the unit, the alerts are %q", alerts)
	}
}

// alertTexts returns the alerts that are shown, or on their way: a cleared
// one has no time left.
func alertTexts(m *Model) []string {
	var ret []string
	for _, a := range m.alerts {
		if a.Duration > 0 {
			ret = append(ret, a.Message)
		}
	}
	return ret
}
