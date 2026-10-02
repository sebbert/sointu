package compiler

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/vm"
)

type Compiler struct {
	Template    *template.Template
	OS          string
	Arch        string
	Output16Bit bool
	RowSync     bool
	// Buffers are the encoded samples of the song's buffers, keyed by
	// sointu.Buffer.ID, for compiling songs that play buffers (wasm only).
	Buffers map[int]EncodedBuffer
	// MathImports makes the wasm player call Math.pow and Math.sin of
	// JavaScript instead of computing them itself: a smaller player, but its
	// output then differs slightly from the Go synth, and between browsers.
	MathImports bool
	// Progressive makes the wasm player render nothing at instantiation:
	// it exports r(rows), which renders the next rows of the song and can be
	// called until the song ends.
	Progressive bool
	// JS makes Song also write a JavaScript module (.js, with its types in
	// .d.ts) that renders the song in the background with the progressive
	// player, in workers, and plays it while it renders. It implies
	// Progressive. The module has only the code this song needs.
	JS bool
	// SeparateSamples leaves the encoded samples of the buffers out of the
	// wasm player, where they are custom sections by default: Song returns
	// them as files with the extensions ".0.<format>", ".1.<format>" and so
	// on, numbered in the order the player asks for them, and the host
	// passes them to the JavaScript module.
	SeparateSamples bool
	// Stages makes the progressive wasm player able to render the song in
	// a pipeline of up to this many stages, each a range of voices run by
	// its own instance (see wasm_stages.go); StageCuts sets the first
	// voices of the stages after the first instead. ChunkRows is the most
	// rows a stage renders in a call; by default, the rows of about half a
	// second. The player without stages is the same.
	Stages    int
	StageCuts []int
	ChunkRows int
	// Log, if not nil, is told how the compiler laid out the stages.
	Log func(string)
	// Layout is set by Song to the layout of the wasm player it compiled.
	Layout *WasmLayout
}

// WasmLayout tells a host where the wasm player keeps what the host reads
// and writes, as addresses in the memory of the player.
type WasmLayout struct {
	Output      int // address of the audio: interleaved stereo float32, or int16 with Output16Bit
	OutputBytes int // size of the audio of the song
	Rows        int // rows of the song
	RowSamples  int // samples of a row
	ChunkRows   int // the rows to render at a time; with stages, the most a call to r may render
	// Stages is the number of stages of the pipeline, 0 without. TapeIn and
	// TapeOut are the addresses of the tapes a stage reads and writes, and
	// StageCells the number of cells (4 bytes) that each stage reads for
	// every sample, which the stage before it writes.
	Stages          int
	TapeIn, TapeOut int
	StageCells      []int
	StageVoices     [][2]int // first voice and the voice after the last of each stage
	// Sync is the address of the sync values, float32s: SyncValues for
	// every 256th sample of the song, SyncTicks times. The values are the
	// row with RowSync, then the signals at the sync units in the order
	// they run. SyncValues is 0 in songs without.
	Sync, SyncValues, SyncTicks int
}

// wasmSyncData is the layout of the sync values in the wasm player.
type wasmSyncData struct {
	NumSyncs  int // sync values of a sample: the sync units of all voices, and the row with RowSync
	SyncBytes int // size of su_syncbuffer
}

// wasmSync counts the sync values of the song. The patch is expanded.
func wasmSync(song *sointu.Song, rowSync bool) (ret wasmSyncData) {
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Type == "sync" && !u.Disabled {
				ret.NumSyncs += instr.NumVoices
			}
		}
	}
	if rowSync {
		ret.NumSyncs++
	}
	ret.SyncBytes = (song.Score.LengthInRows()*song.SamplesPerRow() + 255) >> 8 * ret.NumSyncs * 4
	return
}

// chunkRows returns the rows the players render at a time: ChunkRows, or
// by default the rows of about half a second.
func (com *Compiler) chunkRows(song *sointu.Song) int {
	if com.ChunkRows > 0 {
		return com.ChunkRows
	}
	if spr := song.SamplesPerRow(); spr > 0 {
		return max(1, (22050+spr/2)/spr)
	}
	return 1
}

