//go:build plugin && darwin

// Audio Unit (version 2) factory, plugin interface and view. Everything
// host-independent is forwarded to the Go side in main.go.
//
// The unit is a music device ('aumu') with no input and one stereo output,
// of 32-bit float, non-interleaved. It has no parameters; the song is its
// class info.

#import <AudioToolbox/AudioToolbox.h>
#import <AudioUnit/AUCocoaUIView.h>
#import <Cocoa/Cocoa.h>
#include <pthread.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#include "_cgo_export.h"

#define SOINTU_TYPE kAudioUnitType_MusicDevice
#define SOINTU_SUBTYPE 'Sntu'
#define SOINTU_MANUFACTURER 'Vsar'

// kSointuProperty_Self gives the view the unit that it is the view of
#define kSointuProperty_Self 0x536e7475

#define STATE_KEY CFSTR("sointu-state")
#define MAX_LISTENERS 64
#define MAX_NOTIFY 16
#define DEFAULT_MAX_FRAMES 1156

typedef struct {
	AudioUnitPropertyID id;
	AudioUnitPropertyListenerProc proc;
	void *user;
} listener_t;

typedef struct sointu_au {
	AudioComponentPlugInInterface iface; // must be first
	struct sointu_au *next;              // of the units that are open
	AudioComponentInstance instance;
	uintptr_t handle; // cgo.Handle of the Go instance
	bool initialized;
	bool managed; // the host asked for the view: the window opens with it
	Float64 sample_rate;
	UInt32 max_frames;
	UInt32 latency; // of the resampler at sample_rate, in frames
	OSStatus last_error;
	HostCallbackInfo host;
	AUPreset preset;
	listener_t listeners[MAX_LISTENERS];
	int listener_count;
	AURenderCallbackStruct notify[MAX_NOTIFY];
	int notify_count;
	float *buffers[2]; // for a host that renders without buffers of its own
} sointu_au_t;

// The units that are open. A view, and a change told on the main thread, can
// outlive their unit: they look it up here first.
static pthread_mutex_t open_lock = PTHREAD_MUTEX_INITIALIZER;
static sointu_au_t *open_units;

static bool is_open(sointu_au_t *p) {
	for (sointu_au_t *u = open_units; u != NULL; u = u->next)
		if (u == p)
			return true;
	return false;
}

static void show_window(sointu_au_t *p) {
	pthread_mutex_lock(&open_lock);
	if (is_open(p))
		sointuShowWindow(p->handle);
	pthread_mutex_unlock(&open_lock);
}

static void property_changed(sointu_au_t *p, AudioUnitPropertyID id, AudioUnitScope scope) {
	for (int i = 0; i < p->listener_count; i++)
		if (p->listeners[i].id == id)
			p->listeners[i].proc(p->listeners[i].user, p->instance, id, scope, 0);
}

// sointu_mark_dirty tells the host that the song changed. Audio Units have
// no call for that; a change of the present preset is what hosts listen
// for. Called from any thread; the host is told on the main thread.
void sointu_mark_dirty(uintptr_t plugin) {
	sointu_au_t *p = (sointu_au_t *)plugin;
	dispatch_async(dispatch_get_main_queue(), ^{
		pthread_mutex_lock(&open_lock);
		bool open = is_open(p);
		pthread_mutex_unlock(&open_lock);
		if (open)
			property_changed(p, kAudioUnitProperty_PresentPreset, kAudioUnitScope_Global);
	});
}

// view: the tracker is a window of its own, not a view in a window of the
// host. The view that the host gets says so, and opens that window: when
// the host shows the view, and with a button. The user can then close the
// window, and gets it back from the host.

@interface SointuAUView : NSView {
  @public
	sointu_au_t *unit;
}
@end

@implementation SointuAUView
- (void)showTracker:(id)sender {
	show_window(unit);
}

- (void)viewDidMoveToWindow {
	[super viewDidMoveToWindow];
	if (self.window != nil)
		show_window(unit);
}
@end

@interface SointuAUViewFactory : NSObject <AUCocoaUIBase>
@end

@implementation SointuAUViewFactory
- (unsigned)interfaceVersion {
	return 0;
}

