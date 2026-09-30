package tracker

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

// Buffer returns a BufferModel, a view of the model used to manipulate the
// song's buffers.
func (m *Model) Buffer() *BufferModel { return (*BufferModel)(m) }

type BufferModel Model

// customPreset is the name shown for samples with their own encoding.
const customPreset = "Custom"

// AudioFileExtensions are the extensions offered when importing samples.
var AudioFileExtensions = []string{".wav", ".flac", ".aif", ".aiff", ".mp3", ".ogg", ".opus", ".m4a", ".webm"}

// loadEncodingPresets loads the default encoding presets that songs start
// with, alerting about errors in the user's presets file.
func (m *Model) loadEncodingPresets() {
	var err error
	m.defaultPresets, err = ffmpeg.Presets()
	if err != nil {
		m.Alerts().Add(fmt.Sprintf("Encoding presets: %v", err), Warning)
	}
}

// selected returns the selected buffer, or nil if there are none.
func (m *BufferModel) selected() *sointu.Buffer {
	if len(m.d.Song.Buffers) == 0 {
		return nil
	}
	i := min(max(m.d.BufferIndex, 0), len(m.d.Song.Buffers)-1)
	return &m.d.Song.Buffers[i]
}

func (m *BufferModel) change(kind string) func() {
	return (*Model)(m).change(kind, BufferChange, MajorChange)
}

// List returns a List of the buffers, for selecting a buffer.
func (m *BufferModel) List() List { return MakeList((*bufferList)(m)) }

type bufferList BufferModel

func (v *bufferList) Count() int { return len(v.d.Song.Buffers) }
func (v *bufferList) Selected() int {
	return min(max(v.d.BufferIndex, 0), len(v.d.Song.Buffers)-1)
}
func (v *bufferList) Selected2() int { return v.Selected() }
func (v *bufferList) SetSelected(i int) {
	if i != v.d.BufferIndex {
		v.buffers.customFormat = false
	}
	v.d.BufferIndex = i
}
func (v *bufferList) SetSelected2(int) {}

// Item returns the name of buffer i and a short description of it.
func (m *BufferModel) Item(i int) (name, info string) {
	if i < 0 || i >= len(m.d.Song.Buffers) {
		return "", ""
	}
	buf := m.d.Song.Buffers[i]
	name = buf.Name
	if name == "" {
		name = fmt.Sprintf("Buffer %d", buf.ID)
	}
	status := m.buffers.status[buf.ID]
	switch {
	case buf.Bus:
		info = "bus, 8 channels"
	case buf.Spectrum:
		if size, channels, ok := (*Model)(m).spectrumSize(buf.ID); ok {
			info = fmt.Sprintf("spectrum, %d", size)
			if channels == 2 {
				info += ", stereo"
			}
		} else {
			info = "spectrum, not written"
		}
	case buf.Writable():
		info = fmt.Sprintf("%s, recorded", formatDuration(buf.Frames))
	case buf.Sample == nil:
		info = "empty"
	case status.Processing:
		info = "encoding..."
	case status.Err != nil:
		info = "error"
	default:
		info = fmt.Sprintf("%s, %s", formatDuration(m.buffers.audio[buf.ID].Frames()), formatSize(status.EncodedSize))
	}
	return name, info
}

// Import imports an audio file as a new buffer, or into the selected buffer
// if replace is true. The file is converted in the background.
func (m *BufferModel) Import(r io.ReadCloser, replace bool) {
	fileName := "sample"
	if f, ok := r.(*os.File); ok {
		fileName = filepath.Base(f.Name())
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		(*Model)(m).Alerts().Add(fmt.Sprintf("Error reading %s: %v", fileName, err), Error)
		return
	}
	(*Model)(m).Alerts().Add(fmt.Sprintf("Importing %s...", fileName), Info)
	go func() {
		f, err := ffmpeg.Find("")
		var stored []byte
		var channels int
		if err == nil {
			stored, channels, err = f.Import(data)
		}
		TrySend(m.broker.ToModel, MsgToModel{Data: func() {
			if err != nil {
				(*Model)(m).Alerts().Add(fmt.Sprintf("Error importing %s: %v", fileName, err), Error)
				return
			}
			m.addImported(fileName, stored, min(max(channels, 1), 2), replace)
		}})
	}()
}

