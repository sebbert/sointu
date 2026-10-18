//go:build plugin

// Package engine is the Go side of the VST3, CLAP and Audio Unit plugins:
// it exports the C interface of engine.h, which their C layers call. A
// release plugin imports it into its own library; the engine library of the
// dev loader (cmd/sointu-engine) is it alone.
package engine

/*
#include <stdlib.h>
#include "engine.h"

static void call_mark_dirty(sointu_callbacks *cb, uintptr_t p) {
	if (cb->mark_dirty != NULL)
		cb->mark_dirty(p);
}
static void call_params_changed(sointu_callbacks *cb, uintptr_t p, bool values) {
	if (cb->params_changed != NULL)
		cb->params_changed(p, values);
}
static void call_window_closed(sointu_callbacks *cb, uintptr_t p) {
	if (cb->window_closed != NULL)
		cb->window_closed(p);
}
static void call_request_reload(sointu_callbacks *cb, uintptr_t p) {
	if (cb->request_reload != NULL)
		cb->request_reload(p);
}
*/
import "C"

import (
	"runtime/cgo"
	"runtime/debug"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/vsariola/sointu/cmd/plugin"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/version"
)

// instance is a plugin instance and what it tells the C layer with.
type instance struct {
	*plugin.Instance
	plugin C.uintptr_t
	cb     *C.sointu_callbacks // in C memory, freed by sointuClose
}

type processContext struct {
	sampleRate float64
	bpm        float64
	hasBPM     bool
}

func (c *processContext) BPM() (bpm float64, ok bool) { return c.bpm, c.hasBPM }

func (c *processContext) SampleRate() (samplerate float64, ok bool) {
	return c.sampleRate, c.sampleRate > 0
}

func get(h C.uintptr_t) *instance { return cgo.Handle(h).Value().(*instance) }

// instances counts the instances open in this library
var instances atomic.Int32

// freeAfterClose is how long after its last instance closed the library
// gives its memory back
const freeAfterClose = 3 * time.Second

// freeIfUnused gives the memory of closed instances back to the system once
// no instance is open: an engine that the dev loader replaced stays loaded,
// and idle, its garbage collector would not run again for minutes.
func freeIfUnused() {
	time.AfterFunc(freeAfterClose, func() {
		if instances.Load() == 0 {
			debug.FreeOSMemory()
		}
	})
}

//export sointuEngineABI
func sointuEngineABI() C.int32_t { return C.SOINTU_ENGINE_ABI }

//export sointuVersion
func sointuVersion() *C.char {
	v := version.Version
	if v == "" {
		v = "dev"
	}
	return C.CString(v) // never freed
}

//export sointuMIDIPorts
func sointuMIDIPorts() C.int32_t { return tracker.MAX_MIDI_PORTS }

//export sointuParams
func sointuParams() C.int32_t { return plugin.NumParams }

//export sointuParamRange
func sointuParamRange() C.double { return plugin.ParamRange }

// sointuNew makes an instance whose window opens when the host shows it, or
// after a moment if the host does not: see plugin.NewHidden.
//
//export sointuNew
func sointuNew(p C.uintptr_t, kind *C.char, cb *C.sointu_callbacks) C.uintptr_t {
	i := &instance{plugin: p, cb: (*C.sointu_callbacks)(C.malloc(C.size_t(unsafe.Sizeof(*cb))))}
	*i.cb = *cb
	i.Instance = plugin.NewHidden(C.GoString(kind),
		func() { C.call_mark_dirty(i.cb, p) },
		func(values bool) { C.call_params_changed(i.cb, p, C.bool(values)) },
	)
	if i.cb.request_reload != nil {
		i.EnableReload(func() { C.call_request_reload(i.cb, p) })
	}
	instances.Add(1)
	return C.uintptr_t(cgo.NewHandle(i))
}

