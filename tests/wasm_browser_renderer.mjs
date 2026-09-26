// Renders a song compiled with `sointu-compile -arch=wasm` in headless Chrome,
// decoding the samples of its buffers with the browser's decodeAudioData like
// examples/code/wasm/index.html does, and optionally compares the result with
// an expected rendering.
//
// Usage:
//   node tests/wasm_browser_renderer.mjs song.wasm [expected.raw] [--out got.raw] [--tolerance 1e-3]
//
// The output is stereo interleaved float32 (or int16 for songs compiled with
// -i). Prints a JSON summary and exits with 1 if the difference to the
// expected rendering exceeds the tolerance. Chrome is found from $CHROME or
// the usual install locations; it runs with a temporary profile.

import { createServer } from "node:http";
import { readFileSync, writeFileSync, mkdtempSync, rmSync, existsSync } from "node:fs";
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
const outFile = opt("--out");
const tolerance = parseFloat(opt("--tolerance", "1e-3"));
const [wasmFile, expectedFile] = args;
if (!wasmFile) {
  console.error("usage: wasm_browser_renderer.mjs song.wasm [expected.raw] [--out got.raw] [--tolerance 1e-3]");
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
if (!chrome) {
  console.error("Chrome not found; set CHROME to its path");
  process.exit(2);
}

// The page decodes and renders exactly like the example loader.
const page = `<!DOCTYPE html><script type="module">
async function render(bytes) {
  const module = await WebAssembly.compile(bytes);
  const decoder = new OfflineAudioContext(1, 1, 44100);
  const buffers = await Promise.all(
    WebAssembly.Module.customSections(module, "sointu.buffer").map((s) => decoder.decodeAudioData(s.slice(0)))
  );
  const { exports } = await WebAssembly.instantiate(module, {
    m: Math,
    s: { b: (i, f, c) => buffers[i].getChannelData(Math.min(c, buffers[i].numberOfChannels - 1))[f] ?? 0 },
  });
  return { exports, buffers };
}
try {
  const { exports, buffers } = await render(await (await fetch("/song.wasm")).arrayBuffer());
  const out = new Uint8Array(exports.m.buffer, exports.s.value, exports.l.value).slice();
  await fetch("/output?int16=" + exports.t.value + "&buffers=" + encodeURIComponent(JSON.stringify(
    buffers.map((b) => ({ frames: b.length, channels: b.numberOfChannels, sampleRate: b.sampleRate }))
  )), { method: "POST", body: out });
} catch (e) {
  await fetch("/error", { method: "POST", body: String(e && e.stack || e) });
}
</script>`;

const wasm = readFileSync(wasmFile);
const profile = mkdtempSync(join(tmpdir(), "sointu-chrome-"));
let browser;
const finish = (code, summary) => {
  console.log(JSON.stringify(summary, null, 2));
  server.close();
  browser?.kill();
  setTimeout(() => {
    rmSync(profile, { recursive: true, force: true });
    process.exit(code);
  }, 500);
};

const server = createServer((req, res) => {
  const url = new URL(req.url, "http://localhost");
  if (req.method === "GET" && url.pathname === "/") {
    res.writeHead(200, { "Content-Type": "text/html" }).end(page);
  } else if (req.method === "GET" && url.pathname === "/song.wasm") {
    res.writeHead(200, { "Content-Type": "application/wasm" }).end(wasm);
  } else if (req.method === "POST") {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      res.end();
      const body = Buffer.concat(chunks);
      if (url.pathname === "/error") return finish(1, { ok: false, error: body.toString() });
      const int16 = url.searchParams.get("int16") === "1";
      if (outFile) writeFileSync(outFile, body);
      const view = (b) => (int16 ? new Int16Array(b.buffer, b.byteOffset, b.byteLength / 2) : new Float32Array(b.buffer, b.byteOffset, b.byteLength / 4));
      const got = view(body);
      const summary = { ok: true, frames: got.length / 2, buffers: JSON.parse(url.searchParams.get("buffers")) };
      if (expectedFile) {
        const want = view(readFileSync(expectedFile));
        let maxDiff = 0, at = -1, peak = 0;
        for (let i = 0; i < Math.min(got.length, want.length); i++) {
          const d = Math.abs(got[i] - want[i]);
          if (d > maxDiff) (maxDiff = d), (at = i);
          peak = Math.max(peak, Math.abs(want[i]));
        }
        Object.assign(summary, { expectedFrames: want.length / 2, peak, maxDiff, maxDiffAtFrame: Math.floor(at / 2), maxDiffChannel: at % 2 });
        summary.ok = got.length === want.length && maxDiff <= tolerance;
      }
      finish(summary.ok ? 0 : 1, summary);
    });
  }
});

server.listen(0, "127.0.0.1", () => {
  const url = `http://127.0.0.1:${server.address().port}/`;
  browser = spawn(chrome, ["--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", `--user-data-dir=${profile}`, url], { stdio: "ignore" });
  setTimeout(() => finish(1, { ok: false, error: "timed out" }), 60000).unref();
});
