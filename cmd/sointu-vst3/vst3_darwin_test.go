//go:build plugin && darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHost builds the plugin and a test host of a few calls
// (testdata/host.c), and runs the host on the plugin. With VST3_VALIDATOR
// set to the validator of the VST3 SDK, the validator checks the plugin too.
func TestHost(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugin")
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	dir := t.TempDir()
	lib := filepath.Join(dir, "sointu-vst3.vst3", "Contents", "MacOS", "sointu-vst3")
	build := exec.Command("go", "build", "-buildmode=c-shared", "-tags=plugin", "-o", lib, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the plugin: %v\n%s", err, out)
	}
	host := filepath.Join(dir, "host")
	cc := exec.Command("cc", "-I../../third_party/vst3", "-framework", "CoreFoundation", "-o", host, "testdata/host.c")
	if out, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("building the host: %v\n%s", err, out)
	}
	out, err := exec.Command(host, lib).CombinedOutput()
	t.Logf("%s", out)
	if err != nil || !strings.Contains(string(out), "\nOK\n") {
		t.Fatalf("host: %v", err)
	}
	if v := os.Getenv("VST3_VALIDATOR"); v != "" {
		out, err := exec.Command(v, "-e", filepath.Join(dir, "sointu-vst3.vst3")).CombinedOutput()
		if err != nil || !strings.Contains(string(out), " 0 tests failed") {
			t.Fatalf("validator: %v\n%s", err, out)
		}
	}
}
