package tracker

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/rex"
	"github.com/vsariola/sointu/samplemarkers"
)

// sliceTools are the settings of the slice and sample editing tools of the
// Buffers tab, and the range selected on the waveform. They are not part of
// the song.
type sliceTools struct {
	init     bool
	count    int  // the slices that Slice equally makes
	perBeat  bool // Slice equally makes perBeatN slices per beat instead of count
	perBeatN int  // the slices per beat of Slice equally
	// tempo is what an imported file told of its tempo, by buffer ID: the
	// beats of an ACID loop, or a BPM in its name or its folder's; see
	// LoopBeats
	tempo       map[int]loopTempo
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
		*t = sliceTools{init: true, count: 8, perBeatN: 2, sensitivity: 50, threshold: -60, fadeLength: 10}
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
	frames := m.sliceLength()
	n := m.tools().count
	if m.tools().perBeat {
		n = m.perBeatSlices(m.selected().ID, frames)
	}
	m.SetSlices(equalSlices(n, frames))
}

// equalSlices returns the markers that cut frames into n slices of equal
// length.
func equalSlices(n, frames int) []int {
	n = min(n, maxSliceMarkers+1)
	markers := make([]int, 0, max(n-1, 0))
	for i := 1; i < n; i++ {
		markers = append(markers, int(int64(frames)*int64(i)/int64(n)))
	}
	return markers
}

// equalCount returns the number of slices of equal length that markers cut
// frames into, or 0 if they are not of equal length (within rounding).
func equalCount(markers []int, frames int) int {
	n := len(markers) + 1
	if len(markers) == 0 || frames <= 0 {
		return 0
	}
	for i, m := range markers {
		if d := m - int(int64(frames)*int64(i+1)/int64(n)); d < -2 || d > 2 {
			return 0
		}
	}
	return n
}

// SlicePerBeat returns whether Slice equally makes SlicesPerBeat slices per
// beat of the loop, rather than SliceCount slices.
func (m *BufferModel) SlicePerBeat() Bool { return MakeBool((*slicePerBeat)(m)) }

type slicePerBeat BufferModel

func (v *slicePerBeat) Value() bool       { return (*BufferModel)(v).tools().perBeat }
func (v *slicePerBeat) SetValue(val bool) { (*BufferModel)(v).tools().perBeat = val }

// SlicesPerBeat returns the slices per beat that Slice equally makes when
// SlicePerBeat is on.
func (m *BufferModel) SlicesPerBeat() Int { return MakeInt((*slicesPerBeat)(m)) }

type slicesPerBeat BufferModel

func (v *slicesPerBeat) Value() int { return (*BufferModel)(v).tools().perBeatN }
func (v *slicesPerBeat) SetValue(value int) bool {
	(*BufferModel)(v).tools().perBeatN = value
	return true
}
func (v *slicesPerBeat) Range() RangeInclusive { return RangeInclusive{1, 16} }

// loopTempo is what a file tells of the tempo of the loop in it.
type loopTempo struct {
	beats int     // of an ACID loop, 0 if unknown
	bpm   float64 // from its name or its folder's, 0 if unknown
}

// bpmInName matches a tempo written in a file or folder name, e.g. "140 BPM"
// or "174bpm".
var bpmInName = regexp.MustCompile(`(?i)(?:^|[^0-9.])(\d{2,3}(?:\.\d+)?)\s*[-_ ]?bpm`)

// tempoOfPath returns the BPM written in the name of a file, or else of its
// folder, or 0.
func tempoOfPath(path string) float64 {
	for _, name := range []string{filepath.Base(path), filepath.Base(filepath.Dir(path))} {
		if m := bpmInName.FindStringSubmatch(name); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 40 && v <= 300 {
				return v
			}
		}
	}
	return 0
}

// loopTempoMin is the slowest tempo that the length of a loop is taken
// to be at: loops are taken to be a power of two beats long, which a
// tempo range of an octave, loopTempoMin up to twice it, tells apart for
// any length. Slower loops read as twice as fast, with twice the beats.
const loopTempoMin = 95.0

