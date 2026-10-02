# Changes in the sebbert-custom branch

This branch of Sointu adds macOS and CLAP plugins, audio samples and buffers,
granular synthesis, spectral processing, modules (reusable blocks of units)
and up to 255 voices. Most of the new
synthesis features exist only in the Go synth and the WebAssembly player; the
x86 players (`vm/compiler/templates/amd64-386`) and the native bridge were left
behind on purpose. The [x86 backend](#updating-the-x86-backend) section lists
what they would need to catch up.

The rule for the synths: the Go synth and the wasm player render
**identically**, sample for sample, for every unit the wasm player has. Tests
in `vm/compiler/wasm_*_test.go` render songs in both, with node and wat2wasm,
and require exactly the same output. That covers all the regression songs in
`tests/`, except `sync` and gm.dls samples, which the wasm player lacks. Small
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
- **Reverb module:** the module preset `Reverb` is the standard reverb, made
  of [mc units](#mc-units) (Go synth and wasm player only): stereo in, the
  wet signal out. Its chain is low cut, high cut, predelay, a diffuser of
  four steps (the early reflections) and a feedback delay network of 8 lines
  (the tail). Each module unit using it gets a bus of its own.
  `examples/reverb_module.yml` uses it. The instrument preset Global reverb
  (UTIL) is the aux signal through it: `in` from aux, the module unit, `out`.
  The preset carries the module, the same as the module preset, so a song
  gets it once.
- **Sidechain ducking:** the kick instrument itself turns down a bus. What
  should pump is sent to aux 4/5 instead of the main output (`aux`, channel
  4). The kick instrument, after its own `out`, reads that bus with `in`,
  multiplies it by a gain that the note of the kick triggers, and sends it
  out: `in`, the module unit, `out`. No second pattern and no compressor.
  An `outaux` in place of that `out` sends the ducked bus to the reverb too.
  `examples/ducking.yml` is a bass and a pad on the bus. Two limits:
  - A bus is an aux pair, and there are three: 2/3, which the reverb presets
    read, 4/5, which Kick ducker reads, and 6/7, which the delay presets
    read. `in` clears the pair, so one instrument reads a bus.
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
  `Ducking reverb` (26 units) and `Ducking delay` (13 units) are the Reverb
  and the Ping pong delay module with the wet signal turned down while the
  dry input plays: the space stays out of the way of the notes and blooms
  after them. A stereo `compressor` computes a gain from the input, a
  stereo `xch` puts it below the input, and after the module unit a stereo
  `mulp` multiplies the wet signal by it. They use the
  modules instead of copying their units, so their files carry `Reverb` or
  `Ping pong delay` too, the same as the module presets, and a song gets
  each once. Parameters: `size`, `decay`, `highs`, `lows` and `lowcut` of
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
  they are: Global ducking reverb (aux 2/3 through `Ducking reverb`, 28
  units), Global ping pong delay (aux 6/7 through `Ping pong delay`, 12
  units; its `outaux` can send the repeats on to the reverb) and Global
  mastering 2 ducking (aux 6/7 through `Ducking delay`, a quarter of it on
  to the reverb, aux 2/3 through `Ducking reverb`, then Global mastering 2;
  50 units).

  All of this is measured on rendered audio, not judged by ear. Tests:
  `vm/compiler/wasm_ducking_test.go` (what the modules do, and that the
  wasm player renders them, the example and Global mastering 2 ducking like
  the Go synth) and `TestBuiltinModulePresetsCanonical` and
  `TestDuckingPresets` in `tracker/module_test.go`.
- **Global mastering presets** (UTIL), next to upstream's Global mastering,
  which is unchanged:
  - Global mastering reverb: the aux signal through the Reverb module, then
    Global mastering as it is.
  - Global mastering 2, for loud music with a clean bass: a low cut of
    12 dB per octave at 27 Hz, a compressor (about 4.6:1 above -6 dB, 10 ms
    attack, 150 ms release, +7.5 dB makeup), a `limiter` (-0.3 dB, 2.9 ms
    lookahead, +4.4 dB drive) and a `clip` at full scale for what little
    the limiter lets through. With the limiter it is Go synth and wasm
    player only.
  - Global mastering 2 reverb: the reverb, then Global mastering 2.

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

  The level of the reverb (`gain` of `mcspread`), the level and width of the
  early reflections and of the tail (`mcsum`) and the rate of the modulation
  are not parameters: change them in the module. Small sizes with long
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
  drumbus reverb with the Reverb module, are Global mastering 2 with a
  group bus for the drums in front. The channels: 0/1 the mix, 2/3 the
  reverb send, 4/5 left free for a sidechain bus, 6/7 the drum bus.
  - The drums send to the bus with an `aux` unit, channel 6, in place of
    their `out` unit. The drum presets come with `out`, so that a preset
    makes sound in any song, also one without this Global instrument; the
    comment of each says which unit to change, and `gain` stays as it is.
  - The Global instrument reads the bus (`in`, channel 6), compresses it
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
  Compiled for wasm they are 9.9 and 8.7 KB (4.3 and 4.2 KB gzipped).
  Tests: `tracker/soundset_test.go`, and `vm/compiler/wasm_soundset_test.go`
  (every preset and both songs render in the wasm player exactly as in the
  Go synth; the long song takes most of two minutes and is skipped with
  `-short`).

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
| `softclip`, `limiter`, `width`, `ladder`, `envelope` | drive and oversampling of `softclip`; drive of `limiter` and `ladder`, lowcut of `width`, curve of `envelope` (optional last parameters, `optionalParams`) |
| `oscillator` | the corrections of each bandlimited waveform; the LFO code |
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
sointu-compile -arch wasm -js [-stages N] [-cuts a,b] [-samples] -o out/song song.yml
wat2wasm -o out/song.wasm out/song.wat
```

writes `song.wat`, `song.js`, `song.d.ts` and, with `-samples`, `song.0.ogg`
and so on. `examples/code/web` is a vite and websqz (rootsqz) project that
uses them; its README has the usage.

```js
import wasm from "./song.wasm?websqz-bin";
import { load, duration, rowsPerSecond } from "./song.js";
const song = load(wasm);              // renders in workers; load(wasm, 4): 4 s of runway
await song.ready;                     // the runway is rendered
onclick = () => song.start();         // plays what is rendered, and the rest as it comes
const t = song.time();                // seconds played: the clock of the visuals
```

`song.rendered` is the seconds rendered so far, `song.context` the
`AudioContext`, and `song.start(node)` plays into a node of it.

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
  default) and rendering the rest at 0.8 times the speed measured so far
  ends before the song gets there: `rendered >= duration * (1 - 0.8 *
  speed)`, with the speed in seconds of song for a second. A song that
  renders slower than it plays waits until enough is rendered.
- **If playing catches up** with rendering anyway, the next piece plays
  0.05 to 0.3 s after it arrives, and the clock stops until then: silence,
  and visuals that wait, instead of visuals that run ahead.
- **Clock**: `time()` is `context.currentTime` minus the start time, never
  going back. The row is `time() * rowsPerSecond`. The wasm player has
  neither the `sync` unit nor `-r`: with a fixed tempo, the row follows from
  the time. Songs with the `speed` unit get no module.
- **Samples** are decoded on the page (`decodeAudioData`, which workers do
  not have) and posted to the workers.

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

It then picks the cuts that make the most expensive stage cheapest, from a
cost for each unit fitted to the example songs (`unitCost`), and prints the
stages. `-cuts` sets them by hand. A song with fewer possible cuts gets
fewer stages. The player gets a table of the stages, the code that copies
the cells, and `g(stage)`; without the call an instance runs all voices,
which is the fallback.

Limits: the speedup ends at the most expensive voice (a master chain with a
reverb is one stage), a stage that falls behind queues tapes without bound
(4 bytes for each cell and sample), and the first audio comes a chunk later
for each stage.

### Numbers

Apple M3 Pro, Chrome 154, `tests/wasm_runtime_browser.mjs --scenario
measure`, best of two; the steady rate leaves out the start of the workers
(0.3 to 0.4 s in a browser that just started).

| Song | Workers | Seconds of song for a second | Ready (2 s runway) | All rendered |
|---|---|---|---|---|
| `soundset_loop` (13.7 s) | 1 | 9.1 | 0.59 s | 1.87 s |
| | 2 | 16.9 | 0.43 s | 1.12 s |
| | 4 | 30.9 | 0.45 s | 0.83 s |
| | 8 | 35.9 | 0.41 s | 0.72 s |
| `soundset` (68.6 s) | 1 | 3.3 | 0.88 s | 21.0 s |
| | 2 | 6.4 | 0.74 s | 11.1 s |
| | 4 | 10.1 | 0.60 s | 7.2 s |
| | 8 | 16.4 | 0.58 s | 4.7 s |
| `ducking` (6.9 s) | 1 | 28 | 0.43 s | 0.60 s |
| | 5 (of 8 asked) | 94 | 0.46 s | 0.51 s |
| `reverb_module` (14.4 s) | 1 | 31 | 0.40 s | 0.79 s |
| | 5 (of 8 asked) | 56 | 0.34 s | 0.55 s |

The sound starts 0.05 to 0.3 s after `start()`. While rendering, the main
thread was not held for more than 13 ms in 15 of the 16 runs, and 48 ms in
one. The player that renders at instantiation blocks the page for the whole
render: 1.6 s for `soundset_loop` in Chrome.

Sizes, for `soundset_loop` with the smallest use (load, start, time), bundled
with vite 7 and packed with websqz 0.4:

| | JavaScript, minified | wasm | Packed page |
|---|---|---|---|
| Before: render at instantiation, one buffer | 479 B | 8300 B | 5366 B |
| `-js` | 1402 B | 8268 B | 5800 B |
| `-js -stages 4` | 1747 B | 8828 B | 6168 B |
| `-js -stages 8` | 1755 B | 9092 B | 6243 B |

### Samples and the packer

websqz stores `--pre-compressed-files` (`?websqz-bin&compressed` with the
plugin) as they are, after the compressed data. Measured with Opus files of 9
to 62 KB: compressing them with the rest never makes them larger, it gains
0.2 to 1 % (the Ogg pages), so samples inside the wasm are the smallest:
43104 B of Opus cost 42989 B compressed and 43126 B stored. But the
decompressor of websqz takes about 25 µs for each byte: that file delays the
start of the intro by 1.05 s compressed, and not at all stored. `-samples`
is for that: 0.3 % more bytes for the samples, a second less for each 40 KB.

### Verified, and not

- The progressive player renders, in parts of any size, the bytes of the
  one-shot player, for all regression and example songs
  (`wasm_progressive_test.go`), which other tests compare with the Go synth.
- The pipeline renders the same bytes for every cut the compiler allows in
  those songs (soundset: all 29), and for songs made for each kind of
  dependency, where the cuts found are checked too (`wasm_stages_test.go`).
- In headless Chrome 154 and Firefox 157 (`TestRuntimeInBrowser`, with
  `SOINTU_TEST_BROWSER=1` or `=firefox`): an AudioWorklet records what the
  audio context plays, and it is the one-shot render sample for sample,
  without a gap, in a worker, in 4 stages, on the main thread, with 16 bit
  output, with samples in the wasm and as files; with audio held back for 3
  s, the sound has one gap of silence, the clock stops and never goes back.
- The packed example runs in headless Chrome from http (4 workers), not
  from `file://`, where websqz cannot read the page; an unpacked single file
  runs from `file://` with workers.
- Not verified: Safari, a real sound card (the browsers ran muted and
  headless), x86 processors (denormals may slow the wasm player there, which
  has no flush-to-zero), mobile browsers, and how the clock steps on audio
  hardware with large buffers.

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

## Known differences left

- **Units the wasm player lacks:** `sync`, and sample oscillators (gm.dls).
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