func (m *BufferModel) addImported(fileName string, data []byte, channels int, replace bool) {
	defer m.change("ImportBuffer")()
	if len(m.d.Song.EncodingPresets) == 0 {
		m.d.Song.EncodingPresets = m.defaultPresets.Copy()
	}
	sample := &sointu.AudioSample{FileName: fileName, Data: data}
	if buf := m.selected(); replace && buf != nil {
		if buf.Sample != nil { // keep the encoding
			sample.Preset, sample.Encoding = buf.Sample.Preset, buf.Sample.Encoding
		} else {
			m.setDefaultEncoding(sample)
		}
		buf.Sample = sample
		buf.Channels = channels
		buf.Frames = 0 // no longer recorded into
		return
	}
	id := 1
	for _, buf := range m.d.Song.Buffers {
		id = max(id, buf.ID+1)
	}
	m.setDefaultEncoding(sample)
	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: id, Name: name, Channels: channels, Sample: sample})
	m.d.BufferIndex = len(m.d.Song.Buffers) - 1
}

// NewEmpty returns an Action to add an empty buffer, for bufwrite units to
// record into.
func (m *BufferModel) NewEmpty() Action { return MakeAction((*newEmptyBuffer)(m)) }

type newEmptyBuffer BufferModel

func (m *newEmptyBuffer) Do() {
	defer (*BufferModel)(m).change("NewEmptyBuffer")()
	id := 1
	for _, buf := range m.d.Song.Buffers {
		id = max(id, buf.ID+1)
	}
	name := fmt.Sprintf("Recording %d", id)
	m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: id, Name: name, Channels: 1, Frames: 44100})
	m.d.BufferIndex = len(m.d.Song.Buffers) - 1
}

// IsBus reports whether the selected buffer is a bus of mc units.
func (m *BufferModel) IsBus() bool {
	buf := m.selected()
	return buf != nil && buf.Bus
}

// IsSpectrum reports whether the selected buffer is a spectrum buffer.
func (m *BufferModel) IsSpectrum() bool {
	buf := m.selected()
	return buf != nil && buf.Spectrum
}

// Spectrum returns the latest spectrum of the selected spectrum buffer, as
// SpectrumOf.
func (m *BufferModel) Spectrum() ([]float32, int) {
	if buf := m.selected(); buf != nil && buf.Spectrum {
		return m.SpectrumOf(buf.ID)
	}
	return nil, 0
}

// SpectrumOf returns the magnitudes of bins 0 to size/2 of the latest
// spectrum of a spectrum buffer, for each channel after each other, and the
// size, or 0 if there is none yet.
// While it is being called, the player keeps reporting the spectra.
func (m *BufferModel) SpectrumOf(id int) ([]float32, int) {
	if id <= 0 {
		return nil, 0
	}
	return (*Model)(m).spectrumOf(SpectrumSource{Buffer: id})
}

func (m *Model) spectrumOf(src SpectrumSource) ([]float32, int) {
	if m.spectrumAsked == nil {
		m.spectrumAsked = map[SpectrumSource]time.Time{}
	}
	m.spectrumAsked[src] = time.Now()
	if !slices.Contains(m.spectrumWatch, src) {
		m.spectrumWatch = append(m.spectrumWatch, src)
		TrySend(m.broker.ToPlayer, any(SpectrumWatchMsg(slices.Clone(m.spectrumWatch))))
	}
	s := m.spectra[src]
	return s.Magnitudes, s.Size
}

