package tracker

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/ffmpeg"
)

// The preset watcher picks up the presets of the user that were added,
// changed or deleted on disk while the tracker runs: the instrument presets
// (<config dir>/sointu/presets, all of its folders), the module presets
// (<config dir>/sointu/modules) and the encoding presets
// (<config dir>/sointu/encoding-presets.yml). Every presetWatchInterval it
// stats the files there, on a goroutine; when they differ from the last
// time, it reads the presets of that kind again, also on the goroutine, and
// the model puts them in place. The presets that the tracker comes with are
// in the binary and do not change.

// presetWatchInterval is how often the watcher looks at the preset folders.
var presetWatchInterval = time.Second

type (
	// presetPaths are the files and folders of the presets of the user that
	// the watcher looks at; an empty one is not watched.
	presetPaths struct {
		instruments string // the root of presets/, as presetData.userRoot
		modules     string
		encoding    string
	}

	// presetStamps tell whether the presets of each kind changed.
	presetStamps struct {
		instruments, modules, encoding uint64
	}
)

// presetPaths returns the paths that the model reads the presets of the
// user from.
func (m *Model) presetPaths() presetPaths {
	var p presetPaths
	if dir, ok := m.presetData.userPresetsDir(); ok {
		p.instruments = filepath.Dir(dir)
	}
	if dir, ok := m.modulePresetDir(); ok {
		p.modules = dir
	}
	if m.encodingPresetPath != "" {
		p.encoding = m.encodingPresetPath
	} else if file, ok := ffmpeg.UserPresetsPath(); ok {
		p.encoding = file
	}
	return p
}

func stampPresets(p presetPaths) presetStamps {
	s := presetStamps{modules: stampTree(p.modules), encoding: stampTree(p.encoding)}
	if p.instruments != "" {
		s.instruments = stampTree(filepath.Join(p.instruments, "presets"))
	}
	return s
}

// stampTree hashes the names, sizes and times of change of the file or the
// folder at path and of everything below it. A path that does not exist
// hashes to 0.
func stampTree(path string) uint64 {
	if path == "" {
		return 0
	}
	if _, err := os.Stat(path); err != nil {
		return 0
	}
	h := fnv.New64a()
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := os.Stat(p) // through symbolic links
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return h.Sum64() | 1 // not 0, which is a missing path
}

// watchPresets starts the preset watcher. The presets as they are on disk
// now are the ones that it compares against: it is started before they are
// first read, so that no change between goes unseen.
func (m *Model) watchPresets() {
	if m.presetWatchCancel != nil {
		return
	}
	paths := m.presetPaths()
	last := stampPresets(paths)
	ctx, cancel := context.WithCancel(context.Background())
	m.presetWatchCancel = cancel
	broker := m.broker
	go func() {
		ticker := time.NewTicker(presetWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// stamped before reading: a change while reading is seen next time
			now := stampPresets(paths)
			if now.instruments != last.instruments {
				p := presetData{userRoot: paths.instruments}
				p.load()
				if !TrySend(broker.ToModel, MsgToModel{Data: func() { m.Preset().replacePresets(p) }}) {
					now.instruments = last.instruments // try again
				}
			}
			if now.modules != last.modules {
				presets, user := readModulePresets(paths.modules, paths.modules != "")
				if !TrySend(broker.ToModel, MsgToModel{Data: func() { m.modulePresets, m.userModulePresets = presets, user }}) {
					now.modules = last.modules
				}
			}
			if now.encoding != last.encoding {
				presets, err := ffmpeg.PresetsFrom(paths.encoding)
				if !TrySend(broker.ToModel, MsgToModel{Data: func() { m.setEncodingPresets(presets, err) }}) {
					now.encoding = last.encoding
				}
			}
			if ctx.Err() == nil {
				last = now
			}
		}
	}()
}

// stopWatchingPresets stops the preset watcher.
func (m *Model) stopWatchingPresets() {
	if m.presetWatchCancel != nil {
		m.presetWatchCancel()
		m.presetWatchCancel = nil
	}
}

// setEncodingPresets puts the encoding presets read again in place, alerting
// about an error in the user's file.
func (m *Model) setEncodingPresets(presets sointu.EncodingPresets, err error) {
	m.defaultPresets = presets
	if err != nil {
		m.Alerts().Add("Encoding presets: "+err.Error(), Warning)
	}
}