// EncodedBuffer is the sample of a buffer encoded for the compiled player,
// with the length and channels of its decoded audio.
type EncodedBuffer struct {
	Encoded  []byte
	Frames   int
	Channels int
	// Format is the extension of the file when the sample is written
	// separately, e.g. "ogg"; "bin" if empty.
	Format string
}

// wasmBuffer is a buffer in the wasm player: its encoded sample goes in a
// custom section, which the host decodes, and the decoded audio is stored at
// Offset bytes from su_buffers.
type wasmBuffer struct {
	Offset, Frames, Channels int
	EncodedHex               string
	encoded                  []byte
	format                   string
}

// wasmBufferHeader is the runtime state of a buffer in the wasm player: the
// offset of its audio from su_buffers in bytes, its capacity in frames, its
// channels, and the valid frames, which are the filled frames before head.
type wasmBufferHeader struct {
	Offset, Capacity, Channels, Head, Filled uint32
}

// wasmBufferRegion is an entry of the wasm player's buffer region table.
// Header is the offset of the buffer's header from su_buffer_headers in bytes.
type wasmBufferRegion struct {
	Header, Start, LoopStart, LoopLength, Fade, EdgeFade, Flags uint32
}

// wasmBufferData is the buffer data for the wasm player template.
type wasmBufferData struct {
	Buffers     []wasmBuffer
	Headers     []wasmBufferHeader
	Regions     []wasmBufferRegion
	BufferBytes int
	wasmBufferFeatures
}

// wasmBufferFeatures tells which parts of bufread and bufwrite the song
// needs; the wasm player leaves the others out. Each part does nothing in a
// song that does not need it, so the player renders the same without it.
type wasmBufferFeatures struct {
	BufreadLoop         bool // a bufread loops
	BufreadFade         bool // a looping bufread has a crossfade
	BufreadBackwards    bool // the position of a looping bufread can get below the loop start: negative or modulated speed, modulated loop start
	BufreadMod          bool // start, loop start or loop length is modulated
	BufreadModStart     bool
	BufreadModLoopStart bool
	BufreadModLoopLen   bool
	BufreadNegStart     bool // a start counts from the newest frame
	BufreadNegLoopStart bool // a loop start counts from the newest frame
	BufreadEdgeFade     bool
	BufreadNoteTracking bool
	BufreadPitch        bool // transpose or detune is not 64, or modulated
	BufreadMonoMix      bool // a mono bufread plays a stereo buffer
	BufreadMonoPlain    bool // a mono bufread plays a mono buffer
	BufreadChannelClamp bool // a stereo bufread plays a mono buffer

	BufwriteNoPop     bool
	BufwriteOneShot   bool
	BufwriteRing      bool
	BufwriteMix       bool // several bufwrite units or voices write the same buffer
	BufwriteFeedback  bool
	BufwriteStereoBuf bool // a buffer written is stereo
	BufwriteMonoBuf   bool // a buffer written is mono
}

