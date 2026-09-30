# Changes in the sebbert-custom branch

This branch of Sointu adds macOS and CLAP plugins, audio samples and buffers,
granular synthesis, spectral processing and up to 255 voices. Most of the new
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
  `TopMost` on Windows and turning it off),
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
- `Song.EncodingPresets`: named ffmpeg encodings that samples share.

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

Spectral units run only in the first voice of their instrument. See the
README for the details of each unit.

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
effect, and the wasm player's correction code, the dt computation and the
stricter gate test are included only when some oscillator uses it; songs
without it compile to the same wasm as before.

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
- **Warnings:** spectral units in instruments with several voices, spectra
  with several writers, and buffers or spectra used across threads.
- **Other:** no notes play while typing in text fields; recordings survive
  synth rebuilds; NaNs recorded into buffers are cleared.

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

5. **Spectral units.**
   - Tables are in `su_spectral`, laid out by `wasmSpectral` in
     `vm/compiler/compiler.go`: spectra, unit states, scratch, Hann window
     and twiddles.
   - The operand of each spectral unit is its index in the spectral unit
     table. `spgate`, `spphase` and `spcomb` have extra operands.
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

9. **Bandlimited oscillators.** Flags 0x04 with 0x40, 0x20 or 0x10 mean
   bandlimited, so the gate test becomes flags & 0x74 == 0x04. Keep the
   phase parameter of the last sample in port 7 (offset 60 of the unit),
   compute dt and the corrections as in `oscillatorSine`, `oscillatorTrisaw`
   and `oscillatorPulse` in `vm/go_synth.go`, operation by operation in
   float32.

10. **Native bridge.** `MAX_VOICES` is 32 in the C header; `Polyphony` is a
   32-bit bitmask there.

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
