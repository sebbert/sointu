# Web runtime example

Plays a song in the browser with the JavaScript module that
`sointu-compile -arch wasm -js` writes: the song renders in workers while the
page does other things, starts when a runway is rendered, and the frame loop
reads the audio clock. Bundled with [vite](https://vite.dev) and packed into
one html file with [websqz/rootsqz](https://github.com/r00tkids/rootsqz)
through [rollup-plugin-websqz](https://github.com/r00tkids/rollup-plugin-websqz).
The design, the numbers and what is verified are in `FORK.md`, "Web runtime".

Requirements: go, `wat2wasm` ([wabt](https://github.com/WebAssembly/wabt)),
node. For songs with samples, ffmpeg.

```
npm install
npm run dev      # compiles the song, serves the page with vite
npm run build    # compiles the song, bundles, packs to dist/websqz-output/index.html
npm run serve    # serves the packed page on http://127.0.0.1:8080
```

The packed page reads itself with `fetch`, so it needs http: it does not run
from `file://`.

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
await song.ready;               // the runway is rendered (2 s by default: load(wasm, 4) for 4 s)
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

- `song.rendered` is the seconds rendered so far, for a loading bar;
  `duration` (exported) is the length of the song.
- `song.context` is the `AudioContext` (44100 Hz). `song.start(node)` plays
  into a node of it instead of the speakers, e.g. an `AnalyserNode` that is
  connected on to `song.context.destination`.
- `song.time()` never goes back. If playing catches up with rendering, the
  sound stops until the next half second of audio is there, and the clock
  stops with it, so the visuals stay in sync.
- The module must be minified without changing what the function `renderer`
  refers to: its source text is the worker. The default minifiers of vite
  (esbuild, oxc) and terser keep it self-contained. Do not lower the build
  target below ES2018, which would add helper functions to it.

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
