import { defineConfig } from "@playwright/test";
export default defineConfig({ testDir: "./e2e", use: { browserName: "chromium", headless: true }, reporter: "list" });
