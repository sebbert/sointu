//go:build plugin && darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReload builds two engines and the VST3 plugin with the dev loader
// (cmd/plugin/loader), and runs a test host (testdata/reload_host.c) on it, with a home folder of its own:
// the loader loads the first engine, then the test host installs the second
// one, and with auto reload on, the instance moves to it while the host
// plays.
func TestReload(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the engine")
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	engines := filepath.Join(home, "Library", "Application Support", "sointu", "engine")
	if err := os.MkdirAll(engines, 0755); err != nil {
		t.Fatal(err)
	}
	prefs := "reload:\n  auto: true\n"
	if err := os.WriteFile(filepath.Join(home, "Library", "Application Support", "sointu", "preferences.yml"), []byte(prefs), 0644); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"a", "b"} {
		build := exec.Command("go", "build", "-buildmode=c-shared", "-tags=plugin",
			"-ldflags", "-X github.com/vsariola/sointu/version.Version=test-"+v,
			"-o", filepath.Join(engines, "sointu-engine-test-"+v+".dylib"), "../sointu-engine")
		build.Env = append(os.Environ(), "CGO_ENABLED=1")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building engine %s: %v\n%s", v, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(engines, "current"), []byte("sointu-engine-test-a.dylib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "sointu-vst3")
	cc := exec.Command("cc", "-dynamiclib", "-I../plugin/engine", "-I../../third_party/vst3", "-framework", "Cocoa", "-framework", "CoreFoundation",
		"-o", lib, "../plugin/loader/loader.c", "vst3.c", "view_darwin.m")
	if out, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("building the plugin: %v\n%s", err, out)
	}
	host := filepath.Join(dir, "host")
	cc = exec.Command("cc", "-I../../third_party/vst3", "-framework", "CoreFoundation", "-o", host, "testdata/reload_host.c")
	if out, err := cc.CombinedOutput(); err != nil {
		t.Fatalf("building the host: %v\n%s", err, out)
	}
	run := exec.Command(host, lib, engines, "sointu-engine-test-b.dylib")
	run.Env = append(os.Environ(), "HOME="+home)
	out, err := run.CombinedOutput()
	t.Logf("%s", out)
	if err != nil || !strings.Contains(string(out), "\nOK\n") {
		t.Fatalf("host: %v", err)
	}
	for _, v := range []string{"a", "b"} {
		if !strings.Contains(string(out), "loaded "+filepath.Join(engines, "sointu-engine-test-"+v+".dylib")) {
			t.Errorf("engine %s was not loaded", v)
		}
	}
}
