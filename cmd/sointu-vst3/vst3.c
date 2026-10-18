//go:build plugin

// VST3 factory, plugin and view, written against the C API of the VST3 SDK
// (third_party/vst3/vst3_c_api.h), with no other part of the SDK.
// Everything host-independent is forwarded to the Go side, through engine.h
// (cmd/plugin/engine): linked in, or in the dev loader an engine library
// that it loads.
//
// The plugin is one object that is both the component (the audio processor)
// and its edit controller: getControllerClassId says there is no controller
// of its own, and the host asks the component for IEditController. It is an
// instrument with no audio input, one stereo output, and an event input for
// each MIDI port of Sointu, 16 channels each. Its parameters are the
// automation slots, and hidden parameters that the host maps the MIDI
// controllers of those inputs to (IMidiMapping): VST3 does not pass
// controllers as events.

#include <pthread.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(__APPLE__)
#include <dispatch/dispatch.h>
#endif

#include "vst3_c_api.h"
#include "engine.h"
#include "view.h"

#if defined(_WIN32)
#define SOINTU_EXPORT __declspec(dllexport)
#else
#define SOINTU_EXPORT __attribute__((visibility("default")))
#endif

typedef Steinberg_tresult tresult;
typedef Steinberg_uint32 uint32;
typedef Steinberg_int32 int32;

#define OK Steinberg_kResultOk
#define FALSE_ Steinberg_kResultFalse
#define NO_INTERFACE Steinberg_kNoInterface
#define INVALID Steinberg_kInvalidArgument

#define MAX_PARAMS 64     // at least sointuParams()
#define MAX_PORTS 16      // at least sointuMIDIPorts()
#define CC_BASE 0x10000   // the id of the first parameter of a MIDI controller
#define CC_PER_PORT 2048  // 16 channels of 128 controllers
#define VIEW_WIDTH 360
#define VIEW_HEIGHT 84

static const Steinberg_TUID class_id = SMTG_INLINE_UID(0xD55F03CC, 0x0FB045F4, 0xBF6ED4BF, 0x39A19A91);

static int32 midi_ports(void) {
	int32 n = sointuMIDIPorts();
	return n < MAX_PORTS ? n : MAX_PORTS;
}

static int32 num_params(void) {
	int32 n = sointuParams();
	return n < MAX_PARAMS ? n : MAX_PARAMS;
}

// what the main thread is to tell the host
enum {
	PENDING_DIRTY = 1 << 0,
	PENDING_TITLES = 1 << 1,
	PENDING_VALUES = 1 << 2,
	PENDING_LATENCY = 1 << 3,
};

typedef struct sointu_vst3 {
	Steinberg_Vst_IComponent component; // must be first
	Steinberg_Vst_IAudioProcessor processor;
	Steinberg_Vst_IEditController controller;
	Steinberg_Vst_IMidiMapping midi_mapping;
	Steinberg_Vst_IProcessContextRequirements requirements;
	atomic_uint refs;
	int inits; // initialize calls without terminate: the component's and the controller's
	// handle is the cgo.Handle of the Go instance, from the first initialize
	// to the last terminate; lock guards it for the main thread against
	// the view
	pthread_mutex_t lock;
	uintptr_t handle;
	Steinberg_Vst_IComponentHandler *handler;
	double sample_rate;
	uint32 latency; // of the resampler at sample_rate, in frames
	// the values of the parameters on the side of the controller, normalized
	double values[MAX_PARAMS];
	atomic_uint pending;
} sointu_vst3_t;

#define FROM(iface, ptr) ((sointu_vst3_t *)((char *)(ptr) - offsetof(sointu_vst3_t, iface)))

static bool iid_is(const Steinberg_TUID a, const Steinberg_TUID b) {
	return memcmp(a, b, sizeof(Steinberg_TUID)) == 0;
}

// strings: VST3 has UTF-16 in String128

static void to_string128(Steinberg_Vst_String128 out, const char *in) {
	const unsigned char *s = (const unsigned char *)in;
	int n = 0;
	while (*s != 0 && n < 127) {
		uint32 c = *s++;
		int more = 0;
		if (c >= 0xF0) {
			c &= 0x07;
			more = 3;
		} else if (c >= 0xE0) {
			c &= 0x0F;
			more = 2;
		} else if (c >= 0xC0) {
			c &= 0x1F;
			more = 1;
		} else if (c >= 0x80) {
			c = 0xFFFD;
		}
		for (; more > 0 && (*s & 0xC0) == 0x80; more--)
			c = (c << 6) | (*s++ & 0x3F);
		if (more > 0)
			c = 0xFFFD;
		if (c >= 0x10000) {
			if (n + 2 > 127)
				break;
			c -= 0x10000;
			out[n++] = (Steinberg_char16)(0xD800 + (c >> 10));
			out[n++] = (Steinberg_char16)(0xDC00 + (c & 0x3FF));
		} else {
			out[n++] = (Steinberg_char16)c;
		}
	}
	out[n] = 0;
}

// from_tchar copies the ASCII of a UTF-16 string, what a number is written in
static void from_tchar(char *out, size_t size, const Steinberg_Vst_TChar *in) {
	size_t n = 0;
	for (; in[n] != 0 && n + 1 < size; n++)
		out[n] = (uint16_t)in[n] < 128 ? (char)in[n] : '?';
	out[n] = 0;
}

// parameters: an automation slot is -range to range, 0.5 normalized is 0

