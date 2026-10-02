import { defineConfig } from "vite";
import websqz from "rollup-plugin-websqz";

// `vite build` bundles everything into one script and packs it with websqz
// into dist/websqz-output/index.html. The .wasm (and sample files) get into
// the packed file through their imports: see src/main.js.
export default defineConfig({
  build: {
    modulePreload: false, // no preload helper in the bundle
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
  plugins: [websqz()],
});
