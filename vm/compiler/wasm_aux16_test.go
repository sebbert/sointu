package compiler_test

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm/compiler"
)

// aux16Song uses the channels above 7: a mono instrument and a polyphonic
// one of polyVoices voices write every new pair with mono and stereo aux
// units (also across channels 7 and 8, and to a pair of the first 8), the
// voices of the polyphonic one read a channel that the instrument before and
// their earlier voices wrote, and the last instrument reads all of them, in
// stereo and in mono, each with a gain of its own. The polyphonic instrument
// modulates the last one with a send to a later voice, whose address depends
// on where the voices are. With more than 32 voices those addresses are
// three bytes.
func aux16Song(polyVoices int) sointu.Song {
	aux := func(channel, stereo, gain int) []sointu.Unit {
		return []sointu.Unit{
			fu("push", sointu.ParamMap{"stereo": stereo}),
			fu("aux", sointu.ParamMap{"stereo": stereo, "channel": channel, "gain": gain}),
		}
	}
	read := func(channel, stereo, gain int) []sointu.Unit {
		units := []sointu.Unit{fu("in", sointu.ParamMap{"stereo": stereo, "channel": channel})}
		if stereo == 0 { // the channel to both sides
			units = append(units, fu("pan", sointu.ParamMap{"stereo": 0, "panning": 20 + 6*channel}))
		}
		return append(units,
			fu("gain", sointu.ParamMap{"stereo": 1, "gain": gain}),
			fu("addp", sointu.ParamMap{"stereo": 1}))
	}
	polyGain := 128
	if polyVoices > 8 {
		polyGain = 20
	}
	mono := cat(fsource("saw", 0),
		aux(8, 0, 90), aux(11, 0, 70), aux(13, 0, 110), aux(14, 0, 60), aux(15, 0, 100),
		[]sointu.Unit{fu("out", sointu.ParamMap{"stereo": 0, "gain": 20})})
	poly := cat(fsource("noise", 1),
		[]sointu.Unit{
			// what the mono instrument and the voices before this one left in channel 11
			fu("in", sointu.ParamMap{"stereo": 0, "channel": 11}),
			fu("send", sointu.ParamMap{"stereo": 0, "amount": 80, "target": 50, "port": 0, "sendpop": 1}),
			fu("gain", sointu.ParamMap{"stereo": 1, "gain": polyGain, "id": 50}),
			fu("envelope", sointu.ParamMap{"stereo": 0, "attack": 60, "decay": 70, "sustain": 40, "release": 70}),
			fu("send", sointu.ParamMap{"stereo": 0, "amount": 96, "target": 60, "port": 0, "sendpop": 1}),
		},
		aux(8, 1, 60), aux(10, 1, 80), aux(12, 1, 100), aux(14, 1, 50), aux(7, 1, 70), aux(2, 1, 90), aux(9, 0, 40),
		[]sointu.Unit{fu("out", sointu.ParamMap{"stereo": 1, "gain": 16})})
	reader := cat(
		[]sointu.Unit{fu("loadval", sointu.ParamMap{"stereo": 1, "value": 64})}, // silence, to add to
		read(8, 1, 120), read(10, 1, 50), read(13, 0, 90), read(12, 0, 110), read(15, 0, 70), read(14, 0, 100), read(6, 1, 80), read(2, 1, 60),
		[]sointu.Unit{
			fu("filter", sointu.ParamMap{"stereo": 1, "frequency": 40, "resonance": 100, "id": 60}),
			fu("out", sointu.ParamMap{"stereo": 1, "gain": 128}),
		})
	return fsong([]int{1, polyVoices, 1}, mono, poly, reader)
}