// unwatchSpectra stops the reports of the spectra not asked for in a second:
// they are no longer shown.
func (m *Model) unwatchSpectra() {
	watch := slices.DeleteFunc(slices.Clone(m.spectrumWatch), func(src SpectrumSource) bool {
		if time.Since(m.spectrumAsked[src]) <= time.Second {
			return false
		}
		delete(m.spectrumAsked, src)
		delete(m.spectra, src)
		return true
	})
	if len(watch) != len(m.spectrumWatch) {
		m.spectrumWatch = watch
		TrySend(m.broker.ToPlayer, any(SpectrumWatchMsg(slices.Clone(watch))))
	}
}

// IsWritable reports whether the selected buffer is written by bufwrite
// units instead of having a sample.
func (m *BufferModel) IsWritable() bool {
	buf := m.selected()
	return buf != nil && buf.Writable()
}

// Length returns a String for the length of the selected writable buffer, in
// seconds.
func (m *BufferModel) Length() String { return MakeString((*bufferLength)(m)) }

type bufferLength BufferModel

func (v *bufferLength) Value() string {
	if buf := (*BufferModel)(v).selected(); buf != nil && buf.Writable() {
		return strconv.FormatFloat(float64(buf.Frames)/44100, 'f', -1, 64)
	}
	return ""
}
func (v *bufferLength) SetValue(value string) bool {
	buf := (*BufferModel)(v).selected()
	seconds, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "s")), 64)
	if buf == nil || !buf.Writable() || err != nil || seconds <= 0 || seconds > 600 {
		return false
	}
	defer (*BufferModel)(v).change("BufferLength")()
	buf.Frames = max(int(math.Round(seconds*44100)), 1)
	return true
}

// Clear returns an Action to discard what has been written to the selected
// writable buffer.
func (m *BufferModel) Clear() Action { return MakeAction((*clearBuffer)(m)) }

type clearBuffer BufferModel

func (m *clearBuffer) Enabled() bool { return (*BufferModel)(m).IsWritable() }
func (m *clearBuffer) Do() {
	if buf := (*BufferModel)(m).selected(); buf != nil {
		(*Model)(m).clearBuffer(buf.ID)
	}
}

// FitToRecording returns an Action to set the length of the selected
// writable buffer to what has been written to it.
func (m *BufferModel) FitToRecording() Action { return MakeAction((*fitToRecording)(m)) }

type fitToRecording BufferModel

func (m *fitToRecording) Enabled() bool {
	buf := (*BufferModel)(m).selected()
	if buf == nil || !buf.Writable() {
		return false
	}
	filled, ok := (*Model)(m).bufferFill(buf.ID)
	return ok && filled > 0 && filled != buf.Frames
}
func (m *fitToRecording) Do() {
	buf := (*BufferModel)(m).selected()
	if filled, ok := (*Model)(m).bufferFill(buf.ID); ok && filled > 0 {
		defer (*BufferModel)(m).change("FitToRecording")()
		buf.Frames = filled
	}
}

// bufferFill returns how many frames of a writable buffer the player has
// written, if it reports it.
func (m *Model) bufferFill(id int) (int, bool) {
	f, ok := m.bufferFillOf(id)
	return f.Filled, ok
}

func (m *Model) bufferFillOf(id int) (BufferFill, bool) {
	for _, f := range m.playerStatus.BufferFills {
		if f.ID == id && id != 0 {
			return f, true
		}
	}
	return BufferFill{}, false
}

// Playheads calls yield with the frames of the selected buffer that bufread
// units are playing, and how many frames ago the note was released (0 if it
// is held).
func (m *BufferModel) Playheads(yield func(frame, released int)) {
	if buf := m.selected(); buf != nil {
		m.PlayheadsOf(buf.ID, yield)
	}
}

// PlayheadsOf is Playheads for the buffer with the given ID.
func (m *BufferModel) PlayheadsOf(id int, yield func(frame, released int)) {
	for _, p := range m.playerStatus.Playheads[:m.playerStatus.NumPlayheads] {
		if p.BufferID == id {
			yield(p.Frame, p.Released)
		}
	}
}

