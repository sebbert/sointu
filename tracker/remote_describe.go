package tracker

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/vsariola/sointu"
)

// What Remote tells about the song is text, written to cost a language
// model few tokens: one line for each unit, with the values of its
// parameters and what the tracker displays for them.

// goOnlyUnits are the unit types that only the Go synth and the wasm player
// have: a song using them cannot be compiled for x86.
var goOnlyUnits = map[string]bool{
	"bufread": true, "bufwrite": true, "spawn": true, "arg": true, "window": true,
	"spfft": true, "spifft": true, "spcopy": true, "spfilter": true, "spcompress": true, "spblur": true,
	"spgate": true, "spphase": true, "spscale": true, "spformant": true, "spcross": true, "spcomb": true,
	"ott": true, "limiter": true, "softclip": true, "width": true, "ladder": true, "reverb": true, "convolution": true,
	"mcspread": true, "mcsum": true, "mcdelay": true, "mcmix": true, "mcfilter": true, "mcloop": true, "mcloopend": true,
}

// unitDescriptions tell what each unit type does, in a line.
var unitDescriptions = map[string]string{
	"add":         "adds the top signal to the one below it, and keeps both",
	"addp":        "adds the two top signals into one",
	"arg":         "pushes a value passed by the spawn unit that triggered the voice",
	"aux":         "adds the signal to an output channel (left, right or an aux channel), times gain, and pops it",
	"belleq":      "a bell: raises or lowers the frequencies around frequency by gain, as wide as bandwidth",
	"bufread":     "plays an audio buffer: a sample, or what bufwrite units recorded",
	"bufwrite":    "records the signal into an audio buffer",
	"clip":        "clips the signal to -1..1",
	"convolution": "convolves the signal with an impulse response read from an audio buffer (a sample, or what a bufwrite unit wrote: e.g. decaying noise for a reverb); no latency; a written buffer is read again while playing",
	"compressor":  "pushes the gain that would compress the signal (it does not apply it: follow with mulp, or send it elsewhere for sidechaining)",
	"crush":       "reduces the resolution of the signal",
	"dbgain":      "a gain in decibels, -40 to +40 dB",
	"delay":       "delay lines with feedback and damping: echo, chorus, comb filters, and with many lines a reverb; with notetracking the delay follows the note (plucked strings)",
	"distort":     "a waveshaper: drive 64 leaves the signal, above distorts",
	"envelope":    "pushes an ADSR envelope of the note, times gain",
	"eq":          "a parametric equalizer of bands (set with bands); stands for filter, belleq, ladder and gain units, which count towards the 63 units",
	"filter":      "a state-variable filter of 12 dB per octave: lowpass, bandpass and highpass switch its outputs on, and are summed",
	"gain":        "multiplies the signal by gain, 0 to 1",
	"hold":        "sample and hold: lowers the sample rate of the signal",
	"in":          "pushes an output channel (left, right or an aux channel) and clears it: how a bus instrument takes what others sent to it",
	"invgain":     "divides the signal by invgain: gains from 0 dB up",
	"ladder":      "a low-pass of 24 dB per octave with resonance up to self-oscillation, like a Moog ladder",
	"limiter":     "a lookahead peak limiter",
	"loadnote":    "pushes the note of the voice, -1 to 1",
	"loadval":     "pushes a constant, -1 to 1",
	"mcdelay":     "a delay line for each of the 8 channels of a bus",
	"mcfilter":    "a one-pole filter on each channel of a bus",
	"mcloop":      "starts a feedback loop on a bus, which mcloopend closes",
	"mcloopend":   "closes the feedback loop that mcloop started",
	"mcmix":       "mixes the channels of a bus with an orthogonal matrix",
	"mcspread":    "spreads the signal over the 8 channels of a bus, and pops it",
	"mcsum":       "pushes the sum of the channels of a bus",
	"module":      "stands for the units of a module of the song; p1 to p8 set the parameters of the module",
	"mul":         "multiplies the top signal with the one below it, and keeps both",
	"mulp":        "multiplies the two top signals into one: e.g. an oscillator with its envelope",
	"noise":       "pushes white noise, through a waveshaper",
	"oscillator":  "pushes an oscillator at the note of the voice: sine, trisaw, pulse, gate or sample; with lfo it is slow and ignores the note",
	"ott":         "a three-band upward and downward compressor, like the OTT preset",
	"out":         "adds the signal to the main output, times gain, and pops it",
	"outaux":      "adds the signal to the main output and to the first aux channels, each with its gain, and pops it",
	"pan":         "pans a mono signal into a stereo one (mono), or changes the balance (stereo)",
	"pop":         "removes the top signal",
	"push":        "duplicates the top signal",
	"receive":     "pushes what send units sent to its ports: a way to get a signal from another instrument",
	"reverb":      "a reverb: a diffuser into a feedback delay network of 8 lines; stereo in, wet out",
	"send":        "adds the top signal, times amount (64 is none, above positive, below negative), to a parameter of another unit: modulation; with sendpop it pops the signal",
	"softclip":    "a clipper with a soft knee: leaves the signal below knee as it is",
	"spawn":       "triggers voices of another instrument: grains",
	"spblur":      "smooths a spectrum over time, or freezes it",
	"spcomb":      "resonances at the harmonics of the notes held in another instrument",
	"spcompress":  "pulls the magnitudes of a spectrum towards their mean",
	"spcopy":      "copies a spectrum to another spectrum buffer",
	"spcross":     "cross-synthesis of two spectra: a vocoder",
	"speed":       "changes the speed of the song by the signal, and pops it",
	"spfft":       "analyses the signal into a spectrum buffer, and pops it",
	"spfilter":    "cuts and tilts the bands of a spectrum",
	"spformant":   "shifts the formants of a spectrum",
	"spgate":      "removes the quiet bins of a spectrum",
	"spifft":      "pushes the sound of a spectrum buffer",
	"spphase":     "changes the phases of a spectrum: dispersion, random, robot",
	"spscale":     "scales and shifts the bins of a spectrum: pitch and frequency shifting",
	"sync":        "writes the signal to the sync track of the song, for the intro to read",
	"width":       "stereo width: scales the side signal; lowcut makes the bass mono",
	"window":      "pushes a smooth window over the note of a spawned voice, for grains without clicks",
	"xch":         "exchanges the two top signals",
}

