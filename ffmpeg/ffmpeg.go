// Package ffmpeg imports, encodes and decodes the audio samples of buffers
// using the ffmpeg and ffprobe command line tools.
package ffmpeg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vsariola/sointu"
)

// FFmpeg runs the ffmpeg and ffprobe executables.
type FFmpeg struct {
	Path      string // path of ffmpeg
	ProbePath string // path of ffprobe
	// Context, if not nil, kills running ffmpeg and ffprobe processes when
	// it is done.
	Context context.Context
}

// EnvVar is the environment variable that can be set to the path of ffmpeg.
const EnvVar = "SOINTU_FFMPEG"

// extraDirs are searched for ffmpeg after PATH. Applications started from the
// macOS Finder or loaded as plugins do not get the shell's PATH, so the usual
// Homebrew and MacPorts locations are searched explicitly.
var extraDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

// Find finds ffmpeg and ffprobe. path is used if not empty; otherwise the
// environment variable SOINTU_FFMPEG, PATH and common installation directories
// are searched, in this order. ffprobe is looked up in the same directory as
// ffmpeg first.
func Find(path string) (*FFmpeg, error) {
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	if path == "" {
		path = os.Getenv(EnvVar)
	}
	if path == "" {
		if p, err := exec.LookPath(exe("ffmpeg")); err == nil {
			path = p
		}
	}
	if path == "" {
		for _, dir := range extraDirs {
			if p := filepath.Join(dir, exe("ffmpeg")); isFile(p) {
				path = p
				break
			}
		}
	}
	if path == "" {
		return nil, fmt.Errorf("ffmpeg not found; install it or set %s to its path", EnvVar)
	}
	if !isFile(path) {
		return nil, fmt.Errorf("ffmpeg not found at %s", path)
	}
	probe := filepath.Join(filepath.Dir(path), exe("ffprobe"))
	if !isFile(probe) {
		p, err := exec.LookPath(exe("ffprobe"))
		if err != nil {
			return nil, fmt.Errorf("ffprobe not found next to %s or in PATH", path)
		}
		probe = p
	}
	return &FFmpeg{Path: path, ProbePath: probe}, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Version returns the first line of ffmpeg -version, e.g. "ffmpeg version 8.1
// ...".
func (f *FFmpeg) Version() (string, error) {
	out, err := f.run(f.Path, "-hide_banner", "-version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

// Stream describes the first audio stream of a file.
type Stream struct {
	Codec    string `json:"codec_name"`
	Channels int    `json:"channels"`
	Rate     string `json:"sample_rate"`
}

// Probe returns information about the first audio stream in data.
func (f *FFmpeg) Probe(data []byte) (Stream, error) {
	var stream Stream
	err := withTempDir(func(dir string) error {
		in := filepath.Join(dir, "in")
		if err := os.WriteFile(in, data, 0o600); err != nil {
			return err
		}
		out, err := f.run(f.ProbePath, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,channels,sample_rate", "-of", "json", in)
		if err != nil {
			return err
		}
		var probe struct{ Streams []Stream }
		if err := json.Unmarshal(out, &probe); err != nil {
			return fmt.Errorf("could not parse ffprobe output: %w", err)
		}
		if len(probe.Streams) == 0 {
			return errors.New("the file contains no audio")
		}
		stream = probe.Streams[0]
		return nil
	})
	return stream, err
}

// flacCodecs are the uncompressed integer PCM codecs that FLAC stores
// losslessly. 32-bit integer and floating point PCM are not among them.
var flacCodecs = map[string]bool{
	"pcm_u8": true, "pcm_s8": true,
	"pcm_s16le": true, "pcm_s16be": true,
	"pcm_s24le": true, "pcm_s24be": true,
}

// Import prepares an imported audio file for storing in a song. Uncompressed
// integer PCM (e.g. most WAV and AIFF files) is converted to FLAC, after
// checking that the conversion is lossless; anything else is returned as is.
// It also returns the number of channels in the file.
func (f *FFmpeg) Import(data []byte) (stored []byte, channels int, err error) {
	stream, err := f.Probe(data)
	if err != nil {
		return nil, 0, err
	}
	if !flacCodecs[stream.Codec] {
		return data, stream.Channels, nil
	}
	flac, err := f.convert(data, "flac", "-c:a", "flac", "-compression_level", "12")
	if err != nil {
		return nil, 0, fmt.Errorf("converting to FLAC: %w", err)
	}
	a, err := f.pcmHash(data)
	if err != nil {
		return nil, 0, err
	}
	b, err := f.pcmHash(flac)
	if err != nil {
		return nil, 0, err
	}
	if a != b || len(flac) >= len(data) {
		return data, stream.Channels, nil // keep the original if FLAC does not help
	}
	return flac, stream.Channels, nil
}

// pcmHash returns a hash of the decoded audio of data, as 32-bit integers.
func (f *FFmpeg) pcmHash(data []byte) ([32]byte, error) {
	pcm, err := f.convert(data, "s32le", "-c:a", "pcm_s32le")
	if err != nil {
		return [32]byte{}, fmt.Errorf("decoding: %w", err)
	}
	return sha256.Sum256(pcm), nil
}

// Encode encodes the first audio stream of data with the given encoding and
// number of channels. An encoding without a format returns data as is.
func (f *FFmpeg) Encode(data []byte, enc sointu.Encoding, channels int) ([]byte, error) {
	if enc.Format == "" {
		return data, nil
	}
	args := append([]string{"-ac", fmt.Sprint(channels)}, enc.Args...)
	return f.convert(data, enc.Format, args...)
}

// Decode decodes the first audio stream of data to interleaved 32-bit float
// frames at 44100 Hz with the given number of channels.
func (f *FFmpeg) Decode(data []byte, channels int) (sointu.BufferAudio, error) {
	raw, err := f.convert(data, "f32le", "-c:a", "pcm_f32le", "-ac", fmt.Sprint(channels), "-ar", "44100")
	if err != nil {
		return sointu.BufferAudio{}, err
	}
	return sointu.BufferAudio{Channels: channels, Data: bytesToFloats(raw)}, nil
}

// convert runs ffmpeg on the first audio stream of data, writing the given
// format with the given output arguments. Input and output go through
// temporary files, as some formats need seeking.
func (f *FFmpeg) convert(data []byte, format string, args ...string) ([]byte, error) {
	var ret []byte
	err := withTempDir(func(dir string) error {
		in, out := filepath.Join(dir, "in"), filepath.Join(dir, "out")
		if err := os.WriteFile(in, data, 0o600); err != nil {
			return err
		}
		cmd := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", in, "-map", "0:a:0", "-map_metadata", "-1"}
		cmd = append(cmd, args...)
		cmd = append(cmd, "-f", format, out)
		if _, err := f.run(f.Path, cmd...); err != nil {
			return err
		}
		var err error
		ret, err = os.ReadFile(out)
		return err
	})
	return ret, err
}

func (f *FFmpeg) run(name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	ctx := f.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second // don't wait for output of leftover child processes after a kill
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
		return nil, fmt.Errorf("%s: %s", filepath.Base(name), msg)
	}
	return stdout.Bytes(), nil
}

func withTempDir(f func(dir string) error) error {
	dir, err := os.MkdirTemp("", "sointu-ffmpeg-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return f(dir)
}

func bytesToFloats(b []byte) []float32 {
	ret := make([]float32, len(b)/4)
	for i := range ret {
		ret[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return ret
}

func floatsToBytes(f []float32) []byte {
	ret := make([]byte, len(f)*4)
	for i, v := range f {
		binary.LittleEndian.PutUint32(ret[i*4:], math.Float32bits(v))
	}
	return ret
}
