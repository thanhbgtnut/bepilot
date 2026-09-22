import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `vite dev` always serves index.html (the local preview host) regardless of
// build.lib below — that config only takes effect for `vite build`, which
// produces the single-file embeddable widget script.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    lib: {
      entry: fileURLToPath(new URL("src/embed/embed.ts", import.meta.url)),
      name: "AppraisalAgentWidget",
      formats: ["iife"],
      fileName: () => "appraisal-agent-widget.js",
    },
    cssCodeSplit: false,
  },
});
