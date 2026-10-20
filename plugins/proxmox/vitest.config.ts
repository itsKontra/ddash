import { defineConfig } from "vitest/config";

// Tests run on the sources, not through the library build.
export default defineConfig({
  test: { environment: "jsdom" },
});
