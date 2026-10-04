package oto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/ebitengine/oto/v3"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/resample"
)

const latency = 2048 // in samples at 44100 Hz = ~46 ms

type (
	// OtoContext plays AudioSources, which give frames at 44100 Hz, on the
	// default audio device, at the sample rate that the context was
	// opened with.
	OtoContext struct {
		context    *oto.Context
		sampleRate int
	}

	OtoPlayer struct {
		player *oto.Player
		reader *OtoReader
	}

	OtoReader struct {
		audioSource sointu.AudioSource
		tmpBuffer   sointu.AudioBuffer
		waitGroup   sync.WaitGroup
		err         error
		errMutex    sync.RWMutex
	}
)

// NewContext opens the default audio device at sampleRate, in Hz. With 0
// it is the rate that the device runs at, so that the system does not
// resample what it gets, or 44100 Hz where that rate is not known. Sources
// are resampled from 44100 Hz to the rate; at 44100 Hz their frames go to
// the device as they are.
//
// Whatever the rate, the systems play it: AudioQueue on macOS and WASAPI
// on Windows convert it to that of the device, and so do ALSA's plug
// devices and the sound servers. Only an ALSA device without them takes the
// nearest rate it has instead, without telling oto, and would play too fast
// or too slow; DeviceSampleRate finds that rate.
func NewContext(sampleRate int) (*OtoContext, error) {
	if sampleRate <= 0 {
		sampleRate = DeviceSampleRate()
	}
	op := oto.NewContextOptions{}
	op.SampleRate = sampleRate
	op.ChannelCount = 2
	op.Format = oto.FormatFloat32LE
	context, readyChan, err := oto.NewContext(&op)
	if err != nil {
		return nil, fmt.Errorf("cannot create oto context: %w", err)
	}
	<-readyChan
	return &OtoContext{context: context, sampleRate: sampleRate}, nil
}

// DeviceSampleRate returns the sample rate that the default audio device
// runs at, in Hz, or 44100 where the system does not tell.
func DeviceSampleRate() int {
	if rate := deviceRate(); rate >= 8000 && rate <= 768000 {
		return rate
	}
	return resample.SynthRate
}

// SampleRate returns the sample rate that the device was opened with.
func (c *OtoContext) SampleRate() int { return c.sampleRate }

func (c *OtoContext) Play(r sointu.AudioSource) sointu.CloserWaiter {
	reader := &OtoReader{audioSource: resample.Source(r, resample.SynthRate, c.sampleRate)}
	reader.waitGroup.Add(1)
	player := c.context.NewPlayer(reader)
	// the same time, in bytes of frames at the rate of the device
	player.SetBufferSize(latency * c.sampleRate / resample.SynthRate * 8)
	player.Play()
	return OtoPlayer{player: player, reader: reader}
}

func (o OtoPlayer) Wait() {
	o.reader.waitGroup.Wait()
}

func (o OtoPlayer) Close() error {
	o.reader.closeWithError(errors.New("OtoPlayer was closed"))
	return o.player.Close()
}

func (o *OtoReader) Read(b []byte) (n int, err error) {
	o.errMutex.RLock()
	if o.err != nil {
		o.errMutex.RUnlock()
		return 0, o.err
	}
	o.errMutex.RUnlock()
	if len(b)%8 != 0 {
		return o.closeWithError(fmt.Errorf("oto: Read buffer length must be a multiple of 8"))
	}
	samples := len(b) / 8
	if samples > len(o.tmpBuffer) {
		o.tmpBuffer = append(o.tmpBuffer, make(sointu.AudioBuffer, samples-len(o.tmpBuffer))...)
	} else if samples < len(o.tmpBuffer) {
		o.tmpBuffer = o.tmpBuffer[:samples]
	}
	err = o.audioSource(o.tmpBuffer)
	if err != nil {
		return o.closeWithError(err)
	}
	for i := range o.tmpBuffer {
		binary.LittleEndian.PutUint32(b[i*8:], math.Float32bits(o.tmpBuffer[i][0]))
		binary.LittleEndian.PutUint32(b[i*8+4:], math.Float32bits(o.tmpBuffer[i][1]))
	}
	return samples * 8, nil
}

func (o *OtoReader) closeWithError(err error) (int, error) {
	o.errMutex.Lock()
	defer o.errMutex.Unlock()
	if o.err == nil {
		o.err = err
		o.waitGroup.Done()
	}
	return 0, err
}
