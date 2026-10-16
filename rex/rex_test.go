package rex

import (
	"slices"
	"testing"
)

func TestRender(t *testing.T) {
	// at 120 BPM a quarter note is 22050 frames; an eighth 11025
	l := Loop{Channels: 1, Tempo: 120, PPQLength: 2 * PPQ, Slices: []Slice{
		{PPQ: 0, Audio: []float32{1, 1}},
		{PPQ: PPQ / 2, Audio: []float32{2}},
		{PPQ: PPQ, Audio: []float32{3, 3, 3}},
	}}
	audio, markers := l.Render()
	if audio.Frames() != 44100 {
		t.Fatalf("%d frames", audio.Frames())
	}
	if !slices.Equal(markers, []int{11025, 22050}) {
		t.Errorf("markers %v", markers)
	}
	if audio.Data[0] != 1 || audio.Data[11025] != 2 || audio.Data[22052] != 3 || audio.Data[2] != 0 {
		t.Errorf("slices not where they are in the loop")
	}
	if !IsRex("Break.RX2") || IsRex("break.wav") {
		t.Error("IsRex")
	}
}
