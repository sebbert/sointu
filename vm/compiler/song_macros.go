package compiler

import (
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

type SongMacros struct {
	Song *sointu.Song
	// VoiceTrackBitmask has bit n set when voice n is not the last voice of
	// its track. It is only valid when WideTracks is false.
	VoiceTrackBitmask int
	// VoiceTracks has the same information as VoiceTrackBitmask, a byte for
	// each voice of the score.
	VoiceTracks []byte
	// MultiVoiceTracks is true when a track has more than one voice.
	MultiVoiceTracks bool
	// WideTracks is true when the score has more than 32 voices, so the
	// players use VoiceTracks instead of VoiceTrackBitmask.
	WideTracks bool
	// VoiceBytes is the size of the memory for the voices: 32 voices, or
	// more when the patch or the score has more.
	VoiceBytes int
	MaxSamples int
}

func NewSongMacros(s *sointu.Song) *SongMacros {
	maxSamples := s.SamplesPerRow() * s.Score.LengthInRows()
	p := SongMacros{Song: s, MaxSamples: maxSamples}
	trackVoiceNumber := 0
	for _, t := range s.Score.Tracks {
		for b := 0; b < t.NumVoices-1; b++ {
			if trackVoiceNumber < vm.MAX_VOICES_NARROW {
				p.VoiceTrackBitmask += 1 << trackVoiceNumber
			}
			p.VoiceTracks = append(p.VoiceTracks, 1)
			p.MultiVoiceTracks = true
			trackVoiceNumber++
		}
		// a track takes at least one voice in the players, even without voices
		p.VoiceTracks = append(p.VoiceTracks, 0)
		trackVoiceNumber++ // set all bits except last one
	}
	p.WideTracks = len(p.VoiceTracks) > vm.MAX_VOICES_NARROW
	p.VoiceBytes = 4096 * max(vm.MAX_VOICES_NARROW, s.Patch.NumVoices(), len(p.VoiceTracks))
	return &p
}
