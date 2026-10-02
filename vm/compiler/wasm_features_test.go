package compiler_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
	"github.com/vsariola/sointu/vm/compiler"
)

// featureCase is a song for the parts of units that the wasm player only
// has when the song uses them, with the code that the player has and does not
// have for it.
type featureCase struct {
	name     string
	song     sointu.Song
	has, not []string
}

// fu returns a unit of the type with its default parameters, and those
// given. The parameter "id" is its ID.
func fu(typ string, params sointu.ParamMap) sointu.Unit {
	u := sointu.MakeUnit(typ)
	for k, v := range params {
		if k == "id" {
			u.ID = v
			continue
		}
		u.Parameters[k] = v
	}
	return u
}

// fmod returns units that send a constant to a port of the unit with the ID.
func fmod(target, port, amount int) []sointu.Unit {
	return []sointu.Unit{
		fu("loadval", sointu.ParamMap{"value": 96}),
		fu("send", sointu.ParamMap{"amount": amount, "target": target, "port": port, "sendpop": 1}),
	}
}

// fnoise and fsaw are sources for the songs: bursts of noise and a saw note.
func fsource(typ string, stereo int) []sointu.Unit {
	source := fu("noise", sointu.ParamMap{"stereo": stereo, "gain": 128})
	if typ == "saw" {
		source = fu("oscillator", sointu.ParamMap{"stereo": stereo, "type": sointu.Trisaw, "color": 128, "detune": 70, "gain": 128})
	}
	return []sointu.Unit{
		fu("envelope", sointu.ParamMap{"stereo": stereo, "attack": 32, "decay": 64, "sustain": 64, "release": 64}),
		source,
		fu("mulp", sointu.ParamMap{"stereo": stereo}),
	}
}

// fsong returns a song of instruments, each with the voices given and a
// track of its own playing a few notes.
func fsong(voices []int, instruments ...[]sointu.Unit) sointu.Song {
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 16, Length: 1}}
	patterns := []sointu.Pattern{
		{60, 1, 1, 0, 67, 1, 1, 1, 55, 1, 0, 1, 72, 1, 1, 0},
		{48, 1, 1, 1, 1, 1, 0, 1, 52, 1, 1, 1, 1, 1, 1, 0},
	}
	for i, units := range instruments {
		n := 1
		if i < len(voices) {
			n = voices[i]
		}
		song.Patch = append(song.Patch, sointu.Instrument{NumVoices: n, Units: units})
		song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: n, Order: sointu.Order{0}, Patterns: []sointu.Pattern{patterns[i%len(patterns)]}})
	}
	return song
}

func cat(parts ...[]sointu.Unit) (ret []sointu.Unit) {
	for _, p := range parts {
		ret = append(ret, p...)
	}
	return
}

// runFeatureCases checks that the wasm player of each song has the code the
// song needs and not the code it does not need, and that it renders the song
// exactly like the Go synth, whose bytecode always has every operand.
func runFeatureCases(t *testing.T, cases []featureCase) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			com, err := compiler.New("", "wasm", false, false)
			if err != nil {
				t.Fatal(err)
			}
			files, _, err := com.Song(&c.song)
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
			want, err := sointu.Play(vm.GoSynther{}, c.song, nil)
			if err != nil {
				t.Fatalf("Go synth failed: %v", err)
			}
			compareWasmToGo(t, want, renderWasm(t, node, wat2wasm, c.song, nil, nil, nil))
		})
	}
}

// without returns the strings of all that are not in used.
func without(all []string, used ...string) (ret []string) {
	for _, s := range all {
		found := false
		for _, u := range used {
			found = found || s == u
		}
		if !found {
			ret = append(ret, s)
		}
	}
	return
}

