package sointu

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"time"
)

type (
	// AudioBuffer is a buffer of stereo audio samples of variable length, each
	// sample represented by [2]float32. [0] is left channel, [1] is right
	AudioBuffer [][2]float32

	CloserWaiter interface {
		io.Closer
		Wait()
	}

	// AudioContext represents the low-level audio drivers. There should be at
	// most one AudioContext at a time. The interface is implemented at least by
	// oto.OtoContext, but in future we could also mock it.
	//
	// AudioContext is used to play one or more AudioSources. Playing can be
	// stopped by closing the returned io.Closer.
	AudioContext interface {
		Play(r AudioSource) CloserWaiter
	}

	// AudioSource is an function for reading audio samples into an AudioBuffer.
	// Returns error if the buffer is not filled.
	AudioSource func(buf AudioBuffer) error

	BufferSource struct {
		buffer AudioBuffer
		pos    int
	}

	// Synth represents a state of a synthesizer, compiled from a Patch.
	Synth interface {
		// Render tries to fill a stereo signal buffer with sound from the
		// synthesizer, until either the buffer is full or a given number of
		// timesteps is advanced. Normally, 1 sample = 1 unit of time, but speed
		// modulations may change this. It returns the number of samples filled (in
		// stereo samples i.e. number of elements of AudioBuffer filled), the
		// number of sync outputs written, the number of time steps time advanced,
		// and a possible error.
		Render(buffer AudioBuffer, maxtime int) (sample int, time int, err error)

		// Update recompiles a patch, but should maintain as much as possible of its
		// state as reasonable. For example, filters should keep their state and
		// delaylines should keep their content. Every change in the Patch triggers
		// an Update and if the Patch would be started fresh every time, it would
		// lead to very choppy audio.
		Update(patch Patch, bpm int) error

		// Trigger triggers a note for a given voice. Called between synth.Renders.
		Trigger(voice int, note byte)

		// Release releases the currently playing note for a given voice. Called
		// between synth.Renders.
		Release(voice int)

		// Close disposes the synth, freeing any resources. No other functions should be called after Close.
		Close()

		// Populates the given array with the current CPU load of each thread,
		// returning the number of threads / elements populated
		CPULoad([]CPULoad) int
	}

	// Synther compiles a given Patch into a Synth, throwing errors if the
	// Patch is malformed.
	Synther interface {
		Name() string // Name of the synther, e.g. "Go" or "Native"
		Synth(patch Patch, bpm int) (Synth, error)
		SupportsMultithreading() bool
	}

	CPULoad float32

	// BufferSetter is implemented by Synths that can play buffers.
	BufferSetter interface {
		// SetBuffers sets the audio of the buffers, keyed by Buffer.ID.
		// Buffers without audio are silent. Called between synth.Renders;
		// the caller does not modify the audio afterwards, so the synth can
		// keep it. The synth writes to the Data of writable buffers.
		SetBuffers(buffers map[int]BufferAudio)
	}

	// BufferWriter is implemented by Synths that write to buffers.
	BufferWriter interface {
		// WrittenBuffers returns the writable buffers with what has been
		// written to them so far. Passing them to SetBuffers of another synth
		// continues from there.
		WrittenBuffers() map[int]BufferAudio
	}

	// SpectrumReporter is implemented by Synths with spectral units.
	SpectrumReporter interface {
		// Spectrum appends to dst the magnitudes of bins 0 to size/2 of the
		// latest spectrum of the spectrum buffer with the given ID, and
		// returns them and the size of the spectrum, or 0 if the synth has no
		// such buffer.
		Spectrum(bufferID int, dst []float32) ([]float32, int)
	}

	// UnitLevelsReporter is implemented by Synths that can report the levels
	// of the buses of the mc units.
	UnitLevelsReporter interface {
		// UnitLevels appends to dst the peak level of each channel of the bus
		// right after the mc unit with the given ID, since the last call, and
		// returns them, or nil if there is no such unit.
		UnitLevels(unitID int, dst []float32) []float32
	}

	// UnitSpectrumReporter is implemented by Synths that can report the
	// spectrum right after each spectral unit processed it.
	UnitSpectrumReporter interface {
		// UnitSpectrum appends to dst the magnitudes of bins 0 to size/2 of
		// the spectrum as the spectral unit with the given ID last left it,
		// and returns them and the size, or 0 if there is none yet. Calling
		// it asks the synth to keep the next one.
		UnitSpectrum(unitID int, dst []float32) ([]float32, int)
	}

	// PlayheadReporter is implemented by Synths that can tell where bufread
	// units are playing.
	PlayheadReporter interface {
		// Playheads appends the positions of the notes of bufread units to
		// dst: held ones, and ones released less than MaxPlayheadRelease
		// frames ago.
		Playheads(dst []Playhead) []Playhead
	}

	// Playhead is where a bufread unit is playing: a frame of a buffer.
	// Released is the number of frames since the note was released, or 0 if
	// it is held.
	Playhead struct{ BufferID, Frame, Released int }

	// BufferAudio is the audio of a buffer: interleaved frames at 44100 Hz.
	BufferAudio struct {
		Channels int
		Data     []float32
		// Writable is true for buffers that bufwrite units write to. Their
		// valid frames are the Filled frames before Head, wrapping around the
		// end; for other buffers, all frames are valid.
		Writable     bool
		Head, Filled int
	}
)

