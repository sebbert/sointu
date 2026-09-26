package tracker

import (
	"fmt"
	"maps"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

type (
	// bufferState is the model's view of the buffers' audio: which samples
	// are being processed by the buffer worker, and the results.
	bufferState struct {
		keys   map[int]ffmpeg.Key // key of the audio requested for each buffer ID
		audio  map[int]sointu.BufferAudio
		status map[int]BufferStatus
		// original plays the samples without encoding, for comparison
		original bool
	}

	// BufferStatus tells whether a buffer's audio is ready.
	BufferStatus struct {
		Processing  bool
		Err         error
		EncodedSize int // size of the encoded sample in the compiled player
	}

	// bufferJob asks the buffer worker to process the sample of a buffer.
	bufferJob struct {
		ID       int
		Key      ffmpeg.Key
		Data     []byte
		Encoding sointu.Encoding
		Channels int
	}

	// bufferResult is the buffer worker's reply to a bufferJob.
	bufferResult struct {
		ID     int
		Key    ffmpeg.Key
		Result ffmpeg.Result
		Err    error
	}

	// BufferAudioMsg is sent to the player when the audio of the buffers
	// changes.
	BufferAudioMsg struct {
		Audio map[int]sointu.BufferAudio
	}
)

// syncBuffers compares the song's buffers to the audio requested so far, asks
// the buffer worker to process changed samples and tells the player about
// removed ones.
func (m *Model) syncBuffers() {
	b := &m.buffers
	if b.keys == nil {
		b.keys, b.audio, b.status = map[int]ffmpeg.Key{}, map[int]sointu.BufferAudio{}, map[int]BufferStatus{}
	}
	changed := false
	seen := map[int]bool{}
	for _, buf := range m.d.Song.Buffers {
		seen[buf.ID] = true
		if buf.Sample == nil {
			if _, ok := b.keys[buf.ID]; ok {
				m.forgetBuffer(buf.ID)
				changed = true
			}
			continue
		}
		enc, err := m.d.Song.SampleEncoding(buf.Sample)
		if b.original {
			enc, err = sointu.Encoding{}, nil
		}
		if err != nil {
			if b.status[buf.ID].Err == nil || b.status[buf.ID].Err.Error() != err.Error() {
				delete(b.keys, buf.ID)
				delete(b.audio, buf.ID)
				b.status[buf.ID] = BufferStatus{Err: err}
				changed = true
			}
			continue
		}
		key := ffmpeg.KeyOf(buf.Sample.Data, enc, buf.Channels)
		if k, ok := b.keys[buf.ID]; ok && k == key {
			continue
		}
		b.keys[buf.ID] = key
		b.status[buf.ID] = BufferStatus{Processing: true}
		// the old audio keeps playing until the new one is ready
		job := bufferJob{ID: buf.ID, Key: key, Data: buf.Sample.Data, Encoding: enc, Channels: buf.Channels}
		if !TrySend(m.broker.ToBufferWorker, any(job)) {
			b.status[buf.ID] = BufferStatus{Err: fmt.Errorf("buffer worker is busy")}
		}
	}
	for id := range b.status {
		if !seen[id] {
			m.forgetBuffer(id)
			changed = true
		}
	}
	if changed {
		m.sendBufferAudio()
	}
}

func (m *Model) forgetBuffer(id int) {
	delete(m.buffers.keys, id)
	delete(m.buffers.audio, id)
	delete(m.buffers.status, id)
}

// handleBufferResult stores a result from the buffer worker, unless the
// buffer has changed since the job was sent.
func (m *Model) handleBufferResult(r bufferResult) {
	if k, ok := m.buffers.keys[r.ID]; !ok || k != r.Key {
		return // stale
	}
	if r.Err != nil {
		m.buffers.status[r.ID] = BufferStatus{Err: r.Err}
		m.Alerts().Add(fmt.Sprintf("Buffer %s: %v", m.bufferName(r.ID), r.Err), Error)
		return
	}
	m.buffers.status[r.ID] = BufferStatus{EncodedSize: len(r.Result.Encoded)}
	m.buffers.audio[r.ID] = r.Result.Audio
	m.sendBufferAudio()
}

func (m *Model) bufferName(id int) string {
	if buf, ok := m.d.Song.Buffers.Find(id); ok && buf.Name != "" {
		return buf.Name
	}
	return fmt.Sprint(id)
}

// sendBufferAudio sends the audio of all buffers to the player. The audio
// data is shared, as it is never modified.
func (m *Model) sendBufferAudio() {
	TrySend(m.broker.ToPlayer, any(BufferAudioMsg{Audio: maps.Clone(m.buffers.audio)}))
}

// BufferAudio returns the audio of all buffers that are ready, keyed by
// buffer ID.
func (m *Model) BufferAudio() map[int]sointu.BufferAudio { return maps.Clone(m.buffers.audio) }

// BufferStatus returns whether the audio of a buffer is ready.
func (m *Model) BufferStatus(id int) BufferStatus { return m.buffers.status[id] }

// runBufferWorker encodes and decodes the samples of buffers with ffmpeg,
// replying to the model. Jobs for the same buffer that are still queued are
// skipped in favor of the latest one.
func runBufferWorker(broker *Broker) {
	var cache *ffmpeg.Cache
	var findErr error
	for {
		select {
		case v := <-broker.ToBufferWorker:
			job, ok := v.(bufferJob)
			if !ok {
				continue
			}
			job = latestBufferJob(broker, job)
			if cache == nil && findErr == nil {
				var f *ffmpeg.FFmpeg
				if f, findErr = ffmpeg.Find(""); findErr == nil {
					dir, _ := ffmpeg.DefaultCacheDir()
					cache = ffmpeg.NewCache(f, dir)
				}
			}
			res := bufferResult{ID: job.ID, Key: job.Key, Err: findErr}
			if cache != nil {
				res.Result, res.Err = cache.Get(job.Data, job.Encoding, job.Channels)
			}
			TrySend(broker.ToModel, MsgToModel{Data: res})
		case <-broker.CloseBufferWorker:
			close(broker.FinishedBufferWorker)
			return
		}
	}
}

// latestBufferJob drains queued jobs, returning the latest job for the same
// buffer as job. Jobs for other buffers are put back in the queue.
func latestBufferJob(broker *Broker, job bufferJob) bufferJob {
	var others []any
loop:
	for {
		select {
		case v := <-broker.ToBufferWorker:
			if j, ok := v.(bufferJob); ok && j.ID == job.ID {
				job = j
			} else {
				others = append(others, v)
			}
		default:
			break loop
		}
	}
	for _, v := range others {
		TrySend(broker.ToBufferWorker, v)
	}
	return job
}
