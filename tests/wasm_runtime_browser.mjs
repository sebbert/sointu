// Plays a song with the JavaScript module of `sointu-compile -arch wasm -js`
// in headless Chrome, records what the audio context plays with an
// AudioWorklet, and compares it with the player that renders at instantiation
// (the same song compiled without -js), sample by sample.
//
// Usage:
//   node tests/wasm_runtime_browser.mjs dir [--scenario play] [--runway 0.5] [--margin 0.8] [--out result.json] [--firefox]
//
// dir has song.js and song.wasm (compiled with -js), oneshot.wasm (compiled
// without) and, for songs compiled with -samples, the sample files song.0.*,
// song.1.* and so on. Scenarios:
//   play      render in workers and play
//   noworker  no Worker in the page: the module renders on the main thread
//   stall     the fourth piece of audio arrives 3 s late: the song has to
//             wait for it, with a gap in the sound and a clock that stops
//   measure   no recording: only the times from load to ready and to the end
//             of rendering
// With --margin 0 the song must not be ready before all of it is rendered.
// Prints a JSON summary and exits with 1 if the played audio differs. The
// song plays in real time. Chrome is found from $CHROME or the usual install
// locations; it runs muted, with a temporary profile. With --firefox, Firefox
// runs instead, from $FIREFOX or its usual install locations.

import { createServer } from "node:http";
import { readFileSync, writeFileSync, mkdtempSync, rmSync, existsSync, readdirSync } from "node:fs";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";

const args = process.argv.slice(2);
const opt = (name, def) => {
  const i = args.indexOf(name);
  if (i < 0) return def;
  const v = args[i + 1];
  args.splice(i, 2);
  return v;
};
const scenario = opt("--scenario", "play");
const runway = opt("--runway", "0.5");
const margin = opt("--margin", "0.8");
const outFile = opt("--out");
const timeout = +opt("--timeout", "120") * 1000;
const firefox = args.includes("--firefox");
if (firefox) args.splice(args.indexOf("--firefox"), 1);
const [dir] = args;
if (!dir) {
  console.error("usage: wasm_runtime_browser.mjs dir [--scenario play|noworker|stall|measure] [--runway seconds] [--out result.json]");
  process.exit(2);
}

const chrome = [
  process.env.CHROME,
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
  "/usr/bin/google-chrome",
  "/usr/bin/chromium",
  "/usr/bin/chromium-browser",
  "C:/Program Files/Google/Chrome/Application/chrome.exe",
].find((p) => p && existsSync(p));
const firefoxPath = [
  process.env.FIREFOX,
  "/Applications/Firefox.app/Contents/MacOS/firefox",
  "/usr/bin/firefox",
  "C:/Program Files/Mozilla Firefox/firefox.exe",
].find((p) => p && existsSync(p));
if (firefox ? !firefoxPath : !chrome) {
  console.error(firefox ? "Firefox not found; set FIREFOX to its path" : "Chrome not found; set CHROME to its path");
  process.exit(2);
}

const sampleFiles = readdirSync(dir)
  .filter((f) => /^song\.\d+\./.test(f))
  .sort((a, b) => parseInt(a.split(".")[1]) - parseInt(b.split(".")[1]));