// the code of the parts of the mc units, as it is in the player
const (
	mcVoice       = "(func $mcVoice"
	mcAdd         = ";; add\n"
	mcStereoLanes = "(f32x4.replace_lane 3 (f32x4.replace_lane 1"
	mcGainFunc    = "(func $mcGain"
	mcSpreadGain  = ";; the gain of the spread"
	mcSumGain     = ";; the gain of the sum"
	mcSumWidth    = "(f32.const 0.125))\n                (f32.mul (call $input"
	mcMixType     = ";; the type of the mix"
	mcHadamard    = "(func $mcHadamard4"
	mcHouseholder = ";; Householder"
	mcShuffle     = "(f32.load offset=32 (i32.add (local.get $k) (local.get $c)))"
	mcFeedback    = "(local.set $f (f32x4.splat"
	mcFilterType  = ";; the type of the filter"
	mcHighpass    = ";; high-pass"
	mcDelayFlags  = ";; note tracking and allpass"
	mcDelayMod    = ";; the modulation"
	mcDelayClamp  = ";; a length that is neither"
	mcTracking    = "(local.set $nt (f32x4.splat"
	mcAllpass     = "(f32x4.mul (local.get $g) (local.get $y))"
)

var mcAllParts = []string{mcVoice, mcAdd, mcStereoLanes, mcGainFunc, mcSpreadGain, mcSumGain, mcSumWidth, mcMixType, mcHadamard, mcHouseholder, mcShuffle, mcFeedback, mcFilterType, mcHighpass, mcDelayFlags, mcDelayMod, mcDelayClamp, mcTracking, mcAllpass}

// mcChain is a song of one instrument: a source into mcspread, the units, and
// mcsum to the output.
func mcChain(stereo int, units ...sointu.Unit) []sointu.Unit {
	bus := func(u sointu.Unit) sointu.Unit {
		u.Parameters["bus"] = 1
		return u
	}
	ret := append(fsource("noise", stereo), bus(fu("mcspread", sointu.ParamMap{"stereo": stereo})))
	for _, u := range units {
		if len(sointu.BusParams(u.Type)) > 0 {
			u = bus(u)
		}
		ret = append(ret, u)
	}
	return append(ret, bus(fu("mcsum", sointu.ParamMap{"stereo": stereo})), fu("out", sointu.ParamMap{"stereo": stereo, "gain": 128}))
}

