# Plan: reload the plugin without restarting the host (dev only)

State: implemented (steps 1–6), not tried in Live or Bitwig. Branch `hotreload`, from dcd0e6e. FORK.md, "Dev loader", describes what was built and measured.

## Goal

After a rebuild, the plugin offers "new build, reload"; one click moves
every Sointu instance in the host to the new code, with the song kept. No
restart of Live, no rescan. Dev only, macOS only.

## Findings

### The C layers already fit a loader (verified, by reading)

- VST3 (`vst3.c`, `view_darwin.m`), CLAP (`clap.c`) and AU (`au_darwin.m`)
  are plain C/ObjC. They call Go only through about 20 exported functions
  (`sointuNew`, `sointuProcess`, `sointuMIDI`, `sointuParam*`, `sointuState`,
  `sointuSetState`, `sointuShow/HideWindow`, `sointuManageWindow`,
  `sointuClose`, ...) with an opaque `uintptr_t` handle, and get 2 or 3 C
  callbacks (`sointu_mark_dirty`, `sointu_params_changed`,
  `sointu_window_closed`). They include `_cgo_export.h`.
- The three `main.go` files are near copies; they differ in name strings and
  small signature details (int32 or uint32, AU MIDI has no port, CLAP
  `ManageWindow` takes the plugin pointer).
- VST2 is pure Go (`third_party/vst2`), no C layer. Out of scope; Live has
  VST3 and AU.
- What the host caches is all in C: class IDs, bus layout, parameter count
  (64 slots + 8192 hidden MIDI CC ids in VST3), MIDI ports, the host's view
  (VST3/AU NSView with "Show window"). The Go side owns the window (a floating
  window of its own, not embedded), so the window needs no reparenting.

### Why a rebuild needs a restart today (verified, Makefile)

`make install-*` replaces the bundle. The host still has the old Go image
mapped and cannot unload it (no dlclose of a Go runtime); a changed bundle
also makes Live want a rescan.

### Several Go runtimes in one process (verified, experiment)

Scratch test (`scratchpad/exp`): a C host dlopens 5 Go c-shared libraries
(go1.27.1, darwin/arm64), each running goroutines that allocate (GC),
sleep and loop. Two host threads, like an audio thread, call into the
newest one every 1 ms; the call goes into Go, calls a C function pointer
and recovers a nil dereference. After the swaps the host calls all 5 in
turn.

- Works: all 5 load and run, 22161 calls with no loss, nil-dereference
  panics recovered in old and new runtimes (SIGSEGV is forwarded down the
  chain of handlers), GC runs in each.
- Copies of one build at different paths load as separate images with
  separate runtimes. The engine file only needs a new name per build.
- **Limit found:** Go forwards only synchronous signals to the previous
  handler (`sigfwdgo`, `runtime/signal_unix.go:1212`), not SIGURG, which it
  uses for async preemption. The handler of the newest runtime gets every
  SIGURG and drops those meant for older runtimes. So older runtimes lose
  async preemption: a stop-the-world (GC, ReadMemStats) waits until every
  goroutine reaches a function call. In the test, a loop without calls hung
  the old runtime for good; with calls in the loops everything completed.
  Consequence: after a reload, the old runtime must be idle (all its
  instances closed). Every instance in the process moves to the new engine
  together. A runtime that still renders after it has been replaced can
  stall GC for as long as its longest loop without a call takes.
  This already applies today when a VST2 and an AU Sointu are loaded in one
  process.
- ObjC classes (gio's `GioView`, `GioWindowDelegate`, `GioAppDelegate`):
  defined in two images, the runtime prints "implemented in both", but each
  image's code uses its own class (verified with two dylibs). Gio never looks
  up a class by name (checked by grep). Harmless. Optional: a per-build
  suffix by macro, to silence the warning.
- REX: `REXInitializeDLL_DirPath` succeeds from two images in one process,
  with the same bundle and with separate copies (verified). Decoding was not
  tested from both.
- Leaks, assumed: each old runtime keeps its code mapped (clean pages), its
  idle threads (a few, parked) and whatever heap the closed instance still
  reaches. Its scavenger returns freed memory to the system. To measure in
  step 6 with `footprint`/`vmmap --summary` over a few reloads. A restart
  now and then clears it.

### Hosts (verified, codesign)

Live 12 Suite and Bitwig (and its Plug-in Host) have hardened runtime with
`disable-library-validation`, so they load an ad-hoc signed dylib from any
path, as they load our ad-hoc signed bundles today. Bitwig runs plugins in
its Plug-in Host process; a reload happens inside that process the same way.
Assumed: Live hosts AU and VST3 in its own process (no sandbox).

### MCP id collision (verified, by reading `tracker/mcp/host.go`)

The id is `kind-pid-counter`, and the counter is a global of the runtime. A
new runtime counts from 1 again, so the reloaded instance gets the socket
name of an old one. The new instance's `os.Remove` + listen takes it over,
and then closing the old instance deletes the new socket and info file.
Fix: the loader passes a serial for the process, or the id gets a random
part.

## Options

### A. C loader + engine dylib (recommended)

The bundle the host loads holds only C: the existing `vst3.c`/`clap.c`/
`au_darwin.m` plus a new `loader.c` that defines the `sointu*` functions
as forwarders. The Go side is one engine `sointu-engine-<build>.dylib`, for
all formats, dlopened from a versioned path outside the bundle. A reload
loads the new file and moves each instance over with its own State/SetState.

- Latency: none added. Each call costs one indirect call and one atomic.
- Cost: moderate, ~300 lines of C, a merged Go `main` for the engine,
  Makefile targets, a menu item.
- Risks: the old runtimes accumulate (memory, see above). An ABI change
  between loader and engine needs a loader reinstall and a restart; it is
  guarded by a version number.

### B. Engine out of process (rejected)

Audio over shared memory, with the host's audio thread waiting for another
process each block, or one block late with the latency reported.

- The engine process cannot join the host's audio workgroup (workgroups are
  per process), so its thread runs at normal priority: dropouts under load.
  The async variant adds a block of latency and still needs that scheduling.
