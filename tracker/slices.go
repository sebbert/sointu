package tracker

import (
	"fmt"
	"math"
	"slices"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/rex"
	"github.com/vsariola/sointu/samplemarkers"
)

// sliceTools are the settings of the slice and sample editing tools of the
// Buffers tab, and the range selected on the waveform. They are not part of
// the song.
type sliceTools struct {
	init        bool
	count       int    // the slices that Slice equally makes
	sensitivity int    // of Detect transients, 0 to 100
	selection   [2]int // the selected frames of the waveform, from the oldest valid frame; none if not start < end
	selectionOf int    // the ID of the buffer of the selection
	threshold   int    // the level of Trim silence, in dB
	fadeLength  int    // the length of the fades, in milliseconds
	fadeCurve   int    // the curve of the fades, -100 to 100
	editing     bool   // a sample edit is running
}

const (
	maxSliceMarkers = 255
	// minOnsetGap is the shortest slice that Detect transients makes, in
	// frames: 40 ms
	minOnsetGap = 1764
)

func (m *BufferModel) tools() *sliceTools {
	t := &m.buffers.sliceTools
	if !t.init {
		*t = sliceTools{init: true, count: 8, sensitivity: 50, threshold: -60, fadeLength: 10}
	}
	return t
}

// Slices returns the slice markers of the selected buffer: frames from its
// oldest valid frame.
func (m *BufferModel) Slices() []int {
	if buf := m.selected(); buf != nil {
		return buf.Slices
	}
	return nil
}

// SlicesOf returns the slice markers of the buffer with the ID.
func (m *BufferModel) SlicesOf(id int) []int {
	buf, _ := m.d.Song.Buffers.Find(id)
	return buf.Slices
}

// SliceOrigin returns the frame of the selected buffer's audio where its
// slice markers count from: its oldest valid frame.
func (m *BufferModel) SliceOrigin() int {
	audio, head, filled := m.Waveform()
	if frames := audio.Frames(); frames > 0 {
		return ((head-filled)%frames + frames) % frames
	}
	return 0
}

// CanSlice reports whether the selected buffer holds audio that can have
// slice markers.
func (m *BufferModel) CanSlice() bool {
	buf := m.selected()
	return buf != nil && buf.IsAudio() && (buf.Sample != nil || buf.Writable())
}

// sliceLength returns the frames that the slice markers of the selected
// buffer can be within: the frames of its audio, or its length if the audio
// is not there yet.
func (m *BufferModel) sliceLength() int {
	buf := m.selected()
	if buf == nil {
		return 0
	}
	if f := m.buffers.audio[buf.ID].Frames(); f > 0 {
		return f
	}
	return buf.Frames
}

// SetSlices sets the slice markers of the selected buffer, cleaned as
// sointu.CleanSlices does, as an undoable change.
func (m *BufferModel) SetSlices(markers []int) {
	buf := m.selected()
	if buf == nil || !m.CanSlice() {
		return
	}
	markers = sointu.CleanSlices(markers, m.sliceLength())
	if len(markers) > maxSliceMarkers {
		markers = markers[:maxSliceMarkers]
	}
	if slices.Equal(markers, buf.Slices) {
		return
	}
	defer m.change("Slices")()
	m.selected().Slices = markers
}

// AddSlice adds a slice marker at a frame of the selected buffer, counted
// from its oldest valid frame.
func (m *BufferModel) AddSlice(frame int) {
	m.SetSlices(append(slices.Clone(m.Slices()), frame))
}

// MoveSlice moves slice marker i of the selected buffer to a frame.
func (m *BufferModel) MoveSlice(i, frame int) {
	s := slices.Clone(m.Slices())
	if i < 0 || i >= len(s) {
		return
	}
	s[i] = frame
	m.SetSlices(s)
}

// DeleteSlice deletes slice marker i of the selected buffer.
func (m *BufferModel) DeleteSlice(i int) {
	s := m.Slices()
	if i < 0 || i >= len(s) {
		return
	}
	m.SetSlices(slices.Delete(slices.Clone(s), i, i+1))
}

