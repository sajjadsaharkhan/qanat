import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": resolve(__dirname, "src") } },
  test: {
    // Pure logic runs in node; component tests opt in to jsdom with a
    // `// @vitest-environment jsdom` comment at the top of the file.
    environment: "node",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/test/setup.ts"],
    env: { TZ: "UTC" }, // tests pass explicit time zones; never rely on the runner's
  },
});