// TestAux16GoSynthChannels checks, in the Go synth, that each of the
// channels is one of its own: a signal sent to one comes out of the in unit
// that reads it, and of no other.
func TestAux16GoSynthChannels(t *testing.T) {
	t.Parallel()
	for c := 2; c < sointu.NumChannels; c++ {
		for d := 2; d < sointu.NumChannels; d++ {
			song := fsong(nil,
				[]sointu.Unit{fu("loadval", sointu.ParamMap{"stereo": 0, "value": 128}), fu("aux", sointu.ParamMap{"stereo": 0, "channel": c, "gain": 128})},
				[]sointu.Unit{fu("in", sointu.ParamMap{"stereo": 0, "channel": d}), fu("out", sointu.ParamMap{"stereo": 0, "gain": 128})})
			song.Score.RowsPerPattern = 1
			for i := range song.Score.Tracks {
				song.Score.Tracks[i].Patterns = []sointu.Pattern{{60}}
			}
			buffer := playGo(t, song)
			if got, want := buffer[len(buffer)-1][0] != 0, c == d; got != want {
				t.Errorf("a signal sent to channel %d reaches the in unit of channel %d: %v", c, d, got)
			}
		}
	}
}

func TestAux16WasmMatchesGoSynth(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wat2wasm, err := exec.LookPath("wat2wasm")
	if err != nil {
		t.Skip("wat2wasm not found")
	}
	for _, voices := range []int{1, 3, 34} {
		t.Run(fmt.Sprintf("%d voices", voices), func(t *testing.T) {
			t.Parallel()
			song := aux16Song(voices)
			compareWasmToGo(t, playGo(t, song), renderWasm(t, node, wat2wasm, song, nil, nil, nil))
		})
	}
	t.Run("progressive", func(t *testing.T) {
		t.Parallel()
		runProgressive(t, node, wat2wasm, aux16Song(3), nil, nil, nil, nil)
		runProgressive(t, node, wat2wasm, aux16Song(3), nil, nil, nil, func(c *compiler.Compiler) { c.Output16Bit = true })
	})
	t.Run("stages", func(t *testing.T) {
		t.Parallel()
		// every voice reads what the voices before it wrote, and the last
		// clears every channel: the song can be cut before each
		if got := testStages(t, node, wat2wasm, aux16Song(3)); fmt.Sprint(got) != "[1 2 3 4]" {
			t.Errorf("cuts before voices %v", got)
		}
	})
	t.Run("module", func(t *testing.T) {
		t.Parallel()
		// -js -stages: the JavaScript module, around the same player
		song := aux16Song(3)
		_, staged := compileWasm(t, wat2wasm, song, nil, func(c *compiler.Compiler) { c.Progressive, c.Stages = true, 3 })
		_, module := compileWasm(t, wat2wasm, song, nil, func(c *compiler.Compiler) { c.JS, c.Stages = true, 3 })
		// the player of the module does not export where its audio is
		code := func(wat string) string {
			var lines []string
			for _, line := range strings.Split(wat, "\n") {
				if !strings.HasPrefix(line, ";;") && !(strings.Contains(line, "$output") && strings.Contains(line, "(export ")) {
					lines = append(lines, line)
				}
			}
			return strings.Join(lines, "\n")
		}
		if module[".js"] == "" || code(module[".wat"]) != code(staged[".wat"]) || !strings.Contains(staged[".wat"], "(export \"g\")") {
			t.Errorf("the module of a song with 16 channels: %d bytes of JavaScript, the same player: %v", len(module[".js"]), code(module[".wat"]) == code(staged[".wat"]))
		}
	})
}