// bufferFeatures finds the parts of bufread and bufwrite that the units of
// the song use.
func bufferFeatures(song *sointu.Song, features vm.FeatureSet, b *vm.Bytecode, data *wasmBufferData) (f wasmBufferFeatures) {
	channels := map[int]uint32{}
	for i, r := range b.BufferRegions {
		channels[int(r.BufferID)] = data.Headers[data.Regions[i].Header/wasmBufferHeaderSize].Channels
	}
	mod := func(unit, param string) bool { return features.SupportsModulation(unit, param) }
	f.BufreadModStart = mod("bufread", "start")
	f.BufreadModLoopStart = mod("bufread", "loopstart")
	f.BufreadModLoopLen = mod("bufread", "looplength")
	f.BufreadMod = f.BufreadModStart || f.BufreadModLoopStart || f.BufreadModLoopLen
	f.BufreadPitch = mod("bufread", "transpose") || mod("bufread", "detune")
	f.BufwriteFeedback = mod("bufwrite", "feedback")
	backwards := mod("bufread", "speed") || f.BufreadModLoopStart
	writers := map[int]int{}
	for _, instr := range song.Patch {
		for _, u := range instr.Units {
			if u.Disabled {
				continue
			}
			p := u.Parameters
			ch := channels[p["buffer"]]
			switch u.Type {
			case "bufread":
				loop := p["loop"] == 1
				f.BufreadLoop = f.BufreadLoop || loop
				f.BufreadFade = f.BufreadFade || loop && p["fade"] != 0
				backwards = backwards || p["speed"] < 64
				f.BufreadNegStart = f.BufreadNegStart || int32(p["start"]) < 0
				f.BufreadNegLoopStart = f.BufreadNegLoopStart || loop && int32(p["loopstart"]) < 0
				f.BufreadEdgeFade = f.BufreadEdgeFade || p["edgefade"] > 0
				f.BufreadNoteTracking = f.BufreadNoteTracking || p["notetracking"] == 1
				f.BufreadPitch = f.BufreadPitch || p["transpose"] != 64 || p["detune"] != 64
				if p["stereo"] == 1 {
					f.BufreadChannelClamp = f.BufreadChannelClamp || ch != 2
				} else if ch == 2 {
					f.BufreadMonoMix = true
				} else {
					f.BufreadMonoPlain = true
				}
			case "bufwrite":
				f.BufwriteNoPop = f.BufwriteNoPop || p["pop"] == 0
				if p["oneshot"] == 0 {
					f.BufwriteRing = true
				} else {
					f.BufwriteOneShot = true
				}
				f.BufwriteFeedback = f.BufwriteFeedback || p["feedback"] != 0
				if ch == 2 {
					f.BufwriteStereoBuf = true
				} else {
					f.BufwriteMonoBuf = true
				}
				writers[p["buffer"]] += instr.NumVoices
				f.BufwriteMix = f.BufwriteMix || writers[p["buffer"]] > 1
			}
		}
	}
	f.BufreadBackwards = f.BufreadLoop && backwards
	return
}

// wasmBufferHeaderSize is the size of a buffer header in the wasm player in
// bytes: wasmBufferHeader and the time of the frame written last.
const wasmBufferHeaderSize = 24

//go:embed templates/amd64-386/* templates/wasm/*
var templateFS embed.FS

// New returns a new compiler using the default .asm templates
func New(os string, arch string, output16Bit bool, rowsync bool) (*Compiler, error) {
	var subdir string
	if arch == "386" || arch == "amd64" {
		subdir = "amd64-386"
	} else if arch == "wasm" {
		subdir = "wasm"
	} else {
		return nil, fmt.Errorf("compiler.New failed, because only amd64, 386 and wasm archs are supported (targeted architecture was %v)", arch)
	}
	tmpl, err := template.New("base").Funcs(sprig.TxtFuncMap()).ParseFS(templateFS, "templates/"+subdir+"/*.*")
	if err != nil {
		return nil, fmt.Errorf(`could not create templates: %v`, err)
	}
	return &Compiler{Template: tmpl, OS: os, Arch: arch, RowSync: rowsync, Output16Bit: output16Bit}, nil
}

func NewFromTemplates(os string, arch string, output16Bit bool, rowsync bool, templateDirectory string) (*Compiler, error) {
	globPtrn := filepath.Join(templateDirectory, "*.*")
	tmpl, err := template.New("base").Funcs(sprig.TxtFuncMap()).ParseGlob(globPtrn)
	if err != nil {
		return nil, fmt.Errorf(`could not create template based on directory "%v": %v`, templateDirectory, err)
	}
	return &Compiler{Template: tmpl, OS: os, Arch: arch, RowSync: rowsync, Output16Bit: output16Bit}, nil
}

