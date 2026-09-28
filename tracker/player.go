package tracker

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

type (
	// Player is the audio player for the tracker, run in a separate thread. It
	// is controlled by messages from the model and MIDI messages via the
	// context, typically from the VSTI host. The player sendTargets messages to the
	// model via the playerMessages channel. The model sendTargets messages to the
	// player via the modelMessages channel.
	Player struct {
		synth         sointu.Synth               // the synth used to render audio
		buffers       map[int]sointu.BufferAudio // the audio of the song's buffers
		writable      bool                       // some of the buffers are writable
		hostBPM       int                        // the tempo of the host, last told to the model
		playheads     []sointu.Playhead          // reused for updatePlayheads
		spectra       []SpectrumSource           // the spectra to report
		sinceSpectrum int                        // frames since the spectrum was last reported
		preview       sointu.BufferAudio         // buffer audio being previewed, mixed into the output
		previewPos    int                        // next frame of the preview
		song          sointu.Song                // the song being played
		playing       bool                       // is the player playing the score or not
		rowtime       int                        // how many samples have been played in the current row
		voices        [vm.MAX_VOICES]voice
		loop          Loop

		recording Recording // the recorded MIDI events and BPM

		frame       int64         // the current player frame, used to time events
		frameDeltas map[any]int64 // Player.frame (approx.)= event.Timestamp + frameDeltas[event.Source]
		events      NoteEventList

		midiRouter  midiRouter
		midiAssigns midiAssigns
		prevVal     []byte

		status PlayerStatus // the part of the Player state that is communicated to the model to visualize what Player is doing

		synther sointu.Synther // the synther used to create new synths
		broker  *Broker        // the broker used to communicate with different parts of the tracker
	}

	// PlayerStatus is the part of the player state that is communicated to the
	// model, for different visualizations of what is happening in the player.
	PlayerStatus struct {
		SongPos     sointu.SongPos         // the current position in the score
		VoiceLevels [vm.MAX_VOICES]float32 // a level that can be used to visualize the volume of each voice
		NumThreads  int
		CPULoad     [vm.MAX_THREADS]sointu.CPULoad // current CPU load of the player, used to adjust the render rate
		Previewing  bool                           // a buffer preview is playing
		// BufferFills tells how many frames have been written to the
		// writable buffers, for the first MaxBufferFills of them.
		BufferFills [MaxBufferFills]BufferFill
		// Playheads are where the held notes of bufread units are playing,
		// NumPlayheads of them.
		Playheads    [MaxPlayheads]sointu.Playhead
		NumPlayheads int
	}

	// BufferFill tells which frames of a writable buffer are valid: the
	// Filled frames before Head, wrapping around the end.
	BufferFill struct{ ID, Head, Filled int }

	// PlayerProcessContext is the context given to the player when processing
	// audio. Currently it is only used to get BPM from the VSTI host.
	PlayerProcessContext interface {
		BPM() (bpm float64, ok bool)
	}

	NullPlayerProcessContext struct{}

	// NoteEvent describes triggering or releasing of a note. The timestamps are
	// in frames, and relative to the clock of the event source. Different
	// sources can use different clocks. Player tries to adjust the timestamps
	// so that each note events would fall inside the current processing block,
	// by maintaining an estimate of the delta from the source clock to the
	// player clock.
	NoteEvent struct {
		Timestamp int64 // in frames, relative to whatever clock the source is using
		On        bool
		Channel   int // which track or instrument is triggered, depending on IsTrack
		Note      byte
		IsTrack   bool // true if "Channel" means track number, false if it means instrument number
		Source    any

		playerTimestamp int64 // the timestamp of the event, adjusted to the player's clock, used to sort events
	}
)

type (
	voice struct {
		triggerEvent      NoteEvent // which event triggered this voice, used to release the voice
		sustain           bool
		samplesSinceEvent int
	}

	NoteEventList []NoteEvent
)

const numRenderTries = 10000

func NewPlayer(broker *Broker, synther sointu.Synther) *Player {
	return &Player{
		broker:      broker,
		synther:     synther,
		frameDeltas: make(map[any]int64),
		midiAssigns: midiAssigns{ctoi: map[midiAssignKey][]midiAssignRange{}},
	}
}

