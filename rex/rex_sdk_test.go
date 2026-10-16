//go:build rex && cgo && darwin

package rex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDecodeSDKFiles decodes the test files of the REX SDK, in the folder
// $SOINTU_REX_SDK, with the library found as in a build.
func TestDecodeSDKFiles(t *testing.T) {
	sdk := os.Getenv("SOINTU_REX_SDK")
	if sdk == "" {
		t.Skip("set SOINTU_REX_SDK to the folder of the REX SDK")
	}
	t.Setenv("SOINTU_REX_DIR", filepath.Join(sdk, "Mac", "Deployment"))
	files := filepath.Join(sdk, "REX Test Protocol Files")
	for _, tc := range []struct {
		name     string
		channels int
	}{{"120Mono.rx2", 1}, {"120Stereo.rx2", 2}, {"120FourBeats.rx2", 0}, {"120RcyTest.rcy", 0}} {
		data, err := os.ReadFile(filepath.Join(files, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		loop, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		audio, markers := loop.Render()
		t.Logf("%s: %d channels, %.3f BPM, %d slices, %d frames, markers %v", tc.name, loop.Channels, loop.Tempo, len(loop.Slices), audio.Frames(), markers)
		if tc.channels != 0 && loop.Channels != tc.channels || len(loop.Slices) == 0 || audio.Frames() == 0 {
			t.Errorf("%s: %+v", tc.name, loop)
		}
		peak := float32(0)
		for _, v := range audio.Data {
			peak = max(peak, v, -v)
		}
		if peak < 0.01 {
			t.Errorf("%s is silent", tc.name)
		}
	}
	if _, err := Decode([]byte("not a rex file at all")); err == nil {
		t.Error("decoded garbage")
	}
}

// TestFoundNextToBinary checks, with SOINTU_REX_NEXT=1 and the library next
// to the test binary, that it is found there as a build puts it.
func TestFoundNextToBinary(t *testing.T) {
	if os.Getenv("SOINTU_REX_NEXT") != "1" {
		t.Skip("set SOINTU_REX_NEXT=1 with the library next to the test binary")
	}
	if !Available() {
		t.Errorf("not found in %v: %v", libraryDirs(), loadErr)
	}
}