static double plain(double normalized) {
	double r = sointuParamRange();
	return -r + 2 * r * normalized;
}

static double normalized(double plain) {
	double r = sointuParamRange();
	double v = (plain + r) / (2 * r);
	return v < 0 ? 0 : v > 1 ? 1 : v;
}

static bool is_cc(Steinberg_Vst_ParamID id) {
	return id >= CC_BASE && id < CC_BASE + (uint32)midi_ports() * CC_PER_PORT;
}

// reference counting, shared by every interface of the plugin

static uint32 plugin_add_ref(sointu_vst3_t *p) {
	return atomic_fetch_add(&p->refs, 1) + 1;
}

static void close_instance(sointu_vst3_t *p) {
	pthread_mutex_lock(&p->lock);
	uintptr_t h = p->handle;
	p->handle = 0;
	pthread_mutex_unlock(&p->lock);
	if (h != 0)
		sointuClose(h);
	if (p->handler != NULL) {
		p->handler->lpVtbl->release(p->handler);
		p->handler = NULL;
	}
}

static uint32 plugin_release(sointu_vst3_t *p) {
	uint32 n = atomic_fetch_sub(&p->refs, 1) - 1;
	if (n == 0) {
		close_instance(p); // a host that did not terminate
		pthread_mutex_destroy(&p->lock);
		free(p);
	}
	return n;
}

static tresult plugin_query(sointu_vst3_t *p, const Steinberg_TUID iid, void **obj) {
	if (iid_is(iid, Steinberg_FUnknown_iid) || iid_is(iid, Steinberg_IPluginBase_iid) || iid_is(iid, Steinberg_Vst_IComponent_iid))
		*obj = &p->component;
	else if (iid_is(iid, Steinberg_Vst_IAudioProcessor_iid))
		*obj = &p->processor;
	else if (iid_is(iid, Steinberg_Vst_IEditController_iid))
		*obj = &p->controller;
	else if (iid_is(iid, Steinberg_Vst_IMidiMapping_iid))
		*obj = &p->midi_mapping;
	else if (iid_is(iid, Steinberg_Vst_IProcessContextRequirements_iid))
		*obj = &p->requirements;
	else {
		*obj = NULL;
		return NO_INTERFACE;
	}
	plugin_add_ref(p);
	return OK;
}

#define UNKNOWN_METHODS(iface)                                                                    \
	static tresult iface##_query(void *self, const Steinberg_TUID iid, void **obj) {             \
		return plugin_query(FROM(iface, self), iid, obj);                                         \
	}                                                                                             \
	static uint32 iface##_add_ref(void *self) { return plugin_add_ref(FROM(iface, self)); }      \
	static uint32 iface##_release(void *self) { return plugin_release(FROM(iface, self)); }

UNKNOWN_METHODS(component)
UNKNOWN_METHODS(processor)
UNKNOWN_METHODS(controller)
UNKNOWN_METHODS(midi_mapping)
UNKNOWN_METHODS(requirements)

// telling the host, on the main thread

static void deliver(sointu_vst3_t *p) {
	unsigned pending = atomic_exchange(&p->pending, 0);
	if (pending == 0)
		return;
	pthread_mutex_lock(&p->lock);
	uintptr_t h = p->handle;
	pthread_mutex_unlock(&p->lock);
	Steinberg_Vst_IComponentHandler *handler = p->handler;
	if (h == 0 || handler == NULL) {
		atomic_fetch_or(&p->pending, pending); // for when the host gives one
		return;
	}
	if (pending & PENDING_VALUES)
		for (int32 i = 0; i < num_params(); i++)
			p->values[i] = normalized(sointuParamValue(h, i));
	// one flag a call: Live missed the titles of a song loaded with the
	// project when they came with the values in one call
	if (pending & PENDING_TITLES)
		handler->lpVtbl->restartComponent(handler, Steinberg_Vst_RestartFlags_kParamTitlesChanged);
	if (pending & PENDING_VALUES)
		handler->lpVtbl->restartComponent(handler, Steinberg_Vst_RestartFlags_kParamValuesChanged);
	if (pending & PENDING_LATENCY)
		handler->lpVtbl->restartComponent(handler, Steinberg_Vst_RestartFlags_kLatencyChanged);
	if (pending & PENDING_DIRTY) {
		Steinberg_Vst_IComponentHandler2 *h2 = NULL;
		if (handler->lpVtbl->queryInterface(handler, Steinberg_Vst_IComponentHandler2_iid, (void **)&h2) == OK && h2 != NULL) {
			h2->lpVtbl->setDirty(h2, 1);
			h2->lpVtbl->release(h2);
		}
	}
}

#if defined(__APPLE__)
static void deliver_main(void *ctx) {
	sointu_vst3_t *p = ctx;
	deliver(p);
	plugin_release(p);
}
#endif

// tell asks the main thread to tell the host what. On macOS it is told at
// once; on other systems when the host next calls the controller on the
// main thread.
static void tell(sointu_vst3_t *p, unsigned what) {
	atomic_fetch_or(&p->pending, what);
#if defined(__APPLE__)
	plugin_add_ref(p);
	dispatch_async_f(dispatch_get_main_queue(), p, deliver_main);
#endif
}

// sointu_mark_dirty tells the host that the song changed and the project has
// unsaved changes. Called from any thread.
static void sointu_mark_dirty(uintptr_t plugin) { tell((sointu_vst3_t *)plugin, PENDING_DIRTY); }

