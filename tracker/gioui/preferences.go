package gioui

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"gioui.org/unit"
	"github.com/vsariola/sointu/tracker"
)

type (
	Preferences struct {
		Window WindowPreferences
		Rack   RackPreferences
		MCP    MCPPreferences
	}

	// MCPPreferences are the settings of the MCP server, through which a
	// language model reads and changes the patch: see
	// tracker/mcp.
	MCPPreferences struct {
		// Enabled lets the MCP server reach the tracker and every plugin
		// instance
		Enabled bool `yaml:",omitempty"`
	}

	RackPreferences struct {
		// BufferPreviews shows what the buffers of units hold after their
		// parameters
		BufferPreviews bool
	}

	WindowPreferences struct {
		Width     int
		Height    int
		Maximized bool `yaml:",omitempty"`
		// AlwaysOnTop keeps the window above other windows, e.g. the plugin
		// host's
		AlwaysOnTop bool `yaml:",omitempty"`
	}
)

//go:embed preferences.yml
var defaultPreferences []byte

// ReadCustomConfig modifies the target argument, i.e. needs a pointer. Just
// fails silently if the file cannot be found/read, but will warn about
// malformed files.
func ReadCustomConfig(filename string, target any) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(configDir, "sointu", filename)
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if err := yaml.Unmarshal(bytes, target); err != nil {
		return fmt.Errorf("ReadCustomConfig %v: %w", filename, err)
	}
	return nil
}

// ReadConfig first unmarshals the defaultConfig which should be the embedded
// default config, and then tries to read the custom config with
// ReadCustomConfig. It panics right away if the embedded defaultConfig could
// not be parsed as yaml as this should never happen except during development.
// The returned error should be treated as a warning: this function will always
// return at least the default config, and the warning will just tell if there
// was a problem parsing the custom config.
func ReadConfig(defaultConfig []byte, path string, target any) (warn error) {
	dec := yaml.NewDecoder(bytes.NewReader(defaultConfig))
	dec.KnownFields(true)
	if err := dec.Decode(target); err != nil {
		panic(fmt.Errorf("ReadConfig %v failed to unmarshal the embedded default config: %w", path, err))
	}
	return ReadCustomConfig(path, target)
}

// WriteCustomConfig sets one value in the custom config file, at the path of
// mapping keys, e.g. "window", "alwaysontop". The rest of the file, including
// comments, is kept.
func WriteCustomConfig(filename string, value any, keys ...string) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "sointu", filename)
	var doc yaml.Node
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("WriteCustomConfig %v: %w", filename, err)
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	node := doc.Content[0]
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("WriteCustomConfig %v: not a mapping", filename)
	}
	var valueNode yaml.Node
	if err := valueNode.Encode(value); err != nil {
		return err
	}
	for i, key := range keys {
		var child *yaml.Node
		for j := 0; j+1 < len(node.Content); j += 2 {
			if node.Content[j].Value == key {
				child = node.Content[j+1]
				break
			}
		}
		last := i == len(keys)-1
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, child)
		}
		if last {
			*child = valueNode
		} else if child.Kind != yaml.MappingNode {
			*child = yaml.Node{Kind: yaml.MappingNode}
		}
		node = child
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

func (p Preferences) WindowSize() (unit.Dp, unit.Dp) {
	return unit.Dp(p.Window.Width), unit.Dp(p.Window.Height)
}

// AlwaysOnTop toggles Preferences.Window.AlwaysOnTop and saves it in the
// custom preferences.
func (t *Tracker) AlwaysOnTop() tracker.Bool { return tracker.MakeBool((*alwaysOnTop)(t)) }

type alwaysOnTop Tracker

func (t *alwaysOnTop) Value() bool { return t.preferences.Window.AlwaysOnTop }
func (t *alwaysOnTop) SetValue(val bool) {
	t.preferences.Window.AlwaysOnTop = val
	if err := WriteCustomConfig("preferences.yml", val, "window", "alwaysontop"); err != nil {
		(*Tracker)(t).Alerts().Add(fmt.Sprintf("Could not save preferences: %v", err), tracker.Error)
	}
}

// RemoteControl is what lets a language model change the song, e.g. an
// MCP server: see tracker/mcp.
type RemoteControl interface {
	Enabled() bool
	SetEnabled(on bool) error
}

// SetRemoteControl gives the tracker the remote control, and turns it on if
// the preferences say so. Call it before Main.
func (t *Tracker) SetRemoteControl(rc RemoteControl) {
	t.remoteControl = rc
	if rc != nil && t.preferences.MCP.Enabled {
		if err := rc.SetEnabled(true); err != nil {
			t.Alerts().Add(fmt.Sprintf("Could not enable MCP: %v", err), tracker.Error)
		}
	}
}

// EnableMCP turns the remote control on and off, and saves the choice in
// the custom preferences, for every tracker and plugin instance.
func (t *Tracker) EnableMCP() tracker.Bool { return tracker.MakeBool((*enableMCP)(t)) }

type enableMCP Tracker

func (t *enableMCP) Enabled() bool { return t.remoteControl != nil }
func (t *enableMCP) Value() bool   { return t.remoteControl != nil && t.remoteControl.Enabled() }
func (t *enableMCP) SetValue(val bool) {
	if t.remoteControl == nil {
		return
	}
	if err := t.remoteControl.SetEnabled(val); err != nil {
		(*Tracker)(t).Alerts().Add(fmt.Sprintf("Could not enable MCP: %v", err), tracker.Error)
		return
	}
	t.preferences.MCP.Enabled = val
	if err := WriteCustomConfig("preferences.yml", val, "mcp", "enabled"); err != nil {
		(*Tracker)(t).Alerts().Add(fmt.Sprintf("Could not save preferences: %v", err), tracker.Error)
	}
	if val {
		(*Tracker)(t).Alerts().Add("MCP enabled: an MCP client can now read and change the song, through the sointu-mcp command", tracker.Info)
	}
}
