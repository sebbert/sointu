package tracker

import (
	"errors"
	"fmt"
	"math"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

// Sample edits change the audio of the sample of the selected buffer: the
// stored file is decoded at 44100 Hz, edited, and stored again as 24-bit
// FLAC, which is lossless, whatever the file was before. The slice markers
// move with the audio they mark. Each edit is an undoable change.

type (
	sampleEditKind int

	// sampleEdit is an edit of the audio of a sample. start and end are
	// frames for crop and delete, threshold the level of trim silence,
	// length the frames of a fade and curve its exponent.
	sampleEdit struct {
		kind       sampleEditKind
		start, end int
		threshold  float32
		length     int
		curve      float64
	}
)

const (
	editCrop sampleEditKind = iota
	editDelete
	editTrimSilence
	editFadeIn
	editFadeOut
)

var errSilent = errors.New("the sample is silent")

// applySampleEdit returns the edited audio, and where the edit moves a frame
// of the audio, false for frames that it removes.
func applySampleEdit(a sointu.BufferAudio, e sampleEdit) (sointu.BufferAudio, func(int) (int, bool), error) {
	frames, ch := a.Frames(), a.Channels
	identity := func(f int) (int, bool) { return f, true }
	out := sointu.BufferAudio{Channels: ch}
	switch e.kind {
	case editTrimSilence:
		first, last := -1, -1
		for f := range frames {
			for c := range ch {
				if abs32(a.Data[f*ch+c]) > e.threshold {
					if first < 0 {
						first = f
					}
					last = f
				}
			}
		}
		if first < 0 {
			return out, nil, errSilent
		}
		e.start, e.end = first, last+1
		fallthrough
	case editCrop:
		s, t := min(max(e.start, 0), frames), min(max(e.end, 0), frames)
		if s >= t {
			return out, nil, errors.New("nothing to keep")
		}
		out.Data = append([]float32(nil), a.Data[s*ch:t*ch]...)
		return out, func(f int) (int, bool) { return f - s, f >= s && f < t }, nil
	case editDelete:
		s, t := min(max(e.start, 0), frames), min(max(e.end, 0), frames)
		if s >= t || t-s >= frames {
			return out, nil, errors.New("nothing to keep")
		}
		out.Data = append(append([]float32(nil), a.Data[:s*ch]...), a.Data[t*ch:]...)
		return out, func(f int) (int, bool) {
			switch {
			case f < s:
				return f, true
			case f >= t:
				return f - (t - s), true
			}
			return 0, false
		}, nil
	case editFadeIn, editFadeOut:
		out.Data = append([]float32(nil), a.Data...)
		l := min(e.length, frames)
		for j := range l {
			g := float32(math.Pow(float64(j)/float64(l), e.curve))
			f := j
			if e.kind == editFadeOut {
				f = frames - 1 - j
			}
			for c := range ch {
				out.Data[f*ch+c] *= g
			}
		}
		return out, identity, nil
	}
	return out, nil, fmt.Errorf("unknown edit %d", e.kind)
}

// moveSlices returns the markers moved by an edit, those it removes left out.
func moveSlices(markers []int, move func(int) (int, bool), frames int) []int {
	var ret []int
	for _, m := range markers {
		if f, ok := move(m); ok {
			ret = append(ret, f)
		}
	}
	return sointu.CleanSlices(ret, frames)
}

// Selection returns the frames selected on the waveform of the selected
// buffer, from its oldest valid frame, start < end; ok is false if there is
// no selection.
func (m *BufferModel) Selection() (start, end int, ok bool) {
	t := m.tools()
	buf := m.selected()
	if buf == nil || t.selectionOf != buf.ID || t.selection[0] >= t.selection[1] {
		return 0, 0, false
	}
	return t.selection[0], t.selection[1], true
}

// SetSelection selects frames of the waveform of the selected buffer; start
// >= end selects nothing.
func (m *BufferModel) SetSelection(start, end int) {
	t := m.tools()
	if buf := m.selected(); buf != nil {
		n := m.sliceLength()
		t.selection, t.selectionOf = [2]int{min(max(start, 0), n), min(max(end, 0), n)}, buf.ID
	}
}

// TrimThreshold returns the level, in dB, below which TrimSilence removes
// the ends of the sample.
func (m *BufferModel) TrimThreshold() Int { return MakeInt((*trimThreshold)(m)) }

type trimThreshold BufferModel

func (v *trimThreshold) Value() int { return (*BufferModel)(v).tools().threshold }
func (v *trimThreshold) SetValue(value int) bool {
	(*BufferModel)(v).tools().threshold = value
	return true
}
func (v *trimThreshold) Range() RangeInclusive { return RangeInclusive{-96, -6} }

// FadeLength returns the length of FadeIn and FadeOut, in milliseconds.
func (m *BufferModel) FadeLength() Int { return MakeInt((*fadeLength)(m)) }

type fadeLength BufferModel

func (v *fadeLength) Value() int { return (*BufferModel)(v).tools().fadeLength }
func (v *fadeLength) SetValue(value int) bool {
	(*BufferModel)(v).tools().fadeLength = value
	return true
}
func (v *fadeLength) Range() RangeInclusive { return RangeInclusive{1, 10000} }

// FadeCurve returns the curve of the fades, -100 to 100: the gain is t^k
// over the fade, t from 0 to 1, with k = 4^(curve/100): 0 is linear, above
// it the fade starts slowly, below it quickly.
func (m *BufferModel) FadeCurve() Int { return MakeInt((*fadeCurve)(m)) }

type fadeCurve BufferModel

func (v *fadeCurve) Value() int { return (*BufferModel)(v).tools().fadeCurve }
func (v *fadeCurve) SetValue(value int) bool {
	(*BufferModel)(v).tools().fadeCurve = value
	return true
}
func (v *fadeCurve) Range() RangeInclusive { return RangeInclusive{-100, 100} }

// Crop returns an Action to keep only the selected frames of the sample.
func (m *BufferModel) Crop() Action { return MakeAction(sampleEditAction{m, editCrop}) }

// DeleteRange returns an Action to delete the selected frames of the sample.
func (m *BufferModel) DeleteRange() Action { return MakeAction(sampleEditAction{m, editDelete}) }

// TrimSilence returns an Action to remove the frames at the ends of the
// sample that are below TrimThreshold.
func (m *BufferModel) TrimSilence() Action { return MakeAction(sampleEditAction{m, editTrimSilence}) }

// FadeIn returns an Action to fade in the start of the sample, over
// FadeLength with FadeCurve.
func (m *BufferModel) FadeIn() Action { return MakeAction(sampleEditAction{m, editFadeIn}) }

// FadeOut returns an Action to fade out the end of the sample.
func (m *BufferModel) FadeOut() Action { return MakeAction(sampleEditAction{m, editFadeOut}) }

type sampleEditAction struct {
	m    *BufferModel
	kind sampleEditKind
}

// IsEditing reports whether a sample edit is running.
func (m *BufferModel) IsEditing() bool { return m.tools().editing }

func (a sampleEditAction) Enabled() bool {
	if a.m.selectedSample() == nil || a.m.tools().editing {
		return false
	}
	if a.kind == editCrop || a.kind == editDelete {
		_, _, ok := a.m.Selection()
		return ok
	}
	return true
}

func (a sampleEditAction) Do() {
	m, t := a.m, a.m.tools()
	buf := m.selected()
	e := sampleEdit{kind: a.kind, threshold: float32(math.Pow(10, float64(t.threshold)/20)),
		length: t.fadeLength * 44100 / 1000, curve: math.Pow(4, float64(t.fadeCurve)/100)}
	e.start, e.end, _ = m.Selection()
	id, data, channels := buf.ID, buf.Sample.Data, buf.Channels
	t.editing = true
	go func() {
		stored, move, frames, err := editSample(data, channels, e)
		TrySend(m.broker.ToModel, MsgToModel{Data: func() {
			m.tools().editing = false
			if err != nil {
				(*Model)(m).Alerts().Add(fmt.Sprintf("Editing the sample: %v", err), Error)
				return
			}
			m.applyEditedSample(id, data, stored, move, frames)
		}})
	}()
}

// editSample decodes a stored sample, edits it and stores it again.
func editSample(data []byte, channels int, e sampleEdit) (stored []byte, move func(int) (int, bool), frames int, err error) {
	f, err := ffmpeg.Find("")
	if err != nil {
		return nil, nil, 0, err
	}
	a, err := f.Decode(data, channels)
	if err != nil {
		return nil, nil, 0, err
	}
	out, move, err := applySampleEdit(a, e)
	if err != nil {
		return nil, nil, 0, err
	}
	stored, err = f.Store(out)
	return stored, move, out.Frames(), err
}

// applyEditedSample puts an edited sample in the buffer with the ID, unless
// its sample has changed since the edit started, and moves its markers.
func (m *BufferModel) applyEditedSample(id int, old, stored []byte, move func(int) (int, bool), frames int) {
	for i := range m.d.Song.Buffers {
		buf := &m.d.Song.Buffers[i]
		if buf.ID != id {
			continue
		}
		if buf.Sample == nil || !sointu.Blob(buf.Sample.Data).Equal(old) {
			(*Model)(m).Alerts().Add("The sample changed while it was edited; the edit is dropped", Warning)
			return
		}
		defer m.change("EditSample")()
		s := *buf.Sample
		s.Data = stored
		buf.Sample = &s
		buf.Slices = moveSlices(buf.Slices, move, frames)
		if t := m.tools(); t.selectionOf == id {
			t.selection = [2]int{}
		}
		return
	}
}