func (com *Compiler) Library() (map[string]string, error) {
	if com.Arch != "386" && com.Arch != "amd64" {
		return nil, fmt.Errorf(`compiling as a library is supported only on 386 and amd64 architectures (targeted architecture was %v)`, com.Arch)
	}
	templates := []string{"library.asm", "library.h"}
	features := vm.AllFeatures{}
	retmap := map[string]string{}
	for _, templateName := range templates {
		compilerMacros := *NewCompilerMacros(*com)
		compilerMacros.Library = true
		featureSetMacros := FeatureSetMacros{features}
		x86Macros := *NewX86Macros(com.OS, com.Arch == "amd64", features, false)
		data := struct {
			CompilerMacros
			FeatureSetMacros
			X86Macros
		}{compilerMacros, featureSetMacros, x86Macros}
		populatedTemplate, extension, err := com.compile(templateName, &data)
		if err != nil {
			return nil, fmt.Errorf(`could not execute template "%v": %v`, templateName, err)
		}
		retmap[extension] = populatedTemplate
	}
	return retmap, nil
}

func (com *Compiler) Song(song *sointu.Song) (retmap map[string]string, warnings []string, err error) {
	if com.Arch != "386" && com.Arch != "amd64" && com.Arch != "wasm" {
		return nil, nil, fmt.Errorf(`compiling a song player is supported only on 386, amd64 and wasm architectures (targeted architecture was %v)`, com.Arch)
	}
	if com.JS {
		com.Progressive = true
	}
	if (com.Progressive || com.SeparateSamples) && com.Arch != "wasm" {
		return nil, nil, fmt.Errorf(`the progressive player, its JavaScript module and separate samples are only for wasm (targeted architecture was %v)`, com.Arch)
	}
	var templates []string
	if com.Arch == "386" || com.Arch == "amd64" {
		templates = []string{"player.asm", "player.h", "player.inc"}
	} else if com.Arch == "wasm" {
		templates = []string{"player.wat"}
	}
	if song.HasModules() {
		// the players only know the units the module units stand for
		expanded, expansion := song.Expand()
		if len(expansion.Problems) > 0 {
			return nil, nil, fmt.Errorf(`could not expand the modules: %w`, errors.Join(expansion.Problems...))
		}
		song = &expanded
	}
	features := vm.NecessaryFeaturesFor(song.Patch)
	for _, unit := range features.Instructions() {
		wasmOnly := len(sointu.SpectrumBufferParams(unit)) > 0 || len(sointu.BusParams(unit)) > 0
		switch unit {
		case "bufread", "bufwrite", "spawn", "arg", "window", "ott", "limiter", "softclip", "width", "ladder":
			wasmOnly = true
		}
		if wasmOnly && com.Arch != "wasm" {
			return nil, nil, fmt.Errorf(`the %v unit is only supported when compiling for wasm (targeted architecture was %v)`, unit, com.Arch)
		}
	}
	if features.SupportsParamValue("oscillator", "bandlimit", 1) && com.Arch != "wasm" {
		return nil, nil, fmt.Errorf(`bandlimited oscillators are only supported when compiling for wasm (targeted architecture was %v)`, com.Arch)
	}
	if vm.TransformsParam(features, "envelope", "curve") && com.Arch != "wasm" {
		return nil, nil, fmt.Errorf(`curved or curve-modulated envelopes are only supported when compiling for wasm (targeted architecture was %v)`, com.Arch)
	}
	if n := max(song.Patch.NumVoices(), song.Score.NumVoices()); n > vm.MAX_VOICES_NARROW && com.Arch != "wasm" {
		return nil, nil, fmt.Errorf(`more than %v voices are only supported when compiling for wasm (song uses %v voices, targeted architecture was %v)`, vm.MAX_VOICES_NARROW, n, com.Arch)
	}
	if _, ok := features.Opcode("speed"); ok {
		warnings = append(warnings, fmt.Sprintf(`song uses the speed unit, so SU_LENGTH_IN_SAMPLES, SU_BUFFER_LENGTH, and SU_SYNCBUFFER_LENGTH cannot be known without rendering the entire song. They won't be defined in the generated header file. You have to take responsibility for allocating large enough audio buffer and syncBuf.`))
	}
	retmap = map[string]string{}
	encodedPatch, err := vm.NewBytecode(song.Patch, features, song.BPM)
	if err != nil {
		return nil, nil, fmt.Errorf(`could not encode patch: %v`, err)
	}
	patterns, sequences, err := ConstructPatterns(song)
	if err != nil {
		return nil, nil, fmt.Errorf(`could not encode song: %v`, err)
	}
	for _, templateName := range templates {
		compilerMacros := *NewCompilerMacros(*com)
		featureSetMacros := FeatureSetMacros{features}
		songMacros := *NewSongMacros(song)
		var populatedTemplate, extension string
		var err error
		if com.Arch == "386" || com.Arch == "amd64" {
			x86Macros := *NewX86Macros(com.OS, com.Arch == "amd64", features, false)
			data := struct {
				CompilerMacros
				FeatureSetMacros
				X86Macros
				SongMacros
				*vm.Bytecode
				Patterns       [][]byte
				Sequences      [][]byte
				PatternLength  int
				SequenceLength int
				Hold           int
			}{compilerMacros, featureSetMacros, x86Macros, songMacros, encodedPatch, patterns, sequences, len(patterns[0]), len(sequences[0]), 1}
			populatedTemplate, extension, err = com.compile(templateName, &data)
		} else if com.Arch == "wasm" {
			wasmMacros := *NewWasmMacros()
			buffers, bufErr := com.wasmBuffers(song, encodedPatch)
			if bufErr != nil {
				return nil, nil, bufErr
			}
			buffers.wasmBufferFeatures = bufferFeatures(song, features, encodedPatch, &buffers)
			units := unitFeatures(song, encodedPatch)
			if (com.Stages > 1 || len(com.StageCuts) > 0) && !com.Progressive {
				return nil, nil, errors.New("only the progressive player renders in stages")
			}
			stages, report, stageErr := wasmStages(song, features, com.Stages, com.StageCuts, com.chunkRows(song), com.RowSync)
			if stageErr != nil {
				return nil, nil, stageErr
			}
			if com.Log != nil {
				for _, line := range report {
					com.Log(line)
				}
			}
			data := struct {
				CompilerMacros
				FeatureSetMacros
				WasmMacros
				SongMacros
				*vm.Bytecode
				Patterns       [][]byte
				Sequences      [][]byte
				PatternLength  int
				SequenceLength int
				Hold           int
				wasmBufferData
				wasmSpectralData
				wasmMCData
				wasmUnitFeatures
				wasmStageData
				wasmSyncData
			}{compilerMacros, featureSetMacros, wasmMacros, songMacros, encodedPatch, patterns, sequences, len(patterns[0]), len(sequences[0]), 1, buffers, wasmSpectral(encodedPatch, units), wasmMC(encodedPatch, units, featureSetMacros.MCDelayMod()), units, stages, wasmSync(song, com.RowSync)}
			populatedTemplate, extension, err = com.compile(templateName, &data)
			com.Layout = &WasmLayout{
				Output: wasmMacros.Labels["su_outputbuffer"], OutputBytes: wasmMacros.Labels["su_outputend"] - wasmMacros.Labels["su_outputbuffer"],
				Rows: song.Score.LengthInRows(), RowSamples: song.SamplesPerRow(), ChunkRows: com.chunkRows(song),
				Stages: stages.NumStages, TapeIn: wasmMacros.Labels["su_tape_in"], TapeOut: wasmMacros.Labels["su_tape_out"],
				Sync: wasmMacros.Labels["su_syncbuffer"], SyncValues: data.NumSyncs,
			}
			if data.NumSyncs > 0 {
				com.Layout.SyncTicks = data.SyncBytes / (4 * data.NumSyncs)
			}
			for _, s := range stages.Stages[:stages.NumStages] {
				com.Layout.StageCells = append(com.Layout.StageCells, len(s.InCells))
				com.Layout.StageVoices = append(com.Layout.StageVoices, [2]int{s.First, s.End})
			}
			if com.SeparateSamples && err == nil {
				for i, b := range buffers.Buffers {
					retmap[fmt.Sprintf(".%d.%s", i, b.format)] = string(b.encoded)
				}
			}
			if com.JS && err == nil {
				if _, ok := features.Opcode("speed"); ok {
					return nil, nil, errors.New("the JavaScript module cannot play songs with the speed unit: the lengths of their rows are not known")
				}
				frameBytes := 8
				if com.Output16Bit {
					frameBytes = 4
				}
				jsData := struct {
					*WasmLayout
					Song                                      *sointu.Song
					Frames                                    int // of the song
					FrameBytes                                int // of the audio of a frame
					Samples                                   int // buffers with samples
					SeparateSamples, MathImports, Output16Bit bool
					RowSync                                   bool
				}{com.Layout, song, com.Layout.OutputBytes / frameBytes, frameBytes, len(buffers.Buffers), com.SeparateSamples, com.MathImports, com.Output16Bit, com.RowSync}
				for _, name := range []string{"player.js", "player.d.ts"} {
					result := bytes.NewBufferString("")
					if err := com.Template.ExecuteTemplate(result, name, &jsData); err != nil {
						return nil, nil, fmt.Errorf(`could not execute template "%v": %v`, name, err)
					}
					retmap[strings.TrimPrefix(name, "player")] = result.String()
				}
			}
		}
		if err != nil {
			return nil, nil, fmt.Errorf(`could not execute template "%v": %v`, templateName, err)
		}
		retmap[extension] = populatedTemplate
	}
	return retmap, warnings, nil
}