func mcFeatureCases() []featureCase {
	P := sointu.ParamMap{}
	type M = sointu.ParamMap
	delay := fu("mcdelay", M{"size": 300})
	one := func(name string, has []string, units ...sointu.Unit) featureCase {
		return featureCase{name: name, song: fsong(nil, mcChain(0, units...)), has: has, not: without(mcAllParts, has...)}
	}
	loop := func(feedback sointu.Unit) []sointu.Unit {
		return []sointu.Unit{feedback, fu("mcdelay", M{"size": 300, "decay": 40}), fu("mcmix", M{"type": sointu.MCMixHouseholder}), fu("mcloopend", P)}
	}
	return []featureCase{
		one("plain", nil, delay),
		{name: "stereo", song: fsong(nil, mcChain(1, delay)), has: []string{mcStereoLanes}, not: without(mcAllParts, mcStereoLanes)},
		{name: "two voices", song: fsong([]int{2}, mcChain(0, delay)), has: []string{mcVoice}, not: without(mcAllParts, mcVoice)},
		{name: "two voices next to one", song: fsong([]int{1, 2}, mcChain(0, delay), append(fsource("saw", 0), fu("out", M{"stereo": 0, "gain": 64}))), not: mcAllParts},
		one("add", []string{mcAdd}, delay, fu("loadval", M{"value": 80}), fu("mcspread", M{"add": 1})),
		{name: "spread gain", song: fsong(nil, cat(fsource("noise", 0), []sointu.Unit{fu("mcspread", M{"bus": 1, "gain": 50}), fu("mcsum", M{"bus": 1, "stereo": 0}), fu("out", M{"stereo": 0, "gain": 128})})), has: []string{mcSpreadGain, mcGainFunc}, not: without(mcAllParts, mcSpreadGain, mcGainFunc)},
		{name: "spread gain modulated", song: fsong(nil, cat(fmod(9, 0, 80), fsource("noise", 0), []sointu.Unit{fu("mcspread", M{"bus": 1, "id": 9}), fu("mcsum", M{"bus": 1, "stereo": 0}), fu("out", M{"stereo": 0, "gain": 128})})), has: []string{mcSpreadGain, mcGainFunc}, not: without(mcAllParts, mcSpreadGain, mcGainFunc)},
		{name: "sum gain and width", song: fsong(nil, cat(fsource("noise", 1), []sointu.Unit{fu("mcspread", M{"bus": 1, "stereo": 1}), fu("mcsum", M{"bus": 1, "gain": 70, "width": 100}), fu("out", M{"stereo": 1, "gain": 128})})), has: []string{mcSumGain, mcGainFunc, mcSumWidth, mcStereoLanes}, not: without(mcAllParts, mcSumGain, mcGainFunc, mcSumWidth, mcStereoLanes)},
		{name: "sum width", song: fsong(nil, cat(fsource("noise", 1), []sointu.Unit{fu("mcspread", M{"bus": 1, "stereo": 1}), fu("mcsum", M{"bus": 1, "width": 20}), fu("out", M{"stereo": 1, "gain": 128})})), has: []string{mcSumWidth, mcStereoLanes}, not: without(mcAllParts, mcSumWidth, mcStereoLanes)},
		{name: "sum width modulated", song: fsong(nil, cat(fmod(9, 1, 80), fsource("noise", 1), []sointu.Unit{fu("mcspread", M{"bus": 1, "stereo": 1}), fu("mcsum", M{"bus": 1, "id": 9}), fu("out", M{"stereo": 1, "gain": 128})})), has: []string{mcSumWidth, mcStereoLanes}, not: without(mcAllParts, mcSumWidth, mcStereoLanes)},
		one("hadamard", []string{mcHadamard}, delay, fu("mcmix", M{"type": sointu.MCMixHadamard})),
		one("householder", []string{mcHouseholder}, delay, fu("mcmix", M{"type": sointu.MCMixHouseholder})),
		one("shuffle", []string{mcShuffle}, delay, fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 3})),
		one("two shuffles", []string{mcShuffle}, fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 3}), delay, fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 5})),
		one("hadamard and shuffle", []string{mcHadamard, mcShuffle, mcMixType}, delay, fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 3}), fu("mcmix", M{"type": sointu.MCMixHadamard})),
		one("householder and shuffle", []string{mcHouseholder, mcShuffle, mcMixType}, delay, fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 3}), fu("mcmix", M{"type": sointu.MCMixHouseholder})),
		one("hadamard and householder", []string{mcHadamard, mcHouseholder, mcMixType}, delay, fu("mcmix", M{"type": sointu.MCMixHouseholder}), fu("mcmix", M{"type": sointu.MCMixHadamard})),
		one("all mixes", []string{mcHadamard, mcHouseholder, mcShuffle, mcMixType}, delay, fu("mcmix", M{"type": sointu.MCMixHouseholder}), fu("mcmix", M{"type": sointu.MCMixShuffle, "seed": 1}), fu("mcmix", M{"type": sointu.MCMixHadamard})),
		one("low-pass", nil, fu("mcfilter", M{"frequency": 80})),
		one("high-pass", []string{mcHighpass}, fu("mcfilter", M{"frequency": 60, "type": 1})),
		one("low-pass and high-pass", []string{mcHighpass, mcFilterType}, fu("mcfilter", M{"frequency": 100}), fu("mcfilter", M{"frequency": 40, "type": 1})),
		one("loop", []string{mcHouseholder}, loop(fu("mcloop", P))...),
		one("loop with feedback", []string{mcHouseholder, mcFeedback}, loop(fu("mcloop", M{"feedback": 100}))...),
		{name: "loop with feedback modulated", song: fsong(nil, cat(fmod(9, 0, 40), mcChain(0, loop(fu("mcloop", M{"id": 9}))...))), has: []string{mcHouseholder, mcFeedback}, not: without(mcAllParts, mcHouseholder, mcFeedback)},
		one("delay modulated", []string{mcDelayMod, mcDelayClamp}, fu("mcdelay", M{"size": 300, "moddepth": 40, "modrate": 90})),
		{name: "delay depth modulated", song: fsong(nil, cat(fmod(9, 0, 100), mcChain(0, fu("mcdelay", M{"size": 300, "id": 9})))), has: []string{mcDelayMod, mcDelayClamp}, not: without(mcAllParts, mcDelayMod, mcDelayClamp)},
		{name: "delay rate modulated", song: fsong(nil, cat(fmod(9, 1, 100), mcChain(0, fu("mcdelay", M{"size": 300, "id": 9})))), has: []string{mcDelayMod, mcDelayClamp}, not: without(mcAllParts, mcDelayMod, mcDelayClamp)},
		one("delay tracking the note", []string{mcDelayFlags, mcTracking, mcDelayClamp}, fu("mcdelay", M{"size": 76, "notetracking": 1})),
		one("allpass", []string{mcDelayFlags, mcAllpass}, fu("mcdelay", M{"size": 80, "allpass": 1})),
		one("delays of every kind", []string{mcDelayFlags, mcAllpass, mcTracking, mcDelayClamp, mcDelayMod},
			delay, fu("mcdelay", M{"size": 80, "allpass": 1}), fu("mcdelay", M{"size": 76, "notetracking": 1}), fu("mcdelay", M{"size": 200, "moddepth": 60}), fu("mcdelay", M{"size": 50, "allpass": 1, "notetracking": 1, "decay": 60, "hfdecay": 30, "lfdecay": 90})),
		{name: "everything", song: mcTestSong(), has: mcAllParts},
	}
}

