// The dev loader: engine.h implemented by forwarding to an engine library,
// the Go side of the plugins built alone (cmd/sointu-engine), which it loads
// from the engine folder, and which it can replace with a newer build while
// the host runs. Linked with the C layer of a plugin (vst3.c, clap.c,
// au_darwin.m) instead of the Go side, it makes a plugin that the host
// never sees change: the Go side is what is rebuilt. macOS only; for
// developing Sointu, not for release.
//
// A Go library cannot be unloaded, so an engine that was replaced stays
// loaded, idle. All instances of a loader move to a new engine together:
// a Go runtime that another one loaded after it loses async preemption (Go
// forwards no SIGURG to the handler before its own), so an old runtime is
// to be left with nothing to run. See HOTRELOAD-PLAN.md.
//
// The engine folder is ~/Library/Application Support/sointu/engine. Its file
// current names the newest engine, sointu-engine-<build>.dylib next to it;
// make install-engine writes both. The loader loads the engine that current
// names when the first instance is made. Then it watches current, tells the
// instances when it names another build (sointuReloadAvailable), and moves
// them to it when one of them asks (request_reload): the new instance gets
// the state of the old one, its sample rate, window and title, and the old
// one is closed once no call is in it.
//
// Threads: the host makes and closes instances (sointuNew, sointuClose) on
// its main thread, and reloads happen there. Other calls come from any
// thread, the audio thread among them: they take no lock, only a count of
// the calls in the engine instance, which a reload waits for before it
// closes the old one.

#include <dispatch/dispatch.h>
#include <dlfcn.h>
#include <limits.h>
#include <pthread.h>
#include <pwd.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "engine.h"

#ifndef SOINTU_LOADER_VERSION
#define SOINTU_LOADER_VERSION "dev"
#endif

// what the loader tells the host before an engine is loaded, and what an
// engine must have to be loaded: the host does not expect them to change
#define LOADER_MIDI_PORTS 4
#define LOADER_PARAMS 64
#define LOADER_PARAM_RANGE 128.0

#define ENGINE_DIR "Library/Application Support/sointu/engine"
#define WATCH_INTERVAL_NS (1000 * NSEC_PER_MSEC)

// engine is a loaded engine library, never unloaded
typedef struct engine {
	struct engine *next;
	char name[NAME_MAX + 1]; // the file name in the engine folder
	int32_t (*EngineABI)(void);
	char *(*Version)(void);
	int32_t (*MIDIPorts)(void);
	int32_t (*Params)(void);
	double (*ParamRange)(void);
	uintptr_t (*New)(uintptr_t, char *, sointu_callbacks *);
	void (*Close)(uintptr_t);
	void (*ManageWindow)(uintptr_t);
	void (*ShowWindow)(uintptr_t);
	void (*HideWindow)(uintptr_t);
	void (*WindowTitle)(uintptr_t, char *);
	bool (*WindowShown)(uintptr_t);
	void (*MIDI)(uintptr_t, int32_t, int32_t, uint8_t, uint8_t, uint8_t);
	void (*Param)(uintptr_t, int32_t, int32_t, double, bool);
	double (*ParamValue)(uintptr_t, int32_t);
	char *(*ParamText)(uintptr_t, int32_t, double);
	double (*ParamFromSteps)(uintptr_t, int32_t, double);
	char *(*ParamName)(uintptr_t, int32_t);
	uint32_t (*SetSampleRate)(uintptr_t, double);
	void (*Process)(uintptr_t, float *, float *, int32_t, double, bool, double);
	void *(*State)(uintptr_t, size_t *);
	void (*SetState)(uintptr_t, void *, size_t);
	void (*ReloadAvailable)(uintptr_t, char *);
	void (*Alert)(uintptr_t, char *);
} engine_t;

// target is an instance in an engine, and how many calls are in it
typedef struct target {
	engine_t *engine;
	uintptr_t handle;
	atomic_int calls;
} target_t;

// instance is what the C layer of the plugin has as the handle
typedef struct instance {
	struct instance *next;
	uintptr_t plugin;
	char kind[64];
	sointu_callbacks cb;     // those of the C layer
	sointu_callbacks to_go;  // given to the engine: cb and request_reload
	_Atomic(target_t *) cur; // NULL without an engine
	// what a new engine instance is to be told again, on the main thread
	// alone, but for the rate
	_Atomic double rate; // 0 until the host tells it
	bool managed;
	char title[256];
} instance_t;