func (com *Compiler) compile(templateName string, data interface{}) (string, string, error) {
	result := bytes.NewBufferString("")
	err := com.Template.ExecuteTemplate(result, templateName, data)
	extension := filepath.Ext(templateName)
	return result.String(), extension, err
}

// wasmSpectralData is the layout of the spectral units in the wasm player.
// Spectra and the states of the units are in su_spectral, with offsets in
// bytes from it.
type wasmSpectralData struct {
	// SpectrumTable has 4 i32s for each spectrum: offset of its data, base 2
	// logarithm of its size, the number of spectra written to it and the
	// number of channels, whose data follow each other. Without stereo
	// spectra in the song (SpectralStereo), it has no channels: 3 i32s.
	SpectrumTable []uint32
	// SpectralTable has 4 i32s for each spectral unit: the offset of its
	// state, the offsets of its spectrum and source spectrum in
	// SpectrumTable, and the offset of the voice that runs it from
	// su_voices; that only when an instrument with spectral units has
	// several voices (SpectralVoices), otherwise 3 i32s. The state
	// is the position in its ring, the count of the spectrum it processed
	// last and the state of its random number generator, 16 bytes, followed
	// by the rings of spfft and spifft, the held spectrum of spblur and the
	// smoothed envelope of spcompress, a float for each bin and channel.
	SpectralTable []uint32
	// SpectralBytes is the size of su_spectral. After the spectra and the
	// states of the units, it has a scratch space at SpectralScratch of 2n+8
	// floats for the largest spectrum size n = 2^SpectralMaxLog2, and the tables computed
	// when the player starts, like spectralTables in the vm package: the Hann
	// window of the largest size at SpectralHann, and the twiddle factors
	// at SpectralTwiddles, each as the pair wr, wr, followed by the pairs
	// -wi, wi at SpectralTwiddles + SpectralTwiddleBytes.
	SpectralBytes, SpectralScratch, SpectralMaxLog2      int
	SpectralHann, SpectralTwiddles, SpectralTwiddleBytes int
	SpectralMaxSize                                      int
}