// Process renders audio to the given buffer, trying to fill it completely. If
// the buffer is not filled, the synth is destroyed and an error is sent to the
// model. context tells the player which MIDI events happen during the current
// buffer. It is used to trigger and release notes during processing. The
// context is also used to get the current BPM from the host.
func (p *Player) Process(buffer sointu.AudioBuffer, context PlayerProcessContext) {
	p.processMessages(context)
	p.followHostTempo(context)
	p.events.adjustTimes(p.frameDeltas, p.frame, p.frame+int64(len(buffer)))
	frames := len(buffer)

	for i := 0; i < numRenderTries; i++ {
		for len(p.events) > 0 && p.events[0].playerTimestamp <= p.frame {
			ev := p.events[0]
			copy(p.events, p.events[1:]) // remove processed events
			p.events = p.events[:len(p.events)-1]
			p.recording.Record(ev, p.frame)
			p.processNoteEvent(ev)
		}
		framesUntilEvent := len(buffer)
		if len(p.events) > 0 {
			framesUntilEvent = min(int(p.events[0].playerTimestamp-p.frame), len(buffer))
		}
		if p.playing && p.rowtime >= p.song.SamplesPerRow() {
			p.advanceRow()
		}
		timeUntilRowAdvance := math.MaxInt32
		if p.playing {
			timeUntilRowAdvance = max(p.song.SamplesPerRow()-p.rowtime, 0)
		}
		var rendered, timeAdvanced int
		var err error
		if p.synth != nil {
			rendered, timeAdvanced, err = p.synth.Render(buffer[:framesUntilEvent], timeUntilRowAdvance)
			if err != nil {
				p.destroySynth()
				p.send(Alert{Message: fmt.Sprintf("synth.Render: %s", err.Error()), Priority: Error, Name: "PlayerCrash", Duration: defaultAlertDuration})
			} else {
				// for performance, we don't check for NaN of every sample, because typically NaNs propagate
				if rendered > 0 && (isNaN(buffer[0][0]) || isNaN(buffer[0][1]) || isInf(buffer[0][0]) || isInf(buffer[0][1])) {
					p.destroySynth()
					p.clearWrittenBuffers() // they might have recorded the NaNs
					p.send(Alert{Message: "Inf or NaN detected in synth output", Priority: Error, Name: "PlayerCrash", Duration: defaultAlertDuration})
				}
			}
		} else {
			rendered = min(framesUntilEvent, timeUntilRowAdvance)
			timeAdvanced = rendered
			clear(buffer[:rendered])
		}

		p.mixPreview(buffer[:rendered])

		bufPtr := p.broker.GetAudioBuffer() // borrow a buffer from the broker
		*bufPtr = append(*bufPtr, buffer[:rendered]...)
		if len(*bufPtr) == 0 || !TrySend(p.broker.ToModel, MsgToModel{Data: bufPtr}) {
			// if the buffer is empty or sending the rendered waveform to Model
			// failed, return the buffer to the broker
			p.broker.PutAudioBuffer(bufPtr)
		}
		buffer = buffer[rendered:]
		p.frame += int64(rendered)
		p.rowtime += timeAdvanced
		for i := range p.voices {
			p.voices[i].samplesSinceEvent += rendered
		}
		alpha := float32(math.Exp(-float64(rendered) / 15000))
		for i, state := range p.voices {
			if state.sustain {
				p.status.VoiceLevels[i] = (p.status.VoiceLevels[i]-0.5)*alpha + 0.5
			} else {
				p.status.VoiceLevels[i] *= alpha
			}
		}
		// when the buffer is full, return
		if len(buffer) == 0 {
			if p.synth != nil {
				p.status.NumThreads = p.synth.CPULoad(p.status.CPULoad[:])
				if p.writable {
					p.updateBufferFills()
				}
				if len(p.buffers) > 0 {
					p.updatePlayheads()
				}
				p.reportSpectrum(frames)
			}
			p.send(nil)
			return
		}
	}
	// we were not able to fill the buffer with NUM_RENDER_TRIES attempts, destroy synth and throw an error
	p.destroySynth()
	p.events = p.events[:0] // clear events, so we don't try to process them again
	p.SendAlert("PlayerCrash", fmt.Sprintf("synth did not fill the audio buffer even with %d render calls", numRenderTries), Error)
}

func (p *Player) EmitMIDIMsg(msg *MIDIMessage) bool { return p.midiRouter.route(p.broker, msg) }

