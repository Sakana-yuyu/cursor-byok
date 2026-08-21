import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

test("首页站点余额展示结构化配额窗口和用量进度", async ({ page }) => {
  await seedPreviewTestPlan(page, {
    allBalances: [
      {
        adapterId: "grouped-balance-without-windows",
        displayName: "Demo GPT",
        groupName: "共享配额站",
        baseURL: "https://api.openai.com/v1",
        modelID: "gpt-4.1-mini",
        balance: { supported: true, source: "newapi", currency: "CNY", total: 100, used: 23.5, remaining: 76.5, message: "" },
      },
      {
        adapterId: "grouped-balance-with-windows",
        displayName: "Demo Gemini",
        groupName: "共享配额站",
        baseURL: "https://generativelanguage.googleapis.com/v1beta",
        modelID: "gemini-2.5-pro",
        balance: {
          supported: true,
          source: "token_plan",
          currency: "%",
          total: 100,
          used: 32,
          remaining: 68,
          planName: "Gemini 使用套餐",
          windows: [
            { id: "5h", label: "5小时", unit: "%", usedFraction: 0.32, remainingFraction: 0.68, resetsAt: "2026-08-20T12:00:00Z", status: "ok" },
            { id: "1h", label: "1小时", unit: "%", usedFraction: 0.2, remainingFraction: 0.8, resetsAt: "2026-08-20T10:00:00Z", status: "ok" },
            { id: "7d", label: "周限额", unit: "%", usedFraction: 0.84, remainingFraction: 0.16, resetsAt: "2026-08-24T00:00:00Z", status: "warning" },
          ],
          message: "",
        },
      },
    ],
  }, basePreviewConfig());
  await page.addInitScript(() => {
    localStorage.setItem("cursor-byok.showStationBalance", "1");
    localStorage.setItem("cursor-byok.supplierGroupMode", "name");
  });
  await page.goto("/");

  await expect(page.getByText(/2 个模型/)).toBeVisible();
  const windows = page.getByTestId("provider-usage-windows");
  await expect(windows).toHaveCount(1);
  await expect(windows).toContainText("5小时");
  await expect(windows).toContainText("剩余 68%");
  await expect(windows).toContainText("周限额");
  await expect(windows).toContainText("剩余 16%");
  await expect(windows).toContainText("另有 1 个额度窗口");
  await expect(windows.getByText(/重置/)).toHaveCount(2);
  await expect(windows.getByText(/重置/).first()).toBeVisible();

  await expect(page.getByRole("progressbar", { name: "5小时已用比例" })).toHaveAttribute("aria-valuenow", "32");
  await expect(page.getByRole("progressbar", { name: "周限额已用比例" })).toHaveAttribute("aria-valuenow", "84");
});
