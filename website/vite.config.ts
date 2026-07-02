import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite builds only the React SPA under src/ into dist/.
// EdgeOne Pages serves dist/ statically (STATIC-ONLY: no Pages Functions, no KV).
// All backend/auth lives on the Go broker at chat.example.com (cross-origin).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});