// Waveform returns the audio of the selected buffer, for drawing it, and
// which frames are valid: the filled frames before head, wrapping around the
// end. For samples, all frames are valid. The audio of a writable buffer is
// written by the player while it is read, so what is drawn may be torn.
func (m *BufferModel) Waveform() (audio sointu.BufferAudio, head, filled int) {
	if buf := m.selected(); buf != nil {
		return m.WaveformOf(buf.ID)
	}
	return sointu.BufferAudio{}, 0, 0
}

// WaveformOf is Waveform for the buffer with the given ID.
func (m *BufferModel) WaveformOf(id int) (audio sointu.BufferAudio, head, filled int) {
	audio = m.buffers.audio[id]
	if !audio.Writable {
		return audio, 0, audio.Frames()
	}
	f, _ := (*Model)(m).bufferFillOf(id)
	return audio, f.Head, f.Filled
}

// setDefaultEncoding makes a new sample use the default preset, or else the
// first preset of the song, or else its own encoding.
func (m *BufferModel) setDefaultEncoding(s *sointu.AudioSample) {
	if _, ok := m.d.Song.EncodingPresets.Find(ffmpeg.DefaultPreset); ok {
		s.Preset = ffmpeg.DefaultPreset
	} else if len(m.d.Song.EncodingPresets) > 0 {
		s.Preset = m.d.Song.EncodingPresets[0].Name
	} else {
		s.Encoding = &sointu.Encoding{}
	}
}

// Delete returns an Action to delete the selected buffer. Units playing it
// keep referring to its ID and fall silent.
func (m *BufferModel) Delete() Action { return MakeAction((*deleteBuffer)(m)) }

type deleteBuffer BufferModel

func (m *deleteBuffer) Enabled() bool {
	buf := (*BufferModel)(m).selected()
	// a spectrum buffer or bus in use would be created again right away
	return buf != nil && !(buf.Spectrum && (*Model)(m).spectrumBufferUsed(buf.ID)) && !(buf.Bus && (*Model)(m).busUsed(buf.ID))
}
func (m *deleteBuffer) Do() {
	defer (*BufferModel)(m).change("DeleteBuffer")()
	i := (*bufferList)(m).Selected()
	m.d.Song.Buffers = append(m.d.Song.Buffers[:i:i], m.d.Song.Buffers[i+1:]...)
	m.d.BufferIndex = min(i, len(m.d.Song.Buffers)-1)
}

// HasSelection reports whether a buffer is selected, i.e. there are buffers.
func (m *BufferModel) HasSelection() bool { return len(m.d.Song.Buffers) > 0 }

// Name returns a String for the name of the selected buffer.
func (m *BufferModel) Name() String { return MakeString((*bufferName)(m)) }

type bufferName BufferModel

func (v *bufferName) Value() string {
	if buf := (*BufferModel)(v).selected(); buf != nil {
		return buf.Name
	}
	return ""
}
func (v *bufferName) SetValue(value string) bool {
	buf := (*BufferModel)(v).selected()
	if buf == nil {
		return false
	}
	defer (*Model)(v).change("BufferName", BufferChange, MinorChange)()
	buf.Name = value
	return true
}

// Channels returns an Int for the number of channels of the selected buffer.
func (m *BufferModel) Channels() Int { return MakeInt((*bufferChannels)(m)) }

type bufferChannels BufferModel

func (v *bufferChannels) Value() int {
	if buf := (*BufferModel)(v).selected(); buf != nil {
		return buf.Channels
	}
	return 1
}
func (v *bufferChannels) SetValue(value int) bool {
	buf := (*BufferModel)(v).selected()
	if buf == nil {
		return false
	}
	defer (*BufferModel)(v).change("BufferChannels")()
	buf.Channels = value
	return true
}
func (v *bufferChannels) Range() RangeInclusive { return RangeInclusive{1, 2} }
func (v *bufferChannels) StringOf(value int) string {
	if value == 2 {
		return "stereo"
	}
	return "mono"
}

// selectedSample returns the sample of the selected buffer, or nil.
func (m *BufferModel) selectedSample() *sointu.AudioSample {
	if buf := m.selected(); buf != nil {
		return buf.Sample
	}
	return nil
}

