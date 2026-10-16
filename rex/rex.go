// Package rex loads REX loops (.rex, .rx2, .rcy, made with ReCycle) with
// the REX Shared Library of Reason Studios, which the tracker uses to import
// them: the slices are laid out where they are in the loop, and become a
// sample and its slice markers. The library is closed and licensed; builds
// without the tag rex do not have it, and Decode then fails with
// ErrUnavailable. The song and the players never need it.
package rex

import (
	"errors"
	"math"
	"path/filepath"
	"strings"

	"github.com/vsariola/sointu"
)

type (
	// Loop is a REX loop as the library renders it: its tempo, its length
	// and its slices, at 44100 Hz.
	Loop struct {
		Channels  int
		Tempo     float64 // beats per minute that the loop was made at
		PPQLength int     // the length of the loop, in PPQ
		Slices    []Slice
	}

	// Slice is a slice of a loop: where it starts, in PPQ from the start of
	// the loop, and its audio, interleaved.
	Slice struct {
		PPQ   int
		Audio []float32
	}
)

// PPQ is the resolution of positions in REX loops: steps per quarter note.
const PPQ = 15360

// ErrUnavailable is the error of Decode when the build or the computer does
// not have the REX Shared Library.
var ErrUnavailable = errors.New("REX files need the REX Shared Library of Reason Studios, which this build does not have")

// Extensions are those of REX files.
var Extensions = []string{".rex", ".rx2", ".rcy"}

// IsRex reports whether a file name is that of a REX file.
func IsRex(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range Extensions {
		if ext == e {
			return true
		}
	}
	return false
}

// frameOf returns the frame at 44100 Hz of a position in PPQ at the tempo.
func frameOf(ppq int, tempo float64) int {
	return int(math.Round(float64(ppq) * 44100 * 60 / (PPQ * tempo)))
}

// Render lays out the slices of the loop where they are in it, mixing where
// they overlap, and returns the audio, as long as the loop or the end of its
// last slice, and the slice markers: the starts of the slices after the
// first frame.
func (l Loop) Render() (sointu.BufferAudio, []int) {
	ch := max(l.Channels, 1)
	if l.Tempo <= 0 {
		l.Tempo = 120
	}
	frames := frameOf(l.PPQLength, l.Tempo)
	for _, s := range l.Slices {
		frames = max(frames, frameOf(s.PPQ, l.Tempo)+len(s.Audio)/ch)
	}
	audio := sointu.BufferAudio{Channels: ch, Data: make([]float32, frames*ch)}
	var markers []int
	for _, s := range l.Slices {
		at := frameOf(s.PPQ, l.Tempo)
		for i, v := range s.Audio {
			audio.Data[at*ch+i] += v
		}
		markers = append(markers, at)
	}
	return audio, sointu.CleanSlices(markers, frames)
}
