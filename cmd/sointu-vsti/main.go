//go:build plugin

package main

import (
	"github.com/vsariola/sointu/cmd/plugin"
	"pipelined.dev/audio/vst2"
)

type (
	VSTIProcessContext struct {
		host vst2.Host
	}
)

func (c *VSTIProcessContext) BPM() (bpm float64, ok bool) {
	timeInfo := c.host.GetTimeInfo(vst2.TempoValid)
	if timeInfo == nil || timeInfo.Flags&vst2.TempoValid == 0 || timeInfo.Tempo == 0 {
		return 0, false
	}
	return timeInfo.Tempo, true
}

func (c *VSTIProcessContext) SampleRate() (samplerate float64, ok bool) {
	timeInfo := c.host.GetTimeInfo(0)
	if timeInfo == nil || timeInfo.SampleRate == 0 {
		return 0, false
	}
	return timeInfo.SampleRate, true
}

func init() {
	var (
		version = int32(100)
	)
	vst2.PluginAllocator = func(h vst2.Host) (vst2.Plugin, vst2.Dispatcher) {
		p := plugin.New("sointu-vsti")
		context := &VSTIProcessContext{host: h}
		return vst2.Plugin{
				UniqueID:       [4]byte{'S', 'n', 't', 'u'},
				Version:        version,
				InputChannels:  0,
				OutputChannels: 2,
				Name:           "Sointu",
				Vendor:         "vsariola/sointu",
				Category:       vst2.PluginCategorySynth,
				Flags:          vst2.PluginIsSynth,
				ProcessFloatFunc: func(in, out vst2.FloatBuffer) {
					p.Process(out.Channel(0), out.Channel(1), context)
				},
			}, vst2.Dispatcher{
				CanDoFunc: func(pcds vst2.PluginCanDoString) vst2.CanDoResponse {
					switch pcds {
					case vst2.PluginCanReceiveEvents, vst2.PluginCanReceiveMIDIEvent, vst2.PluginCanReceiveTimeInfo:
						return vst2.YesCanDo
					}
					return vst2.NoCanDo
				},
				ProcessEventsFunc: func(events *vst2.EventsPtr) {
					for i := 0; i < events.NumEvents(); i++ {
						switch ev := events.Event(i).(type) {
						case *vst2.MIDIEvent:
							p.MIDI(int(ev.DeltaFrames), 0, ev.Data)
						}
					}
				},
				CloseFunc: p.Close,
				GetChunkFunc: func(isPreset bool) []byte {
					return p.State()
				},
				SetChunkFunc: func(data []byte, isPreset bool) {
					p.SetState(data)
				},
			}

	}
}

func main() {}
