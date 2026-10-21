// Used by build.sh, copied next to the ui sources of a scratch checkout. Not part of the tolon repo.
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/preview/",
  plugins: [react()],
  build: {
    // No inline module-preload snippet: the poll's CSP has no 'unsafe-inline'.
    modulePreload: { polyfill: false },
    rollupOptions: { input: { app: "app.html", interrupt: "interrupt.html" } },
  },
});
