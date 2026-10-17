package ffmpeg

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vsariola/sointu"
	"gopkg.in/yaml.v3"
)

//go:embed presets.yml
var builtinPresets []byte

// UserPresetsFile is the name of the user's default encoding presets file in
// the sointu config directory.
const UserPresetsFile = "encoding-presets.yml"

// DefaultPreset is the name of the preset used for new samples.
const DefaultPreset = "Opus 24k"

// Presets returns the default encoding presets that songs start with: the
// built-in presets followed by the user's presets from
// <config dir>/sointu/encoding-presets.yml. A user preset with the name of a
// built-in one replaces it. A missing user file is not an error; a malformed
// one is, but the built-in presets are still returned.
func Presets() (sointu.EncodingPresets, error) {
	file, _ := UserPresetsPath()
	return PresetsFrom(file)
}

// UserPresetsPath returns the path of the user's encoding presets file,
// <config dir>/sointu/encoding-presets.yml.
func UserPresetsPath() (string, bool) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "sointu", UserPresetsFile), true
}

// PresetsFrom is Presets with the user's presets read from file; an empty
// file name reads none.
func PresetsFrom(file string) (sointu.EncodingPresets, error) {
	var ret sointu.EncodingPresets
	if err := yaml.Unmarshal(builtinPresets, &ret); err != nil {
		panic(fmt.Sprintf("invalid built-in encoding presets: %v", err)) // a bug
	}
	if file == "" {
		return ret, nil
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return ret, nil
	}
	if err != nil {
		return ret, err
	}
	var user sointu.EncodingPresets
	if err := yaml.Unmarshal(data, &user); err != nil {
		return ret, fmt.Errorf("%s: %w", UserPresetsFile, err)
	}
outer:
	for _, u := range user {
		for i := range ret {
			if ret[i].Name == u.Name {
				ret[i] = u
				continue outer
			}
		}
		ret = append(ret, u)
	}
	return ret, nil
}
