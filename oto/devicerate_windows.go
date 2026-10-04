package oto

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// deviceRate asks WASAPI for the sample rate of the mix format of the
// default output device: the rate that Windows mixes the device at.
func deviceRate() (rate int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err {
	case nil, syscall.Errno(windows.S_FALSE):
		defer windows.CoUninitialize()
	case syscall.Errno(windows.RPC_E_CHANGED_MODE):
		// the thread has COM in another mode, which does as well
	default:
		return 0
	}
	var (
		clsidMMDeviceEnumerator = windows.GUID{Data1: 0xbcde0395, Data2: 0xe52f, Data3: 0x467c, Data4: [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
		iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xa95664d2, Data2: 0x9614, Data3: 0x4f35, Data4: [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
		iidIAudioClient         = windows.GUID{Data1: 0x1cb9ad4c, Data2: 0xdbfa, Data3: 0x4c32, Data4: [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	)
	const (
		clsctxAll = 0x17
		eRender   = 0
		eConsole  = 0
		// places of the methods in the tables of the interfaces
		release                 = 2
		getDefaultAudioEndpoint = 4 // IMMDeviceEnumerator
		activate                = 3 // IMMDevice
		getMixFormat            = 8 // IAudioClient
	)
	// call calls a method of a COM object and tells if it succeeded
	call := func(object unsafe.Pointer, method int, args ...uintptr) bool {
		table := *(**[16]uintptr)(object)
		r, _, _ := syscall.SyscallN(table[method], append([]uintptr{uintptr(object)}, args...)...)
		return int32(r) >= 0
	}
	var enumerator, device, client unsafe.Pointer
	r, _, _ := windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance").Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator)))
	if int32(r) < 0 || enumerator == nil {
		return 0
	}
	defer call(enumerator, release)
	if !call(enumerator, getDefaultAudioEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&device))) || device == nil {
		return 0
	}
	defer call(device, release)
	if !call(device, activate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client))) || client == nil {
		return 0
	}
	defer call(client, release)
	// a WAVEFORMATEX: the format tag, the channels, then the rate
	var format *struct {
		formatTag, channels uint16
		samplesPerSec       uint32
	}
	if !call(client, getMixFormat, uintptr(unsafe.Pointer(&format))) || format == nil {
		return 0
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(format))
	return int(format.samplesPerSec)
}
