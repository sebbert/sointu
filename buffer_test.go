package sointu_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

func TestBlobYAMLRoundTrip(t *testing.T) {
	data := make([]byte, 1001)
	rand.New(rand.NewSource(1)).Read(data)
	copy(data[100:], make([]byte, 64)) // runs of zeros are encoded as 'z'
	for _, blob := range []sointu.Blob{nil, {}, {1}, {0, 0, 0, 0}, data} {
		in := sointu.AudioSample{FileName: "a.wav", Data: blob}
		out, err := yaml.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		var got sointu.AudioSample
		if err := yaml.Unmarshal(out, &got); err != nil {
			t.Fatalf("Unmarshal failed: %v\n%s", err, out)
		}
		if !bytes.Equal(got.Data, blob) {
			t.Errorf("round trip of %d bytes changed the data:\n%s", len(blob), out)
		}
	}
}

func TestBlobYAMLIsCompact(t *testing.T) {
	data := make([]byte, 100000)
	rand.New(rand.NewSource(1)).Read(data)
	song := sointu.Song{Buffers: sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: data}}}}
	out, err := yaml.Marshal(song)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if !strings.Contains(string(out), "!ascii85 |") {
		t.Errorf("expected a tagged literal block:\n%s", out[:200])
	}
	// ascii85 is 5/4 of the data; allow 3% more for line breaks, indentation
	// and the rest of the song
	if max := len(data) * 5 / 4 * 103 / 100; len(out) > max {
		t.Errorf("encoded %d bytes into %d bytes of YAML, expected at most %d", len(data), len(out), max)
	}
}

func TestBlobYAMLAcceptsBase64(t *testing.T) {
	data := []byte("hello, buffer")
	for _, tag := range []string{"", "!!binary "} {
		in := "data: " + tag + base64.StdEncoding.EncodeToString(data) + "\n"
		var got sointu.AudioSample
		if err := yaml.Unmarshal([]byte(in), &got); err != nil {
			t.Fatalf("Unmarshal of %q failed: %v", in, err)
		}
		if !bytes.Equal(got.Data, data) {
			t.Errorf("Unmarshal of %q: got %q", in, got.Data)
		}
	}
}

func TestBlobJSONIsBase64(t *testing.T) {
	data := []byte{0, 1, 2, 255}
	out, err := json.Marshal(sointu.AudioSample{Data: data})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if !strings.Contains(string(out), base64.StdEncoding.EncodeToString(data)) {
		t.Errorf("expected base64 in %s", out)
	}
	var got sointu.AudioSample
	if err := json.Unmarshal(out, &got); err != nil || !bytes.Equal(got.Data, data) {
		t.Errorf("JSON round trip: got %v, %v", got.Data, err)
	}
}

func TestSongCopySharesBufferData(t *testing.T) {
	song := sointu.Song{Buffers: sointu.Buffers{{ID: 1, Channels: 1, Sample: &sointu.AudioSample{Data: sointu.Blob{1, 2, 3}, Encoding: &sointu.Encoding{Args: []string{"-b:a", "32k"}}}}}}
	c := song.Copy()
	c.Buffers[0].Name = "changed"
	c.Buffers[0].Sample.FileName = "changed"
	c.Buffers[0].Sample.Encoding.Args[0] = "changed"
	if song.Buffers[0].Name != "" || song.Buffers[0].Sample.FileName != "" || song.Buffers[0].Sample.Encoding.Args[0] != "-b:a" {
		t.Errorf("changing the copy changed the original: %+v", song.Buffers[0])
	}
	if &c.Buffers[0].Sample.Data[0] != &song.Buffers[0].Sample.Data[0] {
		t.Errorf("copy should share the sample data")
	}
}