func wasmSpectral(b *vm.Bytecode, f wasmUnitFeatures) (ret wasmSpectralData) {
	offset := 0
	stride := 12 // of the spectrum table, in bytes
	if f.SpectralStereo {
		stride = 16
	}
	for _, sp := range b.Spectra {
		ret.SpectrumTable = append(ret.SpectrumTable, uint32(offset), uint32(sp.Log2Size), 0)
		if f.SpectralStereo {
			ret.SpectrumTable = append(ret.SpectrumTable, uint32(sp.Channels))
		}
		offset += 2 * (1 << sp.Log2Size) * 4 * sp.Channels
		ret.SpectralMaxLog2 = max(ret.SpectralMaxLog2, sp.Log2Size)
	}
	for _, u := range b.SpectralUnits {
		source := 0
		if u.Source >= 0 {
			source = u.Source * stride
		}
		ret.SpectralTable = append(ret.SpectralTable, uint32(offset), uint32(u.Spectrum*stride), uint32(source))
		if f.SpectralVoices {
			ret.SpectralTable = append(ret.SpectralTable, uint32(u.Voice*4096))
		}
		offset += 16
		switch u.Type {
		case "spfft", "spifft": // a ring for each channel of the unit
			offset += (1 << b.Spectra[u.Spectrum].Log2Size) * 4 * u.Channels
		case "spblur": // the held spectrum
			offset += (1<<b.Spectra[u.Spectrum].Log2Size + 2) * 4 * b.Spectra[u.Spectrum].Channels
		case "spcompress": // the smoothed envelope of each bin
			if u.Smooth {
				offset += (1<<(b.Spectra[u.Spectrum].Log2Size-1) + 1) * 4 * b.Spectra[u.Spectrum].Channels
			}
		}
	}
	maxSize := 1 << ret.SpectralMaxLog2
	ret.SpectralMaxSize = maxSize
	ret.SpectralScratch = offset
	ret.SpectralHann = ret.SpectralScratch + (2*maxSize+8)*4
	ret.SpectralTwiddles = ret.SpectralHann + maxSize*4
	ret.SpectralTwiddleBytes = (maxSize - 1) * 8
	ret.SpectralBytes = ret.SpectralTwiddles + 2*ret.SpectralTwiddleBytes
	return ret
}

