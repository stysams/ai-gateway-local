import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: { baseURL: "http://127.0.0.1:9245", trace: "retain-on-failure" },
  webServer: { command: "npm run dev", port: 9245, reuseExistingServer: true },
  projects: [
    { name: "desktop-light", use: { viewport: { width: 1440, height: 900 }, colorScheme: "light" } },
    { name: "mobile-dark", use: { ...devices["iPhone 13"], browserName: "chromium", viewport: { width: 390, height: 844 }, colorScheme: "dark" } },
  ],
});