// sointu_params_changed tells the host that the names of the parameters
// changed, or with values their values. Called from any thread.
static void sointu_params_changed(uintptr_t plugin, bool values) {
	tell((sointu_vst3_t *)plugin, values ? PENDING_VALUES : PENDING_TITLES);
}

// VST3 has no call to tell the host that the user closed the window: the
// view stays, with a button to show it again
static sointu_callbacks callbacks = {
	.mark_dirty = sointu_mark_dirty,
	.params_changed = sointu_params_changed,
};

// IPluginBase, shared by the component and the controller: the host may
// initialize both, which are one

static tresult plugin_initialize(sointu_vst3_t *p, Steinberg_FUnknown *context) {
	if (p->inits++ > 0)
		return OK;
	uintptr_t h = sointuNew((uintptr_t)p, "sointu-vst3", &callbacks);
	pthread_mutex_lock(&p->lock);
	p->handle = h;
	pthread_mutex_unlock(&p->lock);
	return OK;
}

static tresult plugin_terminate(sointu_vst3_t *p) {
	if (p->inits == 0)
		return FALSE_;
	if (--p->inits == 0)
		close_instance(p);
	return OK;
}

// state: the same recovery data that the other plugins save

static tresult write_state(sointu_vst3_t *p, Steinberg_IBStream *stream) {
	if (p->handle == 0 || stream == NULL)
		return FALSE_;
	size_t size = 0;
	void *data = sointuState(p->handle, &size);
	if (data == NULL)
		return FALSE_;
	tresult res = OK;
	for (size_t written = 0; written < size;) {
		size_t left = size - written;
		int32 chunk = left > (1 << 30) ? (1 << 30) : (int32)left;
		int32 n = 0;
		if (stream->lpVtbl->write(stream, (char *)data + written, chunk, &n) != OK || n <= 0) {
			res = FALSE_;
			break;
		}
		written += n;
	}
	free(data);
	return res;
}

static tresult read_state(sointu_vst3_t *p, Steinberg_IBStream *stream) {
	if (p->handle == 0 || stream == NULL)
		return FALSE_;
	size_t size = 0, cap = 65536;
	char *data = malloc(cap);
	if (data == NULL)
		return FALSE_;
	for (;;) {
		if (size == cap) {
			char *grown = realloc(data, cap *= 2);
			if (grown == NULL) {
				free(data);
				return FALSE_;
			}
			data = grown;
		}
		int32 n = 0;
		tresult r = stream->lpVtbl->read(stream, data + size, (int32)(cap - size), &n);
		if (r != OK || n <= 0)
			break;
		size += n;
	}
	if (size == 0) {
		free(data);
		return FALSE_;
	}
	sointuSetState(p->handle, data, size);
	free(data);
	return OK;
}

// IComponent

static tresult component_initialize(void *self, Steinberg_FUnknown *context) {
	return plugin_initialize(FROM(component, self), context);
}

static tresult component_terminate(void *self) { return plugin_terminate(FROM(component, self)); }

static tresult component_get_controller_class_id(void *self, Steinberg_TUID class_id) {
	return FALSE_; // the component is its own controller
}

static tresult component_set_io_mode(void *self, Steinberg_Vst_IoMode mode) { return OK; }

static int32 component_get_bus_count(void *self, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir) {
	if (type == Steinberg_Vst_MediaTypes_kAudio)
		return dir == Steinberg_Vst_BusDirections_kOutput ? 1 : 0;
	if (type == Steinberg_Vst_MediaTypes_kEvent)
		return dir == Steinberg_Vst_BusDirections_kInput ? midi_ports() : 0;
	return 0;
}

static tresult component_get_bus_info(void *self, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, int32 index, struct Steinberg_Vst_BusInfo *bus) {
	if (index < 0 || index >= component_get_bus_count(self, type, dir))
		return INVALID;
	memset(bus, 0, sizeof(*bus));
	bus->mediaType = type;
	bus->direction = dir;
	bus->flags = Steinberg_Vst_BusInfo_BusFlags_kDefaultActive;
	bus->busType = index == 0 ? Steinberg_Vst_BusTypes_kMain : Steinberg_Vst_BusTypes_kAux;
	if (type == Steinberg_Vst_MediaTypes_kAudio) {
		bus->channelCount = 2;
		to_string128(bus->name, "Output");
	} else {
		// channels 1-16 on input n are Sointu MIDI channels (n-1)*16+1 to
		// (n-1)*16+16
		char name[64];
		snprintf(name, sizeof(name), "MIDI In %d (channels %d-%d)", index + 1, index * 16 + 1, index * 16 + 16);
		bus->channelCount = 16;
		to_string128(bus->name, name);
	}
	return OK;
}

static tresult component_get_routing_info(void *self, struct Steinberg_Vst_RoutingInfo *in, struct Steinberg_Vst_RoutingInfo *out) {
	return FALSE_;
}

static tresult component_activate_bus(void *self, Steinberg_Vst_MediaType type, Steinberg_Vst_BusDirection dir, int32 index, Steinberg_TBool state) {
	if (index < 0 || index >= component_get_bus_count(self, type, dir))
		return INVALID;
	return OK;
}

static tresult component_set_active(void *self, Steinberg_TBool state) { return OK; }

static tresult component_set_state(void *self, Steinberg_IBStream *state) {
	return read_state(FROM(component, self), state);
}

static tresult component_get_state(void *self, Steinberg_IBStream *state) {
	return write_state(FROM(component, self), state);
}

