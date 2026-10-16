package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// bufFeatureCase is a song for the parts of bufread and bufwrite that the
// wasm player only has when the song uses them: bufwrite units record noise
// into buffers and bufread units play them.
type bufFeatureCase struct {
	name     string
	stereo   bool              // buffer 1 is stereo
	writers  []sointu.ParamMap // changes to the bufwrite unit of each writer instrument
	voices   int               // voices of the first writer
	readers  []sointu.ParamMap // changes to the bufread unit of each reader instrument
	mods     map[int]int       // port of the first bufread -> amount of a constant sent to it
	selfTap  sointu.ParamMap   // if not nil, the first writer plays its buffer with a bufread tap of these parameters
	has, not []string          // what the player has and does not have
}

func (c bufFeatureCase) song() sointu.Song {
	channels := 1
	if c.stereo {
		channels = 2
	}
	song := sointu.Song{
		BPM:         120,
		RowsPerBeat: 4,
		Buffers: sointu.Buffers{
			{ID: 1, Name: "a", Channels: channels, Frames: 6000},
			{ID: 2, Name: "b", Channels: 1, Frames: 9000},
		},
		Score: sointu.Score{RowsPerPattern: 8, Length: 1},
	}
	add := func(instr sointu.Instrument, pattern sointu.Pattern) {
		song.Patch = append(song.Patch, instr)
		song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: instr.NumVoices, Order: sointu.Order{0}, Patterns: []sointu.Pattern{pattern}})
	}
	merge := func(base, changes sointu.ParamMap) sointu.ParamMap {
		for k, v := range changes {
			base[k] = v
		}
		return base
	}
	for i, w := range c.writers {
		p := merge(sointu.ParamMap{"stereo": 0, "feedback": 0, "buffer": 1, "oneshot": 0, "pop": 1}, w)
		units := []sointu.Unit{
			{Type: "noise", Parameters: sointu.ParamMap{"stereo": p["stereo"], "shape": 64, "gain": 128}},
			{Type: "envelope", Parameters: sointu.ParamMap{"stereo": p["stereo"], "attack": 40, "decay": 64, "sustain": 96, "release": 64, "gain": 128}},
			{Type: "mulp", Parameters: sointu.ParamMap{"stereo": p["stereo"]}},
			{Type: "bufwrite", Parameters: p},
		}
		if p["pop"] == 0 {
			units = append(units, sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": p["stereo"], "gain": 20}})
		}
		if i == 0 && c.selfTap != nil { // before the units that write: what was written before
			tap := merge(sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 0, "mode": 3}, c.selfTap)
			units = append([]sointu.Unit{
				{Type: "bufread", Parameters: tap},
				{Type: "out", Parameters: sointu.ParamMap{"stereo": 0, "gain": 100}},
			}, units...)
		}
		voices := 1
		if i == 0 && c.voices > 1 {
			voices = c.voices
		}
		add(sointu.Instrument{Name: "writer", NumVoices: voices, Units: units}, sointu.Pattern{60, 1, 1, 0, 64, 1, 1, 1})
	}
	for i, r := range c.readers {
		p := merge(sointu.ParamMap{"stereo": 0, "transpose": 64, "detune": 64, "gain": 128, "speed": 128, "buffer": 1, "note": 0}, r)
		var units []sointu.Unit
		if i == 0 {
			for port, amount := range c.mods {
				units = append(units,
					sointu.Unit{Type: "loadval", Parameters: sointu.ParamMap{"stereo": 0, "value": 90}},
					sointu.Unit{Type: "send", Parameters: sointu.ParamMap{"stereo": 0, "amount": amount, "target": 100, "port": port, "sendpop": 1}})
			}
		}
		units = append(units,
			sointu.Unit{ID: 100 + i, Type: "bufread", Parameters: p},
			sointu.Unit{Type: "out", Parameters: sointu.ParamMap{"stereo": p["stereo"], "gain": 128}})
		add(sointu.Instrument{Name: "reader", NumVoices: 1, Units: units}, sointu.Pattern{1, 60, 1, 1, 72, 1, 0, 55})
	}
	return song
}