// RemoteUnitTypes describes the unit types: all of them in a line each, or
// with a name, one of them with its parameters.
func RemoteUnitTypes(name string) (string, error) {
	var b strings.Builder
	if name = strings.TrimSpace(name); name != "" {
		t, ok := sointu.UnitTypes[name]
		if !ok {
			return "", fmt.Errorf("no unit type %q; the types are: %s", name, strings.Join(sointu.UnitNames, ", "))
		}
		fmt.Fprintf(&b, "%s: %s\n%s\n", name, unitDescriptions[name], unitTypeFlags(name, t))
		for _, p := range t.Params {
			if !p.CanSet && !p.CanModulate {
				continue
			}
			fmt.Fprintf(&b, "  %s", p.Name)
			if p.CanSet {
				fmt.Fprintf(&b, " %d..%d, default %d", p.MinValue, p.MaxValue, p.Default)
				if p.DisplayFunc != nil && p.MaxValue > p.MinValue {
					// what the values mean, at enough of them to choose one
					step := max((p.MaxValue-p.MinValue)/16, 1)
					if step > 1 {
						step = 8
					}
					var at []int
					for v := p.MinValue; v <= p.MaxValue && len(at) < 40; v += step {
						at = append(at, v)
						if v+step > p.MaxValue && v != p.MaxValue {
							at = append(at, p.MaxValue)
						}
					}
					_, unit := p.DisplayFunc(p.MinValue)
					for _, v := range at {
						if _, u := p.DisplayFunc(v); u != unit {
							unit = "" // e.g. ms and s: each value with its unit
						}
					}
					values := make([]string, len(at))
					for i, v := range at {
						value, u := p.DisplayFunc(v)
						if unit == "" && u != "" {
							value += " " + u
						}
						values[i] = fmt.Sprintf("%d=%s", v, value)
					}
					if unit != "" {
						unit = " in " + unit
					}
					fmt.Fprintf(&b, "; displayed%s: %s", unit, strings.Join(values, ", "))
				}
			} else {
				b.WriteString(" (no value: a port for sends only)")
			}
			if p.CanModulate {
				b.WriteString("; a send can modulate it")
			}
			b.WriteString("\n")
		}
		if name == "delay" {
			b.WriteString("  delaylines: the number of delay lines; delaytime1, delaytime2, ...: their times in samples, or with notetracking 1 relative to the note, or with 2 in beats\n")
		}
		return b.String(), nil
	}
	b.WriteString("Unit types. Stack: signals taken > signals left, mono (stereo). [go/wasm] cannot be compiled for x86.\n")
	for _, n := range sointu.UnitNames {
		t := sointu.UnitTypes[n]
		var params []string
		for _, p := range t.Params {
			if p.CanSet {
				params = append(params, p.Name)
			}
		}
		fmt.Fprintf(&b, "%s %s: %s | %s\n", n, unitTypeFlags(n, t), unitDescriptions[n], strings.Join(params, " "))
	}
	b.WriteString("Call again with a type for the ranges, defaults and displayed values of its parameters.")
	return b.String(), nil
}

