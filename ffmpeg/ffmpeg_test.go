package ffmpeg_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

func find(t *testing.T) *ffmpeg.FFmpeg {
	t.Helper()
	f, err := ffmpeg.Find("")
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	return f
}

// wav returns a 16-bit 44.1 kHz WAV file of a 441 Hz sine, with the given
// number of channels and frames.
func wav(channels, frames int) []byte {
	var b bytes.Buffer
	dataLen := frames * channels * 2
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + dataLen))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(channels))
	w(uint32(44100))
	w(uint32(44100 * channels * 2))
	w(uint16(channels * 2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(dataLen))
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			w(int16(math.Sin(2*math.Pi*441*float64(i)/44100) * 16000))
		}
	}
	return b.Bytes()
}

func TestImportConvertsPCMToFLAC(t *testing.T) {
	f := find(t)
	in := wav(2, 44100)
	stored, channels, err := f.Import(in)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if channels != 2 {
		t.Errorf("got %d channels, want 2", channels)
	}
	if !bytes.HasPrefix(stored, []byte("fLaC")) {
		t.Fatalf("expected FLAC, got %q...", stored[:4])
	}
	if len(stored) >= len(in) {
		t.Errorf("FLAC (%d bytes) not smaller than WAV (%d bytes)", len(stored), len(in))
	}
	// the stored FLAC decodes to exactly the original samples
	a, err := f.Decode(in, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Decode(stored, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Data) != len(b.Data) {
		t.Fatalf("decoded lengths differ: %d and %d", len(a.Data), len(b.Data))
	}
	for i := range a.Data {
		if a.Data[i] != b.Data[i] {
			t.Fatalf("sample %d differs: %v and %v", i, a.Data[i], b.Data[i])
		}
	}
}

func TestImportKeepsCompressedFiles(t *testing.T) {
	f := find(t)
	flac, _, err := f.Import(wav(1, 4410))
	if err != nil {
		t.Fatal(err)
	}
	opus, err := f.Encode(flac, sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus", "-b:a", "32k"}}, 1)
	if err != nil {
		t.Skipf("libopus not available: %v", err)
	}
	stored, channels, err := f.Import(opus)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if !bytes.Equal(stored, opus) || channels != 1 {
		t.Errorf("expected the Opus file back unchanged with 1 channel, got %d bytes and %d channels", len(stored), channels)
	}
}

func TestEncodeDecode(t *testing.T) {
	f := find(t)
	in := wav(2, 44100)
	for _, tc := range []struct {
		name     string
		enc      sointu.Encoding
		channels int
	}{
		{"passthrough", sointu.Encoding{}, 2},
		{"flac mono", sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}, 1},
		{"opus", sointu.Encoding{Format: "ogg", Args: []string{"-c:a", "libopus", "-b:a", "64k"}}, 2},
		{"mp3 22 kHz", sointu.Encoding{Format: "mp3", Args: []string{"-c:a", "libmp3lame", "-b:a", "64k", "-ar", "22050"}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := f.Encode(in, tc.enc, tc.channels)
			if err != nil {
				t.Skipf("encoder not available: %v", err)
			}
			audio, err := f.Decode(enc, tc.channels)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if audio.Channels != tc.channels {
				t.Errorf("got %d channels, want %d", audio.Channels, tc.channels)
			}
			// lossy codecs may add or trim a little at the ends
			if n := audio.Frames(); n < 44100-2048 || n > 44100+2048 {
				t.Errorf("got %d frames, want about 44100", n)
			}
		})
	}
}

func TestCache(t *testing.T) {
	f := find(t)
	dir := t.TempDir()
	data, enc := wav(1, 4410), sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}
	r1, err := ffmpeg.NewCache(f, dir).Get(data, enc, 1, [2]int{})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	// a new cache with a broken ffmpeg must find the result on disk
	r2, err := ffmpeg.NewCache(&ffmpeg.FFmpeg{Path: "/nonexistent"}, dir).Get(data, enc, 1, [2]int{})
	if err != nil {
		t.Fatalf("Get from disk failed: %v", err)
	}
	if !bytes.Equal(r1.Encoded, r2.Encoded) || len(r1.Audio.Data) != len(r2.Audio.Data) {
		t.Errorf("results from disk differ")
	}
	// different settings are different entries
	if ffmpeg.KeyOf(data, enc, 1, [2]int{}) == ffmpeg.KeyOf(data, enc, 2, [2]int{}) {
		t.Errorf("keys should depend on the number of channels")
	}
	enc2 := sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac", "-compression_level", "0"}}
	if ffmpeg.KeyOf(data, enc, 1, [2]int{}) == ffmpeg.KeyOf(data, enc2, 1, [2]int{}) {
		t.Errorf("keys should depend on the arguments")
	}
}