// mixPreview adds the next frames of the buffer preview to the output.
func (p *Player) mixPreview(buffer sointu.AudioBuffer) {
	a := p.preview
	if a.Channels <= 0 {
		return
	}
	n := min(len(buffer), a.Frames()-p.previewPos)
	for i := 0; i < n; i++ {
		f := (p.previewPos + i) * a.Channels
		buffer[i][0] += a.Data[f]
		buffer[i][1] += a.Data[f+a.Channels-1] // mono plays in both channels
	}
	p.previewPos += n
	if p.previewPos >= a.Frames() {
		p.preview, p.status.Previewing = sointu.BufferAudio{}, false
	}
}

// keepWrittenBuffers updates the writable buffers with what the synth has
// written to them, so that the next synth continues from there.
func (p *Player) keepWrittenBuffers() {
	w, ok := p.synth.(sointu.BufferWriter)
	if !ok {
		return
	}
	for id, b := range w.WrittenBuffers() {
		if old, ok := p.buffers[id]; ok && sameData(old.Data, b.Data) {
			p.buffers[id] = b
		}
	}
}

// clearWrittenBuffers discards what has been written to the writable buffers.
func (p *Player) clearWrittenBuffers() {
	for id, b := range p.buffers {
		if b.Writable {
			clear(b.Data)
			b.Head, b.Filled = 0, 0
			p.buffers[id] = b
		}
	}
}

// sameData reports whether a and b are the same slice.
func sameData(a, b []float32) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func (p *Player) destroySynth() {
	p.status.NumPlayheads = 0
	if p.synth != nil {
		p.keepWrittenBuffers()
		p.synth.Close()
		p.synth = nil
	}
	p.prevVal = p.prevVal[:0]
}

func (p *Player) advanceRow() {
	if p.song.Score.Length == 0 || p.song.Score.RowsPerPattern == 0 {
		return
	}
	origPos := p.status.SongPos
	p.status.SongPos.PatternRow++ // advance row (this is why we subtracted one in Play())
	if p.loop.Length > 0 && p.status.SongPos.PatternRow >= p.song.Score.RowsPerPattern && p.status.SongPos.OrderRow == p.loop.Start+p.loop.Length-1 {
		p.status.SongPos.PatternRow = 0
		p.status.SongPos.OrderRow = p.loop.Start
	}
	p.status.SongPos = p.song.Score.Clamp(p.status.SongPos)
	if p.status.SongPos == origPos {
		p.send(IsPlayingMsg{bool: false})
		p.playing = false
		for i := range p.song.Score.Tracks {
			p.processNoteEvent(NoteEvent{Channel: i, IsTrack: true, Source: p})
		}
		return
	}
	for i, t := range p.song.Score.Tracks {
		n := t.Note(p.status.SongPos)
		switch {
		case n == 0:
			p.processNoteEvent(NoteEvent{Channel: i, IsTrack: true, Source: p, On: false})
		case n > 1:
			p.processNoteEvent(NoteEvent{Channel: i, IsTrack: true, Source: p, Note: n, On: true})
		} // n = 1 means hold so do nothing
	}
	p.rowtime = 0
	p.send(nil) // just send volume and song row information
}

func (p NullPlayerProcessContext) BPM() (bpm float64, ok bool) {
	return 0, false // no BPM available
}

func isNaN(f float32) bool {
	return f != f
}

func isInf(f float32) bool {
	return f > math.MaxFloat32 || f < -math.MaxFloat32
}

