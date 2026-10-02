# Changes in the sebbert-custom branch

This branch of Sointu adds macOS and CLAP plugins, audio samples and buffers,
granular synthesis, spectral processing, modules (reusable blocks of units),
a graphical parametric equalizer, up to 255 voices and 16 output channels
(seven aux pairs). Most of the new
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
  `make install-vst`, `make install-clap`.
- A CLAP plugin (`cmd/sointu-clap`, `cmd/plugin` shared with the VST2) with 4
  note ports of 16 MIDI channels each: 64 MIDI channels.
- The plugins follow the tempo of the host, one way: the song's tempo can be
  edited, and it follows again when the host's tempo changes.
- The song is saved in the host's project. Edits mark the project unsaved
  (CLAP `mark_dirty`, VST2 `audioMasterUpdateDisplay`).
- The plugin window comes to the front when it opens. Edit → Keep window on
  top keeps it above other windows; the setting is saved in the user's
  `preferences.yml`.
- Vendored: Gio (`third_party/gio`, macOS plugin window and event loop fixes,
  `TopMost` on Windows and turning it off, `pointer.Event.Notch` for the
  notches of a mouse wheel on macOS),
  vst2 (`third_party/vst2`, `Host.UpdateDisplay`), CLAP headers.

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

## New units

All of these are Go and wasm only.

