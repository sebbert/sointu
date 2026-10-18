// A test host for the dev loader, on macOS: loads the VST3 plugin built with
// the loader, plays, and while an audio thread goes on processing, waits
// for the loader to move the instance to another engine (the test installs
// one, with auto reload on). Then the song, a parameter and the sound must
// be there. Built and run by loader_darwin_test.go; prints OK when
// everything passed. argv: the library of the plugin, the engine folder,
// the name of the second engine.
#include <dlfcn.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <CoreFoundation/CoreFoundation.h>
#include "vst3_c_api.h"
typedef Steinberg_tresult tresult;
#define CHECK(c) do { if (!(c)) { printf("FAIL %s:%d %s\n", __FILE__, __LINE__, #c); fflush(stdout); exit(1);} } while (0)

static atomic_int restarts;
static tresult hq(void *t, const Steinberg_TUID iid, void **o);
static tresult hbegin(void *t, uint32_t id) { return 0; }
static tresult hperf(void *t, uint32_t id, double v) { return 0; }
static tresult hrestart(void *t, int32_t f) { atomic_fetch_or(&restarts, f); return 0; }
static uint32_t hone(void *t) { return 1; }
static Steinberg_Vst_IComponentHandlerVtbl hvt = {hq, hone, hone, hbegin, hperf, hbegin, hrestart};
static Steinberg_Vst_IComponentHandler handler = {&hvt};
static tresult hq(void *t, const Steinberg_TUID iid, void **o) {
	if (memcmp(iid, Steinberg_Vst_IComponentHandler_iid, 16) == 0) { *o = &handler; return 0; }
	*o = NULL; return Steinberg_kNoInterface; }
static void run(double s) { CFRunLoopRunInMode(kCFRunLoopDefaultMode, s, false); }
static const Steinberg_TUID cid = SMTG_INLINE_UID(0xD55F03CC, 0x0FB045F4, 0xBF6ED4BF, 0x39A19A91);

typedef struct { Steinberg_IBStream s; char *buf; int64_t size, cap, pos; } mem_t;
static tresult q(void *t, const Steinberg_TUID iid, void **o) { *o = NULL; return Steinberg_kNoInterface; }
static uint32_t one(void *t) { return 1; }
static tresult mread(void *t, void *b, int32_t n, int32_t *got) { mem_t *m = t; int64_t k = m->size - m->pos; if (k > n) k = n; memcpy(b, m->buf + m->pos, k); m->pos += k; if (got) *got = (int32_t)k; return 0; }
static tresult mwrite(void *t, void *b, int32_t n, int32_t *put) { mem_t *m = t; if (m->pos + n > m->cap) { m->cap = (m->pos + n) * 2; m->buf = realloc(m->buf, m->cap); } memcpy(m->buf + m->pos, b, n); m->pos += n; if (m->pos > m->size) m->size = m->pos; if (put) *put = n; return 0; }
static tresult mseek(void *t, int64_t p, int32_t mode, int64_t *r) { mem_t *m = t; m->pos = mode == 0 ? p : mode == 1 ? m->pos + p : m->size + p; if (r) *r = m->pos; return 0; }
static tresult mtell(void *t, int64_t *p) { *p = ((mem_t *)t)->pos; return 0; }
static Steinberg_IBStreamVtbl mvt = {q, one, one, mread, mwrite, mseek, mtell};

typedef struct { Steinberg_Vst_IEventList l; struct Steinberg_Vst_Event ev[8]; int n; } events_t;
static int32_t ecount(void *t) { return ((events_t *)t)->n; }
static tresult eget(void *t, int32_t i, struct Steinberg_Vst_Event *e) { *e = ((events_t *)t)->ev[i]; return 0; }
static tresult eadd(void *t, struct Steinberg_Vst_Event *e) { return 1; }
static Steinberg_Vst_IEventListVtbl evt = {q, one, one, ecount, eget, eadd};