func (p *Player) processMessages(context PlayerProcessContext) {
loop:
	for { // process new message
		select {
		case msg := <-p.broker.ToPlayer:
			switch m := msg.(type) {
			case PanicMsg:
				if m.bool {
					p.destroySynth()
				} else {
					p.compileOrUpdateSynth()
				}
			case sointu.Song:
				p.song = m
				p.compileOrUpdateSynth()
			case sointu.Patch:
				p.song.Patch = m
				p.compileOrUpdateSynth()
			case sointu.Score:
				p.song.Score = m
			case SpectrumWatchMsg:
				p.spectra = m
			case PreviewMsg:
				p.preview, p.previewPos = m.Audio, 0
				p.status.Previewing = m.Audio.Frames() > 0
			case BufferAudioMsg:
				p.keepWrittenBuffers()
				for id, b := range m.Audio {
					// continue writing to the same audio
					if old, ok := p.buffers[id]; ok && b.Writable && sameData(old.Data, b.Data) {
						m.Audio[id] = old
					}
				}
				p.buffers = m.Audio
				p.writable = false
				for _, b := range p.buffers {
					p.writable = p.writable || b.Writable
				}
				if !p.writable {
					clear(p.status.BufferFills[:])
				}
				if s, ok := p.synth.(sointu.BufferSetter); ok {
					s.SetBuffers(p.buffers)
				}
			case Loop:
				p.loop = m
			case IsPlayingMsg:
				p.playing = bool(m.bool)
				if !p.playing {
					for i := range p.song.Score.Tracks {
						p.processNoteEvent(NoteEvent{Channel: i, IsTrack: true, Source: p})
					}
				} else {
					TrySend(p.broker.ToModel, MsgToModel{Reset: true})
				}
			case BPMMsg:
				p.song.BPM = m.int
				p.compileOrUpdateSynth()
			case RowsPerBeatMsg:
				p.song.RowsPerBeat = m.int
				p.compileOrUpdateSynth()
			case StartPlayMsg:
				p.playing = true
				p.status.SongPos = m.SongPos
				p.status.SongPos.PatternRow--
				p.rowtime = math.MaxInt
				for i, t := range p.song.Score.Tracks {
					if !t.Effect {
						// when starting to play from another position, release only non-effect tracks
						p.processNoteEvent(NoteEvent{Channel: i, IsTrack: true, Source: p})
					}
				}
				TrySend(p.broker.ToModel, MsgToModel{Reset: true})
			case *NoteEvent:
				p.events = append(p.events, *m)
			case *MIDIMessage:
				if m.Data[0] >= 0x80 && m.Data[0] <= 0x9F {
					chn := m.channel() + 1
					note := m.Data[1]
					velocity := m.Data[2]
					on := m.Data[0] >= 0x90
					cb := func(i int, v byte) {
						if i < 0 || i >= len(p.song.Patch) {
							return
						}
						instr := p.song.Patch[i]
						if instr.MIDI.IgnoreNoteOff && !on {
							return // instruments configured to ignore note offs never release
						}
						n := byte(min(max(int(v)+instr.MIDI.Transpose, 2), 255)) // 0 and 1 have special meaning
						if instr.MIDI.NoRetrigger && on && i < len(p.prevVal) && p.prevVal[i] == n {
							return // the instrument is configured to respond only to changes in values and there was no change
						}
						p.events = append(p.events, NoteEvent{Timestamp: m.Timestamp, Channel: i, Note: n, On: on, Source: m.Source})
						for len(p.prevVal) <= i {
							p.prevVal = append(p.prevVal, 0)
						}
						p.prevVal[i] = n
					}
					p.midiAssigns.forEach(chn, false, note, cb)    // trigger instruments that are configured to respond to this midi channel's note events
					p.midiAssigns.forEach(chn, true, velocity, cb) // trigger instruments that are configured to respond to this midi channel's velocity events
				}
			case midiRouter:
				p.midiRouter = m
			case RecordingMsg:
				if m.bool {
					p.recording = Recording{State: RecordingWaitingForNote}
					p.prevVal = p.prevVal[:0] // reset prevVal, so that instruments configured to respond only to changes in values would trigger correctly in the new recording
				} else {
					if p.recording.State == RecordingStarted && len(p.recording.Events) > 0 {
						p.recording.Finish(p.frame, p.frameDeltas)
						p.recording.BPM, _ = context.BPM()
						p.send(p.recording)
					}
					p.recording = Recording{} // reset recording
				}
			case sointu.Synther:
				p.synther = m
				p.destroySynth()
				p.compileOrUpdateSynth()
			default:
				// ignore unknown messages
			}
		default:
			break loop
		}
	}
}