// selectedPreset returns the song's encoding preset used by the selected
// buffer, or nil if the sample has its own encoding or there is none.
func (m *BufferModel) selectedPreset() *sointu.EncodingPreset {
	s := m.selectedSample()
	if s == nil || s.Encoding != nil {
		return nil
	}
	for i := range m.d.Song.EncodingPresets {
		if m.d.Song.EncodingPresets[i].Name == s.Preset {
			return &m.d.Song.EncodingPresets[i]
		}
	}
	return nil
}

// Preset returns an Int for choosing the encoding of the selected buffer: one
// of the song's encoding presets, or, as the last value, "Custom" for an
// encoding of its own.
func (m *BufferModel) Preset() Int { return MakeInt((*bufferPreset)(m)) }

type bufferPreset BufferModel

func (v *bufferPreset) Value() int {
	s := (*BufferModel)(v).selectedSample()
	if s == nil || s.Encoding != nil {
		return len(v.d.Song.EncodingPresets)
	}
	for i, p := range v.d.Song.EncodingPresets {
		if p.Name == s.Preset {
			return i
		}
	}
	return len(v.d.Song.EncodingPresets)
}
func (v *bufferPreset) SetValue(value int) bool {
	s := (*BufferModel)(v).selectedSample()
	if s == nil {
		return false
	}
	var enc sointu.Encoding
	if value == len(v.d.Song.EncodingPresets) {
		// start the custom encoding from the current one
		enc, _ = v.d.Song.SampleEncoding(s)
	}
	defer (*BufferModel)(v).change("BufferPreset")()
	v.buffers.customFormat = false
	c := (*BufferModel)(v).replaceSample()
	if value < len(v.d.Song.EncodingPresets) {
		c.Preset, c.Encoding = v.d.Song.EncodingPresets[value].Name, nil
	} else {
		e := enc.Copy()
		c.Encoding = &e
	}
	return true
}
func (v *bufferPreset) Range() RangeInclusive {
	return RangeInclusive{0, len(v.d.Song.EncodingPresets)}
}
func (v *bufferPreset) StringOf(value int) string {
	if value >= 0 && value < len(v.d.Song.EncodingPresets) {
		return v.d.Song.EncodingPresets[value].Name
	}
	if s := (*BufferModel)(v).selectedSample(); s != nil && s.Encoding == nil && value == v.Value() {
		return s.Preset + " (missing)"
	}
	return customPreset
}
func (v *bufferPreset) Enabled() bool { return (*BufferModel)(v).selectedSample() != nil }

// replaceSample replaces the sample of the selected buffer with a copy, to be
// modified, and returns it; samples are shared with the undo history.
func (m *BufferModel) replaceSample() *sointu.AudioSample {
	buf := m.selected()
	s := *buf.Sample
	if s.Encoding != nil {
		e := s.Encoding.Copy()
		s.Encoding = &e
	}
	buf.Sample = &s
	return &s
}

// IsCustom reports whether the selected buffer's sample has its own encoding
// instead of using a preset.
func (m *BufferModel) IsCustom() bool {
	s := m.selectedSample()
	return s != nil && s.Encoding != nil
}

// PresetUsers returns the number of samples using the encoding preset of the
// selected buffer.
func (m *BufferModel) PresetUsers() int {
	p := m.selectedPreset()
	if p == nil {
		return 0
	}
	n := 0
	for _, buf := range m.d.Song.Buffers {
		if buf.Sample != nil && buf.Sample.Encoding == nil && buf.Sample.Preset == p.Name {
			n++
		}
	}
	return n
}

// Format returns a String for the ffmpeg output format of the selected
// buffer's encoding: its own, or the preset it uses, which then changes for
// all the samples using it. An empty format stores the sample as it is.
func (m *BufferModel) Format() String { return MakeString((*bufferFormat)(m)) }

type bufferFormat BufferModel

