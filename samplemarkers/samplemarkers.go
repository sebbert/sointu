// Package samplemarkers reads the markers that audio files carry besides
// their audio, which ffmpeg drops: the cue points of WAV files (cue chunk,
// named in LIST adtl labl), the markers of AIFF files (MARK chunk), and the
// length in beats of ACID loops (acid chunk). Sample editors, ReCycle and
// sample packs mark the slices of loops with them.
package samplemarkers

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
)

type (
	// Info is what a file says about its audio besides the audio.
	Info struct {
		Rate    float64 // frames per second of the file
		Markers []Marker
		// Beats is the length of an ACID loop in beats, 0 if the file has
		// no acid chunk or is a one shot.
		Beats int
	}

	// Marker is a point in the audio: frames from its start, at Info.Rate.
	Marker struct {
		Frame int
		Name  string
	}
)

// Read reads the markers of a WAV or AIFF file; ok is false for other files
// and files that are not well formed.
func Read(data []byte) (info Info, ok bool) {
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return readWav(data[12:])
	case len(data) >= 12 && string(data[:4]) == "FORM" && (string(data[8:12]) == "AIFF" || string(data[8:12]) == "AIFC"):
		return readAiff(data[12:])
	}
	return Info{}, false
}

// Slices returns the markers as slice markers of a buffer at 44100 Hz:
// sorted, without duplicates, above 0.
func (info Info) Slices() []int {
	if info.Rate <= 0 {
		return nil
	}
	var ret []int
	for _, m := range info.Markers {
		if f := int(math.Round(float64(m.Frame) * 44100 / info.Rate)); f > 0 {
			ret = append(ret, f)
		}
	}
	sort.Ints(ret)
	j := 0
	for i, f := range ret {
		if i == 0 || f != ret[j-1] {
			ret[j] = f
			j++
		}
	}
	if j == 0 {
		return nil
	}
	return ret[:j]
}

// chunks calls f with the chunks of a RIFF or IFF file, after the form
// type: their IDs and contents. Chunks are padded to an even length.
func chunks(data []byte, order binary.ByteOrder, f func(id string, body []byte)) bool {
	for len(data) >= 8 {
		id, size := string(data[:4]), int64(order.Uint32(data[4:8]))
		data = data[8:]
		if size > int64(len(data)) {
			if id == "data" || id == "SSND" { // a truncated or streamed audio chunk
				f(id, data)
				return true
			}
			return false
		}
		f(id, data[:size])
		data = data[min(size+size%2, int64(len(data))):]
	}
	return true
}

func readWav(data []byte) (Info, bool) {
	var info Info
	le := binary.LittleEndian
	type cue struct {
		id    uint32
		frame int
	}
	var cues []cue
	names := map[uint32]string{}
	ok := chunks(data, le, func(id string, b []byte) {
		switch id {
		case "fmt ":
			if len(b) >= 8 {
				info.Rate = float64(le.Uint32(b[4:8]))
			}
		case "cue ":
			if len(b) < 4 {
				return
			}
			n := int(le.Uint32(b))
			for i := 0; i < n && 4+24*(i+1) <= len(b); i++ {
				e := b[4+24*i:]
				// dwSampleOffset, the frame in the data chunk
				cues = append(cues, cue{le.Uint32(e), int(le.Uint32(e[20:24]))})
			}
		case "LIST":
			if len(b) >= 4 && string(b[:4]) == "adtl" {
				chunks(b[4:], le, func(id string, b []byte) {
					if (id == "labl" || id == "note") && len(b) >= 4 {
						if _, ok := names[le.Uint32(b)]; !ok || id == "labl" {
							names[le.Uint32(b)] = string(bytes.TrimRight(b[4:], "\x00"))
						}
					}
				})
			}
		case "acid":
			// flags, root note, 2 bytes, a float, the number of beats
			if len(b) >= 16 && le.Uint32(b)&1 == 0 { // not a one shot
				info.Beats = int(le.Uint32(b[12:16]))
			}
		}
	})
	for _, c := range cues {
		info.Markers = append(info.Markers, Marker{Frame: c.frame, Name: names[c.id]})
	}
	return info, ok && info.Rate > 0
}

func readAiff(data []byte) (Info, bool) {
	var info Info
	be := binary.BigEndian
	ok := chunks(data, be, func(id string, b []byte) {
		switch id {
		case "COMM":
			if len(b) >= 18 {
				info.Rate = extended(b[8:18])
			}
		case "MARK":
			if len(b) < 2 {
				return
			}
			n, b := int(be.Uint16(b)), b[2:]
			for i := 0; i < n && len(b) >= 7; i++ {
				// ID, position in frames, and a Pascal string padded to an
				// even length with its count
				frame, l := int(be.Uint32(b[2:6])), int(b[6])
				if 7+l > len(b) {
					break
				}
				info.Markers = append(info.Markers, Marker{Frame: frame, Name: string(b[7 : 7+l])})
				b = b[min(7+l+(l+1)%2, len(b)):]
			}
		}
	})
	return info, ok && info.Rate > 0
}

// extended returns the 80-bit IEEE 754 extended precision number of AIFF's
// sample rate.
func extended(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b)&0x7fff) - 16383
	mant := binary.BigEndian.Uint64(b[2:10])
	v := math.Ldexp(float64(mant), exp-63)
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}