func (l NoteEventList) adjustTimes(frameDeltas map[any]int64, minFrame, maxFrame int64) {
	// add new sources to the map
	for _, ev := range l {
		if _, ok := frameDeltas[ev.Source]; !ok {
			frameDeltas[ev.Source] = 0 // doesn't matter, we will adjust it immediately after this
		}
	}
	// for each source, calculate the min and max of the frame
	for source, delta := range frameDeltas {
		var srcMinFrame int64 = math.MaxInt64
		var srcMaxFrame int64 = math.MinInt64
		for _, ev := range l {
			if ev.Source != source {
				continue
			}
			if ev.Timestamp < srcMinFrame {
				srcMinFrame = ev.Timestamp
			}
			if ev.Timestamp > srcMaxFrame {
				srcMaxFrame = ev.Timestamp
			}
		}
		if srcMinFrame == math.MaxInt64 || srcMaxFrame == math.MinInt64 {
			continue // no events for this source in this processing block
		}
		// "left" is the difference between the left edge of the source's events
		// and the left edge of the player clock, calculated using the current frameDelta
		left := minFrame - srcMinFrame - delta
		right := maxFrame - srcMaxFrame - delta
		// we try to adjust the frameDelta so that the source's events are
		// within the processing block
		positiveAdjust := min(max(left, 0), max(right, 0)) // always a positive value
		negativeAdjust := max(min(left, 0), min(right, 0)) // always a negative value
		frameDeltas[source] += positiveAdjust + negativeAdjust
	}
	for i, ev := range l {
		l[i].playerTimestamp = ev.Timestamp + frameDeltas[ev.Source]
	}
	// the events should have been sorted already within each source, but they
	// are not necessarily interleaved correctly, so we sort them now
	slices.SortFunc(l, func(a, b NoteEvent) int {
		return cmp.Compare(a.playerTimestamp, b.playerTimestamp)
	})
}

func (p *Player) SendAlert(name, message string, priority AlertPriority) {
	p.send(Alert{
		Name:     name,
		Priority: priority,
		Message:  message,
		Duration: defaultAlertDuration,
	})
}

func (p *Player) compileOrUpdateSynth() {
	if p.song.BPM <= 0 {
		return // bpm not set yet
	}
	if p.synth != nil {
		err := p.synth.Update(p.song.Patch, p.song.BPM)
		if err != nil {
			p.destroySynth()
			p.SendAlert("PlayerCrash", fmt.Sprintf("synth.Update: %v", err), Error)
			return
		}
	} else {
		var err error
		p.synth, err = p.synther.Synth(p.song.Patch, p.song.BPM)
		if err != nil {
			p.destroySynth()
			p.SendAlert("PlayerCrash", fmt.Sprintf("synther.Synth: %v", err), Error)
			return
		}
		if s, ok := p.synth.(sointu.BufferSetter); ok {
			s.SetBuffers(p.buffers)
		}
	}
	voice := 0
	for _, instr := range p.song.Patch {
		if instr.Mute {
			for j := 0; j < instr.NumVoices; j++ {
				p.synth.Release(voice + j)
			}
		}
		voice += instr.NumVoices
	}
	p.midiAssigns.update(p.song.Patch)
}

// HostBPMMsg tells the model the tempo of the host (e.g. a DAW), rounded to
// whole BPM, which the song follows.
type HostBPMMsg int

// followHostTempo makes the song follow the tempo of the host, when the host
// has one and its tempo changes: the player changes tempo right away, and tells
// the model to change the song. The sync goes one way only: the song's tempo
// can still be edited, and it stays until the host's tempo changes again.
func (p *Player) followHostTempo(context PlayerProcessContext) {
	bpm, ok := context.BPM()
	if !ok || bpm <= 0 {
		return
	}
	b := min(max(int(math.Round(bpm)), 1), 999)
	if b == p.hostBPM {
		return
	}
	p.hostBPM = b
	if b != p.song.BPM {
		p.song.BPM = b
		p.compileOrUpdateSynth()
	}
	p.send(HostBPMMsg(b))
}

// MaxBufferFills is the number of writable buffers whose fill the player
// reports, and MaxPlayheads the number of playheads.
const (
	MaxBufferFills = 8
	MaxPlayheads   = 64
)

// updatePlayheads reports where bufread units are playing.
func (p *Player) updatePlayheads() {
	r, ok := p.synth.(sointu.PlayheadReporter)
	if !ok {
		p.status.NumPlayheads = 0
		return
	}
	p.playheads = r.Playheads(p.playheads[:0])
	p.status.NumPlayheads = copy(p.status.Playheads[:], p.playheads)
}

// SpectrumSource is where a spectrum is taken: a spectrum buffer, or a
// spectral unit, right after it processed its buffer. One of them is 0.
type SpectrumSource struct {
	Buffer, Unit int
}

// SpectrumWatchMsg tells the player the spectra it should report with
// SpectrumMsg.
type SpectrumWatchMsg []SpectrumSource

// SpectrumMsg is the latest spectrum of a source: the magnitudes of bins 0 to
// Size/2 of each channel.
type SpectrumMsg struct {
	Source     SpectrumSource
	Size       int
	Magnitudes []float32
}