func (v *bufferFormat) Value() string {
	if e := (*BufferModel)(v).editedEncoding(); e != nil {
		return e.Format
	}
	return ""
}
func (v *bufferFormat) SetValue(value string) bool {
	return (*BufferModel)(v).setEncoding(func(e *sointu.Encoding) { e.Format = strings.TrimSpace(value) })
}

// CommonFormats are the ffmpeg output formats offered in FormatChoice. The
// empty format keeps the sample as it is.
var CommonFormats = []struct{ Format, Label string }{
	{"", "Keep original"},
	{"ogg", "Ogg"},
	{"webm", "WebM"},
	{"flac", "FLAC"},
	{"wav", "WAV"},
	{"mp3", "MP3"},
	{"mp4", "MP4"},
}

// FormatChoice returns an Int for choosing the format of the selected
// buffer's encoding from CommonFormats, or, as the last value, "Custom" to
// type it with Format.
func (m *BufferModel) FormatChoice() Int { return MakeInt((*formatChoice)(m)) }

type formatChoice BufferModel

func (v *formatChoice) Value() int {
	e := (*BufferModel)(v).editedEncoding()
	if e == nil || v.buffers.customFormat {
		return len(CommonFormats)
	}
	for i, f := range CommonFormats {
		if f.Format == e.Format {
			return i
		}
	}
	return len(CommonFormats)
}
func (v *formatChoice) SetValue(value int) bool {
	if value >= len(CommonFormats) {
		v.buffers.customFormat = true
		return true
	}
	v.buffers.customFormat = false
	(*BufferModel)(v).setEncoding(func(e *sointu.Encoding) { e.Format = CommonFormats[value].Format })
	return true
}
func (v *formatChoice) Range() RangeInclusive { return RangeInclusive{0, len(CommonFormats)} }
func (v *formatChoice) StringOf(value int) string {
	if value >= 0 && value < len(CommonFormats) {
		return CommonFormats[value].Label
	}
	return customPreset
}
func (v *formatChoice) Enabled() bool { return (*BufferModel)(v).editedEncoding() != nil }

// IsCustomFormat reports whether the format of the selected buffer's
// encoding is typed in instead of chosen from CommonFormats.
func (m *BufferModel) IsCustomFormat() bool {
	return m.FormatChoice().Value() == len(CommonFormats)
}

// Args returns a String for the ffmpeg output arguments of the selected
// buffer's encoding, quoted like a shell command line; see Format.
func (m *BufferModel) Args() String { return MakeString((*bufferArgs)(m)) }

type bufferArgs BufferModel

func (v *bufferArgs) Value() string {
	if e := (*BufferModel)(v).editedEncoding(); e != nil {
		return JoinArgs(e.Args)
	}
	return ""
}
func (v *bufferArgs) SetValue(value string) bool {
	args, err := SplitArgs(value)
	if err != nil {
		return false
	}
	return (*BufferModel)(v).setEncoding(func(e *sointu.Encoding) { e.Args = args })
}

// editedEncoding returns the encoding that the Format and Args edit: the
// sample's own, or its preset's.
func (m *BufferModel) editedEncoding() *sointu.Encoding {
	if s := m.selectedSample(); s != nil && s.Encoding != nil {
		return s.Encoding
	}
	if p := m.selectedPreset(); p != nil {
		return &p.Encoding
	}
	return nil
}

func (m *BufferModel) setEncoding(f func(*sointu.Encoding)) bool {
	if m.editedEncoding() == nil {
		return false
	}
	defer (*Model)(m).change("BufferEncoding", BufferChange, MinorChange)()
	if m.IsCustom() {
		f(m.replaceSample().Encoding)
		return true
	}
	p := m.selectedPreset()
	p.Encoding = p.Encoding.Copy() // presets are shared with the undo history
	f(&p.Encoding)
	return true
}

// PresetName returns a String for renaming the encoding preset used by the
// selected buffer. The samples using it are updated; names must be unique and
// not empty.
func (m *BufferModel) PresetName() String { return MakeString((*presetName)(m)) }

