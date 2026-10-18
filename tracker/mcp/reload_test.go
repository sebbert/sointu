package mcp

import (
	"strings"
	"testing"
)

// TestReloadPlugin checks that reload_plugin asks the dev loader to reload,
// and fails where nothing can.
func TestReloadPlugin(t *testing.T) {
	host, info := startTestHost(t)
	if _, err := callTool(t, info, "reload_plugin", struct{}{}); err == nil || !strings.Contains(err.Error(), "dev loader") {
		t.Errorf("reload_plugin without a loader: %v", err)
	}
	asked := 0
	host.SetReload(func() { asked++ })
	text, err := callTool(t, info, "reload_plugin", struct{}{})
	if err != nil || asked != 1 || !strings.Contains(text, "list_instances") {
		t.Errorf("reload_plugin: asked %d times, %v\n%s", asked, err, text)
	}
}

// TestHostIDs checks that the ID of a Host has the tag of its Go runtime: a
// new runtime in the same process counts its Hosts from 1 again.
func TestHostIDs(t *testing.T) {
	host, _ := startTestHost(t)
	if !strings.Contains(host.id, "-"+runtimeTag) || len(runtimeTag) != 4 {
		t.Errorf("the ID %q has not the tag %q of the runtime", host.id, runtimeTag)
	}
}
