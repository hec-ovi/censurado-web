import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    globals: true,
    include: ["web/**/*.test.js"],
    setupFiles: ["./web/setup.js"],
    // A fixed origin so root-relative shard/manifest URLs resolve the same way
    // every run (the refiner fetches against document.baseURI).
    environmentOptions: {
      jsdom: { url: "http://localhost/" },
    },
  },
});