//export sointuClose
func sointuClose(h C.uintptr_t) {
	i := get(h)
	i.Close()
	cgo.Handle(h).Delete()
	// the instance tells the C layer nothing more from Close on
	C.free(unsafe.Pointer(i.cb))
	if instances.Add(-1) == 0 {
		freeIfUnused()
	}
}

// sointuManageWindow tells that the host shows and hides the window from now
// on; the host is told when the user closes it, if it has a call for that.
//
//export sointuManageWindow
func sointuManageWindow(h C.uintptr_t) {
	i := get(h)
	i.ManageWindow(func() { C.call_window_closed(i.cb, i.plugin) })
}

//export sointuShowWindow
func sointuShowWindow(h C.uintptr_t) { get(h).ShowWindow() }

//export sointuHideWindow
func sointuHideWindow(h C.uintptr_t) { get(h).HideWindow() }

//export sointuWindowTitle
func sointuWindowTitle(h C.uintptr_t, title *C.char) { get(h).SetWindowTitle(C.GoString(title)) }

//export sointuWindowShown
func sointuWindowShown(h C.uintptr_t) C.bool { return C.bool(get(h).WindowShown()) }

//export sointuMIDI
func sointuMIDI(h C.uintptr_t, time C.int32_t, port C.int32_t, d0, d1, d2 C.uint8_t) {
	get(h).MIDI(int(time), int(port), [3]byte{byte(d0), byte(d1), byte(d2)})
}

// sointuParam handles a value, or with mod a modulation, that the host
// gives a parameter time frames into the next block.
//
//export sointuParam
func sointuParam(h C.uintptr_t, time C.int32_t, id C.int32_t, value C.double, mod C.bool) {
	get(h).Param(int(time), int(id), float64(value), bool(mod))
}

//export sointuParamValue
func sointuParamValue(h C.uintptr_t, id C.int32_t) C.double {
	return C.double(get(h).ParamValue(int(id)))
}

//export sointuParamText
func sointuParamText(h C.uintptr_t, id C.int32_t, value C.double) *C.char {
	return C.CString(get(h).ParamText(int(id), float64(value)))
}

//export sointuParamFromSteps
func sointuParamFromSteps(h C.uintptr_t, id C.int32_t, steps C.double) C.double {
	return C.double(get(h).ParamFromSteps(int(id), float64(steps)))
}

//export sointuParamName
func sointuParamName(h C.uintptr_t, id C.int32_t) *C.char {
	return C.CString(get(h).ParamName(int(id)))
}

//export sointuSetSampleRate
func sointuSetSampleRate(h C.uintptr_t, rate C.double) C.uint32_t {
	return C.uint32_t(get(h).SetSampleRate(float64(rate)))
}

//export sointuProcess
func sointuProcess(h C.uintptr_t, left, right *C.float, frames C.int32_t, rate C.double, hasTempo C.bool, tempo C.double) {
	n := int(frames)
	ctx := processContext{sampleRate: float64(rate), bpm: float64(tempo), hasBPM: bool(hasTempo)}
	get(h).Process(
		unsafe.Slice((*float32)(unsafe.Pointer(left)), n),
		unsafe.Slice((*float32)(unsafe.Pointer(right)), n),
		&ctx,
	)
}

//export sointuState
func sointuState(h C.uintptr_t, size *C.size_t) unsafe.Pointer {
	data := get(h).State()
	if data == nil {
		*size = 0
		return nil
	}
	*size = C.size_t(len(data))
	return C.CBytes(data)
}

//export sointuSetState
func sointuSetState(h C.uintptr_t, data unsafe.Pointer, size C.size_t) {
	get(h).SetState(C.GoBytes(data, C.int(size)))
}

//export sointuReloadAvailable
func sointuReloadAvailable(h C.uintptr_t, build *C.char) {
	get(h).ReloadAvailable(C.GoString(build))
}

//export sointuAlert
func sointuAlert(h C.uintptr_t, message *C.char) { get(h).Alert(C.GoString(message)) }
