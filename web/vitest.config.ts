import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  root,
  test: {
    exclude: ["node_modules", "dist", "e2e/**"],
    environment: "jsdom",
    environmentOptions: {
      jsdom: { url: "http://localhost:5173/" },
    },
    setupFiles: [fileURLToPath(new URL("./src/test/setup.ts", import.meta.url))],
    clearMocks: true,
    restoreMocks: true,
  },
});
