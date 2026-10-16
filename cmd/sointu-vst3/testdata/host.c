// A test host of a few calls for the Sointu VST3, on macOS: a note makes
// sound, a parameter, the MIDI mapping, the state there and back, what the
// host is told on the main thread, the references. Built and run by
// vst3_darwin_test.go; prints OK when everything passed.
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <CoreFoundation/CoreFoundation.h>
#include "vst3_c_api.h"
typedef Steinberg_tresult tresult;
static int restarts, dirty;
static void *handler2_ptr;
static tresult hq(void *t, const Steinberg_TUID iid, void **o);
static tresult hbegin(void *t, uint32_t id) { return 0; }
static tresult hperf(void *t, uint32_t id, double v) { return 0; }
static tresult hrestart(void *t, int32_t f) { restarts |= f; return 0; }
static uint32_t hone(void *t) { return 1; }
static Steinberg_Vst_IComponentHandlerVtbl hvt = {hq, hone, hone, hbegin, hperf, hbegin, hrestart};
static Steinberg_Vst_IComponentHandler handler = {&hvt};
static tresult h2dirty(void *t, Steinberg_TBool s) { dirty++; return 0; }
static tresult h2open(void *t, const char *n) { return 0; }
static tresult h2g(void *t) { return 0; }
static Steinberg_Vst_IComponentHandler2Vtbl h2vt = {hq, hone, hone, h2dirty, h2open, h2g, h2g};
static Steinberg_Vst_IComponentHandler2 handler2 = {&h2vt};
static tresult hq(void *t, const Steinberg_TUID iid, void **o) {
	if (memcmp(iid, Steinberg_Vst_IComponentHandler2_iid, 16) == 0) { *o = &handler2; return 0; }
	if (memcmp(iid, Steinberg_Vst_IComponentHandler_iid, 16) == 0) { *o = &handler; return 0; }
	*o = NULL; return Steinberg_kNoInterface; }
static void run(double s) { CFRunLoopRunInMode(kCFRunLoopDefaultMode, s, false); }

typedef Steinberg_tresult tresult;
static const Steinberg_TUID cid = SMTG_INLINE_UID(0xD55F03CC, 0x0FB045F4, 0xBF6ED4BF, 0x39A19A91);
#define CHECK(c) do { if (!(c)) { printf("FAIL %s:%d %s\n", __FILE__, __LINE__, #c); exit(1);} } while (0)

// memory stream
typedef struct { Steinberg_IBStream s; char *buf; int64_t size, cap, pos; } mem_t;
static tresult q(void *t, const Steinberg_TUID iid, void **o) { *o = NULL; return Steinberg_kNoInterface; }
static uint32_t one(void *t) { return 1; }
static tresult mread(void *t, void *b, int32_t n, int32_t *got) { mem_t *m = t; int64_t k = m->size - m->pos; if (k > n) k = n; memcpy(b, m->buf + m->pos, k); m->pos += k; if (got) *got = (int32_t)k; return 0; }
static tresult mwrite(void *t, void *b, int32_t n, int32_t *put) { mem_t *m = t; if (m->pos + n > m->cap) { m->cap = (m->pos + n) * 2; m->buf = realloc(m->buf, m->cap); } memcpy(m->buf + m->pos, b, n); m->pos += n; if (m->pos > m->size) m->size = m->pos; if (put) *put = n; return 0; }
static tresult mseek(void *t, int64_t p, int32_t mode, int64_t *r) { mem_t *m = t; m->pos = mode == 0 ? p : mode == 1 ? m->pos + p : m->size + p; if (r) *r = m->pos; return 0; }
static tresult mtell(void *t, int64_t *p) { *p = ((mem_t *)t)->pos; return 0; }
static Steinberg_IBStreamVtbl mvt = {q, one, one, mread, mwrite, mseek, mtell};

