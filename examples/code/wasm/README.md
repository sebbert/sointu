Requirements: sointu binaries, `wabt`

To generate the .wasm file:

```
sointu-compile -o . -arch=wasm tests/test_chords.yml
wat2wasm --enable-annotations test_chords.wat
```

To run the example:

```
npx serve examples/code/wasm
```

Songs that play samples carry them encoded in `sointu.buffer` custom sections;
`index.html` decodes them with `decodeAudioData` before instantiating the
module. Compiling such songs needs [ffmpeg](https://ffmpeg.org/).

Older wabt versions need `--enable-bulk-memory` as well; newer ones enable it by
default and reject the flag.
