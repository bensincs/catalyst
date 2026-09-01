import path from "node:path";
import { defineConfig } from "vitest/config";

export default defineConfig({
  // Match the app's "@/" alias so a test can import a real module rather than a
  // copy of its logic.
  resolve: {
    alias: { "@": path.resolve(__dirname, ".") },
  },
  test: {
    globals: true,
    environment: "node",
    include: ["lib/**/*.test.ts", "components/**/*.test.ts", "components/**/*.test.tsx"],
  },
});
