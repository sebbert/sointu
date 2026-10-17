package mcp

import "strings"

// Guide is what the tool guide returns: how sointu patches work, for a
// language model that has not seen the repository.
var Guide = strings.TrimSpace(`
# How sointu patches work

## The stack
Each instrument is a list of units that run from top to bottom, once per sample, for each of its voices. Units take signals from a stack and push signals onto it:
- sources push: envelope, oscillator, noise, loadval, loadnote, velocity, in, receive, bufread
- effects change the top signal: filter, distort, delay, compressor (pushes a gain), belleq, ladder, softclip, limiter, ...
- mulp / addp merge the two top signals into one (oscillator times envelope), mul / add keep both, push duplicates, xch swaps, pop drops
- sinks pop: out (main output), outaux (main and aux), aux (to any output channel), send (with sendpop)
With stereo=1 a unit works on two signals (left on top). pan turns one signal into two. The listings show [before>after]: the signals on the stack before and after each unit. At the end of each voice the stack must be empty; a unit that needs more signals than there are is a PROBLEM shown in the listings.

A typical voice: envelope, oscillator, mulp, filter, pan, outaux.

## Parameters
Whole numbers, mostly 0 to 128; 64 is often neutral (transpose, detune, panning, send amount). The listings show what the tracker displays: frequency=40(686 Hz). Strings name displayed values: type: "sine". unit_types with a type gives ranges and defaults. Times of envelopes are exponential in their value: 64 is about 93 ms. gain 128 is 0 dB, 64 is -6 dB.

## Sends: modulation
A send adds the top signal times (amount-64)/64 to a parameter of another unit (target: the unit ID, port: the name of the parameter). The port only exists while the send runs: it modulates around the value the parameter has. An LFO is an oscillator with lfo=1, then a send with sendpop=1. A send can target a unit of another instrument; voice picks the voice of the target (0: the same voice, for sends within the instrument).

## Automation
A plugin shows its host 64 parameters, the automation slots A1 to A64. automate makes a parameter of a unit that a send can modulate a target of the lane of a slot: the slot then offsets the parameter, in steps of the parameter (-128 to 128) times the amount of the target (-1 to 1, 1 if not given), on top of the value the unit has, which stays. A lane is bipolar (the host moves it from -128 to 128, 0 in the middle: offsets either way around the unit's value) or unipolar (automate unipolar true: from 0 at the bottom of the host's range to 128 at the top): unipolar suits a unit value at the bottom of its range, e.g. a gain at 0 that the host raises; a negative amount then turns a parameter down from its value as the slot rises. A lane can have many targets (automate with the slot of a lane that is there): one slot is then a macro that moves several parameters at once, each by its amount, e.g. a filter opening while the noise rises and the amp gain falls (amount -0.5). Targets are no units: the synth adds the lanes to their parameters before the units run, so they cost no units of the 63 of an instrument. get_instrument marks such a parameter (A3 +0.0: ..., with the amount when it is not 1), get_song lists the lanes and their targets. The host shows a slot by its first target (A3 Bass: filter frequency, +2 with two more targets), or by the name given with automate. The user automates the slot in the host; recording in the plugin turns that into the points of the lane (rows of the song, linear between points), with the notes. The song plays the points when it is played in the tracker and when it is compiled; set_automation writes them directly. Each lane with points costs bytes in the compiled song: about 6, 3 to 4 for each point and 4 for each target. Go synth and wasm player only.

## Notes and voices
Notes are numbered as in the tracker, and as MIDI input arrives in the plugin; the tracker names them by their frequency: 60 is C-3 (130.8 Hz for an oscillator with transpose 64), 69 is A-3 (220 Hz), 36 is C-1 (32.7 Hz). So note 60 from a MIDI keyboard, its C4 key, plays C3 unless the instrument transposes. render_note names the pitch it measures the same way. An instrument with N voices plays N notes at once; each voice has its own state. In the plugin, MIDI channels trigger instruments.

## Instruments, buses and mastering
Instruments run in their order, each sample all voices of one before the next. Output channels: main (left/right) and seven aux pairs, aux1 to aux7. out adds to main, outaux to main and aux1, aux to any channel. in reads a channel and clears it: an instrument with in is a bus, and it hears only the instruments before it. So effects shared by several instruments (reverb, delay, a drum bus, the master chain) are instruments at the end, with one voice, that read their channel with in, process it, and out it (or aux it on to the next bus). get_song marks them "bus: reads ...".
- An instrument after the bus that reads its channel bypasses that bus: after the master chain it is not limited or compressed. add_instrument therefore puts a new instrument before the first bus unless told otherwise; move_instrument fixes the order. get_song warns about it.
- Once a bus reads a channel it is empty: a second bus reading it later gets only what was added in between. A bus that reads a channel must out or aux it again, or it is silent.
- outaux also feeds aux1: if aux1 is a reverb bus, its auxgain is the reverb send. A channel no bus reads goes to that output of the plugin: aux channels to the extra outputs, not the main mix.
The presets in UTIL (Global ...) are such buses and master chains; Global mastering 2 buses: aux1 reverb, aux3 ping pong delay, aux4 drum bus, main through a compressor, limiter and clip. For loud music: drums to a bus with glue compression and soft clipping, bass mono below about 120 Hz (width lowcut), a limiter at the end with peaks near -0.3 dBFS.

## Quirks
- MIDI: each instrument listens on a MIDI channel. With channel auto (the default), the instruments take the lowest free channels in their order, buses included; so adding or moving an instrument can shift the channels of those after it, and the user's DAW tracks then play another instrument. The results say so: tell the user.
- Voices: N voices play N notes at once; a new note takes the voice released longest ago, else the oldest held one; with 1 voice each note cuts the previous one (mono). Every voice runs all its units all the time, even silent: voices cost CPU in the player.
- Unison: the oscillator's unison 0 to 3 adds 1 to 3 detuned copies inside one voice, at full gain each (4 copies are about 12 dB louder: lower gain), with detune spreading them and a phase offset each; with stereo=1 the right side detunes the other way. A supersaw: trisaw with color 0 or 128 (64 is a triangle), unison 3, detune above 64, stereo, bandlimit 1 against aliasing; for more copies, more oscillators with other detunes, added with addp.
- Envelopes: attack, decay and release are times, sustain a level; the release starts at note off (render_note holds the note for its duration). sustain 0 makes it percussive; attack 0 can click.
- filter: frequency is the resonance peak, not linear in Hz (see the displayed value); resonance is inverted: 128 none, lower values more. lowpass, bandpass and highpass add; for 24 dB per octave use two filters or ladder.
- Gain: out and outaux gain 64 is -6 dB; the instruments sum on main, so leave headroom for the master chain.
- Units are named by their ID (#12), which stays when units are added or moved; indices and instrument numbers change.
- Loading a preset into an instrument (edit_instrument preset) replaces all its units.
- In the tracker, each instrument has tracks in the score; in the plugin the DAW sends the notes and the score does not matter.

## Buffers and grains
bufwrite records the signal into an audio buffer of the song (a ring: the newest frames replace the oldest; several writers, also the voices of one instrument, mix), bufread plays it: start counts frames from the oldest one, a negative start back from the newest; a send to start shifts it by a part of the buffer (amount 0.1: a tenth of its length). A bufwrite unit added without a buffer gets one of its own, as long as its length parameter (frames, 44100 a second), and a bufread unit added without one plays the buffer of the nearest bufwrite unit of its instrument: so add both without buffer. add_buffer is for a buffer that several instruments share, e.g. grains; the buffer parameter then takes the number of the buffer in the list of get_song, from 1.
Delay lines: a bufread with mode tap reads the frame at start every sample instead of playing on from it: start -22050 is half a second behind the write head, always, also without a note (in a bus) and while a send moves start (chorus, flanger). A tap before the bufwrite in the same instrument, mixed into what is written (tap, gain, addp with the input, bufwrite with pop 0), is a feedback delay; put a filter in the loop to damp it. timing BPM counts start in 48ths of a beat (48 a quarter note, 36 a dotted eighth, 24 an eighth), timing pitch in frames at note 0, shorter by half per octave (10788: one period of the note, for a plucked string or a comb). With pervoice 1 on the bufwrite every voice has its own buffer, as a string per note needs. edgefade on a playing (not tap) bufread crossfades across the write head of a full ring, for grains that the write head overtakes.
Slices (drum breaks): set_slices puts markers in a buffer; get_song lists them. A bufread with slices continue or stop plays from the marker of slice (0 is the start of the buffer); with note slice, from slice note - 60 + slice (C-3 is slice 0, chromatic up; the pitch does not follow the note), with note slice and pitch the same, and the pitch follows the note too (C-3 at the recorded pitch); wrapped around the number of slices. start is an offset from the marker; slices stop is silent past the next marker (edgefade fades at the slice edges); loopstart and looplength are in 128ths of the slice. A send to slice adds 128 slices per 1: loadnote sent at amount 96 adds note - 64.
bufread mode: one shot plays from start on; loop repeats the loop once it gets there, with loopfade a crossfade at its end; ping-pong plays the loop forwards and backwards in turn, without a crossfade; tap reads a delay line. note: none, pitch (note 60 is the recorded pitch), slice, or slice and pitch; transpose and detune apply with each.
Grains: a spawn unit triggers voices of another instrument (instrument counts from 1 in the order of get_song: 2 is the instrument with index 1; check the name shown), at a rate, in sync with the beat, or on a rising edge of the top signal, each for length. It passes the args top signals to the voice, which reads them with arg: e.g. two noise units for a random position and pan. The grain instrument: window, arg, send to start of the bufread, bufread, mulp, pan, out. A spawn only fires while a note is held on its own instrument: in every voice of an instrument it spawns per held note; in an instrument of its own it needs a note from the score or the DAW on its MIDI channel. With notetracking 1 the grains take the note of the spawner, else note 60: the recorded pitch.

## Modules
A module is a reusable block of units. A unit of the type module stands for its units (module: its name), with p1 to p8 setting the parameters of the module. Changing a module changes every use of it. The units of a module can bind their parameters to the module parameters (edit_units bind); a send to a module unit modulates the parameters bound to that port. Module units, and eq units, count with all the units they stand for.

## Convolution
convolution convolves the signal with an impulse response read from an audio buffer, without latency: a reverb from decaying noise, a cabinet or a body, any fixed filter. length is the part of the buffer used, from start. The response is a sample, or what a bufwrite unit with oneshot wrote while its note was held: the song then makes its own response, which takes as long to write as it lasts, and is read again while playing (follow: how fast, fade: changes fade in instead of stepping). The module preset Convolution reverb is noise with an envelope and filters into a buffer and a convolution unit reading it; its instrument needs a note as long as the decay, e.g. in the first row. The response is not normalized: decaying noise of 2 s is about +25 dB, so gain is far below 0 dB. It costs about 2 KB in the player (1 KB compressed), 0.4 KB with a response of 64 frames.

## Limits and costs
- 63 units per instrument, counting what module and eq units stand for. 255 voices in all.
- This is for 4k/64k intros: every unit, parameter value and instrument costs bytes in the compiled player (a unit is roughly 2 to 10 bytes of data, a new unit type costs its code once, about 50 to 1000 bytes). Prefer reusing unit types the song already has, and fewer instruments. Units marked go/wasm only (reverb, convolution, ott, limiter, softclip, width, ladder, spectral and mc units) cannot be compiled for x86, which is fine for the wasm player.
- Disabled units cost nothing and are not played.

## Working with these tools
0. The user edits the patch in the tracker far more than through you, also between your calls. Never trust what you read earlier in the conversation: read the instrument (get_instrument) right before changing it, and get_song before changing the structure. A result that starts with NOTE: the song changed, means what you knew is out of date. get_selection tells what the user is looking at: "this sound" usually means the selected instrument.
1. get_song, then get_instrument of what to change; unit_types for unfamiliar units; guide is this text.
2. For a new sound, start from a preset when one is close (list_presets): they are tuned. Before changing a sound, render_note it to have numbers to compare against. Then change, and render again. Use render_note with edits to try variants without touching the song, e.g. a unit disabled (dry/wet) or a parameter at several values.
3. Make one coherent change per call; say what you changed and why. The user hears it at once and can undo (undo tool, or in the tracker).
4. You cannot hear. The numbers tell whether a change did what you meant (level, pitch, bands, envelope, clipping), not whether it sounds good: ask the user, and offer play_note.
5. render_note output dry (default) measures the instrument alone; output master runs it through the bus and mastering instruments. For your own analysis (e.g. with numpy), pass wav: true and read the file.
`)