static Steinberg_Vst_IComponentVtbl component_vtbl = {
	.queryInterface = component_query,
	.addRef = component_add_ref,
	.release = component_release,
	.initialize = component_initialize,
	.terminate = component_terminate,
	.getControllerClassId = component_get_controller_class_id,
	.setIoMode = component_set_io_mode,
	.getBusCount = component_get_bus_count,
	.getBusInfo = component_get_bus_info,
	.getRoutingInfo = component_get_routing_info,
	.activateBus = component_activate_bus,
	.setActive = component_set_active,
	.setState = component_set_state,
	.getState = component_get_state,
};

// IAudioProcessor

static tresult processor_set_bus_arrangements(void *self, Steinberg_Vst_SpeakerArrangement *inputs, int32 num_ins, Steinberg_Vst_SpeakerArrangement *outputs, int32 num_outs) {
	if (num_ins == 0 && num_outs == 1 && outputs[0] == Steinberg_Vst_SpeakerArr_kStereo)
		return OK;
	return FALSE_;
}

static tresult processor_get_bus_arrangement(void *self, Steinberg_Vst_BusDirection dir, int32 index, Steinberg_Vst_SpeakerArrangement *arr) {
	if (dir != Steinberg_Vst_BusDirections_kOutput || index != 0)
		return INVALID;
	*arr = Steinberg_Vst_SpeakerArr_kStereo;
	return OK;
}

static tresult processor_can_process_sample_size(void *self, int32 size) {
	return size == Steinberg_Vst_SymbolicSampleSizes_kSample32 ? OK : FALSE_;
}

static uint32 processor_get_latency_samples(void *self) { return FROM(processor, self)->latency; }

static tresult processor_setup_processing(void *self, struct Steinberg_Vst_ProcessSetup *setup) {
	sointu_vst3_t *p = FROM(processor, self);
	if (setup->symbolicSampleSize != Steinberg_Vst_SymbolicSampleSizes_kSample32)
		return FALSE_;
	p->sample_rate = setup->sampleRate;
	if (p->handle == 0)
		return OK;
	// the synth runs at 44100 Hz; at another rate the plugin resamples,
	// which delays the output
	uint32 latency = sointuSetSampleRate(p->handle, setup->sampleRate);
	if (latency != p->latency) {
		p->latency = latency;
		tell(p, PENDING_LATENCY);
	}
	return OK;
}

static tresult processor_set_processing(void *self, Steinberg_TBool state) { return OK; }

static void handle_params(sointu_vst3_t *p, Steinberg_Vst_IParameterChanges *changes) {
	int32 n = changes->lpVtbl->getParameterCount(changes);
	for (int32 i = 0; i < n; i++) {
		Steinberg_Vst_IParamValueQueue *q = changes->lpVtbl->getParameterData(changes, i);
		if (q == NULL)
			continue;
		Steinberg_Vst_ParamID id = q->lpVtbl->getParameterId(q);
		int32 points = q->lpVtbl->getPointCount(q);
		for (int32 j = 0; j < points; j++) {
			int32 offset = 0;
			Steinberg_Vst_ParamValue v = 0;
			if (q->lpVtbl->getPoint(q, j, &offset, &v) != OK)
				continue;
			if (id < (Steinberg_Vst_ParamID)num_params()) {
				sointuParam(p->handle, offset, id, plain(v), false);
			} else if (is_cc(id)) {
				uint32 c = id - CC_BASE;
				int32 port = c / CC_PER_PORT, channel = (c % CC_PER_PORT) / 128, cc = c % 128;
				double value = v * 127 + 0.5;
				sointuMIDI(p->handle, offset, port, 0xB0 | channel, cc, value < 0 ? 0 : value > 127 ? 127 : (uint8_t)value);
			}
		}
	}
}

static uint8_t velocity(float v) {
	double x = v * 127.0 + 0.5;
	return x < 0 ? 0 : x > 127 ? 127 : (uint8_t)x;
}

static void handle_events(sointu_vst3_t *p, Steinberg_Vst_IEventList *events) {
	int32 n = events->lpVtbl->getEventCount(events);
	for (int32 i = 0; i < n; i++) {
		struct Steinberg_Vst_Event e;
		if (events->lpVtbl->getEvent(events, i, &e) != OK)
			continue;
		switch (e.type) {
		case Steinberg_Vst_Event_EventTypes_kNoteOnEvent: {
			struct Steinberg_Vst_NoteOnEvent *ev = &e.Steinberg_Vst_Event_noteOn;
			if (ev->pitch < 0 || ev->pitch > 127 || ev->channel < 0 || ev->channel > 15)
				break;
			uint8_t vel = velocity(ev->velocity);
			// MIDI note on with velocity 0 means note off
			sointuMIDI(p->handle, e.sampleOffset, e.busIndex, 0x90 | ev->channel, ev->pitch, vel == 0 ? 1 : vel);
			break;
		}
		case Steinberg_Vst_Event_EventTypes_kNoteOffEvent: {
			struct Steinberg_Vst_NoteOffEvent *ev = &e.Steinberg_Vst_Event_noteOff;
			if (ev->pitch < 0 || ev->pitch > 127 || ev->channel < 0 || ev->channel > 15)
				break;
			sointuMIDI(p->handle, e.sampleOffset, e.busIndex, 0x80 | ev->channel, ev->pitch, velocity(ev->velocity));
			break;
		}
		}
	}
}

