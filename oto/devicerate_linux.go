//go:build linux && !android

package oto

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

// deviceRate asks ALSA which rate the device that oto will open runs at
// when it is asked for 44100 Hz and ALSA may not resample: the rate of the
// hardware nearest to that. Sound servers behind ALSA (PipeWire,
// PulseAudio) take any rate and resample themselves; for those it is
// 44100.
func deviceRate() int {
	alsa, err := purego.Dlopen("libasound.so.2", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	var (
		open            func(pcm *uintptr, name string, stream, mode int32) int32
		closePCM        func(pcm uintptr) int32
		paramsMalloc    func(params *uintptr) int32
		paramsFree      func(params uintptr)
		paramsAny       func(pcm, params uintptr) int32
		setAccess       func(pcm, params uintptr, access int32) int32
		setFormat       func(pcm, params uintptr, format int32) int32
		setChannels     func(pcm, params uintptr, channels uint32) int32
		setRateResample func(pcm, params uintptr, resample uint32) int32
		setRateNear     func(pcm, params uintptr, rate *uint32, dir *int32) int32
		nameHint        func(card int32, iface string, hints **[1 << 16]uintptr) int32
		getHint         func(hint uintptr, id string) uintptr
		freeHint        func(hints *[1 << 16]uintptr) int32
		free            func(p uintptr)
	)
	for name, f := range map[string]any{
		"snd_pcm_open":                        &open,
		"snd_pcm_close":                       &closePCM,
		"snd_pcm_hw_params_malloc":            &paramsMalloc,
		"snd_pcm_hw_params_free":              &paramsFree,
		"snd_pcm_hw_params_any":               &paramsAny,
		"snd_pcm_hw_params_set_access":        &setAccess,
		"snd_pcm_hw_params_set_format":        &setFormat,
		"snd_pcm_hw_params_set_channels":      &setChannels,
		"snd_pcm_hw_params_set_rate_resample": &setRateResample,
		"snd_pcm_hw_params_set_rate_near":     &setRateNear,
		"snd_device_name_hint":                &nameHint,
		"snd_device_name_get_hint":            &getHint,
		"snd_device_name_free_hint":           &freeHint,
		"free":                                &free,
	} {
		sym, err := purego.Dlsym(alsa, name)
		if err != nil {
			return 0
		}
		purego.RegisterFunc(f, sym)
	}
	// the devices that oto tries, in its order: the first that opens
	devices := []string{"default", "plug:default"}
	var hints *[1 << 16]uintptr
	if nameHint(-1, "pcm", &hints) == 0 && hints != nil {
		for i := 0; i < len(hints) && hints[i] != 0; i++ {
			io, name := getHint(hints[i], "IOID"), getHint(hints[i], "NAME")
			if n := cString(name); name != 0 && cString(io) != "Input" && n != "null" && n != "default" {
				devices = append(devices, n)
			}
			free(io) // free takes nil
			free(name)
		}
		freeHint(hints)
	}
	const (
		streamPlayback   = 0  // SND_PCM_STREAM_PLAYBACK
		accessInterleave = 3  // SND_PCM_ACCESS_RW_INTERLEAVED
		formatFloatLE    = 14 // SND_PCM_FORMAT_FLOAT_LE
	)
	for _, name := range devices {
		var pcm uintptr
		if open(&pcm, name, streamPlayback, 0) < 0 {
			continue
		}
		defer closePCM(pcm)
		var params uintptr
		if paramsMalloc(&params) < 0 {
			return 0
		}
		defer paramsFree(params)
		rate := uint32(44100)
		if paramsAny(pcm, params) < 0 ||
			setAccess(pcm, params, accessInterleave) < 0 ||
			setFormat(pcm, params, formatFloatLE) < 0 ||
			setChannels(pcm, params, 2) < 0 ||
			setRateResample(pcm, params, 0) < 0 ||
			setRateNear(pcm, params, &rate, nil) < 0 {
			return 0
		}
		return int(rate)
	}
	return 0
}

// cString returns the string at p, which C allocated; "" for nil.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(*(*unsafe.Pointer)(unsafe.Pointer(&p)), n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n))
}