// TestMCFeaturesWasmMatchGoSynth checks the parts of the mc units that the
// wasm player only has when the song uses them.
func TestMCFeaturesWasmMatchGoSynth(t *testing.T) {
	runFeatureCases(t, mcFeatureCases())
}

// the code of the parts of spawn and window, as it is in the player
const (
	spawnFlags      = ";; the flags of the spawn"
	spawnEdge       = ";; the input of the edge mode"
	spawnModes      = "(i32.const 1)) (then ;; edge mode"
	spawnSync       = ";; spawns per beat"
	spawnRate       = ";; spawns per second"
	spawnRates      = ";; sync: spawns per beat"
	spawnArgs       = "(func $spawnDropArgs"
	spawnSteal      = "(local.get $busy)) ;; steal"
	spawnStealFlag  = "(i32.const 64)) (i32.const 0))"
	spawnTracking   = ";; note tracking"
	spawnTrackFlag  = "(i32.const 2)) ;; note tracking"
	spawnNoTracking = "(local.set $n (f32.const 60))"
	spawnTranspose  = ";; transpose"
	spawnLength     = ";; release the notes"
	spawnNoTarget   = "(i32.ne (local.get $count) (i32.const 0))"
	lengthFrames    = "(func $lengthFrames"
	windowNote      = ";; the length of the spawned note"
	windowOwn       = "(local.set $frames (f32.max (call $lengthFrames"
	windowClamp     = "(f32.const 0)) (f32.const 1))\n        (f32.const 0.5)"
)

var spawnAllParts = []string{spawnFlags, spawnEdge, spawnModes, spawnSync, spawnRate, spawnRates, spawnArgs, spawnSteal, spawnStealFlag, spawnTracking, spawnTrackFlag, spawnNoTracking, spawnTranspose, spawnLength, spawnNoTarget, lengthFrames, windowNote, windowOwn, windowClamp}