static tresult processor_process(void *self, struct Steinberg_Vst_ProcessData *data) {
	sointu_vst3_t *p = FROM(processor, self);
	if (p->handle == 0)
		return FALSE_;
	if (data->inputParameterChanges != NULL)
		handle_params(p, data->inputParameterChanges);
	if (data->inputEvents != NULL)
		handle_events(p, data->inputEvents);
	if (data->numSamples <= 0 || data->numOutputs < 1 || data->outputs == NULL)
		return OK; // a flush of parameters
	struct Steinberg_Vst_AudioBusBuffers *out = &data->outputs[0];
	if (out->numChannels < 2 || out->Steinberg_Vst_AudioBusBuffers_channelBuffers32 == NULL)
		return FALSE_;
	struct Steinberg_Vst_ProcessContext *ctx = data->processContext;
	bool has_tempo = ctx != NULL && (ctx->state & Steinberg_Vst_ProcessContext_StatesAndFlags_kTempoValid) && ctx->tempo > 0;
	float **ch = out->Steinberg_Vst_AudioBusBuffers_channelBuffers32;
	sointuProcess(p->handle, ch[0], ch[1], data->numSamples, p->sample_rate, has_tempo, has_tempo ? ctx->tempo : 0);
	out->silenceFlags = 0;
	return OK;
}

static uint32 processor_get_tail_samples(void *self) {
	return Steinberg_Vst_kInfiniteTail; // the song plays on, and so do reverbs and delays
}

static Steinberg_Vst_IAudioProcessorVtbl processor_vtbl = {
	.queryInterface = processor_query,
	.addRef = processor_add_ref,
	.release = processor_release,
	.setBusArrangements = processor_set_bus_arrangements,
	.getBusArrangement = processor_get_bus_arrangement,
	.canProcessSampleSize = processor_can_process_sample_size,
	.getLatencySamples = processor_get_latency_samples,
	.setupProcessing = processor_setup_processing,
	.setProcessing = processor_set_processing,
	.process = processor_process,
	.getTailSamples = processor_get_tail_samples,
};

// IProcessContextRequirements: the tempo is all that is used

static uint32 requirements_get(void *self) {
	return Steinberg_Vst_IProcessContextRequirements_Flags_kNeedTempo;
}

static Steinberg_Vst_IProcessContextRequirementsVtbl requirements_vtbl = {
	.queryInterface = requirements_query,
	.addRef = requirements_add_ref,
	.release = requirements_release,
	.getProcessContextRequirements = requirements_get,
};

// IMidiMapping: controller cc of channel on input bus is a hidden parameter

static tresult midi_mapping_get_assignment(void *self, int32 bus, Steinberg_int16 channel, Steinberg_Vst_CtrlNumber cc, Steinberg_Vst_ParamID *id) {
	if (bus < 0 || bus >= midi_ports() || channel < 0 || channel > 15 || cc < 0 || cc > 127)
		return FALSE_;
	*id = CC_BASE + bus * CC_PER_PORT + channel * 128 + cc;
	return OK;
}

static Steinberg_Vst_IMidiMappingVtbl midi_mapping_vtbl = {
	.queryInterface = midi_mapping_query,
	.addRef = midi_mapping_add_ref,
	.release = midi_mapping_release,
	.getMidiControllerAssignment = midi_mapping_get_assignment,
};

// IEditController. The parameters are the automation slots, an offset of the
// parameter that the slot is bound to, in steps of that parameter. The id of
// a parameter is the number of its slot, which is what keeps the automation
// of the host on it whatever happens to the patch.

static tresult controller_initialize(void *self, Steinberg_FUnknown *context) {
	return plugin_initialize(FROM(controller, self), context);
}

static tresult controller_terminate(void *self) { return plugin_terminate(FROM(controller, self)); }

// the component has the state; the controller has none of its own
static tresult controller_set_component_state(void *self, Steinberg_IBStream *state) {
	deliver(FROM(controller, self));
	return OK;
}

static tresult controller_set_state(void *self, Steinberg_IBStream *state) { return OK; }

static tresult controller_get_state(void *self, Steinberg_IBStream *state) { return OK; }

static int32 controller_get_parameter_count(void *self) {
	return num_params() + midi_ports() * CC_PER_PORT;
}

static tresult controller_get_parameter_info(void *self, int32 index, struct Steinberg_Vst_ParameterInfo *info) {
	sointu_vst3_t *p = FROM(controller, self);
	deliver(p);
	if (index < 0 || index >= controller_get_parameter_count(self))
		return INVALID;
	memset(info, 0, sizeof(*info));
	info->unitId = Steinberg_Vst_kRootUnitId;
	if (index >= num_params()) {
		int32 c = index - num_params();
		char name[64];
		snprintf(name, sizeof(name), "MIDI %d CC %d ch %d", c / CC_PER_PORT + 1, c % 128, (c % CC_PER_PORT) / 128 + 1);
		info->id = CC_BASE + c;
		to_string128(info->title, name);
		to_string128(info->shortTitle, name);
		info->stepCount = 127;
		info->defaultNormalizedValue = 0;
		info->flags = Steinberg_Vst_ParameterInfo_ParameterFlags_kIsHidden;
		return OK;
	}
	info->id = index;
	char shortName[16];
	snprintf(shortName, sizeof(shortName), "A%d", index + 1);
	if (p->handle != 0) {
		char *name = sointuParamName(p->handle, index);
		to_string128(info->title, name);
		free(name);
	} else {
		to_string128(info->title, shortName);
	}
	to_string128(info->shortTitle, shortName);
	info->stepCount = 0;
	info->defaultNormalizedValue = 0.5;
	info->flags = Steinberg_Vst_ParameterInfo_ParameterFlags_kCanAutomate;
	return OK;
}

