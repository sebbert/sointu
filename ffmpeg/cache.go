package ffmpeg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/vsariola/sointu"
)

type (
	// Cache encodes and decodes audio samples with ffmpeg, remembering the
	// results in memory and optionally on disk, so that the same sample with
	// the same encoding is only processed once. It is safe for concurrent use.
	Cache struct {
		FFmpeg *FFmpeg
		dir    string // "" = no disk cache

		mu    sync.Mutex
		mem   map[Key]Result
		order []Key // oldest first
	}

	// Key identifies an audio sample processed with an encoding and a number
	// of channels.
	Key [sha256.Size]byte

	// Result is an audio sample encoded for the compiled player, and decoded
	// again for the synth.
	Result struct {
		Encoded []byte
		Audio   sointu.BufferAudio
	}
)

// maxMemEntries limits how many results are kept in memory.
const maxMemEntries = 32

// NewCache returns a cache using f. If dir is not empty, results are also
// stored as files in that directory.
func NewCache(f *FFmpeg, dir string) *Cache {
	return &Cache{FFmpeg: f, dir: dir, mem: map[Key]Result{}}
}

// DefaultCacheDir returns the directory for the disk cache, inside the user
// cache directory.
func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sointu", "buffers"), nil
}

// KeyOf returns the key of sample data processed with the given encoding and
// number of channels.
func KeyOf(data []byte, enc sointu.Encoding, channels int) Key {
	h := sha256.New()
	fmt.Fprintf(h, "sointu buffer v1\x00%d\x00%s\x00%d\x00", channels, enc.Format, len(enc.Args))
	for _, a := range enc.Args {
		fmt.Fprintf(h, "%d\x00%s", len(a), a)
	}
	h.Write(data)
	var k Key
	h.Sum(k[:0])
	return k
}

func (k Key) String() string { return hex.EncodeToString(k[:]) }

// Get returns the result for sample data processed with the given encoding
// and number of channels, processing it if it is not cached yet.
func (c *Cache) Get(data []byte, enc sointu.Encoding, channels int) (Result, error) {
	key := KeyOf(data, enc, channels)
	if r, ok := c.lookup(key); ok {
		return r, nil
	}
	if r, ok := c.load(key, channels); ok {
		c.remember(key, r)
		return r, nil
	}
	encoded, err := c.FFmpeg.Encode(data, enc, channels)
	if err != nil {
		return Result{}, fmt.Errorf("encoding: %w", err)
	}
	audio, err := c.FFmpeg.Decode(encoded, channels)
	if err != nil {
		return Result{}, fmt.Errorf("decoding: %w", err)
	}
	r := Result{Encoded: encoded, Audio: audio}
	c.remember(key, r)
	c.store(key, r)
	return r, nil
}

func (c *Cache) lookup(key Key) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.mem[key]
	return r, ok
}

func (c *Cache) remember(key Key, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.mem[key]; ok {
		return
	}
	c.mem[key] = r
	c.order = append(c.order, key)
	for len(c.order) > maxMemEntries {
		delete(c.mem, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *Cache) paths(key Key) (encoded, audio string) {
	base := filepath.Join(c.dir, key.String())
	return base + ".enc", base + ".f32"
}

// load reads a result from the disk cache. Failures just mean a cache miss.
func (c *Cache) load(key Key, channels int) (Result, bool) {
	if c.dir == "" {
		return Result{}, false
	}
	encPath, audioPath := c.paths(key)
	enc, err := os.ReadFile(encPath)
	if err != nil {
		return Result{}, false
	}
	raw, err := os.ReadFile(audioPath)
	if err != nil {
		return Result{}, false
	}
	return Result{Encoded: enc, Audio: sointu.BufferAudio{Channels: channels, Data: bytesToFloats(raw)}}, true
}

// store writes a result to the disk cache. Failures are ignored; the result
// is just computed again next time. Files are written to temporary names and
// renamed, so that a partially written file is never read.
func (c *Cache) store(key Key, r Result) {
	if c.dir == "" || os.MkdirAll(c.dir, 0o755) != nil {
		return
	}
	encPath, audioPath := c.paths(key)
	write := func(path string, data []byte) bool {
		tmp, err := os.CreateTemp(c.dir, "tmp-")
		if err != nil {
			return false
		}
		_, err = tmp.Write(data)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp.Name(), path)
		}
		if err != nil {
			os.Remove(tmp.Name())
			return false
		}
		return true
	}
	// the audio file is written last, as its presence marks the entry
	// complete
	if write(encPath, r.Encoded) {
		write(audioPath, floatsToBytes(r.Audio.Data))
	}
}

// PlayedBuffers returns the IDs of the buffers played by the enabled bufread
// units of a song, also those of its modules.
func PlayedBuffers(song *sointu.Song) map[int]bool {
	played := map[int]bool{}
	expanded, _ := song.Expand() // the units that the module units stand for
	for _, instr := range expanded.Patch {
		for _, u := range instr.Units {
			if u.Type == "bufread" && !u.Disabled {
				played[u.Parameters["buffer"]] = true
			}
		}
	}
	return played
}

// SongBuffers encodes and decodes the samples of the buffers played by the
// song, returning the results keyed by buffer ID. The function f, if not nil,
// is called with each buffer and its result.
func (c *Cache) SongBuffers(song *sointu.Song, f func(sointu.Buffer, Result)) (map[int]Result, error) {
	played := PlayedBuffers(song)
	ret := map[int]Result{}
	for _, buf := range song.Buffers {
		if !played[buf.ID] || buf.Sample == nil {
			continue
		}
		enc, err := song.SampleEncoding(buf.Sample)
		if err != nil {
			return nil, fmt.Errorf("buffer %q: %v", buf.Name, err)
		}
		r, err := c.Get(buf.Sample.Data, enc, buf.Channels)
		if err != nil {
			return nil, fmt.Errorf("buffer %q: %v", buf.Name, err)
		}
		if f != nil {
			f(buf, r)
		}
		ret[buf.ID] = r
	}
	return ret, nil
}

// NeedsFFmpeg reports whether playing or compiling the song needs ffmpeg, i.e.
// whether it plays buffers with samples.
func NeedsFFmpeg(song *sointu.Song) bool {
	played := PlayedBuffers(song)
	for _, buf := range song.Buffers {
		if played[buf.ID] && buf.Sample != nil {
			return true
		}
	}
	return false
}
