package oto

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// deviceRate asks CoreAudio for the nominal sample rate of the default
// output device.
func deviceRate() int {
	coreAudio, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	type address struct{ selector, scope, element uint32 }
	var getPropertyData func(object uint32, address *address, qualifierSize uint32, qualifier unsafe.Pointer, size *uint32, data unsafe.Pointer) int32
	purego.RegisterLibFunc(&getPropertyData, coreAudio, "AudioObjectGetPropertyData")
	const (
		systemObject        = 1          // kAudioObjectSystemObject
		scopeGlobal         = 0x676c6f62 // 'glob'
		defaultOutputDevice = 0x644f7574 // 'dOut'
		nominalSampleRate   = 0x6e737274 // 'nsrt'
	)
	var device uint32
	size := uint32(unsafe.Sizeof(device))
	if getPropertyData(systemObject, &address{defaultOutputDevice, scopeGlobal, 0}, 0, nil, &size, unsafe.Pointer(&device)) != 0 || device == 0 {
		return 0
	}
	var rate float64
	size = uint32(unsafe.Sizeof(rate))
	if getPropertyData(device, &address{nominalSampleRate, scopeGlobal, 0}, 0, nil, &size, unsafe.Pointer(&rate)) != 0 {
		return 0
	}
	return int(rate + 0.5)
}