// Frames returns the number of frames in the buffer audio.
func (b BufferAudio) Frames() int {
	if b.Channels <= 0 {
		return 0
	}
	return len(b.Data) / b.Channels
}

// Play plays the Song by first compiling the patch with the given Synther,
// returning the stereo audio buffer as a result (and possible errors). Buffers
// are silent; see PlayWithBuffers.
func Play(synther Synther, song Song, progress func(float32)) (AudioBuffer, error) {
	return PlayWithBuffers(synther, song, nil, progress)
}

// PlayWithBuffers is like Play, but gives the synth the audio of the song's
// buffers with samples, keyed by Buffer.ID. Writable buffers start empty. It is an error if the song has buffers with
// samples but the synth cannot play buffers.
func PlayWithBuffers(synther Synther, song Song, buffers map[int]BufferAudio, progress func(float32)) (AudioBuffer, error) {
	err := song.Validate()
	if err != nil {
		return nil, err
	}
	synth, err := synther.Synth(song.Patch, song.BPM)
	if err != nil {
		return nil, fmt.Errorf("sointu.Play failed: %v", err)
	}
	defer synth.Close()
	buffers = maps.Clone(buffers)
	if buffers == nil {
		buffers = map[int]BufferAudio{}
	}
	for _, b := range song.Buffers {
		if b.Writable() {
			buffers[b.ID] = b.NewAudio() // rendering starts with nothing written
		}
	}
	if s, ok := synth.(BufferSetter); ok {
		s.SetBuffers(buffers)
	} else if len(buffers) > 0 {
		return nil, fmt.Errorf("the %v synth cannot play buffers", synther.Name())
	}
	curVoices := make([]int, len(song.Score.Tracks))
	for i := range curVoices {
		curVoices[i] = song.Score.FirstVoiceForTrack(i)
	}
	initialCapacity := song.Score.LengthInRows() * song.SamplesPerRow()
	buffer := make(AudioBuffer, 0, initialCapacity)
	rowbuffer := make(AudioBuffer, song.SamplesPerRow())
	for row := 0; row < song.Score.LengthInRows(); row++ {
		patternRow := row % song.Score.RowsPerPattern
		pattern := row / song.Score.RowsPerPattern
		for t := range song.Score.Tracks {
			order := song.Score.Tracks[t].Order
			if pattern < 0 || pattern >= len(order) {
				continue
			}
			patternIndex := song.Score.Tracks[t].Order[pattern]
			patterns := song.Score.Tracks[t].Patterns
			if patternIndex < 0 || int(patternIndex) >= len(patterns) {
				continue
			}
			pattern := patterns[patternIndex]
			if patternRow < 0 || patternRow >= len(pattern) {
				continue
			}
			note := pattern[patternRow]
			if note > 0 && note <= 1 { // anything but hold causes an action.
				continue
			}
			synth.Release(curVoices[t])
			if note > 1 {
				curVoices[t]++
				first := song.Score.FirstVoiceForTrack(t)
				if curVoices[t] >= first+song.Score.Tracks[t].NumVoices {
					curVoices[t] = first
				}
				synth.Trigger(curVoices[t], note)
			}
		}
		tries := 0
		for rowtime := 0; rowtime < song.SamplesPerRow(); {
			samples, time, err := synth.Render(rowbuffer, song.SamplesPerRow()-rowtime)
			if err != nil {
				return buffer, fmt.Errorf("render failed: %v", err)
			}
			rowtime += time
			buffer = append(buffer, rowbuffer[:samples]...)
			if tries > 100 {
				return nil, fmt.Errorf("Song speed modulation likely so slow that row never advances; error at pattern %v, row %v", pattern, patternRow)
			}
		}
		if progress != nil {
			progress(float32(row+1) / float32(song.Score.LengthInRows()))
		}
	}
	return buffer, nil
}

// Fill fills the AudioBuffer using a Synth, disregarding all syncs and time
// limits. Note that this will change the state of the Synth.
func (buffer AudioBuffer) Fill(synth Synth) error {
	s, _, err := synth.Render(buffer, math.MaxInt32)
	if err != nil {
		return fmt.Errorf("synth.Render failed: %v", err)
	}
	if s != len(buffer) {
		return errors.New("in AudioBuffer.Fill, synth.Render should have filled the whole buffer but did not")
	}
	return nil
}

func (b AudioBuffer) Source() AudioSource {
	return func(buf AudioBuffer) error {
		n := copy(buf, b)
		b = b[n:]
		if n < len(buf) {
			return io.EOF
		}
		return nil
	}
}