- Every call (state, params, MIDI, window, tempo) becomes IPC; plus process
  lifetime and crash handling. Several times the work of A.
- What it gains (true unload, crash isolation) is not needed for a dev
  reload.

### C. Go `-buildmode=plugin` (rejected)

It cannot unload either, and refuses to load a package that is already
loaded with different contents: a rebuilt engine would not load.

## Design of A

### Files and build

- `cmd/sointu-engine` (new, `package main`, c-shared): the union of the
  current exports, one canonical C API in `cmd/plugin/engine.h`, plus
  `sointuEngineABI() int`, `sointuBuildID() *char`,
  `sointuEngineInit(const sointu_host_callbacks *)` with the callbacks as
  function pointers (mark_dirty, params_changed, window_closed,
  request_reload). `sointuNew(p, kind, serial)` takes the format name
  ("sointu-vst3", for the recovery file and the MCP kind) and the serial
  for the MCP id.
- `cmd/plugin/loader/loader.c` (new): per instance a small struct
  `{engine *e; uintptr_t go; cached rate; window shown/managed/title;
  in_audio counter}`. The C files get this struct as the handle, so
  `vst3.c` etc. change little: `#include "engine.h"` instead of
  `_cgo_export.h`, and adapters where the signatures differ.
- The release plugins stay as they are (cgo, Go linked in). The C files
  build both ways: with cgo as now, or with clang alone for the loader.
- Makefile (macOS only):
  - `make dev-loader` / `install-dev-loader`: the VST3, CLAP and AU bundles
    with the loader instead of Go, signed ad-hoc. Same names and IDs as the
    release plugins, so projects keep opening (decided by the user).
    Reinstalled only when C code changes, then one restart.
  - `make engine` / `install-engine`: one `go build -buildmode=c-shared`
    (one Go build instead of four), codesign, copy to
    `~/Library/Application Support/sointu/engine/sointu-engine-<VERSION>-<time>.dylib`,
    then write `current` (the file name) through a temp file and rename. Keeps
    the newest 5 files. Deleting a mapped file is safe. The REX bundle is
    copied next to them once (`rex.libraryDirs` already looks in the
    dylib's own folder).

### Reload sequence (main thread)

1. Loader: on the first instance, dlopen the engine named by `current`, check
   the ABI and the parameter and port counts. If they do not match: load
   nothing new, show an alert "loader too old, reinstall and restart".
2. Engine (Go): a goroutine stats `current` once a second. When it names
   another build, an alert says "New build <id> ready: Edit → Reload
   plugin", and the menu item is enabled.
3. The click calls `request_reload`. The loader schedules the reload on the
   main thread (CLAP `request_callback` → `on_main_thread`; VST3/AU
   `dispatch_async` to the main queue).
4. For each instance of this loader: `new = New(...)`; replay the sample
   rate, then `SetState(old.State())`, which brings the song and the param
   values; replay `ManageWindow`/title. Swap the engine pointer atomically;
   wait until the audio thread is out of the old instance (counter);
   `old.Close()`; `ShowWindow` if the window was open; tell the host that
   param names and values changed, and the latency if it changed.
5. Instances created after the reload use the new engine.

Each loader (AU, VST3, CLAP) reloads its own instances. If two formats are
loaded at once, each needs its own click. Edge case, accepted.

What a reload keeps: song, param values, window open or closed. What it
loses: sounding voices and tails (a short gap), the window position, undo
history (assumed: `MarshalRecovery` holds the model data, not the undo
stack), tracker UI state not in the recovery data.

What a reload may change: any Go code (synth, tracker, MCP). Not: the C
layers, the parameter count, ports, IDs. Those need `install-dev-loader`
and a restart.

### Triggers (all three decided by the user)

- Menu item Edit → Reload plugin, with the alert of step 2.
- MCP tool `reload_plugin` (dev engine only): Claude builds, installs the
  engine, reloads and checks with `render_note`, without the user. It also
  needs the MCP id fix.
- Auto reload on a new build: a setting in the Edit menu, off by default,
  saved in `preferences.yml`.

## Steps

1. `engine.h` with the canonical API; the C files include it; release
   builds unchanged (tests: `go test -tags plugin ./cmd/...`, VST3
   validator).
2. `cmd/sointu-engine` + the MCP id fix.
3. `loader.c` + Makefile targets; the C test host (`cmd/sointu-vst3/testdata/host.c`)
   loads the loader, renders, swaps between two engine builds, checks
   that the state carried over and audio goes on.
4. Watcher, alert, menu item, auto reload setting, `request_reload` plumbing.
5. MCP `reload_plugin`.
6. Measure the footprint over 5 reloads. FORK.md section. Commit and push.
7. User: install loader once, restart Live, then try a reload with AU and
   VST3 in Live and CLAP in Bitwig.

## Risks

- Accumulating runtimes: memory per reload unknown until step 6.
- A replaced runtime that keeps rendering stalls its GC (preemption,
  above). The design closes every instance, so it is idle.
- Host calls during the swap on threads other than the main and audio
  threads (e.g. state from a worker thread in some hosts). The swap takes a
  per-instance lock that those calls take too.
- Untested in Live and Bitwig until step 7. No screen capture: the user
  checks the window and the button.

## Dev only

The loader bundles, `make engine`, the watcher, the menu item and
`reload_plugin` exist only in the dev build (build tag `devreload` in the
engine; release plugins have none of it). Release builds and CI unchanged.
