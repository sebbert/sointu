// The reference for an intro: the song starts rendering when the page loads,
// plays from a click once it has its runway, and the frame loop reads the
// audio clock.
//
// With the websqz plugin, ?websqz-bin gives the bytes of a file as a
// Uint8Array, from the packed page. Without it: `import url from
// "./song.wasm?url"` and `load(await (await fetch(url)).arrayBuffer())`.
import wasm from "./song.wasm?websqz-bin";
import { load, duration, rowsPerSecond, rowsPerPattern } from "./song.js";

const t0 = performance.now();
const song = load(wasm); // rendering starts here, in workers
let readyAfter, started;
song.ready.then(() => (readyAfter = performance.now() - t0));

document.body.style.cssText = "margin:0;background:#000;color:#fff;font:16px monospace;cursor:pointer";
const text = document.body.appendChild(document.createElement("pre"));
text.style.cssText = "margin:2em";

// start on the first click after the song is ready (a click before that waits for it)
onclick = () => song.ready.then(() => (started ||= (song.start(), 1)));

const frame = () => {
  const t = song.time(); // seconds of the song at the speakers
  const row = t * rowsPerSecond;
  const beat = row / 4; // 4 rows per beat in this song
  // everything the intro shows is a function of t: here, a flash on every beat
  const flash = started ? Math.max(0, 1 - 4 * (beat % 1)) : 0;
  document.body.style.background = `rgb(${flash * 90},${flash * 40},${flash * 120})`;
  text.textContent = [
    `rendered  ${song.rendered.toFixed(1).padStart(6)} / ${duration.toFixed(1)} s`,
    `ready     ${readyAfter ? `after ${readyAfter.toFixed(0)} ms` : "not yet"}`,
    `time      ${t.toFixed(3).padStart(8)} s`,
    `pattern   ${String(Math.floor(row / rowsPerPattern)).padStart(4)}   row ${String(Math.floor(row % rowsPerPattern)).padStart(3)}`,
    ``,
    started ? (t < duration ? "playing" : "the end") : readyAfter ? "click to start" : "rendering the runway",
  ].join("\n");
  requestAnimationFrame(frame);
};
frame();
