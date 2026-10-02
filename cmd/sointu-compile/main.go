package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
	"github.com/vsariola/sointu/version"
	"github.com/vsariola/sointu/vm/compiler"
)

func filterExtensions(input map[string]string, extensions []string) map[string]string {
	ret := map[string]string{}
	for _, ext := range extensions {
		extWithDot := "." + ext
		if inputVal, ok := input[extWithDot]; ok {
			ret[extWithDot] = inputVal
		}
	}
	return ret
}

func main() {
	safe := flag.Bool("n", false, "Never overwrite files; if file already exists and would be overwritten, give an error.")
	list := flag.Bool("l", false, "Do not write files; just list files that would change instead.")
	stdout := flag.Bool("s", false, "Do not write files; write to standard output instead.")
	help := flag.Bool("h", false, "Show help.")
	rowsync := flag.Bool("r", false, "Write the current fractional row as sync #0")
	library := flag.Bool("a", false, "Compile Sointu into a library. Input files are not needed.")
	jsonOut := flag.Bool("j", false, "Output the song as .json file instead of compiling.")
	yamlOut := flag.Bool("y", false, "Output the song as .yml file instead of compiling.")
	tmplDir := flag.String("t", "", "When compiling, use the templates in this directory instead of the standard templates.")
	outPath := flag.String("o", "", "Directory or filename where to write compiled code. Extension is ignored. Directory and its parents are created if needed. By default, everything is placed in the same directory where the original song file is.")
	extensionsOut := flag.String("e", "", "Output only the compiled files with these comma separated extensions. For example: h,asm")
	targetArch := flag.String("arch", runtime.GOARCH, "Target architecture. Defaults to OS architecture. Possible values: 386, amd64, wasm")
	output16bit := flag.Bool("i", false, "Compiled song should output 16-bit integers, instead of floats.")
	targetOs := flag.String("os", runtime.GOOS, "Target OS. Defaults to current OS. Possible values: windows, darwin, linux. Anything else exits with error code. Ignored when targeting wasm.")
	versionFlag := flag.Bool("v", false, "Print version.")
	mathImports := flag.Bool("imports", false, "Make the wasm player call Math.pow and Math.sin of JavaScript instead of computing them itself: a smaller player, whose output differs slightly from the Go synth, as used by the tracker, and between browsers.")
	js := flag.Bool("js", false, "For wasm: also write a JavaScript module (.js and .d.ts) that renders the song in the background and plays it while it renders. The player then renders in parts when asked, instead of the whole song when instantiated.")
	stages := flag.Int("stages", 0, "With -js: render in a pipeline of up to this many workers, each running a part of the voices. The output is the same.")
	stageCuts := flag.String("cuts", "", "With -js: the first voices of the stages after the first, comma separated, instead of the balanced stages of -stages.")
	outputClock := flag.Bool("outputclock", false, "With -js: time() of the module is the clock of the output, from AudioContext.getOutputTimestamp() carried on with performance.now(): behind the time of the audio context by the output latency, and moving between audio blocks. By default it is the time of the audio context.")
	separateSamples := flag.Bool("samples", false, "For wasm: write the encoded samples of the buffers as separate files (.0.<format>, .1.<format>, ...) instead of custom sections of the wasm, e.g. to pack them as already compressed files.")
	ffmpegPath := flag.String("ffmpeg", "", "Path of ffmpeg, for encoding the samples of songs that play buffers. By default, $"+ffmpeg.EnvVar+", PATH and common installation directories are searched.")
	allowUnknown := flag.Bool("allow-unknown-units", false, "Compile songs with units of a type that this version does not have, e.g. songs of a newer version, without those units, with a warning for each. By default such songs are an error.")
	flag.Usage = printUsage
	flag.Parse()
	// Validate and guard against some oddly specific typos:
	if *targetOs != "windows" && *targetOs != "linux" && *targetOs != "darwin" {
		fmt.Fprintf(os.Stderr, "error: invalid OS: `%s` (must be `windows`, `linux` or `darwin`).\n", *targetOs)
		os.Exit(1)
	}
	if *targetArch != "386" && *targetArch != "amd64" && *targetArch != "wasm" {
		fmt.Fprintf(os.Stderr, "error: invalid target architecture: `%s` (must be `386`, `amd64` or `wasm`).\n", *targetOs)
		os.Exit(1)
	}
	if *versionFlag {
		fmt.Println(version.VersionOrHash)
		os.Exit(0)
	}
	if (flag.NArg() == 0 && !*library) || *help {
		flag.Usage()
		os.Exit(0)
	}
	compile := !*jsonOut && !*yamlOut // if the user gives nothing to output, then the default behaviour is to compile the file
	var comp *compiler.Compiler
	if compile || *library {
		var err error
		if *tmplDir != "" {
			comp, err = compiler.NewFromTemplates(*targetOs, *targetArch, *output16bit, *rowsync, *tmplDir)
		} else {
			comp, err = compiler.New(*targetOs, *targetArch, *output16bit, *rowsync)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, `error creating compiler: %v`, err)
			os.Exit(1)
		}
		comp.MathImports = *mathImports
		comp.JS = *js
		comp.Stages = *stages
		comp.OutputClock = *outputClock
		comp.SeparateSamples = *separateSamples
		if *stageCuts != "" {
			for _, c := range strings.Split(*stageCuts, ",") {
				v, err := strconv.Atoi(strings.TrimSpace(c))
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: invalid -cuts: %v\n", err)
					os.Exit(1)
				}
				comp.StageCuts = append(comp.StageCuts, v)
			}
		}
		comp.Log = func(s string) { fmt.Fprintln(os.Stderr, s) }
	}
	output := func(filename string, extension string, contents []byte) error {
		if *stdout {
			fmt.Print(string(contents))
			return nil
		}
		_, name := filepath.Split(filename)
		var dir string
		if *outPath != "" {
			// check if it's an already existing directory and the user just forgot trailing slash
			if info, err := os.Stat(*outPath); err == nil && info.IsDir() {
				dir = *outPath
			} else {
				outdir, outname := filepath.Split(*outPath)
				if outdir != "" {
					dir = outdir
				}
				if outname != "" {
					name = outname
				}
			}
		}
		if dir == "" {
			var err error
			dir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("could not get working directory, specify the output directory explicitly: %v", err)
			}
		}
		name = strings.TrimSuffix(name, filepath.Ext(name)) + extension
		f := filepath.Join(dir, name)
		original, err := ioutil.ReadFile(f)
		if err == nil {
			if bytes.Compare(original, contents) == 0 {
				return nil // no need to update
			}
			if !*list && *safe {
				return fmt.Errorf("file %v would be overwritten by compiler", f)
			}
		}
		if *list {
			fmt.Println(f)
		} else {
			if dir != "" {
				if err := os.MkdirAll(dir, os.ModePerm); err != nil {
					return fmt.Errorf("could not create output directory %v: %v", dir, err)
				}
			}
			err := ioutil.WriteFile(f, contents, 0644)
			if err != nil {
				return fmt.Errorf("could not write file %v: %v", f, err)
			}
		}
		return nil
	}
	process := func(filename string) error {
		inputBytes, err := ioutil.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("could not read file %v: %v", filename, err)
		}
		var song sointu.Song
		if errJSON := json.Unmarshal(inputBytes, &song); errJSON != nil {
			if errYaml := yaml.Unmarshal(inputBytes, &song); errYaml != nil {
				return fmt.Errorf("song could not be unmarshaled as a .json (%v) or .yml (%v)", errJSON, errYaml)
			}
		}
		if song.RowsPerBeat == 0 {
			song.RowsPerBeat = 4
		}
		if song.Score.Length == 0 {
			song.Score.Length = len(song.Score.Tracks[0].Patterns)
		}
		var compiledPlayer map[string]string
		if compile {
			var err error
			if comp.Buffers, err = encodeBuffers(&song, *ffmpegPath); err != nil {
				return fmt.Errorf("encoding buffers failed: %v", err)
			}
			var warnings []string
			comp.AllowUnknownUnits = *allowUnknown
			compiledPlayer, warnings, err = comp.Song(&song)
			if err != nil {
				if errors.As(err, new(sointu.UnknownUnit)) {
					return fmt.Errorf("compiling player failed: %v\n(-allow-unknown-units compiles the song without these units)", err)
				}
				return fmt.Errorf("compiling player failed: %v", err)
			}
			for _, warning := range warnings {
				fmt.Fprintf(os.Stderr, "warning: %v\n", warning)
			}
			if len(*extensionsOut) > 0 {
				compiledPlayer = filterExtensions(compiledPlayer, strings.Split(*extensionsOut, ","))
			}
			for extension, code := range compiledPlayer {
				if err := output(filename, extension, []byte(code)); err != nil {
					return fmt.Errorf("error outputting %v file: %v", extension, err)
				}
			}
		}
		if *jsonOut {
			jsonSong, err := json.Marshal(song)
			if err != nil {
				return fmt.Errorf("could not marshal the song as json file: %v", err)
			}
			if err := output(filename, ".json", jsonSong); err != nil {
				return fmt.Errorf("error outputting json file: %v", err)
			}
		}
		if *yamlOut {
			yamlSong, err := yaml.Marshal(song)
			if err != nil {
				return fmt.Errorf("could not marshal the song as yaml file: %v", err)
			}
			if err := output(filename, ".yml", yamlSong); err != nil {
				return fmt.Errorf("error outputting yaml file: %v", err)
			}
		}
		return nil
	}
	retval := 0
	if *library {
		compiledLibrary, err := comp.Library()
		if err != nil {
			fmt.Fprintf(os.Stderr, "compiling library failed: %v\n", err)
			retval = 1
		} else {
			if len(*extensionsOut) > 0 {
				compiledLibrary = filterExtensions(compiledLibrary, strings.Split(*extensionsOut, ","))
			}
			for extension, code := range compiledLibrary {
				if err := output("sointu", extension, []byte(code)); err != nil {
					fmt.Fprintf(os.Stderr, "error outputting %v file: %v", extension, err)
					retval = 1
				}
			}
		}
	}
	for _, param := range flag.Args() {
		if info, err := os.Stat(param); err == nil && info.IsDir() {
			jsonfiles, err := filepath.Glob(filepath.Join(param, "*.json"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not glob the path %v for json files: %v\n", param, err)
				retval = 1
				continue
			}
			ymlfiles, err := filepath.Glob(filepath.Join(param, "*.yml"))
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not glob the path %v for yml files: %v\n", param, err)
				retval = 1
				continue
			}
			files := append(ymlfiles, jsonfiles...)
			for _, file := range files {
				err := process(file)
				if err != nil {
					fmt.Fprintf(os.Stderr, "could not process file %v: %v\n", file, err)
					retval = 1
				}
			}
		} else {
			err := process(param)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not process file %v: %v\n", param, err)
				retval = 1
			}
		}
	}
	os.Exit(retval)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Sointu compiler. Input .yml or .json songs, outputs compiled songs (e.g. .asm and .h files).\nUsage: %s [flags] [path ...]\n", os.Args[0])
	flag.PrintDefaults()
}

