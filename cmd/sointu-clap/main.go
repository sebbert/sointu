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

void sointu_mark_dirty(uintptr_t plugin);
void sointu_params_changed(uintptr_t plugin, bool values);
void sointu_window_closed(uintptr_t plugin);
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
func sointuNew(p C.uintptr_t) C.uintptr_t {
	// the window opens when the host shows it, or after a moment if the
	// host does not take it over: see the gui extension in clap.c
	return C.uintptr_t(cgo.NewHandle(plugin.NewHidden("sointu-clap",
		func() { C.sointu_mark_dirty(p) },
		func(values bool) { C.sointu_params_changed(p, C.bool(values)) },
	)))
}

// sointuManageWindow tells that the host shows and hides the window from now
// on; the host is told when the user closes it.
//
//export sointuManageWindow
func sointuManageWindow(h C.uintptr_t, p C.uintptr_t) {
	instance(h).ManageWindow(func() { C.sointu_window_closed(p) })
}

//export sointuShowWindow
func sointuShowWindow(h C.uintptr_t) { instance(h).ShowWindow() }

//export sointuHideWindow
func sointuHideWindow(h C.uintptr_t) { instance(h).HideWindow() }

//export sointuWindowTitle
func sointuWindowTitle(h C.uintptr_t, title *C.char) {
	instance(h).SetWindowTitle(C.GoString(title))
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

//export sointuParams
func sointuParams() C.uint32_t { return plugin.NumParams }

//export sointuParamRange
func sointuParamRange() C.double { return plugin.ParamRange }

// sointuParam handles a value, or with mod a modulation, that the host
// gives a parameter time frames into the next block.
//
//export sointuParam
func sointuParam(h C.uintptr_t, time C.uint32_t, id C.uint32_t, value C.double, mod C.bool) {
	instance(h).Param(int(time), int(id), float64(value), bool(mod))
}

//export sointuParamValue
func sointuParamValue(h C.uintptr_t, id C.uint32_t) C.double {
	return C.double(instance(h).ParamValue(int(id)))
}

// sointuParamText returns a value that the host has for a parameter as
// text, in memory allocated with malloc, to be freed by the caller.
//
//export sointuParamText
func sointuParamText(h C.uintptr_t, id C.uint32_t, value C.double) *C.char {
	return C.CString(instance(h).ParamText(int(id), float64(value)))
}

// sointuParamFromSteps returns the value that the host has for a parameter
// at the steps that sointuParamText shows.
//
//export sointuParamFromSteps
func sointuParamFromSteps(h C.uintptr_t, id C.uint32_t, steps C.double) C.double {
	return C.double(instance(h).ParamFromSteps(int(id), float64(steps)))
}

// sointuParamName returns the name of a parameter in memory allocated with
// malloc, to be freed by the caller.
//
//export sointuParamName
func sointuParamName(h C.uintptr_t, id C.uint32_t) *C.char {
	return C.CString(instance(h).ParamName(int(id)))
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