func unitTypeFlags(name string, t sointu.UnitType) string {
	stack := func(stereo int) string {
		u := sointu.MakeUnit(name)
		if slices.ContainsFunc(t.Params, func(p sointu.UnitParameter) bool { return p.Name == "stereo" }) {
			u.Parameters["stereo"] = stereo
		} else if stereo == 1 {
			return ""
		}
		use := u.StackUse()
		return fmt.Sprintf("%d>%d", len(use.Inputs), use.NumOutputs)
	}
	ret := "[" + stack(0)
	if s := stack(1); s != "" {
		ret += " (" + s + ")"
	}
	if name == "module" || name == "eq" {
		ret = "[like its units"
	}
	if goOnlyUnits[name] {
		ret += ", go/wasm"
	}
	return ret + "]"
}

// Song describes the song: the instruments, the modules and the buffers in
// a line each, the tempo, the score in summary and what is wrong.
func (r *Remote) Song() string {
	m := (*Model)(r)
	song := &m.d.Song
	var b strings.Builder
	if m.d.FilePath != "" {
		fmt.Fprintf(&b, "file %s\n", m.d.FilePath)
	}
	rows := song.Score.LengthInRows()
	seconds := 0.0
	if song.BPM > 0 && song.RowsPerBeat > 0 {
		seconds = float64(rows) * 60 / float64(song.BPM*song.RowsPerBeat)
	}
	fmt.Fprintf(&b, "%d BPM, %d rows per beat | score: %d tracks, %d patterns of %d rows, %.1f s\n",
		song.BPM, song.RowsPerBeat, len(song.Score.Tracks), song.Score.Length, song.Score.RowsPerPattern, seconds)
	fmt.Fprintf(&b, "instruments, in the order they run (%d voices of %d):\n", song.Patch.NumVoices(), 255)
	channels := r.midiChannels()
	for i, instr := range song.Patch {
		fmt.Fprintf(&b, "  %d %q: %d voices, %s", i, instr.Name, max(instr.NumVoices, 1), r.unitCount(instr.Units))
		switch {
		case channels[i] == 0:
			b.WriteString(", no MIDI channel")
		case instr.MIDI.Channel == 0:
			fmt.Fprintf(&b, ", MIDI channel %d (auto)", channels[i])
		default:
			fmt.Fprintf(&b, ", MIDI channel %d", channels[i])
		}
		if instr.Mute {
			b.WriteString(", muted")
		}
		if reads := pairNames(r.channelUse(instr.Units).reads); reads != "" {
			fmt.Fprintf(&b, ", bus: reads %s", reads)
		}
		if outs := outputsOf(instr.Units); outs != "" {
			fmt.Fprintf(&b, ", to %s", outs)
		}
		if c := firstLine(instr.Comment); c != "" {
			fmt.Fprintf(&b, " | %s", c)
		}
		b.WriteString("\n")
	}
	if len(song.Modules) > 0 {
		b.WriteString("modules:\n")
		for i, mod := range song.Modules {
			fmt.Fprintf(&b, "  %s\n", r.moduleLine(i, &mod))
		}
	}
	if len(song.Buffers) > 0 {
		b.WriteString("buffers:\n")
		for _, buf := range song.Buffers {
			kind := "audio, empty"
			switch {
			case buf.Bus:
				kind = "bus of 8 channels for mc units"
			case buf.Spectrum:
				kind = "spectrum for spectral units"
			case buf.Sample != nil:
				kind = fmt.Sprintf("sample %s, %d bytes encoded", buf.Sample.FileName, len(buf.Sample.Data))
			case buf.Writable():
				kind = fmt.Sprintf("written by bufwrite units, %.2f s", float64(buf.Frames)/44100)
			}
			fmt.Fprintf(&b, "  %d %q: %s\n", buf.ID, buf.Name, kind)
		}
	}
	b.WriteString(r.problems())
	b.WriteString(r.Selection(false))
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	return s
}

