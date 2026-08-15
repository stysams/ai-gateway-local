import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  server: { host: "127.0.0.1", port: 9245, strictPort: true },
  plugins: [react()],
  build: { outDir: "../cmd/desktop/assets", emptyOutDir: true },
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    exclude: ["e2e/**", "node_modules/**", "dist/**"],
    css: true,
  },
});