// LoopBeats returns the length in beats of the buffer with the ID, of
// frames, and where that came from: the beats of an ACID loop; or at the BPM
// in the name of the file or its folder, if that makes close to a whole
// number of beats; or else the power of two beats whose tempo is from
// loopTempoMin up to twice it.
func (m *BufferModel) LoopBeats(id, frames int) (beats int, from string) {
	t := m.tools().tempo[id]
	if t.beats > 0 {
		return t.beats, "ACID"
	}
	seconds := float64(frames) / 44100
	if seconds <= 0 {
		return 1, "length"
	}
	if t.bpm > 0 {
		b := seconds * t.bpm / 60
		if r := math.Round(b); r >= 1 && math.Abs(b-r) < 0.05 {
			return int(r), fmt.Sprintf("%g BPM in the name", t.bpm)
		}
	}
	return lengthBeats(seconds), fmt.Sprintf("%.1f BPM from the length", 60*float64(lengthBeats(seconds))/seconds)
}

// lengthBeats returns the power of two beats that a loop of seconds is at a
// tempo from loopTempoMin up to twice it.
func lengthBeats(seconds float64) int {
	n := 1
	for n < 1<<12 && 60*float64(n)/seconds < loopTempoMin {
		n *= 2
	}
	return n
}

func (m *BufferModel) perBeatSlices(id, frames int) int {
	beats, _ := m.LoopBeats(id, frames)
	return max(beats*m.tools().perBeatN, 1)
}

// sliceKeep is how a buffer whose sample is replaced was sliced, for the
// new sample to be sliced the same way: count slices of equal length, or
// none.
type sliceKeep struct{ count int }

// keepSlicing returns how the buffer with the ID, whose sample is about to
// be replaced, is sliced.
func (m *BufferModel) keepSlicing(id int) sliceKeep {
	buf, ok := m.d.Song.Buffers.Find(id)
	if !ok || id == 0 {
		return sliceKeep{}
	}
	return sliceKeep{equalCount(buf.Slices, m.buffers.audio[id].Frames())}
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
	bpm     float64 // written in the name of the file or its folder
	frames  int     // at 44100 Hz, when asked for by importSample
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
//
// path is the file's, for a BPM in its name; with frames, the stored sample
// is decoded to count its frames.
func importSample(f *ffmpeg.FFmpeg, path string, data []byte, frames bool) (stored []byte, channels int, meta importMeta, err error) {
	if rex.IsRex(path) {
		loop, err := rex.Decode(data)
		if err != nil {
			return nil, 0, meta, err
		}
		audio, markers := loop.Render()
		stored, err = f.Store(audio)
		return stored, audio.Channels, importMeta{markers: markers, frames: audio.Frames()}, err
	}
	stored, channels, err = f.Import(data)
	if err != nil {
		return nil, 0, meta, err
	}
	info, _ := samplemarkers.Read(data)
	meta = importMeta{markers: info.Slices(), beats: info.Beats, bpm: tempoOfPath(path)}
	if frames && len(meta.markers) == 0 {
		ch := min(max(channels, 1), 2)
		if audio, err := f.Decode(stored, ch); err == nil {
			meta.frames = audio.Frames()
		}
	}
	return stored, channels, meta, nil
}

// setImportedSlices gives the selected buffer, a sample just imported, the
// slice markers of its file, if it has any. An ACID loop without markers
// gets none, but Slice equally is set to its beats, so that one click
// slices it per beat. Call it within the change of the import.
//
// A sample that replaced one sliced equally (keep) is sliced equally too,
// by SliceCount slices or SlicesPerBeat per beat, as set, if it has no
// markers of its own.
func (m *BufferModel) setImportedSlices(meta importMeta, keep sliceKeep) {
	buf := m.selected()
	if buf == nil {
		return
	}
	t := m.tools()
	if t.tempo == nil {
		t.tempo = map[int]loopTempo{}
	}
	t.tempo[buf.ID] = loopTempo{beats: meta.beats, bpm: meta.bpm}
	if len(meta.markers) > 0 {
		buf.Slices = sointu.CleanSlices(meta.markers, 0)
		return
	}
	if keep.count > 0 && meta.frames > 0 {
		n := keep.count
		if t.perBeat {
			n = m.perBeatSlices(buf.ID, meta.frames)
			beats, from := m.LoopBeats(buf.ID, meta.frames)
			(*Model)(m).Alerts().Add(fmt.Sprintf("%d beats (%s): %d slices", beats, from, n), Info)
		}
		buf.Slices = equalSlices(n, meta.frames)
		return
	}
	if r := (*sliceCount)(m).Range(); meta.beats >= r.Min && meta.beats <= r.Max {
		m.tools().count = meta.beats
		(*Model)(m).Alerts().Add(fmt.Sprintf("An ACID loop of %d beats: Slice equally makes a slice per beat", meta.beats), Info)
	}
}
