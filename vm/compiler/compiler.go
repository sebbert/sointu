package compiler

import (
	"bytes"
	"embed"
	"fmt"
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
}

// EncodedBuffer is the sample of a buffer encoded for the compiled player,
// with the length and channels of its decoded audio.
type EncodedBuffer struct {
	Encoded  []byte
	Frames   int
	Channels int
}

// wasmBuffer is a buffer in the wasm player: its encoded sample goes in a
// custom section, which the host decodes, and the decoded audio is stored at
// Offset bytes from su_buffers.
type wasmBuffer struct {
	Offset, Frames, Channels int
	EncodedHex               string
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
	var templates []string
	if com.Arch == "386" || com.Arch == "amd64" {
		templates = []string{"player.asm", "player.h", "player.inc"}
	} else if com.Arch == "wasm" {
		templates = []string{"player.wat"}
	}
	features := vm.NecessaryFeaturesFor(song.Patch)
	for _, unit := range features.Instructions() {
		wasmOnly := len(sointu.SpectrumBufferParams(unit)) > 0
		switch unit {
		case "bufread", "bufwrite", "spawn", "arg", "window":
			wasmOnly = true
		}
		if wasmOnly && com.Arch != "wasm" {
			return nil, nil, fmt.Errorf(`the %v unit is only supported when compiling for wasm (targeted architecture was %v)`, unit, com.Arch)
		}
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
			}{compilerMacros, featureSetMacros, wasmMacros, songMacros, encodedPatch, patterns, sequences, len(patterns[0]), len(sequences[0]), 1, buffers, wasmSpectral(encodedPatch)}
			populatedTemplate, extension, err = com.compile(templateName, &data)
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
	// logarithm of its size, the number of spectra written to it and 0.
	SpectrumTable []uint32
	// SpectralTable has 4 i32s for each spectral unit: the offset of the
	// voice that runs it from su_voices, the offset of its state, and the
	// offsets of its spectrum and source spectrum in SpectrumTable. The state
	// is the position in its ring and the count of the spectrum it processed
	// last, 16 bytes, followed by the ring for spfft and spifft.
	SpectralTable []uint32
	// SpectralBytes is the size of su_spectral. After the spectra and the
	// states of the units, it has a scratch space at SpectralScratch for the
	// largest spectrum, of size 2^SpectralMaxLog2, and the tables computed
	// when the player starts, like spectralTables in the vm package: the Hann
	// window of the largest size at SpectralHann, and the twiddle factors
	// at SpectralTwiddles, each as the pair wr, wr, followed by the pairs
	// -wi, wi at SpectralTwiddles + SpectralTwiddleBytes.
	SpectralBytes, SpectralScratch, SpectralMaxLog2      int
	SpectralHann, SpectralTwiddles, SpectralTwiddleBytes int
	SpectralMaxSize                                      int
}

const wasmSpectrumTableStride = 16

func wasmSpectral(b *vm.Bytecode) (ret wasmSpectralData) {
	offset := 0
	for _, sp := range b.Spectra {
		ret.SpectrumTable = append(ret.SpectrumTable, uint32(offset), uint32(sp.Log2Size), 0, 0)
		offset += 2 * (1 << sp.Log2Size) * 4
		ret.SpectralMaxLog2 = max(ret.SpectralMaxLog2, sp.Log2Size)
	}
	for _, u := range b.SpectralUnits {
		source := 0
		if u.Source >= 0 {
			source = u.Source * wasmSpectrumTableStride
		}
		ret.SpectralTable = append(ret.SpectralTable, uint32(u.Voice*4096), uint32(offset), uint32(u.Spectrum*wasmSpectrumTableStride), uint32(source))
		offset += 16
		switch u.Type {
		case "spfft", "spifft":
			offset += (1 << b.Spectra[u.Spectrum].Log2Size) * 4
		case "spblur":
			offset += (1<<b.Spectra[u.Spectrum].Log2Size + 2) * 4
		}
	}
	maxSize := 1 << ret.SpectralMaxLog2
	ret.SpectralMaxSize = maxSize
	ret.SpectralScratch = offset
	ret.SpectralHann = ret.SpectralScratch + 2*maxSize*4
	ret.SpectralTwiddles = ret.SpectralHann + maxSize*4
	ret.SpectralTwiddleBytes = (maxSize - 1) * 8
	ret.SpectralBytes = ret.SpectralTwiddles + 2*ret.SpectralTwiddleBytes
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
				ret.Buffers = append(ret.Buffers, wasmBuffer{Offset: ret.BufferBytes, Frames: enc.Frames, Channels: enc.Channels, EncodedHex: hex.String()})
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
