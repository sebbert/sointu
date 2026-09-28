package gioui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCustomConfigKeepsOtherValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(configDir, "sointu", "preferences.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\nwindow:\n  width: 1000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteCustomConfig("preferences.yml", true, "window", "alwaysontop"); err != nil {
		t.Fatal(err)
	}
	var p Preferences
	if warn := ReadConfig(defaultPreferences, "preferences.yml", &p); warn != nil {
		t.Fatal(warn)
	}
	if !p.Window.AlwaysOnTop || p.Window.Width != 1000 || p.Window.Height != 600 {
		t.Errorf("got %+v", p.Window)
	}
	b, _ := os.ReadFile(path)
	if string(b[:6]) != "# mine" {
		t.Errorf("comment lost:\n%s", b)
	}
	if err := WriteCustomConfig("preferences.yml", false, "window", "alwaysontop"); err != nil {
		t.Fatal(err)
	}
	p = Preferences{}
	ReadConfig(defaultPreferences, "preferences.yml", &p)
	if p.Window.AlwaysOnTop {
		t.Error("not turned off")
	}
}
