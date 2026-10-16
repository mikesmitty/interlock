import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go server owns /api; in development Vite proxies to it so the app
// runs the same way it will when embedded.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