// unitCount tells how many units an instrument has, and how many once its
// module and eq units are expanded, of the 63 it can have.
func (r *Remote) unitCount(units []sointu.Unit) string {
	expanded := r.d.Song.Modules.NumExpandedUnits(units)
	if expanded == len(units) {
		return fmt.Sprintf("%d units of %d", len(units), maxUnits)
	}
	return fmt.Sprintf("%d units, %d of %d expanded", len(units), expanded, maxUnits)
}

// outputsOf tells where the units of an instrument put their signal.
func outputsOf(units []sointu.Unit) string {
	var outs []string
	for _, u := range units {
		if u.Disabled {
			continue
		}
		switch u.Type {
		case "out", "outaux":
			outs = append(outs, u.Type)
		case "aux":
			name := strconv.Itoa(u.Parameters["channel"])
			if p, ok := sointu.BindableParam("aux", "channel"); ok && p.DisplayFunc != nil {
				name, _ = p.DisplayFunc(u.Parameters["channel"])
			}
			outs = append(outs, "aux "+name)
		}
	}
	return strings.Join(outs, ", ")
}

func (r *Remote) moduleLine(index int, mod *sointu.Module) string {
	m := (*Model)(r)
	outputs := "?"
	if n, err := m.d.Song.Modules.Outputs(index); err == nil {
		outputs = strconv.Itoa(n)
	}
	names := make([]string, len(mod.Params))
	for k, p := range mod.Params {
		names[k] = fmt.Sprintf("p%d %s", k+1, p.Name)
	}
	line := fmt.Sprintf("%q (id %d): %d in, %s out, %d units, used by %d module units", mod.Name, mod.ID, max(mod.Inputs, 0), outputs, len(mod.Units), m.moduleUses(mod.ID))
	if len(names) > 0 {
		line += " | params: " + strings.Join(names, ", ")
	}
	if c := firstLine(mod.Comment); c != "" {
		line += " | " + c
	}
	return line
}

// problems tells what is wrong with the patch, as the tracker shows it.
func (r *Remote) problems() string {
	m := (*Model)(r)
	var b strings.Builder
	if err := m.derived.railError.Err; err != nil {
		fmt.Fprintf(&b, "PROBLEM: %s\n", err)
	}
	if m.expansion != nil {
		for _, p := range m.expansion.Problems {
			fmt.Fprintf(&b, "PROBLEM: %s\n", p)
		}
	}
	for i, instr := range m.d.Song.Patch {
		if n := m.d.Song.Modules.NumExpandedUnits(instr.Units); n > maxUnits {
			fmt.Fprintf(&b, "PROBLEM: instrument %d %q has %d units once expanded; an instrument can have %d\n", i, instr.Name, n, maxUnits)
		}
	}
	b.WriteString(r.routingWarnings())
	return b.String()
}

// Instrument describes an instrument and its units. With verbose, each
// parameter comes with its range.
func (r *Remote) Instrument(ref string, verbose bool) (string, error) {
	i, err := r.instrument(ref)
	if err != nil {
		return "", err
	}
	return r.describeInstrument(i, verbose), nil
}

// Module describes a module, its parameters and its units.
func (r *Remote) Module(ref string, verbose bool) (string, error) {
	i, err := r.module(ref)
	if err != nil {
		return "", err
	}
	return r.describeModule(i, verbose), nil
}

func (r *Remote) describeInstrument(index int, verbose bool) string {
	m := (*Model)(r)
	instr := &m.d.Song.Patch[index]
	var b strings.Builder
	fmt.Fprintf(&b, "instrument %d %q: %d voices, %s", index, instr.Name, max(instr.NumVoices, 1), r.unitCount(instr.Units))
	if instr.Mute {
		b.WriteString(", muted")
	}
	b.WriteString("\n")
	if instr.Comment != "" {
		fmt.Fprintf(&b, "comment: %s\n", strings.ReplaceAll(strings.TrimSpace(instr.Comment), "\n", " / "))
	}
	var rails []Rail
	if index < len(m.derived.patch) {
		rails = m.derived.patch[index].rails
	}
	r.describeUnits(&b, unitLoc{&instr.Units, 0, index, -1}, rails, verbose)
	b.WriteString(r.problems())
	b.WriteString(r.Selection(false))
	return strings.TrimRight(b.String(), "\n")
}

