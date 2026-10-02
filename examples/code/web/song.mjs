// Compiles the song to src/song.wasm, src/song.js and src/song.d.ts:
//   node song.mjs [song.yml] [flags of sointu-compile, e.g. -stages 4 -samples]
// By default examples/soundset_loop.yml in a pipeline of 4 workers. Needs go
// and wat2wasm (wabt).
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..", "..");
const [song = join(root, "examples", "soundset_loop.yml"), ...flags] = process.argv.slice(2);
const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: "inherit" });
run("go", ["run", "./cmd/sointu-compile", "-arch", "wasm", "-js", ...(flags.length ? flags : ["-stages", "4"]), "-o", join(here, "src", "song"), resolve(song)], root);
run("wat2wasm", ["-o", join(here, "src", "song.wasm"), join(here, "src", "song.wat")]);