// lock guards engines, newest and instances; the loader's main thread holds
// it through a reload
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static engine_t *engines; // all that were loaded
static engine_t *newest;  // that the instances are in
static instance_t *instances;
static char announced[NAME_MAX + 1]; // the build last told to the instances
static dispatch_source_t watcher;
static atomic_bool reload_queued;

static void say(const char *format, ...) {
	va_list args;
	va_start(args, format);
	fprintf(stderr, "sointu loader: ");
	vfprintf(stderr, format, args);
	fprintf(stderr, "\n");
	va_end(args);
}

static const char *home(void) {
	const char *h = getenv("HOME");
	if (h != NULL && h[0] != 0)
		return h;
	struct passwd *pw = getpwuid(getuid());
	return pw != NULL ? pw->pw_dir : "/tmp";
}

// current_name reads the name of the newest engine from the file current
static bool current_name(char *name, size_t size) {
	char path[PATH_MAX];
	snprintf(path, sizeof(path), "%s/" ENGINE_DIR "/current", home());
	FILE *f = fopen(path, "r");
	if (f == NULL)
		return false;
	size_t n = fread(name, 1, size - 1, f);
	fclose(f);
	while (n > 0 && (name[n - 1] == '\n' || name[n - 1] == '\r' || name[n - 1] == ' '))
		n--;
	name[n] = 0;
	// a file name in the folder, nothing else
	return n > 0 && strchr(name, '/') == NULL && strcmp(name, ".") != 0 && strcmp(name, "..") != 0;
}