func (r *Remote) describeModule(index int, verbose bool) string {
	m := (*Model)(r)
	mod := &m.d.Song.Modules[index]
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n", r.moduleLine(index, mod))
	for k, p := range mod.Params {
		fmt.Fprintf(&b, "  p%d %q: default %d", k+1, p.Name, p.Default)
		if up, ok := m.d.Song.Modules.Param(index, k+1); ok {
			fmt.Fprintf(&b, ", %d..%d", up.MinValue, up.MaxValue)
			if up.DisplayFunc != nil {
				value, unit := up.DisplayFunc(p.Default)
				fmt.Fprintf(&b, " (%s)", strings.TrimSpace(value+" "+unit))
			}
		}
		if t, name, ok := m.d.Song.Modules.ParamSource(index, k+1); ok {
			fmt.Fprintf(&b, ", like %s.%s", t, name)
		} else {
			b.WriteString(", nothing bound")
		}
		b.WriteString("\n")
	}
	var rails []Rail
	if index < len(m.derived.modules) {
		rails = m.derived.modules[index].rails
	}
	r.describeUnits(&b, unitLoc{&mod.Units, 0, -1, index}, rails, verbose)
	b.WriteString(r.problems())
	b.WriteString(r.Selection(false))
	return strings.TrimRight(b.String(), "\n")
}

// describeUnits writes a line for each unit: its ID, its type, the signals
// on the stack before and after it, and its parameters.
func (r *Remote) describeUnits(b *strings.Builder, loc unitLoc, rails []Rail, verbose bool) {
	m := (*Model)(r)
	units := *loc.list
	if len(units) == 0 {
		b.WriteString("  (no units)\n")
		return
	}
	b.WriteString("units, as #id type [stack before>after] parameters:\n")
	var goOnly []string
	for i := range units {
		u := &units[i]
		fmt.Fprintf(b, "  #%d %s", u.ID, u.Type)
		if u.Type == "" {
			b.WriteString("(empty)")
		}
		if u.Disabled {
			b.WriteString(" DISABLED")
		}
		if i < len(rails) {
			fmt.Fprintf(b, " [%d>%d]", rails[i].PassThrough+len(rails[i].StackUse.Inputs), rails[i].StackAfter())
		}
		if goOnlyUnits[u.Type] && !u.Disabled && !strings.Contains(strings.Join(goOnly, " "), u.Type) {
			goOnly = append(goOnly, u.Type)
		}
		params := m.deriveParams(u, nil)
		for j := range params {
			p := &params[j]
			if p.vtable == nil || p.trackerSetting() {
				continue
			}
			if _, isPreset := p.vtable.(*reverbParameter); isPreset {
				continue // a chooser of the tracker for the delay times
			}
			name := remoteParamName(p)
			if p.Type() == NoParameter {
				continue // a port for sends only: see the unit type
			}
			if shown := p.Name(); shown != name && p.arg != nil || strings.HasPrefix(name, "p") && u.Type == "module" && shown != name {
				name += ":" + shown
			}
			value := p.Value()
			fmt.Fprintf(b, " %s=%d", name, value)
			if hint := strings.TrimSpace(p.Hint().Label); hint != "" && hint != strconv.Itoa(value) {
				fmt.Fprintf(b, "(%s)", hint)
			}
			if verbose {
				rng := p.Range()
				fmt.Fprintf(b, "[%d..%d]", rng.Min, rng.Max)
			}
		}
		if u.Type == "send" {
			b.WriteString(" -> " + r.sendTarget(u, loc))
		}
		if u.Type == "eq" {
			b.WriteString(" bands:")
			if len(u.Bands) == 0 {
				b.WriteString(" none")
			}
			for _, band := range u.Bands {
				band = band.Normalized()
				fmt.Fprintf(b, " {%s %g Hz", band.Type, band.Frequency)
				if sointu.EQHasGain(band.Type) {
					fmt.Fprintf(b, " %+g dB", band.Gain)
				}
				fmt.Fprintf(b, " q %g", band.Q)
				if band.Disabled {
					b.WriteString(" off")
				}
				b.WriteString("}")
			}
			fmt.Fprintf(b, " (%d units)", u.NumEQUnits())
		}
		if len(u.Bind) > 0 {
			names := make([]string, 0, len(u.Bind))
			for name := range u.Bind {
				names = append(names, name)
			}
			sort.Strings(names)
			b.WriteString(" bound:")
			for _, name := range names {
				bind := u.Bind[name]
				fmt.Fprintf(b, " %s=p%d", name, bind.Param)
				if bind.Scaled {
					fmt.Fprintf(b, "(%d..%d)", bind.Min, bind.Max)
				}
			}
		}
		if u.Comment != "" {
			fmt.Fprintf(b, " // %s", strings.ReplaceAll(u.Comment, "\n", " "))
		}
		b.WriteString("\n")
	}
	if len(goOnly) > 0 {
		fmt.Fprintf(b, "go/wasm only (not x86): %s\n", strings.Join(goOnly, ", "))
	}
}