// spawnSong is a song of an instrument of 3 voices that is only spawned,
// and an instrument for each spawn unit, which gets a constant for each
// argument and, in edge mode, noise as its input. The spawned voices put
// their envelope times the first argument and the window on the left and
// their note on the right. mods are sends of noise to ports of the first
// spawn unit, windowMod one to a port of the window, if not -1. Like
// TestSpawnWasmMatchesGoSynth, it has no oscillators.
func spawnSong(window sointu.ParamMap, mods map[int]int, windowMod int, spawners ...sointu.ParamMap) sointu.Song {
	type M = sointu.ParamMap
	target := []sointu.Unit{fu("envelope", M{"attack": 32, "decay": 64, "sustain": 64, "release": 64})}
	args := 0
	for _, s := range spawners {
		args = max(args, s["args"])
	}
	if args > 0 {
		target = append(target, fu("arg", M{"index": 0}), fu("mulp", M{"stereo": 0}))
	}
	if window != nil {
		if windowMod >= 0 {
			target = append(target, fu("noise", M{"gain": 128}), fu("send", M{"amount": 96, "target": 50, "port": windowMod, "sendpop": 1}))
		}
		w := fu("window", window)
		w.ID = 50
		target = append(target, w, fu("mulp", M{"stereo": 0}))
	}
	target = append(target, fu("loadnote", nil), fu("out", M{"stereo": 1, "gain": 128}))
	song := sointu.Song{BPM: 120, RowsPerBeat: 4, Score: sointu.Score{RowsPerPattern: 16, Length: 1}}
	song.Patch = append(song.Patch, sointu.Instrument{NumVoices: 3, Units: target})
	song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: 3, Order: sointu.Order{0}, Patterns: []sointu.Pattern{{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}})
	patterns := []sointu.Pattern{{60, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 72, 1, 1, 1}, {1, 1, 55, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1}}
	for i, s := range spawners {
		var units []sointu.Unit
		if i == 0 {
			for port, amount := range mods {
				units = append(units, fu("noise", M{"gain": 128}), fu("send", M{"amount": amount, "target": 60, "port": port, "sendpop": 1}))
			}
		}
		for a := range s["args"] {
			units = append(units, fu("loadval", M{"value": 128 - 20*a - 10*i}))
		}
		if s["mode"] == sointu.SpawnModeEdge {
			units = append(units, fu("noise", M{"gain": 128}))
		}
		u := fu("spawn", M{"instrument": 1, "rate": 88 + 6*i})
		for k, v := range s {
			u.Parameters[k] = v
		}
		u.ID = 60 + i
		song.Patch = append(song.Patch, sointu.Instrument{NumVoices: 1, Units: append(units, u)})
		song.Score.Tracks = append(song.Score.Tracks, sointu.Track{NumVoices: 1, Order: sointu.Order{0}, Patterns: []sointu.Pattern{patterns[i%2]}})
	}
	return song
}

