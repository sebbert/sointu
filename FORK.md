# Changes in the sebbert-custom branch

This branch of Sointu adds macOS, CLAP and VST3 plugins, audio samples and buffers,
granular synthesis, spectral processing, convolution, modules (reusable blocks of units),
a graphical parametric equalizer, up to 255 voices and 16 output channels
(seven aux pairs), [automation](#automation) from the plugin host that the
compiled song plays, and an [MCP server](#mcp-server) through
which Claude reads and changes the patch of a running tracker or plugin. Most of the new
synthesis features exist only in the Go synth and the WebAssembly player; the
x86 players (`vm/compiler/templates/amd64-386`) and the native bridge were left
behind on purpose. The [x86 backend](#updating-the-x86-backend) section lists
what they would need to catch up.

The rule for the synths: the Go synth and the wasm player render
**identically**, sample for sample, for every unit the wasm player has. Tests
in `vm/compiler/wasm_*_test.go` render songs in both, with node and wat2wasm,
and require exactly the same output. That covers all the regression songs in
`tests/`, except gm.dls samples, which the wasm player lacks. Small
changes to the sound are fine, as long as both stay in sync.

## Build and plugins

- `Makefile` builds all the tools and plugins, also in CI: `make`,
  `make install-vst`, `make install-vst3`, `make install-clap`,
  `make install-au`.
- A CLAP plugin (`cmd/sointu-clap`, `cmd/plugin` shared with the VST2) with 4
  note ports of 16 MIDI channels each: 64 MIDI channels.
- The plugins follow the tempo of the host, one way: the song's tempo can be
  edited, and it follows again when the host's tempo changes.
- The plugins play at any sample rate of the host, and the tracker at that
  of the audio device: the synth stays at 44100 Hz and its output is
  resampled. See [Sample rate](#sample-rate).
- The song is saved in the host's project. Edits mark the project unsaved
  (CLAP `mark_dirty`, VST2 `audioMasterUpdateDisplay`, VST3
  `IComponentHandler2::setDirty`).
- The plugin window comes to the front when it opens. Edit → Keep window on
  top keeps it above other windows; the setting is saved in the user's
  `preferences.yml`.
- The plugin window is the tracker's own, not embedded in a window of the
  host. On macOS it floats above the windows of the host while the host (or
  the helper process that the plugin runs in) is the application in front,
  and goes behind when another application comes to the front
  (`tracker/gioui/hostfront_darwin.go`, `window.go`): a helper process is
  not among the applications to switch to, so a window behind the host's
  could not be reached.
  In a host that loads its plugins in its own process (e.g. Ableton Live
  with the Audio Unit) the window does not float: it is one of the windows
  of the host, and the system's switching between the windows of an
  application reaches it.
- Removing a plugin does not hold the host up: `Instance.Close` does not
  wait for the tracker on the main thread of macOS, where the window that
  the tracker waits to close can only close once `Close` has returned
  (before, 3 seconds for every plugin removed with its window open), and
  Gio delivers nothing to a window after its `DestroyEvent`, where the main
  thread could wait for good.
  The loop of the tracker does not wait for the thread of the window when
  it closes, hides, shows or raises the window outside the answer to an
  event (`windowDo`): that thread could be waiting to hand over an event
  that only the loop takes, e.g. that the window was closed, and then
  neither went on. That was the hang on removing a plugin; a test host
  that removes the Audio Unit with its window open hung every time, and
  does not now.
- A window that is wholly behind others is not drawn (Gio,
  `windowDidChangeOcclusionState`), and is drawn again when it shows.
  Drawing it waited up to a second per frame, on the main thread, for a
  drawable that does not come back while the window is not shown: in a
  sample of Ableton Live the main thread spent 3.6 of 10 seconds in
  `[CAMetalLayer nextDrawable]`, and switching to the window took a second
  or two.
- A plugin shows its frames on its own, not with the transaction of the
  main thread and not in step with the display (`app.PresentAsync`, added
  to Gio), and draws at most 30 of them a second (`app.MinFrameInterval`;
  `tracker/gioui/framerate_plugin.go`): with the fix above alone, a second
  sample of Live still had the main thread waiting for the next drawable
  two thirds of the time, with the window in view. The tracker on its own
  draws as before. With that, a third sample still had the wait, and the
  window came a second after it was switched to: a window that was just
  brought to the front is only shown when the main thread is done, so a
  frame drawn then waits for a drawable until the wait gives up. So the
  drawable of the next frame is got ahead of time, off the main thread
  (`mtlContext.fetch`), and a frame that comes before it is left out and
  drawn when it has come (`errNoFrame`). **Not verified** in a host.
- CLAP: the plugin has the `gui` extension, as a floating window
  (`cmd/sointu-clap/clap.c`). A host that supports floating windows shows
  and hides the window, e.g. with the button of the device, suggests its
  title, and is told when the user closes it; the tracker then runs without
  a window (`Tracker.runHidden`) until it is shown again. The window opens
  when the host shows it; if the host has not created the gui within a
  second, it opens as before and cannot be closed (`plugin.NewHidden`). A
  window that was hidden opens again where the system puts new windows, not
  where it was. The VST2 plugin has no editor: its window always opens.
- An Audio Unit (version 2) plugin for macOS (`cmd/sointu-au`,
  `make install-au`, `sointu-au.component`), for hosts that load neither
  CLAP nor a VST2 they like, e.g. Ableton Live: a music device `aumu` /
  `Sntu` / `Vsar` with one stereo output and a parameter for each
  automation slot (see [Automation](#automation)), written against
  the system's C interface (`au_darwin.m`), with no SDK. MIDI on 16
  channels; tempo from the host's callbacks; the song is in the class info
  (`sointu-state`), so the host saves it with the project and in presets;
  the latency of the resampler is reported. An edit is told to the host as
  a change of the present preset, which is what Audio Units have for it.
  The view that the host shows for the plugin is a label and a "Show
  window" button: the tracker window opens when the host shows that view
  and from the button, and the user can close it. A host that never asks
  for the view gets the window after a second, as with CLAP.
  Checked with a host of a few calls (format, render with the host's and
  the unit's buffers, a note, class info there and back, the view).
  **Not verified:** in a host, and with `auval`.
- A VST3 plugin (`cmd/sointu-vst3`, `make install-vst3`,
  `sointu-vst3.vst3`, a bundle on every system), written in C against the
  C API of the VST3 SDK (`third_party/vst3/vst3_c_api.h`, Steinberg's own
  header, BSD licensed; nothing else of the SDK), so the build needs no
  CMake and no C++. Why not clap-wrapper around the CLAP: it needs CMake,
  C++ and the whole SDK, and asks a CLAP for an embedded GUI only, which
  ours is not. One object is the component and its own edit controller
  (`getControllerClassId` has none). An instrument (`Instrument|Synth`) with
  one stereo output and 4 event inputs of 16 MIDI channels each, as the
  CLAP's note ports. Notes come as VST3 events; MIDI controllers (for the
  tracker's MIDI learn) do not exist as events in VST3, so each controller
  of each channel of each input is a hidden parameter that the host maps
  them to (`IMidiMapping`, ids from `0x10000`; 8192 of them), turned back
  into MIDI CC. The automation slots are parameters 0 to 63, sample
  accurate (each point of the host's queues at its offset), normalized 0 to
  1 with 0.5 in the middle. Tempo from the process context; latency of the
  resampler from `setupProcessing`, told with `restartComponent`; the song
  is the state of the component, the controller has none; a loaded state
  tells the host the values of the parameters, a change of the bindings
  their titles. What is told to the host goes to the main thread with
  `dispatch_async` on macOS; on Linux and Windows it waits for the host's
  next call of the controller.
  VST3 has no plugin window outside the host's: the view that the host
  gets (macOS: an NSView, `view_darwin.m`) is a label and a "Show window"
  button, like the Audio Unit's. The tracker window opens when the host
  attaches the view, closes when it removes it (e.g. the host's window of
  the plugin is closed), and the user can close it on its own and get it
  back from the button. Linux and Windows have no view: the window opens
  after a second and cannot be closed, as with a host that asks for no
  view. REX builds bundle the REX library as for the others.
  Checked with the validator of the VST3 SDK 3.8.0 (537 tests, none
  failed, also the REX build) and a test host (`testdata/host.c`, run by
  `go test -tags plugin ./cmd/sointu-vst3`). **Not verified:** in Bitwig or
  Live, the view in any host, Linux and Windows (not even built).
- Vendored: Gio (`third_party/gio`, macOS plugin window and event loop fixes,
  `TopMost` on Windows and turning it off, `pointer.Event.Notch` for the
  notches of a mouse wheel on macOS, `app.FileDropEvent` for files dropped
  from the Finder: the view of a window, also of the plugin's, is a
  dragging destination of file URLs, and a drop is a window event of the
  paths, the position, the modifiers held and the pointer handlers there
  (`input.Router.TargetsAt`); other platforms send none),
  vst2 (`third_party/vst2`, `Host.UpdateDisplay`), CLAP headers, the VST3
  C API header (`third_party/vst3`, from steinbergmedia/vst3_c_api at
  93a116f, for SDK 3.8).

## Song format

All additions are optional fields, so older songs load unchanged.
Parameters added to existing unit types get values that keep old songs
sounding the same (`addedParameters` in `patch.go`).

- `Song.Buffers`: audio buffers, referenced by units through their `ID`.
  - A buffer with a `Sample` plays an imported audio file, stored in the song
    and encoded with ffmpeg for the compiled player.
  - A buffer with `Frames` is written by `bufwrite` units while the song plays.
  - A buffer with `Spectrum` holds FFT spectra. `Auto` marks buffers the
    tracker created for units and deletes with them.
  - A buffer with `Bus` is a bus of 8 channels for the mc units.
- `Song.EncodingPresets`: named ffmpeg encodings that samples share.
- `Song.Modules`: reusable blocks of units, and `Unit.Bind` in their units.
  See [Modules](#modules).
- `Unit.Bands`: the bands of an `eq` unit. See [eq](#eq).
- `Song.Automation`: the lanes of the automation slots. See
  [Automation](#automation).

## New units

All of these are Go and wasm only.

| Unit | What it does |
|---|---|
| `bufread` | Plays a buffer. `mode`: one shot, loop (with the crossfade `loopfade`), ping-pong, or tap, a tap of a delay line, which reads the frame at `start` every sample. `note`: what the note does, none, pitch, slice, or slice and pitch. Modulatable start/loop points, negative start counting back from the newest frame, `speed` (-1 to 1), edge fade, which in a full ring is a crossfade across the write head. `timing` counts the positions in frames, in frames at note 0 (pitch) or in 48ths of a beat (BPM). With `slices` continue or stop, it plays a slice of the buffer, picked by `slice` or by the note, with stop silent past the next marker. See [Parameters of bufread](#parameters-of-bufread), [Buffers as delay lines](#buffers-as-delay-lines) and [Slices and sample editing](#slices-and-sample-editing) |
| `bufwrite` | Writes into a buffer: a continuous ring buffer, or `oneshot` while the note is held. Several writers mix; `feedback`, `pop`. Without a buffer, it gets one of its own, `length` frames long; with `pervoice`, every voice has a copy of it |
| `spawn` | Triggers voices of another instrument: at a `rate`, synced to the beat, or on a rising edge. Transpose, note length, up to 4 `args`, `steal` |
| `arg` | Pushes a value passed by the `spawn` that triggered the voice |
| `automation` | Pushes the value of the lane of an automation slot at the time of the song. The tracker puts one, with a send, before every parameter that a slot is bound to. See [Automation](#automation) |
| `velocity` | Pushes the velocity of the note of the voice, 0 to 1: the MIDI velocity over 127. Notes of the score, and every note of a compiled song, have 1. Unlike the units above, it also compiles for x86 (as 1). See [Velocity](#velocity) |
| `window` | A smoothstep window over the spawned note, for click-free grains; timed by pitch, whole cycles of an oscillator |
| `spfft`, `spifft`, `spcopy` | Spectral analysis and resynthesis (Hann, 4× overlap, 256 to 8192 samples, mono or stereo), copying spectra |
| `spfilter`, `spcompress`, `spblur`, `spgate`, `spphase`, `spscale`, `spformant` | Change a spectrum in place: band cut and tilt, magnitudes pulled to their mean (with optional attack and release per bin), time smoothing and freeze, gate, phase dispersion/randomization/robot, bin scaling and shifting, formant shift |
| `spcross`, `spcomb` | Cross-synthesis/vocoder with another spectrum; resonances at the harmonics of up to 8 notes held in another instrument |
| `ott` | A three-band upward and downward compressor, like Ableton's OTT preset: crossovers at 88.3 Hz and 2.5 kHz, `depth`, `time`, `upward`, `downward`, a gain per band |
| `limiter` | A lookahead peak limiter: `threshold`, `release`, `lookahead` (0 to 11.5 ms, which is how late its output is), and `drive`, a gain before it of up to 18 dB that costs nothing in songs that do not use it. See [limiter](#limiter) |
| `softclip` | A clipper with a soft knee, a saturator that leaves the signal as it is below `knee`: `drive` (up to 18 dB), `knee`, and `oversample`, which clips at twice the sample rate against aliasing and costs nothing in songs that do not use it. See [softclip, width and ladder](#softclip-width-and-ladder) |
| `width` | Stereo width: scales the side signal (0 mono, 64 as it is, 128 double), with `lowcut`, a high-pass on the side signal that makes the bass mono and costs nothing in songs that do not use it |
| `ladder` | A low-pass of 24 dB per octave with resonance up to self-oscillation, like a Moog ladder: `frequency`, `resonance`, and `drive` into its saturator, up to 18 dB, that costs nothing in songs that do not use it |
| `reverb` | The standard reverb as one unit: low cut, high cut, predelay, a diffuser and a feedback delay network of 8 lines, stereo in, wet out. Renders what the Reverb module preset renders, in a third of its bytes; more parameters set what the module fixes. See [reverb unit](#reverb-unit) |
| `convolution` | Convolves the signal with an impulse response read from an audio buffer, a sample or what `bufwrite` units wrote, without latency: 1.5 ms to 11.9 s, mono or stereo, `gain`, `predelay`, `dry`. A written buffer is read again while playing (`follow`, `fade`). See [convolution unit](#convolution-unit) |
| `mcspread`, `mcsum` | Spread a mono or stereo signal over a bus of 8 channels (replacing or adding), and sum it back (with `width`) |
| `mcdelay`, `mcmix`, `mcfilter`, `mcloop`, `mcloopend` | Change a bus in place every sample: a delay line per channel (seeded lengths, modulation, note tracking, allpass, per-band decay), orthogonal mixes (Hadamard, Householder, seeded shuffle), one-pole filters, and a feedback loop. See [mc units](#mc-units) |

Spectral units and mc units run only in the first voice of their
instrument. See the README for the details of each unit.

`ott`'s constants, fixed in the unit (`vm/ott.go`, documented in `patch.go`),
approximate the OTT preset of Ableton's Multiband Dynamics:

| Band | Crossover | Upper threshold | Lower threshold | Attack | Release |
|---|---|---|---|---|---|
| low | below 88.3 Hz | -33.8 dB | -40.8 dB | 47.8 ms | 282 ms |
| mid | to 2.5 kHz | -30.2 dB | -41.8 dB | 22.4 ms | 282 ms |
| high | above | -35.5 dB | -40.8 dB | 13.5 ms | 132 ms |

Downward 66.7:1 above the upper threshold (the preset's high band is ∞:1),
upward 4:1 below the lower one, at most +24 dB. Thresholds are of the mean
square level (a full-scale sine is -3 dB); in stereo, of the sum of the
channels'. The crossovers are the `filter` unit's state-variable low-pass,
damping √2 (Q 0.707): the mid band is the low-pass of what the low band
leaves, the high band what is left after that, so the bands sum back to the
input, up to rounding. The preset's input gain
(+5.2 dB) and output gains (about +10.3, +5.7, +10.3 dB) are not built in,
so that `upward` and `downward` at 0 pass the input through.

## Modules

A module is a reusable block of units: `Song.Modules`, each with an `ID`, a
name, the number of signals its units expect on the stack (`Inputs`), up to
8 parameters and its units. A unit of the type `module` in an instrument, or
in another module, stands for the units of a module: its parameter `module`
is the ID of the module, and `p1` to `p8` set the parameters of the module.

```yaml
modules:
    - id: 1
      name: saws
      params:
        - {name: detune, default: 70}
      units:
        - {type: oscillator, id: 11, parameters: {detune: 70, type: 1, ...}, bind: {detune: 1}}
        - {type: oscillator, id: 12, parameters: {detune: 58, type: 1, ...}}
        - {type: addp, id: 13, parameters: {stereo: 0}}
patch:
    - numvoices: 4
      units:
        - {type: module, id: 20, parameters: {module: 1, p1: 80}}
        ...
```

**Expansion.** The synths and the compiled players never see modules:
`Song.Expand` (`module.go`) replaces every module unit with a copy of the
units of its module, so each module unit has its own state in every voice,
and the copies count towards the 63 units of an instrument. The tracker's
player, `sointu.Play` and the compiler expand the song first. Nothing in
the VM, the wasm player or the x86 players changed: a song with modules
compiles to exactly the player of the same song written without them, also
for x86 if its units compile for x86, and songs without modules compile to
exactly the same players as before. The `module` unit has no opcode
(`UnitType.Virtual`).

- **Parameters.** `Unit.Bind` of a unit of a module binds its parameters to
  the parameters of the module: the name of the parameter to the number of
  the module parameter, from 1. The copy gets the value of the module unit
  (the default of the module parameter if the unit does not set it), clamped
  to the range of the bound parameter. Any parameter that can be set or
  modulated can be bound, also those that cannot be modulated, like the type
  of an oscillator or a buffer; not `stereo`, the `args` and `mode` of
  `spawn`, and of `send` only `amount`, as they change how the unit uses the
  stack or where a send goes (`CanBind`). The delay times of a `delay` unit
  can be bound too, as `delaytime1`, `delaytime2`, ... in the order of its
  `varargs`.
- **Scaled bindings.** A binding can map the module parameter onto a range
  of its own: `bind: {frequency: {p: 1, min: 40, max: 100}}`. The module
  parameter then goes from 0 to 128, and the bound parameter gets
  `min` + (`max` - `min`)·value/128, rounded: `min` at 0, `max` at 128,
  which may be less than `min`. So one module parameter can move several
  parameters over different ranges, some of them the other way. A send to
  the module unit modulates each bound parameter by that much less: its
  amount, as (amount - 64)/64, is multiplied by (`max` - `min`)/128 and
  rounded to the nearest of the 64 steps of an amount, which is coarse for
  weak sends into narrow ranges (through modules using modules, by the
  product of the scales).
- **Range and display.** A module parameter takes its range and display
  from the first parameter bound to it: the range of that parameter, or if
  that binding is scaled, 0 to 128, displayed as the values it is mapped
  to. `display` (`type.parameter`, e.g. `filter.frequency`) of the module
  parameter overrides the display.
- **Sends.** The copies get new IDs, above every ID of the song. A send in a
  module to a unit of the module goes to the copy made with it; a send to a
  unit outside stays as it is. A mono send to a module unit, port k-1,
  modulates the parameters bound to module parameter k: it becomes a send to
  each of them, only the last one popping, and each of those sends counts as
  a unit. With nothing bound that can be modulated, a popping send becomes a
  `pop`. A stereo send also modulates the parameters of the next module
  parameter with its other channel. Where the two module parameters are
  bound to two ports next to each other of one unit, like `left` and `right`
  of a `receive`, it stays one stereo send. The others become mono sends of
  the top signal, an `xch`, mono sends of the other signal, an `xch`, and a
  stereo `pop` if it pops. A parameter
  that can only be modulated, like the inputs of `receive`, can be bound
  too: signals then reach the module through sends.
- **Buffers.** A buffer that the tracker created (`Auto`: spectra and buses)
  and that only the units of one module use belongs to that module. The
  first module unit using the module gets the buffer itself, every further
  one a clone, so that two reverbs made of one module do not share a bus.
  Other buffers are shared, and a buffer parameter can be bound instead.
- **Stack.** A module unit takes the inputs of its module and leaves its
  outputs: what its units leave, given the inputs (`Modules.Outputs`).
- **Modules using modules** are expanded too. A module cannot use itself.

`Expansion.Problems` lists what cannot be expanded as meant, and is left
out: modules using themselves, module units whose module is missing,
bindings that are not allowed, and sends from outside a module to one of
its units, which are ambiguous (send to the
module unit instead). The compiler and `sointu.Play` refuse such songs; the
tracker shows the first problem and plays the rest.

**Tests.** `module_test.go` (expansion), `vm/compiler/wasm_module_test.go`
(a song with modules expands to, renders and compiles like the same song
written without them, for wasm, 386 and amd64, and the wasm player renders
it like the Go synth) and `tracker/module_test.go`.

## eq

A unit of the type `eq` is a parametric equalizer, edited on a plot in the
tracker (see [Tracker](#tracker), EQ editor). It is not a unit of the
synths: like the `module` unit it is virtual, and `Song.Expand` replaces
it with a chain of `filter`, `belleq`, `ladder` and gain units before the
song is played or compiled (`Unit.CompileEQ` in `eq.go`). Nothing in the VM
or the players changed, a song with an eq compiles to exactly the player of
the same song with those units written by hand, and songs without one
compile as before. An eq whose bands are all off or do nothing expands to no
units at all. Its units count towards the 63 of the instrument.

```yaml
- type: eq
  parameters: {gain: -15, stereo: 1}
  bands:
    - {type: lowcut, frequency: 27, q: 1}
    - {type: bell, frequency: 250, gain: -4, q: 1.4}
    - {type: highshelf, frequency: 5000, gain: -3, q: 1, disabled: true}
```

**Data.** `Unit.Bands` is a list of `EQBand`: `type`, `frequency` in Hz,
`gain` in dB (bells and shelves), `q` (0 or missing is the default of the
type) and `disabled`. The unit has two parameters: `stereo`, and `gain`, the
gain of the whole eq in tenths of a decibel (±24 dB). Any number of bands;
the tracker adds up to 16. The eq takes and leaves one signal, or two in
stereo. An eq unit of a module is copied with the other units of the module
for every module unit, and expanded after that, like the eq units of the
instruments; its parameters cannot be bound, and it has no ports for sends.
`Expansion.EQs` tells where the units of each eq unit are in the expanded
song, by its ID (of its copy, for a module unit): the instrument, the first
unit and how many, which is where the tracker taps the signal before and
after it (see [Taps](#taps)).

**Bands.** What each compiles to, with the fewest units that do it:

| Type | Units | Compiles to |
|---|---|---|
| `bell` | 1 (0 at 0 dB) | `belleq` |
| `lowcut`, `highcut` (12 dB per octave) | 1 from Q 1, else 2 | `filter` (high-pass or low-pass); below Q 1 a `belleq` after it |
| `lowcut24`, `highcut24` (24 dB per octave) | 3 for Q 0.54 to 1.31, 2 above, 4 below | two such stages with Q·0.765 and Q·1.848: Butterworth at Q 0.71 |
| `ladder` (high cut, 24 dB per octave) | 1 | `ladder`; Go synth and wasm player only |
| `lowshelf`, `highshelf` | 4, or 5 below Q 1 | `push`, `filter`, `gain`, `addp` (and `belleq`): the signal plus k times a filtered copy of it |
| `notch` | 1 from Q 1 | `filter`, low-pass plus high-pass |
| `bandpass` | 1 from Q 1 | `filter`, band-pass |

and at the end of the eq one gain unit, if needed: for the `gain` of the eq,
for every shelf that lowers, for the band-passes (the band-pass of the
`filter` unit raises its center by Q) and for the ladders (whose resonance
lowers the level). All of these are one factor, so one unit: `gain` (up to
0 dB, in steps of 1/128) or `invgain` (from 0 dB), or `dbgain` (steps of
0.625 dB) where it comes nearer by more than 0.1 dB. Below 0.1 dB the unit is
left out.

- **Q below 1.** The damping of the `filter` unit is its `resonance`, 1/Q,
  which is at most 1: one unit cannot do a Q below 1, so not the 0.71 of a
  cut that is flat up to its corner. With Q 1 the level is 1.25 dB up next
  to the corner. A `belleq` after it makes up for it: the filter with Q 1
  has the poles of s² + s + 1, and a bell that lowers by Q (as a factor,
  -3 dB for 0.71) with the Q √Q is (s² + s + 1)/(s² + s/Q + 1), which
  replaces them. That is why new cuts and shelves start with Q 1: one unit
  less. A Butterworth cut of 24 dB per octave has the Qs 0.54 and 1.31,
  one stage of each kind: 3 units.
- **Shelves.** The `filter` unit has no shelf, and its outputs can only be
  added with the factors 1, 0 and -1. So a shelf is the signal plus k times
  its low-pass and band-pass (or high-pass and band-pass):
  (s² + (d + k)s + 1 + k)/(s² + ds + 1) with d = 1/Q, which raises that end
  by 1 + k. A shelf that lowers is the other shelf raising, with the whole
  signal lowered by the gain unit at the end. While it runs, a shelf needs
  one more signal on the stack (two in stereo).
- **ladder.** The resonance of the `ladder` unit is set from Q: its poles
  have Q 0.5 without feedback (4 one-pole low-passes in a row, -3 dB at the
  frequency of the band and soft), 0.71 with the feedback 0.25, more above.
  Its `frequency` is the one that puts the level 3 dB below that of the
  bass at the frequency of the band. Its saturator bends loud signals (a
  sine of level 0.5 gets about 1 % of third harmonic); the plot shows what
  it does to quiet ones.

**What the units can do, and what they cannot.** The parameters of the
units are whole numbers from 0 to 128, so the bands are not exactly where
they were put; `EQCompiledBand.Actual` has the values that the units have,
and the editor shows them.

- *Frequency.* `filter` has 2·asin(v²/32768)·44100/2π Hz: 27, 35, 43, 52,
  62, 72, 84, 96, 110, 124 Hz from the value 8 on, steps of 14 % at 100 Hz
  and 3 % at 2 kHz, up to 7350 Hz. `belleq` has 44100·v²/(16384π) Hz: 42,
  55, 69, 86, 104, 123, 145, 168 Hz from 7 on, steps of 19 % at 100 Hz and
  6 % at 1 kHz, up to 10.9 kHz (above the value 113 the unit comes back
  down, as it computes the cosine of its frequency as a square root). So in
  the bass a bell or a cut can be a few semitones from where it was put.
- *Gain and Q.* A bell has steps of 0.625 dB up to ±24 dB (the unit: ±40),
  and Q 32/n for n from 1 to 128: 0.25 to 32. A `filter` has Q 128/n: from
  1 up.
- *Bell, notch, band-pass, cuts with Q from 1:* one unit that is that
  filter. A low cut with Q 1 is within 0.1 dB of the filter it is modelled
  on up to 16 kHz; a notch within 0.5 dB.
- *Cuts with Q below 1:* at 100 Hz within 0.3 dB of a Butterworth filter
  (0.6 dB for 24 dB per octave), 0.7 dB at 1 kHz, 1.5 dB at 2 kHz and 4 dB
  at 4 kHz, as the bell and the filter are warped differently.
- *High cuts with `filter`:* its low-pass stops falling towards half the
  sample rate. A high cut of 12 dB per octave at 2 kHz is -31 dB at 16 kHz
  (-36 dB for the filter it is modelled on), at 4 kHz -16 dB (-24), at
  5 kHz -11 dB, and at the highest frequency, 7350 Hz, it is back at 0 dB.
  Above about 3 kHz the `ladder` type is the high cut: its -3 dB point goes
  from 13 Hz to 21.7 kHz.
- *Shelves:* k is what gives the two ends, at 10 Hz and 16 kHz, the gain
  asked for between them (within 0.25 dB), and the frequency of the filter
  the one with which the units come nearest to the shelf, with the middle
  of the shelf, in dB, at the frequency. With Q 1 the level goes beyond the
  shelf next to it: by 1.5 dB for a low shelf of ±6 dB, 2.4 dB for +12 dB,
  0.6 to 1.4 dB for high shelves. With Q 0.71 (5 units) it does not. The
  corner is from 20 Hz to 8 kHz. A shelf beyond what the filter reaches, or
  from 2 kHz up with a Q below 0.71, which has not ended at 16 kHz, is the
  nearest that the units can do, with less than its gain. The high-pass of the `filter` unit rises
  above 1 towards half the sample rate, and its band-pass does not fall to
  0 there, which k and the gain unit make up for; a high shelf that lowers,
  above 3 kHz, is the least like a shelf (up to 1.7 dB up below its corner).
- *Not there:* cuts of 6 dB per octave, tilt, all-pass, and a high cut of
  12 dB per octave above 3 kHz: the units have no one-pole filter. The
  bands cannot be modulated. A fraction of a step of the frequency would
  take a `loadval` and a `send` more for every band.

**The response.** `EQResponse(units, frequency)` returns what a chain of
units does to a sine: the product of the transfer functions of the units,
from their difference equations at 44100 Hz, with their parameters as they
are (and `push` and `addp` around a parallel path). For `filter`:
low = f²z/D, band = f(z² - z)/D, high = (z - 1)²/D with
D = z² + (rf + f² - 2)z + 1 - rf, f the frequency parameter squared and r
the resonance. For `belleq` the peaking filter of the Audio EQ Cookbook
with the cosine as the unit computes it; for `ladder`
(1 + k/2)·L⁴/(1 + k·L⁴) with L = g(1 + z⁻¹)/(1 - (1 - 2g)z⁻¹). The plot of
the editor draws this: of each band, and of all the units. Measured against
it (`TestEQResponseMatchesSynth`): 40 sines from 20 Hz to 20 kHz through
the units in the Go synth, for 27 bands of every type and for 8 bands in a
row; the level of each is within 0.03 dB of the computed one. Checked
another way, with noise instead of sines (32 s of the `noise` unit through
three eqs of 3 to 5 bands, the transfer function estimated with numpy over
about 170 windows of 16384 samples): 0.004 to 0.02 dB RMS apart over the 7423
bins from 20 Hz to 20 kHz, the most where a window is too coarse: 0.2 dB
on the slope of a low cut below 25 Hz, 1.5 dB at the bottom of a notch.

Compiling a shelf tries every frequency value of its filter, about 1 ms;
`compileEQBand` keeps the bands it has compiled, as the eq units of a song
are compiled every time the song is expanded.

**Tests.** `eq_test.go` (the units of each band, the response against the
Go synth and against the filters the bands are modelled on, shelves, YAML,
expansion, also in modules), `vm/compiler/wasm_eq_test.go` (a song with an
eq expands to, renders and compiles like the same song with the units
written by hand, for wasm, 386 and amd64, and without a `ladder` band only
for wasm; an eq that does nothing compiles like no eq; the wasm player
renders it and `examples/eq.yml` exactly like the Go synth),
`tracker/eq_test.go` (undo, a gesture as one step, numbers, saving, files,
clipboard, modules), `tracker/taps_test.go` (the spectra before and after
the eq) and `tracker/gioui/eq_editor_test.go` (the plot, the
mouse and the keys, without a window).

## limiter

`limiter` keeps a signal below `threshold`, with one gain for both channels
in stereo. It is the cheap kind: a delay line and two one-pole followers
(`vm/limiter.go`).

1. The input, times `drive` (1 + 7·drive, up to 18 dB), goes into a delay
   line of `lookahead`: 4 samples per step, up to 508 (11.5 ms).
2. The level follows the peaks: the larger of what goes into the delay line
   and what comes out of it. It jumps up to a higher peak at once and falls
   back by `release` (the same times as the compressor's), so it stays up
   until a peak has come out.
3. The gain reduction that brings the level down to `threshold`,
   1 - threshold/level, is smoothed with a time constant of a quarter of the
   lookahead, and the delayed signal is multiplied by 1 minus it.

The smoothing has done all but about 2 % of a gain change when the peak
comes out, so the output can exceed the threshold a little: a burst 12 dB
above the threshold after silence comes out up to 0.5 dB above it, a peak
6 dB above it about 0.2 dB. Where nothing may exceed full scale, put a
`clip` after it or set `threshold` a little lower. With `lookahead` 0 the
gain drops at once: no delay and nothing above the threshold, but the
sudden drops distort, like a clipper that recovers slowly. The output is
late by the lookahead, also against `sync`.

`drive` is the last transformed parameter of the unit and optional, like
`curve` of the envelope: songs that leave it at 0 and do not modulate it
compile without its operand and without its code (`LimiterDrive`). The
lookahead is an operand after the transformed parameters.

The state does not fit in a unit: 4112 bytes, the level, the reduction, the
frame of the delay line to write next, 4 unused bytes and 512 frames of two
floats. Like `ott`, the states are in a table of their own, in the order the
units run, voice by voice: `su_limiter` and `$limiterWRK` in the wasm
player, `GoSynth.limiters`, `Patch.NumLimiters()` of them. They are not
cleared when a note is triggered. Without limiters, nothing of it is in the
player. Tests: `vm/limiter_test.go` and `vm/compiler/wasm_limiter_test.go`
(the wasm player renders it like the Go synth, with and without drive).

## softclip, width and ladder

Three units for shaping a signal, in `vm/shaping.go`. Their state fits in
the unit. They are tuned by measurement, not by ear.

`softclip` multiplies the signal by `drive` (1 + 7·drive, up to 18 dB) and
clips it with a knee: up to the level `knee` it passes as it is, from there
it bends, |y| = |x| - (|x| - knee)² / (4·(1 - knee)), and at 2 - knee it
reaches full scale with no slope left and stays there. `knee` 128 is the
`clip` unit, `knee` 0 bends from silence. Unlike `distort`, a signal below
the knee is not changed at all, so on a bus or the master it only acts on
the peaks.

With `oversample` 1 it clips at twice the sample rate. The half-band filter
is two first-order allpasses, (A0(z²) + z⁻¹·A1(z²))/2 with the coefficients
0.19104233 and 0.66083542: flat to 15.4 kHz, 44 dB down from 28.7 kHz. The
two allpasses give two samples for each input sample; after the clipper the
same two, crossed, give the average. A signal below the knee comes out
through A0·A1, an allpass: every frequency keeps its level, the phases
shift. Measured on a 5 kHz tone clipped hard at +12 dB, the overtones that
fold back to 9.1 and 0.9 kHz drop from -20 and -26 dB to -47 and -51 dB;
the one that folds to 19.1 kHz from -14 to -28 dB, as the filter is not
steep there. Peaks can exceed full scale a little after the filter (up to
about 2 dB on such a signal): put a `clip` after it where nothing may.
`oversample` is an operand after the transformed parameters, and it and
the code are only in songs with such a softclip (`SoftclipOversample`). The
state is the four allpasses for each channel.

`width` splits left and right into mid, (l + r)/2, and side, (l - r)/2,
multiplies the side by 2·width and puts them back: 0 is mono, 64 leaves the
signal, 128 doubles the side. `lowcut` is a high-pass on the side signal
before that, a state-variable filter like the `filter` unit's with the same
frequencies and Q 0.707: below it the signal becomes mono (measured with
`lowcut` 17, about 120 Hz: the side of a 40 Hz tone 19 dB down, of a 2 kHz
tone unchanged). The unit takes and leaves a stereo signal; it has no
`stereo` parameter. `lowcut` is optional like `drive` of the limiter
(`WidthLowcut`).

`ladder` is four one-pole low-passes in a row with the output fed back into
the input, 4.5·resonance times, through a saturator, 1.5·(u - u³/3) for
u = x/1.5 clipped to ±1. The low-passes are trapezoidal,
y = G·x + (1 - G)·s with G = frequency² (at most 0.99), which puts the
cutoff at atan(G/(1 - G))/π·44100 Hz: 223 Hz at 16, 4.5 kHz at 64,
12.8 kHz at 96. The feedback has no delay: the output the ladder would have
without the saturator, L(x)/(1 + k·G⁴) with L(x) the input through the four
low-passes, is computed first and subtracted from the input. So the
resonance sits at the cutoff and is equally strong at every cutoff, which
it is not with the usual one sample of delay in the loop; from `resonance`
114 the filter oscillates by itself, at a level of about 0.2. The feedback
is taken against half of the input, so the bass drops by at most 4.6 dB
with the resonance instead of 14.8 dB. `drive` (1 + 7·drive) pushes the
input into the saturator; it is optional (`LadderDrive`). A signal at full
scale is already bent by the saturator at `drive` 0: a sine of level 0.5
gets about 1 % of third harmonic.

Tests: `vm/shaping_test.go` and `vm/compiler/wasm_shaping_test.go` (the wasm
player renders them like the Go synth, with and without the optional
parts, which are only in the players of songs that use them).

## mc units

A modular multichannel reverb, after Geraint Luff's "Let's write a
reverb": the units share a bus of 8 channels, which they read and change
in place every sample, like the spectral units share a spectrum buffer
frame by frame. A diffuser (`mcdelay` without decay, `mcmix` shuffle,
`mcmix` hadamard, a few times with shrinking sizes) feeding a feedback
delay network (`mcloop`, `mcdelay` with decay, `mcsum`, `mcmix`
householder, `mcloopend`) is a reverb; the presets Reverb FDN Room, Hall,
Ambient and Plate in UTIL are such chains.

**Parameters.**

| Unit | Parameter | Values |
|---|---|---|
| `mcspread` | `gain` | ±40 dB, like `dbgain` (modulatable) |
| | `add` | 0 replaces the bus, 1 adds to it |
| `mcsum` | `gain` | ±40 dB (modulatable) |
| | `width` | 0 to 200 % of the stereo difference (modulatable) |
| `mcdelay` | `size` | the longest line, 0.1 to 2000 ms in steps of 0.1 ms |
| | `spread` | the shortest line is `size`·(1-`spread`), 0 to 100 % |
| | `seed` | 0 to 255 |
| | `decay` | 0 is no loss; else the time to fall by 60 dB, 2^(v/16-4) s: 1 s at 64, doubling every 16 steps (0.07 to 16 s) |
| | `hfdecay` | the decay time above 3 kHz relative to `decay`, v/128 |
| | `lfdecay` | the decay time below 250 Hz relative to `decay`, 2^((v-64)/32): 1/4 to 4 |
| | `moddepth` | 8·(v/128)² ms, up to 8 ms (modulatable) |
| | `modrate` | 2^((v-64)/16) Hz, 1/16 to 16 Hz (modulatable) |
| | `notetracking` | the lengths times 2^((60-note)/12) |
| | `allpass`, `apgain` | Schroeder allpasses with coefficient v/128, up to 0.9375 |
| `mcmix` | `type`, `seed` | hadamard, householder, or shuffle with `seed` |
| `mcloop` | `feedback` | 0 to 100 % (modulatable) |
| `mcfilter` | `frequency`, `type` | 20·2^(10v/128) Hz, 20 Hz to 20 kHz (modulatable); lowpass or highpass |

**Channels and polarity.** `mcspread` puts the left signal on the even
channels and the right one on the odd ones (both on all in mono), with
polarity +1 for channels 0, 1, 4, 5 and -1 for 2, 3, 6, 7. `mcsum` sums
with the same polarities, the even channels to the left and the odd ones
to the right, times 1/4 (all times 1/8 in mono), so that a spread followed
by a sum passes the signal through. For decorrelated channels, as after a
diffuser, the sum is then 6 dB (stereo) or 9 dB (mono) below the input;
the presets raise the gain of `mcspread` to compensate.

**mcdelay.** When the patch is encoded (`newMCDelay` in `vm/mc.go`),
channel c gets the length size·(1-spread·(p(c)+r)/8), where p is a random
permutation of 0 to 7 and r a random number in [0, 1): one length in each
eighth of the range, in random order. The lengths are rounded to whole
samples, which linear interpolation does not damp, unless note tracking
scales them. The random numbers come from a 32-bit linear congruential
generator (x·1664525 + 1013904223, Numerical Recipes), seeded with
seed·0x9E3779B9 + 1 and advanced 4 times; it runs only in Go, and the
players read the lengths from tables. Per sample and channel:

1. The modulation: the phase advances by the rate times 1, 1.125, ...,
   1.875 for channels 0 to 7; the triangle |2·frac(phase + c/8) - 1| times
   the depth is added to the length.
2. The line is read at that delay, clamped to [1, frames-2], with linear
   interpolation, and written with the channel: or with allpass, x + g·y,
   the output being y - g·(x + g·y).
3. The decay filter (`mcDecay`): `lo += 0.035·(y - lo)`, `y += A·lo`,
   `hi += 0.348·(y - hi)`, `out = B·y + C·hi`: one-pole low-passes at
   250 Hz and 3 kHz making a low shelf and a high shelf. With the gains
   g = 10^(-3L/(44100·T)) of a line of length L for the decay time T of
   each band, A = glo/gmid - 1, B = ghi, C = gmid - ghi. Each shelf's
   magnitude lies between its gains, and `hfdecay` is at most 1, so the
   magnitude stays below max(glo, gmid) < 1 and a loop with orthogonal
   mixes is stable (Jot). Losses of several lines in a loop add up, so two
   `mcdelay`s with the same decay in one loop still decay in that time.
4. In allpass mode, the modes near an allpass' resonances are delayed up
   to (1+g)/(1-g) times the length, decay slowest and make the tail; the
   decay is calibrated for that longer length. The tail then decays in
   roughly the decay time (-53 dB/s for 1 s in the tests), the rest of the
   sound faster.

The ring of a unit has 2^n frames of 8 floats, enough for the longest line
(times 32 with note tracking, for note 0) plus 32·44.1 samples of
modulation, at most 2^17 frames (3 s).

**mcmix.** Hadamard: butterflies of channels 4, 2 and 1 apart, then times
1/√8. Householder: x - (Σx)/4, the sum computed as
((x0+x4)+(x2+x6)) + ((x1+x5)+(x3+x7)). Shuffle: channel c becomes channel
p(c) times ±1, from the generator above.

**Encoding.** Operands: the transformed parameters, then the index of the
unit in `Bytecode.MCUnits`, then for `mcspread` add, for `mcmix` the type,
for `mcfilter` the type and for `mcdelay` flags (1 note tracking, 2
allpass); each of these four only in songs whose units differ in it (see
[Parts conditional on use](#parts-conditional-on-use)). `mcspread` and
`mcsum` use the stereo bit. `Bytecode.Buses` are
the buffer IDs of the buses, `MCUnit.Delay` and `MCUnit.Shuffle` the
tables.

**Wasm player.** `su_mc_table` has 4 i32s per unit: the offsets of its bus
and state in `su_mc`, the offset of its constants in `su_mc_consts`, and the
offset of the voice running it from `su_voices`; the voice, and the check
for it, only when an instrument with mc units has several voices. A bus is 16
floats: the frame, and the frame stored by `mcloopend`. An `mcdelay` state
is the phases, the low and high filter states, the ring position (128
bytes) and the ring; an `mcfilter` state 64 bytes. `su_mc_consts` starts
with the modulation rates and phase offsets (only in songs that modulate
an `mcdelay`) and the channel byte offsets, then per `mcdelay` the lengths, A, B, C, the ring mask, the longest delay and
apgain, and per shuffle the source offsets and signs. Every unit computes
the 8 channels as two f32x4 vectors; `mcdelay` gathers its reads lane by
lane. Code for `add`, each `mcmix` type, the high-pass, note tracking,
allpass, the modulation, the gains, the width and the feedback is included
only when a unit uses it; without mc units, nothing changes. The shuffle uses the unit's own state in the voice as scratch.

**Costs.** Under node, a song of 20 s with a burst of noise into the
Reverb FDN Hall preset renders in 2.2 s (0.3 s without the reverb), like
the old Reverb Hall preset of 32 delay lines (2.1 s). Its player is 4 KB
larger than without the reverb, 1.4 KB gzipped, 0.9 KB more than with the
old preset.

## reverb unit

`reverb` is the standard reverb: the network of the Reverb module preset
(`tracker/modules/Reverb.yml`, 22 [mc units](#mc-units) and an `addp`) as
one unit, which costs a third of the bytes in the player, and extended so
that it also renders the presets Reverb FDN Room, Hall, Ambient and Plate
and the chain of `examples/reverb.yml`. The presets with a reverb (Global
reverb, Global mastering reverb, Global mastering 2 reverb, Global
mastering 2 drumbus reverb) and the module preset Ducking reverb, with the
presets that carry it, have this unit; before, they had the Reverb module,
and each renders exactly what it rendered with it
(`TestSwitchedPresetsRenderAsBefore`). The module preset Reverb, the four
FDN presets, `examples/reverb.yml` and `examples/reverb_module.yml` are
still chains of mc units, for changing the network itself; the presets
Reverb unit Room, Hall, Ambient and Plate (UTIL) are the FDN presets as one
unit each, and `examples/reverb_unit.yml` is `examples/reverb_module.yml`
with the unit. The code is `vm/reverb.go` and `templates/wasm/reverb.wat`.

**The unit.** Stereo in, the wet signal out, with the parameters of the
module and their ranges: `size`, `decay`, `highs`, `lows`, `predelay`,
`mod`, `highcut`, `lowcut`. It computes what the units of the module
compute, in the same operations and the same order, so it renders exactly
what the module renders at the same values: no sample differs
(`TestReverbUnitRendersLikeTheModule`, 7 settings, also with `mod`
modulated). Of what the module leaves to its units, the 8 channels, the
seeds and the mixes are fixed; the rest are [more parameters](#more-parameters)
with the values of the module as defaults. Where the bytes come from:

- One opcode with four operands instead of 23 with theirs, no table of
  units, no bus: the frame a step works on is the frame of the ring of the
  next step.
- The low cut, the high cut and the predelay run on left and right only,
  as the 8 channels are those two with the polarities of `mcspread` until
  the first step of the diffuser. The predelay and the first step are one
  stereo ring, read at the sum of their lengths.
- The delays of the diffuser have whole lengths and no decay: no
  interpolation, no decay filter. The shuffle after each is in its taps: a
  tap is 16 bits, how far behind it reads its ring and whether it flips
  the sign bit.
- The Householder mix is not stored: the outputs of the lines and Σx/4 are
  kept and subtracted when the lines are fed.
- Scalar loops over the 8 channels instead of two f32x4 vectors: SIMD
  instructions and constants are 2 to 18 bytes each.
- The modulation of the lines (103 bytes) is only in songs with `mod` not
  0 or modulated (`ReverbMod`).

The lengths and decay coefficients are computed by the code of `mcdelay`
when the patch is encoded (`newReverb`), 192 bytes for each unit
(`su_reverb_consts`): A, B, C and the lengths of the 8 lines, and the 32
taps. Units with the same `size`, `decay`, `highs`, `lows` and `predelay`
share them. The state, 778400 bytes, is in a table of its own like that of
the limiter (`su_reverb`, `$reverbWRK`, `GoSynth.reverbs`,
`Patch.NumReverbs()`), one for each voice of the instrument. x86 has a stub
and the compiler refuses the unit for it.

<a id="more-parameters"></a>**More parameters.** What the module fixes in
its units, and what the four FDN presets do differently, are parameters
after `lowcut`. None can be modulated: they are read when the patch is
encoded and end up in the constants of the unit. A song saved without them
gets the defaults (`addedParameters`). Each costs code and data only in
songs that set it, and a song whose units leave them all at their defaults
compiles to the same bytes as before they existed (checked: the players of
the three songs below are identical byte for byte). Bytes are measured on
song 1, one unit; gzip of the whole player.

| Parameter | Default | Sets | Code | Data per unit | gzip |
|---|---|---|---|---|---|
| `gain`, `early`, `earlywidth`, `tailwidth`, `modrate` | 76, 52, 80, 96, 56 | the level of the input and of the early reflections, the two widths, the rate of the modulation: read from the constants instead of being constants of the code (`ReverbLevels`) | +5 | +20 | +16 |
| `steps` | 4 | 1 to 4 steps of the diffuser (`ReverbSteps`) | +4 | +1 | 0 |
| `spread`, `network`, `diffuser`, `pretime` | 77, 0, 0, 0 | how far the lines differ; the longest line of the network, of the first step of the diffuser (each further step half) and the predelay in 0.1 ms instead of by `size` and `predelay`: other numbers in the same constants | 0 | 0 | 0 |
| `bypass` | 0 | bit 0 no low cut, bit 1 no high cut, bit 2 no predelay. A filter that no unit of the song has is not in the player; one that only some have costs a test of a bit | -36, -29, 0; -95 for both filters | 0 | -10, -11, 0; -48 |
| | | filters in some units only | +36 | +1 | about +25 |
| `allpass` | 0 | the delays of the diffuser as Schroeder allpasses with this coefficient (`ReverbAllpass`). Each channel then has its own allpass in the first step too, so the predelayed input is spread on a ring of 8 channels of its own (131 KB more state) | +139 | +8 | +83 |
| | | allpasses in some units only: both kinds of taps | +220 | +8 | about +130 |
| `loopsize`, `loopgain`, `loopmod`, `looprate` | 0, 0, 0, 64 | a second set of 8 lines in the network before the others: their longest length, allpass coefficient and modulation, with seed 5 and the `spread` and decay of the network (`ReverbLoop`). A line is then a function, called for each set (524 KB more state) | +126 | +140 | +213 |

The filters, the levels and the steps are decided for the song from the
values of the parameters (`FeatureSetMacros`) or from the encoded units
(`wasmUnitFeatures`); `wasmReverb` in `compiler.go` lays the constants of a
unit out accordingly, 192 to 362 bytes. In the Go synth a unit takes the
path of its own parameters.

**The FDN presets as one unit.** Each renders exactly what its chain of mc
units renders, in the Go synth (`TestReverbUnitRendersLikeThePresets`) and
in the wasm player. Bytes of the player of song 1 with the preset in place
of its reverb, mc units → unit:

| Preset | What it needs beyond the module | Total | Code | Data | gzip | brotli |
|---|---|---|---|---|---|---|
| Room | 3 steps, other lengths, levels and rate, no filters, no predelay | 5258 → 3621 | 3929 → 3190 | 1299 → 401 | 2607 → 2152 | 2514 → 2108 |
| Hall | other lengths and widths, no filters, no predelay | 5512 → 3616 | 3929 → 3186 | 1553 → 400 | 2709 → 2202 | 2610 → 2157 |
| Ambient | other lengths, levels and rate, no filters, no predelay | 5514 → 3616 | 3931 → 3186 | 1553 → 400 | 2671 → 2164 | 2589 → 2127 |
| Plate | 2 steps of allpasses, a second set of lines (allpasses), no low cut, no predelay | 5374 → 4093 | 4119 → 3514 | 1225 → 549 | 2828 → 2504 | 2707 → 2447 |
| `examples/reverb.yml` | no high cut, predelay 20.0 ms, network 150 ms | 5848 → 3657 | 4089 → 3247 | 1729 → 380 | 2787 → 2227 | 2694 → 2197 |

So Room, Hall and Ambient cost less than the plain reverb (they have no
filters), 0.45 to 0.5 KB gzipped less than as mc units; the Plate, with
both expensive parts, 0.32 KB less. Under node they render in 0.70, 0.70,
0.69 and 0.75 s instead of 0.85, 0.96, 0.91 and 0.84 s (0.61 s without a
reverb). The state of the unit is 778 KB whatever the lengths, 1.43 MB in
a song with a Plate; the mc chains take 0.39 (Room), 0.66, 0.92 and
0.52 MB.

**Where it differs from the module.**

- A send to `highcut` or `lowcut` moves it by the amount in steps of the
  frequency of `mcfilter`; the module scales the amount by the range of the
  binding (56/128 and 85/128). A send to `mod` is the same.
- `mod` modulated above 2 (256): the lengths are clamped to the ring, which
  is 2^14 frames in the unit and depends on `size` in the module.
- In an instrument with several voices, the module runs in the first voice
  only and the others get silence; the unit has a reverb for each voice.
- Infinite signals, and the sign of a zero: x + 0·y and x·1 are left out.

**Size.** Bytes of the wasm player, module → unit. Code is everything but
the data section; gzip -9 and brotli -q 11 of the whole file. Song 1 is
`examples/reverb_module.yml` and `examples/reverb_unit.yml` (two
instruments and the reverb); song 2 has a plate of 8 mc units on another
bus as well; song 3 a second reverb with other settings.

| Song | Total | Code | Data | gzip | brotli |
|---|---|---|---|---|---|
| without the reverb | 2481 | 2269 | 182 | 1413 | 1385 |
| 1: one reverb | 5874 → 3686 | 4098 → 3276 | 1746 → 380 | 2799 → 2239 | 2714 → 2211 |
| 2: one reverb, mc units anyway | 6447 → 5872 | 4236 → 4931 | 2181 → 911 | 3031 → 3235 | 2935 → 3148 |
| 3: two reverbs | 7439 → 3988 | 4186 → 3364 | 3223 → 594 | 3162 → 2448 | 3066 → 2387 |
| `examples/soundset_loop.yml`, as it was with the module → as it is | 8300 → 6151 | 5833 → 5049 | 2437 → 1072 | 4068 → 3524 | 3917 → 3443 |
| `examples/soundset.yml`, the same | 9602 → 7451 | 5545 → 4759 | 4027 → 2662 | 4206 → 3669 | 4021 → 3561 |

So the reverb costs 3.4 KB (1.39 KB gzipped) as a module and 1.2 KB
(0.83 KB) as the unit: 2.2 KB less, 0.56 KB gzipped, 0.50 KB with brotli.
Each further reverb costs about 0.36 KB gzipped as a module and 0.21 KB as
a unit. In a song that has mc units anyway, the unit comes on top of their
code: 0.2 KB more gzipped, although 0.6 KB less uncompressed. The code of
the mc units is 1747 bytes in 11 functions (`mcdelay` 757, `mcmix` 450),
that of the unit 980 in 4 (837, of which 103 the modulation); the data of
the module 1.56 KB (264 the table of units, 1216 the constants), of the
unit 198 bytes.

**Speed and memory.** In the Go synth (`BenchmarkReverb`), the reverb takes
1070 ns per sample as a module and 270 ns as the unit (220 ns before it had
the allpasses and the second lines to test for); in the wasm player
under node, on song 1, 580 ns and 166 ns (the song renders in 0.71 s
instead of 0.98 s; 0.61 s without a reverb). The state of the module is
787 KB with the default `size` and `predelay`, 460 KB with the smallest
and 1.64 MB with the largest; that of the unit always 778 KB.

**Sound.** Nothing to compare: impulse responses of the module and the
unit at four settings, and the wasm renders of the songs above, are the
same sample for sample. What both do, measured on the impulse response,
not judged by ear: with the defaults the first sample comes after 28 ms,
the reverb time (T30) is 3.5 s at 125 Hz, 3.1 s at 500 Hz, 2.4 s at 2 kHz
and 1.3 s at 8 kHz, the echo density reaches that of noise after 100 ms
(0.54 of it after 10 ms), and left and right of the tail correlate by
-0.40.

**What is lost.** The unit is a diffuser into a feedback delay network of
8 channels, with the parameters above. The mc units can also: other seeds,
note tracking, other mixes, less feedback, another level of the tail, a
mono input or output, modulated levels, filters in the loop, more than two
sets of lines or steps that differ in kind, several inputs on one bus, any
other order, and the meters of the tracker after each unit. No preset
needs those: what had the Reverb module (Global reverb, the mastering
presets with a reverb, the Ducking reverb module) has the unit now, and
the four FDN presets have unit versions next to them.

**Left out,** as nothing uses them and each would be code that only such
a song has: note tracking of the lines (about 40 bytes), the other mixes
(about 150), a mono input (about 40), the level of the tail and the
feedback as parameters (a few bytes each), modulating the levels (about
45 bytes and two `$exp2f` a sample). Where it stops paying: the code of
the unit is 980 bytes plain and 1245 with allpasses and the second lines,
against 1747 for the mc units; with everything in this list it would be
at about 1550, and the 0.3 KB gzipped that the constants of the unit save
would be most of what is left. Every part is also a branch of the template
and a row of the test table (`TestReverbPartsOnlyWhenUsed`, 19 songs now).

**Not done.** Computing the decay coefficients in the player from the
lengths (96 bytes of data less for each reverb, about 60 of code more) and
dropping the clamp of the modulated lengths (12 bytes) would both end the
exact match with the module.

## convolution unit

`convolution` convolves its input with an impulse response read from an
audio buffer: a convolution reverb from a response that the song makes
itself, a cabinet, a body or a resonator, early reflections, any fixed FIR
filter. The code is `vm/convolution.go` and `templates/wasm/convolution.wat`.
Go synth and wasm player only; x86 has a stub and the compiler refuses the
unit for it.

**Parameters.**

| Parameter | Default | Sets |
|---|---|---|
| `stereo` | 0 | a mono unit uses the left channel of a stereo buffer; a stereo unit convolves left with left and right with right, or both with a mono buffer. There is no true stereo (left into right) |
| `gain` | 64 (0 dB) | the level of the wet signal, ±40 dB like `dbgain`; can be modulated. The response is not normalized |
| `buffer` | none | the audio buffer of the response, by ID, as for `bufread` |
| `start` | 0 | the first frame of the response in the buffer |
| `length` | 88 (2.97 s) | the frames of the response: 64·2^(v/8), from 64 (1.5 ms) to 524288 (11.9 s) in steps of 9 %. Frames beyond the valid frames of the buffer are silent |
| `predelay` | 0 | delays the wet signal: 64 samples a step, up to 184 ms |
| `follow` | 0 (1×) | how many blocks of each part of a written response are read again at a time: 1, 2, 4 or 8 |
| `fade` | 0 | what a block read again changes fades in over the block instead of stepping |
| `dry` | 0 (off) | the input times this is added to the output; can be modulated |

**The algorithm** has no latency: the first sample of the output has the
first frame of the response in it. The response is cut into four parts
(Gardner's partitioned convolution, with fixed sizes):

| Part of the response | How | Work |
|---|---|---|
| frames 0 to 64 | the sum of the last 64 input samples times those frames, every sample | 64 multiplications a sample |
| 64 to 512 | FFTs of 128 frames, every 64 samples | up to 7 blocks |
| 512 to 4096 | FFTs of 1024 frames, every 512 samples | up to 7 blocks |
| 4096 to the end | FFTs of 8192 frames, every 4096 samples | a block for every 4096 frames |

A part with blocks of B frames keeps the spectrum of each of its blocks of
the response, and the spectra of the last blocks of the input, as many.
Every B samples it transforms the last 2B input samples, multiplies the
spectrum of the input p blocks ago with that of block p of the response,
adds the products up and transforms the sum back: the second half of the
result is its output for the next B samples. What a part of the response
that starts at frame B does to the input is at least B samples late, so
nothing is late. A response of 64 frames or less has no FFT at all, and the
player of a song with only such units has none of that code; a part that a
response does not reach does nothing.

- The FFT is that of the spectral units (`fft`, `$fft`: complex, radix 2,
  f32x4 in the player), with their twiddle tables, now also in songs with a
  convolution unit and no spectral unit. A real FFT of half the size would
  halve the work of the FFTs, for more code.
- The spectra are stored as the real parts of bins 0 to B and then the
  imaginary parts, so that the player multiplies and adds four bins at a
  time without shuffling. The inverse transform is the forward one of the
  conjugate.
- The outputs of the parts are added into a ring of 16384 frames, `predelay`
  frames ahead, which is all that the predelay costs.
- Go synth and wasm player compute the same operations in the same order:
  no sample differs.

**Where the response comes from.** `length` frames of the buffer from
`start`, counted from the beginning of the buffer, not from its oldest
frame as `bufread` counts. A frame that the buffer has not got (beyond its
end, or beyond what a oneshot `bufwrite` has written so far) is silent.

- *A sample* is read once: when the song starts (in the tracker, when the
  sample is decoded or the unit changes).
- *A buffer that is written* is read again all the time, block by block:

  | Part | Read again | Once around |
  |---|---|---|
  | frames 0 to 64 | every sample, from the buffer | no lag |
  | 64 to 512 | a block every 64 samples | 448 samples, 10 ms |
  | 512 to 4096 | a block every 512 samples | 3584 samples, 81 ms |
  | 4096 to the end | a block every 4096 samples | the length of that part |

  `follow` reads 2, 4 or 8 blocks of each part at a time: the tail of a 3 s
  response is then replaced in 1.5, 0.75 or 0.37 s, for one FFT more per
  block read.
- *A oneshot `bufwrite` that starts with the song* (or any multiple of the
  time once around later) is read just behind where it writes: block p of a
  part is read in the sample after its last frame was written (with
  `follow` 1×). Input from two blocks of 4096 frames on, 186 ms, is convolved
  with the whole response as far as it is written; a note in the first
  186 ms misses the parts of the response that were not read yet when it
  passed. So a song can write a response of 3 s in its first 3 s and use it
  from the start. Written at another time, the blocks are read in another
  order than they are written, and the response is whole one time around
  after its last frame.
- *What a block read again changes.* The block is first computed with the
  spectra as they were. Then the blocks of the response that are read again
  get their new spectra, and what that changes in the output, the spectrum
  of the input for each such block times the difference of its spectra,
  transformed back, is added: at once, or with `fade` times a ramp from 0 to
  1 over the block (1.5, 11.6 or 93 ms), after which the output is that of
  the new response. Both cost the same, one FFT; only the ramp differs.
- A new note of a oneshot `bufwrite` starts the buffer from nothing: the
  blocks beyond what is written are silent when they are read again, so the
  old response goes as the new one comes.

Measured on a response that morphs fast, a decaying resonance moving two
octaves up and down twice a second, with a 220 Hz sine as input, the energy
above 3 kHz, where neither has anything, relative to the output:

| Response | `follow` | without `fade` | with `fade` |
|---|---|---|---|
| 4096 frames | 1× | -23 dB | -49 dB |
| 4096 frames | 8× | -34 dB | -51 dB |
| 512 frames | 1× | -39 dB | -55 dB |
| 512 frames | 8× | -55 dB | -55 dB |

So a response that moves needs `fade`; one that is written once, or again
with the same sound, does not. The lag stays: `fade` smooths each step, it
does not make the steps come sooner.

**Voices.** Each voice of the instrument has a convolution of its own, with
its own copy of the spectra of the response, like the `reverb` unit. The
state is 131 KB for a channel plus 16 bytes for each frame of the response
after the first 64 (2.2 MB for 3 s mono, 17 MB for 11.9 s stereo), in a
table of its own (`su_conv`, `$convWRK`, `GoSynth.convs`,
`Patch.NumConvolutions()`), in the order the units run. It is memory, not
data of the player.

**Encoding.** Operands: `gain`, `dry` (the optional last transformed
parameter: only in songs that use it, `optionalParams`), then the index of
the unit in `Bytecode.Convs`; the stereo bit. A `Conv` is the buffer, the
start, the length, the predelay, the channels, `follow` and `fade`; units
with the same share it. `Bytecode.ConvStates` are the units of the states in
the order they run. In the player a unit has 28 bytes of constants
(`su_conv_consts`, `wasmConv` in `compiler.go`): the header of its buffer,
start, length, the size of a channel of its state and the blocks of the
three parts; then the predelay, `follow` and `fade`, each only in songs
whose units use or differ in it. The buffers of the units get headers like
those of `bufread`, and their samples are encoded with those that are played
(`ffmpeg.PlayedBuffers`).

**Size.** Bytes that the unit adds to the wasm player of a song without it
(`TestConvolutionSizes`: an instrument writing the response, one convolved);
gzip -9 of the whole file.

| Song | Bytes | Code | Data | gzip |
|---|---|---|---|---|
| written buffer, 64 frames (no FFT) | +424 | +392 | +32 | +214 |
| written buffer, 3 s | +2078 | +2046 | +32 | +1070 |
| ... with `fade` | +2082 | +2050 | +32 | +1073 |
| ... with `fade` and `follow` | +2123 | +2087 | +36 | +1102 |
| ... with `fade`, `gain`, `dry`, `predelay` | +2154 | +2117 | +37 | +1124 |
| ... stereo, with `fade` and `gain` | +2122 | +2090 | +32 | +1104 |
| two units, one with `fade` | +2143 | +2072 | +71 | +1101 |
| sample, 64 frames (no FFT) | +533 | +477 | +56 | +303 |
| sample, 3 s | +2077 | +2021 | +56 | +1102 |
| a sample and a written buffer, 3 s | +2258 | +2171 | +87 | +1170 |
| written buffer, 3 s, in a song with spectral units | +1384 | +1352 | +32 | +657 |

About 700 bytes of the 2 KB (0.4 KB gzipped) are the FFT, its tables and
`$sinTurns`, which a song with spectral units has anyway. The rows with a
sample include the import and the loop that fills the buffer, which a song
with `bufread` has anyway. The length of the response costs nothing: it is
a number.

**Speed.** The time for a sample of a mono unit, on an Apple M3 Pro: a song
of 32 s with the unit against the same song without it
(`TestConvolutionSpeed`), the response in a written buffer unless noted.

| Response | wasm player (node) | Go synth | of a core, Go |
|---|---|---|---|
| 64 frames | 128 ns | 154 ns | 0.7 % |
| 512 frames | 167 ns | 226 ns | 1.0 % |
| 4096 frames | 188 ns | 318 ns | 1.4 % |
| 1 s | 259 ns | 432 ns | 1.9 % |
| 3 s | 250 ns | 448 ns | 2.0 % |
| 6 s | 272 ns | 494 ns | 2.2 % |
| 12 s | 292 ns | 571 ns | 2.5 % |
| 3 s, `follow` 8× | 452 ns | 886 ns | 3.9 % |
| 3 s, stereo | 562 ns | 911 ns | 4.0 % |
| 12 s, stereo, `follow` 8× | 1068 ns | 1988 ns | 8.8 % |
| 3 s, not read again (as a sample) | 124 ns | 236 ns | 1.0 % |

So beyond the first seconds, a second of response costs about 14 ns a
sample in the Go synth and 5 ns in the player (0.06 % and 0.02 % of a core):
the length hardly matters, as the long part works once in 4096 samples. Most
of the rest is the FFTs, and for a written buffer reading its first 64
frames every sample. The work of the long part comes at once, every 93 ms:
at most the time of the unit for those 4096 samples, 1.8 ms for 3 s mono in
the Go synth and 8 ms for 12 s stereo with `follow` 8×, which an audio
device with a small buffer may not forgive (not tried on one). In the Go
synth, setting the buffers or changing `buffer`, `start`, `length` or
`stereo` computes the FFTs of the whole response at once (not timed).

**Precision.** Against `scipy.signal.fftconvolve` in float64, a response of
3 s of decaying noise and 6 s of noise: the largest difference is 111 dB
below the peak. `TestConvolutionMatchesDirectSum` compares ten responses
from 40 to 26909 frames, with starts, predelays, mono and stereo, with the
convolution sum in float64: within 2·10⁻⁵ of the peak.

**The Convolution reverb module preset** (`tracker/modules/Convolution_reverb.yml`,
8 units): stereo in, wet out. It writes its own response: an `envelope`
with a curve, under stereo `noise`, through a low-pass and a high-pass
`filter`, into a stereo oneshot `bufwrite`; and a stereo `convolution` unit
with `fade` reads that buffer. The instrument it is in needs a note as long
as the decay, e.g. in the first row of the song: every note writes a new
response, with other noise.

| Parameter | Default | Sets |
|---|---|---|
| `decay` | 88 (2.1 s) | the time of the envelope, 0.20 to 5.9 s, and with it `length` of the unit |
| `tone` | 110 | the low-pass of the response, as `frequency` of `filter` |
| `lowcut` | 16 | its high-pass |
| `gain` | 28 (-22.5 dB) | `gain` of the unit: the level of the reverb |
| `predelay`, `dry` | 0 | those of the unit |

The level is set by measurement: `examples/convolution.yml` is
`examples/reverb_unit.yml` with this module in place of the `reverb` unit,
and with `gain` 28 its reverb is as loud as that one's in the rows after
the last note (-34.4 against -34.6 dB RMS), and 1.3 dB below it half a
second later. Compiled for wasm the example is 5030 bytes (2743 gzipped),
`examples/reverb_unit.yml` 3686 (2248).
A longer decay is louder, 3 dB for twice the time, as the response has more
energy. Left and right of the tail correlate by 0.08 (the `reverb` unit:
-0.37). The response has no early reflections and no change of colour over
time: it is noise under one envelope.

**In the tracker.**

- `buffer` is the menu of the audio buffers, `start` a time in the buffer,
  `length` and `predelay` times. The hint of `length` tells the frames and,
  when the buffer has less from `start`, how much it has. `follow` and
  `fade` are not shown for a sample, which is not read again.
- A warning names an instrument with a convolution unit without a buffer,
  and tells when the convolution units of the song together take more than
  an eighth of a processor core, estimated from the table above with the
  voices of their instruments.
- A preset that reads a response it writes itself brings its buffer: when a
  `convolution` unit and a `bufwrite` unit of an instrument or a module
  refer to a buffer that the song does not have, the song gets it
  (`fixConvolutionBuffers`), stereo if the `bufwrite` is, as long as the
  longest response of the unit (of a unit of a module: the longest that the
  parameter of the module gives it), and loses it with the last unit that
  uses it. Loaded into a song where the ID is taken, the units get an ID of
  their own (`assignBuses`). Two module units of one module get a buffer
  each when the song is compiled or played with `sointu-play`
  (`Song.Expand`); in the tracker the second has no audio to write to and
  should be silent (not tried).
- The Buffers tab shows the response as it is written, like any written
  buffer. The row of the unit has no plot of it.
- The buffer that a unit reads and another instrument writes has to be on
  the same thread, like that of `bufread`; the stages of the web runtime
  keep such instruments together.

**Tests.** `vm/convolution_internal_test.go` (against the sum; gain and dry;
a response written from the first sample; a part changed while playing,
with and without `fade` and `follow`; the first 64 frames following every
sample; the blocks of each length; `BenchmarkConvolution`),
`vm/compiler/wasm_convolution_test.go` (the wasm player renders 13 songs
with written responses and 3 with samples exactly like the Go synth, and
has the code of the parts each uses and no others; the example; stages; a
song without the unit has nothing of it, and x86 refuses it; sizes and
speed), `tracker/convolution_test.go` (the buffer of the preset, the
warnings, the hints) and `TestConvolutionRow` in `tracker/gioui`
(`SOINTU_TEST_SCREENSHOTS=<dir> go test ./tracker/gioui -run ConvolutionRow`).
The songs of `tests/` and `examples/` that were there compile to the same
bytes as before for wasm, 386 and amd64.

**Not done.** True stereo. Normalizing the response. A plot of the response
in the row of the unit. A real FFT. Spreading the work of the long part
over its 4096 samples, which would need a longer first part. An instrument
preset: the module preset is the reverb, to put between an `in` and an
`out`. The player was not tried in a browser, only under node.

## Automation

What is automated in the plugin host ends up in the compiled song. The
plugins show the host 64 parameters, the automation slots A1 to A64. A slot
is bound to a parameter of a unit and offsets it, in steps of that
parameter, on top of the value that the unit has, which is never changed.
While the host plays, its values move the Go synth at their sample; a
recording in the plugin turns them into points of a lane in the song, with
the notes; and the song plays the lanes when it is played in the tracker and
when it is compiled. Go synth and wasm player only.

Why slots, and why recording: a CLAP or VST2 plugin cannot read the
automation lanes of its host. It is only given values while the host plays
(`CLAP_EVENT_PARAM_VALUE`, sample accurate; VST2 `setParameter`, between
blocks), as densely as the host likes. And the host keeps its lanes by the
ID of a parameter, so the IDs must not depend on the patch: a parameter is a
slot, whatever the slot is bound to.

**In the song.** `Song.Automation`, optional, a lane for each slot in use:

```yaml
automation:
    - slot: 0            # A1
      name: Bass cutoff  # what the host shows; optional
      unit: 4            # the ID of a unit of an instrument
      param: frequency
      points: [[0, 0], [32, 40], [48, 70], [48, 10], [64, 30]]
```

A point is a row of the song, from its start and with a fraction, and an
offset in steps of the parameter, -128 to 128, with a fraction. Between two
points the value is linear, before the first it is that of the first, after
the last that of the last; two points at one row are a step. Rows, so the
lanes follow the tempo with the score. Any parameter that a send can
modulate can have a lane (`CanAutomate`), also `p1` to `p8` of a module
unit; not the units inside a module. A lane without `unit` is bound to
nothing and played only by `automation` units placed by hand.

**Expansion.** `Song.Expand` puts two units before every unit that a lane is
bound to: an `automation` unit with the slot, and a `send` with the amount
1 to the port of the parameter. So the lane is added the way every
modulation is, `value/128 + port`: no unit is needed to add it, and the send
is the code that the player has anyway. A send to a module unit becomes
sends to what is bound to that parameter of the module, as any send does
(with the coarse scale of a scaled binding). After the modules are expanded,
every `automation` unit, also those placed by hand in an instrument or a
module, gets the points of the lane of its slot as `VarArgs`: the time in
samples and the value in 1/128 steps of each. The two units count towards
the 63 of the instrument. `Expansion.Problems` lists lanes bound to units
that are missing or to parameters without a port, and slots with two lanes.

**The unit.** `automation` has one parameter, `slot`, and pushes the value
of the lane over 128. `Bytecode.Automation` is the table of the lanes, the
same bytes in the Go synth and the wasm player (`su_automation`):

```
value            the value before the first record
time, value      a record: the value reached that long after the record before
...
0                the end: the last value stays
```

Times are 16 bits in units of 16 samples (0.36 ms), never 0: a step takes
one unit, a gap of more than 23.8 s gets a record on its way. Values are
16 bits in 1/128 steps; when every value of the song is a whole step from
-128 to 127 they are 8 bits in steps (`AutomationFine`). The operand of the
unit is the offset of its lane, 2 bytes; units with the same slot and
points share a lane. The state of the unit is the offset of its record and
the sample at which the record before it was reached, in the unit: when a
note clears it, the unit steps through its records up to the sample of the
song again, so nothing is kept outside the voices and a stage of the
pipeline needs nothing from the others. The value is
`(v0 + (v1 - v0)·t/dt)·scale` in float32, the same operations in both
synths: no sample differs. The time is the count of samples since the start
of the song, so not for songs with the `speed` unit, whose rows have other
lengths.

**Conditional on use.** A lane without a point other than 0 adds exactly
nothing and is not compiled (`Automation.Active`), so a slot that is bound
but not recorded costs nothing. Songs without lanes compile to the same
bytes as before, for wasm, 386 and amd64 (all songs of `tests` and
`examples`, 1236 files, also with `-js` and `-js -stages 4`). x86 has a stub
and the compiler refuses the unit.

**Size.** Bytes of the wasm player of `examples/automation.yml`, against
the same song without lanes (1842 bytes, 1144 gzipped;
`TestAutomationSizes`):

| Song | Total | Code | Data | gzip |
|---|---|---|---|---|
| 1 lane, 5 points, whole steps | +235 | +210 | +25 | +149 |
| the same, in a song that has a `send` already | +170 | +147 | +23 | +108 |
| 3 lanes, 9 points, whole steps | +267 | +213 | +54 | +174 |
| 4 lanes, 16 points, one with fractions (16 bits) | +312 | +213 | +99 | +196 |
| 1 lane, 64 points of a curve, 16 bits | +476 | +210 | +266 | +350 |
| 1 lane, 64 points of a curve, whole steps | +412 | +210 | +202 | +269 |

So 147 bytes of code once (63 more for the `send` in a song without one),
about 10 bytes for each lane (two opcodes, five operands, the first value,
the end) and 3 bytes for each point, 4 with 16 bits; gzipped about 2 and 3
a point.

**What 8 and 16 bits change.** Only the points are rounded; between them the
value is a float, so a ramp is smooth either way. With whole steps a point
is where the knob could be: within 0.5 step, which is 1.5 % of a filter
frequency at 2 kHz and 6 % at 100 Hz, 0.13 dB of a gain at half level. What
is lost is what is smaller than a step: `transpose` is a semitone a step,
so a vibrato or a bend below half a semitone is gone, and so are fine moves
of a detune, a delay time or a position in a buffer. What a host records
has fractions, so it is 16 bits unless the points are rounded.

**In the tracker and the plugins.**

- Edit → Automate parameter binds the parameter under the cursor to the
  first free slot, or removes its lane (`AutomationModel`). The hint of an
  automated parameter starts with the slot and what the host gives it now:
  `A3 +12.5: 686 Hz`. A lane goes with its unit when the unit is deleted.
- The plugins have a parameter for each slot, -128 to 128 with the default
  0, named by the slot and what it is bound to (`A3 Bass: filter
  frequency`), or by the `name` of its lane alone. With an automated
  parameter under the cursor, the toolbar under the units has the slot and
  a field for that name, which shows the name by the binding while it is
  empty (at most 64 bytes, one line; a host that takes VST2 names as they
  are specified shows 24). CLAP rescans the names when they change, and takes the
  modulation of the host (`CLAP_EVENT_PARAM_MOD`) on top of the value. In
  VST2 the range is 0 to 1 with 0.5 in the middle, and a value takes effect
  at the start of the next block. The vendored vst2 has `GetNameFunc`, and
  24 bytes for the name of a parameter instead of 8. In the Audio Unit the
  parameters are global, with the slot as the id; `ScheduleParameters`
  gives values at their frames (a ramp in steps of 32 frames, ending on its
  last frame), `SetParameter`, from any thread, at the start of the next
  render. A change of the names is told as a change of `ParameterList` and
  `ParameterInfo`, the values of a loaded state with
  `AUParameterListenerNotify`, on the main thread.
- A value of the host reaches the player with its frame, like a note
  (`Player.Automate`), and the Go synth at that sample
  (`sointu.Automator`), where it is reached in a ramp of 128 samples
  against steps in the sound. It is not a change of the song: no undo step,
  and the project of the host is not marked as changed. The values are
  saved with the state of the plugin and restored.
- While the song is played in the tracker, the `automation` units read
  their lanes, at the row that is played, also after a loop or a jump;
  otherwise they push the values of the host.
- Recording (the record button of the plugin) records the values with the
  notes, from the first note on, with the values that the slots had then.
  The values of a slot are joined by lines, with a step where the slot was
  left alone for more than 0.1 s, and reduced to the fewest points within
  0.1 step of them (`Recording.Lanes`, `ReduceAutomation`: Ramer, Douglas
  and Peucker). The lanes keep their bindings; slots that are bound to
  nothing and were given values get a lane too.

**Tests.** `automation_test.go` (expansion, also through a module and for
units placed by hand, problems, YAML and JSON, reducing),
`vm/automation_test.go` (the records, the values at their samples, a note
triggered on the way, the song going back, a changed table, live values),
`vm/compiler/wasm_automation_test.go` (the wasm player renders a song with
four kinds of lanes and `examples/automation.yml` exactly like the Go synth,
with 8 and 16 bits, in parts and cut into stages at every voice; lanes that
add nothing compile to the player of the song without them for wasm, 386
and amd64; x86 refuses; sizes), `tracker/automation_test.go` (binding and
undo, the hint, naming a slot, a lane going with its unit, a live value at its frame,
recording a ramp, a hold and a step, playing the lanes, a change of the
tempo, the values saved and restored, the remote tools) and
`cmd/plugin/plugin_test.go` (a value of the host at its frame at 44100 and
48000 Hz, the modulation), and `tracker/gioui/automation_test.go` (the field
for the name, laid out without a window). **Not verified:** anything in a host. The
plugins build; what Bitwig and Live send, and how they show the parameters,
has not been seen.

**Not done.** A list of the lanes and a plot of a lane in the tracker, and
editing points there (the MCP tools read and set them); turning a knob in
the tracker does not write automation in the host, as the knob is not the
parameter; importing the envelopes of a Live set (`.als`) or MIDI
controllers; lanes together with the `speed` unit; x86. The parameters
of the Audio Unit were tried only by calling it directly, not by auval or a
host.

## Bandlimited oscillators

The `oscillator` has a new parameter, `bandlimit` (0 or 1, not modulatable;
0 in older songs). With 1, sine, trisaw and pulse oscillators that are not
LFOs correct their jumps with a 2-sample polyBLEP and their corners with
polyBLAMP, in the Go synth and the wasm player (not x86):

- **Phase advance.** dt = |ω + frequency modulation + change of the phase
  parameter since the last sample|, kept between 2^-20 and 0.5, for each
  unison voice and channel. The phase parameter of the last sample is kept
  in the unit's port 7, which the oscillator has no input for. On the first
  sample of a note it is 0, as if the phase had moved from 0.
- **Corrections**, for a discontinuity at phase 0 at distance d = min(t,
  1−t), y = max(1 − d/dt, 0): polyBLEP ∓y² (after/before) for a step of +2,
  polyBLAMP y³·dt/6 per unit of slope change.
- **pulse:** color kept in [0, 1]; +polyBLEP at 0, −polyBLEP at color.
- **trisaw:** color kept in [dt, 1−dt], so the slope change 2/(c(1−c)) times
  dt stays at most 4; a saw's jump becomes a one-sample ramp. +polyBLAMP at 0,
  −polyBLAMP at color.
- **sine:** color kept in [dt, 1]; slope change 2π/color at 0 and at color.
- The waveshaper after the waveform still aliases.

**Encoding.** The type bits of the flags byte are one-hot, and gate (0x04)
never goes with sine (0x40), trisaw (0x20) or pulse (0x10). So 0x04 together
with one of them means bandlimited: no extra bytes at all. A gate is then
flags & 0x74 == 0x04. The compiler sets the bit only where bandlimit has an
effect, and the wasm player's correction code (of each waveform only when
a bandlimited oscillator has it), the dt computation and the stricter gate
test are included only when some oscillator uses it.

## Envelope curve

The `envelope` has a `curve` parameter, 0 to 128, modulatable, in the Go synth
and the wasm player. 0, the value old songs get, is the linear envelope as
before. Above 0, with c = 12·curve² (curve from 0 to 1, 12 at 128), a stage
from `start` to `end` (0 to 1, 1 to sustain, or the level where the release
starts to 0) moves each sample as

    target = end + (end - start)/(2^c - 1)
    level += (target - level)·(1 - 2^(-c·delta/|end - start|))

where delta is the rate of the linear stage, 2^(-24·p) per sample for the
parameter p. The target lies beyond the end: 2^c/(2^c - 1) of the stage away
from the level at the start, 1/(2^c - 1) at the end, so the distance to it
shrinks by 2^(-c) over the stage, and by 2^(-c·delta/|end - start|) per
sample. The stage therefore takes |end - start|/delta samples, exactly as
long as the linear one. As c grows, the target comes closer and the stage
bends more: attack fast then slow, decay and release fast then slow. As c goes
to 0, the target goes to infinity and the stage becomes linear; below
c = 2^-20 it is computed as linear. The state machine is unchanged, except
that the state keeps the level where the release starts.

2^x - 1 is computed with `exp2m1f` (`$exp2m1f`): for a slow stage,
1 - 2^(-x) is below the precision of `1 - exp2f(-x)`, which would stop the
stage.

The curve is one-directional: an opposite curvature below a linear 64 made
the wasm player 9 bytes larger (5 gzipped) and needed a clamp, as a strongly
opposite curve puts the target on the start, where the stage never moves.

**Encoding.** `curve` is the last transformed parameter of the envelope,
after `gain`. `NecessaryFeatures.TransformCount` counts it only when an
envelope of the song has a curve other than 0 or something modulates it
(`optionalParams` in `vm/featureset.go`); the bytecode (`defOperands`) and
the transform count tables of the players follow it, and the wasm player
leaves out the curved envelope. So songs with linear envelopes compile to
exactly the same players as before, on every target. Otherwise every
envelope of the song has the curve operand. The Go synth, which encodes with
`AllFeatures`, always has it, as does the x86 library.

## Voices

- Up to 255 voices in the Go synth and wasm player (`vm.MAX_VOICES`). Patches
  of 32 voices or fewer compile to exactly the same code as before.
- Above 32 voices (`Bytecode.WideVoices`):
  - send addresses are 3 bytes, with the global flag in bit 23 instead of 15;
  - the players use per-voice tables (`Bytecode.Polyphony`,
    `SongMacros.VoiceTracks`) instead of the polyphony and voice-track
    bitmasks;
  - the voice memory is sized by the number of voices.

## Velocity

The `velocity` unit pushes the velocity of the note of its voice, 0 to 1,
mono or stereo, for sends: to a `gain` for dynamics, to a filter or a
module parameter for brightness.

- *Where it comes from.* The player passes the velocity of a MIDI note on
  (`NoteEvent.Velocity`) to synths that have `sointu.VelocityTriggerer`
  (`TriggerVelocity(voice, note, velocity)`): the Go synth and the
  multithreaded one. `Trigger` is velocity 127, and so are notes of the
  score, notes played from the keyboard of the tracker and synths without
  the interface. The plugins already send 1 for a note on of velocity 0
  (CLAP, VST3, AU); the player now takes a note on of velocity 0 from any
  other source (VST2, MIDI input of the tracker) as a note off, as MIDI has
  it, which it took as a note on before.
- *In the voice.* The Go synth keeps 1 - velocity (`voice.quiet`), so that
  a voice that is reset or never triggered, and so every path that makes a
  new voice, has velocity 1. A voice that a `spawn` unit triggers has the
  velocity of the voice that spawned it.
- *Compiled songs* have no velocities: the wasm and x86 players push 1
  (`f32.const 1`, `fld1`), so a song renders in them as in the Go synth
  (`TestVelocityWasmMatchesGoSynth`, no sample differs). The x86 code
  compiles, but was not assembled here (no nasm).
- *MCP:* `render_note` and `play_note` take `velocity`, 1 to 127.

Tests: `vm/velocity_test.go` (the values, 1 without a velocity, after a
release, for spawned voices), `vm/compiler/wasm_velocity_test.go`,
`cmd/plugin/velocity_test.go` (MIDI note ons of several velocities, and of
velocity 0, through the player).

## Output channels

- 16 output channels in the Go synth and the wasm player
  (`sointu.NumChannels`): left and right, and seven aux pairs instead of
  three. `aux` and `in` take a `channel` up to 14 (aux7 left; a stereo unit
  uses its channel and the next), named aux4 left to aux7 right after the
  first eight. `out` and `outaux` are as they were: 0/1, and 0/1 with 2/3.
- The Go synth always has the 16 (`synthState.outputs`). The wasm player has
  them only in a song where an enabled `aux` or `in` unit, after the modules
  are expanded, reaches a channel above 7 (`Patch.MaxChannel`,
  `wasmUnitFeatures.WideAux`): `su_globalports` is then 64 bytes instead of
  32, and the voices start 32 bytes later, at `su_synth` + 96. The addresses
  of global sends, which count from 64 bytes before the voices, take that
  constant from the place of the voices; nothing else in the player knows
  the difference, and the bytecode is the same. A stereo unit on channel 7
  uses channel 8 and counts.
- A song that uses no channel above 7 compiles to exactly the player it
  compiled to before. One that does costs no bytes of wasm in the songs
  measured (`examples/soundset_loop.yml`, 6151 bytes with its drum bus on 6/7
  and on 8/9; `examples/ducking.yml` and others with their channels moved):
  the code is the same, with other addresses, and the 32 bytes of ports are
  memory, not data. The voices are followed by an alignment to 128 bytes
  that takes the 32 bytes up, so nothing after them moves.
- The stages (`-js -stages`) know both layouts: the cells of the ports and
  of the voices are where the player has them
  (`vm/compiler/wasm_stages.go`), and a channel above 7 is cut like any aux
  channel.
- The x86 players and the native bridge keep 8 channels: the compiler refuses
  a song that uses a channel above 7 for 386 and amd64, and the bridge
  refuses the patch. See [Updating the x86 backend](#updating-the-x86-backend).
- The tracker shows the names; it has no meter or check of its own that
  counts the channels.
- Tests: `vm/compiler/wasm_aux16_test.go` (each channel is one of its own in
  the Go synth; mono and stereo `aux` and `in` on every new pair, in a
  polyphonic instrument, with global sends and more than 32 voices, rendered
  alike by both synths, in parts and in stages; the layout only when used,
  and the x86 refusal), cuts with channels above 7 in `TestStageCuts`, and
  `channels_test.go`.

## Go synth behavior changes

The Go synth now computes every unit the way the wasm player does, operation
by operation, in float32:

- **Shared math.** `exp2f`, `log2f`, `powf` and `sinTurns` (sin(2π·t), t in
  turns) are float32 routines in `vm/mathf.go`. The wasm player has the same
  ones (`$exp2f`, `$log2f`, `$powf`, `$sinTurns` in `patch.wat`). They
  replace `math.Exp2`, `math.Pow` and `math.Sin`. `exp2m1f` (`$exp2m1f`),
  2^y - 1, serves the curved envelope.
- **Oscillator.** The phase is float32 instead of float64, as in the wasm
  player. The gate state moves with unison and stereo like in the wasm
  player.
- **Envelope.** Decay ends in a sustain state that holds the level, instead of
  following a modulated sustain. See also [Envelope curve](#envelope-curve).
- **belleq.** It uses the wasm player's form of the biquad, including
  cos(ω) = √(1−sin²ω).
- **delay.** The damping is computed as (state−s)·damp + s, and note tracking
  uses `exp2f`.
- **speed.** The time step is truncated instead of rounded, as in the wasm
  player.
- **pan.** A mono pan is s·p and s − s·p, unless the patch has stereo pans;
  the wasm player's code depends on that (`Bytecode.StereoPan`).
- **waveshape.** It matches the wasm and x86 waveshapers and clips its input.
- **crush.** It rounds halves to even, like `f32.nearest` of the wasm player
  and the x87; `math.Round` differed by a step on samples exactly between
  two steps.
- **Rounding.** Products that are added are rounded with `float32()` first.
  Go may otherwise fuse them into multiply-adds on arm64, even across
  statements, which the wasm player never does.
- **Sends.** Sends to units after the 31st unit of an instrument now reach the
  right unit; before, Go decoded the unit index from 5 bits instead of 6.

These change the Go synth's sound by tiny amounts, mostly below 1e-5.

### State across changes of the patch

When the tracker changes the patch while the synth plays (`GoSynth.Update`),
the units that are still there keep their state, in every voice
(`vm/carry.go`). Before, whenever the opcodes changed, e.g. a unit was
added, the units of every voice started from nothing: envelopes began
again, filters and compressors fell silent, and the delay lines, which are
kept in the order the units run, belonged to other units than before. Only
the Go synth does this; the compiled players have one patch.

- What a unit keeps: its 8 floats of state and its 8 ports in each voice,
  and its delay lines, `ott`, `limiter` and `reverb` states. Spectral units and
  mc units already kept theirs, by their buffers.
- **Instruments** are matched by the units they share, by ID and type, in
  their order (the longest common sequence, weighted by the shared units).
  An instrument that shares no unit with any is matched with the one in its
  place, if the patch has as many instruments as before: so patches without
  IDs work too. The voices of an instrument are matched in their order, and
  keep their note with their units; voices that are new are silent.
- **Units** of matched instruments are aligned in their order: units of the
  same type can be matched, and of all such alignments the one with the most
  matches wins, a match of units with the same ID counting as four. So IDs
  decide where there are any; units without IDs (those that an eq unit
  stands for) and units whose IDs changed (the copies made for module units
  get new ones when a unit is added) are matched by type. A unit left over
  that has the ID and the type of a unit left over in the old instrument
  was moved, and is matched too.
- A unit of another type than before, or a new one, starts from nothing; so
  does a delay unit with another number of delay lines.
- If nothing moved (only parameters changed, or IDs), nothing is copied.
  Otherwise the voices are copied (1 MB) and the tables that changed are
  built anew, which for delay lines is 256 KB each: a few milliseconds for a
  patch with many, once per change.

Tests (`vm/carry_test.go`): after a unit that leaves the signal as it is
(a gain of 1) was added, removed, moved or disabled, with and without IDs,
the render is exactly that of a synth that had the new patch all along, and
an update that changes nothing leaves the render as it was; an instrument
goes on exactly as it would have alone when instruments and voices before
it come and go, are swapped, or lose a delay; a unit whose type changed
starts like a unit added.

### Taps

`sointu.Tapper` (`vm/tap.go`): the Go synth, and the multithread synth
around it, can record the signal at any place of the patch, for whatever
shows it. It is meant for meters on every unit; so far the eq editor and
the analysis panels of the rack (see [Tracker](#tracker)) use it.

- A place is a `TapPoint{Instrument, Unit, Voice}`: before the unit with
  that index among the units of the instrument, as the patch that the synth
  got has them (disabled units count; the synth knows which it runs). That
  is after the unit before it. `Voice` is a voice of the instrument, from 1,
  or 0 for the sum of all its voices.
- `SetTaps(points)` sets the places; `Tapped(i, dst)` returns the frames
  that tap i recorded since they were last taken, one for every frame
  rendered: the two signals on top of the stack there, the top one first,
  which of a stereo signal is the left one. Of a mono signal only the first
  is the signal; the synth does not know which it is, the tracker does. A
  tap that nobody takes from keeps the latest 65536 frames at most.
- Cost: `Render` asks `s.taps != nil` before every unit and after every
  frame, and nothing else while there are no taps: no difference to be
  measured (8.1 to 9.0 ms for 0.1 s of 8 voices of 8 units, with two taps
  and without). With taps, each unit run is compared with each tap.
- After `Update` the places are those of the new patch; the tracker sets
  them again when the units before them changed.

In the tracker (`tracker/taps.go`):

1. Whoever shows a signal asks the model for it every time it is drawn:
   `Model.watchTap(tapKey{Unit, After}, point)`. The key says what is
   watched (the signal before or after the unit with that ID, as it is
   played), the point where that is now; `Expansion.EQs` and `playedID`
   give it for an eq unit, also for the copy played for a module unit.
2. The model tells the player the places of all watches (`TapsMsg`), also
   when one moved; the player sets them in the synth, and again when it
   makes a new synth.
3. After every buffer, the player takes what each tap recorded and sends it
   to the model (`TapAudio`, in a buffer of the broker's pool), which gives
   it to the watches of that place: `tapSpectrum`, the smoothed spectrum of
   the windows it has got (`specAnalyzer`, as for the master).
4. A watch that was not asked for in a second ends; with the last one the
   player sets no taps, and the synth records nothing.

The analysis panels find the place after any unit with
`Model.analysisPoint`: after the units it stands for in the expanded song,
by `Expansion.EQs` for an eq unit, `Expansion.Spans` for a module unit (and
a send to one, which becomes several sends), else the index of its ID (of
its copy, for an inner unit) in `Model.expanded`. A `tapWatch` of a panel
has a `tapScope` (a ring of frames, `RingBuffer` as the oscilloscope of the
song) next to `spectrum`, whose `channels` are the spectrum of each
channel. A meter on every unit would be the same with a level. What is not
there yet: one place per watch, so every unit of a rack is as many taps,
each compared with every unit run, which a table by unit would make cheap;
the native synth and the compiled players have no taps.

Tests: `vm/tap_test.go` (before and after a unit, one voice and all, stereo,
the multithread synth, disabled units, after an update, the render as
without taps) and `tracker/taps_test.go` (noise through an eq, with a model,
a player and the Go synth: the spectrum after the eq is that before it
moved by the curve of the editor, within 1.5 dB over three ranges; the
taps follow the eq when its units change, also in a module; a second after
the last time they were asked for, the synth records nothing).

## Sample rate

The synth runs at 44100 Hz only. Where the audio device or the plugin host
runs at another rate, the output of the player is resampled to it
(`resample`); at 44100 Hz nothing touches the frames. Before, the plugins
played too fast and too high at any other rate, with a warning, and the
tracker left the conversion to the system.

**The resampler** (`resample.Resampler`) is a windowed sinc kernel for any
pair of rates: a low-pass at half the slower rate, 36 frames of the slower
rate to each side, Kaiser window with β 10.06, evaluated where each output
frame lies between the input frames. The positions are whole numbers of
1/out of an input frame, so nothing drifts. With up to 2048 different
positions (48000 Hz has 160, 96000 Hz 320, 192000 Hz 640, 88200 Hz 2) each
has its own row of a table; other rates (47999 Hz) have 1024 rows and
interpolate between two. Every row is scaled to pass DC as it is.

| | 44100 → 48000, 88200, 96000, 176400, 192000 Hz | → 32000 Hz | → 22050 Hz |
|---|---|---|---|
| Level within 0.0001 dB up to | 20 kHz | 14.5 kHz | 10 kHz |
| -0.36 dB at | 21 kHz | 15.2 kHz | 10.5 kHz |
| -6 dB at | 22.05 kHz | 16 kHz | 11.025 kHz |
| Below -99 dB from | 24.1 kHz | 17.5 kHz | 12.05 kHz |
| Latency, frames of the output | 40, 72, 79, 144, 157 | 37 | 36 |
| Latency | 0.82 to 0.83 ms | 1.16 ms | 1.63 ms |
| One core, playing in real time (M3 Pro) | 0.17 % at 48000, 0.34 % at 96000, 0.72 % at 192000 | | 0.24 % |

- Measured (`resample_test.go`): tones up to the end of the passband keep
  their level within 0.0001 dB, and everything else in the output (images,
  aliases, rounding) is 98.9 dB or more below the tone; the images of such
  tones, fitted alone, are below -99 dB; the output is the input 40 frames
  (at 48000 Hz) late within 0.00001. With numpy, 12 tones from 41 Hz to
  19.9 kHz at once: no other bin above -104.9 dB at 48000, 96000 and
  192000 Hz. Interpolated kernels (47999, 50000 Hz) measure the same, at
  twice the time.
- The transition lies around half the slower rate, not below it. Going up,
  what the synth has between 20 and 22.05 kHz leaves images between 22.05
  and 24.1 kHz, which no one hears. Going down to 32000 or 22050 Hz, tones
  in the upper 9 % below half the output rate come with aliases.
- A signal at full scale can exceed it after resampling, as its peaks
  between the samples show: a square wave at full scale peaks 2.2 dB above
  it at 48000 Hz.
- The output is the input `Latency` output frames late, exactly, whatever
  the sizes of the blocks, and after h output frames the resampler has
  taken ⌈h·in/out⌉ input frames: `Need(n)` tells how many the next n take.
  The output does not depend on the blocks, to the bit.

**Plugins** (`cmd/plugin`, `output`). For a block of n frames the player
renders `Need(n)` frames, n·44100/rate within one, which are resampled into
the block.

- The host tells its rate when it activates the plugin (CLAP), in
  `setupProcessing` (VST3), or with
  `effSetSampleRate` (VST2, `Dispatcher.SetSampleRateFunc`, added to the
  vendored vst2); the VST2 plugin also asks the time info every 2 seconds,
  as before. The resampler is made there, not in the audio thread, which
  takes it over at its next block.
- The latency is told to the host: the CLAP latency extension (it changes
  only in `activate`), and `initialDelay` with `audioMasterIOChanged` for
  VST2 (`Host.SetInitialDelay`, added to the vendored vst2), and
  `getLatencySamples` with `kLatencyChanged` for VST3. 0 at 44100 Hz.
- A MIDI event at frame d of a block is played at frame `Need(d)` of the
  player's block: the first frame of the synth at or after its time. So a
  note is heard at its frame plus the latency, up to one frame of the synth
  (23 µs) late, never early, and the same wherever the host cuts its blocks
  (`TestNoteTiming`, `TestBlockSizes`: blocks of 0 to 4096 frames give the
  same output to the bit, with notes on the first and last frames of
  blocks).
- The clock of the player counts frames of the synth, as the tracker's
  keyboard and MIDI inputs assume (44100 a second), and recordings, whose
  rows are computed from it, are right at any rate. The tempo of the host is
  in beats per minute and needs no conversion; the plugins use nothing else
  of the host's transport. They have no audio input.
- The alert tells the rate and the latency (info), in place of the error
  that the rate was not supported.

**Tracker and sointu-play** (`oto`). What oto does when it is asked for
44100 Hz on a device that runs at another rate, in the version used here:
on macOS the AudioQueue converts, on Windows WASAPI in shared mode (auto
convert, default quality) or WinMM, on Linux ALSA's `plug` devices and the
sound servers (PipeWire, PulseAudio). Only an ALSA device without any of
those takes the nearest rate it has, which oto does not look at: that plays
too fast. So `oto.NewContext(0)` asks for the rate the device runs at
(`DeviceSampleRate`), opens it at that rate and resamples itself:

- macOS: the nominal rate of the default output device (CoreAudio).
- Windows: the rate of the mix format of the default device (WASAPI).
- Linux: the rate ALSA gives for 44100 Hz with its own resampling off, on
  the device oto will open: 48000 for a `dmix` or a card at 48000 Hz, 44100
  behind a sound server, which then resamples as before.
- Where the system tells nothing, 44100 Hz as before.

`-samplerate` of `sointu-track` and `sointu-play` sets the rate instead
(`-samplerate 44100` is how it was). The rate is read once, at the start;
if the device changes later, the system converts from the rate chosen.
The buffer of the player is as long in time as before. The tracker tells
the rate in an alert when it resamples.

Everything the tracker shows and measures is the signal of the synth at
44100 Hz, before the resampler: the player sends its own frames to the
model (oscilloscope, spectrum, loudness and peaks), and the taps are in the
synth. Files that `sointu-play` writes are 44100 Hz.

**Not verified here:** the Windows query was only compiled; the Linux one
ran in a container against ALSA devices with fixed rates, not on hardware;
no host was run with the plugins.

## Tracker

- **Buffers tab:** import samples, encoding presets and formats, preview,
  writable buffers for recording (length, clear, fit to recording), a
  waveform with the write head and where `bufread` notes play, and live
  spectra of spectrum buffers.
- **Sample browser:** a panel at the right of the Buffers tab
  (`sample_browser.go`, `tracker/browser.go`), shown and hidden by a button
  of the tab (`browser.shown` in `preferences.yml`): a tree of the folders
  and the audio files that the loader reads, folders first, hidden files
  left out. Up and down move through the rows, and a selected file plays at
  once, its first 30 s in stereo, as the preview of a buffer plays; a
  folder, Escape or hiding the panel stops it. With the switch of the
  header off (`browser.autopreview`) a file plays only by the play button. Right expands a folder, or
  goes into it; left collapses it, or goes to the folder above, which at the
  top becomes the root (Backspace too). The filter field above the rows
  keeps those whose names contain it, ignoring case, among the root and the
  expanded folders (it searches no further), with what is in a folder that
  matches and the folders the rows kept are in; Enter goes to the rows,
  Escape clears it. Enter or a double click loads the
  file into the buffer that was selected, as Replace does, one undo step,
  or as a new buffer if there are none or it is a spectrum or a bus. The
  roots offered: the home folder, the last folder (of the file last loaded
  into a buffer, by the browser, the dialog or a drop; `browser.lastfolder`,
  where the browser starts) and the folder of the song; a button opens the
  folder panel of the system (macOS only, `folderpicker_darwin.go`; it
  floats, as the plugin window does).
  ffmpeg decodes a preview on a goroutine, reading the file in place
  (`FFmpeg.DecodeFile`, to a pipe); selecting another file kills it, and a
  late result is dropped by its number. The last 8 decodes are kept, each
  with the time and size of its file: a file that changed is decoded again.
  The folders shown, the root and the expanded ones, are watched: a
  goroutine stats them every second, and when one has changed (a file was
  added, removed or renamed in it) they are read again. No fsnotify: a
  poll of a few folders costs nothing and works on every platform. A
  preview of the browser and one of a buffer share the player's preview:
  one stops the other.
- **Drag and drop:** audio files dropped onto the window from the Finder
  (macOS only, `app.FileDropEvent`; `filedrop.go`) onto the properties of
  the Buffers tab replace the sample of the selected buffer with the first
  and add the rest; dropped onto the list of the buffers, or anywhere else,
  they become new buffers in their order, and the Buffers tab shows. What
  is under the drop is what a press there would reach in the last frame
  (`FileDropEvent.Targets`). Other files are left out with an alert; a
  folder dropped alone becomes the root of the browser. In the plugin
  window too, which is the same Gio window.
- **Samples in presets:** an instrument preset or file, a module preset and
  instruments, units and modules on the clipboard carry the sample buffers
  their units read (`presetSamples`, `tracker/preset_samples.go`): each
  buffer as the song stores it, its encoded file, slices and channels, and
  the encoding presets of the song that the samples use. The IDs are found
  in the buffer parameters of bufread, convolution and bufwrite units, in
  the parameters of module units bound to them, through any depth of
  modules, and in the defaults of those parameters. Loading gives each
  buffer an ID that neither the song nor the loaded units use, and points
  the units, module units and defaults at it, before the modules are
  imported, so that a module the song has matches; a buffer of the song
  with the same file, channels and slices is used instead of a copy. A
  preset that the song lacks comes along; one it has with another encoding
  becomes the sample's own encoding. Written buffers, spectra and buses are
  not carried: the tracker makes them as before (fixConvolutionBuffers,
  assignBuses). The MCP server's presets are the same. Old presets have no
  buffers and load as before.
- **Plots:** zoom with Alt+scroll, pan by dragging or horizontal scrolling,
  Alt+drag to zoom y, double-click or right-click to reset, bounded zoom out.
- **Units:** spawn targets follow instruments when they move or are pasted;
  new spectral units pick up the spectrum above them.
- **Oscillator cycle:** an oscillator shows one cycle of its waveform after
  its parameters, 3 cells wide (`tracker.OscillatorInlineCells`; the comment
  follows it): its type, color, shape, phase, gain, unison voices, gate
  pattern and bandlimit, as played (for an inner unit, with the values of
  its bound parameters), without modulation, from -1 to 1 or its peak, with
  a line at 0. `vm.OscillatorCycle` computes it with the waveform functions
  of the Go synth (`oscillatorWave`, `oscillatorGate`, `oscillatorFlags`),
  512 points, as if a cycle were 512 samples for the corrections of
  bandlimit. Left out: the detune of the unison voices and channels, which
  over one cycle are all but in phase, and the smoothing of a gate, which
  only a slow gate does not hide. An LFO shows the same cycle. A sample
  oscillator shows nothing. The model keeps the cycles by the parameters
  they depend on (`UnitModel.OscillatorCycle`), and the rack the plot as
  recorded ops (`cycleView`), drawn again only when the cycle or its size
  changes.
- **Analysis panel:** every unit with an ID can show a panel under its row
  in the rack, as wide as the rack and 200 dp high, with an oscilloscope
  and a spectrum of the signal after the unit, as the synth plays it (see
  [Taps](#taps)), both channels of a stereo signal. The chart button at
  the top left of the cell after the parameters of the row (after the plot
  of an eq or an oscillator, where the comment starts) or Ctrl/Cmd+Alt+A
  (`AnalysisToggle`) on the selected unit opens and closes it. Under an
  unfolded eq unit it is under the editor. The rows after it move down,
  the rail runs past it, and when it does not fit as it opens, the rack
  scrolls the row to the top.
  - *Signal.* After the unit: after all the units it stands for (a module
    unit's, an eq unit's). A unit that leaves nothing on the stack (`out`,
    a popping `send`, `pop`) shows the signal before it instead, and the
    panel says so. The synth records the top two signals of the stack; it
    is stereo for a unit with `stereo` on, a `pan`, or a module unit with
    two outputs, else only the first is shown. A disabled unit is not
    played and shows nothing.
  - *Voices.* A menu in the panel chooses: all the voices of the instrument
    summed (the default), the voice of the last note (`Last note`; as the
    player gives notes to voices, `MsgToModel.TriggerVoice`: notes that
    `spawn` units start in the synth are not followed), or one voice.
  - *Oscilloscope.* As long as the oscilloscope of the song (its length in
    beats, with the same beat lines); it restarts at the left at every note
    of the instrument and wraps. Zoom and pan as the other plots.
  - *Spectrum.* 2048 bins of 10.8 Hz, each new window counting a quarter
    (as the spectra of the eq editor), on the scale of the spectrum of the
    song.
  - *Cost.* Nothing in the song and nothing in the exported player: the
    open panels are a setting of the tracker, by unit ID (and for an inner
    unit the module unit it is in), not saved and not undone. The synth
    records only where a panel is laid out; closing one stops its tap at
    once, and one not drawn for a second (scrolled out of view, another
    instrument shown) stops too. No analysis unit is needed: every place
    between two units is after a unit, or before the first.
  - `tracker/analysis.go` (`UnitModel.AnalysisOf`, `analysisPoint`,
    `AnalysisVoice`), `tracker/gioui/analysis_panel.go`; the plot data of
    the oscilloscope and spectrum of the song are shared
    (`scopeData`, `beatTicks`, `spectrumCurve`). Tests:
    `tracker/analysis_test.go` (places, stereo, the sine in the
    oscilloscope and the spectrum, the pan in two channels, the last note's
    voice, closing, inside a module), `TestExpandSpans`,
    `TestAnalysisPanelInRack` and `TestOscillatorCycleRow` (headless
    renders: `SOINTU_TEST_SCREENSHOTS=<dir> go test ./tracker/gioui -run
    'AnalysisPanel|OscillatorCycle'`), `vm/oscillator_test.go` (the cycle
    against the synth).
- **Buses:** `mcspread` gets a new bus, and new mc units the bus of the mc
  unit above them; buses the tracker created are deleted with their last
  unit. An instrument loaded from a preset or file gets its own buses when
  its bus IDs are taken. The rack shows the peak level of each channel of
  the bus after each mc unit. Presets: Reverb FDN Room, Hall, Ambient and
  Plate.
- **Delay times:** the `delay` unit's delay times are on a grid, and show
  its values by name: note lengths when the delay follows the tempo (1/8,
  1/8D dotted, 1/8T triplet, from 1/128T to 2/1D; large steps move between
  the straight notes), semitones when it follows the note (large steps:
  octaves) and whole milliseconds when it is fixed (large steps: 10 ms).
  Note lengths are chosen from a dropdown, or stepped by keys; semitones and
  milliseconds are a knob that moves over the grid by keys, wheel or drag.
  The `rate` knob of a `spawn` unit in sync mode names the straight note
  lengths in the same way (1/8 at 72; large steps move between them). The `free` switch of
  the unit gives every value again, shown as stored and stepped as before.
  It is a setting of the tracker, not part of the song: the stored values
  mean what they did. A unit starts free if any of its times is not on the
  grid (a loaded song or preset, a pasted unit, a reverb preset of the
  unit), so that a step does not move the time to the grid; after that the
  switch changes only by hand. With the switch off, a value that is not on
  the grid shows as it is (50, or 25.3 for milliseconds) and stays until it
  is edited. The hint adds beats, rows and milliseconds, and tells
  when the tempo makes a delay longer than the longest delay line.
- **Warnings:** spectral units and mc units in instruments with several
  voices, spectra with several writers, and buffers, spectra or buses used
  across threads.
- **Modules tab:** the list of the modules, with the name, the inputs, the
  outputs and the parameters of the selected one (name and default).
  Next to it, the unit editor edits the units of the selected module instead
  of those of the selected instrument; notes still play the selected
  instrument. The link button of a module parameter binds the parameter
  under the cursor of the rack to it; a bound parameter shows the name of
  the module parameter and edits its default. With the cursor on a bound
  parameter, the Range of its module parameter is the range of that
  binding: the values the parameter gets at the lowest and the highest
  value of the module parameter. Changing the range of the first binding
  changes the default and the values of the module units to keep what they
  give, as near as the new range allows.
- **Module units:** a module unit shows the name of its module and its
  parameters, and sends can target them. Each parameter looks and works
  like the parameter bound to it: a menu of the buffers, spectra or buses, of
  the instruments for a spawn target, of note lengths for a delay time
  following the tempo, of the values of a choice (also through a module
  unit of another module, and the default on the Modules tab), a switch, or
  a knob with the same scale and labels.
  With a scaled binding it is a knob from 0 to 128, labelled with the value
  that the bound parameter gets.
  The buttons under the rack make a module of the selected units (Ctrl+G;
  its inputs are the signals they take from before them), show the module of
  a module unit on the Modules tab (Ctrl+Shift+G), replace the module unit
  with the units of its module, and give it a copy of the module of its own.
- **Sends follow:** making a module of units that sends from outside them
  target makes the modulated parameters parameters of the module, named
  after them, and the sends go to those ports of the module unit. A stereo
  send takes two parameters next to each other. With no parameter left of
  the 8, the send stays as it was, which the alert tells, naming the send.
  Replacing a module unit with the units of its module makes each send to it
  the sends that it is played as: one to each parameter bound to its port,
  with scaled amounts, stereo where it can be (`sointu.SendToPorts`, which
  `Song.Expand` uses too, and `Module.Ports`). A send to a port that nothing
  is bound to stays, without a target.
- **Unfolding:** a module unit is like a section that can be collapsed (an
  `eq` unit too: see EQ editor): the
  chevron on its row in the unit list and the rack, or in the footer
  (Ctrl+Alt+G), unfolds it. Its inner units then follow it, set into the
  rack, darker and indented: the units of its module, as they are in the
  module. A module unit among them can be unfolded in turn. The signals run
  through the module unit into them; those only passing by stay bright.
  Which module units are unfolded is saved with the song (`unfolded` of the
  unit, a hint like `mute` of an instrument). The footer shows how many of
  the 63 units the instrument has once expanded.
- **Editing inner units:** the cursor goes through the inner units like
  through any other row, and what is done there is done to the module, so to
  every module unit using it: changing parameters by mouse or keyboard,
  adding, deleting, moving, disabling, copying and pasting units, changing
  their type and comment, making a module of them. The footer then says
  `editing module <name>, used N×`.
  - A parameter bound to a parameter of the module shows the value that the
    module unit above gives it, and changing it changes that value of the
    module unit, through the binding, like turning the knob on the row of
    the module unit: the module and the other module units stay as they are.
    With a scaled binding, a step is a step of the parameter of the module.
    On the Modules tab, where the module unit is a unit of the module being
    edited and its parameter may be bound too, it changes what the knob of
    the module unit changes there: the default.
  - `Bind` in the footer binds the parameter under the cursor to a
    parameter of the module, or to a new one named after it, or unbinds it.
  - A selection stays in one list of units: the units of the instrument, or
    the inner units of one module unit. Shift+arrows from a module unit go
    past its inner units; moving out of the list without Shift leaves the
    selection behind. Delete deletes inner units, never the module unit that
    the cursor is inside, and leaves the last inner unit: a module is
    emptied on the Modules tab.
  - Ctrl+Alt+G on an inner unit folds the module unit that the cursor is
    inside, and Ctrl+Shift+G shows its module on the Modules tab.
  - Previews, spectra and the curves of the selected filter or envelope are
    those of the unit played for the module unit above. If a module unit
    uses a module more than once through other modules, they are those of
    the first copy.
  - `tracker/rows.go`: the rows, `modelData.UnitPath` (the module units that
    the cursor is inside) and `Model.scope` (the units being edited);
    `innerBinding` in `tracker/params.go` (the bound parameters).
- **Module presets:** the Presets menu of the Modules tab saves the selected
  module, with the modules it uses, as a file in `sointu/modules` of the
  user's configuration directory, adds a saved one to the song, and deletes
  one (`Delete <name>`). Saving over a preset with the same name and
  deleting one ask first, like the instrument presets do. The tracker comes
  with module presets of its own (`tracker/modules/*.yml`, embedded), listed
  after those of the user; they cannot be deleted, and a preset of the user
  with the same name is used instead.
- **Reverb:** the standard reverb is the [reverb unit](#reverb-unit) (Go
  synth and wasm player only): stereo in, the wet signal out. Its chain is
  low cut, high cut, predelay, a diffuser of four steps (the early
  reflections) and a feedback delay network of 8 lines (the tail). The
  instrument preset Global reverb (UTIL) is the aux signal through it: `in`
  from aux, the reverb unit, `out`, 3 units (25 when it was the module).
  The module preset `Reverb` is the same reverb made of
  [mc units](#mc-units), 23 units, for changing the network itself; it
  renders what the unit renders with the same values, and takes three times
  the bytes in the player. Each module unit using it gets a bus of its own.
  `examples/reverb_module.yml` uses it, `examples/reverb_unit.yml` the
  unit.
- **Sidechain ducking:** the kick instrument itself turns down a bus. What
  should pump is sent to aux 4/5 instead of the main output (`aux`, channel
  4). The kick instrument, after its own `out`, reads that bus with `in`,
  multiplies it by a gain that the note of the kick triggers, and sends it
  out: `in`, the module unit, `out`. No second pattern and no compressor.
  An `outaux` in place of that `out` sends the ducked bus to the reverb too.
  `examples/ducking.yml` is a bass and a pad on the bus. Two limits:
  - A bus is an aux pair, and there are seven (three for x86): 2/3, which
    the reverb presets read, 4/5, which Kick ducker reads, 6/7, which the
    delay presets read, 8/9, which the drum bus presets read, and 10/11 to
    14/15, which no preset uses. `in` clears the pair, so one instrument
    reads a bus.
  - The kick instrument has to come after the instruments on the bus in the
    instrument list, and before a mastering preset. Before them, it reads
    what they sent in the sample before: the bus is one sample late.

  The module preset `Ducker` (3 units: `envelope`, `send`, `gain`) is the
  gain: stereo in, stereo out, the signal times 1 - `depth`·envelope. The
  envelope falls from 1 to 0 and is sent, times -1, to the gain of a `gain`
  unit of 1. With nothing after it but `out`, the send can go to the gain of
  that `out` instead: 2 units, in a copy of the module.

  | Parameter | Default | Sets |
  |---|---|---|
  | `depth` | 128 (100 %) | how far down: 64 is -6 dB, 96 -12 dB, 112 -18 dB, 128 silence |
  | `release` | 70 (203 ms) | the time back to full level, as the times of the envelope: 64 is 93 ms, 76 443 ms |
  | `curve` | 0 (linear) | `curve` of the envelope: the gain comes back fast first, then slowly |

  Measured, with a constant signal on the bus, so that the output is the
  gain (`release` 70, times after the note):

  | `curve` | -12 dB | -6 dB | -3 dB | -1 dB | full |
  |---|---|---|---|---|---|
  | 0 | 52 ms | 103 ms | 145 ms | 182 ms | 204 ms |
  | 32 | 43 ms | 90 ms | 133 ms | 176 ms | 204 ms |
  | 64 | 25 ms | 57 ms | 95 ms | 149 ms | 204 ms |
  | 96 | 14 ms | 31 ms | 53 ms | 94 ms | 204 ms |
  | 128 | 8 ms | 18 ms | 31 ms | 55 ms | 203 ms |

  The gain is 1 until the note, and down after 49 samples (1.1 ms): the
  attack of the envelope is 30, not 0, which is not a parameter. The largest
  step of the gain is 0.02 per sample (0.16 at `curve` 128, which bends the
  attack too). With attack 0 the gain steps to its lowest value in one
  sample, and the bus steps by whatever level it has then: in the example,
  by up to 0.16 against 0.02, with 5 to 11 dB more above 5 kHz in the 6 ms
  around the kick. The note of the kick should last as long as the release:
  released earlier, a curved envelope comes back sooner (149 ms to -1 dB
  becomes 132 ms at `curve` 64 for a note of one row), a linear one the
  same. In the example (140 beats per minute), the bus is 13 dB lower below
  120 Hz in the 93 ms after a kick, back within 1 dB after 182 ms, and the
  mix peaks at -2.8 dB instead of 0.0 dB without ducking.

  The preset Kick ducker (DR; 18 units, 13 of them the kick) is a kick with
  this after it: a sine an octave below the note, falling to it from 52
  semitones above along a curved envelope, a curved amplitude envelope,
  `distort` for harmonics and a flat body, and 2.4 ms of high-passed noise
  as click. For the G below note 60: 49 Hz from 100 ms on (540 Hz over the
  first 10 ms, 170 Hz from 10 to 30 ms), -40 dB after 260 ms, peak -4.8 dB,
  -14 dB RMS over 400 ms; of eight other kick presets measured, seven peak
  7 to 13 dB lower and DR kickedm 8 dB higher. Envelope curves: Go synth
  and wasm player only.

  The alternative that needs no envelope is the module preset `Sidechain`
  (4 units: `compressor`, `send`, `pop`, `gain`), a real audio sidechain: it
  takes a mono key on top of the stereo signal and multiplies the signal by
  the gain that the compressor computes from the key. In the kick
  instrument: `in` from the bus first, then the kick, `push`, `pan`, `out`,
  the module unit, `out`. Its parameters are `threshold` (16, -18 dB),
  `ratio` (128), `attack` (44) and `release` (58). The gain then follows the
  kick as it is, so a longer or louder kick ducks longer and deeper.
  Measured with the kick above as key: -17.4 dB at the lowest, -10 dB after
  1.3 ms, back to -6 dB after 283 ms and to -1 dB after 332 ms; -23.4 dB
  with `threshold` 8. The level follows the square of the key, 98 Hz for a
  49 Hz kick, so the gain ripples by 1.2 dB within a period, which distorts
  the bus a little: 2.5 dB with `release` 48, 0.3 dB with 72, which comes
  back in 1.1 s.
- **Ping pong delay module:** the module preset `Ping pong delay` (10
  units) is a tempo-synced stereo delay, wet only. The `delay` unit cannot
  feed one side into the other: in stereo it is two delays next to each
  other. So the cross-feed is made of units: the sum of the input goes into
  one side (`addp`, `pan`), a stereo `delay` without feedback of its own
  delays both sides, and its output goes through a low-pass and a high-pass
  `filter`, an `xch` and a `send` to a `receive` before the delay, left to
  right and right to left.

  | Parameter | Default | Sets |
  |---|---|---|
  | `time` | 36 (a dotted eighth) | the time between repeats, in 1/48 beat; 65535 samples (1.49 s) at most |
  | `feedback` | 80 (44 %) | the level of a repeat relative to the one before, 0 to 70 % |
  | `tone` | 80 (3.4 kHz) | the low-pass in the feedback, 250 Hz to 7.4 kHz: each repeat darker |
  | `lowcut` | 32 (110 Hz) | the high-pass in the feedback, off to 1.8 kHz: each repeat thinner |
  | `pan` | 0 | the side of the first repeat: 0 left, 128 right; 64 is a mono delay |

  Measured with a click of noise at 120 beats per minute: the repeats come
  16537 samples (375.0 ms) apart, 1, 5, 7 and 10 samples late for the first
  four (a sample per pass for the `send`, the rest the delay of the
  filters), on the left, right, left, right, with nothing on the other side.
  From the first to the fourth repeat the level falls by 20 dB from 125 Hz
  to 4 kHz (three passes at 44 % are 21.5 dB), by 25 dB at 60 Hz, 31 dB at
  4 to 8 kHz and 62 dB above; with `tone` 128 and `lowcut` 0 by 21.5 dB
  from 250 Hz up. The feedback ends at 70 % because the filters are not
  flat: each raises the level by about 1.2 dB next to its frequency, both
  together by up to 2.7 dB where they meet (computed from the filter), and
  the loop has to lose more than that. The worst setting still decays, by
  5 dB per second at 174 beats per minute with a time of 12.
- **Ducking reverb and Ducking delay modules:** the module presets
  `Ducking reverb` (4 units; 26 when it had the Reverb module) and `Ducking
  delay` (13 units) are the reverb unit
  and the Ping pong delay module with the wet signal turned down while the
  dry input plays: the space stays out of the way of the notes and blooms
  after them. A stereo `compressor` computes a gain from the input, a
  stereo `xch` puts it below the input, and after the reverb unit or the
  module unit a stereo `mulp` multiplies the wet signal by it. The Ducking
  delay uses the Ping pong delay module instead of copying its units, so its
  file carries that too, the same as the module preset, and a song gets it
  once. The parameters of the reverb are bound to those of the reverb unit
  of the same names. Parameters: `size`, `decay`, `highs`, `lows` and `lowcut` of
  the reverb (`predelay`, `mod` and `highcut` stay at their defaults), or
  `time`, `feedback`, `tone`, `lowcut` and `pan` of the delay; then `duck`
  (96; the ratio of the compressor: 0 is no ducking), `release` (62) and
  `threshold` (8, -24 dB). Measured with a held saw note of one second: the
  reverb is 15.7 dB lower when the note starts, 9.4 dB lower at its sustain
  level, within 1 dB 180 ms after the note ends, and from then on exactly
  the reverb without ducking. `duck` 48 gives 7.8 dB, 128 gives 20.9 dB;
  `release` 52 comes back in 40 ms, 68 in 420 ms, 74 in 1.1 s. As with any
  compressor, the depth follows the level of the input: 6 dB less input,
  4.5 dB less ducking. The delay measures the same.

  The stereo `xch` next to the mono one of the Ping pong delay, and next to
  its stereo `delay`, needs the wasm player with the stereo `xch` fixed (see
  [Wasm player](#wasm-player)). Before that fix the modules had a `send`,
  a `pop` and a `gain` instead, a unit more, and rendered exactly the same.
- **Global presets with them** (UTIL), which leave the existing ones as
  they are: Global ducking reverb (aux 2/3 through `Ducking reverb`, 6
  units; 28 with the Reverb module), Global ping pong delay (aux 6/7 through `Ping pong delay`, 12
  units; its `outaux` can send the repeats on to the reverb) and Global
  mastering 2 ducking (aux 6/7 through `Ducking delay`, a quarter of it on
  to the reverb, aux 2/3 through `Ducking reverb`, then Global mastering 2;
  28 units, 50 with the Reverb module). Global mastering 2 buses has the
  `Ping pong delay` on aux 6/7 next to a reverb unit and the drum bus, and
  Global mastering 2 buses ducking is Global mastering 2 ducking with the
  drum bus: see the drum bus below.

  All of this is measured on rendered audio, not judged by ear. Tests:
  `vm/compiler/wasm_ducking_test.go` (what the modules do, and that the
  wasm player renders them, the example and Global mastering 2 ducking like
  the Go synth) and `TestBuiltinModulePresetsCanonical` and
  `TestDuckingPresets` in `tracker/module_test.go`.
- **EQ editor:** an `eq` unit (see [eq](#eq)) is edited in its row of the
  rack, and folds and unfolds like a module unit (see Unfolding under
  Modules): the same chevron in the unit list and above its name in the
  rack, the same button in the footer and key (Ctrl+Alt+G), saved with the
  song in the same `unfolded` of the unit, undone in the same way. Each eq
  unit has its own editor and its own selected band; several can be
  unfolded at once.
  - *Folded*, the row shows its `stereo` switch and `gain` knob, then a
    small plot 5 cells wide (`tracker.EQInlineCells`): the curve of all the
    units, a 0 dB line, lines at 100 Hz, 1 kHz and 10 kHz, and small handles
    without numbers; no curves of the bands, no spectra, no labels. The
    mouse and the keys work there as in the large plot; a click on it puts
    the cursor of the rack on the row, on its parameters. The comment
    follows the plot.
  - *Unfolded*, the row shows its parameters, and under it, inside the rack
    and scrolling with it, as wide as the rack, the whole editor
    (280 dp): a plot from 20 Hz to 20 kHz and ±24 dB with the curve of
    every band, the curve of all the units (what is heard: see
  the response there) and a numbered handle for every band, at the
  frequency and the gain that its units have, or at 0 dB for a band without
  gain. Under it the selected band: its type (a menu), on or off, its
  frequency, gain and Q as numbers to type (`1.2k`, `-4,5 dB`), then what
  it was compiled to (`→ 990 Hz · 3.12 dB · Q 1.23 · 1 unit`, with
  `Go synth and wasm only` for a ladder), the number of units of the eq and
  the gain of its gain unit. The signals passing the eq run on through
  the rail beside the editor, and the rows after it move down by its
  height; when it is unfolded and does not fit, the rack scrolls the row to
  the top. `examples/eq.yml` has three.
  - *Mouse.* Dragging a handle changes the frequency and the gain, or for a
    band without gain the frequency and the Q; with Shift a fifth as far;
    with Alt the Q only (60 dp up doubles it). Scrolling over the plot
    scrolls the rack; with Alt held it changes the Q of the band under the
    pointer, or of the selected one (the plot takes the scroll only while
    the key is held, `Tracker.plotZoomModifier`, like the plots that
    zoom). A
    double click on empty space adds a band there and drags it: a bell, a
    low cut below 40 Hz, a `ladder` high cut above 12 kHz. A double click
    on a handle removes its band; the right button switches it on or off.
  - *Knobs.* The row under the plot has a knob for the frequency, the gain
    and the Q of the selected band, beside the numbers
    (`EQEditor.layoutKnob`): dragging up or to the right goes through the
    range in 256 dp, a fifth as fast with Shift, as one step of the undo
    history; the right button sets the gain to 0 dB and the Q to that of
    the type. The gain knob of a band without gain is greyed out.
  - *Keys*, with the focus on a plot, small or large (Tab, or a click):
    Left and Right
    select a band, Shift+Left and Shift+Right change its frequency by a
    semitone, Up and Down its gain by 0.5 dB, Alt+Up and Alt+Down its Q by
    a sixth of an octave, Alt+Left and Alt+Right its type; with Ctrl/Cmd the
    steps are a quarter as large. Enter adds a band in the widest gap,
    Ctrl/Cmd+Enter switches the band on or off, Delete removes it. The keys
    of the notes still play.
  - *Undo.* Every change goes through `Model.change`. A drag is one step of
    the undo history, and so is what is scrolled within half a second
    (`EQModel.BeginGesture`).
  - *While playing*, the player gets the new units with every change. When
    the units themselves change, not only their parameters (a band on or
    off, a bell through 0 dB, a Q through 1, the gain unit coming or going),
    the units that are still there keep their state in the Go synth (see
    [State across changes of the patch](#state-across-changes-of-the-patch)):
    the notes go on, and only the new units start from nothing. The units
    of an eq have no IDs and are matched by their type, in their order: when
    one of several units of the same type goes, the ones after it can take
    the state of their neighbour for a moment.
  - *Spectra.* Behind the curves are the spectra of the signal at the eq,
    as the synth plays it (see [Taps](#taps)): before the eq as an area,
    after it as a line, so the line is the area moved by the curve. Of all
    the voices of the instrument together, of a stereo eq the louder
    channel; 0 to -90 dB over the height of the plot, 2048 bins of 10.8 Hz,
    each new window counting a quarter. The button next to the unit count
    hides them, and the synth then records nothing. With the native synth
    there are none.
  - `tracker/eq.go` (`Model.EQ` for the selected unit, `Model.EQAt` for the
    unit on a row; their state by instrument or module and unit ID),
    `tracker/gioui/eq_editor.go` (`eqGeometry`, `eqHit`, `eqDragged`: where
    things are and what a drag does, without a window), `layoutEQ` and
    `rowHeight` in `tracker/gioui/instrument_editor.go` (the rows of the
    rack are not all as high: `ScrollTableStyle.RowHeight`). Headless
    renders: `SOINTU_TEST_SCREENSHOTS=<dir> go test ./tracker/gioui -run EQ`.
- **Global mastering presets** (UTIL), next to upstream's Global mastering,
  which is unchanged:
  - Global mastering reverb: the aux signal through the reverb unit, then
    Global mastering as it is (9 units; 31 with the Reverb module).
  - Global mastering 2, for loud music with a clean bass: a low cut of
    12 dB per octave at 27 Hz, a compressor (about 4.6:1 above -6 dB, 10 ms
    attack, 150 ms release, +7.5 dB makeup), a `limiter` (-0.3 dB, 2.9 ms
    lookahead, +4.4 dB drive) and a `clip` at full scale for what little
    the limiter lets through. With the limiter it is Go synth and wasm
    player only.
  - Global mastering 2 reverb: the reverb unit, then Global mastering 2
    (10 units; 32 with the Reverb module).

  Why a second version: measured with sines and a test mix (bass, saw
  chords, noise snare), Global mastering does this:
  - Its "Limit highs" filter (low-pass minus band-pass at the top
    frequency) raises the highs: +2 dB at 3.5 kHz, +6 dB at 10 kHz, +9 dB
    at 20 kHz, relative to 1 kHz.
  - Its low cut (high-pass plus band-pass) raises the bass by 3 dB around
    40 to 55 Hz and cuts only below about 20 Hz.
  - Its compressor releases in 5 ms, so its gain follows the waveform of a
    bass note: a 55 Hz sine comes out with 4.5 % distortion.
  - Nothing limits the peaks: the test mix peaks 6 dB higher after it than
    before, so a mix that just reaches full scale is clipped by whatever
    plays it.

  Global mastering 2 is flat from 30 Hz up, gives the same sine 0.8 %
  distortion, also when it is 7 dB louder, and never exceeds full scale. On
  the test mix it is 3 to 5 dB louder (RMS) than Global mastering followed
  by a clip, for a mix peaking at -2 to +1 dB (-7.7 and -6.6 dB RMS against
  -12.5 and -9.7), with 0.01 % of the samples clipped: the limiter turns
  the gain down ahead of the peaks instead. Before it had the limiter, the
  clip alone took the peaks: 1 to 3 % of the samples, and 1 to 1.5 dB less
  loudness. The slow release of the compressor can be heard as pumping
  with a loud kick. The values to tune: `drive` of the limiter for
  loudness, `release` of the compressor and of the limiter. All of this is
  measured, not judged by ear.

  | Parameter | Default | Sets |
  |---|---|---|
  | `size` | 64 | the lengths of the lines: the network from 40 to 280 ms (160 ms), the diffuser steps with it |
  | `decay` | 90 (3.1 s) | the reverb time, as `decay` of `mcdelay`; 0 holds the sound |
  | `highs` | 48 | the time above 3 kHz relative to `decay` (`hfdecay`) |
  | `lows` | 72 | the time below 250 Hz relative to `decay` (`lfdecay`) |
  | `predelay` | 13 (20 ms) | 0.1 to 200 ms before the reverb starts |
  | `mod` | 24 | how far the lines move (`moddepth`), against ringing; can be modulated |
  | `highcut` | 98 (10 kHz) | a low-pass on the input, 1 to 20 kHz; can be modulated |
  | `lowcut` | 56 (150 Hz) | a high-pass on the input, 20 Hz to 2 kHz; can be modulated |

  These are the parameters of the Reverb module and the first eight of the
  reverb unit. The level of the reverb, the level and width of the early
  reflections, the width of the tail and the rate of the modulation are
  further parameters of the unit (see [reverb unit](#reverb-unit)); in the
  module they are values of its units (`gain` of `mcspread`, `mcsum`). Small sizes with long
  decays ring, as small rooms do: the tail is dense from about `size` 32
  with the default decay.
- **Files:** instrument files, presets and the units and instruments on the
  clipboard carry the modules they use. Loading them does not add a module
  that the song already has with the same name and the same content (apart
  from IDs); another module with a taken name gets a number added.
- **UI zoom:** Ctrl/Cmd+scroll zooms by the distance scrolled: a step for
  every 50 dp on a trackpad, and for every notch of a mouse wheel
  (`zoom_scroll.go`). Before, every scroll event was a step, which on macOS
  went through the whole range in a short swipe. It zooms wherever the
  pointer is, also over lists and knobs, which scrolled or stepped instead
  before. The momentum of a trackpad gesture does not zoom. The vendored Gio
  has two additions for this: `pointer.Event.Notch` and `.Momentum`, set by
  the macOS backend.
- **Signal rail:** the rail at the left of the rack has room for at least 6
  signals (`signalrail.minsignals` in the theme), so that a parameter
  changing the signals on the stack (`stereo`, the `args` and `mode` of a
  `spawn`, the `module` of a module unit, ...) does not move the rack under
  the pointer. A deeper stack widens it, eased over 150 ms, and it stays
  that wide: it starts anew when another instrument or module is shown, or
  when the first unit is another one (`RailLane` in `signal_rail.go`).
  Before, it was as wide as the deepest stack, and followed every change.
- **Knob drags:** dragging a knob of the rack follows the pointer in the
  window, also when the knob itself moves, as when the rail widens. Before,
  the drag was measured from the knob, and the value jumped by the distance
  the knob moved.
- **Colliding IDs:** when two units of a song have the same ID, e.g. a unit
  of a module and one of an instrument in a song put together from parts,
  the later one gets a new ID, as before; now the sends follow. A send
  means the unit of its own instrument or module with that ID, if there is
  one: when that unit gets a new ID, the sends among the same units go to
  it (`fixIDCollisions`). Before, the sends of the module went to the unit
  of the instrument that kept the ID.
- **Unknown unit types:** a song with a unit of a type that this version
  does not have (`sointu.UnknownUnit`), e.g. saved by a newer version, is
  no longer played or compiled as if it were fine.
  - `sointu-compile` and `sointu-play` refuse it, naming the type, the unit
    and the instrument or module, for every architecture; so do
    `sointu.Play`, `vm.NewBytecode` and the synths. Before, the compiler
    gave the type an opcode of its own and wrote a player that had no code
    for it. With `-allow-unknown-units` (`Compiler.AllowUnknownUnits`,
    `Song.WithoutUnknownUnits`) the song is compiled or played without
    those units, with a warning for each. Disabled units are not looked
    at, as they are not played.
  - The tracker keeps such a unit in the song, with its parameters, and
    saves it; it shows a warning naming the first one, and plays the song
    without them. Before, the synth refused the whole patch (silence, and
    an error), and the next change of the patch removed the parameters of
    the unit as invalid. What it cannot keep is what the unit has besides
    parameters that this version does not read, like the bands of an `eq`
    unit in a version without it.
- **Other:** no notes play while typing in text fields; recordings survive
  synth rebuilds; NaNs recorded into buffers are cleared.
- **Club presets:** a sound set for dnb, techno and trance: 21 instrument
  presets whose names start with `Club`, in the directories of their kind,
  and two Global presets with a bus for the drums. They are **tuned by
  measurement, not by ear**: nobody has listened to them yet. Each was
  rendered with the Go synth and its level, spectrum, envelope, pitch,
  stereo image and aliasing measured; the values below are from those
  renders. All but Club sub bass use units or parameters that only the Go
  synth and the wasm player have, which their comments say.

  | Preset | Units | What it is | Measured |
  |---|---|---|---|
  | DR Club 909 kick | 8 | a sine swept down to the note, soft-clipped | at A-1: 226 Hz after 4 ms, 117 after 11, 70 after 24, 55 from 50 ms; -20 dB after 240 ms |
  | DR Club kick hard | 14 | a kick driven hard, and its own echoes in 16th notes, low-passed below about 100 Hz, driven and faded in after each hit: the rumble | 429 Hz after 2 ms, 57 after 57 ms; crest 5.7 dB; the rumble is 5 dB below the kick in the 16th after it and 10 dB below it in the next |
  | DR Club 909 snare | 13 | a sine body with a pitch drop, and band-passed noise | body at the note (F#3: 185 Hz), noise from 2.4 kHz up, most of it between 5 and 10 kHz; -20 dB after 86 ms, -40 after 142 |
  | DR Club 909 clap | 9 | band-passed noise in four bursts 11 ms apart, then a tail | 0.6 to 2.4 kHz; the tail 8 dB below the bursts, -40 dB after 250 ms |
  | DR Club 909 hat closed, hat open | 11 | square waves, frequency modulated into a dense spectrum, and noise, through a low cut and a band-pass | centre of the spectrum 9.2 kHz, most of it from 5 to 15 kHz; -40 dB after 42 ms closed, 526 ms open; releasing the note chokes the open hat within 6 ms |
  | DR Club 909 ride, crash | 11 | the same, more metal for the ride, more noise for the crash | centre 7.7 and 12 kHz; -40 dB after 1.1 and 1.7 s |
  | DR Club 909 tom | 8 | a sine with a pitch drop | starts an octave above the note, at the note after 45 ms; -20 dB after 210 ms |
  | BA Club sub bass | 5 | a sine | nothing else above -87 dB; no click (largest step at the start 0.001) |
  | BA Club acid bass | 8 | a saw through a `ladder` with resonance, drive and an envelope on the cutoff | the highs above 2.4 kHz fall by 19 dB within 200 ms and are gone after that; aliasing -52 dB at A-2, -42 at A-4 (-42 and -34 without `bandlimit`) |
  | BA Club reese bass | 13 | two detuned saws a side through a slowly moving `ladder` and a `softclip`, over a sine sub | the sub varies by 1.1 dB (below 65 Hz, at F-1); side 21 dB below mid under 150 Hz; crest 10 dB |
  | BA Club dist bass | 11 | a saw a side, clipped hard above a crossover, over a sine sub | the sub varies by 1.3 dB; side 22 dB below mid under 150 Hz; aliasing -75 dB at F-1, -65 at F-3 (-65 and -54 without `oversample`) |
  | LEAD Club supersaw lead | 8 | 16 detuned saws, 8 a side, through a `ladder` | aliasing -47 dB at C-7, -53 at C-5 (-16 and -25 without `bandlimit`); correlation 0.2 to 0.5, mono 1.2 to 2.3 dB quieter than stereo; side 8 to 37 dB below mid under 150 Hz |
  | PAD Club supersaw pad | 8 | the same, slow, darker, wider | correlation 0 to 0.35; attack 340 ms, release 580 ms |
  | PL Club supersaw pluck | 10 | the same with an envelope closing the `ladder` | bright for 60 ms, -20 dB after 120 to 190 ms; left and right within 1.5 dB of each other |
  | LEAD Club hoover | 14 | four detuned pulses with a moving pulse width, four detuned saws a side an octave below, and a pitch swoop from 6 semitones below | correlation 0.6 to 0.9; no offset from the moving pulse width |
  | FX Club riser | 8 | detuned saws rising two octaves in 5.9 s while the `ladder` opens | the level rises by 28 dB, the centre of the spectrum from 280 Hz to 1.1 kHz |
  | FX Club noise sweep up | 7 | stereo noise through a resonant low-pass that opens in 5.9 s | -56 to -19 dB, centre of the spectrum 300 Hz to 3.7 kHz |
  | FX Club downlifter | 7 | the same, falling | -19 to -64 dB in 5.5 s, 3.5 kHz to 250 Hz |
  | FX Club impact | 11 | a sine dropping two octaves to the note, with low-passed noise, soft-clipped, long | at C-1: 117 Hz after 9 ms, 34 Hz after 200 ms; -20 dB after 1.7 s |

  **Levels.** The presets are levelled against each other, so that a mix
  starts without gain changes. The anchor is the kick at -6 dB peak, which
  leaves 6 dB for what plays with it: kick and sub together stay below full
  scale (-1.4 dB). Peaks: kicks and impact -6 dB, snare and clap -8, tom
  -9, crash -12, hats -15 to -16, ride -16, sub -9 (-12 dB RMS), reese -5,
  dist bass -7, acid -8.5, pluck -12. RMS of a held note: acid, reese and
  dist bass -15 to -17 dB, hoover -20, lead -22 (a chord of three -17), pad
  -24 (a chord of four -18), the riser and the sweeps -16 to -18 at their
  loudest. The loop of `examples/soundset_loop.yml` peaks at +3.2 dB before
  the master, mostly the drums (+2.8 dB alone), which is what the master
  chain and the drum bus are for.

  **What the measurements changed.**
  - *A clean sub under distortion.* Saturating saws makes tones at their
    fundamental, which beat against a sub. So reese and dist bass cut the
    saws below 200 Hz *before* the clipper and add a sine that the clipper
    never sees. With the low cut after the clipper the sub was steadier
    still (0.4 dB), but the peaks were 4 dB higher at the same loudness
    (crest 14 dB against 10): a high-pass turns a clipped wave into
    spikes.
  - *Left and right equally loud.* A stereo oscillator with unison starts
    every note with its voices a twelfth of a cycle apart, and the detune
    brings them together on one side and apart on the other: for the first
    100 ms one side is 5 dB louder, on every note. A second oscillator
    detuned the other way, 1.125 times as far, with `phase` 76, brings that
    to within 1.5 dB with a correlation above 0 (lead, pad, pluck). In the
    hoover it is the pulses, which are therefore in the middle.
  - *Mono compatibility.* Mirrored detune makes the sides differ more than
    they agree: at `width` 64 the lead was 4 dB quieter in mono. `width` 48
    makes that 2 dB.
  - *The hoover's pulse width* moves the mean of the signal a few times a
    second, by a fifth of full scale as computed from its values. A low
    cut at 43 Hz removes it.
  - *`ott`* was tried on the saws of the reese: the level was steady
    without it, and with it the crest of the saws rose from 13 to 20 dB.
    None of the presets uses it.

  Known: reese, dist and acid bass leave an offset of up to 0.02 (-34 dB),
  as their clippers bend an asymmetric wave; the low cut of Global
  mastering 2 removes it. A drum released before its decay ends is shorter
  (the kick: -20 dB after 158 ms instead of 240, released after one row
  at 140 BPM); hold drum notes for two rows or more. "Mono below 150 Hz"
  is the side signal cut at 12 dB per octave from there: a note between
  100 and 150 Hz keeps some width.
- **Hardstyle kicks:** 33 instrument presets in DR whose names start with
  `Hardstyle kick`. 29 are modern (2018 to 2025) euphoric and rawstyle
  kicks: euphoric (5), raw (5), zaag (6), tok (3), gated (2), reverse bass
  (2), long tail (2), frenchcore (2), clean, ott. Four are a 90s sound,
  gabber, gabber rotterdam, uptempo and uptempo hard: a sine swept and
  distorted, mono, from the first version of the set. Like the Club presets
  they are **tuned by measurement, not by ear**. One voice, up to 58
  units; softclip, ladder, width, belleq modulation, curved envelopes and
  (some) ott: Go synth and wasm player only.

  The modern kicks are layers, built as producers build them now:
  - *Pitch:* a sine at the note with a fast drop of 36 semitones in the
    first ms and a slow one of 30 semitones that halves about every 30 ms:
    at G-1, 216 Hz after 10 ms, 159 after 20, 85 after 50, 57 after 120,
    50 after 200.
  - *Punch and sub:* a copy of that sine, clean, low-passed at 800 Hz and
    saturated a little: the punch while it is high, then the sub at the
    note under the tail while the note is held. It is the only low end.
  - *Tok:* a sine 2 to 4 octaves above the note with its own pitch drop,
    clipped and band-passed: a tuned knock. *Click:* high-passed noise of a
    few ms.
  - *Tail:* the same sine through an offset (even harmonics), +12 to +24 dB
    into an oversampled clipper, then two peaks (belleq: which harmonics
    scream, and a formant), a bell, a second clipper, and in the raw kicks a
    short comb with feedback and a third clipper (crunch) and ott; then a
    high-pass at 180 Hz, so the tail sits above the sub. The peaks are
    between the clippers because before the first one the sine has no
    harmonics for them to raise. The tail comes in after the punch.
  - *Zaag:* a saw LFO sweeps the frequency of the first peak between the
    clippers, roughly 0.3 to 3 kHz, in 8th, 16th, 16th-triplet or 32nd notes
    at 150 BPM (5, 10, 15 or 20 Hz; the LFO restarts with each note; the
    rate is in Hz, which the comments say how to change for another tempo).
    The harmonics saw: the spectral centroid of the tail moves by 1.3 to
    2 octaves at the rate, its level by 0.4 to 4 dB (not a tremolo). In
    the screech kicks an envelope sweeps a narrow peak down over the tail
    instead.
  - *Stereo:* only the tail: a chorus of two modulated delays, different
    left and right, a stereo clipper, so the sides differ in their
    harmonics, and `width` with a low cut at 350 Hz. Below 150 Hz the side
    is 24 to 40 dB under the mid; mono loses 0.1 to 0.7 dB.
  - *Master:* the layers summed into an oversampled softclip, a clip and a
    ceiling of -1.8 dB: true peak at most 0.0 dB from F-1 to A-1. About -6 LUFS
    short-term at one kick a beat at 150 BPM (raw and zaag -5 to -6, the
    reverse kicks -8 to -9): 6 dB louder than the Club kicks, which their
    comments say how to match.

  Measured at G-1, the note held for a beat: short-term loudness (LUFS),
  crest (dB), time to -20 dB (ms), centroid of the tail (Hz), the tail above
  2 kHz (dB of the total), the zaag (rate and movement of the centroid),
  side under mid below 150 Hz / 150 Hz to 1 kHz / 1 to 5 kHz (dB), and what
  mono loses (dB).

  | Preset | LUFS | Crest | -20 dB | Centroid | > 2 kHz | Zaag | Side | Mono |
  |---|---|---|---|---|---|---|---|---|
  | euphoric | -6.0 | 5.4 | 422 | 276 | -20 | - | -33 / -8 / -7 | -0.3 |
  | euphoric warm | -6.1 | 5.5 | 424 | 233 | -21 | - | -34 / -12 / -7 | -0.2 |
  | euphoric bright | -5.3 | 5.0 | 420 | 419 | -16 | - | -33 / -7 / -8 | -0.5 |
  | euphoric punchy | -5.8 | 4.9 | 421 | 229 | -20 | - | -35 / -10 / -8 | -0.2 |
  | euphoric long | -6.0 | 6.0 | 485 | 274 | -19 | - | -32 / -8 / -6 | -0.3 |
  | raw | -5.7 | 6.9 | 425 | 1178 | -10 | - | -28 / -11 / -5 | -0.5 |
  | raw screech | -5.5 | 7.2 | 422 | 2543 | -3 | - | -27 / -10 / -4 | -0.6 |
  | raw crunch | -5.3 | 6.8 | 437 | 1429 | -9 | - | -30 / -11 / -6 | -0.5 |
  | raw formant | -4.8 | 6.1 | 425 | 1422 | -8 | - | -30 / -12 / -9 | -0.3 |
  | raw tok | -5.8 | 6.9 | 425 | 1179 | -10 | - | -29 / -11 / -5 | -0.5 |
  | zaag | -5.7 | 6.9 | 421 | 1186 | -7 | 11 Hz, 2.0 oct | -28 / -9 / -5 | -0.5 |
  | zaag 32nd | -5.7 | 7.0 | 422 | 1320 | -6 | 20 Hz, 1.3 oct | -28 / -9 / -5 | -0.6 |
  | zaag triplet | -5.6 | 6.8 | 420 | 1124 | -7 | 15 Hz, 1.8 oct | -27 / -9 / -5 | -0.5 |
  | zaag 8th | -5.8 | 7.0 | 418 | 1269 | -6 | 5 Hz (set; too slow to read in the tail), 1.3 oct | -28 / -9 / -5 | -0.5 |
  | zaag up | -5.0 | 6.1 | 438 | 878 | -9 | 10 Hz, 1.9 oct | -24 / -9 / -4 | -0.6 |
  | zaag screech | -5.5 | 7.0 | 421 | 1428 | -6 | 11 Hz, 1.6 oct | -28 / -10 / -5 | -0.6 |
  | tok | -6.0 | 5.4 | 422 | 274 | -20 | - | -34 / -9 / -8 | -0.3 |
  | tok high | -6.0 | 5.5 | 422 | 274 | -20 | - | -34 / -8 / -10 | -0.3 |
  | tok deep | -6.0 | 5.4 | 421 | 274 | -20 | - | -34 / -9 / -8 | -0.3 |
  | gated | -6.0 | 5.3 | 407 | 276 | -20 | - | -33 / -8 / -7 | -0.3 |
  | gated raw | -5.7 | 6.8 | 410 | 1178 | -10 | - | -28 / -12 / -5 | -0.5 |
  | reverse | -8.2 | 6.1 | 268 | 442 | -18 | - | -34 / -11 / -9 | -0.2 |
  | reverse tight | -9.0 | 6.4 | 233 | 455 | -18 | - | -34 / -12 / -10 | -0.1 |
  | long | -6.7 | 6.5 | 475 | 210 | -21 | - | -36 / -12 / -9 | -0.1 |
  | long raw | -5.6 | 7.4 | 484 | 1284 | -6 | 11 Hz, 1.9 oct | -28 / -9 / -5 | -0.5 |
  | frenchcore | -5.0 | 7.4 | 505 | 1693 | -6 | - | -27 / -12 / -4 | -0.7 |
  | frenchcore screech | -6.0 | 8.0 | 505 | 1866 | -5 | - | -28 / -10 / -4 | -0.5 |
  | clean | -7.2 | 5.5 | 411 | 70 | -62 | - | -40 / -16 / -18 | -0.0 |
  | ott | -7.1 | 5.9 | 413 | 200 | -18 | - | -39 / -13 / -11 | -0.1 |
  | gabber | -6.5 | 4.8 | 419 | 65 | -30 | - | mono | 0.0 |
  | gabber rotterdam | -6.4 | 4.8 | 414 | 65 | -28 | - | mono | 0.0 |
  | uptempo | -7.6 | 5.8 | 324 | 93 | -25 | - | mono | 0.0 |
  | uptempo hard | -8.2 | 6.4 | 323 | 91 | -25 | - | mono | 0.0 |

  The only hardstyle references on disk are the HEX2K7 kicks (2007): -8.5
  LUFS, crest 9.6 dB, -20 dB after 441 ms (medians of 54), and many of them
  wide in the lows (side 8 to 10 dB under the mid below 150 Hz), which
  current practice avoids. The tails, held for a second without the
  chorus's movement, have no aliasing to measure: the energy between the
  harmonics is -93 to -107 dB (a naive square wave: -57). The chorus moves
  the highs of the tail by a few cents, which puts -9 to -15 dB of the
  energy above 4 kHz between the harmonics, as a chorus does. Units that
  are not used: phaser and formant filters are built from `filter`
  (low + high - band is an allpass) and `belleq`; none of the kicks needs
  them yet. Not tested in the wasm player. Tests:
  `tracker/hardstyle_test.go` (each expands, compiles, peaks between -2.2
  and -1 dB, has the side at least 6 dB under the mid, and is silent after
  the note).
- **Horror presets:** a sound set for horror scores, for building tension:
  80 instrument presets whose names start with `Horror`, in PAD (drones,
  pads, glass, choirs), ST (strings), FX (risers, braams, impacts, metal,
  textures), KEYS and DR (heartbeat, pulse, clock), several variations of
  each kind; two Global presets with the space they are made for; and two
  module presets, `Horror shepard` and `Horror echoes`. Like the Club
  presets they are **tuned by measurement, not by ear**. They use units
  that only the Go synth and the wasm player have (ladder, softclip, width,
  reverb, curved envelopes, bandlimited oscillators); not tested in the
  wasm player.
  - *Knobs.* Every instrument but the impacts is one `module` unit and an
    `outaux`: the module holds the sound, its parameters are the knobs, up
    to six, named for what they do: mostly intensity (brightness, drive,
    bow noise), dissonance (a section a semitone above, a tritone, a wider
    cluster), movement (how far the LFOs move things), and swell or attack
    and release. Knobs are module parameters, so a send or an automation
    slot can move them (a DAW maps the mod wheel to a slot). The variations
    of a kind share their module and differ in the knob values and the
    send; a song with several of them gets the module once (the test checks
    that modules of the same name are equal).
  - *Velocity* ([velocity unit](#velocity)): the level of every preset
    follows it (a `velocity` and a `mulp` after the module: -6 dB at 64,
    -12 dB at 32), and in most a `velocity` unit turns the main tone knob
    down, by up to 48 steps at the softest (`velocity`, `loadval` -1,
    `addp`, a send of amount 88 to the module unit): intensity, brightness,
    tone, air or howl; the Shepard tones their brightness, the struck metal
    its clank. Velocity 127, and every note of the score, renders exactly
    what the preset rendered before it had velocity (the test checks it), so
    the levels below are those of full velocity. Measured from 127 to 32:
    the centre of the spectrum falls by 10 to 40 % in those with a knob
    (pads and the dark tremolo most: 122 to 70 Hz, 201 to 137 Hz), and stays
    where it is in the rest (risers, sub drops, waterphone, scrapes, music
    box, toy piano, pulses, breath, whispers, choirs, heartbeats, cluster
    glissandi), whose first knob is not a tone.
  - *Space.* **Global horror space** (UTIL, 16 units) is the bus they are
    made for: aux 2/3 into a large dark hall (reverb unit: 8 s, the highs
    dying three times faster, the lows 1.4 times slower, 62 ms predelay,
    `mod` 48 against ringing, input at 0 dB as long tails pile up, tail
    width 100 % instead of 150 %), aux 6/7 into the `Horror echoes` module
    (a dotted quarter, 60 % feedback, each echo darker and thinner, moving
    between the sides) of which a half goes on into the hall. **Global
    horror space mastering** (23 units) adds a low cut at 20 Hz, a slow
    1.5:1 compressor and a limiter at -1 dB without drive: it keeps the
    dynamics. The presets send with `outaux`; `auxgain` is set relative to
    `outgain` (equal: the hall about as loud as the dry sound), as `outaux`
    sends do not follow `outgain`. Measured as the energy of the hall
    against the dry sound: -12 dB for the sub rumble and the col legno,
    -5 to -8 dB for strings, braams and drones, about 0 dB for the
    waterphone and the distant piano, +3 dB for the distant bell. The
    impacts (FX Horror impact, deep, metal, boom distant, hit) have a hall
    of their own instead (one voice: 0.8 MB of state).
  - *Levels.* A held note of a drone -20 LUFS (integrated), a chord of
    three strings or pads -18, glass -22, breath -24, whispers -23.5,
    the Shepard tones -18; peaks: braams and impacts -3 dB, risers and
    reverse swells -4, sub drops and pianos -6, music box and toy piano -8.
    Through Global horror space mastering no render peaked above -0.9 dB.

  Measured with the Go synth (a note, chord or phrase each; loudness after
  ITU-R BS.1770). Columns: voices, units (after expansion), level (LUFS
  integrated / peak dB), correlation of left and right and the loudness lost
  in mono (dB), and how many voices one core of an Apple M3 Pro runs in real
  time (every voice runs all the time, sounding or not).

  | Preset | Voices | Units | What it is for | Listen for | Level | Corr / mono | Voices a core |
  |---|---|---|---|---|---|---|---|
  | PAD drone sub rumble | 2 | 32 | the floor under a scene: a sub and a moving rumble of noise | the rumble breathing without a pattern; on small speakers, whether intensity makes the sub audible | -20 / -10 | 1.0 / -0.1 | 33 |
  | PAD drone beating | 2 | 29 | dark detuned saws beating slowly, the low-pass breathing | the speed of the beating; dissonance turning it sick | -20 / -8 | 0.4 / -2.1 | 29 |
  | PAD drone cluster | 2 | 33 | a Ligeti cluster of four tones a semitone apart, each swelling by itself | whether the shimmer feels alive or busy; intensity from soft to reedy | -20 / -11 | 0.9 / -0.2 | 30 |
  | PAD drone tritone | 2 | 27 | a dark organ: note, tritone, octave below, moving pulse widths | the slow phasing of the pulses | -20 / -8 | 0.8 / -0.7 | 41 |
  | PAD drone metal | 2 | 27 | a grinding bell-like drone: ring modulation and two combs | the inharmonic tones gliding (movement), the ring of the combs (intensity) | -20 / -8 | 0.9 / -0.2 | 42 |
  | PAD drone abyss | 2 | 25 | a resonant growl an octave below, the pitch drifting out of tune | the seasick drift; whether the growl at intensity 128 is too much | -20 / -10 | 1.0 / -0.1 | 38 |
  | PAD glass harmonica | 6 | 22 | a glass harmonica: pure, piercing, a séance | the tremolo; whether it pierces too much | -22 / -15 | 1.0 / -0.1 | 58 |
  | PAD glass rub | 6 | 22 | a wine glass rubbed with a wet finger, beating | the squeak (friction) | -22 / -13 | 0.8 / -0.8 | 57 |
  | PAD choir moan | 6 | 26 | a low wordless choir moaning oo and ah | the vowel moving; chords | -18 / -5 | 0.7 / -0.9 | 41 |
  | PAD choir ghost | 6 | 26 | a high breathy choir of children or ghosts | the breath | -19 / -3 | 0.3 / -1.7 | 42 |
  | PAD pad dissonant | 4 | 28 | a slow pad, every note against the semitone above | chords turning sour | -18 / -5 | 0.7 / -0.8 | 24 |
  | PAD pad dark | 4 | 28 | a dark muffled pad, slowly breathing | warmth | -18 / -5 | 0.7 / -0.8 | 23 |
  | PAD pad hollow | 4 | 28 | a hollow pad with squares an octave below | emptiness | -18 / -5 | 0.8 / -0.7 | 24 |
  | ST strings tremolo | 6 | 27 | the standard suspense bed: a section in tremolo | the realism of the bow strokes; speed and depth | -18 / -5 | 0.6 / -1.0 | 28 |
  | ST strings tremolo frantic | 6 | 27 | the same bright, fast and deep, a section a semitone above: panic | harshness at the top of the range | -18 / -6 | 0.1 / -2.4 | 27 |
  | ST strings tremolo distant | 6 | 27 | the same dark, slow, soft, swelling in: unease far off | whether it sits behind the mix | -21 / -8 | 0.7 / -0.9 | 28 |
  | ST strings ponticello | 6 | 26 | sul ponticello: thin, glassy, hissing, quick tremolo | the glassy whistle (glass) and the bow hiss; it is meant to be uncomfortable | -18 / -6 | 0.2 / -2.2 | 33 |
  | ST strings ponticello sustained | 6 | 26 | ponticello without tremolo, slowly bowed | the whistle on long notes | -18 / -8 | 0.2 / -2.2 | 34 |
  | ST strings col legno | 8 | 22 | col legno battuto: a dry pitched click for ticking ostinatos | the click against the pitch; repeated 16ths | -32 / -8 | 0.9 / -0.3 | 64 |
  | ST strings col legno ringing | 8 | 22 | the same, a hollow tuned knock | how long the knock rings | -29 / -8 | 0.9 / -0.3 | 62 |
  | ST strings cluster gliss up | 4 | 24 | Penderecki: a quarter-tone cluster sliding up 6 semitones in 5 s | the scream of the slide; gliss and time | -18 / -5 | 0.6 / -1.0 | 26 |
  | ST strings cluster gliss down | 4 | 24 | the cluster sliding down 8 semitones in 9 s | the sinking | -18 / -6 | 0.6 / -1.1 | 26 |
  | ST strings cluster | 4 | 24 | the cluster without a slide: a dense block | its width (dissonance) | -18 / -4 | 0.7 / -0.8 | 26 |
  | ST strings screech | 4 | 27 | Psycho stabs: high strings scooping into a harsh accent | the scoop and the attack on short repeated notes at C-5 to C-7 | -13 / -6 | 0.1 / -2.5 | 25 |
  | ST strings screech cluster | 4 | 27 | the screech with a semitone above, driven: a shriek | harshness | -13 / -6 | 0.2 / -2.4 | 25 |
  | ST strings low swell | 4 | 28 | cellos and basses in a 3 s crescendo that opens up | the brightening as it grows; something approaching | -18 / -6 | 0.7 / -1.0 | 26 |
  | ST strings low swell slow | 4 | 28 | the same over 8 s with a semitone above: dread | the long build | -18 / -2 | 0.8 / -1.0 | 26 |
  | ST strings harmonics | 6 | 22 | violin harmonics flautando: pale, airy, wide | the air (bow noise) and the faint tritone | -20 / -10 | 0.1 / -2.4 | 39 |
  | ST strings staccato | 8 | 21 | short accented low strings for chase ostinatos | the accent at 8ths and 16ths; length | -22 / -8 | 0.6 / -1.5 | 35 |
  | FX shepard riser | 1 | 62 | an endless rise (Dunkirk), an octave in 6 s | whether the rise feels endless or you hear tones entering at the bottom | -18 / -8 | 0.7 / -0.6 | 22 |
  | FX shepard riser fast | 1 | 62 | the endless rise, an octave in 1.5 s, saws | panic; the same illusion at speed | -18 / -7 | 0.7 / -0.6 | 22 |
  | FX shepard falling | 1 | 62 | an endless fall, triangles | the sinking illusion | -18 / -9 | 0.7 / -0.6 | 22 |
  | FX riser cluster | 1 | 26 | a cluster of saws climbing 12 semitones in 8 s while it opens and swells | release where the hit lands; the last second | -14 / -4 | 0.3 / -2.3 | 31 |
  | FX riser cluster long | 1 | 26 | the same over 16 s, 24 semitones | the long build | -16 / -4 | 0.4 / -2.0 | 32 |
  | FX riser noise | 1 | 17 | a rising wind of noise, 6.5 s | the swell into the hit | -16 / -6 | 0.3 / -1.8 | 86 |
  | FX riser noise whistle | 1 | 17 | the noise riser narrow, with a rising sine, 10 s | the whistle | -17 / -6 | 0.3 / -1.7 | 85 |
  | FX reverse swell | 1 | 23 | a reversed cymbal, 3 s, stopping dead | the suck before a hit; place the note 3 s ahead | -14 / -4 | 0.3 / -1.8 | 43 |
  | FX reverse swell tonal | 1 | 23 | a reversed chord (note and fifth), 4 s | the chord rushing in | -16 / -4 | 0.5 / -1.6 | 42 |
  | FX reverse swell long | 1 | 23 | a long reversed swell of bright noise, 8 s | the length | -14 / -4 | 0.3 / -1.7 | 43 |
  | FX braam | 2 | 25 | the trailer braam: brass and tuba blaring open and closed | the blare and its tail at G-1/G-2 | -18 / -3 | 0.8 / -0.9 | 24 |
  | FX braam dissonant | 2 | 25 | the braam with a semitone and a tritone | ugliness | -18 / -3 | 0.8 / -1.0 | 24 |
  | FX braam distorted | 2 | 25 | the braam driven hard, wide open | the rasp | -18 / -3 | 0.8 / -1.1 | 23 |
  | FX braam swell | 2 | 25 | the braam crescendo over 2 s | the approach | -19 / -3 | 0.8 / -1.0 | 24 |
  | FX impact | 1 | 20 | a cinematic impact into its own 8 s hall | the drop and the tail | -18 / -3 | 0.3 / -2.3 | 40 |
  | FX impact deep | 1 | 20 | a slow, deep impact into an 11 s dark hall | the sub on big speakers | -19 / -3 | 0.6 / -1.1 | 40 |
  | FX impact metal | 1 | 28 | the impact with a clang of inharmonic partials | the clang | -18 / -3 | 0.4 / -2.3 | 28 |
  | FX boom distant | 1 | 20 | a boom far away, mostly hall | distance | -22 / -6 | 0.1 / -3.1 | 39 |
  | FX hit | 1 | 20 | a short hard hit in a short room: a slammed door | the punch | -24 / -4 | 0.8 / -0.4 | 40 |
  | FX sub drop | 1 | 10 | a clean sine falling two octaves in 1.5 s | under impacts and cuts; on big speakers | -14 / -6 | 1.0 / 0.0 | 99 |
  | FX sub drop long | 1 | 10 | three octaves in 4 s | the same | -14 / -6 | 1.0 / 0.0 | 99 |
  | FX bowed metal | 2 | 41 | a bowed cymbal: singing inharmonic partials | the partials swelling by themselves; the bow noise | -20 / -9 | 0.7 / -0.8 | 28 |
  | FX bowed metal screech | 2 | 41 | a cymbal bowed hard: a shriek of metal | harshness | -20 / -9 | 0.6 / -1.3 | 28 |
  | FX bowed metal gong | 2 | 41 | a bowed gong, dark and slow | played low | -20 / -7 | 0.9 / -0.4 | 28 |
  | FX waterphone | 4 | 35 | a bowed waterphone: high rods wailing, sliding in pitch | the slides (water) in the hall | -20 / -7 | 0.8 / -0.4 | 37 |
  | FX waterphone struck | 4 | 35 | a struck waterphone | the slide as it decays | -21 / -8 | 0.8 / -0.4 | 36 |
  | FX scrape | 2 | 23 | metal on metal: a dragged blade, a gate | the jumping band and the stutter | -20 / -6 | 0.1 / -2.5 | 66 |
  | FX scrape screech | 2 | 23 | a high fast scrape with a screech at the note: nails on a blackboard | unpleasantness | -20 / -6 | 0.1 / -2.5 | 67 |
  | FX scrape grind | 2 | 23 | a slow low grind: a heavy door, a coffin lid | the grip | -20 / -6 | 0.1 / -2.4 | 65 |
  | FX metal resonance | 4 | 44 | a struck plate or pipe: a clank and a 10 s ring | the ring | -23 / -6 | 0.9 / -0.2 | 27 |
  | FX metal resonance short | 4 | 44 | the same shorter and harder: a boiler room | the clank | -24 / -6 | 0.9 / -0.4 | 28 |
  | FX bell distant | 2 | 51 | a church bell far away, its minor-third tierce | distance (the high cut) and the hall | -23 / -8 | 0.9 / -0.2 | 18 |
  | FX bell near | 2 | 51 | the bell close by | the strike | -21 / -6 | 0.9 / -0.2 | 18 |
  | FX breath | 1 | 20 | slow breathing close to the ear | whether it reads as breath | -24 / -9 | 0.4 / -1.5 | 90 |
  | FX breath panic | 1 | 20 | fast panicked breathing | the same | -24 / -9 | 0.4 / -1.5 | 89 |
  | FX whispers | 1 | 46 | wordless whispering voices, one each side | whether it reads as voices; speed | -23 / -6 | 0.6 / -1.1 | 40 |
  | FX whispers frantic | 1 | 46 | the whispers fast and urgent | the same | -24 / -6 | 0.6 / -1.1 | 40 |
  | FX wind | 1 | 24 | a cold wind howling in gusts | the howl | -22 / -2 | 0.4 / -1.5 | 78 |
  | KEYS music box | 8 | 34 | a music box, a little warped: innocence gone wrong | the wow; a lullaby at C-4 to C-7 | -17 / -8 | 0.9 / -0.1 | 32 |
  | KEYS music box detuned | 8 | 34 | a music box out of tune, beating | the beating of the second tine | -18 / -8 | 0.9 / -0.1 | 32 |
  | KEYS music box warped | 8 | 34 | a music box on a melted tape, the notes sagging | whether the wow is too seasick | -18 / -8 | 0.9 / -0.1 | 31 |
  | KEYS toy piano | 8 | 31 | a toy piano in an empty nursery | the clunk and the inharmonic ring | -22 / -8 | 1.0 / -0.1 | 33 |
  | KEYS toy piano detuned | 8 | 31 | the toy piano broken and ringing | the detune | -21 / -8 | 1.0 / -0.1 | 33 |
  | KEYS piano distant | 8 | 23 | a piano in another room, blurred by the hall | distance | -24 / -6 | 0.7 / -0.7 | 62 |
  | KEYS piano detuned | 8 | 22 | an old piano, its two strings a quarter tone apart | the beating; it is not a realistic piano | -24 / -6 | 0.2 / -2.4 | 64 |
  | KEYS piano prepared | 8 | 28 | a prepared piano, bolts rattling and buzzing | the buzz on low clusters | -29 / -6 | 0.2 / -2.4 | 49 |
  | KEYS piano low | 8 | 22 | the lowest piano notes struck hard: stingers, ostinatos | the thud | -28 / -6 | 0.5 / -1.8 | 62 |
  | DR heartbeat | 2 | 16 | lub-dub, one note a beat | tempo up for fear | -23 / -6 | 1.0 / 0.0 | 87 |
  | DR heartbeat muffled | 2 | 16 | a heartbeat from inside the body | whether it is felt | -24 / -8 | 1.0 / 0.0 | 87 |
  | DR pulse sub | 1 | 19 | a throbbing sub pulse at a free rate | set rate to the tempo | -18 / -2 | 1.0 / 0.0 | 76 |
  | DR clock tick | 2 | 21 | a clock in an empty house: tick and tock on two notes | the tick | -38 / -10 | 1.0 / -0.0 | 62 |

  **What was measured, besides the table.**
  - *Every knob* at 0 and 128, one at a time, on every module: nothing above
    -0.5 dBFS, no offset above 0.002, no NaN; each changes the loudness or
    the spectrum, except the ones that move things in time (speed, vibrato,
    wow, clank, rate) and those whose 0 is the default; those were checked
    on the pitch or the level over time.
  - *Movement and loops.* 60 s of each drone, pad, choir and texture: the
    correlation of the octave-band levels with themselves at lags of 2 to
    30 s is 0.1 to 0.6 (0.76 for the metal drone, whose bands move by
    0.4 dB); the drones with LFOs at fixed rates (beating, cluster,
    tritone) are the 0.56 to 0.59. The filter movements are an LFO plus a second one 1.6 times as
    fast plus low-passed noise, so they do not repeat.
  - *Shepard tones.* The pitch classes rotate steadily, +2.0 semitones a
    second for Horror shepard riser (an octave in 6 s), +8.0 for the fast
    one, -2.0 for the falling one, while the centre of the spectrum stays
    between 260 and 390 Hz: the rise does not arrive anywhere.
  - *Pitch.* Cluster gliss up: C-2 rises 6 semitones in 5 s; down: C-3
    falls 8 in 9 s. Sub drop: from 131 Hz to the note (C-1) in 1.5 s; long:
    262 to 50 Hz in 4 s. Music box warped: the pitch wanders by up to half
    a semitone. Waterphone: the rods slide by about a semitone.
  - *Aliasing.* For the brightest strings at the top of their range
    (screech at C-7, ponticello and frantic tremolo at C-5) the energy
    between the harmonics is -11 to -24 dB, and is the same with the
    oscillators' `bandlimit` off: it is the bow noise, not aliasing.
    `bandlimit` lowers what lies above 16 kHz by 2 to 9 dB.
  - *Clicks and ends.* No note starts with a step above what its attack
    makes; every preset is silent (below -60 dB) within 20 s of its note
    off.

  **What the measurements changed.**
  - *Ladders after resonance boosts* made an offset of 0.005 and 13 Hz
    wobble (the ponticello: +15 dB of resonance into the saturator of a
    ladder). That high cut is a `filter` now; ladders that are driven on
    purpose have a low cut after them.
  - *Sines detuned between the sides* (partials of metal, glass) cancel in
    mono, losing 3 dB: such partials are mono and panned apart instead.
    Detuned saws at `width` above 52 went negative in correlation at high
    notes; the string sections have `width` 40 to 52.
  - *`hold` on noise is not random at slow rates*: with `holdfreq` 1, 2 or
    4 the hold samples the shared noise generator every 2^14, 2^12 or 2^10
    samples, and those samples of a multiplicative generator step almost
    linearly: the "random" gusts of the wind repeated every 9 to 30 s (a
    correlation of 0.85). Slow random movement is low-passed noise raised
    by a `dbgain` instead.
  - *A send to `delaytime`* moves it by 32767 samples per 1: a chorus needs
    an LFO with a small `gain` (8: 0.7 ms) and amount 65.
  - *The reverb tail at its default width* correlates by -0.4 between the
    sides; the hall has `tailwidth` 64.

  Known: Horror boom distant is mostly hall and loses 3.1 dB in mono;
  ponticello, scrape, glass rub and the noise risers have -18 to -32 dB of
  their energy above 16 kHz, as their bow noise and friction are meant to
  be harsh (the reverse swells, -8 dB, are cymbals); the glissando moves in
  steps of 2 semitones of its knob; LFO rates are in Hz, not synced to the
  tempo; Horror heartbeat muffled peaks at -8 dB at full gain. A knob set
  low and a soft note together can push a parameter past the end of its
  range (the muffled heartbeat went silent at velocity 64 when velocity
  turned its tone down; it now follows velocity in level only). Tests:
  `tracker/horror_test.go` (each expands, compiles, renders a note without
  NaN below full scale, the same at velocity 127 as without velocity and
  4 to 12 dB lower at 64; shared modules are equal; every module parameter
  is bound; with `SOINTU_TEST_LONG=1` also silent 20 s after the note).
- **Hard techno screeches:** 11 instrument presets in LEAD whose names
  start with `Hard techno screech`, for hard techno leads and stabs at C-4
  to C-6 (acid and hoover an octave lower). All share one chain: detuned
  oscillators with a pitch dive (or rise) into the note, a resonant ladder
  opened by an envelope, a first distortion, then a short reverb *before*
  a second distortion, so the reverb roars with the note, as hard techno
  screeches do. Before the second distortion a low-pass and two bell peaks
  pick the harmonics that scream; after it a high-pass (about 400 Hz, above
  the kick and the bass), ott, a bell that tames the harshest band, width
  (mono below about 250 Hz) and outaux to the song's reverb. Mono up to the
  reverb: stereo unison and a wide reverb into a distortion put the side
  above the mid (6.8 dB lost in mono in the first try). 3 or 4 voices, so
  the reverb of a note rings on under the next.
  - screech: four detuned saws and three squares an octave up, an octave
    dive over about 80 ms.
  - dive: two octaves down into the note over about 200 ms.
  - rise: up an octave into the note over about 0.45 s, slow attack: for
    held notes.
  - roar: a long, loud reverb into the distortion, 4 voices.
  - siren: a sine vibrato of ±4 semitones at about 4 Hz (not synced).
  - acid: one saw through a ladder at the edge of self-oscillation,
    snapped open.
  - metal: a sine phase-modulated at a ratio that is not whole (+19 st),
    through a comb at the octave of the note.
  - formant: the scream peak swept between vowels by a slow triangle.
  - yell: the scream peak sawed up and down at about 5 Hz.
  - stab: short, no sustain, two octaves of dive, the reverb carries it.
  - hoover: detuned pulses and saws an octave down, a swoop up into the
    note.

  Measured with the Go synth on a riff at C-4 to G-4, 150 BPM:

  | | LUFS-S max | true peak dB | mono loss dB | side/mid 1-5 kHz dB | centroid Hz |
  |---|---|---|---|---|---|
  | screech | -12.9 | -6.3 | 1.0 | -5.6 | 2057 |
  | dive | -12.4 | -7.6 | 1.2 | -5.0 | 2172 |
  | rise | -13.0 | -6.1 | 0.9 | -5.9 | 1940 |
  | roar | -13.2 | -7.3 | 1.4 | -4.4 | 1963 |
  | siren | -12.8 | -7.2 | 1.1 | -5.3 | 2155 |
  | acid | -13.2 | -5.4 | 0.2 | -12.1 | 1379 |
  | metal | -13.2 | -7.8 | 1.3 | -4.5 | 2167 |
  | formant | -12.7 | -6.5 | 1.0 | -4.2 | 1191 |
  | yell | -13.0 | -6.3 | 1.0 | -5.6 | 1884 |
  | stab | -13.2 | -8.4 | 1.4 | -4.1 | 1862 |
  | hoover | -13.3 | -6.3 | 0.8 | -5.8 | 1297 |

  The level holds within 3 dB from C-3 to C-7. About 20 voices per core.
  Aliasing: on a held C-4 without unison, dive or reverb, -21 to -24 dB
  of the energy lies between the harmonics above 6 kHz (a bare
  bandlimited saw: -39 dB). The two distortions are hard, and softclip
  oversamples only twice; a low-pass before each one (the ladder before the
  peaks) keeps it there. Darker settings measured -33 dB, but no longer
  screamed (centroid 1.5 kHz). 4x oversampling in softclip would lower it.
  Not heard, not tested in the wasm player. Test:
  `tracker/screech_test.go` (each expands, compiles, peaks below -3 dB,
  has the side under the mid, no offset, and is silent 3 s after the
  note).
- **Drum bus** (UTIL): Global mastering 2 drumbus, and Global mastering 2
  drumbus reverb with the reverb unit (15 units; 37 with the Reverb
  module), are Global mastering 2 with a
  group bus for the drums in front. The channels: 0/1 the mix, 2/3 the
  reverb send, 4/5 left free for the bus that Kick ducker ducks, 6/7 left
  free for the delay send, 8/9 the drum bus. The bus was on 6/7, where it
  could not be used with the delay presets; on 8/9 it renders the same
  (`TestDrumBusChannel`), and makes these presets Go synth and wasm player
  only also for their channels (see [Output channels](#output-channels)).
  - The drums send to the bus with an `aux` unit, channel 8 (aux4 left), in
    place of their `out` unit. The drum presets come with `out`, so that a preset
    makes sound in any song, also one without this Global instrument; the
    comment of each says which unit to change, and `gain` stays as it is.
  - Global mastering 2 buses is the Global preset with every bus, for a
    song that uses the four of them: aux 6/7 through the `Ping pong delay`
    module, a quarter of its repeats on to the reverb; aux 2/3 through the
    reverb unit; the drum bus on aux 8/9; then Global mastering 2. 27 units
    (49 with the Reverb module in place of the reverb unit). The bus on
    4/5 is read by the Kick ducker itself, which comes before this
    instrument. `examples/buses.yml` has a bass on 4/5, a stab sent to the
    reverb and the delay, a hat and the kick on the drum bus, and this
    preset; without what is sent to any one of the four buses it renders
    differently (`TestBusesExample`).
  - Global mastering 2 buses ducking is the same with the delay and the
    reverb ducked: aux 6/7 through the `Ducking delay` module, a quarter of
    its repeats on to the reverb; aux 2/3 through the `Ducking reverb`
    module; the drum bus on aux 8/9; then Global mastering 2. 33 units (55
    with the Reverb module), three modules. It is Global mastering 2
    ducking with the drum bus chain between the reverb and the master
    chain: with nothing sent to the drum bus it renders exactly what that
    preset renders, and with only the drum bus sent to, exactly what Global
    mastering 2 buses renders (`TestBusesDuckingPreset`). Go synth and wasm
    player only, like the others with the drum bus.
  - The Global instrument reads the bus (`in`, channel 8), compresses it
    (about 2:1 above -11 dB, 20 ms attack, 93 ms release, +2 dB makeup),
    clips it softly (`softclip`: +3.2 dB drive, knee at -6 dB) and adds it
    to the mix (`out`, gain 108) before the mix is read and mastered.
  - Measured on the drums of the example loop: on the main output they peak
    at +2.8 dB with -14.9 dB RMS (crest 17.7 dB); through the bus at
    -1.5 dB with -13.6 dB RMS (crest 12.1 dB): 4.3 dB less peak, 1.3 dB
    more level. The whole loop comes out of the master at -9.0 dB RMS
    either way, as the limiter sets that.
- **Examples:** `examples/soundset.yml` plays every Club preset in turn,
  with the preset Global reverb and no mastering, for listening and
  measuring: in patterns of 1.7 s, kick (0), hard kick (1-2), snare (3),
  clap (4), closed hat (5), open hat (6), ride (7), crash (8-9), tom (10),
  sub (11-12), acid (13-14), reese (15-16), dist bass (17-18), lead
  (19-20), hoover (21), pad (22-24), pluck (25), riser (26-29), noise
  sweep (30-33), downlifter (34-37), impact (38-39).
  `examples/soundset_loop.yml` is a loop of kit, acid bass and lead chords
  with the drums on the bus, through Global mastering 2 drumbus reverb.
  Their instruments are the presets (`TestSoundsetExamplesUseThePresets`).
  Compiled for wasm they are 7.5 and 6.2 KB (3.7 and 3.5 KB gzipped); with
  the Reverb module in place of the reverb unit they were 9.6 and 8.3 KB
  (4.2 and 4.1 KB).
  Tests: `tracker/soundset_test.go`, and `vm/compiler/wasm_soundset_test.go`
  (every preset and both songs render in the wasm player exactly as in the
  Go synth; the long song takes most of two minutes and runs only with
  `SOINTU_TEST_LONG=1`, see [Tests](#tests)).


## Buffers as delay lines

A `bufwrite` unit and `bufread` units make what the `delay` unit does, in
parts: the write head of a ring is the input of a delay line, and a tap
reads it some time later. Go synth and wasm player, which render the same
(`vm/compiler/wasm_buffeatures_test.go`).

- **Tap** (`bufread` `mode` tap). A play head starts at `start` when its
  note is triggered and goes on from there at its speed; a tap reads the
  frame at `start` every sample, counted from the ring as it is then. A
  negative `start` is that many frames behind the write head: -1 is the
  frame written last, so a tap before the `bufwrite` unit in the same voice
  is a delay of that many samples, and the two with a gain between them a
  feedback loop. The tap is interpolated, follows a modulation of `start`
  while it sounds (a chorus or flanger; the modulation is in parts of the
  length of the buffer, as for a play head) and sounds without a note, as
  in a bus instrument. A tap further behind the write head than the ring
  has frames is silent, like a delay line that has not run that long.
  `note`, speed, the loop, the edge fade and the slices are not used, and
  not shown; transpose and detune only with pitch timing.
- **Timing** (`bufread` `timing`). frames: as before. pitch: `start` is in
  frames at note 0 and divided by 2^(note/12), like a pitch tracked delay
  time: 10788 is one period of the note, and parts of frames count, which
  the delay unit rounds. BPM: `start`, `loopstart` and `looplength` are in
  48ths of a beat, like the delay times: 48 is a quarter note, 36 a dotted
  eighth. They are made frames when the patch is compiled
  (`vm.BPMFrames`), so the tempo costs nothing while playing. The tracker
  shows the values as what they count, and edits them on a grid like the
  delay times (`tracker/bufreadgrid.go`): with pitch timing ±1/4, 1/3, 1/2,
  2/3, 3/4, 1, 1.5, 2 … 16 periods, then 1.5·2^k and 2^k; with BPM timing
  the note lengths of the delay, then whole bars. A drag and the arrows move
  from one grid value to the next, a large step between whole periods,
  straight notes and bars. The `free` switch that the unit has then (a
  setting of the tracker, kept with that of the delay units in
  `Model.gridFree`) takes any value; a unit starts free if a position is
  off the grid when it is first seen.
- **Crossfade at the write head** (`edgefade`). `edgefade` fades a play head
  near the edges of the valid frames. In a ring that is full the edges meet
  at the write head, and a play head that the write head overtakes (any
  speed below 1) or that overtakes it used to fade out and in again. There
  the fade is now a crossfade: within `edgefade` frames of the write head,
  the frames on the other side of it are mixed in with the weight that the
  fade takes away, and when the heads cross, the play head goes on
  `edgefade` frames further, where the crossfade has brought the sound. In
  such a ring the position also wraps around the end, instead of the note
  ending after one round. Samples, one shot recordings and rings that are
  not full yet have edges, and fade as before. This changes how songs with
  an edge fade on a ring sound.
- **A buffer of its own** (`bufwrite` without a buffer). The tracker gives a
  `bufwrite` unit with buffer 0 a buffer (`Model.fixWriteBuffers`,
  `Buffer.Auto`), as long as its `length` parameter and stereo if the unit
  is, and deletes it when no unit refers to it anymore. A `bufread` unit
  with buffer 0 gets the buffer of the nearest `bufwrite` unit before it
  among its units, or else after it. A buffer that the user made keeps its
  own length.
- **A copy per voice** (`bufwrite` `pervoice`). All voices of an instrument
  share a buffer and mix into it. With `pervoice`, every voice has its own
  copy, which the `bufread` units of that voice read: the lines of a delay
  unit, e.g. a plucked string per note with a pitch timed tap. Units of
  other instruments read the copy of the first voice, which is also the one
  the Buffers tab shows; the others are in the synth alone, are empty again
  after a change of the buffer's size, and cost the length of the buffer
  each.
- **Panic clears them.** What `bufwrite` units recorded survives a new
  synth (after an edit of the patch), but not a panic: the player clears
  the writable buffers and their reported fill, as after a NaN, and the
  model drops the spectra it shows, which the next synth reports afresh.

What the `delay` unit has and this does not: its damping is a one-pole
low-pass, the `filter` unit a two-pole one; its eight lines per channel
would be eight buffers and loops, more than the 63 units of an instrument
for a reverb; and it runs in the x86 players.

**A tap in the tracker.** `mode` is a menu (one shot, loop, ping-pong,
tap), as every parameter of two named values is (before, a switch). The start
of a tap is shown as `delay`: the frames behind the write head, without
the sign of the negative value that is stored; its range ends at -1, and
its knob, arrows and grid turn the other way, up being a longer delay. With
pitch timing, `transpose` and `detune` stay shown and tune the delay: the
start is divided by 2^((note + transpose + detune)/12), in the Go synth
and the wasm player alike; with other timings, and in the other modes,
they do as before.

**Module presets with a tap.** `Plucked string` (13 units, no inputs): a
noise burst (`burst`, high cut `pick`), or with `cycle` one cycle of a sine
at the note (a window of one period, tuned with the string), crossfaded,
into a pitch timed tap one period
long, tuned by `tune` and `fine`, round a `ladder` low-pass (`tone`, 4.5
to 19 kHz) with the loop gain `decay` (0.94 to 1), a voice its own line.
`Comb filter` (4 units, stereo): the same loop on its input, `feedback`
up to 97 %. The loop filter is a ladder without resonance, which never
raises the level: the `filter` unit peaks about 1.25 dB at its cutoff even
at its least resonance, and a loop that gives back nearly all it gets
grows without end through it. The ladder delays the loop, so high notes
are flat when `tone` is low: C5 12 cents at the default, 32 at `tone` 64;
the decay is per round of the loop, so high notes ring shorter (at the
defaults, C1 3 s, C3 2 s, C5 0.2 s to -60 dB).

**A window of periods.** `window` has `timing` like `bufread`: frames
(as before, `length`), pitch or BPM. Timed, its length is `time`, shown as
`length` on the grid of a `bufread` position with the `free` switch: with
pitch, in frames at note 0 (10788 is one period), divided by
2^((note + transpose + detune)/12); with BPM, in 48ths of a beat. A trigger
clears the voice, so an oscillator starts every note at its `phase`: a
window of N periods with shape 0 cuts N whole cycles out of it, e.g. one
cycle of a sine as the exciter of a pluck or a drum. The length is taken
when the note starts, so modulating the pitch afterwards does not move the
end. Windows timed in frames are encoded as before; when one is timed, the
window units get the time as a four byte operand (0 for those in frames,
the frames at note 0 negated with pitch, the frames with BPM). Go synth
and wasm player; the tracker gives windows from before timing `time`,
`transpose` and `detune` of a new unit.

## Slices and sample editing

Drum breaks: an audio buffer has slice markers, and a `bufread` unit plays
from one. Go synth and wasm player, which render the same
(`vm/compiler/wasm_slices_test.go`).

- **Markers** (`Buffer.Slices`, saved as a flow list): frames from the
  oldest valid frame, increasing, each above 0. Frame 0 is always the start
  of slice 0, so n markers make n+1 slices; the last ends at the newest
  valid frame. A sample or a buffer that bufwrite units record into can
  have them. They reach the synth with the audio (`BufferAudio.Slices`,
  attached by `PlayWithBuffers` and by the tracker when it sends the
  audio), so editing them does not make a new synth.
- **`slices`** off, continue or stop; **`slice`** 0 to 127, modulatable;
  **`note`** slice, or slice and pitch, to pick it by the note. The slice
  is `slice`, with those plus note − 60 (C-3 is slice 0, chromatic up, as
  in samplers), plus the
  modulation of `slice` times 128, rounded to the nearest (ties to even):
  a `loadnote` sent at amount 96 (×0.5) adds note − 64 exactly. It wraps
  around the number of slices, so that every note and modulation plays a
  slice. It is read when the note is triggered, like `start`. With
  `note` slice the pitch does not follow the note; with slice and pitch
  it does too, C-3 at the recorded pitch. `transpose` and `detune` apply
  with every value of `note`.
- **`start`** is an offset from the marker, timed as before (frames,
  pitch, BPM); negative reaches back before it. Its modulation still
  shifts it by parts of the buffer's length.
- **The loop** is in the slice: `loopstart` and `looplength` are in 128ths
  of the slice's length (`sointu.BufreadSliceParts`; 128 the whole slice,
  shown in percent), their modulation in parts of the slice's length too;
  the loop start is clamped to the slice and the loop length to what is
  left of it. With BPM timing they are not converted; only `start` is.
- **Stop at the next marker** (`slices` stop): frames outside the slice
  read as silence, and `edgefade` measures to the slice's edges as well,
  at both ends (a fade in at the marker, a fade out before the next).
  continue plays on as before.
- **Not with**: a tap (`mode` tap) has no slices; a sliced unit does not
  crossfade at the write head of a full ring (no `BufferRegionRingRead`).
  Without slices, `note` slice does nothing and slice and pitch is pitch.
  Old songs have `slices` 0 and play unchanged.
- **Bytecode**: `BufferRegionSliced`, `BufferRegionSliceNote`,
  `BufferRegionSliceStop` in the flags of the region, and `slice` in bits
  24 to 30 (the first voice of a buffer per voice, bits 16 to 23, is now
  masked). `slice` is `NoTransform`, so bufread units have no new operand.
  State: `state[7]` (WRK offset 28) is the slice; port 7 is the
  modulation of `slice`, cleared by the unit. In the wasm player, the
  regions are 8 i32s instead of 7 when the song has sliced units, the
  eighth the offset of the buffer's markers in `su_slices` (their number,
  then the markers); the code for note, stop, loop and modulation is only
  there when the song uses them.

**In the tracker.** The waveform of the Buffers tab shows the markers with
a handle, and the unit preview of a bufread unit shows them as lines.
ctrl/cmd+click adds a marker, dragging a handle moves it (one undo step, on
release), right-clicking one deletes it, shift+drag selects a range and
shift+click clears it; plain dragging pans as before. Below the waveform:
Slice equally (2 to 128 slices), Detect transients (sensitivity 0 to 100),
Clear. Detect transients finds where the level of the audio with its highs
emphasized (the energy plus 4 times that of its first difference, in
windows of 256 frames every 128) rises above that of the 4 windows before
it by a threshold, 15 dB at sensitivity 0 to 1.5 dB at 100, ignoring
windows 40 dB below the loudest, at least 40 ms apart; the marker is 16
frames before the first frame of the window that reaches a quarter of its
peak. Marker edits are undoable. A sample replaced by an import loses its
markers.

**Markers in the file.** An imported WAV or AIFF file brings its markers
as slice markers (`samplemarkers`, `tracker.FileSlices`), as ReCycle,
sample editors and sample packs mark the slices of loops: the cue points of
a WAV file (`cue ` chunk, `dwSampleOffset`; names from `LIST adtl labl` are
read but not kept) and the markers of an AIFF file (`MARK`), scaled from
the file's rate to 44100 Hz; a marker at 0 is dropped. ffmpeg drops these
chunks, so the file is read before it is converted. The import and its
markers are one undo step, from the import button, the sample browser and
drops alike. The loop points of a WAV `smpl` chunk are a loop,
not slices, and are not read. An ACID loop (`acid` chunk, not a one shot)
without markers gets none, but Slice equally is set to its beats, so that
one click slices it per beat; an alert says so. REX files (.rex, .rx2, .rcy, from
ReCycle) are rendered with the REX Shared Library of Reason Studios, a
closed, licensed library that only the tracker uses (package `rex`, build
tag `rex`): each slice at 44100 Hz where it is in the loop, at the loop's
tempo (positions in 15360ths of a quarter note), mixed where slices
overlap, as long as the loop; the slice starts become the markers, and the
audio is stored as FLAC like an edited sample. The song and the players
never need the library. Builds without it refuse REX files, also in the
browser's preview.

`make REX=1` builds the tracker and the plugins with it (macOS): it
downloads the REX SDK 1.9.2 into `.rexsdk/` (ignored by git; `REXSDK=...`
uses an SDK unpacked elsewhere), compiles the SDK's `REX.c`, which loads
the library at run time, through cgo (`rex/rex_sdk.go`, tag `rex`), and
puts `REX Shared Library.bundle` next to `out/sointu-track` and into
`Contents/Frameworks` of each plugin bundle, as the SDK asks (a plugin
must not take the library from its host), with the SDK's license beside
it. The library is looked for in `$SOINTU_REX_DIR`, then next to the
binary the code is in, then in `../Frameworks` and `../Resources` of it.
The SDK's license allows using it, not publishing it or builds with it:
such builds are for yourself, and no SDK file is in the repository. Tested
with the SDK's test loops (`SOINTU_REX_SDK=<sdk> go test -tags rex ./rex`
with `CGO_CFLAGS=-I<sdk>`): 10 slices each, mono and stereo, REX2 and
.rcy. The rendered audio is as long as the loop or, when the last slice
rings on past it, as long as that slice.

**Sample editing** (samples, not recordings): Crop (keep the selection),
Delete (the selection), Trim silence (the ends below a level, -60 dB by
default, of any channel), Fade in and Fade out at the ends (length in ms,
10 by default; curve -100 to 100: the gain is t^(4^(curve/100)), 0
linear). The stored file is decoded by ffmpeg at 44100 Hz, edited, and
stored again as 24-bit FLAC (`ffmpeg.Store`): lossless from then on,
whatever it was, and larger than a lossy original. The encoding preset is
kept, so the compiled player encodes it as before. The edit runs in the
background; it is dropped if the sample changed meanwhile, and otherwise
is one undo step. Markers in a removed range are deleted, those after it
move with the audio.

**MCP server.** `get_song` lists the frames and the markers of each buffer;
`set_slices` sets them: by frames, `count` equal slices, or at the
transients (`sensitivity`), or clears them, as one undo step.

**Ping-pong loops.** `mode` ping-pong: the loop plays
forwards to its last frame, backwards to its start, and so on. Go synth and
wasm player render the same (`vm/compiler/wasm_pingpong_test.go`).

- **The turns** are at the last frame of the loop (loop start + length −
  1) and at its start: a position past one is folded back into the loop,
  as often as it has passed one, and each fold turns the direction. The
  signal is continuous at a turn, so there is no crossfade: `loopfade`
  does nothing and is not shown. A loop of one frame holds that frame.
- **Speed**: the direction multiplies the rate. A negative speed plays
  backwards from `start`; inside the loop it turns at the loop start. A
  position before the loop is not folded until it has been in the loop, as
  with loop: started before the loop, a backwards unit plays out to the
  start of the buffer. Started past the loop, it is folded in as if it had
  bounced there.
- **Slices, modulation, edge fade, stereo**: as with loop. The loop
  points are read every sample; the position folds into the loop as it is
  then. A sliced loop is in parts of the slice. Both channels read at the
  same position. The edge fade fades near the edges of the valid frames or
  of a stopped slice, wherever the position is.
- **No release**: bufread does not see the note-off; the loop plays on
  until an envelope or the next note ends it, as with loop.
- **State and bytecode**: `BufferRegionPingPong` (4096) with
  `BufferRegionLoop`; the region's fade is 0. The direction is bit 2 of
  `state[4]` (WRK offset 16), which is 1 once in the loop and 3 while a
  ping-pong loop plays it backwards. The wasm code for it is only there
  when a unit loops ping-pong; old songs compile to the same bytes. x86 has
  no bufread.

### Parameters of bufread

bufread had grown switches that overlapped: `mode` play or tap, `loop`
off or on, `notetracking`, `slices` off, slice or note (which ignored
`notetracking`), `sliceend`. They are now three choices, menus in the
tracker instead of knobs, with `timing`:

| Parameter | Values | Was |
|---|---|---|
| `mode` | 0 one shot, 1 loop, 2 ping-pong, 3 tap | `mode` play/tap and `loop` |
| `note` | 0 none, 1 pitch, 2 slice, 3 slice and pitch | `notetracking`, and `slices` note |
| `slices` | 0 off, 1 continue, 2 stop | `slices` and `sliceend` |
| `loopfade` | frames | `fade` |

Slice and pitch is new: the note picks the slice and also sets the pitch,
C-3 at the recorded pitch (`BufferRegionSliceNote` and
`BufferRegionNoteTracking` together). The order is mode, buffer, stereo,
note, transpose, detune, gain, speed, timing, start, loopstart,
looplength, loopfade, edgefade, slices, slice: the modulatable parameters
keep their order, so the ports that sends name stay. What does nothing is
hidden: the loop points outside loop and ping-pong, `loopfade` outside
loop, `slice` without slices, and for a tap `note`, `speed`, `edgefade`
and `slices` (transpose and detune with pitch timing only).

**Old songs** are converted when a unit is read (`upgradeBufread`, from
`Unit.UnmarshalYAML` and `UnmarshalJSON`): a bufread without `note` and
`loopfade` and with a `mode` of 0 or 1 was saved before. Then `mode` is 3
for a tap, else the old `loop`; `note` is 2 with the old `slices` note,
else `notetracking`; `slices` is off, or 1 plus `sliceend`; `fade`
becomes `loopfade`. A module unit's bindings to `notetracking`, `fade` and
`loop` move to `note`, `loopfade` and `mode`, whose values are the same;
one to `sliceend` is dropped, and those to the old `mode` and `slices` are
not converted. A hand-written new unit should give `note`, or it is read
as old. The region flags are those of before, so the Go synth renders old
songs to the same samples and the wasm player compiles them to the same
bytes, measured on 11 songs of every old combination; two get smaller,
because code that could not run is left out: a tap with a hidden loop on
(the loop code) and slices note with `notetracking` 1 (the note tracking
code). The bundled modules and `examples/granular.yml` are converted.

### Choices are menus

A parameter whose values are names, not amounts, is a menu in the unit
editor instead of a knob: `UnitParameter.Choice`, which the tracker
alone reads (`ChoiceParameter`); the song files and the synths are the
same. Before, only the choices of two values were menus. They are:

| Unit | Parameter | Values |
|---|---|---|
| delay | `notetracking` (tracking) | fixed, pitch, BPM |
| reverb | `bypass` | none, lowcut, highcut, low+highcut, predelay, lowcut+pre, highcut+pre, all |
| aux, in | `channel` | the channel pairs |
| oscillator | `type` | sine, trisaw, pulse, gate, sample |
| bufread | `mode`, `note`, `timing`, `slices` | see above |
| spphase | `mode` | disperse, random, robot |
| spawn | `mode` | rate, edge, sync |
| window | `timing` | frames, pitch, BPM |
| mcmix | `type` | hadamard, householder, shuffle |
| mcfilter | `type` | lowpass, highpass |

A module parameter is a choice when the first parameter bound to it is
and the binding is not scaled (`Modules.Param`), also when that is a
parameter of a module unit; with a display override (`Display`) only
when the ranges are the same. A module unit then shows a menu, and the
Modules tab offers the default in a menu. A test checks that every
parameter with named values is a choice (`TestEveryChoiceIsAChoice`).
Numbers stay knobs: `unison`, a send's `port`, `slot`, `steps`.

## MCP server

A language model, e.g. Claude in Claude Code, can read and change the patch
of a running tracker or plugin instance, through the Model Context Protocol:
it sees the instruments and units as the tracker shows them, changes them
as the user would, and renders notes offline to measure what a change did.
The user hears and sees every change at once, and undoes it like any other.

**Turning it on.** Edit → Enable MCP, in the tracker or a plugin
window. It is off by default; the choice is saved in the user's
`preferences.yml` (`mcp: enabled:`), so every tracker and plugin instance
started after that listens too. Instances that are already open change
when their own menu is used.

**Connecting Claude Code.** Build the command and register it once:

```
make mcp                                   # out/sointu-mcp
claude mcp add --scope user sointu -- "$PWD/out/sointu-mcp"
```

or in a project's `.mcp.json`:

```json
{"mcpServers": {"sointu": {"command": "/path/to/sointu-mcp"}}}
```

`sointu-mcp -list` prints the instances that are listening.

**How it is built.** A plugin runs in the host's process, which Claude Code
does not start, so its standard input and output cannot be the server's.
Instead:

```
Claude Code --stdio--> sointu-mcp --unix socket--> tracker, plugin instances
```

- Each instance (`tracker/mcp.Host`) listens on a unix socket
  `<config>/sointu/mcp/<kind>-<pid>-<n>.sock`, mode 0600 in a directory of
  mode 0700 (on macOS `<config>` is `~/Library/Application Support`), and
  writes `<kind>-<pid>-<n>.json` next to it with its ID, pid and socket.
  Where that path is longer than a unix socket allows (104 bytes on macOS),
  the socket goes to `$TMPDIR/sointu-mcp-<uid>/`, also mode 0700.
- `sointu-mcp` (`cmd/sointu-mcp`) is the MCP server, with the official Go
  SDK (`github.com/modelcontextprotocol/go-sdk`). Claude Code starts it; for
  each call it reads the files, asks each instance for its song and
  instruments, and passes the call on to the one chosen by the argument
  `instance`, which every tool has: needed only when several listen. The
  files of an instance that does not answer and whose process is gone are
  removed. Each request is a line of JSON on the socket, and so is the
  answer: the tool's name and arguments, the text or an error.
- Only `sointu-mcp` links the SDK (it needs Go 1.25 from its v1.5.0 on, so
  `go.mod` says 1.25). The tracker and the plugins only use the standard
  library for the socket; the players and `sointu-compile` have none of it.

**Safety.** Nothing listens on a network port: what can connect is what can
open a file that only the user can, i.e. the user's own programs, which
could change the song file anyway. There is no token. A call reaches the
model as a `func()` message on the broker (`MsgToModel.Data`), the way a
plugin host's request for the state does, so it runs on the goroutine that
owns the model, between two frames of the GUI, never on the audio thread;
the caller waits at most 10 s. A tool that panics is recovered: the song
is put back as it was and the call fails, so a fault does not take the
plugin host down. Renders run on the goroutine of the connection, with a
synth of their own.

**Tools.** The tools are `tracker.Remote` (`tracker/remote.go`): units by
their ID, instruments by index (from 0) or name, modules by name, parameters
by name. Values are checked against the ranges the tracker gives them,
including those of buffers, spawn targets and module parameters; a string
names a value as it is displayed (`type: "sine"`). Each change is one step
of the undo history, refused as a whole if any part is not valid (a value
out of range, an unknown parameter, more than 63 units once expanded, an
instrument left without units), moves the cursor to the unit or instrument
it was about, and shows an alert ("MCP: ..."). It returns the units as
they are after it. `get_song`, `get_instrument`, `get_module` and what the
changes return end with a `selection:` line (`Remote.Selection`): the tab,
the instrument, the module or the buffer of the tab, the selected units and
the parameter under the cursor, so that "this unit" or "here" can be
resolved; the instructions say so.

| Tool | What it does |
|---|---|
| `list_instances` | the trackers and plugin instances listening: ID, program, host process, file, instruments |
| `guide` | how sointu patches work, for a model that has not seen the repository |
| `unit_types` | every unit type in a line (stack effect, go/wasm only, parameters), or one with ranges, defaults and the displayed values at every 8th value |
| `get_selection` | what the user has open and selected: the tab, the instrument, the module (Modules tab), the buffer (Buffers tab), the units and the parameter under the cursor, whether the song plays and where, the cursor in the score |
| `get_song` | tempo, score in summary, instruments in the order they run (MIDI channel, the channels a bus reads, outputs), modules and buffers in a line each, problems, routing warnings |
| `get_changes` | what changed since the previous call, made by the user (below) |
| `get_instrument`, `get_module` | the units in order: `#id type [stack before>after] name=value(displayed)`, sends with what they modulate, eq bands, bindings, comments, the go/wasm only units; with `verbose`, ranges |
| `list_presets` | instrument presets by directory, and module presets with their parameters |
| `edit_units` | parameters, disabled, comment, eq bands and bindings of any number of units, as one step |
| `add_units` | units in a row after or before a unit, or at the end of an instrument or a module; a send can target `new:N`, a unit of the same call |
| `delete_units`, `move_units` | moved units keep their IDs, also into another instrument or module |
| `add_instrument`, `edit_instrument`, `delete_instrument` | a preset or the default instrument, before or after an instrument, by default before the first bus; name, comment, voices, mute, solo, loading a preset |
| `move_instrument` | before or after another instrument |
| `add_module`, `edit_module`, `delete_module` | a module preset or an empty module; name, comment, inputs, parameters (added, named, defaults, deleted) |
| `get_automation`, `automate`, `set_automation` | the points of the lane of an automation slot; binding a slot to a parameter of a unit, naming it for the host, or removing that; setting the points. `get_song` lists the lanes, `get_instrument` marks automated parameters (`A3 +0.0: ...`), and the changes of the user list lanes added, removed, bound elsewhere and recorded |
| `add_buffer` | an empty audio buffer (name, seconds, 1 or 2 channels) for `bufwrite` and `bufread` units; returns its ID |
| `set_bpm`, `undo`, `redo` | undo and redo go through the same history as the user's |
| `render_note` | renders notes of an instrument offline and measures them (below) |
| `play_note` | plays a note in the running tracker, for the user to hear |

**Order and routing.** Instruments run in order, and in clears the channel
it reads, so a bus hears only the instruments before it. A model appending a
new instrument after the master chain bypassed it, so `add_instrument`
puts it before the first instrument with an in unit (`Remote.firstBus`,
looking into modules) unless told otherwise. `get_song` marks buses ("bus:
reads aux1") and warns of an instrument that writes a channel only after the
last instrument reading it (`routingWarnings`). With MIDI channel auto the
instruments take channels in order, buses included, so adding or moving one
can shift the channels of those after it; the result lists such shifts, for
the model to tell the user. When the tracks of the score do not reach the
voices involved (the buses usually have none), the instrument is added or
moved without its tracks, where the tracker's own move would be refused.

**The user's changes.** The user edits the patch in the tracker far more
than through the model, also between its calls. Each call
(`Remote.Call`) keeps a copy of the song as it leaves it; the next one
compares the song with it and, when the user changed it, starts its answer
with "NOTE: the song changed ..." and the changes (`Remote.Changes`, at most
60 lines): instruments matched by the IDs of their units (added, deleted,
renamed, reordered, voices, mute, MIDI, comment), units by ID (added,
deleted, reordered, type, disabled, parameters with old and new values as
displayed, varargs, bindings, comment), modules by ID (also their inputs and
parameters), buffers by ID, the tempo, the score per track, the encoding
presets. `get_changes` asks for them alone. The guide tells the model not to
trust what it read earlier and to read an instrument right before changing
it.

**Alerts.** The warnings and errors that the tracker shows the user as
notifications (`Alerts.AddAlert`: a stack left unbalanced, a module using
itself, a sample that failed to import or encode, a file that failed to
load, a fault of the synth) are kept for the model too (`Model.remoteAlerts`,
the last 32, a named alert with the same message once). The next call
starts its answer with "ALERTS: ..." and a line each, `warning:` or
`error:`, and forgets them; those that the call itself causes come with its
own answer, also when it fails. Info alerts, such as the "MCP: ..." one of
each change, are not told. `get_changes` returns them when nothing else
changed.

**Measuring.** A model cannot hear; until now the sounds of this fork were
tuned by rendering and measuring, by hand. `render_note` takes a copy of the
song from the model (`Remote.RenderSource`), with module and eq units
expanded, triggers the notes on the first voices of the instrument in a new
Go synth, holds them for `hold_ms`, releases them and renders `tail_ms`
more. The measurements (`tracker/mcp/measure.go`): peak (per channel, and
when), samples above full scale, rms, crest factor, DC, stereo correlation,
side against mid, the pitch at 8 times (a YIN-like difference function, 25
Hz to 4 kHz, none for noise), octave bands from 31 Hz to 16 kHz (Welch
spectra, scaled so that the bands add up to the rms), the envelope in about
16 steps, onset, attack to 90 % of the peak, and the time after the release
until the level stays below -60 dBFS. About 600 bytes of text.

- `output: dry` (the default) silences the outputs of the other instruments
  (`out`, the main part of `outaux`, `aux` to channel 0) in the copy; an
  instrument that only writes to aux channels gets them sent to the main
  output, and is told so. `output: master` changes nothing: the instrument
  through the buses and the mastering it sends to.
- `edits` are changes made to the copy only, in the same form as
  `edit_units`: to compare variants (a unit disabled, a parameter at several
  values) without changing the song. They are made like any change and
  then cancelled, so they are checked the same way, and the song, the undo
  history and the player never see them.
- `wav: true` also writes the render, 32-bit float stereo at 44100 Hz, to
  `$TMPDIR/sointu-mcp-renders/` and returns its path, for analysis with
  other tools (renders older than a day are removed).

Notes are numbered as in the tracker and as MIDI input arrives in the
plugin: 60 is C-3, 130.8 Hz for an oscillator with transpose 64, an octave
below what the same MIDI number means elsewhere. The tool descriptions and
the guide say so.

**Verified.** `tracker/remote_test.go`: every change is one undo step that
undo reverses and redo repeats, and invalid input (out of range, unknown
parameters, units, presets or ports, too many units or voices, the last
unit of an instrument, bindings outside modules) is refused without
changing the song or the history; adding units with a send to a new unit
and eq bands, moving them across instruments, presets, solo, modules from
presets and empty ones with bound and scaled parameters, deleting module
parameters with their bindings, a render whose edits leave the song as it
was, the dry routing, and a panic inside a change. `tracker/mcp/mcp_test.go`:
a model with a Host on a socket, as the tracker runs it: the info, the tools
over the socket, a misspelled argument refused, undo, render_note of the
default instrument (note 69: 220.0 Hz) with a wav file, play_note, the files
gone when turned off and stale files removed; the measurements of a sine
and of noise. `cmd/sointu-mcp/main_test.go`: the server with an MCP client
of the SDK: instructions, the tools and their schemas with `instance`,
local tools without a tracker, a change and a render through two hops, and
two trackers. By hand: the standalone tracker with its own `HOME`, and
`claude -p` with `--mcp-config`: Claude listed it, read an instrument,
rendered, added a filter at 988 Hz and rendered again. The VST2 and CLAP
plugins build. **Not verified:** in a plugin inside a DAW, and the menu item
on screen.

**Not done.** Notes and patterns; buffers and samples; the signal at a unit
(the taps of the Go synth); rendering a passage of the song; capturing what
the plugin plays while the host plays; the name of the host's track (CLAP
`track-info`) and its transport.

## Wasm player

- Samples are stored as custom sections (`sointu.buffer`). The host decodes
  them and passes them through the imported function `s.b` before
  instantiation.
- The player computes 2^x, sin and pow itself, in float32, like the Go synth.
  It no longer imports anything from JavaScript except samples. With
  `sointu-compile -imports` (`Compiler.MathImports`), it calls Math.pow and
  Math.sin through the `m` import as before. That compiles to exactly the
  old player, which is a bit smaller, but it no longer matches the Go synth
  exactly. The spectral units use the built-in routines either way.
- The FFT uses SIMD (f32x4), with window and twiddle tables computed at
  startup with `$sinTurns`.
- Template errors are no longer ignored (they used to give an empty module).
- The `sync` unit and row sync (`-r`), in songs and compiles that use them:
  see [Sync values](#sync-values).
- Stereo `push` copies the pair (left and right), like the Go synth and the
  x86 players; it used to copy the top signal twice. This changes the players
  of songs with stereo pushes.
- Stereo `xch` exchanges the two pairs in place. Before (and upstream), a
  song with a mono and a stereo `xch` compiled to a player that did not
  assemble, and a stereo `xch` next to a stereo `delay` exchanged wrongly.

### Parts conditional on use

The player has the code of a unit only when the song has the unit, and of
many units only the parts the song uses. The compiler finds them in three
ways, and the templates test them:

- `FeatureSetMacros` (`vm/compiler/featureset_macros.go`): what follows from
  the values of one parameter, e.g. `MCDelayMod`, `SpfilterTilt`, `OttTime`.
- `wasmUnitFeatures` and `wasmBufferFeatures`
  (`vm/compiler/wasm_features.go`, `compiler.go`): what needs the units
  themselves, e.g. which waveforms are bandlimited, whether an instrument
  with mc units has several voices, whether a spectrum is stereo.
- `vm/operands.go`: operands that only songs have whose units differ in
  them. `vm.NewBytecode` and the templates ask the same function, so they
  agree. Only for units that x86 does not have, as the bytecode is the same
  for every target.

| Unit | Conditional |
|---|---|
| `bufread`, `bufwrite` | looping, crossfade, backwards in a loop, modulated and negative positions, edge fade, note tracking, pitch, channel mixes; one shot, ring, mixing writers, feedback, no pop |
| mc units | the first-voice check and the voice in the table; `mcdelay` modulation (with its 16 constants), the clamp of its lengths, note tracking, allpass; gain of `mcspread` and `mcsum`, width of `mcsum`, feedback of `mcloop`, stereo lanes of `mcspread`, each `mcmix` type, the high-pass. Operands: add of `mcspread`, type of `mcmix` and `mcfilter`, flags of `mcdelay` |
| `automation` | the unit and its table, only in songs with lanes that have a point other than 0; values of 8 bits when all are whole steps from -128 to 127, else of 16 |
| `spawn`, `window` | each mode, arguments, stealing, note tracking, transpose, note length and the release loop, the test for a missing target; own length, note length and the clamp of a modulated shape of `window`. Operand: the flags of `spawn` |
| spectral units | the first-voice check and the voice in the table; the loops over channels, the rings of `spifft` and the channels in the spectrum table (only with a stereo spectrum); low, high and tilt of `spfilter` (`$log2f`, `$powf`); freeze of `spblur`; each mode of `spphase` (`$rotate`, `$tablePhase`, `$randomPhase`); scale and shift of `spscale`; voices and intervals of `spcomb`. Operands: invert of `spgate`, mode of `spphase`, voices and intervals of `spcomb` |
| `ott` | time, upward, downward, the right channel's powers |
| `reverb` | the modulation of its lines; each filter of its input, and the test for it when only some units have it; the levels, widths and rate as constants of the unit instead of the code; the number of steps; allpasses in the diffuser, and plain delays; the second set of lines. See [reverb unit](#reverb-unit) |
| `convolution` | the FFT parts (only with a response longer than 64 frames; the FFT and its tables are those of the spectral units); reading a written buffer again, several blocks at a time, and the ramp of `fade` or the test for it when only some units have it; reading a sample at the first sample; the loop over the channels; gain, predelay, and dry (an optional last parameter). See [convolution unit](#convolution-unit) |
| `softclip`, `limiter`, `width`, `ladder`, `envelope` | drive and oversampling of `softclip`; drive of `limiter` and `ladder`, lowcut of `width`, curve of `envelope` (optional last parameters, `optionalParams`) |
| `oscillator` | the corrections of each bandlimited waveform; the LFO code |
| `aux`, `in` | the global ports of the channels above 7, 16 ports instead of 8, which move the voices by 32 bytes (see [Output channels](#output-channels)) |
| shared | `$swap`, `$peek2`, `$stereoHelper`, each only when a unit calls it; stereo and mono `xch` |

The rule for adding one:

1. The part must do nothing in a song that does not use it, exactly: x·1,
   x+0 and 2^0 = 1 are exact, a filter whose output is multiplied by 0 is
   not (a huge input overflows to infinity, and 0·∞ is not 0), and neither
   is (l+l)·0.5. A parameter counts as unused only if no unit has another
   value than the neutral one and nothing modulates it
   (`FeatureSetMacros.set`). When in doubt, keep the code.
2. Test both ways: `vm/compiler/wasm_features_test.go` and
   `wasm_buffeatures_test.go` have a table of songs for each group, with the
   part used, not used, and mixed with units that use the other parts. Each
   checks that the player has the code of the parts it uses and no others,
   and that it renders the song exactly like the Go synth, whose bytecode
   always has every operand (`AllFeatures`).
3. Songs that do not use the unit must compile to the same player as
   before, for wasm, 386 and amd64.

Looked at and left, as not exact: the interpolation of `mcdelay` with whole
lengths, its decay filter with decay 0, and depth 128 of `ott`.

## Web runtime

For intros in the browser: the song renders in the background while the page
compiles shaders, starts as soon as a runway is rendered, and the intro reads
the audio clock. `sointu-compile` writes the JavaScript for it, so it has only
the code the song needs. Without the new flags, the output of
`sointu-compile` is unchanged.

```
sointu-compile -arch wasm -js [-stages N] [-cuts a,b] [-samples] [-r] [-outputclock] -o out/song song.yml
wat2wasm -o out/song.wasm out/song.wat
```

writes `song.wat`, `song.js`, `song.d.ts` and, with `-samples`, `song.0.ogg`
and so on. `examples/code/web` is a vite and websqz (rootsqz) project that
uses them; its README has the usage.

```js
import wasm from "./song.wasm?websqz-bin";
import { load, duration, rowsPerSecond } from "./song.js";
const song = load(wasm);              // renders in workers
await song.ready;                     // the runway is rendered
onclick = () => song.start();         // plays what is rendered, and the rest as it comes
const t = song.time();                // seconds played: the clock of the visuals
const kick = song.sync(0);            // in songs with sync units: the signal at the first, now
```

`song.rendered` is the seconds rendered so far, `song.context` the
`AudioContext`, and `song.start(node)` plays into a node of it.
`load(wasm, runway, margin)` sets the runway in seconds (2) and the part of
the measured speed that the rest of the song is expected to render at (0.8).

### The player

- `Compiler.Progressive` (`-js`): the player has no start function. It
  exports `r(rows)`, which renders the next rows and can be called until the
  song ends. The state of the synth was in memory and globals already, so
  only the loop changed. The first call fills the sample buffers and the FFT
  tables. `s`, `l` and `t` are not exported with `-js`: the module has their
  values. The player is 32 bytes smaller than the one that renders at
  instantiation.
- `Compiler.Layout` gives a host the addresses (output, tapes) and the
  lengths, for hosts that do not use the module.
- `Compiler.SeparateSamples` (`-samples`): the encoded samples are returned
  as files instead of `sointu.buffer` sections.

### The module

- **Rendering** runs in a worker, made from a Blob of the source text of the
  function `renderer` (`Function.prototype.toString`), so the bundler
  minifies it with the rest and nothing is stored twice. The page compiles
  the `WebAssembly.Module` once and posts it to the workers. This needs no
  `SharedArrayBuffer` and no cross-origin isolation, and works from `http://`
  and `file://`. If `new Worker` throws, the same function renders on the
  main thread, a row for each timer tick.
- **Playing**: the audio is posted in pieces of half a second, and each
  piece is an `AudioBufferSourceNode` scheduled at its time. A piece starts
  on a multiple of a quarter of a second of the context, because only such
  start times are whole frames in floating point: from other times Chrome
  got a fraction of a frame (1e-12) for one piece in seven and interpolated,
  which changed a few samples in a million by their last bit. A single
  `AudioBuffer` written ahead of the playhead was not tried: the Web Audio
  specification lets the node take the contents of the buffer when it
  starts. An `AudioWorklet` needs a secure context.
- **Runway**: `ready` resolves when `runway` seconds are rendered (2 by
  default) and rendering the rest at `margin` times the speed measured so
  far (0.8 by default) ends before the song gets there: `rendered >=
  duration * (1 - margin * speed)`, with the speed in seconds of song for a
  second. A song that renders slower than it plays waits until enough is
  rendered. Both are arguments of `load`. The runway has to cover the
  longest time the page keeps the main thread busy after the start, as the
  pieces are scheduled from there; the margin, how much slower rendering
  may get after the start than it was before. A margin of 0 waits for the
  whole song.
- **If playing catches up** with rendering anyway, the next piece plays
  0.05 to 0.3 s after it arrives, and the clock stops until then: silence,
  and visuals that wait, instead of visuals that run ahead.
- **Clock**: `time()` is `context.currentTime` minus the start time, never
  going back. The row is `time() * rowsPerSecond`. Songs with the `speed`
  unit get no module. This clock is the time of the audio given to the
  output: it is ahead of the sound by the output latency, and advances an
  audio block at a time (5.8 ms steps in Chrome and Firefox on the test
  machine). `-outputclock` gives the other one: see
  [The clock of the output](#the-clock-of-the-output).
- **Sync values**: in songs with `sync` units, `sync(channel)` is the signal
  at a sync unit at the time of `time()`, and `sync(channel, t)` at another
  time: see below.
- **Samples** are decoded on the page (`decodeAudioData`, which workers do
  not have) and posted to the workers.

### The clock of the output

`-outputclock` (`Compiler.OutputClock`, with `-js`) makes `time()` follow
what is heard instead of what is given to the output. It is off by default
and costs nothing then: the code is only in modules compiled with the flag,
and the module and its types without it are the same bytes as before.

- `AudioContext.getOutputTimestamp()` gives a pair: a time of the context,
  and the time of the page (`performance.now()`) at which that time was at
  the output. The clock is the first carried on by the time of the page
  since the second: behind `context.currentTime` by the output latency, and
  moving between audio blocks.
- It never goes back and stops while the song waits for late audio, as the
  plain clock does: the same maximum over the times reported, the same
  limit at what is rendered, the same start time, which moves when a piece
  comes late. It ends at `duration`, an output latency after the plain
  clock would.
- `sync(channel)` without a time reads at `time()`, so it follows.
- Fallback to the plain clock: where the browser has no
  `getOutputTimestamp`, or where it gives zeros (Chrome before the context
  runs).
- Firefox takes the timestamp when asked: the time of the context less the
  latency, and the time of the page less the latency. Carried on, that is
  the time of the context again, without the latency. So when the time
  carried on comes within a millisecond of `context.currentTime`, the clock
  is the time of the timestamp alone: in Firefox it is behind by the
  latency, and still advances a block at a time. The same holds the clock
  in any browser while the context stands still.
- Cost, `soundset_loop` with the smallest use (load, start, time), minified
  by vite 7 and packed with websqz 0.4.1 (423821d): 125 B of JavaScript and
  54 B of the packed page (1389 → 1514 B and 5334 → 5388 B with `-js`;
  1734 → 1859 B and 5715 → 5769 B with `-js -stages 4`).
- Measured in headless browsers on the test machine, over a song of 5 s,
  sampled every 5 ms: in Chrome 154 the clock is 29 to 35 ms behind the
  time of the context (median 32.6 ms), where the context reports an output
  latency of 32 ms, and its steps are within 0.2 ms of those of the time
  of the page, where the time of the context steps by 5.8 ms. In Firefox
  157 it is 23.4 ms behind, the latency reported, in steps of about 7 ms.
  `sync(channel)` was the value at the time of the clock in every one of
  some 750 reads of a run.
  It is never ahead of the time of the context.
- Not verified: whether the latency a browser reports is the latency of
  the device. A headless browser has no sound card, and the test compares
  the clock with what the browser itself reports (`outputLatency`), so it
  shows that the module uses the timestamp right, not that the timestamp
  is right. That needs a microphone or a loopback on real hardware; with
  Bluetooth output browsers are known to report too little. Safari is not
  tested: it has `getOutputTimestamp` and no `outputLatency`.

### Sync values

The wasm player has the `sync` unit: before, a song with one compiled to a
player that did not assemble (`tests/test_sync.yml` and two of
`examples/patches`; these three are the only songs whose default output
changed). As in the x86 players, every 256th sample (5.8 ms) the signal at
each sync unit is stored, without changing it, in a sync buffer: one float
for each sync unit and voice, in the order they run. With `-r`
(`Compiler.RowSync`), the row with the fraction of its samples comes first.
The player that renders at instantiation exports the address and the size in
bytes as `y` and `z`.

All of it is conditional: the unit, the buffer and the code that points
into it exist only in songs with enabled sync units or compiled with `-r`,
and the module has `sync()`, `syncChannels` and the transport of the values
only then. The values are posted with the audio, those of the samples of
each piece, so they are there before their time is played. In stages, each
stage writes the values of its voices, and passes the values of the chunk on
with the tape. The row is `time() * rowsPerSecond` anyway, so `-r` is for
hosts that read the buffer themselves; it costs 31 bytes of wasm.

The Go synth records the same values (`GoSynther.Syncs`), and
`wasm_syncunit_test.go` compares them exactly; the sync buffer of the x86
player in `tests/expected_output` agrees within 1e-4.

### Stages: rendering in several threads

A song is rendered sample by sample, and every sample of a voice can depend
on the voices before it, so the song cannot be cut in time. It is cut across
the voices: with `-stages N`, the workers form a pipeline. Stage 1 runs the
first voices for a chunk of rows and records, for every sample, the memory
cells that both sides of the cut use: the output and aux ports, and the ports
that earlier voices send to. Stage 2 starts every sample from those values,
runs its voices and records its own cut, and so on; the last stage writes the
audio. Every cell holds exactly the value it has when one instance runs all
voices, so the output is the same, bit for bit. Adding the outputs of
independent groups of instruments would not be: float addition rounds
differently in another order.

`vm/compiler/wasm_stages.go` finds where a song can be cut:

- Not where something flows back: a send to an earlier voice, an aux channel
  written after the `in` that reads it or never read, a send to a port that
  its unit does not clear.
- Not between voices that share memory: a `spawn` and its instrument, the
  units of a spectrum, a bus or a written buffer, signals left on the stack.
- Not at all with the `speed` unit.
- Noise is no obstacle: the seed is multiplied by 16007 for each noise
  sample, so a stage steps it over the noise of the other stages with one
  multiplication.
- Units with a state of their own are no obstacle either, also where the
  states are in a table in the order the units run (delay lines, `ott`,
  `limiter`, `reverb`, `convolution`): a stage starts at the place of its
  first voice in each table. A `convolution` unit and the instrument that
  writes its buffer stay in one stage, like `bufread`. A `reverb` unit can be modulated from an earlier stage like
  any unit.

It then picks the cuts that make the most expensive stage cheapest, from a
cost for each unit fitted to the example songs (`unitCost`), and prints the
stages. `-cuts` sets them by hand. A song with fewer possible cuts gets
fewer stages. The player gets a table of the stages, the code that copies
the cells, and `g(stage)`; without the call an instance runs all voices,
which is the fallback.

**Stages and the threads of the tracker.** Instruments have a thread mask
(`Instrument.ThreadMaskM1`, `vm/multithread_synth.go`), for playing in the
tracker: the patch is split into one patch for each thread, each rendered
by its own synth, and the outputs are added. The threads do not see each
other: a send, an aux channel or a buffer that crosses threads does nothing
(the tracker warns), and the sum depends on which thread finishes first, in
the last bits. The compiled players disregard the masks and render the song
as one patch. Stages differ in all of it: they keep every dependency, give
exactly the samples of the one patch, and are ranges of consecutive voices
in a pipeline instead of any set of instruments side by side.

The stages ignore the thread masks, on purpose. As a constraint the masks
cannot be kept: a set like instruments 1, 3 and 5 is not a range of voices,
and instruments that depend on each other would have to be cut apart the
way the tracker does, which changes the sound. As a hint they add nothing:
the compiler knows the cost of each voice and every place the song can be
cut, and balances with that, where a mask was chosen for another split and
another machine. `-cuts` is the way to set the stages by hand. A song that
is correct in the threads of the tracker, with each instrument on one
thread, has no dependencies across them, and sounds the same in stages, up
to the last bits that the tracker's sum varies in.

Limits: the speedup ends at the most expensive voice (a master chain with a
reverb is one stage), a stage that falls behind queues tapes without bound
(4 bytes for each cell and sample), and the first audio comes a chunk later
for each stage.

### Numbers

Apple M3 Pro, Chrome 154, `tests/wasm_runtime_browser.mjs --scenario
measure`, best of three, after the presets got the reverb unit; the steady
rate leaves out the start of the workers (0.3 to 0.4 s in a browser that
just started). The machine was not idle (other work took 3 to 4 of its 12
cores), so the rates with 8 workers are on the low side.

| Song | Workers | Seconds of song for a second | Ready (2 s runway) | All rendered |
|---|---|---|---|---|
| `soundset_loop` (13.7 s) | 1 | 9.8 | 0.52 s | 1.84 s |
| | 2 | 16.6 | 0.56 s | 1.26 s |
| | 4 | 28.2 | 0.53 s | 0.94 s |
| | 8 | 48.0 | 0.46 s | 0.74 s |
| `soundset` (68.6 s) | 1 | 3.4 | 1.22 s | 21.0 s |
| | 2 | 6.2 | 0.71 s | 11.5 s |
| | 4 | 9.8 | 0.77 s | 7.6 s |
| | 8 | 15.7 | 0.68 s | 4.9 s |
| `ducking` (6.9 s) | 1 | 28 | 0.42 s | 0.92 s |
| | 5 (of 8 asked) | 89 | 0.33 s | 0.39 s |
| `reverb_module` (14.4 s) | 1 | 31 | 0.53 s | 0.92 s |
| | 2 | 55 | 0.36 s | 0.61 s |
| | 5 (of 8 asked) | 51 | 0.56 s | 0.81 s |
| `reverb_unit` (14.4 s) | 1 | 41 | 0.40 s | 0.71 s |
| | 4 | 81 | 0.35 s | 0.50 s |
| | 5 (of 8 asked) | 102 | 0.49 s | 0.73 s |

With the reverb unit, the voice of the master chain of `soundset_loop` costs
less than a voice of the supersaw lead, which now bounds the pipeline: 8
workers render 4.9 times as fast as one, where it was 3.9 with the reverb
made of mc units. `soundset` spends its time in its 30 voices of
oscillators, and is as before. Songs that render in well under a second
gain nothing from more than 2 to 4 workers: the workers start as slowly, and
a stage more delays the first audio.

The sound starts 0.05 to 0.3 s after `start()`. While rendering, the main
thread was held for at most 17 ms in 57 of the 60 runs, and for 30 to 40 ms
in three. The player that renders at instantiation blocks the page for the
whole render, about as long as one worker takes.

Sizes, for `soundset_loop` with the smallest use (load, start, time), bundled
with vite 7 and packed with rootsqz from GitHub (423821d, websqz 0.4.1,
default profile). The JavaScript includes 21 bytes that make the plugin work
with that packer. For the sync rows, the song has a sync unit after the
envelope of the kick, and the page reads `sync(0)`.

| | JavaScript, minified | wasm | Packed page |
|---|---|---|---|
| Before: render at instantiation, one buffer | 500 B | 6151 B | 4908 B |
| `-js` | 1430 B | 6119 B | 5351 B |
| `-js -stages 2` | 1771 B | 6566 B | 5688 B |
| `-js -stages 4` | 1775 B | 6710 B | 5736 B |
| `-js -stages 8` | 1783 B | 6990 B | 5821 B |
| `-js`, a sync unit | 1670 B | 6181 B | 5486 B |
| `-js -r`, a sync unit | 1670 B | 6212 B | 5505 B |
| `-js -stages 4`, a sync unit | 2160 B | 6802 B | 5904 B |

`soundset` packs to 5083 B before, 5525 B with `-js`, 5906 B with 4 stages
and 5976 B with 8. With `--size-profile 64k` every one of these pages is
about 250 B larger; that profile is made for larger inputs.

### Samples and the packer

rootsqz stores `--pre-compressed-files` (`?websqz-bin&compressed` with the
plugin) as they are, after the compressed data. Measured with Opus files of 9
to 62 KB and the packer from GitHub: compressing them with the rest never
makes them larger, it gains 0.2 to 1 % (the Ogg pages), so samples inside
the wasm are the smallest: 43104 B of Opus cost 42982 B compressed and
43127 B stored. A song with 4386 B of Opus packs to 7544 B with the sample
in the wasm, 7542 B with the sample as a compressed file and 7735 B with it
stored. But the decompressor takes about 19 µs for each byte (25 with
websqz 0.4): the 43 KB file delays the start of the intro by 0.8 s
compressed, and not at all stored. `-samples` is for that: 0.3 % more bytes
for large samples, most of a second less for each 40 KB. Small samples are
better left in the wasm.

The page packed with the packer from GitHub starts its script 1.6 s after
it is opened (0.7 s with websqz 0.4), before any payload: nothing of the
song can render until then.

### Verified, and not

- The progressive player renders, in parts of different sizes, the bytes of
  the one-shot player, for all regression and example songs (the first 10 s
  of the long ones; `wasm_progressive_test.go`), which other tests compare
  with the Go synth.
- The pipeline renders the same bytes for every cut the compiler allows in
  the first second of those songs (soundset: all 29), and for songs made for
  each kind of dependency, where the cuts found are checked too
  (`wasm_stages_test.go`). That includes songs with channels above 7,
  whose player has 16 global ports and its voices 32 bytes later
  (`wasm_aux16_test.go`).
- The sync values of the wasm player are those of the Go synth, also
  rendered in parts and in stages (`wasm_syncunit_test.go`, and the two
  tests above).
- In headless Chrome 154 and Firefox 157 (`TestRuntimeInBrowser`, with
  `SOINTU_TEST_BROWSER=1` or `=firefox`): an AudioWorklet records what the
  audio context plays, and it is the one-shot render sample for sample,
  without a gap, in a worker, in 4 stages, on the main thread, with 16 bit
  output, with samples in the wasm and as files; with audio held back for 3
  s, the sound has one gap of silence, the clock stops and never goes back.
  `sync()` returns the values of the one-shot player for every 256th
  sample, in a worker, in 3 stages, on the main thread and with late audio.
  `time()` is exactly the time of the context less the start, read before
  and after it, and `sync(channel)` the value at that time. With
  `-outputclock`: see [The clock of the output](#the-clock-of-the-output);
  also in stages, on the main thread, with late audio (one stop, never
  back), and with a timestamp of zeros or none, where it is the plain
  clock exactly.
- The example packed with rootsqz from GitHub runs in headless Chrome from
  http (4 workers). From `file://` the packed page does not start, with
  that packer as with websqz 0.4: it reads itself with `fetch`. An unpacked
  single file runs from `file://` with workers.
- Not verified: Safari, a real sound card (the browsers ran muted and
  headless), x86 processors (denormals may slow the wasm player there, which
  has no flush-to-zero), mobile browsers, and how the clock steps on audio
  hardware with large buffers.

## Tests

`go test ./...` covers every feature with short songs; `vm/compiler` takes
about 15 s, with its wasm tests running in parallel (`t.Parallel`). The long
renders are opt-in:

```
SOINTU_TEST_LONG=1 go test ./...               # about 2 minutes in vm/compiler
SOINTU_TEST_BROWSER=1 go test ./vm/compiler    # headless Chrome, 2 minutes more; =firefox for Firefox
```

What the default run leaves out, and `SOINTU_TEST_LONG=1` adds (`-short`
leaves it out in any case):

- `examples/soundset.yml` (68 s, every sound of the sound set in turn): in
  the Go synth against the wasm player, in parts in the progressive player,
  and in stages. Every preset of the sound set is still rendered alone, in
  both synths (`TestSoundsetPresets`), and `examples/soundset_loop.yml`
  plays the kit, bass and lead through the drum bus and the master chain.
- Of `examples/soundset_loop.yml`, all but the first two patterns in the Go
  synth against the wasm player (3.4 of 13.7 s by default).
- In the progressive player, more than the first 2 s of the songs longer
  than that (10 s with the variable).
- In stages, every possible cut of the songs with more than 6: by default 4
  cuts spread over the voices, and the pipelines of 2 to 4 stages. Every
  kind of dependency between voices has a short song where all cuts are
  rendered (`TestStageCuts`).

The browser tests need neither variable for anything else: they are only
opt-in because they launch a browser and play in real time.

The plugins need the build tag: `go test -tags plugin ./cmd/...`. The VST3
test builds the plugin and a test host (macOS); with
`VST3_VALIDATOR=<path>` it runs the validator of the VST3 SDK on it too.
The validator is built from the SDK, e.g. cloned into `.vst3sdk/` (ignored
by git): `cmake -S .vst3sdk -B .vst3sdk/build -DSMTG_ENABLE_VSTGUI_SUPPORT=OFF`
and `cmake --build .vst3sdk/build --target validator`.

## Updating the x86 backend

What the x86 players (`vm/compiler/templates/amd64-386`) and the native bridge
(`vm/compiler/bridge`) would need to catch up. The compiler currently refuses
songs that need any of it for x86.

1. **Voice header.** The wasm voice has a 64-byte header before its units:

   | Offset | Field |
   |---|---|
   | 0 | note |
   | 4 | sustain |
   | 8 | global time + 1 when spawned |
   | 12 | release tick |
   | 16–31 | 4 spawn args |
   | 32 | spawned note length |

   Units start at +64. `spawn`, `arg`, `window` and `bufread` use these
   fields.

2. **More than 32 voices.** 3-byte send addresses with the global flag in bit
   23. Replace the polyphony and voice-track bitmasks with per-voice byte
   tables when `WideVoices`. Size the voice memory by the voice count.

3. **Buffers.** Audio loaded by the host (the native bridge has no decoder).
   The buffer header and region tables are as in `player.wat`: a header is 6
   i32s, a region 7 i32s. Implement `bufread` and `bufwrite` as in
   `vm/go_synth.go`.

4. **Spawn.** Voice allocation, the rate/sync/edge modes and the
   whole-frame countdown, as in `GoSynth.spawn`. The compiler keeps holds
   on tracks covering spawned voices (`spawnedVoices` in `patterns.go`).
   The flags operand is only there when the spawn units differ in it
   (`vm.SpawnFlagsOperand`).

5. **Spectral units.**
   - Tables are in `su_spectral`, laid out by `wasmSpectral` in
     `vm/compiler/compiler.go`: spectra, unit states, scratch, Hann window
     and twiddles.
   - The operand of each spectral unit is its index in the spectral unit
     table. `spgate`, `spphase` and `spcomb` have extra operands, each only
     in songs whose units differ in it or use it (`vm/operands.go`).
   - `spcompress` with a nonzero attack or release is encoded with the
     stereo bit, and its attack and release follow the index as two more
     operands. Its state holds a smoothed envelope, a float for each bin
     (size/2+1) and channel, which moves toward the envelope each frame by
     1 - 2^(-hop/(44.1·T)·log2 e), hop = size/4, T = 2^(12p/128) - 1 ms,
     or 1 when T = 0; the attack when rising, the release when falling. The
     mean is then the mean of the smoothed envelope, as in `spcompress` in
     `vm/spectral.go`.
   - Stereo spectra keep their channels one after the other.
   - The float32 `log2f`, `exp2f` and `powf` must be computed operation by
     operation as in `vm/spectral.go`, or x87's 80-bit precision will make
     x86 differ. SSE would make that easy.

6. **Envelope curve.** The compiler refuses songs whose envelopes have the
   curve operand (a curve other than 0, or a modulated curve). The operand
   follows the gain; the x86 library, encoded with `AllFeatures`, already
   reads it as a sixth transformed parameter and ignores it. The envelope
   would need the curved stages of `envelopeStep` in `vm/go_synth.go`, with
   `exp2m1f`, computed in float32 operation by operation, and the level where
   the release starts, at offset 8 of the unit state.

7. **Math and formulas.** To render like the Go synth and the wasm player,
   the x86 players would need the same float32 routines (`exp2f`, `log2f`,
   `powf`, `sinTurns` in `vm/mathf.go`) instead of the x87 `fsin`/`f2xm1`,
   float32 oscillator phases, and the formulas listed under
   [Go synth behavior changes](#go-synth-behavior-changes). The waveshaper
   already matches.

8. **ott.** A stereo `ott` needs 11 floats of state (two crossovers × low and
   band × two channels, and three band levels) and a unit has 8. Instead of
   taking two unit slots, which would move the addresses of the units after
   it, the states live in a table of their own, like delay lines:
   `su_ott` in the wasm player, 44 bytes (11 floats) per state and
   `Patch.NumOtts()` states (ott units × voices), and `GoSynth.otts`. The
   player walks them with `$ottWRK`, reset to `su_ott` every sample and
   advanced by 44 after each ott, so the states go in the order the units
   run, voice by voice. A state holds, for channel i at float 4i, the low
   and band of the 88 Hz crossover, then those of the 2.5 kHz one, and the
   levels of the low, mid and high bands at floats 8 to 10. They are not
   cleared when a note is triggered. The x86 players would need the same
   table and pointer, `ott` as in `vm/ott.go`, and the float32 `exp2f` and
   `log2f` (see 7). The x86 template has a stub that leaves the signal
   unchanged; the compiler refuses ott for x86.

   `limiter` is kept the same way: `su_limiter`, 4112 bytes per state,
   `Patch.NumLimiters()` states, walked with `$limiterWRK`; see
   [limiter](#limiter) for the state and `vm/limiter.go` for the unit. Its
   lookahead is an operand byte after the transformed parameters, and its
   last transformed parameter, `drive`, is only there in songs that use it.
   The x86 template has a stub; the compiler refuses limiter for x86.

   `softclip`, `width` and `ladder` keep their state in the unit, as in
   `vm/shaping.go`: the four allpasses of an oversampled softclip at
   floats 0 to 3 for the left channel and 4 to 7 for the right, the low and
   band of the lowcut of width at floats 0 and 1, the four low-passes of a
   ladder at floats 0 to 3 and 4 to 7. softclip has an operand byte after
   the transformed parameters in songs with an oversampled softclip; the
   lowcut of width and the drive of ladder are only there in songs that use
   them. The x86 template has stubs; the compiler refuses the units for x86.

   `reverb` keeps its states in `su_reverb`, walked with `$reverbWRK`, and
   its constants in `su_reverb_consts`, with the index of the unit's as an
   operand byte after the transformed parameters; see
   [reverb unit](#reverb-unit) and `vm/reverb.go`. The x86 template has a
   stub; the compiler refuses the unit for x86.

   `convolution` keeps its states in `su_conv`, walked with `$convWRK`,
   and its constants in `su_conv_consts`, with the index of the unit's as
   an operand byte after the transformed parameters, of which `dry` is only
   there in songs that use it; it needs the buffers (3) and the FFT of the
   spectral units (5). See [convolution unit](#convolution-unit) and
   `vm/convolution.go`. The x86 template has a stub; the compiler refuses
   the unit for x86.

   **Automation.** `su_op_automation` reads the table `su_automation`
   (`Bytecode.Automation`, `vm/automation.go`) by the offset in its two
   operand bytes, keeps the offset of its record and the sample of the
   record before it in the first two cells of the unit, and needs the
   count of samples since the start of the song (`$globaltick`). The x86
   template has a stub; the compiler refuses the unit for x86.

9. **Bandlimited oscillators.** Flags 0x04 with 0x40, 0x20 or 0x10 mean
   bandlimited, so the gate test becomes flags & 0x74 == 0x04. Keep the
   phase parameter of the last sample in port 7 (offset 60 of the unit),
   compute dt and the corrections as in `oscillatorSine`, `oscillatorTrisaw`
   and `oscillatorPulse` in `vm/go_synth.go`, operation by operation in
   float32.

10. **Native bridge.** `MAX_VOICES` is 32 in the C header; `Polyphony` is a
   32-bit bitmask there.

11. **mc units.** Tables and states as in the wasm player (see
   [mc units](#mc-units)), and the units as in `vm/mc.go`, operation by
   operation in float32. Their operands after the index are only there when
   the units differ in them (`vm/operands.go`). The x86 templates have no stubs for them, as for
   the spectral units, so the x86 library does not assemble with
   `AllFeatures`; the compiler refuses songs with mc units for x86.

12. **16 output channels.** `su_synthworkspace` has left, right and
   `.aux resd 6`; the voices follow it, and global send addresses count
   from there. For channels above 7 (`Patch.MaxChannel`): `.aux resd 14`,
   in songs that use them only, the voices 32 bytes later, and the base of
   the global sends with them, as in the wasm player (`WideAux` in
   `player.wat` and `sinks.wat`). `su_op_aux` and `su_op_in` index the
   ports with their operand and need no change. The `SynthWorkspace` of
   the native bridge (`library.h`, `Aux[6]`) would always have the 14. The
   compiler refuses songs with a channel above 7 for x86, and the bridge
   such patches.

## Known differences left

- **Units the wasm player lacks:** sample oscillators (gm.dls).
- **Native synth:** it plays curved envelopes linearly, as the x86 library
  ignores the curve.
- **Against the x86 players:** the Go synth now follows the wasm player, so
  it differs from the x86 references wherever the wasm player did.
  - Some songs are beyond the regression tests' tolerance: `crush`, whose
    steps flip at quantization boundaries, and frequency modulation, whose
    float32 phases drift from the x87's 80 bits. `differsFromX86` in
    `vm/go_synth_test.go` skips them in the Go regression test.
  - `belleq`, `compressor` and `speed` also differ from x86 in places, as the
    wasm player always did.
- **CTest:** `tests/wasm_test_renderer.es6` never fails, because its
  `return 1` is inside an async function. The wasm-vs-x86 differences above
  went unnoticed there.