// sendTarget tells what a send modulates.
func (r *Remote) sendTarget(send *sointu.Unit, from unitLoc) string {
	m := (*Model)(r)
	id := send.Parameters["target"]
	if id == 0 {
		return "no target"
	}
	loc, err := r.findUnit(id)
	if err != nil {
		return fmt.Sprintf("#%d, which is missing", id)
	}
	t := loc.unit()
	port := send.Parameters["port"]
	name := "port " + strconv.Itoa(port)
	if t.Type == "module" {
		name = sointu.ModuleParamName(port + 1)
		if i, ok := m.d.Song.Modules.Find(t.Parameters["module"]); ok && port < len(m.d.Song.Modules[i].Params) {
			name += ":" + m.d.Song.Modules[i].Params[port].Name
		}
	} else if ports := sointu.Ports[t.Type]; port >= 0 && port < len(ports) {
		name = ports[port]
	}
	ret := fmt.Sprintf("%s of #%d %s", name, id, t.Type)
	if loc.instr != from.instr || loc.module != from.module {
		ret += " in " + r.scopeName(loc)
	}
	return ret
}

// Presets lists the instrument presets, by their directories, and the
// module presets. search keeps those with it in their name or directory.
func (r *Remote) Presets(search string) string {
	m := (*Model)(r)
	search = strings.ToLower(strings.TrimSpace(search))
	var b strings.Builder
	dirs := map[string][]string{}
	var order []string
	for _, p := range m.presetData.presets {
		if search != "" && !strings.Contains(strings.ToLower(p.dir+"/"+p.instr.Name), search) {
			continue
		}
		if _, ok := dirs[p.dir]; !ok {
			order = append(order, p.dir)
		}
		name := p.instr.Name
		if p.user {
			name += " (user)"
		}
		dirs[p.dir] = append(dirs[p.dir], name)
	}
	sort.Strings(order)
	b.WriteString("instrument presets, by directory (load with add_instrument or edit_instrument, as name or directory/name):\n")
	for _, dir := range order {
		fmt.Fprintf(&b, "  %s: %s\n", dir, strings.Join(dirs[dir], ", "))
	}
	if len(order) == 0 {
		b.WriteString("  none match\n")
	}
	b.WriteString("module presets (add with add_module):\n")
	found := false
	for i, p := range m.modulePresets {
		if search != "" && !strings.Contains(strings.ToLower(p.name), search) || len(p.modules) == 0 {
			continue
		}
		found = true
		mod := p.modules[len(p.modules)-1]
		names := make([]string, len(mod.Params))
		for k, mp := range mod.Params {
			names[k] = mp.Name
		}
		fmt.Fprintf(&b, "  %s: %d in, %d units", p.name, max(mod.Inputs, 0), len(mod.Units))
		if len(names) > 0 {
			fmt.Fprintf(&b, ", params: %s", strings.Join(names, ", "))
		}
		if i < m.userModulePresets {
			b.WriteString(" (user)")
		}
		if c := firstLine(mod.Comment); c != "" {
			fmt.Fprintf(&b, " | %s", c)
		}
		b.WriteString("\n")
	}
	if !found {
		b.WriteString("  none match\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Label names the song for a list of running trackers: its file and its
// instruments.
func (r *Remote) Label() (file string, instruments []string) {
	for _, instr := range r.d.Song.Patch {
		instruments = append(instruments, instr.Name)
	}
	return r.d.FilePath, instruments
}