typedef struct { Steinberg_Vst_IParamValueQueue q; uint32_t id; int32_t off; double v; } queue_t;
static uint32_t qid(void *t) { return ((queue_t *)t)->id; }
static int32_t qcount(void *t) { return 1; }
static tresult qget(void *t, int32_t i, int32_t *off, double *v) { queue_t *x = t; *off = x->off; *v = x->v; return 0; }
static tresult qadd(void *t, int32_t o, double v, int32_t *i) { return 1; }
static Steinberg_Vst_IParamValueQueueVtbl qvt = {q, one, one, qid, qcount, qget, qadd};
typedef struct { Steinberg_Vst_IParameterChanges c; queue_t *queue; } changes_t;
static int32_t ccount(void *t) { return ((changes_t *)t)->queue ? 1 : 0; }
static struct Steinberg_Vst_IParamValueQueue *cget(void *t, int32_t i) { return &((changes_t *)t)->queue->q; }
static struct Steinberg_Vst_IParamValueQueue *cadd(void *t, const uint32_t *id, int32_t *i) { return NULL; }
static Steinberg_Vst_IParameterChangesVtbl cvt = {q, one, one, ccount, cget, cadd};

static Steinberg_Vst_IAudioProcessor *proc;

static double block(events_t *ev, changes_t *ch) {
	static float L[512], R[512];
	float *bufs[2] = {L, R};
	struct Steinberg_Vst_AudioBusBuffers out = {.numChannels = 2};
	out.Steinberg_Vst_AudioBusBuffers_channelBuffers32 = bufs;
	struct Steinberg_Vst_ProcessContext ctx = {.state = Steinberg_Vst_ProcessContext_StatesAndFlags_kTempoValid, .sampleRate = 48000, .tempo = 120};
	struct Steinberg_Vst_ProcessData d = {.numSamples = 512, .numOutputs = 1, .outputs = &out,
		.inputEvents = ev ? &ev->l : NULL, .inputParameterChanges = ch ? &ch->c : NULL, .processContext = &ctx};
	CHECK(proc->lpVtbl->process(proc, &d) == 0);
	double s = 0;
	for (int i = 0; i < 512; i++) s += L[i] * L[i] + R[i] * R[i];
	return sqrt(s / 1024);
}

// note plays a note for a while and returns its rms
static double note(void) {
	events_t ev = {.l.lpVtbl = &evt, .n = 1};
	ev.ev[0].type = Steinberg_Vst_Event_EventTypes_kNoteOnEvent;
	ev.ev[0].sampleOffset = 100;
	ev.ev[0].Steinberg_Vst_Event_noteOn = (struct Steinberg_Vst_NoteOnEvent){.channel = 0, .pitch = 60, .velocity = 0.8f, .noteId = -1};
	double on = block(&ev, NULL);
	for (int i = 0; i < 20; i++) on += block(NULL, NULL);
	ev.ev[0].type = Steinberg_Vst_Event_EventTypes_kNoteOffEvent;
	ev.ev[0].Steinberg_Vst_Event_noteOff = (struct Steinberg_Vst_NoteOffEvent){.channel = 0, .pitch = 60, .noteId = -1};
	block(&ev, NULL);
	for (int i = 0; i < 40; i++) block(NULL, NULL);
	return on / 21;
}

static atomic_bool playing;
static atomic_long blocks;

// audio processes blocks, as the audio thread of a host, while the reload
// happens on the main thread
static void *audio(void *arg) {
	while (atomic_load(&playing)) {
		block(NULL, NULL);
		atomic_fetch_add(&blocks, 1);
		usleep(2000);
	}
	return NULL;
}