// SliceCount returns the number of slices that SliceEqually makes.
func (m *BufferModel) SliceCount() Int { return MakeInt((*sliceCount)(m)) }

type sliceCount BufferModel

func (v *sliceCount) Value() int { return (*BufferModel)(v).tools().count }
func (v *sliceCount) SetValue(value int) bool {
	(*BufferModel)(v).tools().count = value
	return true
}
func (v *sliceCount) Range() RangeInclusive { return RangeInclusive{2, 128} }

// SliceEqually returns an Action to cut the selected buffer into SliceCount
// slices of equal length.
func (m *BufferModel) SliceEqually() Action { return MakeAction((*sliceEqually)(m)) }

type sliceEqually BufferModel

func (v *sliceEqually) Enabled() bool {
	return (*BufferModel)(v).CanSlice() && (*BufferModel)(v).sliceLength() > 1
}
func (v *sliceEqually) Do() {
	m := (*BufferModel)(v)
	n, frames := m.tools().count, m.sliceLength()
	markers := make([]int, 0, n-1)
	for i := 1; i < n; i++ {
		markers = append(markers, int(int64(frames)*int64(i)/int64(n)))
	}
	m.SetSlices(markers)
}

// SliceSensitivity returns the sensitivity of DetectTransients, 0 to 100.
func (m *BufferModel) SliceSensitivity() Int { return MakeInt((*sliceSensitivity)(m)) }

type sliceSensitivity BufferModel

func (v *sliceSensitivity) Value() int { return (*BufferModel)(v).tools().sensitivity }
func (v *sliceSensitivity) SetValue(value int) bool {
	(*BufferModel)(v).tools().sensitivity = value
	return true
}
func (v *sliceSensitivity) Range() RangeInclusive { return RangeInclusive{0, 100} }

// DetectTransients returns an Action to put the slice markers of the
// selected buffer at the transients of its audio, as detectOnsets finds them.
func (m *BufferModel) DetectTransients() Action { return MakeAction((*detectTransients)(m)) }

type detectTransients BufferModel

func (v *detectTransients) Enabled() bool {
	return (*BufferModel)(v).CanSlice() && (*BufferModel)(v).validFrames() > 0
}
func (v *detectTransients) Do() {
	m := (*BufferModel)(v)
	audio, _, filled := m.Waveform()
	origin, frames := m.SliceOrigin(), audio.Frames()
	mono := func(i int) float32 { // from the oldest valid frame
		f := (origin + i) % frames
		var v float32
		for c := range audio.Channels {
			v += audio.Data[f*audio.Channels+c]
		}
		return v / float32(audio.Channels)
	}
	markers := detectOnsets(mono, filled, m.tools().sensitivity)
	m.SetSlices(markers)
	(*Model)(m).Alerts().Add(fmt.Sprintf("Found %d transients", len(markers)), Info)
}

// validFrames returns the valid frames of the selected buffer's audio.
func (m *BufferModel) validFrames() int {
	_, _, filled := m.Waveform()
	return filled
}

// ClearSlices returns an Action to delete the slice markers of the selected
// buffer.
func (m *BufferModel) ClearSlices() Action { return MakeAction((*clearSlices)(m)) }

type clearSlices BufferModel

func (v *clearSlices) Enabled() bool { return len((*BufferModel)(v).Slices()) > 0 }
func (v *clearSlices) Do()           { (*BufferModel)(v).SetSlices(nil) }

