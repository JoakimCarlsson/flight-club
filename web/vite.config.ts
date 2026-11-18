import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const API = "http://localhost:7080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "src") },
  },
  server: {
    port: 7031,
    proxy: {
      "/api": API,
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
