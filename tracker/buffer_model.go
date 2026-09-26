package tracker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

// Buffer returns a BufferModel, a view of the model used to manipulate the
// song's buffers.
func (m *Model) Buffer() *BufferModel { return (*BufferModel)(m) }

type BufferModel Model

// customPreset is shown as the preset of buffers whose encoding does not
// match any preset.
const customPreset = "Custom"

// AudioFileExtensions are the extensions offered when importing samples.
var AudioFileExtensions = []string{".wav", ".flac", ".aif", ".aiff", ".mp3", ".ogg", ".opus", ".m4a", ".webm"}

// loadEncodingPresets loads the encoding presets, alerting about errors in
// the user's presets file.
func (m *Model) loadEncodingPresets() {
	var err error
	m.encodingPresets, err = ffmpeg.Presets()
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
func (v *bufferList) Selected2() int    { return v.Selected() }
func (v *bufferList) SetSelected(i int) { v.d.BufferIndex = i }
func (v *bufferList) SetSelected2(int)  {}

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
	sample := &sointu.AudioSample{FileName: fileName, Data: data}
	if buf := m.selected(); replace && buf != nil {
		if buf.Sample != nil {
			sample.Encoding = buf.Sample.Encoding
		} else {
			sample.Encoding = m.defaultEncoding()
		}
		buf.Sample = sample
		buf.Channels = channels
		return
	}
	id := 1
	for _, buf := range m.d.Song.Buffers {
		id = max(id, buf.ID+1)
	}
	sample.Encoding = m.defaultEncoding()
	name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	m.d.Song.Buffers = append(m.d.Song.Buffers, sointu.Buffer{ID: id, Name: name, Channels: channels, Sample: sample})
	m.d.BufferIndex = len(m.d.Song.Buffers) - 1
}

func (m *BufferModel) defaultEncoding() sointu.Encoding {
	for _, p := range m.encodingPresets {
		if p.Name == ffmpeg.DefaultPreset {
			return p.Encoding()
		}
	}
	return sointu.Encoding{}
}

// Delete returns an Action to delete the selected buffer. Units playing it
// keep referring to its ID and fall silent.
func (m *BufferModel) Delete() Action { return MakeAction((*deleteBuffer)(m)) }

type deleteBuffer BufferModel

func (m *deleteBuffer) Enabled() bool { return len(m.d.Song.Buffers) > 0 }
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

// Preset returns an Int for choosing the encoding preset of the selected
// buffer. The last value is "Custom", shown when the encoding does not match
// a preset.
func (m *BufferModel) Preset() Int { return MakeInt((*bufferPreset)(m)) }

type bufferPreset BufferModel

func (v *bufferPreset) Value() int {
	buf := (*BufferModel)(v).selected()
	if buf == nil || buf.Sample == nil {
		return len(v.encodingPresets)
	}
	for i, p := range v.encodingPresets {
		if p.Name == buf.Sample.Encoding.Preset && p.Format == buf.Sample.Encoding.Format && slicesEqual(p.Args, buf.Sample.Encoding.Args) {
			return i
		}
	}
	return len(v.encodingPresets)
}
func (v *bufferPreset) SetValue(value int) bool {
	buf := (*BufferModel)(v).selected()
	if buf == nil || buf.Sample == nil || value >= len(v.encodingPresets) {
		return false // choosing "Custom" does nothing; edit the arguments instead
	}
	defer (*BufferModel)(v).change("BufferPreset")()
	s := *buf.Sample
	s.Encoding = v.encodingPresets[value].Encoding()
	buf.Sample = &s
	return true
}
func (v *bufferPreset) Range() RangeInclusive { return RangeInclusive{0, len(v.encodingPresets)} }
func (v *bufferPreset) StringOf(value int) string {
	if value >= 0 && value < len(v.encodingPresets) {
		return v.encodingPresets[value].Name
	}
	return customPreset
}
func (v *bufferPreset) Enabled() bool {
	buf := (*BufferModel)(v).selected()
	return buf != nil && buf.Sample != nil
}

// Format returns a String for the ffmpeg output format of the selected
// buffer's encoding. An empty format stores the sample as it is.
func (m *BufferModel) Format() String { return MakeString((*bufferFormat)(m)) }

type bufferFormat BufferModel

func (v *bufferFormat) Value() string {
	if buf := (*BufferModel)(v).selected(); buf != nil && buf.Sample != nil {
		return buf.Sample.Encoding.Format
	}
	return ""
}
func (v *bufferFormat) SetValue(value string) bool {
	return (*BufferModel)(v).setEncoding(func(e *sointu.Encoding) { e.Format = strings.TrimSpace(value) })
}

// Args returns a String for the ffmpeg output arguments of the selected
// buffer's encoding, quoted like a shell command line.
func (m *BufferModel) Args() String { return MakeString((*bufferArgs)(m)) }

type bufferArgs BufferModel

func (v *bufferArgs) Value() string {
	if buf := (*BufferModel)(v).selected(); buf != nil && buf.Sample != nil {
		return JoinArgs(buf.Sample.Encoding.Args)
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

// setEncoding changes the encoding of the selected buffer, marking it custom.
func (m *BufferModel) setEncoding(f func(*sointu.Encoding)) bool {
	buf := m.selected()
	if buf == nil || buf.Sample == nil {
		return false
	}
	defer (*Model)(m).change("BufferEncoding", BufferChange, MinorChange)()
	s := *buf.Sample
	s.Encoding.Args = append([]string(nil), s.Encoding.Args...)
	f(&s.Encoding)
	s.Encoding.Preset = customPreset
	buf.Sample = &s
	return true
}

// Info returns a description of the selected buffer's sample: file, sizes,
// length and status.
func (m *BufferModel) Info() string {
	buf := m.selected()
	if buf == nil {
		return ""
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
