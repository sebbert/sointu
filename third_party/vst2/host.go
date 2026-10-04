package vst2

import "pipelined.dev/signal"

type (
	// Host handles all callbacks from plugin.
	Host struct {
		GetSampleRate   HostGetSampleRateFunc
		GetBufferSize   HostGetBufferSizeFunc
		GetProcessLevel HostGetProcessLevelFunc
		GetTimeInfo     HostGetTimeInfoFunc
		UpdateDisplay   HostUpdateDisplayFunc
		SetInitialDelay HostSetInitialDelayFunc
	}

	// HostGetSampleRateFunc returns host sample rate.
	HostGetSampleRateFunc func() signal.Frequency
	// HostGetBufferSizeFunc returns host buffer size.
	HostGetBufferSizeFunc func() int
	// HostGetProcessLevel returns the context of execution.
	HostGetProcessLevelFunc func() ProcessLevel
	// HostGetTimeInfo returns current time info.
	HostGetTimeInfoFunc func(flags TimeInfoFlag) *TimeInfo
	// HostUpdateDisplayFunc tells the host that the plugin changed, e.g. its
	// state; hosts use it to mark the project unsaved.
	HostUpdateDisplayFunc func()
	// HostSetInitialDelayFunc sets the latency of the plugin, in frames,
	// and tells the host when it changed.
	HostSetInitialDelayFunc func(frames int)
)