// event list
typedef struct { Steinberg_Vst_IEventList l; struct Steinberg_Vst_Event ev[8]; int n; } events_t;
static int32_t ecount(void *t) { return ((events_t *)t)->n; }
static tresult eget(void *t, int32_t i, struct Steinberg_Vst_Event *e) { *e = ((events_t *)t)->ev[i]; return 0; }
static tresult eadd(void *t, struct Steinberg_Vst_Event *e) { return 1; }
static Steinberg_Vst_IEventListVtbl evt = {q, one, one, ecount, eget, eadd};

// parameter changes: one queue of one point
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

static float L[512], R[512];

static double block(events_t *ev, changes_t *ch) {
	float *bufs[2] = {L, R};
	struct Steinberg_Vst_AudioBusBuffers out = {.numChannels = 2};
	out.Steinberg_Vst_AudioBusBuffers_channelBuffers32 = bufs;
	struct Steinberg_Vst_ProcessContext ctx = {.state = Steinberg_Vst_ProcessContext_StatesAndFlags_kTempoValid, .sampleRate = 48000, .tempo = 120};
	struct Steinberg_Vst_ProcessData d = {.processMode = 0, .symbolicSampleSize = 0, .numSamples = 512, .numOutputs = 1, .outputs = &out,
		.inputEvents = ev ? &ev->l : NULL, .inputParameterChanges = ch ? &ch->c : NULL, .processContext = &ctx};
	CHECK(proc->lpVtbl->process(proc, &d) == 0);
	double s = 0;
	for (int i = 0; i < 512; i++) s += L[i] * L[i] + R[i] * R[i];
	return sqrt(s / 1024);
}