// detectOnsets finds the transients of n frames of audio: where the level,
// with the highs emphasized, rises by more than a threshold over that of the
// frames just before, from 15 dB at sensitivity 0 down to 1.5 dB at 100.
// Frames 40 dB below the loudest are ignored, and transients are at least
// minOnsetGap frames apart. A marker is put a little before the first frame
// of the transient that reaches a quarter of its peak; none at frame 0, where
// the first slice starts anyway.
func detectOnsets(frame func(i int) float32, n, sensitivity int) []int {
	const hop, win, history = 128, 256, 4
	if n < win {
		return nil
	}
	windows := (n-win)/hop + 1
	energy := make([]float64, windows)
	loudest := 0.0
	for k := range energy {
		prev := float64(frame(max(k*hop-1, 0)))
		e := 0.0
		for i := k * hop; i < k*hop+win; i++ {
			v := float64(frame(i))
			d := v - prev // the highs, for hi-hats and snares
			e += v*v + 4*d*d
			prev = v
		}
		energy[k] = e / win
		loudest = max(loudest, energy[k])
	}
	if loudest == 0 {
		return nil
	}
	threshold := math.Pow(10, 15*math.Pow(0.1, float64(min(max(sensitivity, 0), 100))/100)/10) // as a ratio of energies
	floor := loudest * 1e-4                                                                    // -40 dB
	var ret []int
	last := -minOnsetGap
	for k := 1; k < windows; k++ {
		before := 0.0
		for j := max(k-history, 0); j < k; j++ {
			before += energy[j]
		}
		before /= float64(k - max(k-history, 0))
		if energy[k] < floor || energy[k] < threshold*(before+floor*1e-2) {
			continue
		}
		// the first frame that reaches a quarter of the peak of the window,
		// a little earlier to keep the attack
		peak := float32(0)
		for i := k * hop; i < k*hop+win; i++ {
			peak = max(peak, abs32(frame(i)))
		}
		at := k * hop
		for i := k * hop; i < k*hop+win; i++ {
			if abs32(frame(i)) >= peak/4 {
				at = i
				break
			}
		}
		at = max(at-16, 0)
		if at-last < minOnsetGap {
			continue
		}
		if at > 0 {
			ret = append(ret, at)
		}
		last = at
	}
	return ret
}

func abs32(v float32) float32 { return float32(math.Abs(float64(v))) }

// importMeta is what an imported file says about its slices: its markers,
// and the length in beats of an ACID loop.
type importMeta struct {
	markers []int
	beats   int
}

// FileSlices returns the slice markers that an imported audio file carries:
// the cue points of a WAV file or the markers of an AIFF file, at 44100 Hz.
func FileSlices(data []byte) []int {
	info, _ := samplemarkers.Read(data)
	return info.Slices()
}

// importSample prepares an imported file for storing in a song, as
// ffmpeg.Import does, and returns what it says about its slices. A REX file
// is rendered with the REX Shared Library, its slices where they are in the
// loop, and stored as FLAC.
func importSample(f *ffmpeg.FFmpeg, name string, data []byte) (stored []byte, channels int, meta importMeta, err error) {
	if rex.IsRex(name) {
		loop, err := rex.Decode(data)
		if err != nil {
			return nil, 0, meta, err
		}
		audio, markers := loop.Render()
		stored, err = f.Store(audio)
		return stored, audio.Channels, importMeta{markers: markers}, err
	}
	stored, channels, err = f.Import(data)
	info, _ := samplemarkers.Read(data)
	return stored, channels, importMeta{markers: info.Slices(), beats: info.Beats}, err
}

// setImportedSlices gives the selected buffer, a sample just imported, the
// slice markers of its file, if it has any. An ACID loop without markers
// gets none, but Slice equally is set to its beats, so that one click
// slices it per beat. Call it within the change of the import.
func (m *BufferModel) setImportedSlices(meta importMeta) {
	buf := m.selected()
	if buf == nil {
		return
	}
	if len(meta.markers) > 0 {
		buf.Slices = sointu.CleanSlices(meta.markers, 0)
		return
	}
	if r := (*sliceCount)(m).Range(); meta.beats >= r.Min && meta.beats <= r.Max {
		m.tools().count = meta.beats
		(*Model)(m).Alerts().Add(fmt.Sprintf("An ACID loop of %d beats: Slice equally makes a slice per beat", meta.beats), Info)
	}
}
