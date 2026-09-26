package ffmpeg_test

import (
	"bytes"
	"encoding/binary"
	"math"
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
	s := &sointu.AudioSample{Data: wav(1, 4410), Encoding: sointu.Encoding{Format: "flac", Args: []string{"-c:a", "flac"}}}
	r1, err := ffmpeg.NewCache(f, dir).Get(s, 1)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	// a new cache with a broken ffmpeg must find the result on disk
	r2, err := ffmpeg.NewCache(&ffmpeg.FFmpeg{Path: "/nonexistent"}, dir).Get(s, 1)
	if err != nil {
		t.Fatalf("Get from disk failed: %v", err)
	}
	if !bytes.Equal(r1.Encoded, r2.Encoded) || len(r1.Audio.Data) != len(r2.Audio.Data) {
		t.Errorf("results from disk differ")
	}
	// different settings are different entries
	if ffmpeg.KeyOf(s, 1) == ffmpeg.KeyOf(s, 2) {
		t.Errorf("keys should depend on the number of channels")
	}
	s2 := *s
	s2.Encoding.Args = []string{"-c:a", "flac", "-compression_level", "0"}
	if ffmpeg.KeyOf(s, 1) == ffmpeg.KeyOf(&s2, 1) {
		t.Errorf("keys should depend on the arguments")
	}
}

func TestFindWithBadPath(t *testing.T) {
	if _, err := ffmpeg.Find("/nonexistent/ffmpeg"); err == nil {
		t.Errorf("expected an error")
	}
}