- (NSView *)uiViewForAudioUnit:(AudioUnit)inAudioUnit withSize:(NSSize)inPreferredSize {
	sointu_au_t *p = NULL;
	UInt32 size = sizeof(p);
	if (AudioUnitGetProperty(inAudioUnit, kSointuProperty_Self, kAudioUnitScope_Global, 0, &p, &size) != noErr || p == NULL)
		return nil;
	SointuAUView *view = [[SointuAUView alloc] initWithFrame:NSMakeRect(0, 0, 360, 84)];
	view->unit = p;
	NSTextField *label = [NSTextField labelWithString:@"The Sointu tracker is a window of its own."];
	label.frame = NSMakeRect(16, 48, 328, 20);
	[view addSubview:label];
	NSButton *button = [NSButton buttonWithTitle:@"Show window" target:view action:@selector(showTracker:)];
	button.frame = NSMakeRect(12, 12, 130, 28);
	[view addSubview:button];
	return [view autorelease];
}
@end

// properties

static OSStatus output_element(AudioUnitScope scope, AudioUnitElement elem) {
	if (scope == kAudioUnitScope_Input)
		return kAudioUnitErr_InvalidElement; // there are no inputs
	if (scope != kAudioUnitScope_Output)
		return kAudioUnitErr_InvalidScope;
	return elem == 0 ? noErr : kAudioUnitErr_InvalidElement;
}

static OSStatus property_info(AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, UInt32 *size, Boolean *writable) {
	bool global = true; // most properties are of the unit as a whole
	*writable = false;
	switch (id) {
	case kAudioUnitProperty_ClassInfo:
		*size = sizeof(CFPropertyListRef);
		*writable = true;
		break;
	case kAudioUnitProperty_SampleRate:
		*size = sizeof(Float64);
		*writable = true;
		return output_element(scope, elem);
	case kAudioUnitProperty_StreamFormat:
		*size = sizeof(AudioStreamBasicDescription);
		*writable = true;
		return output_element(scope, elem);
	case kAudioUnitProperty_ParameterList:
		*size = 0;
		global = false;
		break;
	case kAudioUnitProperty_ElementCount:
		*size = sizeof(UInt32);
		global = false;
		break;
	case kAudioUnitProperty_Latency:
		*size = sizeof(Float64);
		break;
	case kAudioUnitProperty_SupportedNumChannels:
		*size = sizeof(AUChannelInfo);
		break;
	case kAudioUnitProperty_MaximumFramesPerSlice:
		*size = sizeof(UInt32);
		*writable = true;
		break;
	case kAudioUnitProperty_LastRenderError:
		*size = sizeof(OSStatus);
		break;
	case kAudioUnitProperty_HostCallbacks:
		*size = sizeof(HostCallbackInfo);
		*writable = true;
		break;
	case kAudioUnitProperty_CocoaUI:
		*size = sizeof(AudioUnitCocoaViewInfo);
		break;
	case kAudioUnitProperty_PresentPreset:
		*size = sizeof(AUPreset);
		*writable = true;
		break;
	case kMusicDeviceProperty_InstrumentCount:
		*size = sizeof(UInt32);
		break;
	case kSointuProperty_Self:
		*size = sizeof(sointu_au_t *);
		break;
	default:
		return kAudioUnitErr_InvalidProperty;
	}
	if (global)
		return scope == kAudioUnitScope_Global ? noErr : kAudioUnitErr_InvalidScope;
	if (scope != kAudioUnitScope_Global && scope != kAudioUnitScope_Input && scope != kAudioUnitScope_Output)
		return kAudioUnitErr_InvalidScope;
	return noErr;
}

static void stream_format(sointu_au_t *p, AudioStreamBasicDescription *f) {
	memset(f, 0, sizeof(*f));
	f->mSampleRate = p->sample_rate;
	f->mFormatID = kAudioFormatLinearPCM;
	f->mFormatFlags = kAudioFormatFlagsNativeFloatPacked | kAudioFormatFlagIsNonInterleaved;
	f->mBytesPerPacket = sizeof(Float32);
	f->mFramesPerPacket = 1;
	f->mBytesPerFrame = sizeof(Float32);
	f->mChannelsPerFrame = 2;
	f->mBitsPerChannel = 32;
}

