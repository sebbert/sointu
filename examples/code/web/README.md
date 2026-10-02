# Web runtime example

Plays a song in the browser with the JavaScript module that
`sointu-compile -arch wasm -js` writes: the song renders in workers while the
page does other things, starts when a runway is rendered, and the frame loop
reads the audio clock. Bundled with [vite](https://vite.dev) and packed into
one html file with [websqz/rootsqz](https://github.com/r00tkids/rootsqz)
through [rollup-plugin-websqz](https://github.com/r00tkids/rollup-plugin-websqz).
The design, the numbers and what is verified are in `FORK.md`, "Web runtime".

Requirements: go, `wat2wasm` ([wabt](https://github.com/WebAssembly/wabt)),
node, and for `npm run build` the packer built from GitHub, as its npm
releases are behind. For songs with samples, ffmpeg.

```
cargo install --git https://github.com/r00tkids/rootsqz   # installs `websqz`
npm install
npm run dev      # compiles the song, serves the page with vite
npm run build    # compiles the song, bundles, packs to dist/websqz-output/index.html
npm run serve    # serves the packed page on http://127.0.0.1:8080
```

The packed page reads itself with `fetch`, so it needs http: it does not
start from `file://` in Chrome. The plugin (1.1.3) makes the imports read
`wsqz.files`, and the packer from GitHub names that `rsqz`: `vite.config.js`
adds `wsqz=self.wsqz||rsqz;` in front of the bundle, without which the packed
page stops with an error. The plugin has no option for the packer's
`--size-profile 64k`.

`node song.mjs path/to/song.yml -stages 8` compiles another song, or the same
with other flags. It runs:

```
sointu-compile -arch wasm -js -stages 4 -o src/song song.yml   # song.wat, song.js, song.d.ts
wat2wasm -o src/song.wasm src/song.wat
```

## Using the module in an intro

Copy `song.wasm`, `song.js` and `song.d.ts` into the intro (or generate them
there in the build). The module is source: the bundler minifies it with the
intro, and it has only the code this song needs.

```js
import wasm from "./song.wasm?websqz-bin"; // the bytes, from the packed page
import { load, rowsPerSecond } from "./song.js";

const song = load(wasm);        // rendering starts now, in the background
await song.ready;               // the runway is rendered
button.onclick = () => {
  song.start();                 // in a user gesture; plays what is rendered, the rest as it comes
  requestAnimationFrame(function frame() {
    const t = song.time();      // seconds of the song played: the clock for the visuals
    const row = t * rowsPerSecond;
    draw(t, row);
    requestAnimationFrame(frame);
  });
};
```

- `load(wasm, runway, margin)`: the song is ready when `runway` seconds are
  rendered (2 by default) and rendering the rest at `margin` times the
  speed measured so far (0.8 by default) ends before playing gets there.
  The audio is scheduled from the main thread, half a second at a time as
  it arrives, so the runway has to cover the longest time the intro keeps
  the main thread busy after the start: `load(wasm, 5)` if a shader compile
  can block for 4 s. A lower margin is safer when the machine will be
  busier after the start than while the runway rendered: `load(wasm, 2,
  0.5)` expects half the speed. `load(wasm, 1e9)` waits for the whole song.
- `song.rendered` is the seconds rendered so far, for a loading bar;
  `duration` (exported) is the length of the song.
- `song.context` is the `AudioContext` (44100 Hz). `song.start(node)` plays
  into a node of it instead of the speakers, e.g. an `AnalyserNode` that is
  connected on to `song.context.destination`.
- `song.time()` never goes back. If playing catches up with rendering, the
  sound stops until the next half second of audio is there, and the clock
  stops with it, so the visuals stay in sync.
- Songs with `sync` units get `song.sync(channel)`: the signal at the
  sync unit at the time of `song.time()`, with a value for every 256 samples
  (5.8 ms), e.g. an envelope to flash with the kick. The channels are the
  sync units in the order of the patch, voice by voice; `syncChannels`
  (exported) is their number. `song.sync(channel, t)` reads another time.
  Compiled with `-r`, channel 0 is the row with its fraction and the units
  follow; with a fixed tempo that is `song.time() * rowsPerSecond`, so `-r`
  is only for hosts that read the sync buffer themselves. Songs without sync
  units have none of this.
- The source text of the function `renderer` is the worker, so the bundler
  must leave it using nothing outside itself. The default minifier of vite
  does. A build target old enough to make the bundler add helper functions
  for arrow functions or destructuring would break it.
- `-stages N` renders in a pipeline of N workers (see `FORK.md`): pick N for
  the machine the intro runs on, as stages beyond its cores do not help.
  `sointu-compile` prints how it cut the song.

Without websqz, give `load` the bytes any other way:

```js
import url from "./song.wasm?url";
const song = load(await (await fetch(url)).arrayBuffer());
```

### Samples

A song with samples (buffers with encoded audio) carries them in the `.wasm`
as custom sections by default, and the module finds them there. With
`-samples`, `sointu-compile` writes them as files (`song.0.ogg`, ...)
instead, and `load` takes them as its second argument, so the packer can
store them as they are instead of compressing the encoded audio again:

```js
import wasm from "./song.wasm?websqz-bin";
import sample0 from "./song.0.ogg?websqz-bin&compressed";
const song = load(wasm, [sample0]);
```