int main(int argc, char **argv) {
	setvbuf(stdout, NULL, _IONBF, 0);
	CHECK(argc == 4);
	void *lib = dlopen(argv[1], RTLD_NOW);
	CHECK(lib);
	bool (*entry)(void *) = dlsym(lib, "bundleEntry");
	CHECK(entry && entry(NULL));
	Steinberg_IPluginFactory *(*get)(void) = dlsym(lib, "GetPluginFactory");
	Steinberg_IPluginFactory *f = get();

	Steinberg_Vst_IComponent *comp;
	CHECK(f->lpVtbl->createInstance(f, (const char *)cid, (const char *)Steinberg_Vst_IComponent_iid, (void **)&comp) == 0);
	CHECK(comp->lpVtbl->initialize(comp, NULL) == 0);
	Steinberg_Vst_IEditController *ctl;
	CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IEditController_iid, (void **)&ctl) == 0);
	CHECK(ctl->lpVtbl->setComponentHandler(ctl, &handler) == 0);
	CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IAudioProcessor_iid, (void **)&proc) == 0);
	printf("params %d\n", ctl->lpVtbl->getParameterCount(ctl));
	struct Steinberg_Vst_ProcessSetup setup = {0, 0, 512, 48000};
	CHECK(proc->lpVtbl->setupProcessing(proc, &setup) == 0);
	CHECK(comp->lpVtbl->setActive(comp, 1) == 0);
	CHECK(proc->lpVtbl->setProcessing(proc, 1) == 0);
	run(2); // the tracker starts

	double before = note();
	printf("rms of a note before the reload %.5f\n", before);
	CHECK(before > 0.001);
	queue_t qu = {.q.lpVtbl = &qvt, .id = 2, .off = 10, .v = 0.75};
	changes_t ch = {.c.lpVtbl = &cvt, .queue = &qu};
	block(NULL, &ch);
	run(0.5);
	mem_t m = {.s.lpVtbl = &mvt};
	CHECK(comp->lpVtbl->getState(comp, &m.s) == 0);
	printf("state before %lld bytes\n", (long long)m.size);

	// install the second engine while the audio thread plays
	atomic_store(&playing, true);
	pthread_t th;
	pthread_create(&th, NULL, audio, NULL);
	char path[4096], tmp[4096];
	snprintf(path, sizeof(path), "%s/current", argv[2]);
	snprintf(tmp, sizeof(tmp), "%s/current.tmp", argv[2]);
	FILE *cur = fopen(tmp, "w");
	CHECK(cur);
	fprintf(cur, "%s\n", argv[3]);
	fclose(cur);
	CHECK(rename(tmp, path) == 0);
	atomic_store(&restarts, 0);
	for (int i = 0; i < 100 && !(atomic_load(&restarts) & Steinberg_Vst_RestartFlags_kParamValuesChanged); i++)
		run(0.1);
	run(1);
	atomic_store(&playing, false);
	pthread_join(th, NULL);
	printf("blocks during the reload %ld, restart flags %#x\n", (long)atomic_load(&blocks), atomic_load(&restarts));
	CHECK(atomic_load(&restarts) & Steinberg_Vst_RestartFlags_kParamValuesChanged);
	CHECK(atomic_load(&blocks) > 100);

	double v = ctl->lpVtbl->getParamNormalized(ctl, 2);
	printf("param 2 after the reload %.3f\n", v);
	CHECK(fabs(v - 0.75) < 1e-6);
	double after = note();
	printf("rms of a note after the reload %.5f\n", after);
	CHECK(fabs(after - before) < 0.1 * before);
	mem_t m2 = {.s.lpVtbl = &mvt};
	CHECK(comp->lpVtbl->getState(comp, &m2.s) == 0);
	printf("state after %lld bytes\n", (long long)m2.size);
	CHECK(m2.size > 100);

	CHECK(proc->lpVtbl->setProcessing(proc, 0) == 0);
	CHECK(comp->lpVtbl->setActive(comp, 0) == 0);
	CHECK(comp->lpVtbl->terminate(comp) == 0);
	proc->lpVtbl->release(proc);
	ctl->lpVtbl->release(ctl);
	run(0.5);
	CHECK(comp->lpVtbl->release(comp) == 0);
	printf("OK\n");
	return 0;
}
