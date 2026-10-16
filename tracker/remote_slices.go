package tracker

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SetSlices sets the slice markers of an audio buffer, which bufread units
// with slices play from: the given frames, count slices of equal length, or
// with count 0 and no frames, at the transients of the audio, detected with
// the sensitivity (0 to 100, 50 if 0); clear removes them.
func (r *Remote) SetSlices(buffer int, frames []int, count, sensitivity int, clear bool) (string, error) {
	m := (*Model)(r)
	return r.edit(BufferChange, func() (remoteFocus, string, error) {
		focus := remoteFocus{instr: -1, module: -1}
		index := -1
		for i, buf := range m.d.Song.Buffers {
			if buf.ID == buffer {
				index = i
			}
		}
		if index < 0 {
			return focus, "", fmt.Errorf("no buffer %d", buffer)
		}
		m.d.BufferIndex = index
		b := m.Buffer()
		if !b.CanSlice() {
			return focus, "", errors.New("only a sample or a buffer written by bufwrite units has slices")
		}
		switch {
		case clear:
			b.SetSlices(nil)
		case len(frames) > 0:
			b.SetSlices(frames)
		case count > 0:
			if count < 2 || count > 128 {
				return focus, "", errors.New("count is 2 to 128 slices")
			}
			b.SliceCount().SetValue(count)
			b.SliceEqually().Do()
		default:
			if b.validFrames() == 0 {
				return focus, "", errors.New("the buffer has no audio yet")
			}
			if sensitivity == 0 {
				sensitivity = 50
			}
			b.SliceSensitivity().SetValue(sensitivity)
			b.DetectTransients().Do()
		}
		return focus, "buffer " + strconv.Itoa(buffer) + ": " + describeSlices(b.Slices(), b.sliceLength()), nil
	})
}

// describeSlices tells how many slices markers make, and where they are.
func describeSlices(markers []int, frames int) string {
	if len(markers) == 0 {
		return "1 slice (no markers)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d slices, markers at frames", len(markers)+1)
	for i, f := range markers {
		if i == 32 {
			fmt.Fprintf(&b, " ... (%d more)", len(markers)-i)
			break
		}
		b.WriteString(" " + strconv.Itoa(f))
	}
	if frames > 0 {
		fmt.Fprintf(&b, " of %d", frames)
	}
	return b.String()
}
