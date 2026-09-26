//go:build plugin

// CLAP entry point, factory and plugin callbacks. Everything host-independent
// is forwarded to the Go side in main.go.

#include <stdlib.h>
#include <string.h>

#include <clap/clap.h>
#include "_cgo_export.h"

typedef struct {
	clap_plugin_t plugin;
	const clap_host_t *host;
	uintptr_t handle; // cgo.Handle of the Go instance
	double sample_rate;
} sointu_plugin_t;

static const char *const features[] = {
	CLAP_PLUGIN_FEATURE_INSTRUMENT,
	CLAP_PLUGIN_FEATURE_SYNTHESIZER,
	CLAP_PLUGIN_FEATURE_STEREO,
	NULL,
};

static clap_plugin_descriptor_t descriptor = {
	.clap_version = CLAP_VERSION_INIT,
	.id = "com.vsariola.sointu",
	.name = "Sointu",
	.vendor = "vsariola/sointu",
	.url = "https://github.com/vsariola/sointu",
	.manual_url = "",
	.support_url = "",
	.version = "", // set in entry_init
	.description = "A modular synthesizer and tracker for 4k intros",
	.features = features,
};

static sointu_plugin_t *self(const clap_plugin_t *plugin) {
	return (sointu_plugin_t *)plugin->plugin_data;
}

// audio-ports: one stereo output

static uint32_t audio_ports_count(const clap_plugin_t *plugin, bool is_input) {
	return is_input ? 0 : 1;
}

static bool audio_ports_get(const clap_plugin_t *plugin, uint32_t index, bool is_input, clap_audio_port_info_t *info) {
	if (is_input || index != 0)
		return false;
	info->id = 0;
	strncpy(info->name, "Output", sizeof(info->name));
	info->flags = CLAP_AUDIO_PORT_IS_MAIN;
	info->channel_count = 2;
	info->port_type = CLAP_PORT_STEREO;
	info->in_place_pair = CLAP_INVALID_ID;
	return true;
}

static const clap_plugin_audio_ports_t audio_ports = {
	.count = audio_ports_count,
	.get = audio_ports_get,
};

// note-ports: one input, taking CLAP notes or MIDI

static uint32_t note_ports_count(const clap_plugin_t *plugin, bool is_input) {
	return is_input ? 1 : 0;
}

static bool note_ports_get(const clap_plugin_t *plugin, uint32_t index, bool is_input, clap_note_port_info_t *info) {
	if (!is_input || index != 0)
		return false;
	info->id = 0;
	info->supported_dialects = CLAP_NOTE_DIALECT_CLAP | CLAP_NOTE_DIALECT_MIDI;
	info->preferred_dialect = CLAP_NOTE_DIALECT_MIDI;
	strncpy(info->name, "MIDI In", sizeof(info->name));
	return true;
}

static const clap_plugin_note_ports_t note_ports = {
	.count = note_ports_count,
	.get = note_ports_get,
};

// state: the same recovery data the VST2 version stores in its chunk

static bool state_save(const clap_plugin_t *plugin, const clap_ostream_t *stream) {
	struct sointuState_return st = sointuState(self(plugin)->handle);
	if (st.r0 == NULL)
		return false;
	bool ok = true;
	for (uint64_t written = 0; written < st.r1;) {
		int64_t n = stream->write(stream, (const char *)st.r0 + written, st.r1 - written);
		if (n <= 0) {
			ok = false;
			break;
		}
		written += n;
	}
	free(st.r0);
	return ok;
}

static bool state_load(const clap_plugin_t *plugin, const clap_istream_t *stream) {
	size_t size = 0, cap = 65536;
	char *data = malloc(cap);
	if (data == NULL)
		return false;
	for (;;) {
		if (size == cap) {
			char *grown = realloc(data, cap *= 2);
			if (grown == NULL) {
				free(data);
				return false;
			}
			data = grown;
		}
		int64_t n = stream->read(stream, data + size, cap - size);
		if (n == 0)
			break;
		if (n < 0) {
			free(data);
			return false;
		}
		size += n;
	}
	if (size == 0) {
		free(data);
		return false;
	}
	sointuSetState(self(plugin)->handle, data, size);
	free(data);
	return true;
}

static const clap_plugin_state_t state = {
	.save = state_save,
	.load = state_load,
};

// plugin

static bool plugin_init(const clap_plugin_t *plugin) {
	self(plugin)->handle = sointuNew();
	return true;
}

static void plugin_destroy(const clap_plugin_t *plugin) {
	sointu_plugin_t *p = self(plugin);
	if (p->handle)
		sointuClose(p->handle);
	free(p);
}

static bool plugin_activate(const clap_plugin_t *plugin, double sample_rate, uint32_t min_frames, uint32_t max_frames) {
	self(plugin)->sample_rate = sample_rate;
	return true;
}

static void plugin_deactivate(const clap_plugin_t *plugin) {}

static bool plugin_start_processing(const clap_plugin_t *plugin) { return true; }

