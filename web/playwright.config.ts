import { defineConfig, devices } from "@playwright/test";

// The e2e suite runs the real dviz binary against the local Docker daemon plus a
// docker:dind host; see e2e/global-setup.ts.
export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1400, height: 900 },
    trace: "retain-on-failure",
    launchOptions: {
      // Software WebGL so the 3D canvas renders in headless CI.
      args: ["--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
    },
  },
});