// the code of each part, as it is in the player
const (
	bufLoop      = "(i32.const 2)) (then ;; loop"
	bufFade      = "(param $fade i32)"
	bufBackwards = "(local.get $inloop)"
	bufMod       = "$bufreadFrames"
	bufNegStart  = ";; from the newest frame\n"
	bufNegLoop   = ";; from the newest frame at the trigger"
	bufEdge      = ";; fade out near the edges"
	bufTracking  = ";; note tracking"
	bufPitch     = `(call $pow2 (f32.div (local.get $semitones)`
	bufClamp     = "(i32.sub (local.get $channels) (i32.const 1))"
	bufNoPop     = ";; no pop"
	bufOneShot   = ";; the note started: a new recording"
	bufRing      = "(i32.rem_u (local.get $head) (local.get $cap))"
	bufMix       = ";; another writer wrote this frame already"
	bufFeedback  = "(local.get $fb)"
	bufTap       = ";; a tap sounds without a note"
	bufTiming    = ";; in frames at note 0"
	bufRingFade  = ";; valid all around"
	bufPerVoice  = ";; the copy of the buffer that the voice has"
)

var bufAllParts = []string{bufLoop, bufFade, bufBackwards, bufMod, bufNegStart, bufNegLoop, bufEdge, bufTracking, bufPitch, bufClamp, bufNoPop, bufOneShot, bufMix, bufFeedback, bufTap, bufTiming, bufRingFade, bufPerVoice}

