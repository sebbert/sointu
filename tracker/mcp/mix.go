package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
)

type (
	renderMixArgs struct {
		Notes  []mixNote                `json:"notes" jsonschema:"the notes, of any instruments, in any order: each with its instrument, note, start and length in beats"`
		BPM    float64                  `json:"bpm,omitempty" jsonschema:"the tempo of the beats of the notes; the tempo of the song if left out. Tempo-synced units (delays) follow the tempo of the song"`
		Beats  float64                  `json:"beats,omitempty" jsonschema:"how long the render is, in beats; if left out, until the end of the last note and tail_ms after it"`
		TailMs int                      `json:"tail_ms,omitempty" jsonschema:"when beats is left out: how long the render goes on after the last note ends, 500 if left out"`
		Edits  []tracker.RemoteUnitEdit `json:"edits,omitempty" jsonschema:"unit changes, as for edit_units, made only to the copy that is rendered, e.g. the limiter disabled to compare"`
		Wav    bool                     `json:"wav,omitempty" jsonschema:"also writes the render to a temporary 32-bit float stereo wav file at 44100 Hz and returns its path"`
	}
	mixNote struct {
		Instrument string  `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Note       int     `json:"note,omitempty" jsonschema:"60 is C-3 in the tracker, as MIDI input numbers it; 60 if left out"`
		Start      float64 `json:"start" jsonschema:"in beats from the start of the render"`
		Length     float64 `json:"length" jsonschema:"how long it is held, in beats"`
		Velocity   int     `json:"velocity,omitempty" jsonschema:"1 to 127; 127 if left out"`
	}
)

// mixEvent is a note on or off at a frame of a mix render.
type mixEvent struct {
	frame, note int
	instr       int // index into the sources
	on          bool
	velocity    int
	voice       int // set when the note on is played
	off         *mixEvent
}

// renderMix is the tool render_mix: notes of several instruments at their
// times, through the whole song (buses, ducking, mastering), as the host
// would play them.
func renderMix(h *Host, raw json.RawMessage) (string, error) {
	a, err := decode[renderMixArgs](raw)
	if err != nil {
		return "", err
	}
	if len(a.Notes) == 0 {
		return "", errors.New("give the notes to play")
	}
	var refs []string
	which := map[string]int{}
	for _, n := range a.Notes {
		if _, ok := which[n.Instrument]; !ok {
			which[n.Instrument] = len(refs)
			refs = append(refs, n.Instrument)
		}
	}
	srcs, err := onModel(h, func(r *tracker.Remote) ([]tracker.RemoteRender, error) {
		var whatIf *tracker.RemoteWhatIf
		if len(a.Edits) > 0 {
			whatIf = &tracker.RemoteWhatIf{Edits: a.Edits}
		}
		return r.RenderMixSource(refs, whatIf)
	})
	if err != nil {
		return "", err
	}
	// two refs can name one instrument, e.g. its index and its name: its
	// notes share its voices
	unique := map[int]int{} // instrument -> index into srcs
	for i, src := range srcs {
		if _, ok := unique[src.Instrument]; !ok {
			unique[src.Instrument] = len(unique)
		}
		which[refs[i]] = unique[src.Instrument]
	}
	srcs = slices.DeleteFunc(srcs, func(src tracker.RemoteRender) bool {
		_, ok := unique[src.Instrument]
		delete(unique, src.Instrument) // keeps the first of each
		return !ok
	})
	bpm := a.BPM
	if bpm == 0 {
		bpm = float64(srcs[0].Song.BPM)
	}
	if bpm < 1 || bpm > 999 {
		return "", errors.New("bpm is 1 to 999")
	}
	framesPerBeat := sampleRate * 60 / bpm
	frame := func(beats float64) int { return int(math.Round(beats * framesPerBeat)) }
	var events []*mixEvent
	end := 0
	for _, n := range a.Notes {
		note := n.Note
		if note == 0 {
			note = 60
		}
		if note < 2 || note > 255 {
			return "", errors.New("a note is 2 to 255: 60 is C-3 in the tracker")
		}
		if n.Velocity < 0 || n.Velocity > 127 {
			return "", errors.New("a velocity is 1 to 127")
		}
		if n.Start < 0 || n.Length <= 0 {
			return "", errors.New("a note starts at 0 beats or later and is longer than 0 beats")
		}
		off := &mixEvent{frame: frame(n.Start + n.Length), note: note, instr: which[n.Instrument]}
		on := &mixEvent{frame: frame(n.Start), note: note, instr: off.instr, on: true, velocity: n.Velocity, off: off}
		if off.frame <= on.frame {
			off.frame = on.frame + 1
		}
		events = append(events, on, off)
		end = max(end, off.frame)
	}
	length := end + max(a.TailMs, 0)*int(sampleRate)/1000
	if a.TailMs == 0 {
		length = end + int(sampleRate)/2
	}
	if a.Beats > 0 {
		length = frame(a.Beats)
	}
	if length > maxMixSeconds*int(sampleRate) {
		return "", fmt.Errorf("a mix render is at most %d s: %.0f beats at %g BPM", maxMixSeconds, maxMixSeconds*bpm/60, bpm)
	}
	// offs before ons at the same frame, so that a voice is free again
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].frame != events[j].frame {
			return events[i].frame < events[j].frame
		}
		return !events[i].on && events[j].on
	})
	buf, err := playMix(srcs, events, length)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d notes of %d instruments (", len(a.Notes), len(srcs))
	for i, src := range srcs {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d %q", src.Instrument, src.Name)
	}
	fmt.Fprintf(&b, "), %g BPM, %.2f beats, through the whole song", bpm, float64(length)/framesPerBeat)
	if len(a.Edits) > 0 {
		fmt.Fprintf(&b, ", with %d edits made to the copy only", len(a.Edits))
	}
	b.WriteString("\n")
	b.WriteString(Measure(buf, -1).String())
	b.WriteString("\n")
	b.WriteString(perBeat(buf, framesPerBeat))
	if a.Wav {
		path, err := writeWav(buf)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\nwav: %s", path)
	}
	return b.String(), nil
}

// maxMixSeconds is the longest render of render_mix.
const maxMixSeconds = 60

// playMix plays the events on one synth of the song of the sources. Each
// note takes the voice of its instrument that was released longest ago, or
// else the one that has played longest, as the synths of the tracker do.
func playMix(srcs []tracker.RemoteRender, events []*mixEvent, length int) (sointu.AudioBuffer, error) {
	song := srcs[0].Song
	if err := song.CheckUnitTypes(); err != nil {
		return nil, err
	}
	synth, err := srcs[0].Synther.Synth(song.Patch, song.BPM)
	if err != nil {
		return nil, fmt.Errorf("the patch cannot be played: %w", err)
	}
	defer synth.Close()
	buffers := maps.Clone(srcs[0].Buffers)
	if buffers == nil {
		buffers = map[int]sointu.BufferAudio{}
	}
	for _, b := range song.Buffers {
		if b.Writable() {
			buffers[b.ID] = b.NewAudio()
		}
	}
	if s, ok := synth.(sointu.BufferSetter); ok {
		s.SetBuffers(buffers)
	}
	type voice struct {
		playing *mixEvent // the note off of the note it plays
		since   int       // frame of its last note on or off
	}
	voices := make([][]voice, len(srcs))
	for i, src := range srcs {
		voices[i] = make([]voice, src.NumVoices)
		for v := range voices[i] {
			voices[i][v].since = -1 - v // the first notes take the first voices
		}
	}
	buf := make(sointu.AudioBuffer, length)
	pos := 0
	fill := func(to int) error {
		to = min(to, length)
		for tries := 0; pos < to; tries++ {
			n, _, err := synth.Render(buf[pos:to], math.MaxInt32)
			if err != nil {
				return fmt.Errorf("the render failed: %w", err)
			}
			if pos += n; tries > 1000 {
				return errors.New("the render does not advance")
			}
		}
		return nil
	}
	for _, e := range events {
		if e.frame >= length {
			break
		}
		if err := fill(e.frame); err != nil {
			return nil, err
		}
		vs := voices[e.instr]
		first := srcs[e.instr].FirstVoice
		if !e.on {
			for v := range vs {
				if vs[v].playing == e {
					synth.Release(first + v)
					vs[v] = voice{since: e.frame}
				}
			}
			continue
		}
		v := slices.IndexFunc(vs, func(x voice) bool { return x.playing == nil })
		best := -1
		for i := range vs {
			free := vs[i].playing == nil
			if v >= 0 && !free {
				continue
			}
			if best < 0 || vs[i].since < vs[best].since {
				best = i
			}
		}
		if t, ok := synth.(sointu.VelocityTriggerer); ok && e.velocity > 0 {
			t.TriggerVelocity(first+best, byte(e.note), byte(e.velocity))
		} else {
			synth.Trigger(first+best, byte(e.note))
		}
		vs[best] = voice{playing: e.off, since: e.frame}
	}
	if err := fill(length); err != nil {
		return nil, err
	}
	return buf, nil
}

// perBeat lists the peak and the RMS of each beat of a render, for seeing
// a pump, a build or a drop over time.
func perBeat(buf sointu.AudioBuffer, framesPerBeat float64) string {
	var peaks, rmss strings.Builder
	beats := 0
	for start := 0.0; int(start) < len(buf); start += framesPerBeat {
		from, to := int(start), min(int(start+framesPerBeat), len(buf))
		peak := 0.0
		for _, f := range buf[from:to] {
			peak = max(peak, math.Abs(float64(f[0])), math.Abs(float64(f[1])))
		}
		fmt.Fprintf(&peaks, " %.1f", db(peak))
		fmt.Fprintf(&rmss, " %.1f", rmsOf(buf, from, to))
		beats++
	}
	return fmt.Sprintf("per beat (%d), peak dBFS:%s\nper beat, rms dB:%s", beats, peaks.String(), rmss.String())
}