// spectrumReportFrames is how often the player reports the watched spectrum.
const spectrumReportFrames = 2205

// reportSpectrum sends the spectra of the watched spectrum buffers to the
// model every spectrumReportFrames frames.
func (p *Player) reportSpectrum(frames int) {
	if len(p.spectra) == 0 {
		return
	}
	if p.sinceSpectrum += frames; p.sinceSpectrum < spectrumReportFrames {
		return
	}
	p.sinceSpectrum = 0
	for _, src := range p.spectra {
		var mags []float32
		n := 0
		if r, ok := p.synth.(sointu.UnitSpectrumReporter); ok && src.Unit != 0 {
			mags, n = r.UnitSpectrum(src.Unit, nil)
		} else if r, ok := p.synth.(sointu.SpectrumReporter); ok && src.Buffer != 0 {
			mags, n = r.Spectrum(src.Buffer, nil)
		}
		if n > 0 {
			p.send(SpectrumMsg{Source: src, Size: n, Magnitudes: mags})
		}
	}
}

// updateBufferFills reports how much has been written to the writable
// buffers.
func (p *Player) updateBufferFills() {
	clear(p.status.BufferFills[:])
	w, ok := p.synth.(sointu.BufferWriter)
	if !ok {
		return
	}
	i := 0
	for id, b := range w.WrittenBuffers() {
		if i == MaxBufferFills {
			break
		}
		p.status.BufferFills[i] = BufferFill{ID: id, Head: b.Head, Filled: b.Filled}
		i++
	}
}

// all sendTargets from player are always non-blocking, to ensure that the player thread cannot end up in a dead-lock
func (p *Player) send(message interface{}) {
	TrySend(p.broker.ToModel, MsgToModel{HasPanicPlayerStatus: true, Panic: p.synth == nil, PlayerStatus: p.status, Data: message})
}

func (p *Player) processNoteEvent(ev NoteEvent) {
	if p.synth == nil {
		return
	}
	// release previous voice
	for i := range p.voices {
		if p.voices[i].sustain &&
			p.voices[i].triggerEvent.Source == ev.Source &&
			p.voices[i].triggerEvent.Channel == ev.Channel &&
			p.voices[i].triggerEvent.IsTrack == ev.IsTrack &&
			(ev.IsTrack || (p.voices[i].triggerEvent.Note == ev.Note)) { // tracks don't match the note number when triggering new event, but instrument events do
			p.voices[i].sustain = false
			p.voices[i].samplesSinceEvent = 0
			p.synth.Release(i)
		}
	}
	if !ev.On {
		return
	}
	var voiceStart, voiceEnd int
	if ev.IsTrack {
		if ev.Channel < 0 || ev.Channel >= len(p.song.Score.Tracks) {
			return
		}
		voiceStart = p.song.Score.FirstVoiceForTrack(ev.Channel)
		voiceEnd = voiceStart + p.song.Score.Tracks[ev.Channel].NumVoices
	} else {
		if p.song.Patch == nil || ev.Channel < 0 || ev.Channel >= len(p.song.Patch) {
			return
		}
		voiceStart = p.song.Patch.FirstVoiceForInstrument(ev.Channel)
		voiceEnd = voiceStart + p.song.Patch[ev.Channel].NumVoices
	}
	var age int = 0
	oldestReleased := false
	oldestVoice := 0
	for i := voiceStart; i < voiceEnd; i++ {
		// find a suitable voice to trigger. if the voice has been released,
		// then we prefer to trigger that over a voice that is still playing. in
		// case two voices are both playing or or both are released, we prefer
		// the older one
		if (!p.voices[i].sustain && !oldestReleased) ||
			(!p.voices[i].sustain == oldestReleased && p.voices[i].samplesSinceEvent >= age) {
			oldestVoice = i
			oldestReleased = !p.voices[i].sustain
			age = p.voices[i].samplesSinceEvent
		}
	}
	instrIndex, err := p.song.Patch.InstrumentForVoice(oldestVoice)
	if err != nil || p.song.Patch[instrIndex].Mute {
		return
	}
	p.voices[oldestVoice] = voice{triggerEvent: ev, sustain: true, samplesSinceEvent: 0}
	p.status.VoiceLevels[oldestVoice] = 1.0
	p.synth.Trigger(oldestVoice, ev.Note)
	TrySend(p.broker.ToModel, MsgToModel{TriggerChannel: instrIndex + 1})
}