// load returns the engine of that name, loaded now or before, or NULL with
// why in err. Call with lock held.
static engine_t *load(const char *name, char *err, size_t err_size) {
	for (engine_t *e = engines; e != NULL; e = e->next)
		if (strcmp(e->name, name) == 0)
			return e;
	char path[PATH_MAX];
	snprintf(path, sizeof(path), "%s/" ENGINE_DIR "/%s", home(), name);
	void *lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (lib == NULL) {
		snprintf(err, err_size, "could not load %s: %s", path, dlerror());
		return NULL;
	}
	engine_t *e = calloc(1, sizeof(*e));
	if (e == NULL) {
		snprintf(err, err_size, "out of memory");
		return NULL;
	}
	snprintf(e->name, sizeof(e->name), "%s", name);
	bool ok = true;
#define SYM(field, symbol)                                         \
	if ((*(void **)&e->field = dlsym(lib, #symbol)) == NULL) {  \
		snprintf(err, err_size, "%s has no %s", name, #symbol); \
		ok = false;                                              \
	}
	SYM(EngineABI, sointuEngineABI)
	if (ok && e->EngineABI() != SOINTU_ENGINE_ABI) {
		snprintf(err, err_size, "%s has the engine interface %d, the loader %d: make install-dev-loader and restart the host", name, e->EngineABI(), SOINTU_ENGINE_ABI);
		ok = false;
	}
	if (ok) {
		SYM(Version, sointuVersion)
		SYM(MIDIPorts, sointuMIDIPorts)
		SYM(Params, sointuParams)
		SYM(ParamRange, sointuParamRange)
		SYM(New, sointuNew)
		SYM(Close, sointuClose)
		SYM(ManageWindow, sointuManageWindow)
		SYM(ShowWindow, sointuShowWindow)
		SYM(HideWindow, sointuHideWindow)
		SYM(WindowTitle, sointuWindowTitle)
		SYM(WindowShown, sointuWindowShown)
		SYM(MIDI, sointuMIDI)
		SYM(Param, sointuParam)
		SYM(ParamValue, sointuParamValue)
		SYM(ParamText, sointuParamText)
		SYM(ParamFromSteps, sointuParamFromSteps)
		SYM(ParamName, sointuParamName)
		SYM(SetSampleRate, sointuSetSampleRate)
		SYM(Process, sointuProcess)
		SYM(State, sointuState)
		SYM(SetState, sointuSetState)
		SYM(ReloadAvailable, sointuReloadAvailable)
		SYM(Alert, sointuAlert)
	}
#undef SYM
	if (ok && (e->MIDIPorts() != LOADER_MIDI_PORTS || e->Params() != LOADER_PARAMS || e->ParamRange() != LOADER_PARAM_RANGE)) {
		snprintf(err, err_size, "%s has %d MIDI ports and %d parameters of range %g, the loader %d, %d and %g: make install-dev-loader and restart the host",
			name, e->MIDIPorts(), e->Params(), e->ParamRange(), LOADER_MIDI_PORTS, LOADER_PARAMS, LOADER_PARAM_RANGE);
		ok = false;
	}
	if (!ok) {
		// a Go library cannot be closed: it stays, unused
		free(e);
		return NULL;
	}
	e->next = engines;
	engines = e;
	say("loaded %s", path);
	return e;
}

// enter returns the target of the instance with a call counted in it, or
// NULL; leave ends the call. Lock-free, for the audio thread.
static target_t *enter(instance_t *i) {
	for (;;) {
		target_t *t = atomic_load(&i->cur);
		if (t == NULL)
			return NULL;
		atomic_fetch_add(&t->calls, 1);
		if (atomic_load(&i->cur) == t)
			return t;
		atomic_fetch_sub(&t->calls, 1); // replaced meanwhile
	}
}

static void leave(target_t *t) {
	if (t != NULL)
		atomic_fetch_sub(&t->calls, 1);
}

// retire closes a target that no instance has any more, once no call is in
// it. On the main thread.
static void retire(void *ctx) {
	target_t *t = ctx;
	if (atomic_load(&t->calls) > 0) {
		dispatch_after_f(dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_MSEC), dispatch_get_main_queue(), t, retire);
		return;
	}
	t->engine->Close(t->handle);
	free(t);
}

static void alert(instance_t *i, const char *message) {
	say("%s", message);
	target_t *t = enter(i);
	if (t != NULL)
		t->engine->Alert(t->handle, (char *)message);
	leave(t);
}

// new_target makes an instance in the engine
static target_t *new_target(instance_t *i, engine_t *e) {
	target_t *t = calloc(1, sizeof(*t));
	if (t == NULL)
		return NULL;
	t->engine = e;
	t->handle = e->New(i->plugin, i->kind, &i->to_go);
	return t;
}

// move moves an instance to the engine e: a new instance there with what the
// old one has, then the old one closed. On the main thread, with lock held.
static void move(instance_t *i, engine_t *e) {
	target_t *old = atomic_load(&i->cur);
	if (old != NULL && old->engine == e)
		return;
	size_t size = 0;
	void *state = NULL;
	bool shown = false;
	if (old != NULL) {
		state = old->engine->State(old->handle, &size);
		if (state == NULL) {
			alert(i, "reload: could not get the song from the running instance; it stays as it is");
			return;
		}
		shown = old->engine->WindowShown(old->handle);
	}
	target_t *t = new_target(i, e);
	if (t == NULL) {
		free(state);
		return;
	}
	double rate = atomic_load(&i->rate);
	if (rate > 0)
		e->SetSampleRate(t->handle, rate);
	if (state != NULL) {
		e->SetState(t->handle, state, size);
		free(state);
	}
	if (i->title[0] != 0)
		e->WindowTitle(t->handle, i->title);
	if (i->managed)
		e->ManageWindow(t->handle);
	atomic_store(&i->cur, t);
	if (old != NULL)
		retire(old);
	if (shown)
		e->ShowWindow(t->handle);
	// the host reads the names and values of the parameters again
	if (i->cb.params_changed != NULL) {
		i->cb.params_changed(i->plugin, false);
		i->cb.params_changed(i->plugin, true);
	}
}

// reload moves every instance to the engine that current names. On the
// main thread.
static void reload(void *ctx) {
	atomic_store(&reload_queued, false);
	char name[NAME_MAX + 1], err[PATH_MAX + 256] = "";
	pthread_mutex_lock(&lock);
	engine_t *e = NULL;
	if (!current_name(name, sizeof(name)))
		snprintf(err, sizeof(err), "reload: no engine installed (make install-engine)");
	else
		e = load(name, err, sizeof(err));
	if (e == NULL) {
		for (instance_t *i = instances; i != NULL; i = i->next)
			alert(i, err);
		pthread_mutex_unlock(&lock);
		return;
	}
	newest = e;
	snprintf(announced, sizeof(announced), "%s", name);
	for (instance_t *i = instances; i != NULL; i = i->next)
		move(i, e);
	pthread_mutex_unlock(&lock);
}

// request_reload is what an instance asks a reload with, from any thread
static void request_reload(uintptr_t plugin) {
	if (!atomic_exchange(&reload_queued, true))
		dispatch_async_f(dispatch_get_main_queue(), NULL, reload);
}

// build writes what a file name of an engine tells the user: the version and
// time of the build
static char *build(char *out, size_t size, const char *name) {
	const char *prefix = "sointu-engine-", *suffix = ".dylib";
	if (strncmp(name, prefix, strlen(prefix)) == 0)
		name += strlen(prefix);
	size_t n = strlen(name);
	if (n > strlen(suffix) && strcmp(name + n - strlen(suffix), suffix) == 0)
		n -= strlen(suffix);
	snprintf(out, size, "%.*s", (int)n, name);
	return out;
}

// watch tells the instances when current names another engine. On the main
// thread, every second.
static void watch(void *ctx) {
	char name[NAME_MAX + 1], b[NAME_MAX + 1];
	if (!current_name(name, sizeof(name)))
		return;
	pthread_mutex_lock(&lock);
	if (newest != NULL && strcmp(name, newest->name) != 0 && strcmp(name, announced) != 0) {
		snprintf(announced, sizeof(announced), "%s", name);
		for (instance_t *i = instances; i != NULL; i = i->next) {
			target_t *t = enter(i);
			if (t != NULL)
				t->engine->ReloadAvailable(t->handle, build(b, sizeof(b), name));
			leave(t);
		}
	}
	pthread_mutex_unlock(&lock);
}

// the interface of engine.h

int32_t sointuEngineABI(void) { return SOINTU_ENGINE_ABI; }

char *sointuVersion(void) { return SOINTU_LOADER_VERSION; }

int32_t sointuMIDIPorts(void) { return LOADER_MIDI_PORTS; }

int32_t sointuParams(void) { return LOADER_PARAMS; }

double sointuParamRange(void) { return LOADER_PARAM_RANGE; }

uintptr_t sointuNew(uintptr_t plugin, char *kind, sointu_callbacks *cb) {
	instance_t *i = calloc(1, sizeof(*i));
	if (i == NULL)
		return 0;
	i->plugin = plugin;
	snprintf(i->kind, sizeof(i->kind), "%s", kind);
	i->cb = *cb;
	i->to_go = *cb;
	i->to_go.request_reload = request_reload;
	char name[NAME_MAX + 1], err[PATH_MAX + 256] = "";
	pthread_mutex_lock(&lock);
	if (newest == NULL) {
		if (current_name(name, sizeof(name))) {
			newest = load(name, err, sizeof(err));
			snprintf(announced, sizeof(announced), "%s", name);
		} else {
			snprintf(err, sizeof(err), "no engine installed: make install-engine");
		}
		if (newest == NULL)
			say("%s; the plugin stays silent", err);
	}
	if (newest != NULL)
		atomic_store(&i->cur, new_target(i, newest));
	i->next = instances;
	instances = i;
	if (watcher == NULL) {
		watcher = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, dispatch_get_main_queue());
		dispatch_source_set_timer(watcher, dispatch_time(DISPATCH_TIME_NOW, WATCH_INTERVAL_NS), WATCH_INTERVAL_NS, 100 * NSEC_PER_MSEC);
		dispatch_source_set_event_handler_f(watcher, watch);
		dispatch_resume(watcher);
	}
	// an engine newer than the one running was installed before this instance
	if (newest != NULL && announced[0] != 0 && strcmp(announced, newest->name) != 0) {
		target_t *t = atomic_load(&i->cur);
		char b[NAME_MAX + 1];
		if (t != NULL)
			t->engine->ReloadAvailable(t->handle, build(b, sizeof(b), announced));
	}
	pthread_mutex_unlock(&lock);
	return (uintptr_t)i;
}

void sointuClose(uintptr_t h) {
	instance_t *i = (instance_t *)h;
	pthread_mutex_lock(&lock);
	for (instance_t **p = &instances; *p != NULL; p = &(*p)->next) {
		if (*p == i) {
			*p = i->next;
			break;
		}
	}
	pthread_mutex_unlock(&lock);
	target_t *t = atomic_exchange(&i->cur, NULL);
	if (t != NULL) {
		while (atomic_load(&t->calls) > 0)
			usleep(100);
		t->engine->Close(t->handle);
		free(t);
	}
	free(i);
}

// FORWARD calls the engine instance with the arguments, if there is one
#define FORWARD(h, call)                         \
	do {                                         \
		target_t *t_ = enter((instance_t *)(h)); \
		if (t_ != NULL)                          \
			t_->engine->call;                    \
		leave(t_);                               \
	} while (0)

void sointuManageWindow(uintptr_t h) {
	((instance_t *)h)->managed = true;
	FORWARD(h, ManageWindow(t_->handle));
}

void sointuShowWindow(uintptr_t h) { FORWARD(h, ShowWindow(t_->handle)); }

void sointuHideWindow(uintptr_t h) { FORWARD(h, HideWindow(t_->handle)); }

void sointuWindowTitle(uintptr_t h, char *title) {
	instance_t *i = (instance_t *)h;
	snprintf(i->title, sizeof(i->title), "%s", title);
	FORWARD(h, WindowTitle(t_->handle, title));
}

bool sointuWindowShown(uintptr_t h) {
	target_t *t = enter((instance_t *)h);
	bool shown = t != NULL && t->engine->WindowShown(t->handle);
	leave(t);
	return shown;
}

void sointuMIDI(uintptr_t h, int32_t time, int32_t port, uint8_t d0, uint8_t d1, uint8_t d2) {
	FORWARD(h, MIDI(t_->handle, time, port, d0, d1, d2));
}

void sointuParam(uintptr_t h, int32_t time, int32_t id, double value, bool mod) {
	FORWARD(h, Param(t_->handle, time, id, value, mod));
}

double sointuParamValue(uintptr_t h, int32_t id) {
	target_t *t = enter((instance_t *)h);
	double v = t != NULL ? t->engine->ParamValue(t->handle, id) : 0;
	leave(t);
	return v;
}

char *sointuParamText(uintptr_t h, int32_t id, double value) {
	target_t *t = enter((instance_t *)h);
	char *text = NULL;
	if (t != NULL)
		text = t->engine->ParamText(t->handle, id, value);
	leave(t);
	if (text == NULL && (text = malloc(32)) != NULL)
		snprintf(text, 32, "%+.2f", value);
	return text;
}

double sointuParamFromSteps(uintptr_t h, int32_t id, double steps) {
	target_t *t = enter((instance_t *)h);
	double v = t != NULL ? t->engine->ParamFromSteps(t->handle, id, steps) : steps;
	leave(t);
	return v;
}

char *sointuParamName(uintptr_t h, int32_t id) {
	target_t *t = enter((instance_t *)h);
	char *name = NULL;
	if (t != NULL)
		name = t->engine->ParamName(t->handle, id);
	leave(t);
	if (name == NULL && (name = malloc(16)) != NULL)
		snprintf(name, 16, "A%d", id + 1);
	return name;
}

uint32_t sointuSetSampleRate(uintptr_t h, double rate) {
	instance_t *i = (instance_t *)h;
	atomic_store(&i->rate, rate);
	target_t *t = enter(i);
	uint32_t latency = t != NULL ? t->engine->SetSampleRate(t->handle, rate) : 0;
	leave(t);
	return latency;
}

void sointuProcess(uintptr_t h, float *left, float *right, int32_t frames, double rate, bool has_tempo, double tempo) {
	target_t *t = enter((instance_t *)h);
	if (t != NULL) {
		t->engine->Process(t->handle, left, right, frames, rate, has_tempo, tempo);
	} else if (frames > 0) {
		memset(left, 0, frames * sizeof(float));
		memset(right, 0, frames * sizeof(float));
	}
	leave(t);
}

void *sointuState(uintptr_t h, size_t *size) {
	*size = 0;
	target_t *t = enter((instance_t *)h);
	void *state = t != NULL ? t->engine->State(t->handle, size) : NULL;
	leave(t);
	return state;
}

void sointuSetState(uintptr_t h, void *data, size_t size) { FORWARD(h, SetState(t_->handle, data, size)); }

void sointuReloadAvailable(uintptr_t h, char *name) { FORWARD(h, ReloadAvailable(t_->handle, name)); }

void sointuAlert(uintptr_t h, char *message) { FORWARD(h, Alert(t_->handle, message)); }