// ReadAudio reads audio samples from an AudioSource into an AudioBuffer.
// Returns an error when the buffer is fully consumed.
func (a *BufferSource) ReadAudio(buf AudioBuffer) error {
	n := copy(buf, a.buffer[a.pos:])
	a.pos += n
	if a.pos >= len(a.buffer) {
		return io.EOF
	}
	return nil
}

// Wav converts an AudioBuffer into a valid WAV-file, returned as a []byte
// array.
//
// If pcm16 is set to true, the samples in the WAV-file will be 16-bit signed
// integers; otherwise the samples will be 32-bit floats
func (buffer AudioBuffer) Wav(pcm16 bool) ([]byte, error) {
	buf := new(bytes.Buffer)
	wavHeader(len(buffer)*2, pcm16, buf)
	err := buffer.rawToBuffer(pcm16, buf)
	if err != nil {
		return nil, fmt.Errorf("Wav failed: %v", err)
	}
	return buf.Bytes(), nil
}

// Raw converts an AudioBuffer into a raw audio file, returned as a []byte
// array.
//
// If pcm16 is set to true, the samples will be 16-bit signed integers;
// otherwise the samples will be 32-bit floats
func (buffer AudioBuffer) Raw(pcm16 bool) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := buffer.rawToBuffer(pcm16, buf)
	if err != nil {
		return nil, fmt.Errorf("Raw failed: %v", err)
	}
	return buf.Bytes(), nil
}

func (p *CPULoad) Update(duration time.Duration, frames int64) {
	if frames <= 0 {
		return // no frames rendered, so cannot compute CPU load
	}
	realtime := float64(duration) / 1e9
	songtime := float64(frames) / 44100
	newload := realtime / songtime
	alpha := math.Exp(-songtime) // smoothing factor, time constant of 1 second
	*p = CPULoad(float64(*p)*alpha + newload*(1-alpha))
}

func (data AudioBuffer) rawToBuffer(pcm16 bool, buf *bytes.Buffer) error {
	var err error
	if pcm16 {
		int16data := make([][2]int16, len(data))
		for i, v := range data {
			int16data[i][0] = int16(clamp(int(v[0]*math.MaxInt16), math.MinInt16, math.MaxInt16))
			int16data[i][1] = int16(clamp(int(v[1]*math.MaxInt16), math.MinInt16, math.MaxInt16))
		}
		err = binary.Write(buf, binary.LittleEndian, int16data)
	} else {
		err = binary.Write(buf, binary.LittleEndian, data)
	}
	if err != nil {
		return fmt.Errorf("could not binary write data to binary buffer: %v", err)
	}
	return nil
}

// wavHeader writes a wave header for either float32 or int16 .wav file into the
// bytes.buffer. It needs to know the length of the buffer and assumes stereo
// sound, so the length in stereo samples (L + R) is bufferlength / 2. If pcm16
// = true, then the header is for int16 audio; pcm16 = false means the header is
// for float32 audio. Assumes 44100 Hz sample rate.
func wavHeader(bufferLength int, pcm16 bool, buf *bytes.Buffer) {
	// Refer to: http://www-mmsp.ece.mcgill.ca/Documents/AudioFormats/WAVE/WAVE.html
	numChannels := 2
	sampleRate := 44100
	var bytesPerSample, chunkSize, fmtChunkSize, waveFormat int
	var factChunk bool
	if pcm16 {
		bytesPerSample = 2
		chunkSize = 36 + bytesPerSample*bufferLength
		fmtChunkSize = 16
		waveFormat = 1 // PCM
		factChunk = false
	} else {
		bytesPerSample = 4
		chunkSize = 50 + bytesPerSample*bufferLength
		fmtChunkSize = 18
		waveFormat = 3 // IEEE float
		factChunk = true
	}
	buf.Write([]byte("RIFF"))
	binary.Write(buf, binary.LittleEndian, uint32(chunkSize))
	buf.Write([]byte("WAVE"))
	buf.Write([]byte("fmt "))
	binary.Write(buf, binary.LittleEndian, uint32(fmtChunkSize))
	binary.Write(buf, binary.LittleEndian, uint16(waveFormat))
	binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate*numChannels*bytesPerSample)) // avgBytesPerSec
	binary.Write(buf, binary.LittleEndian, uint16(numChannels*bytesPerSample))            // blockAlign
	binary.Write(buf, binary.LittleEndian, uint16(8*bytesPerSample))                      // bits per sample
	if fmtChunkSize > 16 {
		binary.Write(buf, binary.LittleEndian, uint16(0)) // size of extension
	}
	if factChunk {
		buf.Write([]byte("fact"))
		binary.Write(buf, binary.LittleEndian, uint32(4))            // fact chunk size
		binary.Write(buf, binary.LittleEndian, uint32(bufferLength)) // sample length
	}
	buf.Write([]byte("data"))
	binary.Write(buf, binary.LittleEndian, uint32(bytesPerSample*bufferLength))
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// MaxPlayheadRelease is how long, in frames, released notes of bufread units
// still have playheads: the synth does not know when they fall silent.
const MaxPlayheadRelease = 44100
