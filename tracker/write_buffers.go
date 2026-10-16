package tracker

import (
	"fmt"

	"github.com/vsariola/sointu"
)

// fixWriteBuffers gives the bufwrite and bufread units their buffers, after
// every change of the patch, as fixSpectrumBuffers does for the spectral
// units: a bufwrite unit without a buffer gets one of its own, which the song
// keeps for as long as a unit refers to it (fixConvolutionBuffers deletes
// those that none does). The buffer is as long as the length parameter of
// the units that write it, and stereo if one of them is. A bufread unit
// without a buffer plays that of the nearest bufwrite unit before it among
// its units, or else after it, as in a delay with feedback, where the tap
// comes first.
//
// The buffers of impulse responses, which a convolution unit reads, are as
// long as the response: see fixConvolutionBuffers.
func (m *Model) fixWriteBuffers() {
	next, changed := 1, false
	for _, b := range m.d.Song.Buffers {
		next = max(next, b.ID+1)
	}
	responses := map[int]bool{}
	for units := range m.d.Song.UnitLists() {
		for id := range responseBuffers(units) {
			responses[id] = true
		}
		for _, u := range units {
			if u.Type != "bufwrite" || u.Parameters["buffer"] != 0 {
				continue
			}
			m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: next, Name: fmt.Sprintf("Buffer %d", next), Channels: 1, Frames: sointu.BufwriteDefaultLength, Auto: true})
			u.Parameters["buffer"] = next
			next++
			changed = true
		}
		for i, u := range units {
			if u.Type != "bufread" || u.Parameters["buffer"] != 0 {
				continue
			}
			if id := nearestWriteBuffer(units, i); id != 0 {
				u.Parameters["buffer"] = id
			}
		}
	}
	// the buffers that the tracker made follow the units that write them
	type shape struct{ frames, channels int }
	shapes := map[int]shape{}
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if id := u.Parameters["buffer"]; u.Type == "bufwrite" && id != 0 {
				s := shapes[id]
				shapes[id] = shape{max(s.frames, min(max(u.Parameters["length"], 1), sointu.BufwriteMaxLength)), max(s.channels, 1+u.Parameters["stereo"]&1)}
			}
		}
	}
	for i := range m.d.Song.Buffers {
		b := &m.d.Song.Buffers[i]
		s, written := shapes[b.ID]
		if !written || !b.Auto || !b.Writable() || b.Spectrum || b.Bus || responses[b.ID] {
			continue
		}
		if b.Frames != s.frames || b.Channels != s.channels {
			b.Frames, b.Channels = s.frames, s.channels
			changed = true
		}
	}
	if changed {
		m.syncBuffers() // the audio that the synth writes to
	}
}

// nearestWriteBuffer returns the buffer of the bufwrite unit nearest before
// unit i among the units, or else nearest after it, or 0.
func nearestWriteBuffer(units []sointu.Unit, i int) int {
	for j := i - 1; j >= 0; j-- {
		if id := units[j].Parameters["buffer"]; units[j].Type == "bufwrite" && id != 0 {
			return id
		}
	}
	for j := i + 1; j < len(units); j++ {
		if id := units[j].Parameters["buffer"]; units[j].Type == "bufwrite" && id != 0 {
			return id
		}
	}
	return 0
}

// ownBuffer tells if the buffer of a bufwrite unit is one that the tracker
// made for it, whose length the unit sets.
func (m *Model) ownBuffer(u *sointu.Unit) bool {
	b, ok := m.d.Song.Buffers.Find(u.Parameters["buffer"])
	return ok && b.Auto && b.Writable()
}
