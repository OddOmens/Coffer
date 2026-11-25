import { defineConfig } from "vite"

// The connect page is bundled into the app (served from tauri://localhost).
export default defineConfig({
  clearScreen: false,
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: ["safari14", "es2021"],
    assetsInlineLimit: 0,
  },
})