// wasmMCData is the layout of the mc units in the wasm player. The buses and
// the states of the units are in su_mc, with offsets in bytes from it; their
// constant data in su_mc_consts.
type wasmMCData struct {
	// MCTable has 4 i32s for each mc unit: the offsets of its bus and its
	// state in su_mc, the offset of its constant data in su_mc_consts and
	// the offset of the voice that runs it from su_voices; that only when an
	// instrument with mc units has several voices (MCVoices), otherwise 3
	// i32s. A bus is the frame
	// the units process and the frame stored by mcloopend, 8 floats each. A
	// state is the phases of the modulation, the states of the low and high
	// decay filters (8 floats each) and the position in the ring, 128 bytes,
	// followed by the ring, frames of 8 floats.
	MCTable []uint32
	// MCConsts starts with the rates and the phase offsets of the
	// modulation of mcdelay, 8 each, in songs that modulate an mcdelay, and
	// the byte offsets of the channels, 8, at MCChannelOffsets.
	// The constant data of an mcdelay follows: its lengths and decay
	// coefficients A, B and C (8 floats each), the mask of its ring, the
	// longest delay and the allpass coefficient; of an mcmix of type shuffle
	// the byte offsets of the source channels and their signs.
	MCConsts         []uint32
	MCBytes          int
	MCChannelOffsets int
}

// wasmMCStateBytes is the size of the state of an mcdelay in the wasm player
// before its ring.
const wasmMCStateBytes = 128

func wasmMC(b *vm.Bytecode, features wasmUnitFeatures, delayMod bool) (ret wasmMCData) {
	if len(b.MCUnits) == 0 {
		return ret
	}
	f := math.Float32bits
	if delayMod {
		for _, x := range [...]float32{1, 1.125, 1.25, 1.375, 1.5, 1.625, 1.75, 1.875, 0, 0.125, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875} {
			ret.MCConsts = append(ret.MCConsts, f(x))
		}
	}
	ret.MCChannelOffsets = 4 * len(ret.MCConsts)
	for c := range sointu.MCChannels {
		ret.MCConsts = append(ret.MCConsts, uint32(4*c))
	}
	ret.MCBytes = 64 * len(b.Buses)
	for _, u := range b.MCUnits {
		state, consts := 0, 0
		switch {
		case u.Delay != nil:
			state, consts = ret.MCBytes, 4*len(ret.MCConsts)
			ret.MCBytes += wasmMCStateBytes + 4*sointu.MCChannels<<u.Delay.Log2Frames
			for _, v := range [...][sointu.MCChannels]float32{u.Delay.Lengths, u.Delay.A, u.Delay.B, u.Delay.C} {
				for _, x := range v {
					ret.MCConsts = append(ret.MCConsts, f(x))
				}
			}
			mask := uint32(1)<<u.Delay.Log2Frames - 1
			ret.MCConsts = append(ret.MCConsts, mask, f(float32(mask-1)), f(u.Delay.APGain), 0)
		case u.Type == "mcfilter":
			state = ret.MCBytes
			ret.MCBytes += 64
		case u.Shuffle != nil:
			consts = 4 * len(ret.MCConsts)
			for _, c := range u.Shuffle.Source {
				ret.MCConsts = append(ret.MCConsts, uint32(4*c))
			}
			for _, x := range u.Shuffle.Sign {
				ret.MCConsts = append(ret.MCConsts, f(x))
			}
		}
		ret.MCTable = append(ret.MCTable, uint32(64*u.Bus), uint32(state), uint32(consts))
		if features.MCVoices {
			ret.MCTable = append(ret.MCTable, uint32(u.Voice*4096))
		}
	}
	return ret
}

