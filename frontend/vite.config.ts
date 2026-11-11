import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The app is built to static files and embedded in the Go binary,
// served under /app (see internal/web/appdist). In dev, /api is
// proxied to the Go server on :8080.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/app/",
  build: {
    outDir: "../internal/web/appdist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: "./src/test/setup.ts",
  },
});