static tresult controller_get_param_string_by_value(void *self, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v, Steinberg_Vst_String128 out) {
	sointu_vst3_t *p = FROM(controller, self);
	char text[32];
	if (id < (Steinberg_Vst_ParamID)num_params() && p->handle != 0) {
		char *t = sointuParamText(p->handle, (int32)id, plain(v));
		snprintf(text, sizeof(text), "%s", t);
		free(t);
	} else if (id < (Steinberg_Vst_ParamID)num_params())
		snprintf(text, sizeof(text), "%+.2f", plain(v));
	else if (is_cc(id))
		snprintf(text, sizeof(text), "%d", (int)(v * 127 + 0.5));
	else
		return INVALID;
	to_string128(out, text);
	return OK;
}

static tresult controller_get_param_value_by_string(void *self, Steinberg_Vst_ParamID id, Steinberg_Vst_TChar *in, Steinberg_Vst_ParamValue *v) {
	char text[64];
	from_tchar(text, sizeof(text), in);
	char *end;
	double x = strtod(text, &end);
	if (end == text)
		return FALSE_;
	sointu_vst3_t *p = FROM(controller, self);
	if (id < (Steinberg_Vst_ParamID)num_params())
		*v = normalized(p->handle != 0 ? sointuParamFromSteps(p->handle, (int32)id, x) : x);
	else if (is_cc(id))
		*v = x < 0 ? 0 : x > 127 ? 1 : x / 127;
	else
		return INVALID;
	return OK;
}

static Steinberg_Vst_ParamValue controller_normalized_to_plain(void *self, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v) {
	if (id < (Steinberg_Vst_ParamID)num_params())
		return plain(v);
	return is_cc(id) ? v * 127 : v;
}

static Steinberg_Vst_ParamValue controller_plain_to_normalized(void *self, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v) {
	if (id < (Steinberg_Vst_ParamID)num_params())
		return normalized(v);
	return is_cc(id) ? v / 127 : v;
}

static Steinberg_Vst_ParamValue controller_get_param_normalized(void *self, Steinberg_Vst_ParamID id) {
	sointu_vst3_t *p = FROM(controller, self);
	deliver(p);
	return id < (Steinberg_Vst_ParamID)num_params() ? p->values[id] : 0;
}

static tresult controller_set_param_normalized(void *self, Steinberg_Vst_ParamID id, Steinberg_Vst_ParamValue v) {
	sointu_vst3_t *p = FROM(controller, self);
	if (id < (Steinberg_Vst_ParamID)num_params()) {
		p->values[id] = v < 0 ? 0 : v > 1 ? 1 : v;
		return OK;
	}
	return is_cc(id) ? OK : INVALID;
}

static tresult controller_set_component_handler(void *self, Steinberg_Vst_IComponentHandler *handler) {
	sointu_vst3_t *p = FROM(controller, self);
	if (handler == p->handler)
		return OK;
	if (handler != NULL)
		handler->lpVtbl->addRef(handler);
	if (p->handler != NULL)
		p->handler->lpVtbl->release(p->handler);
	p->handler = handler;
	deliver(p);
	return OK;
}

static Steinberg_IPlugView *view_new(sointu_vst3_t *p);

static Steinberg_IPlugView *controller_create_view(void *self, Steinberg_FIDString name) {
	if (name == NULL || strcmp(name, "editor") != 0)
		return NULL;
	return view_new(FROM(controller, self));
}

static Steinberg_Vst_IEditControllerVtbl controller_vtbl = {
	.queryInterface = controller_query,
	.addRef = controller_add_ref,
	.release = controller_release,
	.initialize = controller_initialize,
	.terminate = controller_terminate,
	.setComponentState = controller_set_component_state,
	.setState = controller_set_state,
	.getState = controller_get_state,
	.getParameterCount = controller_get_parameter_count,
	.getParameterInfo = controller_get_parameter_info,
	.getParamStringByValue = controller_get_param_string_by_value,
	.getParamValueByString = controller_get_param_value_by_string,
	.normalizedParamToPlain = controller_normalized_to_plain,
	.plainParamToNormalized = controller_plain_to_normalized,
	.getParamNormalized = controller_get_param_normalized,
	.setParamNormalized = controller_set_param_normalized,
	.setComponentHandler = controller_set_component_handler,
	.createView = controller_create_view,
};

// view: the tracker is a window of its own; VST3 has no windows of a plugin
// that are not in a window of the host. The view that the host gets says
// so, and opens that window when the host shows it, and with a button; the
// window closes when the host closes the view. Only on macOS: elsewhere
// there is no view, and the window opens after a moment, as with a host
// that does not ask for one (plugin.NewHidden).

typedef struct {
	Steinberg_IPlugView view; // must be first
	atomic_uint refs;
	sointu_vst3_t *plugin;
	void *native; // the view of the system, while attached
} sointu_view_t;

// sointu_view_show shows the window, from the button of the view. Called on
// the main thread.
void sointu_view_show(uintptr_t plugin) {
	sointu_vst3_t *p = (sointu_vst3_t *)plugin;
	pthread_mutex_lock(&p->lock);
	if (p->handle != 0)
		sointuShowWindow(p->handle);
	pthread_mutex_unlock(&p->lock);
}