int main(int argc, char **argv) {
	void *lib = dlopen(argv[1], RTLD_NOW);
	CHECK(lib);
	bool (*entry)(void *) = dlsym(lib, "bundleEntry");
	CHECK(entry && entry(NULL));
	Steinberg_IPluginFactory *(*get)(void) = dlsym(lib, "GetPluginFactory");
	Steinberg_IPluginFactory *f = get();
	struct Steinberg_PClassInfo2 ci;
	Steinberg_IPluginFactory2 *f2;
	CHECK(f->lpVtbl->queryInterface(f, Steinberg_IPluginFactory2_iid, (void **)&f2) == 0);
	CHECK(f2->lpVtbl->getClassInfo2(f2, 0, &ci) == 0);
	printf("class %s / %s / %s / version %s\n", ci.name, ci.category, ci.subCategories, ci.version);

	Steinberg_Vst_IComponent *comp;
	CHECK(f->lpVtbl->createInstance(f, (const char *)cid, (const char *)Steinberg_Vst_IComponent_iid, (void **)&comp) == 0);
	CHECK(comp->lpVtbl->initialize(comp, NULL) == 0);
	Steinberg_Vst_IEditController *ctl;
	CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IEditController_iid, (void **)&ctl) == 0);
	CHECK(ctl->lpVtbl->initialize(ctl, NULL) == 0); // as some hosts do: the same object
	CHECK(ctl->lpVtbl->setComponentHandler(ctl, &handler) == 0);
	CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IAudioProcessor_iid, (void **)&proc) == 0);
	Steinberg_Vst_IMidiMapping *mm;
	CHECK(comp->lpVtbl->queryInterface(comp, Steinberg_Vst_IMidiMapping_iid, (void **)&mm) == 0);
	printf("event buses %d, audio out %d, params %d\n", comp->lpVtbl->getBusCount(comp, 1, 0), comp->lpVtbl->getBusCount(comp, 0, 1), ctl->lpVtbl->getParameterCount(ctl));

	struct Steinberg_Vst_ProcessSetup setup = {0, 0, 512, 48000};
	CHECK(proc->lpVtbl->setupProcessing(proc, &setup) == 0);
	printf("latency at 48000 Hz: %u\n", proc->lpVtbl->getLatencySamples(proc));
	CHECK(comp->lpVtbl->setActive(comp, 1) == 0);
	CHECK(proc->lpVtbl->setProcessing(proc, 1) == 0);
	run(2); // the tracker starts
	printf("restart flags after setup: %#x\n", restarts);
	CHECK(restarts & Steinberg_Vst_RestartFlags_kLatencyChanged);

	double silent = 0;
	for (int i = 0; i < 10; i++) silent += block(NULL, NULL);
	events_t ev = {.l.lpVtbl = &evt, .n = 1};
	ev.ev[0].type = Steinberg_Vst_Event_EventTypes_kNoteOnEvent;
	ev.ev[0].sampleOffset = 100;
	ev.ev[0].Steinberg_Vst_Event_noteOn = (struct Steinberg_Vst_NoteOnEvent){.channel = 0, .pitch = 60, .velocity = 0.8f, .noteId = -1};
	double on = block(&ev, NULL);
	for (int i = 0; i < 20; i++) on += block(NULL, NULL);
	ev.ev[0].type = Steinberg_Vst_Event_EventTypes_kNoteOffEvent;
	ev.ev[0].Steinberg_Vst_Event_noteOff = (struct Steinberg_Vst_NoteOffEvent){.channel = 0, .pitch = 60, .velocity = 0, .noteId = -1};
	block(&ev, NULL);
	printf("rms before note %.5f, with note %.5f\n", silent / 10, on / 21);
	CHECK(on > 0.001);

	// a parameter and a controller
	queue_t qu = {.q.lpVtbl = &qvt, .id = 2, .off = 10, .v = 0.75};
	changes_t ch = {.c.lpVtbl = &cvt, .queue = &qu};
	block(NULL, &ch);
	uint32_t ccid = 0;
	CHECK(mm->lpVtbl->getMidiControllerAssignment(mm, 1, 3, 74, &ccid) == 0);
	CHECK(mm->lpVtbl->getMidiControllerAssignment(mm, 0, 0, 128, &ccid) != 0);
	CHECK(mm->lpVtbl->getMidiControllerAssignment(mm, 0, 0, 7, &ccid) == 0);
	qu.id = ccid; qu.v = 1.0;
	block(NULL, &ch);
	struct Steinberg_Vst_ParameterInfo pi;
	CHECK(ctl->lpVtbl->getParameterInfo(ctl, 2, &pi) == 0);
	Steinberg_Vst_String128 s;
	CHECK(ctl->lpVtbl->getParamStringByValue(ctl, 2, 0.75, s) == 0);
	char a[64]; int k; for (k = 0; s[k] && k < 63; k++) a[k] = (char)s[k]; a[k] = 0;
	char t[64]; for (k = 0; pi.title[k] && k < 63; k++) t[k] = (char)pi.title[k]; t[k] = 0;
	printf("param 2 '%s' default %.2f, 0.75 shows %s; cc7 ch1 is param %u\n", t, pi.defaultNormalizedValue, a, ccid);

	// state there and back
	mem_t m = {.s.lpVtbl = &mvt};
	CHECK(comp->lpVtbl->getState(comp, &m.s) == 0);
	printf("state %lld bytes\n", (long long)m.size);
	CHECK(m.size > 100);
	m.pos = 0;
	CHECK(comp->lpVtbl->setState(comp, &m.s) == 0);
	CHECK(ctl->lpVtbl->setComponentState(ctl, &m.s) == 0);
	restarts = 0;
	run(1);
	printf("after state: restart flags %#x, param 2 normalized %.3f, dirty calls %d\n", restarts, ctl->lpVtbl->getParamNormalized(ctl, 2), dirty);
	CHECK(restarts & Steinberg_Vst_RestartFlags_kParamValuesChanged);
	CHECK(fabs(ctl->lpVtbl->getParamNormalized(ctl, 2) - 0.75) < 1e-6);

	CHECK(proc->lpVtbl->setProcessing(proc, 0) == 0);
	CHECK(comp->lpVtbl->setActive(comp, 0) == 0);
	CHECK(ctl->lpVtbl->terminate(ctl) == 0);
	CHECK(comp->lpVtbl->terminate(comp) == 0);
	mm->lpVtbl->release(mm);
	proc->lpVtbl->release(proc);
	ctl->lpVtbl->release(ctl);
	run(0.5);
	uint32_t refs = comp->lpVtbl->release(comp);
	printf("last release -> %u\n", refs);
	CHECK(refs == 0);
	printf("OK\n");
	return 0;
}