| Unit | What it does |
|---|---|
| `bufread` | Plays a buffer: note tracking, looping with crossfade, modulatable start/loop points, negative start counting back from the newest frame, `speed` (-1 to 1), edge fade |
| `bufwrite` | Writes into a buffer: a continuous ring buffer, or `oneshot` while the note is held. Several writers mix; `feedback`, `pop` |
| `spawn` | Triggers voices of another instrument: at a `rate`, synced to the beat, or on a rising edge. Transpose, note length, up to 4 `args`, `steal` |
| `arg` | Pushes a value passed by the `spawn` that triggered the voice |
| `window` | A smoothstep window over the spawned note, for click-free grains |
| `spfft`, `spifft`, `spcopy` | Spectral analysis and resynthesis (Hann, 4× overlap, 256 to 8192 samples, mono or stereo), copying spectra |
| `spfilter`, `spcompress`, `spblur`, `spgate`, `spphase`, `spscale`, `spformant` | Change a spectrum in place: band cut and tilt, magnitudes pulled to their mean (with optional attack and release per bin), time smoothing and freeze, gate, phase dispersion/randomization/robot, bin scaling and shifting, formant shift |
| `spcross`, `spcomb` | Cross-synthesis/vocoder with another spectrum; resonances at the harmonics of up to 8 notes held in another instrument |
| `ott` | A three-band upward and downward compressor, like Ableton's OTT preset: crossovers at 88.3 Hz and 2.5 kHz, `depth`, `time`, `upward`, `downward`, a gain per band |
| `limiter` | A lookahead peak limiter: `threshold`, `release`, `lookahead` (0 to 11.5 ms, which is how late its output is), and `drive`, a gain before it of up to 18 dB that costs nothing in songs that do not use it. See [limiter](#limiter) |
| `softclip` | A clipper with a soft knee, a saturator that leaves the signal as it is below `knee`: `drive` (up to 18 dB), `knee`, and `oversample`, which clips at twice the sample rate against aliasing and costs nothing in songs that do not use it. See [softclip, width and ladder](#softclip-width-and-ladder) |
| `width` | Stereo width: scales the side signal (0 mono, 64 as it is, 128 double), with `lowcut`, a high-pass on the side signal that makes the bass mono and costs nothing in songs that do not use it |
| `ladder` | A low-pass of 24 dB per octave with resonance up to self-oscillation, like a Moog ladder: `frequency`, `resonance`, and `drive` into its saturator, up to 18 dB, that costs nothing in songs that do not use it |
| `reverb` | The standard reverb as one unit: low cut, high cut, predelay, a diffuser and a feedback delay network of 8 lines, stereo in, wet out. Renders what the Reverb module preset renders, in a third of its bytes; more parameters set what the module fixes. See [reverb unit](#reverb-unit) |
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
shows it. It is meant for meters on every unit; so far the eq editor uses
it.

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

A meter on every unit would be this with other watches: a unit of an
instrument is at `TapPoint{instrument, index}` (after it: index + 1), a unit
of a module at the place of its copy (`Model.playedUnit` finds the copy;
its index in `Model.expanded` is the place), and a `tapWatch` would get a
level, or a ring of frames for an oscilloscope, next to `spectrum`, filled
in `Model.tapped`. Whether the signal there is mono or stereo is what
`derivedInstrument.rails` knows. What is not there yet: a tap is the top of
the stack, so for a unit that puts nothing on the stack (`send`, `out`,
`pop`) it is the signal below; one place per watch, so every unit of a rack
is as many taps, each compared with every unit run, which a table by unit
would make cheap; the native synth and the compiled players have no taps.

Tests: `vm/tap_test.go` (before and after a unit, one voice and all, stereo,
the multithread synth, disabled units, after an update, the render as
without taps) and `tracker/taps_test.go` (noise through an eq, with a model,
a player and the Go synth: the spectrum after the eq is that before it
moved by the curve of the editor, within 1.5 dB over three ranges; the
taps follow the eq when its units change, also in a module; a second after
the last time they were asked for, the synth records nothing).

## Tracker

- **Buffers tab:** import samples, encoding presets and formats, preview,
  writable buffers for recording (length, clear, fit to recording), a
  waveform with the write head and where `bufread` notes play, and live
  spectra of spectrum buffers.
- **Plots:** zoom with Alt+scroll, pan by dragging or horizontal scrolling,
  Alt+drag to zoom y, double-click or right-click to reset, bounded zoom out.
- **Units:** spawn targets follow instruments when they move or are pasted;
  new spectral units pick up the spectrum above them.
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
  following the tempo, a switch, or a knob with the same scale and labels.
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
- **Unfolding:** a module unit is like a section that can be collapsed: the
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
  `Ping pong delay` on aux 6/7 next to a reverb unit and the drum bus: see
  the drum bus below.

  All of this is measured on rendered audio, not judged by ear. Tests:
  `vm/compiler/wasm_ducking_test.go` (what the modules do, and that the
  wasm player renders them, the example and Global mastering 2 ducking like
  the Go synth) and `TestBuiltinModulePresetsCanonical` and
  `TestDuckingPresets` in `tracker/module_test.go`.
- **EQ editor:** while the selected unit is an `eq` unit (see [eq](#eq)),
  its editor is under the rack: a plot from 20 Hz to 20 kHz and ±24 dB with
  the curve of every band, the curve of all the units (what is heard: see
  the response there) and a numbered handle for every band, at the
  frequency and the gain that its units have, or at 0 dB for a band without
  gain. Under it the selected band: its type (a menu), on or off, its
  frequency, gain and Q as numbers to type (`1.2k`, `-4,5 dB`), then what
  it was compiled to (`→ 990 Hz · 3.12 dB · Q 1.23 · 1 unit`, with
  `Go synth and wasm only` for a ladder), the number of units of the eq and
  the gain of its gain unit. `examples/eq.yml` has three.
  - *Mouse.* Dragging a handle changes the frequency and the gain, or for a
    band without gain the frequency and the Q; with Shift a fifth as far;
    with Alt the Q only (60 dp up doubles it). Scrolling over the plot
    changes the Q of the band under the pointer, or of the selected one. A
    double click on empty space adds a band there and drags it: a bell, a
    low cut below 40 Hz, a `ladder` high cut above 12 kHz. A double click
    on a handle removes its band; the right button switches it on or off.
  - *Keys*, with the focus on the plot (Tab, or a click): Left and Right
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
  - `tracker/eq.go` (`Model.EQ`), `tracker/gioui/eq_editor.go`
    (`eqGeometry`, `eqHit`, `eqDragged`: where things are and what a drag
    does, without a window).
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
| `spawn`, `window` | each mode, arguments, stealing, note tracking, transpose, note length and the release loop, the test for a missing target; own length, note length and the clamp of a modulated shape of `window`. Operand: the flags of `spawn` |
| spectral units | the first-voice check and the voice in the table; the loops over channels, the rings of `spifft` and the channels in the spectrum table (only with a stereo spectrum); low, high and tilt of `spfilter` (`$log2f`, `$powf`); freeze of `spblur`; each mode of `spphase` (`$rotate`, `$tablePhase`, `$randomPhase`); scale and shift of `spscale`; voices and intervals of `spcomb`. Operands: invert of `spgate`, mode of `spphase`, voices and intervals of `spcomb` |
| `ott` | time, upward, downward, the right channel's powers |
| `reverb` | the modulation of its lines; each filter of its input, and the test for it when only some units have it; the levels, widths and rate as constants of the unit instead of the code; the number of steps; allpasses in the diffuser, and plain delays; the second set of lines. See [reverb unit](#reverb-unit) |
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
sointu-compile -arch wasm -js [-stages N] [-cuts a,b] [-samples] [-r] -o out/song song.yml
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
  unit get no module.
- **Sync values**: in songs with `sync` units, `sync(channel)` is the signal
  at a sync unit at the time of `time()`, and `sync(channel, t)` at another
  time: see below.
- **Samples** are decoded on the page (`decodeAudioData`, which workers do
  not have) and posted to the workers.

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
  `limiter`, `reverb`): a stage starts at the place of its first voice in
  each table. A `reverb` unit can be modulated from an earlier stage like
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
