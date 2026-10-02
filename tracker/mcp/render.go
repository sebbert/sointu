package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
)

// maxRenderSeconds is the longest render of render_note.
const maxRenderSeconds = 30

// renderNote is the tool render_note: the model gives a copy of the song,
// and the render and the measuring happen here, on the goroutine of the
// connection, with a synth of their own.
func renderNote(h *Host, raw json.RawMessage) (string, error) {
	a, err := decode[renderNoteArgs](raw)
	if err != nil {
		return "", err
	}
	if a.HoldMs == 0 {
		a.HoldMs = 500
	}
	if a.TailMs == 0 {
		a.TailMs = 1000
	}
	if len(a.Notes) == 0 {
		a.Notes = []int{60}
	}
	if a.HoldMs < 1 || a.TailMs < 0 || a.HoldMs+a.TailMs > maxRenderSeconds*1000 {
		return "", fmt.Errorf("hold_ms and tail_ms are positive and at most %d s together", maxRenderSeconds)
	}
	dry := true
	switch a.Output {
	case "", "dry":
	case "master":
		dry = false
	default:
		return "", fmt.Errorf("output is dry or master, not %q", a.Output)
	}
	for _, n := range a.Notes {
		if n < 2 || n > 255 {
			return "", errors.New("a note is 2 to 255: 60 is C-3 in the tracker")
		}
	}
	src, err := onModel(h, func(r *tracker.Remote) (tracker.RemoteRender, error) {
		var whatIf *tracker.RemoteWhatIf
		if len(a.Edits) > 0 {
			whatIf = &tracker.RemoteWhatIf{Edits: a.Edits}
		}
		return r.RenderSource(a.Instrument, dry, whatIf)
	})
	if err != nil {
		return "", err
	}
	if len(a.Notes) > src.NumVoices {
		return "", fmt.Errorf("instrument %d %q has %d voices: at most that many notes at once", src.Instrument, src.Name, src.NumVoices)
	}
	hold := a.HoldMs * int(sampleRate) / 1000
	buf, err := Render(src, a.Notes, hold, a.TailMs*int(sampleRate)/1000)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	output := "dry"
	if !dry {
		output = "master"
	}
	fmt.Fprintf(&b, "instrument %d %q, notes %v, held %d ms, output %s", src.Instrument, src.Name, a.Notes, a.HoldMs, output)
	if len(a.Edits) > 0 {
		fmt.Fprintf(&b, ", with %d edits made to the copy only", len(a.Edits))
	}
	b.WriteString("\n")
	for _, note := range src.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	b.WriteString(Measure(buf, hold).String())
	if a.Wav {
		path, err := writeWav(buf)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\nwav: %s", path)
	}
	return b.String(), nil
}

// Render plays notes of the instrument of src for hold frames, releases
// them and goes on for tail frames.
func Render(src tracker.RemoteRender, notes []int, hold, tail int) (sointu.AudioBuffer, error) {
	if err := src.Song.CheckUnitTypes(); err != nil {
		return nil, err
	}
	synth, err := src.Synther.Synth(src.Song.Patch, src.Song.BPM)
	if err != nil {
		return nil, fmt.Errorf("the patch cannot be played: %w", err)
	}
	defer synth.Close()
	buffers := maps.Clone(src.Buffers)
	if buffers == nil {
		buffers = map[int]sointu.BufferAudio{}
	}
	for _, b := range src.Song.Buffers {
		if b.Writable() {
			buffers[b.ID] = b.NewAudio() // rendering starts with nothing written
		}
	}
	if s, ok := synth.(sointu.BufferSetter); ok {
		s.SetBuffers(buffers)
	}
	buf := make(sointu.AudioBuffer, hold+tail)
	fill := func(part sointu.AudioBuffer) error {
		for tries := 0; len(part) > 0; tries++ {
			n, _, err := synth.Render(part, math.MaxInt32)
			if err != nil {
				return fmt.Errorf("the render failed: %w", err)
			}
			if part = part[n:]; tries > 1000 {
				return errors.New("the render does not advance")
			}
		}
		return nil
	}
	for i, note := range notes {
		synth.Trigger(src.FirstVoice+i, byte(note))
	}
	if err := fill(buf[:hold]); err != nil {
		return nil, err
	}
	for i := range notes {
		synth.Release(src.FirstVoice + i)
	}
	if err := fill(buf[hold:]); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeWav writes a render to a file in the temporary directory, and
// removes the renders there that are older than a day.
func writeWav(buf sointu.AudioBuffer) (string, error) {
	dir := filepath.Join(os.TempDir(), "sointu-mcp-renders")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	data, err := buf.Wav(false)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "render-*.wav")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}