static void set_number(CFMutableDictionaryRef dict, CFStringRef key, SInt32 value) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberSInt32Type, &value);
	CFDictionarySetValue(dict, key, n);
	CFRelease(n);
}

static bool number_is(CFDictionaryRef dict, CFStringRef key, SInt32 want) {
	CFTypeRef n = CFDictionaryGetValue(dict, key);
	SInt32 value;
	return n != NULL && CFGetTypeID(n) == CFNumberGetTypeID() && CFNumberGetValue(n, kCFNumberSInt32Type, &value) && value == want;
}

// class_info returns the state of the unit: the keys that every Audio Unit
// has, and the same recovery data that the VST2 version stores in its chunk.
static CFPropertyListRef class_info(sointu_au_t *p) {
	CFMutableDictionaryRef dict = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	set_number(dict, CFSTR(kAUPresetVersionKey), 0);
	set_number(dict, CFSTR(kAUPresetTypeKey), SOINTU_TYPE);
	set_number(dict, CFSTR(kAUPresetSubtypeKey), SOINTU_SUBTYPE);
	set_number(dict, CFSTR(kAUPresetManufacturerKey), SOINTU_MANUFACTURER);
	CFDictionarySetValue(dict, CFSTR(kAUPresetNameKey), p->preset.presetName);
	CFDataRef empty = CFDataCreate(NULL, NULL, 0);
	CFDictionarySetValue(dict, CFSTR(kAUPresetDataKey), empty);
	CFRelease(empty);
	struct sointuState_return st = sointuState(p->handle);
	if (st.r0 != NULL) {
		CFDataRef data = CFDataCreate(NULL, st.r0, st.r1);
		CFDictionarySetValue(dict, STATE_KEY, data);
		CFRelease(data);
		free(st.r0);
	}
	return dict;
}

static void set_preset_name(sointu_au_t *p, CFStringRef name) {
	CFRetain(name);
	CFRelease(p->preset.presetName);
	p->preset.presetName = name;
}

static OSStatus set_class_info(sointu_au_t *p, CFPropertyListRef plist) {
	if (plist == NULL || CFGetTypeID(plist) != CFDictionaryGetTypeID())
		return kAudioUnitErr_InvalidPropertyValue;
	CFDictionaryRef dict = plist;
	if (!number_is(dict, CFSTR(kAUPresetTypeKey), SOINTU_TYPE) ||
		!number_is(dict, CFSTR(kAUPresetSubtypeKey), SOINTU_SUBTYPE) ||
		!number_is(dict, CFSTR(kAUPresetManufacturerKey), SOINTU_MANUFACTURER))
		return kAudioUnitErr_InvalidPropertyValue;
	CFTypeRef name = CFDictionaryGetValue(dict, CFSTR(kAUPresetNameKey));
	if (name != NULL && CFGetTypeID(name) == CFStringGetTypeID()) {
		set_preset_name(p, name);
		p->preset.presetNumber = -1;
	}
	CFTypeRef data = CFDictionaryGetValue(dict, STATE_KEY);
	if (data != NULL && CFGetTypeID(data) == CFDataGetTypeID() && CFDataGetLength(data) > 0)
		sointuSetState(p->handle, (void *)CFDataGetBytePtr(data), CFDataGetLength(data));
	return noErr;
}

static OSStatus au_get_property_info(void *self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, UInt32 *outSize, Boolean *outWritable) {
	UInt32 size = 0;
	Boolean writable = false;
	OSStatus err = property_info(id, scope, elem, &size, &writable);
	if (err != noErr)
		return err;
	if (outSize != NULL)
		*outSize = size;
	if (outWritable != NULL)
		*outWritable = writable;
	return noErr;
}