func spawnFeatureCases() []featureCase {
	type M = sointu.ParamMap
	c := func(name string, has []string, song sointu.Song) featureCase {
		return featureCase{name: name, song: song, has: has, not: without(spawnAllParts, has...)}
	}
	edge, sync := M{"mode": sointu.SpawnModeEdge}, M{"mode": sointu.SpawnModeSync, "rate": 80}
	return []featureCase{
		c("rate", []string{spawnRate, spawnTracking}, spawnSong(nil, nil, -1, M{})),
		c("two at a rate", []string{spawnRate, spawnTracking}, spawnSong(nil, nil, -1, M{}, M{})),
		c("sync", []string{spawnSync, spawnTracking}, spawnSong(nil, nil, -1, sync)),
		c("edge", []string{spawnEdge, spawnTracking}, spawnSong(nil, nil, -1, edge)),
		c("rate and sync", []string{spawnFlags, spawnRate, spawnSync, spawnRates, spawnTracking}, spawnSong(nil, nil, -1, M{}, sync)),
		c("rate and edge", []string{spawnFlags, spawnRate, spawnEdge, spawnModes, spawnTracking}, spawnSong(nil, nil, -1, M{}, edge)),
		c("sync and edge", []string{spawnFlags, spawnSync, spawnEdge, spawnModes, spawnTracking}, spawnSong(nil, nil, -1, edge, sync)),
		c("all modes", []string{spawnFlags, spawnRate, spawnSync, spawnRates, spawnEdge, spawnModes, spawnTracking}, spawnSong(nil, nil, -1, M{}, edge, sync)),
		c("no tracking", []string{spawnRate, spawnNoTracking}, spawnSong(nil, nil, -1, M{"notetracking": 0})),
		c("tracking and not", []string{spawnFlags, spawnRate, spawnTracking, spawnTrackFlag}, spawnSong(nil, nil, -1, M{"notetracking": 0, "transpose": 64}, M{})),
		c("steal", []string{spawnRate, spawnTracking, spawnSteal}, spawnSong(nil, nil, -1, M{"steal": 1, "rate": 100})),
		c("steal and not", []string{spawnFlags, spawnRate, spawnTracking, spawnSteal, spawnStealFlag}, spawnSong(nil, nil, -1, M{"steal": 1, "rate": 100}, M{"rate": 100})),
		c("arguments", []string{spawnRate, spawnTracking, spawnArgs}, spawnSong(nil, nil, -1, M{"args": 2})),
		c("arguments and none", []string{spawnFlags, spawnRate, spawnTracking, spawnArgs}, spawnSong(nil, nil, -1, M{"args": 1}, M{})),
		c("one and three arguments", []string{spawnFlags, spawnRate, spawnTracking, spawnArgs}, spawnSong(nil, nil, -1, M{"args": 1}, M{"args": 3})),
		c("edge with arguments", []string{spawnEdge, spawnTracking, spawnArgs}, spawnSong(nil, nil, -1, M{"mode": sointu.SpawnModeEdge, "args": 1})),
		c("transpose", []string{spawnRate, spawnTracking, spawnTranspose}, spawnSong(nil, nil, -1, M{"transpose": 71})),
		c("transpose modulated", []string{spawnRate, spawnTracking, spawnTranspose}, spawnSong(nil, map[int]int{1: 80}, -1, M{})),
		c("length", []string{spawnRate, spawnTracking, spawnLength, lengthFrames}, spawnSong(nil, nil, -1, M{"length": 40})),
		c("length modulated", []string{spawnRate, spawnTracking, spawnLength, lengthFrames}, spawnSong(nil, map[int]int{2: 90}, -1, M{})),
		c("no target", []string{spawnRate, spawnTracking, spawnNoTarget}, spawnSong(nil, nil, -1, M{}, M{"instrument": 0})),
		c("no target with length and arguments", []string{spawnFlags, spawnRate, spawnTracking, spawnNoTarget, spawnLength, lengthFrames, spawnArgs}, spawnSong(nil, nil, -1, M{"length": 40}, M{"instrument": 0, "args": 2})),
		c("window of the note", []string{spawnRate, spawnTracking, spawnLength, lengthFrames, windowNote}, spawnSong(M{}, nil, -1, M{"length": 50})),
		c("window of the note without a length", []string{spawnRate, spawnTracking, windowNote}, spawnSong(M{}, nil, -1, M{})),
		c("window with a length", []string{spawnRate, spawnTracking, lengthFrames, windowOwn}, spawnSong(M{"length": 50, "shape": 60}, nil, -1, M{})),
		c("window with a modulated shape", []string{spawnRate, spawnTracking, lengthFrames, windowOwn, windowClamp}, spawnSong(M{"length": 50}, nil, 1, M{})),
		c("window with a modulated length", []string{spawnRate, spawnTracking, lengthFrames, windowNote, windowOwn}, spawnSong(M{"length": 40}, nil, 0, M{})),
		c("everything", without(spawnAllParts, spawnNoTracking, windowOwn), spawnSong(M{"length": 0, "shape": 80}, map[int]int{0: 70, 1: 80, 2: 75}, 1,
			M{"args": 2, "steal": 1, "length": 30}, M{"mode": sointu.SpawnModeEdge, "notetracking": 0, "args": 1}, M{"mode": sointu.SpawnModeSync, "rate": 90, "instrument": 9},
			M{"mode": sointu.SpawnModeSync, "rate": 96, "transpose": 59})),
	}
}

// TestSpawnFeaturesWasmMatchGoSynth checks the parts of spawn and window
// that the wasm player only has when the song uses them.
func TestSpawnFeaturesWasmMatchGoSynth(t *testing.T) {
	runFeatureCases(t, spawnFeatureCases())
}
