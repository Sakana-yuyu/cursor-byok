import { defineConfig } from "@playwright/test";

const configuredPort = Number.parseInt(process.env.PLAYWRIGHT_PORT || "4180", 10);
const port = Number.isInteger(configuredPort) && configuredPort > 0 && configuredPort <= 65535 ? configuredPort : 4180;
const baseURL = `http://127.0.0.1:${port}`;

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
    baseURL,
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
    command: `npm run dev:browser -- --port ${port} --strictPort`,
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 60000,
  },
});
