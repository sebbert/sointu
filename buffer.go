package sointu

import (
	"bytes"
	"encoding/ascii85"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type (
	// Buffer is a block of audio frames at 44100 Hz that units can read from,
	// e.g. the bufread unit. A buffer is filled from an AudioSample when the
	// synth starts; buffers without one are silent.
	Buffer struct {
		// ID is used by units to refer to this buffer, and stays the same when
		// buffers are reordered. ID 0 means no buffer.
		ID       int
		Name     string       `yaml:",omitempty"`
		Channels int          // 1 for mono, 2 for stereo
		Sample   *AudioSample `yaml:",omitempty"`
	}

	// AudioSample is an audio file that fills a buffer. The file is stored in
	// the song, and encoded and decoded again to get the audio the synth
	// plays, so that the song sounds like the compiled player.
	AudioSample struct {
		// FileName is the name of the imported file, for display only.
		FileName string `yaml:",omitempty"`
		// Data is the imported file. Uncompressed PCM files are stored as
		// lossless FLAC instead to save space.
		Data Blob
		// Preset is the name of the song's encoding preset used for this
		// sample, unless Encoding is set.
		Preset string `yaml:",omitempty"`
		// Encoding overrides the preset for this sample only.
		Encoding *Encoding `yaml:",omitempty"`
	}

	// Encoding describes how ffmpeg encodes an AudioSample for the compiled
	// player. An empty Format stores the sample as it is.
	Encoding struct {
		Format string   `yaml:",omitempty"`      // ffmpeg output format (container), e.g. ogg
		Args   []string `yaml:",flow,omitempty"` // ffmpeg output arguments, e.g. -c:a libopus -b:a 32k
	}

	// EncodingPreset is a named encoding that samples can share. Changing a
	// preset changes the encoding of all the samples using it.
	EncodingPreset struct {
		Name     string
		Encoding `yaml:",inline"`
	}

	// EncodingPresets is the list of encoding presets of a song.
	EncodingPresets []EncodingPreset

	// Blob is binary data. In YAML, it is stored as ascii85, tagged !ascii85,
	// in a literal block, which is more compact than the base64 of YAML's
	// !!binary; base64 is also accepted when reading. In JSON, it is stored as
	// base64 like any []byte, as JSON would escape many ascii85 characters.
	//
	// Blobs are treated as immutable, so copies of songs can share them.
	Blob []byte
)

const (
	blobTag       = "!ascii85"
	blobLineWidth = 1024
)

// MarshalYAML implements yaml.Marshaler.
func (b Blob) MarshalYAML() (any, error) {
	enc := make([]byte, ascii85.MaxEncodedLen(len(b)))
	enc = enc[:ascii85.Encode(enc, b)]
	var s strings.Builder
	for len(enc) > blobLineWidth {
		s.Write(enc[:blobLineWidth])
		s.WriteByte('\n')
		enc = enc[blobLineWidth:]
	}
	s.Write(enc)
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: blobTag, Style: yaml.LiteralStyle, Value: s.String()}, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *Blob) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: binary data should be a string", node.Line)
	}
	s := strings.Join(strings.Fields(node.Value), "")
	if node.Tag == blobTag {
		dec := make([]byte, 4*len(s)) // a 'z' decodes to 4 bytes
		n, _, err := ascii85.Decode(dec, []byte(s), true)
		if err != nil {
			return fmt.Errorf("line %d: invalid ascii85 data: %w", node.Line, err)
		}
		*b = dec[:n:n]
		return nil
	}
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid base64 data: %w", node.Line, err)
	}
	*b = dec
	return nil
}

// Equal reports whether a and b contain the same bytes.
func (b Blob) Equal(other Blob) bool { return bytes.Equal(b, other) }

// Copy returns a copy of the buffers. Sample data is shared, as it is treated
// as immutable.
func (b Buffers) Copy() Buffers {
	if b == nil {
		return nil
	}
	ret := make(Buffers, len(b))
	for i, buf := range b {
		ret[i] = buf
		if buf.Sample != nil {
			s := *buf.Sample
			if s.Encoding != nil {
				e := s.Encoding.Copy()
				s.Encoding = &e
			}
			ret[i].Sample = &s
		}
	}
	return ret
}

// Copy returns a copy of the encoding.
func (e Encoding) Copy() Encoding {
	e.Args = append([]string(nil), e.Args...)
	return e
}

// Equal reports whether two encodings are the same.
func (e Encoding) Equal(other Encoding) bool {
	if e.Format != other.Format || len(e.Args) != len(other.Args) {
		return false
	}
	for i := range e.Args {
		if e.Args[i] != other.Args[i] {
			return false
		}
	}
	return true
}

// Copy returns a copy of the presets.
func (p EncodingPresets) Copy() EncodingPresets {
	if p == nil {
		return nil
	}
	ret := make(EncodingPresets, len(p))
	for i, preset := range p {
		ret[i] = EncodingPreset{Name: preset.Name, Encoding: preset.Encoding.Copy()}
	}
	return ret
}

// Find returns the preset with the given name.
func (p EncodingPresets) Find(name string) (EncodingPreset, bool) {
	for _, preset := range p {
		if preset.Name == name {
			return preset, true
		}
	}
	return EncodingPreset{}, false
}

// SampleEncoding returns the encoding of a sample: its own encoding, or else
// the song's encoding preset it names.
func (s *Song) SampleEncoding(sample *AudioSample) (Encoding, error) {
	if sample.Encoding != nil {
		return *sample.Encoding, nil
	}
	if p, ok := s.EncodingPresets.Find(sample.Preset); ok {
		return p.Encoding, nil
	}
	if sample.Preset == "" {
		return Encoding{}, errors.New("the sample has no encoding preset")
	}
	return Encoding{}, fmt.Errorf("encoding preset %q not found", sample.Preset)
}

// Buffers is a list of buffers in a song.
type Buffers []Buffer

// Find returns the buffer with the given ID.
func (b Buffers) Find(id int) (Buffer, bool) {
	for _, buf := range b {
		if buf.ID == id {
			return buf, true
		}
	}
	return Buffer{}, false
}