// encodeBuffers encodes the samples of the buffers played by the song's
// bufread units with ffmpeg, printing the ffmpeg version and the sizes. It
// returns nil if the song plays no buffers.
func encodeBuffers(song *sointu.Song, ffmpegPath string) (map[int]compiler.EncodedBuffer, error) {
	if !ffmpeg.NeedsFFmpeg(song) {
		return nil, nil
	}
	f, err := ffmpeg.Find(ffmpegPath)
	if err != nil {
		return nil, err
	}
	if v, err := f.Version(); err == nil {
		fmt.Fprintf(os.Stderr, "encoding samples with %s\n", v)
	}
	dir, _ := ffmpeg.DefaultCacheDir()
	results, err := ffmpeg.NewCache(f, dir).SongBuffers(song, func(buf sointu.Buffer, r ffmpeg.Result) {
		fmt.Fprintf(os.Stderr, "buffer %q: %d bytes, %d frames\n", buf.Name, len(r.Encoded), r.Audio.Frames())
	})
	if err != nil {
		return nil, err
	}
	ret := map[int]compiler.EncodedBuffer{}
	for id, r := range results {
		format := ""
		if buf, ok := song.Buffers.Find(id); ok {
			if enc, err := song.SampleEncoding(buf.Sample); err == nil {
				format = enc.Format
			}
		}
		ret[id] = compiler.EncodedBuffer{Encoded: r.Encoded, Frames: r.Audio.Frames(), Channels: r.Audio.Channels, Format: format}
	}
	return ret, nil
}