static tresult view_query(void *self, const Steinberg_TUID iid, void **obj) {
	if (iid_is(iid, Steinberg_FUnknown_iid) || iid_is(iid, Steinberg_IPlugView_iid)) {
		*obj = self;
		atomic_fetch_add(&((sointu_view_t *)self)->refs, 1);
		return OK;
	}
	*obj = NULL;
	return NO_INTERFACE;
}

static uint32 view_add_ref(void *self) {
	return atomic_fetch_add(&((sointu_view_t *)self)->refs, 1) + 1;
}

static tresult view_removed(void *self);

static uint32 view_release(void *self) {
	sointu_view_t *v = self;
	uint32 n = atomic_fetch_sub(&v->refs, 1) - 1;
	if (n == 0) {
		if (v->native != NULL)
			view_removed(v);
		plugin_release(v->plugin);
		free(v);
	}
	return n;
}

static tresult view_is_platform_type_supported(void *self, Steinberg_FIDString type) {
#if defined(__APPLE__)
	return type != NULL && strcmp(type, Steinberg_kPlatformTypeNSView) == 0 ? OK : FALSE_;
#else
	return FALSE_;
#endif
}

static tresult view_attached(void *self, void *parent, Steinberg_FIDString type) {
	sointu_view_t *v = self;
	if (parent == NULL || view_is_platform_type_supported(self, type) != OK || v->native != NULL)
		return FALSE_;
	sointu_vst3_t *p = v->plugin;
	deliver(p);
	v->native = sointu_view_attach(parent, (uintptr_t)p, VIEW_WIDTH, VIEW_HEIGHT);
	if (v->native == NULL)
		return FALSE_;
	pthread_mutex_lock(&p->lock);
	if (p->handle != 0) {
		sointuManageWindow(p->handle);
		sointuShowWindow(p->handle);
	}
	pthread_mutex_unlock(&p->lock);
	return OK;
}

static tresult view_removed(void *self) {
	sointu_view_t *v = self;
	if (v->native == NULL)
		return FALSE_;
	sointu_view_remove(v->native);
	v->native = NULL;
	sointu_vst3_t *p = v->plugin;
	pthread_mutex_lock(&p->lock);
	if (p->handle != 0)
		sointuHideWindow(p->handle);
	pthread_mutex_unlock(&p->lock);
	return OK;
}

static tresult view_on_wheel(void *self, float distance) { return FALSE_; }

static tresult view_on_key(void *self, Steinberg_char16 key, Steinberg_int16 code, Steinberg_int16 modifiers) {
	return FALSE_;
}

static tresult view_get_size(void *self, struct Steinberg_ViewRect *size) {
	if (size == NULL)
		return INVALID;
	*size = (struct Steinberg_ViewRect){0, 0, VIEW_WIDTH, VIEW_HEIGHT};
	return OK;
}

static tresult view_on_size(void *self, struct Steinberg_ViewRect *size) { return OK; }

static tresult view_on_focus(void *self, Steinberg_TBool state) { return OK; }

static tresult view_set_frame(void *self, Steinberg_IPlugFrame *frame) { return OK; }

static tresult view_can_resize(void *self) { return FALSE_; }

static tresult view_check_size_constraint(void *self, struct Steinberg_ViewRect *rect) {
	if (rect == NULL)
		return INVALID;
	*rect = (struct Steinberg_ViewRect){rect->left, rect->top, rect->left + VIEW_WIDTH, rect->top + VIEW_HEIGHT};
	return OK;
}

static Steinberg_IPlugViewVtbl view_vtbl = {
	.queryInterface = view_query,
	.addRef = view_add_ref,
	.release = view_release,
	.isPlatformTypeSupported = view_is_platform_type_supported,
	.attached = view_attached,
	.removed = view_removed,
	.onWheel = view_on_wheel,
	.onKeyDown = view_on_key,
	.onKeyUp = view_on_key,
	.getSize = view_get_size,
	.onSize = view_on_size,
	.onFocus = view_on_focus,
	.setFrame = view_set_frame,
	.canResize = view_can_resize,
	.checkSizeConstraint = view_check_size_constraint,
};

static Steinberg_IPlugView *view_new(sointu_vst3_t *p) {
#if defined(__APPLE__)
	sointu_view_t *v = calloc(1, sizeof(*v));
	if (v == NULL)
		return NULL;
	v->view.lpVtbl = &view_vtbl;
	atomic_init(&v->refs, 1);
	v->plugin = p;
	plugin_add_ref(p);
	return &v->view;
#else
	return NULL;
#endif
}

// factory

static sointu_vst3_t *plugin_new(void) {
	sointu_vst3_t *p = calloc(1, sizeof(*p));
	if (p == NULL)
		return NULL;
	p->component.lpVtbl = &component_vtbl;
	p->processor.lpVtbl = &processor_vtbl;
	p->controller.lpVtbl = &controller_vtbl;
	p->midi_mapping.lpVtbl = &midi_mapping_vtbl;
	p->requirements.lpVtbl = &requirements_vtbl;
	atomic_init(&p->refs, 1);
	atomic_init(&p->pending, 0);
	pthread_mutex_init(&p->lock, NULL);
	p->sample_rate = 44100;
	for (int i = 0; i < MAX_PARAMS; i++)
		p->values[i] = 0.5;
	return p;
}

