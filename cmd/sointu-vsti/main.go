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
		// a parameter for each automation slot: 0.5 is no offset. The host
		// sets them between the blocks, so they take effect at the start of
		// the next one.
		var p *plugin.Instance
		parameters := make([]*vst2.Parameter, plugin.NumParams)
		last := make([]float32, plugin.NumParams)
		toSteps := func(v float32) float64 { return (float64(v) - 0.5) * 2 * plugin.ParamRange }
		for i := range parameters {
			parameters[i] = &vst2.Parameter{
				Value:             0.5,
				Unit:              "steps",
				GetNameFunc:       func() string { return p.ParamName(i) },
				GetValueLabelFunc: func(v float32) string { return plugin.ParamText(toSteps(v)) },
			}
			last[i] = 0.5
		}
		// the host reads the names and the values of the parameters again
		// when the display is updated
		p = plugin.New("sointu-vsti", h.UpdateDisplay, func(values bool) {
			if values { // of a state that was loaded
				for i := range parameters {
					parameters[i].Value = float32(p.ParamValue(i)/(2*plugin.ParamRange) + 0.5)
				}
			}
			h.UpdateDisplay()
		})
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
				Parameters:     parameters,
				ProcessFloatFunc: func(in, out vst2.FloatBuffer) {
					for i, param := range parameters {
						if v := param.Value; v != last[i] {
							last[i] = v
							p.Param(0, i, toSteps(v), false)
						}
					}
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