type presetName BufferModel

func (v *presetName) Value() string {
	if p := (*BufferModel)(v).selectedPreset(); p != nil {
		return p.Name
	}
	return ""
}
func (v *presetName) SetValue(value string) bool {
	p := (*BufferModel)(v).selectedPreset()
	value = strings.TrimSpace(value)
	if p == nil || value == "" {
		return false
	}
	if _, exists := v.d.Song.EncodingPresets.Find(value); exists {
		return false
	}
	defer (*BufferModel)(v).change("RenamePreset")()
	old := p.Name
	p.Name = value
	for i := range v.d.Song.Buffers {
		if s := v.d.Song.Buffers[i].Sample; s != nil && s.Preset == old {
			c := *s
			c.Preset = value
			v.d.Song.Buffers[i].Sample = &c
		}
	}
	return true
}

// NewPreset returns an Action to add an encoding preset with the selected
// buffer's current encoding, and use it for the buffer.
func (m *BufferModel) NewPreset() Action { return MakeAction((*newPreset)(m)) }

type newPreset BufferModel

func (v *newPreset) Enabled() bool { return (*BufferModel)(v).selectedSample() != nil }
func (v *newPreset) Do() {
	s := (*BufferModel)(v).selectedSample()
	enc, _ := v.d.Song.SampleEncoding(s)
	base := "Preset"
	if p := (*BufferModel)(v).selectedPreset(); p != nil {
		base = p.Name
	}
	name := base
	for i := 2; ; i++ {
		if _, exists := v.d.Song.EncodingPresets.Find(name); !exists {
			break
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
	defer (*BufferModel)(v).change("NewPreset")()
	v.d.Song.EncodingPresets = append(v.d.Song.EncodingPresets, sointu.EncodingPreset{Name: name, Encoding: enc.Copy()})
	c := (*BufferModel)(v).replaceSample()
	c.Preset, c.Encoding = name, nil
}

// DeletePreset returns an Action to delete the encoding preset used by the
// selected buffer. Samples using it get its encoding as their own.
func (m *BufferModel) DeletePreset() Action { return MakeAction((*deletePreset)(m)) }

type deletePreset BufferModel

func (v *deletePreset) Enabled() bool { return (*BufferModel)(v).selectedPreset() != nil }
func (v *deletePreset) Do() {
	p := *(*BufferModel)(v).selectedPreset()
	defer (*BufferModel)(v).change("DeletePreset")()
	for i := range v.d.Song.Buffers {
		if s := v.d.Song.Buffers[i].Sample; s != nil && s.Encoding == nil && s.Preset == p.Name {
			c := *s
			e := p.Encoding.Copy()
			c.Encoding = &e
			v.d.Song.Buffers[i].Sample = &c
		}
	}
	presets := v.d.Song.EncodingPresets[:0:0]
	for _, q := range v.d.Song.EncodingPresets {
		if q.Name != p.Name {
			presets = append(presets, q)
		}
	}
	v.d.Song.EncodingPresets = presets
}

// Info returns a description of the selected buffer's sample: file, sizes,
// length and status.
func (m *BufferModel) Info() string {
	buf := m.selected()
	if buf == nil {
		return ""
	}
	if buf.Bus {
		return "Bus of 8 channels, processed in place every sample by\nmc units: mcspread spreads a signal over it, mcdelay,\nmcmix, mcfilter, mcloop and mcloopend change it, and\nmcsum sums it back. Not stored in the song."
	}
	if buf.Spectrum {
		size, channels, ok := (*Model)(m).spectrumSize(buf.ID)
		if !ok {
			return "Spectrum buffer, not written by any spfft or spcopy unit."
		}
		kind := "Spectrum"
		if channels == 2 {
			kind = "Stereo spectrum"
		}
		return fmt.Sprintf("%s of %d samples: %d bins of %s Hz.\nWritten by spfft units, changed by spectral units and\nturned back into sound by spifft units. Not stored in the song.",
			kind, size, size/2+1, strconv.FormatFloat(44100/float64(size), 'g', 3, 64))
	}
	if buf.Writable() {
		filled, _ := (*Model)(m).bufferFill(buf.ID)
		line := fmt.Sprintf("Recorded: %s of %s", formatDuration(filled), formatDuration(buf.Frames))
		if filled >= buf.Frames {
			line += " (full)"
		}
		return line + "\nWritten by bufwrite units while the song plays; kept until\nrecorded again or cleared. Not stored in the song."
	}
	if buf.Sample == nil {
		return "No sample"
	}
	status := m.buffers.status[buf.ID]
	lines := []string{
		fmt.Sprintf("File: %s (stored as %s)", buf.Sample.FileName, formatSize(len(buf.Sample.Data))),
	}
	switch {
	case status.Processing:
		lines = append(lines, "Encoding...")
	case status.Err != nil:
		lines = append(lines, "Error: "+status.Err.Error())
	default:
		lines = append(lines, fmt.Sprintf("Encoded: %s, %s", formatSize(status.EncodedSize), formatDuration(m.buffers.audio[buf.ID].Frames())))
	}
	return strings.Join(lines, "\n")
}

// Original returns a Bool that, when set, plays the buffers' samples without
// encoding them, to compare with the encoded versions.
func (m *BufferModel) Original() Bool { return MakeBool((*bufferOriginal)(m)) }

type bufferOriginal BufferModel

func (v *bufferOriginal) Value() bool { return v.buffers.original }
func (v *bufferOriginal) SetValue(value bool) {
	v.buffers.original = value
	(*Model)(v).syncBuffers()
}

// Preview returns a Bool for playing the selected buffer's audio once, as the
// synth hears it: encoded, or original when Original is on. Setting it to
// false stops the preview.
func (m *BufferModel) Preview() Bool { return MakeBool((*bufferPreview)(m)) }

type bufferPreview BufferModel

func (v *bufferPreview) Value() bool { return v.playerStatus.Previewing }
func (v *bufferPreview) SetValue(value bool) {
	var audio sointu.BufferAudio
	if buf := (*BufferModel)(v).selected(); value && buf != nil {
		audio = v.buffers.audio[buf.ID]
	}
	TrySend(v.broker.ToPlayer, any(PreviewMsg{Audio: audio}))
	v.playerStatus.Previewing = audio.Frames() > 0 // until the player reports back
}
func (v *bufferPreview) Enabled() bool {
	buf := (*BufferModel)(v).selected()
	return v.playerStatus.Previewing || (buf != nil && !buf.Writable() && v.buffers.audio[buf.ID].Frames() > 0)
}

// bufferFrames returns the number of frames in the audio of the buffer with
// the given ID, or 0 if it is not ready.
func (m *Model) bufferFrames(id int) int { return m.buffers.audio[id].Frames() }

func formatSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func formatDuration(frames int) string {
	return fmt.Sprintf("%.2f s", float64(frames)/44100)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SplitArgs splits a command line into arguments like a POSIX shell does for
// words: whitespace separates arguments, and single quotes, double quotes and
// backslashes quote characters.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inArg = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inArg = r, true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unterminated quote or escape")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// JoinArgs joins arguments into a command line that SplitArgs splits back
// into the same arguments.
func JoinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\") {
			quoted[i] = a
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// Show returns an Action to select the buffer with the given ID in the
// Buffers tab and show the tab.
func (m *BufferModel) Show(id int) Action { return MakeAction(showBuffer{id, m}) }

type showBuffer struct {
	id int
	m  *BufferModel
}

func (s showBuffer) Enabled() bool {
	_, ok := s.m.d.Song.Buffers.Find(s.id)
	return ok
}
func (s showBuffer) Do() {
	for i, buf := range s.m.d.Song.Buffers {
		if buf.ID == s.id {
			s.m.List().SetSelected(i)
			(*Model)(s.m).Instrument().Tab().SetValue(int(InstrumentBuffersTab))
			return
		}
	}
}
