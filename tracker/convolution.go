package tracker

import (
	"fmt"

	"github.com/vsariola/sointu"
)

// convolutionLengthParameter vtable: the length of the impulse response of
// a convolution unit. The hint adds the frames, and how much of the buffer
// there is from start, when that is less.

func (c *convolutionLengthParameter) Hint(p *Parameter) ParameterHint {
	frames := sointu.ConvolutionLength(c.Value(p))
	value, unit := p.up.DisplayFunc(c.Value(p))
	text := fmt.Sprintf("%s %s (%d)", value, unit, frames)
	if buf, found := p.m.d.Song.Buffers.Find(p.unit.Parameters["buffer"]); found {
		// of a sample that is not decoded yet, the length is not known
		if have := p.m.bufferFrames(buf.ID) - p.unit.Parameters["start"]; have < frames && (buf.Writable() || have > 0) {
			text += fmt.Sprintf(", the buffer has %s", formatDuration(max(have, 0)))
		}
	}
	return ParameterHint{text, true}
}

// convolutionCost estimates the time that a convolution unit takes for a
// sample of a channel in the Go synth, in nanoseconds, fitted to
// BenchmarkConvolution and TestConvolutionSpeed on an Apple M3 Pro: the head, the FFTs of each level, a partition of the
// largest blocks, and for a written buffer reading it again.
func convolutionCost(length, follow int, written bool) float64 {
	cost, tail := 50.0, 0
	for _, level := range []struct {
		from int
		ns   float64
	}{{sointu.ConvolutionHead, 40}, {512, 45}, {4096, 65}} {
		if length > level.from {
			cost += level.ns
			tail = (length - 1) / level.from
		}
	}
	if length > 4096 {
		cost += 1.1 * float64(tail)
	}
	if written {
		cost += 220 + 57*float64(follow-1)
	}
	return cost
}

// convolutionBudget is the time for a sample that the convolution units of
// a song may take together before the tracker warns: an eighth of a sample
// at 44100 Hz, in nanoseconds.
const convolutionBudget = 1e9 / 44100 / 8

// warnConvolution warns about convolution units without a buffer, and when
// the convolution units of the song together take more time than
// convolutionBudget.
func (m *Model) warnConvolution() {
	const name = "Convolution"
	total := 0.0
	for i, instr := range m.runPatch() {
		for _, u := range instr.Units {
			if u.Disabled || u.Type != "convolution" {
				continue
			}
			p := u.Parameters
			buf, found := m.d.Song.Buffers.Find(p["buffer"])
			if !found || !buf.IsAudio() {
				m.Alerts().AddNamed(name, fmt.Sprintf("Instrument %d '%s' has a convolution unit without a buffer: it is silent", i+1, instr.Name), Warning)
				return
			}
			total += float64(instr.NumVoices*(1+p["stereo"]&1)) * convolutionCost(sointu.ConvolutionLength(p["length"]), sointu.ConvolutionFollow(p["follow"]), buf.Writable())
		}
	}
	if total > convolutionBudget {
		m.Alerts().AddNamed(name, fmt.Sprintf("The convolution units take about %.0f %% of a processor core: shorten their responses, or use fewer voices", total*44100/1e7), Warning)
		return
	}
	m.Alerts().ClearNamed(name)
}

// responseBuffers returns the IDs of the buffers that a convolution unit
// among the units reads and a bufwrite unit among them writes: impulse
// responses that the units make themselves.
func responseBuffers(units []sointu.Unit) map[int]bool {
	read, ret := map[int]bool{}, map[int]bool{}
	for _, u := range units {
		if u.Type == "convolution" {
			read[u.Parameters["buffer"]] = true
		}
	}
	for _, u := range units {
		if id := u.Parameters["buffer"]; u.Type == "bufwrite" && id != 0 && read[id] {
			ret[id] = true
		}
	}
	return ret
}

// fixConvolutionBuffers keeps the buffers of impulse responses that units
// make themselves, after every change of the patch, like fixBuses: when a
// convolution unit and a bufwrite unit of an instrument or a module refer
// to a buffer that the song does not have, e.g. after loading a preset,
// the song gets it, with the ID, as long as the response of the unit and
// stereo if the bufwrite unit is. Such buffers are deleted when no unit
// refers to them anymore.
func (m *Model) fixConvolutionBuffers() {
	used, changed := map[int]bool{}, false
	for units := range m.d.Song.UnitLists() {
		for _, u := range units {
			if u.Type == "convolution" || u.Type == "bufwrite" || u.Type == "bufread" {
				used[u.Parameters["buffer"]] = true
			}
		}
		for id := range responseBuffers(units) {
			if _, ok := m.d.Song.Buffers.Find(id); ok {
				continue
			}
			buf := sointu.Buffer{ID: id, Name: fmt.Sprintf("Response %d", id), Channels: 1, Auto: true}
			for _, u := range units {
				if u.Parameters["buffer"] != id {
					continue
				}
				switch u.Type {
				case "convolution":
					// as long as the longest response the unit can get: of
					// a unit of a module, by the parameter of the module
					length := u.Parameters["length"]
					if b, bound := u.Bind["length"]; bound {
						length = max(b.Map(0), b.Map(128))
					}
					buf.Frames = max(buf.Frames, u.Parameters["start"]+sointu.ConvolutionLength(length))
				case "bufwrite":
					buf.Channels = max(buf.Channels, 1+u.Parameters["stereo"]&1)
				}
			}
			m.d.Song.Buffers = append(m.d.Song.Buffers, buf)
			changed = true
		}
	}
	bufs := m.d.Song.Buffers[:0]
	for _, b := range m.d.Song.Buffers {
		if b.Auto && b.IsAudio() && !used[b.ID] {
			changed = true
			continue
		}
		bufs = append(bufs, b)
	}
	m.d.Song.Buffers = bufs
	if changed {
		m.syncBuffers() // the audio that the synth writes to
	}
	m.d.BufferIndex = clamp(m.d.BufferIndex, 0, len(m.d.Song.Buffers)-1)
	m.warnConvolution()
}
