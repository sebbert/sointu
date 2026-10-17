import { defineConfig } from "vite";
import rootsqz from "@r00tkids/rollup-plugin-rootsqz";

// `vite build` bundles everything into one script and packs it into
// dist/rootsqz-output/index.html with rootsqz, which the plugin installs for
// the platform from npm; $ROOTSQZ_BINARY_PATH names another executable. The
// .wasm (and sample files) get into the packed file through their imports:
// see src/main.js.
export default defineConfig({
  build: {
    modulePreload: false, // no preload helper in the bundle
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
  plugins: [rootsqz()],
});