// TestAux16OnlyWhenUsed checks that only songs that use a channel above 7
// get the 16 global ports, which move the voices, that x86 refuses those
// songs, and that the highest channel the first 8 allow is still narrow.
func TestAux16OnlyWhenUsed(t *testing.T) {
	t.Parallel()
	song := func(channel, stereo int) sointu.Song {
		u := func(typ string, params sointu.ParamMap) sointu.Unit {
			return sointu.Unit{Type: typ, Parameters: params}
		}
		return fsong(nil,
			[]sointu.Unit{
				u("loadval", sointu.ParamMap{"stereo": 0, "value": 100}),
				u("send", sointu.ParamMap{"stereo": 0, "amount": 96, "target": 5, "port": 0, "sendpop": 0}),
				u("pan", sointu.ParamMap{"stereo": 0, "panning": 64}),
				u("aux", sointu.ParamMap{"stereo": stereo, "channel": channel, "gain": 128}),
				u("pop", sointu.ParamMap{"stereo": 1 - stereo}),
			},
			[]sointu.Unit{
				u("in", sointu.ParamMap{"stereo": stereo, "channel": channel}),
				{ID: 5, Type: "gain", Parameters: sointu.ParamMap{"stereo": stereo, "gain": 64}},
				u("out", sointu.ParamMap{"stereo": stereo, "gain": 128}),
			})
	}
	compile := func(arch string, s sointu.Song) (*compiler.Compiler, string, error) {
		com, err := compiler.New("linux", arch, false, false)
		if err != nil {
			t.Fatal(err)
		}
		files, _, err := com.Song(&s)
		return com, files[".wat"], err
	}
	_, narrowWat, err := compile("wasm", song(6, 1))
	if err != nil {
		t.Fatal(err)
	}
	// the global ports are 8 or 16 floats before the voices, and the global
	// sends count from 64 bytes before the voices
	address := func(wat, pattern string) int {
		m := regexp.MustCompile(pattern).FindStringSubmatch(wat)
		if m == nil {
			t.Fatalf("the player has no %q", pattern)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	layout := func(wat string) (ports, sends int) {
		voices := address(wat, `\(global\.set \$voice \(i32\.const (\d+)\)\)`)
		return voices - address(wat, `\(call \$scanOperand\) \(i32\.const 4\)\) \(i32\.const (\d+)\)`), voices - address(wat, `\(select\s+\(i32\.const (\d+)\)`)
	}
	for _, tc := range []struct {
		channel, stereo int
		wide            bool
	}{{2, 1, false}, {6, 1, false}, {7, 0, false}, {7, 1, true}, {8, 0, true}, {8, 1, true}, {14, 1, true}, {15, 0, true}} {
		s := song(tc.channel, tc.stereo)
		_, wat, err := compile("wasm", s)
		if err != nil {
			t.Fatalf("channel %d: %v", tc.channel, err)
		}
		want := 4 * sointu.NarrowChannels
		if tc.wide {
			want = 4 * sointu.NumChannels
		}
		if ports, sends := layout(wat); ports != want || sends != 64 {
			t.Errorf("channel %d, stereo %d: %d bytes of global ports before the voices, want %d; global sends count from %d bytes before the voices", tc.channel, tc.stereo, ports, want, sends)
		}
		if tc.channel == 2 && tc.stereo == 1 {
			// apart from the operands, the same player
			if a, b := strings.Split(wat, "(data ")[0], strings.Split(narrowWat, "(data ")[0]; a != b {
				t.Errorf("the code of the players of songs with channels 2 and 6 differs")
			}
		}
		for _, arch := range []string{"386", "amd64"} {
			_, _, err := compile(arch, s)
			if tc.wide && (err == nil || !strings.Contains(err.Error(), "only supported when compiling for wasm")) {
				t.Errorf("channel %d, stereo %d, %s: expected an error, got %v", tc.channel, tc.stereo, arch, err)
			}
			if !tc.wide && err != nil {
				t.Errorf("channel %d, stereo %d, %s: %v", tc.channel, tc.stereo, arch, err)
			}
		}
	}
	// a disabled unit is not in the player
	disabled := song(6, 1)
	disabled.Patch[0].Units = append(disabled.Patch[0].Units, sointu.Unit{Type: "aux", Disabled: true, Parameters: sointu.ParamMap{"stereo": 1, "channel": 12, "gain": 128}})
	if _, wat, err := compile("wasm", disabled); err != nil || wat != narrowWat {
		t.Errorf("a disabled aux unit with channel 12 changed the player: %v", err)
	}
}