static OSStatus au_get_property(void *self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, void *outData, UInt32 *ioSize) {
	sointu_au_t *p = self;
	UInt32 size = 0;
	Boolean writable = false;
	OSStatus err = property_info(id, scope, elem, &size, &writable);
	if (err != noErr)
		return err;
	if (ioSize == NULL)
		return kAudio_ParamError;
	if (outData == NULL) {
		*ioSize = size;
		return noErr;
	}
	if (*ioSize < size)
		return kAudioUnitErr_InvalidPropertyValue;
	switch (id) {
	case kAudioUnitProperty_ClassInfo:
		*(CFPropertyListRef *)outData = class_info(p);
		break;
	case kAudioUnitProperty_SampleRate:
		*(Float64 *)outData = p->sample_rate;
		break;
	case kAudioUnitProperty_StreamFormat:
		stream_format(p, outData);
		break;
	case kAudioUnitProperty_ParameterList:
		break;
	case kAudioUnitProperty_ElementCount:
		*(UInt32 *)outData = scope == kAudioUnitScope_Input ? 0 : 1;
		break;
	case kAudioUnitProperty_Latency:
		*(Float64 *)outData = p->latency / p->sample_rate;
		break;
	case kAudioUnitProperty_SupportedNumChannels:
		*(AUChannelInfo *)outData = (AUChannelInfo){.inChannels = 0, .outChannels = 2};
		break;
	case kAudioUnitProperty_MaximumFramesPerSlice:
		*(UInt32 *)outData = p->max_frames;
		break;
	case kAudioUnitProperty_LastRenderError:
		*(OSStatus *)outData = p->last_error;
		p->last_error = noErr;
		break;
	case kAudioUnitProperty_HostCallbacks:
		*(HostCallbackInfo *)outData = p->host;
		break;
	case kAudioUnitProperty_CocoaUI: {
		// the caller releases both
		AudioUnitCocoaViewInfo *info = outData;
		info->mCocoaAUViewBundleLocation = (CFURLRef)[[[NSBundle bundleForClass:[SointuAUViewFactory class]] bundleURL] retain];
		info->mCocoaAUViewClass[0] = CFSTR("SointuAUViewFactory");
		if (!p->managed) {
			p->managed = true;
			sointuManageWindow(p->handle);
		}
		break;
	}
	case kAudioUnitProperty_PresentPreset:
		// the caller releases the name
		CFRetain(p->preset.presetName);
		*(AUPreset *)outData = p->preset;
		break;
	case kMusicDeviceProperty_InstrumentCount:
		*(UInt32 *)outData = 0;
		break;
	case kSointuProperty_Self:
		*(sointu_au_t **)outData = p;
		break;
	}
	*ioSize = size;
	return noErr;
}

static OSStatus au_set_property(void *self, AudioUnitPropertyID id, AudioUnitScope scope, AudioUnitElement elem, const void *data, UInt32 dataSize) {
	sointu_au_t *p = self;
	UInt32 size = 0;
	Boolean writable = false;
	OSStatus err = property_info(id, scope, elem, &size, &writable);
	if (err != noErr)
		return err;
	if (!writable)
		return kAudioUnitErr_PropertyNotWritable;
	if (data == NULL || dataSize < size)
		return kAudioUnitErr_InvalidPropertyValue;
	switch (id) {
	case kAudioUnitProperty_ClassInfo:
		err = set_class_info(p, *(CFPropertyListRef *)data);
		if (err == noErr)
			property_changed(p, kAudioUnitProperty_PresentPreset, kAudioUnitScope_Global);
		return err;
	case kAudioUnitProperty_SampleRate: {
		Float64 rate = *(const Float64 *)data;
		if (p->initialized)
			return kAudioUnitErr_Initialized;
		if (!(rate > 0))
			return kAudioUnitErr_InvalidPropertyValue;
		p->sample_rate = rate;
		break;
	}
	case kAudioUnitProperty_StreamFormat: {
		const AudioStreamBasicDescription *f = data;
		AudioStreamBasicDescription want;
		if (p->initialized)
			return kAudioUnitErr_Initialized;
		stream_format(p, &want);
		if (f->mFormatID != want.mFormatID || f->mFormatFlags != want.mFormatFlags ||
			f->mChannelsPerFrame != want.mChannelsPerFrame || f->mBitsPerChannel != want.mBitsPerChannel ||
			f->mBytesPerFrame != want.mBytesPerFrame || !(f->mSampleRate > 0))
			return kAudioUnitErr_FormatNotSupported;
		p->sample_rate = f->mSampleRate;
		break;
	}
	case kAudioUnitProperty_MaximumFramesPerSlice:
		if (p->initialized)
			return kAudioUnitErr_Initialized;
		p->max_frames = *(const UInt32 *)data;
		break;
	case kAudioUnitProperty_HostCallbacks:
		p->host = *(const HostCallbackInfo *)data;
		break;
	case kAudioUnitProperty_PresentPreset: {
		const AUPreset *preset = data;
		if (preset->presetName == NULL)
			return kAudioUnitErr_InvalidPropertyValue;
		set_preset_name(p, preset->presetName);
		p->preset.presetNumber = preset->presetNumber;
		break;
	}
	}
	property_changed(p, id, scope);
	return noErr;
}