static void plugin_stop_processing(const clap_plugin_t *plugin) {}

static void plugin_reset(const clap_plugin_t *plugin) {}

static void handle_event(sointu_plugin_t *p, const clap_event_header_t *hdr) {
	if (hdr->space_id != CLAP_CORE_EVENT_SPACE_ID)
		return;
	switch (hdr->type) {
	case CLAP_EVENT_NOTE_ON:
	case CLAP_EVENT_NOTE_OFF: {
		const clap_event_note_t *ev = (const clap_event_note_t *)hdr;
		// Wildcard notes cannot be expressed as MIDI; skip them
		if (ev->key < 0 || ev->key > 127)
			break;
		uint8_t status = hdr->type == CLAP_EVENT_NOTE_ON ? 0x90 : 0x80;
		uint8_t channel = ev->channel >= 0 && ev->channel < 16 ? ev->channel : 0;
		double v = ev->velocity * 127.0 + 0.5;
		uint8_t velocity = v < 0 ? 0 : v > 127 ? 127 : (uint8_t)v;
		// MIDI note on with velocity 0 means note off
		if (status == 0x90 && velocity == 0)
			velocity = 1;
		sointuMIDI(p->handle, hdr->time, status | channel, ev->key, velocity);
		break;
	}
	case CLAP_EVENT_MIDI: {
		const clap_event_midi_t *ev = (const clap_event_midi_t *)hdr;
		sointuMIDI(p->handle, hdr->time, ev->data[0], ev->data[1], ev->data[2]);
		break;
	}
	}
}

static clap_process_status plugin_process(const clap_plugin_t *plugin, const clap_process_t *process) {
	sointu_plugin_t *p = self(plugin);
	uint32_t n = process->in_events->size(process->in_events);
	for (uint32_t i = 0; i < n; i++)
		handle_event(p, process->in_events->get(process->in_events, i));
	if (process->audio_outputs_count < 1 || process->audio_outputs[0].channel_count < 2)
		return CLAP_PROCESS_ERROR;
	const clap_event_transport_t *t = process->transport;
	bool has_tempo = t != NULL && (t->flags & CLAP_TRANSPORT_HAS_TEMPO) && t->tempo > 0;
	float **out = process->audio_outputs[0].data32;
	sointuProcess(p->handle, out[0], out[1], process->frames_count, p->sample_rate, has_tempo, has_tempo ? t->tempo : 0);
	return CLAP_PROCESS_CONTINUE;
}

static const void *plugin_get_extension(const clap_plugin_t *plugin, const char *id) {
	if (strcmp(id, CLAP_EXT_AUDIO_PORTS) == 0)
		return &audio_ports;
	if (strcmp(id, CLAP_EXT_NOTE_PORTS) == 0)
		return &note_ports;
	if (strcmp(id, CLAP_EXT_STATE) == 0)
		return &state;
	return NULL;
}

static void plugin_on_main_thread(const clap_plugin_t *plugin) {}

// factory

static uint32_t factory_get_plugin_count(const clap_plugin_factory_t *factory) { return 1; }

static const clap_plugin_descriptor_t *factory_get_plugin_descriptor(const clap_plugin_factory_t *factory, uint32_t index) {
	return index == 0 ? &descriptor : NULL;
}

static const clap_plugin_t *factory_create_plugin(const clap_plugin_factory_t *factory, const clap_host_t *host, const char *plugin_id) {
	if (!clap_version_is_compatible(host->clap_version) || strcmp(plugin_id, descriptor.id) != 0)
		return NULL;
	sointu_plugin_t *p = calloc(1, sizeof(*p));
	if (p == NULL)
		return NULL;
	p->host = host;
	p->sample_rate = 44100;
	p->plugin = (clap_plugin_t){
		.desc = &descriptor,
		.plugin_data = p,
		.init = plugin_init,
		.destroy = plugin_destroy,
		.activate = plugin_activate,
		.deactivate = plugin_deactivate,
		.start_processing = plugin_start_processing,
		.stop_processing = plugin_stop_processing,
		.reset = plugin_reset,
		.process = plugin_process,
		.get_extension = plugin_get_extension,
		.on_main_thread = plugin_on_main_thread,
	};
	return &p->plugin;
}

static const clap_plugin_factory_t factory = {
	.get_plugin_count = factory_get_plugin_count,
	.get_plugin_descriptor = factory_get_plugin_descriptor,
	.create_plugin = factory_create_plugin,
};

// entry

static bool entry_init(const char *plugin_path) {
	descriptor.version = sointuVersion();
	return true;
}

static void entry_deinit(void) {}

static const void *entry_get_factory(const char *factory_id) {
	return strcmp(factory_id, CLAP_PLUGIN_FACTORY_ID) == 0 ? &factory : NULL;
}

CLAP_EXPORT const clap_plugin_entry_t clap_entry = {
	.clap_version = CLAP_VERSION_INIT,
	.init = entry_init,
	.deinit = entry_deinit,
	.get_factory = entry_get_factory,
};