const page = `<!DOCTYPE html><script type="module">
const scenario = ${JSON.stringify(scenario)}, runway = ${runway}, margin = ${margin}, sampleFiles = ${JSON.stringify(sampleFiles)};
const bytes = async (name) => new Uint8Array(await (await fetch("/" + name)).arrayBuffer());
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
try {
  // the expected audio: the player that renders at instantiation, as float
  const expected = async () => {
    const module = await WebAssembly.compile(await bytes("oneshot.wasm"));
    const decoder = new OfflineAudioContext(1, 1, 44100);
    const sections = sampleFiles.length ? await Promise.all(sampleFiles.map(bytes)) : WebAssembly.Module.customSections(module, "sointu.buffer");
    const buffers = await Promise.all(sections.map((s) => decoder.decodeAudioData(new Uint8Array(s).slice().buffer)));
    const t = performance.now();
    const { exports } = await WebAssembly.instantiate(module, {
      m: Math,
      s: { b: (i, f, c) => buffers[i].getChannelData(Math.min(c, buffers[i].numberOfChannels - 1))[f] ?? 0 },
    });
    const ms = performance.now() - t;
    const a = exports.t.value
      ? Float32Array.from(new Int16Array(exports.m.buffer, exports.s.value, exports.l.value / 2), (v) => v / 32767)
      : new Float32Array(exports.m.buffer, exports.s.value, exports.l.value / 4);
    // the sync values, in songs that have them
    const sync = exports.y && new Float32Array(exports.m.buffer, exports.y.value, exports.z.value / 4);
    return { audio: a, ms, sync };
  };
  const want = scenario == "measure" ? null : await expected();

  if (scenario == "noworker") delete window.Worker;
  if (scenario == "stall") {
    // the fourth message of a worker arrives 3 s late, and those after it in order
    const Real = window.Worker;
    window.Worker = class extends Real {
      set onmessage(f) {
        let n = 0, held;
        super.onmessage = (e) => {
          if (++n == 4) {
            held = [];
            setTimeout(() => { held.forEach(f); held = 0; }, 3000);
          }
          held ? held.push(e) : f(e);
        };
      }
    };
  }

  const lib = await import("/song.js");
  const wasm = await bytes("song.wasm");
  const samples = await Promise.all(sampleFiles.map(bytes));
  const t0 = performance.now();
  const song = sampleFiles.length ? lib.load(wasm, samples, runway, margin) : lib.load(wasm, runway, margin);
  const summary = { ok: true, scenario, duration: lib.duration, rowsPerSecond: lib.rowsPerSecond, wasmBytes: wasm.length };
  // progress, by polling
  let longest = 0, last = performance.now();
  const poll = setInterval(() => {
    const now = performance.now();
    longest = Math.max(longest, now - last); // how long the main thread was held up
    last = now;
    if (song.rendered > 0 && !summary.firstChunkMs) summary.firstChunkMs = now - t0, summary.firstChunkSeconds = song.rendered;
    if (song.rendered >= lib.duration && !summary.renderedMs) summary.renderedMs = now - t0;
  }, 4);
  await song.ready;
  summary.readyMs = performance.now() - t0;
  summary.readyRendered = song.rendered;

  if (scenario == "measure") {
    while (!summary.renderedMs) await sleep(20);
    summary.longestMainThreadGapMs = longest;
    summary.realtimeFactor = lib.duration / (summary.renderedMs / 1000);
  } else {
    summary.oneshotMs = want.ms;
    // record what the context plays
    const ctx = song.context;
    await ctx.audioWorklet.addModule(URL.createObjectURL(new Blob([\`registerProcessor("rec", class extends AudioWorkletProcessor {
      process(inputs) {
        const [l, r] = inputs[0];
        this.port.postMessage([l ? l.slice() : new Float32Array(128), r ? r.slice() : l ? l.slice() : new Float32Array(128)]);
        return true;
      }
    })\`], { type: "text/javascript" })));
    const rec = new AudioWorkletNode(ctx, "rec", { numberOfInputs: 1, numberOfOutputs: 1, channelCount: 2, channelCountMode: "explicit" });
    const blocks = [];
    rec.port.onmessage = (e) => blocks.push(e.data);
    rec.connect(ctx.destination);
    if (song.time() !== 0) throw new Error("time() is " + song.time() + " before the start");
    song.start(rec);
    summary.startMs = performance.now() - t0;
    // the clock: never back, and it reaches the end of the song
    let prev = 0, back = 0, stalledMs = 0, stallFrom = 0;
    const clock = [];
    const began = performance.now();
    while (song.time() < lib.duration && performance.now() - began < (lib.duration + 8) * 1000) {
      const t = song.time(), now = performance.now();
      if (t < prev) back++;
      if (t == prev && t > 0) stallFrom = stallFrom || now;
      else if (stallFrom) (stalledMs = Math.max(stalledMs, now - stallFrom)), (stallFrom = 0);
      prev = t;
      clock.push([now - began, t]);
      await sleep(5);
    }
    summary.playedMs = performance.now() - began;
    summary.clockEnd = song.time();
    summary.clockWentBack = back;
    summary.longestClockStopMs = stalledMs;
    // the drift of the clock against the wall clock, after the start
    const steady = clock.filter(([, t]) => t > 0.2);
    if (steady.length > 2) summary.clockRate = (steady.at(-1)[1] - steady[0][1]) / ((steady.at(-1)[0] - steady[0][0]) / 1000);
    await sleep(400);
    const frames = blocks.length * 128;
    const got = new Float32Array(frames * 2);
    blocks.forEach(([l, r], b) => { for (let i = 0; i < 128; i++) (got[(b * 128 + i) * 2] = l[i]), (got[(b * 128 + i) * 2 + 1] = r[i]); });
    const exp = want.audio, expFrames = exp.length / 2;
    const firstSound = (a) => { for (let i = 0; i < a.length; i++) if (a[i] !== 0) return i >> 1; return -1; };
    let offset = firstSound(got) - firstSound(exp);
    summary.recordedFrames = frames;
    summary.expectedFrames = expFrames;
    summary.firstSoundAtFrame = firstSound(got);
    summary.firstSoundMs = summary.startMs + (firstSound(got) / 44100) * 1000; // from load, as the recording starts with the song
    // compare; where the recording differs, look for a gap of silence
    const gaps = [];
    let differing = 0, maxDiff = 0, searches = 0;
    const same = (i, o) => got[(i + o) * 2] === exp[i * 2] && got[(i + o) * 2 + 1] === exp[i * 2 + 1];
    for (let i = 0; i < expFrames; i++) {
      if (same(i, offset)) continue;
      let found = 0;
      if (gaps.length < 8 && searches++ < 16) {
        for (let g = 1; g < 3 * 44100 && !found; g++) {
          let k = 0;
          while (k < 2000 && i + k < expFrames && same(i + k, offset + g)) k++;
          if (k == 2000 || i + k == expFrames) found = g;
        }
      }
      if (found) {
        let silent = true;
        for (let j = 0; j < found; j++) silent = silent && got[(i + offset + j) * 2] === 0 && got[(i + offset + j) * 2 + 1] === 0;
        gaps.push({ atFrame: i, frames: found, silent });
        offset += found;
      } else {
        if (differing < 20) (summary.differences ||= []).push([i, exp[i * 2], exp[i * 2 + 1], got[(i + offset) * 2], got[(i + offset) * 2 + 1]]);
        differing++;
        maxDiff = Math.max(maxDiff, Math.abs((got[(i + offset) * 2] ?? 9) - exp[i * 2]), Math.abs((got[(i + offset) * 2 + 1] ?? 9) - exp[i * 2 + 1]));
      }
    }
    Object.assign(summary, { differingFrames: differing, maxDiff, gaps });
    // the sync values of every 256th sample, read through the module
    let syncDiffers = 0, syncPeak = 0;
    if (want.sync) {
      const n = lib.syncChannels, ticks = want.sync.length / n;
      for (let k = 0; k < ticks; k++)
        for (let c = 0; c < n; c++) {
          if (song.sync(c, (k * 256 + 0.5) / 44100) !== want.sync[k * n + c]) syncDiffers++;
          syncPeak = Math.max(syncPeak, Math.abs(want.sync[k * n + c]));
        }
      // by default, the values of now: the song is over
      if (song.sync(n - 1) !== want.sync[(ticks - 1) * n + n - 1] || song.sync(0, 0) !== want.sync[0]) syncDiffers++;
      Object.assign(summary, { syncChannels: n, syncTicks: ticks, syncDiffers, syncPeak });
    }
    let peak = 0;
    for (const v of exp) peak = Math.max(peak, Math.abs(v));
    summary.peak = peak;
    // why the run failed. A gap without the stall scenario means that playing
    // caught up with rendering, which a busy machine can cause.
    summary.failures = [
      differing && "the played audio differs",
      peak <= 0.01 && "the song is silent",
      margin == 0 && summary.readyRendered < lib.duration && "the song was ready before all of it was rendered, with a margin of 0",
      margin > 0 && runway < lib.duration && summary.readyRendered >= lib.duration && "the song was only ready when all of it was rendered",
      syncDiffers && "the sync values differ",
      want.sync && !syncPeak && "the sync values are all zero",
      !want.sync != !lib.syncChannels && "the module and the one-shot player differ in having sync values",
      back && "the clock went back",
      Math.abs(summary.clockEnd - lib.duration) > 1e-6 && "the clock did not reach the end of the song",
      !gaps.every((g) => g.silent) && "a gap is not silent",
      scenario == "stall" ? (!gaps.length || stalledMs < 300) && "the song did not wait for the late audio" : gaps.length && "playing caught up with rendering",
    ].filter((f) => f);
    summary.ok = !summary.failures.length;
  }
  clearInterval(poll);
  await fetch("/result", { method: "POST", body: JSON.stringify(summary) });
} catch (e) {
  await fetch("/error", { method: "POST", body: String((e && e.stack) || e) });
}
</script>`;

