//go:build plugin

// Command sointu-clap is the Sointu CLAP plugin. The CLAP ABI is implemented in
// clap.c, which calls the exported functions below.
package main

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/clap/include
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"

	"github.com/vsariola/sointu/cmd/plugin"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/version"
)

type processContext struct {
	sampleRate float64
	bpm        float64
	hasBPM     bool
}

func (c *processContext) BPM() (bpm float64, ok bool) { return c.bpm, c.hasBPM }

func (c *processContext) SampleRate() (samplerate float64, ok bool) {
	return c.sampleRate, c.sampleRate > 0
}

func instance(h C.uintptr_t) *plugin.Instance {
	return cgo.Handle(h).Value().(*plugin.Instance)
}

//export sointuMIDIPorts
func sointuMIDIPorts() C.uint32_t { return tracker.MAX_MIDI_PORTS }

//export sointuVersion
func sointuVersion() *C.char {
	v := version.Version
	if v == "" {
		v = "dev"
	}
	return C.CString(v) // never freed; lives as long as the plugin descriptor
}

//export sointuNew
func sointuNew() C.uintptr_t {
	return C.uintptr_t(cgo.NewHandle(plugin.New("sointu-clap")))
}

//export sointuClose
func sointuClose(h C.uintptr_t) {
	instance(h).Close()
	cgo.Handle(h).Delete()
}

//export sointuMIDI
func sointuMIDI(h C.uintptr_t, time C.uint32_t, port C.int, d0, d1, d2 C.uint8_t) {
	instance(h).MIDI(int(time), int(port), [3]byte{byte(d0), byte(d1), byte(d2)})
}

//export sointuProcess
func sointuProcess(h C.uintptr_t, left, right *C.float, frames C.uint32_t, sampleRate C.double, hasTempo C.bool, tempo C.double) {
	n := int(frames)
	ctx := processContext{sampleRate: float64(sampleRate), bpm: float64(tempo), hasBPM: bool(hasTempo)}
	instance(h).Process(
		unsafe.Slice((*float32)(unsafe.Pointer(left)), n),
		unsafe.Slice((*float32)(unsafe.Pointer(right)), n),
		&ctx,
	)
}

// sointuState returns the state in memory allocated with malloc, to be freed
// by the caller, or NULL on failure.
//
//export sointuState
func sointuState(h C.uintptr_t) (unsafe.Pointer, C.size_t) {
	data := instance(h).State()
	if data == nil {
		return nil, 0
	}
	return C.CBytes(data), C.size_t(len(data))
}

//export sointuSetState
func sointuSetState(h C.uintptr_t, data unsafe.Pointer, size C.size_t) {
	instance(h).SetState(C.GoBytes(data, C.int(size)))
}

func main() {}