func TestFindWithBadPath(t *testing.T) {
	if _, err := ffmpeg.Find("/nonexistent/ffmpeg"); err == nil {
		t.Errorf("expected an error")
	}
}

func TestBuiltinPresets(t *testing.T) {
	presets, err := ffmpeg.Presets()
	if err != nil {
		t.Logf("user presets: %v", err)
	}
	names := map[string]bool{}
	for _, p := range presets {
		names[p.Name] = true
	}
	if !names[ffmpeg.DefaultPreset] || !names["Passthrough"] {
		t.Fatalf("missing default or passthrough preset in %v", names)
	}
	f := find(t)
	in := wav(1, 4410)
	for _, p := range presets {
		t.Run(p.Name, func(t *testing.T) {
			enc, err := f.Encode(in, p.Encoding, 1)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			if _, err := f.Decode(enc, 1); err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
		})
	}
}

func TestDecodeFile(t *testing.T) {
	f := find(t)
	path := filepath.Join(t.TempDir(), "a file.wav")
	if err := os.WriteFile(path, wav(1, 44100), 0o600); err != nil {
		t.Fatal(err)
	}
	audio, err := f.DecodeFile(path, 2, 0.5)
	if err != nil {
		t.Fatalf("DecodeFile failed: %v", err)
	}
	if audio.Channels != 2 || audio.Frames() != 22050 {
		t.Errorf("got %d channels and %d frames, want 2 and 22050", audio.Channels, audio.Frames())
	}
	if _, err := f.DecodeFile(filepath.Join(t.TempDir(), "missing.wav"), 2, 1); err == nil {
		t.Error("DecodeFile of a missing file did not fail")
	}
}

// A trimmed sample keeps only the frames of its trim, stored as FLAC when it
// has no encoding format, and is cached apart from the untrimmed one.
func TestCacheTrim(t *testing.T) {
	f := find(t)
	data := wav(1, 4410)
	c := ffmpeg.NewCache(f, "")
	for _, enc := range []sointu.Encoding{{}, {Format: "flac", Args: []string{"-c:a", "flac"}}} {
		for _, c2 := range []struct {
			trim   [2]int
			frames int
		}{{[2]int{1000, 3000}, 2000}, {[2]int{1000, 0}, 3410}, {[2]int{0, 441}, 441}} {
			r, err := c.Get(data, enc, 1, c2.trim)
			if err != nil {
				t.Fatalf("%v %v: %v", enc, c2.trim, err)
			}
			if n := r.Audio.Frames(); n != c2.frames {
				t.Errorf("%v %v: %d frames, want %d", enc, c2.trim, n, c2.frames)
			}
		}
	}
	if ffmpeg.KeyOf(data, sointu.Encoding{}, 1, [2]int{}) == ffmpeg.KeyOf(data, sointu.Encoding{}, 1, [2]int{0, 441}) {
		t.Error("keys should depend on the trim")
	}
	if _, err := c.Get(data, sointu.Encoding{}, 1, [2]int{5000, 0}); err == nil {
		t.Error("a trim past the end keeps nothing, but got no error")
	}
}
