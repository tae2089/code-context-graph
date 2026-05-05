import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/wiki/",
  plugins: [react()],
  server: {
    proxy: {
      "/wiki/api": "http://localhost:8080"
    }
  }
});