static OSStatus au_add_property_listener(void *self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, void *user) {
	sointu_au_t *p = self;
	if (p->listener_count == MAX_LISTENERS)
		return kAudio_MemFullError;
	p->listeners[p->listener_count++] = (listener_t){.id = id, .proc = proc, .user = user};
	return noErr;
}

static void remove_listeners(sointu_au_t *p, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, void *user, bool any_user) {
	int n = 0;
	for (int i = 0; i < p->listener_count; i++) {
		listener_t l = p->listeners[i];
		if (l.id == id && l.proc == proc && (any_user || l.user == user))
			continue;
		p->listeners[n++] = l;
	}
	p->listener_count = n;
}

static OSStatus au_remove_property_listener(void *self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc) {
	remove_listeners(self, id, proc, NULL, true);
	return noErr;
}

static OSStatus au_remove_property_listener_with_user_data(void *self, AudioUnitPropertyID id, AudioUnitPropertyListenerProc proc, void *user) {
	remove_listeners(self, id, proc, user, false);
	return noErr;
}

// parameters: there are none

static OSStatus au_get_parameter(void *self, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitElement elem, AudioUnitParameterValue *value) {
	return kAudioUnitErr_InvalidParameter;
}

static OSStatus au_set_parameter(void *self, AudioUnitParameterID id, AudioUnitScope scope, AudioUnitElement elem, AudioUnitParameterValue value, UInt32 offset) {
	return kAudioUnitErr_InvalidParameter;
}

static OSStatus au_schedule_parameters(void *self, const AudioUnitParameterEvent *events, UInt32 count) {
	return count == 0 ? noErr : kAudioUnitErr_InvalidParameter;
}

// rendering

static void free_buffers(sointu_au_t *p) {
	for (int i = 0; i < 2; i++) {
		free(p->buffers[i]);
		p->buffers[i] = NULL;
	}
}

static OSStatus au_initialize(void *self) {
	sointu_au_t *p = self;
	if (p->initialized)
		return noErr;
	for (int i = 0; i < 2; i++) {
		p->buffers[i] = calloc(p->max_frames > 0 ? p->max_frames : 1, sizeof(float));
		if (p->buffers[i] == NULL) {
			free_buffers(p);
			return kAudio_MemFullError;
		}
	}
	// the synth runs at 44100 Hz; at another rate the plugin resamples,
	// which delays the output
	UInt32 latency = sointuSetSampleRate(p->handle, p->sample_rate);
	p->initialized = true;
	if (latency != p->latency) {
		p->latency = latency;
		property_changed(p, kAudioUnitProperty_Latency, kAudioUnitScope_Global);
	}
	return noErr;
}

static OSStatus au_uninitialize(void *self) {
	sointu_au_t *p = self;
	p->initialized = false;
	free_buffers(p);
	return noErr;
}

static OSStatus au_reset(void *self, AudioUnitScope scope, AudioUnitElement elem) { return noErr; }

static OSStatus au_add_render_notify(void *self, AURenderCallback proc, void *user) {
	sointu_au_t *p = self;
	if (p->notify_count == MAX_NOTIFY)
		return kAudio_MemFullError;
	p->notify[p->notify_count] = (AURenderCallbackStruct){.inputProc = proc, .inputProcRefCon = user};
	p->notify_count++;
	return noErr;
}

