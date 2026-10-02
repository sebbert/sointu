Requirements: sointu binaries, `wabt`

To generate the .wasm file:

```
sointu-compile -o . -arch=wasm tests/test_chords.yml
wat2wasm test_chords.wat
```

With older wabt versions, which don't enable annotations by default, add
`--enable-annotations` for songs with samples.

To run the example:

```
npx serve examples/code/wasm
```

Songs that play samples carry them encoded in `sointu.buffer` custom sections;
`index.html` decodes them with `decodeAudioData` before instantiating the
module. Compiling such songs needs [ffmpeg](https://ffmpeg.org/).

Older wabt versions need `--enable-bulk-memory` as well; newer ones enable it by
default and reject the flag.

This example renders the whole song when the module is instantiated, on the
main thread. `examples/code/web` renders in the background and plays while it
renders, with the JavaScript module of `sointu-compile -js`.