// wasmBuffers lays out the buffers played by the patch's bufread units in the
// wasm player: a header for each buffer, and the audio of the buffers with
// samples after each other in su_buffers.
func (com *Compiler) wasmBuffers(song *sointu.Song, b *vm.Bytecode) (ret wasmBufferData, err error) {
	index := map[uint32]int{} // buffer ID -> header index
	for _, r := range b.BufferRegions {
		i, ok := index[r.BufferID]
		if !ok {
			i = len(ret.Headers)
			index[r.BufferID] = i
			header := wasmBufferHeader{Channels: 1} // a missing buffer has no frames and is silent
			if buf, found := song.Buffers.Find(int(r.BufferID)); found && buf.Writable() {
				if buf.Channels < 1 || buf.Channels > 2 {
					return ret, fmt.Errorf("buffer %q has %d channels, expected 1 or 2", buf.Name, buf.Channels)
				}
				header = wasmBufferHeader{Offset: uint32(ret.BufferBytes), Capacity: uint32(buf.Frames), Channels: uint32(buf.Channels)}
				ret.BufferBytes += buf.Frames * buf.Channels * 4
			} else if found && buf.Sample != nil {
				enc, ok := com.Buffers[buf.ID]
				if !ok {
					return ret, fmt.Errorf("buffer %q has not been encoded", buf.Name)
				}
				if enc.Channels < 1 || enc.Channels > 2 {
					return ret, fmt.Errorf("buffer %q has %d channels, expected 1 or 2", buf.Name, enc.Channels)
				}
				var hex strings.Builder
				for _, c := range enc.Encoded {
					fmt.Fprintf(&hex, "\\%02x", c)
				}
				format := enc.Format
				if format == "" {
					format = "bin"
				}
				ret.Buffers = append(ret.Buffers, wasmBuffer{Offset: ret.BufferBytes, Frames: enc.Frames, Channels: enc.Channels, EncodedHex: hex.String(), encoded: enc.Encoded, format: format})
				header = wasmBufferHeader{Offset: uint32(ret.BufferBytes), Capacity: uint32(enc.Frames), Channels: uint32(enc.Channels), Filled: uint32(enc.Frames)}
				ret.BufferBytes += enc.Frames * enc.Channels * 4
			}
			ret.Headers = append(ret.Headers, header)
		}
		if r.Flags&vm.BufferRegionWrite != 0 && ret.Headers[i].Capacity > 0 {
			if buf, _ := song.Buffers.Find(int(r.BufferID)); !buf.Writable() {
				return ret, fmt.Errorf("a bufwrite unit writes to buffer %q, which has a sample", buf.Name)
			}
		}
		ret.Regions = append(ret.Regions, wasmBufferRegion{
			Header: uint32(i * wasmBufferHeaderSize), Start: r.Start, LoopStart: r.LoopStart,
			LoopLength: r.LoopLength, Fade: r.Fade, EdgeFade: r.EdgeFade, Flags: r.Flags,
		})
	}
	return ret, nil
}