static OSStatus au_remove_render_notify(void *self, AURenderCallback proc, void *user) {
	sointu_au_t *p = self;
	int n = 0;
	for (int i = 0; i < p->notify_count; i++) {
		AURenderCallbackStruct c = p->notify[i];
		if (c.inputProc == proc && c.inputProcRefCon == user)
			continue;
		p->notify[n++] = c;
	}
	p->notify_count = n;
	return noErr;
}

static void render_notify(sointu_au_t *p, AudioUnitRenderActionFlags flags, const AudioTimeStamp *time, UInt32 bus, UInt32 frames, AudioBufferList *io) {
	for (int i = 0; i < p->notify_count; i++) {
		AudioUnitRenderActionFlags f = flags;
		p->notify[i].inputProc(p->notify[i].inputProcRefCon, &f, time, bus, frames, io);
	}
}

static OSStatus render(sointu_au_t *p, AudioUnitRenderActionFlags flags, const AudioTimeStamp *time, UInt32 bus, UInt32 frames, AudioBufferList *io) {
	if (!p->initialized)
		return kAudioUnitErr_Uninitialized;
	if (bus != 0)
		return kAudioUnitErr_InvalidElement;
	if (frames > p->max_frames)
		return kAudioUnitErr_TooManyFramesToProcess;
	if (io == NULL || io->mNumberBuffers != 2)
		return kAudio_ParamError;
	UInt32 bytes = frames * sizeof(float);
	for (int i = 0; i < 2; i++) {
		if (io->mBuffers[i].mData == NULL)
			io->mBuffers[i].mData = p->buffers[i];
		else if (io->mBuffers[i].mDataByteSize < bytes)
			return kAudio_ParamError;
		io->mBuffers[i].mDataByteSize = bytes;
	}
	render_notify(p, flags | kAudioUnitRenderAction_PreRender, time, bus, frames, io);
	Float64 beat = 0, tempo = 0;
	bool has_tempo = p->host.beatAndTempoProc != NULL &&
		p->host.beatAndTempoProc(p->host.hostUserData, &beat, &tempo) == noErr && tempo > 0;
	sointuProcess(p->handle, io->mBuffers[0].mData, io->mBuffers[1].mData, frames, p->sample_rate, has_tempo, has_tempo ? tempo : 0);
	render_notify(p, flags | kAudioUnitRenderAction_PostRender, time, bus, frames, io);
	return noErr;
}

static OSStatus au_render(void *self, AudioUnitRenderActionFlags *ioFlags, const AudioTimeStamp *time, UInt32 bus, UInt32 frames, AudioBufferList *io) {
	sointu_au_t *p = self;
	OSStatus err = render(p, ioFlags != NULL ? *ioFlags : 0, time, bus, frames, io);
	if (err != noErr && p->last_error == noErr)
		p->last_error = err;
	return err;
}

// notes: MIDI, and the note calls of a music device, which become MIDI

static OSStatus au_midi_event(void *self, UInt32 status, UInt32 data1, UInt32 data2, UInt32 offset) {
	sointu_au_t *p = self;
	sointuMIDI(p->handle, offset, status, data1, data2);
	return noErr;
}

static OSStatus au_sysex(void *self, const UInt8 *data, UInt32 length) { return noErr; }

static OSStatus au_prepare_instrument(void *self, MusicDeviceInstrumentID instrument) { return noErr; }

static OSStatus au_release_instrument(void *self, MusicDeviceInstrumentID instrument) { return noErr; }

static UInt8 midi_value(Float32 v) { return v < 0 ? 0 : v > 127 ? 127 : (UInt8)(v + 0.5f); }

static OSStatus au_start_note(void *self, MusicDeviceInstrumentID instrument, MusicDeviceGroupID group, NoteInstanceID *outNote, UInt32 offset, const MusicDeviceNoteParams *params) {
	sointu_au_t *p = self;
	if (params == NULL || params->argCount < 2)
		return kAudio_ParamError;
	UInt8 key = midi_value(params->mPitch), velocity = midi_value(params->mVelocity);
	// MIDI note on with velocity 0 means note off
	sointuMIDI(p->handle, offset, 0x90 | (group & 0xF), key, velocity == 0 ? 1 : velocity);
	if (outNote != NULL)
		*outNote = key;
	return noErr;
}

