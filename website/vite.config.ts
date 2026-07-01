import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite builds only the React SPA under src/ into dist/.
// Cloudflare Pages deploys dist/ statically and functions/ as Workers
// (file-based routing) — functions/ is NOT bundled by Vite.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
