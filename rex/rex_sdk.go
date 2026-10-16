//go:build rex && cgo && darwin

package rex

// The binding to the REX Shared Library, built with the tag rex and the REX
// SDK of Reason Studios, which is not part of this repository: CGO_CFLAGS
// has -I with the folder of the SDK, whose REX.c loads the library at run
// time from the folder of "REX Shared Library.bundle". That is looked for in
// $SOINTU_REX_DIR, and else next to the binary that this code is in and in
// the Frameworks and Resources folders of its bundle, where the build puts
// it: a plugin must carry it, never take it from its host.

/*
#cgo CFLAGS: -DREX_MAC=1 -DREX_WINDOWS=0 -DREX_DLL_LOADER=1 -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreFoundation
#include <dlfcn.h>
#include <stdlib.h>
#include "REX.c"

static const char* rexSelfPath(void) {
	Dl_info info;
	if (dladdr((void*)rexSelfPath, &info) == 0) {
		return NULL;
	}
	return info.dli_fname;
}

static REXError rexRender(REXHandle h, REX_int32_t slice, REX_int32_t frames, float* left, float* right) {
	float* buffers[2] = {left, right};
	return REXRenderSlice(h, slice, frames, buffers);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

var (
	mu      sync.Mutex // the library is used from one goroutine at a time
	loaded  bool
	loadErr error
)

// Available reports whether REX files can be loaded: the library is found
// and loads.
func Available() bool {
	mu.Lock()
	defer mu.Unlock()
	return load() == nil
}

// libraryDirs are the folders where the library is looked for.
func libraryDirs() []string {
	var dirs []string
	if d := os.Getenv("SOINTU_REX_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if p := C.rexSelfPath(); p != nil {
		self := filepath.Dir(C.GoString(p))
		dirs = append(dirs, self, filepath.Join(self, "..", "Frameworks"), filepath.Join(self, "..", "Resources"))
	}
	return dirs
}

func load() error {
	if loaded || loadErr != nil {
		return loadErr
	}
	loadErr = ErrUnavailable
	for _, dir := range libraryDirs() {
		if _, err := os.Stat(filepath.Join(dir, "REX Shared Library.bundle")); err != nil {
			continue
		}
		cdir := C.CString(dir)
		res := C.REXInitializeDLL_DirPath(cdir)
		C.free(unsafe.Pointer(cdir))
		if res == C.kREXError_NoError {
			loaded, loadErr = true, nil
			return nil
		}
		loadErr = fmt.Errorf("loading the REX Shared Library from %s: %w", dir, rexError(res))
	}
	return loadErr
}

// Decode renders the slices of a REX file at 44100 Hz.
func Decode(data []byte) (Loop, error) {
	mu.Lock()
	defer mu.Unlock()
	if err := load(); err != nil {
		return Loop{}, err
	}
	if len(data) == 0 {
		return Loop{}, errors.New("the REX file is empty")
	}
	buf := C.CBytes(data)
	defer C.free(buf)
	var h C.REXHandle
	if res := C.REXCreate(&h, (*C.char)(buf), C.REX_int32_t(len(data)), nil, nil); res != C.kREXError_NoError {
		return Loop{}, rexError(res)
	}
	defer C.REXDelete(&h)
	if res := C.REXSetOutputSampleRate(h, 44100); res != C.kREXError_NoError {
		return Loop{}, rexError(res)
	}
	var info C.REXInfo
	if res := C.REXGetInfo(h, C.REX_int32_t(unsafe.Sizeof(info)), &info); res != C.kREXError_NoError {
		return Loop{}, rexError(res)
	}
	ch := int(info.fChannels)
	if ch < 1 || ch > 2 {
		return Loop{}, fmt.Errorf("the REX file has %d channels", ch)
	}
	loop := Loop{Channels: ch, Tempo: float64(info.fTempo) / 1000, PPQLength: int(info.fPPQLength)}
	for i := range int(info.fSliceCount) {
		var si C.REXSliceInfo
		if res := C.REXGetSliceInfo(h, C.REX_int32_t(i), C.REX_int32_t(unsafe.Sizeof(si)), &si); res != C.kREXError_NoError {
			return Loop{}, rexError(res)
		}
		frames := int(si.fSampleLength)
		if frames <= 0 {
			continue
		}
		planes := (*C.float)(C.malloc(C.size_t(4 * frames * ch)))
		left := unsafe.Slice(planes, frames*ch)
		var right *C.float
		if ch == 2 {
			right = &left[frames]
		}
		res := C.rexRender(h, C.REX_int32_t(i), C.REX_int32_t(frames), planes, right)
		if res != C.kREXError_NoError {
			C.free(unsafe.Pointer(planes))
			return Loop{}, rexError(res)
		}
		audio := make([]float32, frames*ch)
		for f := range frames {
			for c := range ch {
				audio[f*ch+c] = float32(left[c*frames+f])
			}
		}
		C.free(unsafe.Pointer(planes))
		loop.Slices = append(loop.Slices, Slice{PPQ: int(si.fPPQPos), Audio: audio})
	}
	return loop, nil
}

// rexError returns the error of a result of the library.
func rexError(res C.REXError) error {
	switch res {
	case C.kREXError_OutOfMemory:
		return errors.New("REX: out of memory")
	case C.kREXError_FileCorrupt:
		return errors.New("REX: the file is corrupt")
	case C.kREXError_REX2FileTooNew:
		return errors.New("REX: the file is from a newer version of ReCycle")
	case C.kREXError_FileHasZeroLoopLength:
		return errors.New("REX: the loop has no length")
	case C.kREXError_DLLNotFound, C.kREXError_UnableToLoadDLL:
		return ErrUnavailable
	case C.kREXError_DLLTooOld:
		return errors.New("REX: the REX Shared Library is too old")
	case C.kREXError_OSVersionNotSupported:
		return errors.New("REX: this version of macOS is not supported")
	}
	return fmt.Errorf("REX: error %d", int(res))
}