static OSStatus au_stop_note(void *self, MusicDeviceGroupID group, NoteInstanceID note, UInt32 offset) {
	sointu_au_t *p = self;
	sointuMIDI(p->handle, offset, 0x80 | (group & 0xF), note & 0x7F, 0);
	return noErr;
}

// component

static OSStatus au_open(void *self, AudioComponentInstance instance) {
	sointu_au_t *p = self;
	p->instance = instance;
	p->handle = sointuNew((uintptr_t)p);
	pthread_mutex_lock(&open_lock);
	p->next = open_units;
	open_units = p;
	pthread_mutex_unlock(&open_lock);
	return noErr;
}

static OSStatus au_close(void *self) {
	sointu_au_t *p = self;
	pthread_mutex_lock(&open_lock);
	for (sointu_au_t **u = &open_units; *u != NULL; u = &(*u)->next) {
		if (*u == p) {
			*u = p->next;
			break;
		}
	}
	pthread_mutex_unlock(&open_lock);
	if (p->handle)
		sointuClose(p->handle);
	free_buffers(p);
	CFRelease(p->preset.presetName);
	free(p);
	return noErr;
}

static AudioComponentMethod au_lookup(SInt16 selector) {
	switch (selector) {
	case kAudioUnitInitializeSelect:
		return (AudioComponentMethod)au_initialize;
	case kAudioUnitUninitializeSelect:
		return (AudioComponentMethod)au_uninitialize;
	case kAudioUnitGetPropertyInfoSelect:
		return (AudioComponentMethod)au_get_property_info;
	case kAudioUnitGetPropertySelect:
		return (AudioComponentMethod)au_get_property;
	case kAudioUnitSetPropertySelect:
		return (AudioComponentMethod)au_set_property;
	case kAudioUnitAddPropertyListenerSelect:
		return (AudioComponentMethod)au_add_property_listener;
	case kAudioUnitRemovePropertyListenerSelect:
		return (AudioComponentMethod)au_remove_property_listener;
	case kAudioUnitRemovePropertyListenerWithUserDataSelect:
		return (AudioComponentMethod)au_remove_property_listener_with_user_data;
	case kAudioUnitAddRenderNotifySelect:
		return (AudioComponentMethod)au_add_render_notify;
	case kAudioUnitRemoveRenderNotifySelect:
		return (AudioComponentMethod)au_remove_render_notify;
	case kAudioUnitGetParameterSelect:
		return (AudioComponentMethod)au_get_parameter;
	case kAudioUnitSetParameterSelect:
		return (AudioComponentMethod)au_set_parameter;
	case kAudioUnitScheduleParametersSelect:
		return (AudioComponentMethod)au_schedule_parameters;
	case kAudioUnitRenderSelect:
		return (AudioComponentMethod)au_render;
	case kAudioUnitResetSelect:
		return (AudioComponentMethod)au_reset;
	case kMusicDeviceMIDIEventSelect:
		return (AudioComponentMethod)au_midi_event;
	case kMusicDeviceSysExSelect:
		return (AudioComponentMethod)au_sysex;
	case kMusicDevicePrepareInstrumentSelect:
		return (AudioComponentMethod)au_prepare_instrument;
	case kMusicDeviceReleaseInstrumentSelect:
		return (AudioComponentMethod)au_release_instrument;
	case kMusicDeviceStartNoteSelect:
		return (AudioComponentMethod)au_start_note;
	case kMusicDeviceStopNoteSelect:
		return (AudioComponentMethod)au_stop_note;
	}
	return NULL;
}

// SointuAUFactory is the factory function that Info.plist names.
__attribute__((visibility("default"))) void *SointuAUFactory(const AudioComponentDescription *desc) {
	sointu_au_t *p = calloc(1, sizeof(*p));
	if (p == NULL)
		return NULL;
	p->iface.Open = au_open;
	p->iface.Close = au_close;
	p->iface.Lookup = au_lookup;
	p->sample_rate = 44100;
	p->max_frames = DEFAULT_MAX_FRAMES;
	p->preset = (AUPreset){.presetNumber = -1, .presetName = CFSTR("Untitled")};
	return p;
}