static tresult factory_query(void *self, const Steinberg_TUID iid, void **obj) {
	if (iid_is(iid, Steinberg_FUnknown_iid) || iid_is(iid, Steinberg_IPluginFactory_iid) || iid_is(iid, Steinberg_IPluginFactory2_iid) || iid_is(iid, Steinberg_IPluginFactory3_iid)) {
		*obj = self;
		return OK;
	}
	*obj = NULL;
	return NO_INTERFACE;
}

static uint32 factory_add_ref(void *self) { return 1; }

static uint32 factory_release(void *self) { return 1; }

static tresult factory_get_factory_info(void *self, struct Steinberg_PFactoryInfo *info) {
	memset(info, 0, sizeof(*info));
	snprintf(info->vendor, sizeof(info->vendor), "vsariola/sointu");
	snprintf(info->url, sizeof(info->url), "https://github.com/vsariola/sointu");
	info->flags = Steinberg_PFactoryInfo_FactoryFlags_kUnicode;
	return OK;
}

static int32 factory_count_classes(void *self) { return 1; }

#define CLASS_NAME "Sointu"
#define CLASS_CATEGORY "Audio Module Class"
#define CLASS_VENDOR "vsariola/sointu"
#define CLASS_SUBCATEGORIES "Instrument|Synth"
#define CLASS_SDK "VST 3.8.0"

static tresult factory_get_class_info(void *self, int32 index, struct Steinberg_PClassInfo *info) {
	if (index != 0)
		return INVALID;
	memset(info, 0, sizeof(*info));
	memcpy(info->cid, class_id, sizeof(Steinberg_TUID));
	info->cardinality = Steinberg_PClassInfo_ClassCardinality_kManyInstances;
	snprintf(info->category, sizeof(info->category), CLASS_CATEGORY);
	snprintf(info->name, sizeof(info->name), CLASS_NAME);
	return OK;
}

static const char *version;

static tresult factory_get_class_info2(void *self, int32 index, struct Steinberg_PClassInfo2 *info) {
	if (index != 0)
		return INVALID;
	memset(info, 0, sizeof(*info));
	memcpy(info->cid, class_id, sizeof(Steinberg_TUID));
	info->cardinality = Steinberg_PClassInfo_ClassCardinality_kManyInstances;
	snprintf(info->category, sizeof(info->category), CLASS_CATEGORY);
	snprintf(info->name, sizeof(info->name), CLASS_NAME);
	snprintf(info->subCategories, sizeof(info->subCategories), CLASS_SUBCATEGORIES);
	snprintf(info->vendor, sizeof(info->vendor), CLASS_VENDOR);
	snprintf(info->version, sizeof(info->version), "%s", version);
	snprintf(info->sdkVersion, sizeof(info->sdkVersion), CLASS_SDK);
	return OK;
}

static tresult factory_get_class_info_unicode(void *self, int32 index, struct Steinberg_PClassInfoW *info) {
	if (index != 0)
		return INVALID;
	memset(info, 0, sizeof(*info));
	memcpy(info->cid, class_id, sizeof(Steinberg_TUID));
	info->cardinality = Steinberg_PClassInfo_ClassCardinality_kManyInstances;
	snprintf(info->category, sizeof(info->category), CLASS_CATEGORY);
	to_string128(info->name, CLASS_NAME);
	snprintf(info->subCategories, sizeof(info->subCategories), CLASS_SUBCATEGORIES);
	to_string128(info->vendor, CLASS_VENDOR);
	to_string128(info->version, version);
	to_string128(info->sdkVersion, CLASS_SDK);
	return OK;
}

static tresult factory_set_host_context(void *self, Steinberg_FUnknown *context) { return OK; }

static tresult factory_create_instance(void *self, Steinberg_FIDString cid, Steinberg_FIDString iid, void **obj) {
	*obj = NULL;
	if (cid == NULL || iid == NULL || !iid_is(cid, class_id))
		return NO_INTERFACE;
	sointu_vst3_t *p = plugin_new();
	if (p == NULL)
		return FALSE_;
	tresult r = plugin_query(p, iid, obj);
	plugin_release(p);
	return r;
}

static Steinberg_IPluginFactory3Vtbl factory_vtbl = {
	.queryInterface = factory_query,
	.addRef = factory_add_ref,
	.release = factory_release,
	.getFactoryInfo = factory_get_factory_info,
	.countClasses = factory_count_classes,
	.getClassInfo = factory_get_class_info,
	.createInstance = factory_create_instance,
	.getClassInfo2 = factory_get_class_info2,
	.getClassInfoUnicode = factory_get_class_info_unicode,
	.setHostContext = factory_set_host_context,
};

static Steinberg_IPluginFactory3 factory = {.lpVtbl = &factory_vtbl};

// entry points

SOINTU_EXPORT Steinberg_IPluginFactory *GetPluginFactory(void) {
	if (version == NULL)
		version = sointuVersion();
	return (Steinberg_IPluginFactory *)&factory;
}

#if defined(__APPLE__)
SOINTU_EXPORT bool bundleEntry(void *bundle) { return true; }
SOINTU_EXPORT bool bundleExit(void) { return true; }
#elif defined(_WIN32)
SOINTU_EXPORT bool InitDll(void) { return true; }
SOINTU_EXPORT bool ExitDll(void) { return true; }
#else
SOINTU_EXPORT bool ModuleEntry(void *library) { return true; }
SOINTU_EXPORT bool ModuleExit(void) { return true; }
#endif