const profile = mkdtempSync(join(tmpdir(), "sointu-chrome-"));
let browser;
const finish = (code, summary) => {
  const text = JSON.stringify(summary, null, 2);
  console.log(text);
  if (outFile) writeFileSync(outFile, text);
  server.close();
  browser?.kill();
  setTimeout(() => {
    rmSync(profile, { recursive: true, force: true });
    process.exit(code);
  }, 500);
};

const types = { js: "text/javascript", wasm: "application/wasm" };
const server = createServer((req, res) => {
  const url = new URL(req.url, "http://localhost");
  const name = url.pathname.slice(1);
  if (req.method === "GET" && url.pathname === "/") {
    res.writeHead(200, { "Content-Type": "text/html" }).end(page);
  } else if (req.method === "GET" && /^[\w.]+$/.test(name) && existsSync(join(dir, name))) {
    res.writeHead(200, { "Content-Type": types[name.split(".").pop()] ?? "application/octet-stream" }).end(readFileSync(join(dir, name)));
  } else if (req.method === "POST") {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      res.end();
      const body = Buffer.concat(chunks).toString();
      if (url.pathname === "/error") return finish(1, { ok: false, scenario, error: body });
      const summary = JSON.parse(body);
      finish(summary.ok ? 0 : 1, summary);
    });
  } else {
    res.writeHead(404).end();
  }
});

server.listen(0, "127.0.0.1", () => {
  const url = `http://127.0.0.1:${server.address().port}/`;
  if (firefox) {
    // audio may start without a user gesture, and nothing reaches the speakers
    writeFileSync(join(profile, "user.js"), ["media.autoplay.default", "media.autoplay.blocking_policy"].map((p) => `user_pref("${p}", 0);`).join("\n") +
      `\nuser_pref("media.volume_scale", "0.0");\nuser_pref("browser.shell.checkDefaultBrowser", false);\nuser_pref("datareporting.policy.dataSubmissionEnabled", false);\n`);
    browser = spawn(firefoxPath, ["--headless", "--no-remote", "--profile", profile, url], { stdio: "ignore" });
  } else browser = spawn(chrome, ["--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--mute-audio", "--autoplay-policy=no-user-gesture-required", `--user-data-dir=${profile}`, url], { stdio: "ignore" });
  setTimeout(() => finish(1, { ok: false, scenario, error: "timed out" }), timeout).unref();
});
