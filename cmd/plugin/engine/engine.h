// The C interface of the Go side of the VST3, CLAP and Audio Unit plugins:
// what their C layers (cmd/sointu-vst3/vst3.c, cmd/sointu-clap/clap.c,
// cmd/sointu-au/au_darwin.m) call. A release plugin links the Go side in
// (package engine, which implements this); the dev loader
// (cmd/plugin/loader) implements it by forwarding to an engine library that
// it loads, and can load a newer one while the host runs.
//
// The prototypes must match those that cgo writes for the exports of
// package engine, which includes this header to check that they do.

#ifndef SOINTU_ENGINE_H
#define SOINTU_ENGINE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

// SOINTU_ENGINE_ABI is the version of this interface. The loader loads only
// an engine of the same version; change it with any change of this file.
#define SOINTU_ENGINE_ABI 1

// sointu_callbacks are what the Go side tells the C layer, each with the
// plugin pointer given to sointuNew. They are called from any thread.
typedef struct sointu_callbacks {
	// mark_dirty tells that the song changed: the project has unsaved
	// changes
	void (*mark_dirty)(uintptr_t plugin);
	// params_changed tells that the names of the parameters changed, or
	// with values their values
	void (*params_changed)(uintptr_t plugin, bool values);
	// window_closed tells that the user closed the window of a host that
	// manages it (sointuManageWindow); NULL if the host is not told
	void (*window_closed)(uintptr_t plugin);
	// request_reload asks to move the instance to the newest engine; set by
	// the dev loader alone, NULL otherwise
	void (*request_reload)(uintptr_t plugin);
} sointu_callbacks;

int32_t sointuEngineABI(void);
// sointuVersion returns the version of the build; never freed
char *sointuVersion(void);
int32_t sointuMIDIPorts(void);
int32_t sointuParams(void);
double sointuParamRange(void);

// sointuNew makes an instance and returns its handle. kind names the plugin
// (e.g. "sointu-vst3"), for its recovery file and MCP. cb is copied.
uintptr_t sointuNew(uintptr_t plugin, char *kind, sointu_callbacks *cb);
void sointuClose(uintptr_t h);

void sointuManageWindow(uintptr_t h);
void sointuShowWindow(uintptr_t h);
void sointuHideWindow(uintptr_t h);
void sointuWindowTitle(uintptr_t h, char *title);
// sointuWindowShown tells if the window is open
bool sointuWindowShown(uintptr_t h);

void sointuMIDI(uintptr_t h, int32_t time, int32_t port, uint8_t d0, uint8_t d1, uint8_t d2);
void sointuParam(uintptr_t h, int32_t time, int32_t id, double value, bool mod);
double sointuParamValue(uintptr_t h, int32_t id);
// sointuParamText and sointuParamName return memory from malloc, which the
// caller frees
char *sointuParamText(uintptr_t h, int32_t id, double value);
double sointuParamFromSteps(uintptr_t h, int32_t id, double steps);
char *sointuParamName(uintptr_t h, int32_t id);

// sointuSetSampleRate returns the latency at the rate, in frames
uint32_t sointuSetSampleRate(uintptr_t h, double rate);
void sointuProcess(uintptr_t h, float *left, float *right, int32_t frames, double rate, bool has_tempo, double tempo);

// sointuState returns the state in memory from malloc, which the caller
// frees, and its size, or NULL
void *sointuState(uintptr_t h, size_t *size);
void sointuSetState(uintptr_t h, void *data, size_t size);

// sointuReloadAvailable tells the instance that another engine is installed,
// named build; only the dev loader calls it
void sointuReloadAvailable(uintptr_t h, char *build);
// sointuAlert shows a message among the notifications of the tracker, as a
// warning; the dev loader tells its errors with it
void sointuAlert(uintptr_t h, char *message);

#endif
