import { defineConfig } from "@playwright/test";

// 按钮级回归：仅运行于 browser-preview 模式（内存 mock，不访问真实供应商/桌面服务）。
// 本地与 CI 统一由该 webServer 启动 Vite dev server。
export default defineConfig({
  testDir: "./e2e",
  timeout: 30000,
  fullyParallel: true,
  workers: 4,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],
  use: {
    baseURL: "http://127.0.0.1:4180",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    locale: "zh-CN",
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
  webServer: {
    command: "npm run dev:browser -- --port 4180 --strictPort",
    url: "http://127.0.0.1:4180",
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
  },
});