var bufFeatureCases = []bufFeatureCase{
	{name: "plain", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{}}, has: []string{bufRing}, not: bufAllParts},
	{name: "note tracking", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"note": 1}}, has: []string{bufTracking, bufPitch}},
	{name: "transpose", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"transpose": 70}}, has: []string{bufPitch}, not: []string{bufTracking}},
	{name: "detune modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{}}, mods: map[int]int{1: 80}, has: []string{bufPitch}, not: []string{bufTracking, bufMod}},
	{name: "slow", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"speed": 100}}, not: bufAllParts},
	{name: "loop", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": 500, "looplength": 700}}, has: []string{bufLoop}, not: []string{bufFade, bufBackwards, bufMod, bufNegLoop}},
	{name: "loop with fade", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": 500, "looplength": 700, "loopfade": 200}}, has: []string{bufLoop, bufFade}, not: []string{bufBackwards}},
	{name: "loop backwards", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "start": 900, "loopstart": 500, "looplength": 700, "speed": 20}}, has: []string{bufLoop, bufBackwards}, not: []string{bufFade}},
	{name: "backwards without loop", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": 3000, "speed": 20}}, not: bufAllParts},
	{name: "loop from the end", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": -1500, "looplength": 700}}, has: []string{bufLoop, bufNegLoop}, not: []string{bufNegStart}},
	{name: "start from the end", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": -1500}}, has: []string{bufNegStart}, not: []string{bufNegLoop, bufLoop}},
	{name: "start beyond the end", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": 100000}, {"start": 5990}}, not: bufAllParts},
	{name: "edge fade", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": -1500, "edgefade": 300}}, has: []string{bufEdge}},
	{name: "stereo of mono", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"stereo": 1}}, has: []string{bufClamp}},
	{name: "mono of stereo", stereo: true, writers: []sointu.ParamMap{{"stereo": 1}}, readers: []sointu.ParamMap{{}}, not: bufAllParts},
	{name: "stereo", stereo: true, writers: []sointu.ParamMap{{"stereo": 1}}, readers: []sointu.ParamMap{{"stereo": 1}}, not: bufAllParts},
	{name: "mono of both", stereo: true, writers: []sointu.ParamMap{{"stereo": 1}, {"buffer": 2}}, readers: []sointu.ParamMap{{}, {"buffer": 2}}, not: bufAllParts},
	{name: "start modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{}}, mods: map[int]int{4: 80}, has: []string{bufMod}, not: []string{bufLoop}},
	{name: "loop length modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": 500, "looplength": 700}}, mods: map[int]int{6: 70}, has: []string{bufMod, bufLoop}, not: []string{bufBackwards}},
	{name: "loop start modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": 500, "looplength": 700}}, mods: map[int]int{5: 70}, has: []string{bufMod, bufLoop, bufBackwards}},
	{name: "speed modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 1, "loopstart": 500, "looplength": 700}}, mods: map[int]int{3: 20}, has: []string{bufLoop, bufBackwards}},
	{name: "one shot", writers: []sointu.ParamMap{{"oneshot": 1}}, readers: []sointu.ParamMap{{}}, has: []string{bufOneShot}, not: []string{bufRing, bufMix}},
	{name: "one shot and ring", writers: []sointu.ParamMap{{"oneshot": 1}, {"buffer": 2}}, readers: []sointu.ParamMap{{}, {"buffer": 2}}, has: []string{bufOneShot, bufRing}, not: []string{bufMix}},
	{name: "feedback", writers: []sointu.ParamMap{{"feedback": 60}}, readers: []sointu.ParamMap{{}}, has: []string{bufFeedback}},
	{name: "feedback stereo", stereo: true, writers: []sointu.ParamMap{{"feedback": 60, "stereo": 1}}, readers: []sointu.ParamMap{{}}, has: []string{bufFeedback}},
	{name: "no pop", writers: []sointu.ParamMap{{"pop": 0}}, readers: []sointu.ParamMap{{}}, has: []string{bufNoPop}},
	{name: "two writers", writers: []sointu.ParamMap{{}, {}}, readers: []sointu.ParamMap{{}}, has: []string{bufMix}},
	{name: "two one shot writers", writers: []sointu.ParamMap{{"oneshot": 1}, {"oneshot": 1}}, readers: []sointu.ParamMap{{}}, has: []string{bufMix, bufOneShot}, not: []string{bufRing}},
	{name: "polyphonic writer", stereo: true, writers: []sointu.ParamMap{{}}, voices: 2, readers: []sointu.ParamMap{{}}, has: []string{bufMix}},
	{name: "tap", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -700}}, has: []string{bufTap, bufNegStart}, not: []string{bufTiming, bufRingFade, bufPerVoice, bufMod}},
	{name: "tap from the oldest frame", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": 2500, "speed": 10, "transpose": 80, "looplength": 50}}, has: []string{bufTap}, not: []string{bufNegStart}},
	{name: "tap and play head", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -700}, {"start": -3000, "transpose": 70}}, has: []string{bufTap}},
	{name: "tap modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -2000}}, mods: map[int]int{4: 70}, has: []string{bufTap, bufMod}},
	{name: "tap of its own writer", writers: []sointu.ParamMap{{}}, selfTap: sointu.ParamMap{"start": -1}, readers: []sointu.ParamMap{{}}, has: []string{bufTap}},
	{name: "tap with pitch timing", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -40000, "timing": 1}}, has: []string{bufTap, bufTiming}},
	{name: "tap with pitch timing, modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -40001, "timing": 1}}, mods: map[int]int{4: 67}, has: []string{bufTap, bufTiming, bufMod}},
	{name: "tap with pitch timing, transposed and detuned", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -40000, "timing": 1, "transpose": 71, "detune": 50}}, has: []string{bufTap, bufTiming}},
	{name: "tap with pitch timing, detune modulated", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -40001, "timing": 1, "detune": 70}}, mods: map[int]int{1: 80}, has: []string{bufTap, bufTiming}},
	{name: "play head with pitch timing", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": -90001, "timing": 1}}, has: []string{bufTiming}, not: []string{bufTap}},
	{name: "tap with BPM timing", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"mode": 3, "start": -6, "timing": 2}}, has: []string{bufTap}, not: []string{bufTiming}},
	{name: "loop with BPM timing", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": 1, "mode": 1, "loopstart": 2, "looplength": 3, "timing": 2}}, has: []string{bufLoop}, not: []string{bufTiming, bufTap}},
	{name: "ring crossfade, overtaken", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": 300, "speed": 100, "edgefade": 500}}, has: []string{bufRingFade, bufEdge}},
	{name: "ring crossfade, overtaking", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": -800, "transpose": 76, "edgefade": 400}}, has: []string{bufRingFade}},
	{name: "ring crossfade, backwards", writers: []sointu.ParamMap{{}}, readers: []sointu.ParamMap{{"start": -100, "speed": 20, "edgefade": 5000}}, has: []string{bufRingFade}},
	{name: "ring crossfade, stereo", stereo: true, writers: []sointu.ParamMap{{"stereo": 1}}, readers: []sointu.ParamMap{{"stereo": 1, "start": 50, "detune": 20, "note": 1, "edgefade": 900}, {"start": 100, "speed": 90, "edgefade": 100}}, has: []string{bufRingFade}},
	{name: "edge fade of a one shot", writers: []sointu.ParamMap{{"oneshot": 1}}, readers: []sointu.ParamMap{{"start": 100, "speed": 100, "edgefade": 500}}, has: []string{bufEdge}, not: []string{bufRingFade}},
	{name: "buffer per voice", writers: []sointu.ParamMap{{"pervoice": 1}}, voices: 3, selfTap: sointu.ParamMap{"start": -900}, readers: []sointu.ParamMap{{}}, has: []string{bufPerVoice, bufTap}},
	{name: "buffer per voice, stereo, and a shared one", stereo: true, writers: []sointu.ParamMap{{"pervoice": 1, "stereo": 1, "feedback": 50}, {"buffer": 2}, {"buffer": 2}}, voices: 2, selfTap: sointu.ParamMap{"start": -2000, "timing": 1}, readers: []sointu.ParamMap{{"start": -500, "edgefade": 300, "speed": 110}, {"buffer": 2, "mode": 3, "start": -4000}}, has: []string{bufPerVoice, bufTap, bufMix, bufTiming, bufRingFade}},
	{name: "plain and everything", stereo: true,
		writers: []sointu.ParamMap{{"stereo": 1, "feedback": 40, "pop": 0, "pervoice": 1}, {"buffer": 2, "oneshot": 1}, {"buffer": 2, "oneshot": 1}},
		voices:  2, selfTap: sointu.ParamMap{"start": -30000, "timing": 1},
		readers: []sointu.ParamMap{{}, {"buffer": 2}, {"stereo": 1, "buffer": 2, "note": 1, "transpose": 60, "detune": 70, "speed": 30, "start": -3000, "mode": 1, "loopstart": -2500, "looplength": 1200, "loopfade": 300, "edgefade": 200}, {"start": 200, "speed": 90, "edgefade": 700}},
		mods:    map[int]int{4: 70}, has: append([]string{bufRing}, bufAllParts...)},
}

// TestBufferFeaturesWasmMatchGoSynth checks that the wasm player renders
// songs like the Go synth whichever parts of bufread and bufwrite they use,
// and that the player has the parts the song uses and no others.
func TestBufferFeaturesWasmMatchGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, c := range bufFeatureCases {
		t.Run(c.name, func(t *testing.T) {
			song := c.song()
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			files, _, err := com.Song(&song)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range c.has {
				if !strings.Contains(files[".wat"], s) {
					t.Errorf("the player does not have %q", s)
				}
			}
			for _, s := range c.not {
				if strings.Contains(files[".wat"], s) {
					t.Errorf("the player has %q", s)
				}
			}
			want, err := sointu.Play(vm.GoSynther{}, song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
}
