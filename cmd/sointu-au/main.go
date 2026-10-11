//go:build plugin && darwin

// Command sointu-au is the Sointu Audio Unit (version 2) plugin, for macOS.
// The Audio Unit ABI is implemented in au_darwin.m, which calls the exported
// functions below.
package main

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation -framework Cocoa
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

void sointu_mark_dirty(uintptr_t plugin);
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"

	"github.com/vsariola/sointu/cmd/plugin"
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

//export sointuNew
func sointuNew(p C.uintptr_t) C.uintptr_t {
	// the window opens when the host shows the view of the plugin, or after
	// a moment if the host does not ask for one: see the view in
	// au_darwin.m
	return C.uintptr_t(cgo.NewHandle(plugin.NewHidden("sointu-au", func() { C.sointu_mark_dirty(p) })))
}

// sointuManageWindow tells that the window opens with the view of the plugin
// from now on, and that the user can close it.
//
//export sointuManageWindow
func sointuManageWindow(h C.uintptr_t) { instance(h).ManageWindow(func() {}) }

//export sointuShowWindow
func sointuShowWindow(h C.uintptr_t) { instance(h).ShowWindow() }

//export sointuClose
func sointuClose(h C.uintptr_t) {
	instance(h).Close()
	cgo.Handle(h).Delete()
}

//export sointuMIDI
func sointuMIDI(h C.uintptr_t, time C.uint32_t, d0, d1, d2 C.uint8_t) {
	instance(h).MIDI(int(time), 0, [3]byte{byte(d0), byte(d1), byte(d2)})
}

// sointuSetSampleRate tells the instance the sample rate of the host and
// returns its latency at that rate, in frames.
//
//export sointuSetSampleRate
func sointuSetSampleRate(h C.uintptr_t, sampleRate C.double) C.uint32_t {
	return C.uint32_t(instance(h).SetSampleRate(float64(sampleRate)))
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
