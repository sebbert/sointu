package tracker

import (
	"errors"
	"fmt"
	"math"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

// Sample edits change the sample of the selected buffer. Crop, Trim silence
// and the trims to a part of the length only change its trim (see
// sointu.AudioSample.Trim): the stored file stays whole. Delete and the fades
// change the audio: the stored file is decoded at 44100 Hz, edited, and
// stored again as 24-bit FLAC, which is lossless, whatever the file was
// before. The slice markers move with the audio they mark. Each edit is an
// undoable change.

type (
	sampleEditKind int

	// sampleEdit is an edit of the audio of a sample. start and end are
	// frames for crop and delete, threshold the level of trim silence,
	// length the frames of a fade and curve its exponent, and part the
	// division of the whole file that a trim to a part keeps (1 keeps all).
	sampleEdit struct {
		kind       sampleEditKind
		start, end int
		threshold  float32
		length     int
		curve      float64
		part       int
	}
)

const (
	editCrop sampleEditKind = iota
	editDelete
	editTrimSilence
	editFadeIn
	editFadeOut
	editTrimPart
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
func (m *BufferModel) Crop() Action { return MakeAction(sampleEditAction{m: m, kind: editCrop}) }

// DeleteRange returns an Action to delete the selected frames of the sample.
func (m *BufferModel) DeleteRange() Action {
	return MakeAction(sampleEditAction{m: m, kind: editDelete})
}

// TrimSilence returns an Action to remove the frames at the ends of the
// sample that are below TrimThreshold.
func (m *BufferModel) TrimSilence() Action {
	return MakeAction(sampleEditAction{m: m, kind: editTrimSilence})
}

// FadeIn returns an Action to fade in the start of the sample, over
// FadeLength with FadeCurve.
func (m *BufferModel) FadeIn() Action { return MakeAction(sampleEditAction{m: m, kind: editFadeIn}) }

// FadeOut returns an Action to fade out the end of the sample.
func (m *BufferModel) FadeOut() Action { return MakeAction(sampleEditAction{m: m, kind: editFadeOut}) }

// TrimPart returns an Action to trim the sample to a part of the whole
// file: from the start of its trim, the length of the file divided by part;
// part 1 keeps all of the file. The file stays whole.
func (m *BufferModel) TrimPart(part int) Action {
	return MakeAction(sampleEditAction{m: m, kind: editTrimPart, part: part})
}

// IsTrimmed reports whether the selected sample is trimmed.
func (m *BufferModel) IsTrimmed() bool {
	s := m.selectedSample()
	return s != nil && s.Trim() != [2]int{}
}

type sampleEditAction struct {
	m    *BufferModel
	kind sampleEditKind
	part int
}

// IsEditing reports whether a sample edit is running.
func (m *BufferModel) IsEditing() bool { return m.tools().editing }

func (a sampleEditAction) Enabled() bool {
	if a.m.selectedSample() == nil || a.m.tools().editing {
		return false
	}
	if a.kind == editTrimPart && a.part <= 1 {
		return a.m.IsTrimmed()
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
		length: t.fadeLength * 44100 / 1000, curve: math.Pow(4, float64(t.fadeCurve)/100), part: a.part}
	e.start, e.end, _ = m.Selection()
	id, sample, channels := buf.ID, *buf.Sample, buf.Channels
	t.editing = true
	go func() {
		r, err := editSample(sample, channels, e)
		TrySend(m.broker.ToModel, MsgToModel{Data: func() {
			m.tools().editing = false
			if err != nil {
				(*Model)(m).Alerts().Add(fmt.Sprintf("Editing the sample: %v", err), Error)
				return
			}
			m.applyEditedSample(id, sample, r)
		}})
	}()
}

// editResult is what an edit makes of a sample: its stored file (nil if
// it stays) and trim, and where the edit moves a frame of the audio played
// before it, from the start of the trim, and the frames played after.
type editResult struct {
	stored []byte
	trim   [2]int
	move   func(int) (int, bool)
	frames int
}

// isTrim reports whether an edit only changes the trim of the sample.
func (k sampleEditKind) isTrim() bool {
	return k == editCrop || k == editTrimSilence || k == editTrimPart
}

// editSample decodes a stored sample and edits it, within its trim: the
// trims change only the trim, the others the audio, which is stored again.
func editSample(sample sointu.AudioSample, channels int, e sampleEdit) (editResult, error) {
	f, err := ffmpeg.Find("")
	if err != nil {
		return editResult{}, err
	}
	a, err := f.Decode(sample.Data, channels)
	if err != nil {
		return editResult{}, err
	}
	n := a.Frames()
	s0, e0 := trimRange(sample.Trim(), n)
	played := ffmpeg.TrimAudio(a, [2]int{s0, e0})
	if e.kind.isTrim() {
		var s, t int // the frames of played to keep
		switch e.kind {
		case editTrimPart:
			s, t = 0, min(int(int64(n)/int64(max(e.part, 1))), n-s0)
			if e.part <= 1 {
				s0, s, t = 0, 0, n // all of the file
			}
		default:
			_, move, err := applySampleEdit(played, e)
			if err != nil {
				return editResult{}, err
			}
			s, t = keptRange(move, played.Frames())
		}
		if s >= t {
			return editResult{}, errors.New("nothing to keep")
		}
		trim := [2]int{s0 + s, s0 + t}
		if trim[0] == 0 && trim[1] >= n {
			trim = [2]int{}
		} else if trim[1] >= n {
			trim[1] = 0
		}
		shift := s0 + s - (sample.TrimStart) // where the played audio starts now, from where it started
		return editResult{trim: trim, frames: t - s, move: func(f int) (int, bool) {
			g := f - shift
			return g, g >= 0 && g < t-s
		}}, nil
	}
	// the edit of the played audio, put back between the rest of the file
	out, move, err := applySampleEdit(played, e)
	if err != nil {
		return editResult{}, err
	}
	whole := sointu.BufferAudio{Channels: a.Channels}
	whole.Data = append(append(append(whole.Data, a.Data[:s0*a.Channels]...), out.Data...), a.Data[e0*a.Channels:]...)
	stored, err := f.Store(whole)
	trim := sample.Trim()
	if trim[1] > 0 {
		trim[1] = s0 + out.Frames()
	}
	return editResult{stored: stored, trim: trim, move: move, frames: out.Frames()}, err
}

// trimRange returns the frames that a trim keeps of n: from start up to end.
func trimRange(trim [2]int, n int) (start, end int) {
	start, end = min(max(trim[0], 0), n), n
	if trim[1] > 0 {
		end = min(trim[1], n)
	}
	return start, max(start, end)
}

// keptRange returns the frames of n that an edit keeps, given where it
// moves them, for edits that keep one range.
func keptRange(move func(int) (int, bool), n int) (start, end int) {
	start, end = -1, -1
	for f := range n {
		if _, ok := move(f); ok {
			if start < 0 {
				start = f
			}
			end = f + 1
		}
	}
	return max(start, 0), max(end, 0)
}

// applyEditedSample puts an edited sample in the buffer with the ID, unless
// its sample has changed since the edit started, and moves its markers.
func (m *BufferModel) applyEditedSample(id int, old sointu.AudioSample, r editResult) {
	for i := range m.d.Song.Buffers {
		buf := &m.d.Song.Buffers[i]
		if buf.ID != id {
			continue
		}
		if buf.Sample == nil || !sointu.Blob(buf.Sample.Data).Equal(old.Data) || buf.Sample.Trim() != old.Trim() {
			(*Model)(m).Alerts().Add("The sample changed while it was edited; the edit is dropped", Warning)
			return
		}
		defer m.change("EditSample")()
		s := *buf.Sample
		if r.stored != nil {
			s.Data = r.stored
		}
		s.TrimStart, s.TrimEnd = r.trim[0], r.trim[1]
		buf.Sample = &s
		buf.Slices = moveSlices(buf.Slices, r.move, r.frames)
		if t := m.tools(); t.selectionOf == id {
			t.selection = [2]int{}
		}
		return
	}
}
