import { defineConfig } from "vite";
import websqz from "rollup-plugin-websqz";

// `vite build` bundles everything into one script and packs it into
// dist/websqz-output/index.html with rootsqz (websqz) built from GitHub:
//   cargo install --git https://github.com/r00tkids/rootsqz
// which puts `websqz` on the PATH; $WEBSQZ names another binary. The .wasm
// (and sample files) get into the packed file through their imports: see
// src/main.js.
export default defineConfig({
  build: {
    modulePreload: false, // no preload helper in the bundle
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
        // The plugin reads the packed files from `wsqz.files`; the packer
        // from GitHub names it `rsqz`, the released 0.4 `wsqz`.
        banner: "wsqz=self.wsqz||rsqz;",
      },
    },
  },
  plugins: [websqz({ websqzPath: process.env.WEBSQZ ?? "websqz" })],
});
