import { expect, test } from "@playwright/test";
import {
  basePreviewConfig,
  openSupplierPage,
  supportedBalance,
  testResultSuccess,
} from "./helpers.mjs";

const SUPPLIER_TITLE = "浏览器预览示例";

function cardByModelName(page, name) {
  return page.locator('[data-testid="model-card"]').filter({ hasText: name });
}

test("余额查询成功时页头展示余额明细，刷新按钮再次发起查询", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { balance: supportedBalance() },
  });

  await expect(page.getByRole("heading", { name: SUPPLIER_TITLE })).toBeVisible();
  await expect(page.getByText(/余额 \$76\.50 \/ 总额 \$100\.00 \/ 已用 \$23\.50/)).toBeVisible();
  // 展示性余额 chip 不应泄漏密钥
  await expect(page.getByText(/browser-preview-demo-key/)).toHaveCount(0);

  await page.getByTitle("刷新余额").click();
  await expect(page.getByText(/余额 \$76\.50/)).toBeVisible();
});

test("Token Plan 余额展示多个结构化额度窗口", async ({ page }) => {
  await openSupplierPage(page, {
    plan: {
      balance: supportedBalance({
        source: "token_plan",
        currency: "%",
        total: 100,
        used: 25,
        remaining: 75,
        planName: "Kimi For Coding",
        fetchedAt: "2026-08-20T08:00:00Z",
        windows: [
          { id: "5h", label: "5小时", unit: "%", used: 25, limit: 100, remaining: 75, usedFraction: 0.25, remainingFraction: 0.75, status: "ok" },
          { id: "1h", label: "1小时", unit: "%", used: 40, limit: 100, remaining: 60, usedFraction: 0.4, remainingFraction: 0.6, status: "ok" },
          { id: "7d", label: "周限额", unit: "%", used: 88, limit: 100, remaining: 12, usedFraction: 0.88, remainingFraction: 0.12, status: "warning" },
        ],
      }),
    },
  });

  await expect(page.getByText(/Kimi For Coding · 已用 25% \/ 剩余 75%/)).toBeVisible();
  const windows = page.getByTestId("provider-usage-window");
  await expect(windows).toHaveCount(3);
  await expect(windows.nth(0)).toContainText("5小时");
  await expect(windows.nth(0)).toContainText("剩余 75%");
  await expect(windows.nth(1)).toContainText("1小时");
  await expect(windows.nth(1)).toContainText("剩余 60%");
  await expect(windows.nth(2)).toContainText("周限额");
  await expect(windows.nth(2)).toContainText("剩余 12%");
});

test("结构化额度窗口在英文界面使用本地化标签", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("cursor-client:locale:v1", "en-US");
    localStorage.setItem("cursor-client:locale-source:v1", "manual");
  });
  await openSupplierPage(page, {
    config: basePreviewConfig(),
    plan: {
      balance: supportedBalance({
        source: "token_plan",
        currency: "%",
        total: 100,
        used: 25,
        remaining: 75,
        planName: "Kimi For Coding",
        windows: [
          { id: "5h", label: "5小时", unit: "%", usedFraction: 0.25, remainingFraction: 0.75, resetsAt: "2026-08-20T12:00:00Z", status: "ok" },
          { id: "7d", label: "周限额", unit: "%", usedFraction: 0.88, remainingFraction: 0.12, status: "warning" },
        ],
      }),
    },
  });

  const windows = page.getByTestId("provider-usage-window");
  await expect(windows.nth(0)).toContainText("5 hours");
  await expect(windows.nth(0)).toContainText("Remaining 75%");
  await expect(windows.nth(0)).toContainText("Resets at");
  await expect(windows.nth(1)).toContainText("Weekly limit");
  await expect(windows.nth(1)).toContainText("Remaining 12%");
});

test("余额瞬时失败时保留上次成功值并标记可能过期", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { balance: supportedBalance() },
  });
  await expect(page.getByText(/余额 \$76\.50/)).toBeVisible();

  // 同一上下文内切换测试计划：改写 localStorage 后点刷新，mock 动态读取注入瞬时失败
  await page.evaluate((key) => {
    localStorage.setItem(
      key,
      JSON.stringify({ balance: { supported: false, transient: true, message: "注入失败" } }),
    );
  }, "cursor-byok.browser-preview.test-plan");
  await page.getByTitle("刷新余额").click();

  await expect(page.getByText(/余额 \$76\.50/)).toBeVisible();
  await expect(page.getByText("（可能过期）")).toBeVisible();
});

test("卡片测试按钮驱动测试流程并展示测试结果", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { testResult: testResultSuccess() },
  });

  const openaiCard = cardByModelName(page, "Demo GPT");
  await openaiCard.getByRole("button", { name: "测试", exact: true }).click();

  await expect(openaiCard.getByText("输出 22 t/s · 首响 24.6 s")).toBeVisible();
  await expect(openaiCard.getByText("可用")).toBeVisible();
});

test("搜索框过滤模型列表，清除按钮恢复全部", async ({ page }) => {
  await openSupplierPage(page);

  await expect(page.getByText("显示 2/2")).toBeVisible();
  await page.getByPlaceholder("搜索模型名 / 标识").fill("gemini");
  await expect(page.getByText("显示 1/2")).toBeVisible();
  await expect(page.getByText("Demo GPT", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Demo Gemini", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "清除搜索" }).click();
  await expect(page.getByText("显示 2/2")).toBeVisible();
});

test("状态过滤按钮切换可见模型", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { testResult: testResultSuccess() },
  });
  await expect(page.getByText("显示 2/2")).toBeVisible();

  // 先测试 Demo GPT => ok；Demo Gemini 保持未测
  await cardByModelName(page, "Demo GPT").getByRole("button", { name: "测试", exact: true }).click();
  await expect(cardByModelName(page, "Demo GPT").getByText("可用")).toBeVisible();

  await page.getByRole("button", { name: "可用", exact: true }).click();
  await expect(page.getByText("显示 1/2")).toBeVisible();
  await expect(page.getByText("Demo GPT", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "未测", exact: true }).click();
  await expect(page.getByText("Demo Gemini", { exact: true })).toBeVisible();
  await expect(page.getByText("Demo GPT", { exact: true })).toHaveCount(0);
});

test("删除模型：取消确认不删除，确认后列表移除", async ({ page }) => {
  await openSupplierPage(page);

  const card = cardByModelName(page, "Demo Gemini");
  await card.getByRole("button", { name: "删除", exact: true }).click();
  await expect(page.getByText("删除模型")).toBeVisible();

  await page.getByRole("button", { name: "取消", exact: true }).click();
  await expect(page.getByText("Demo Gemini", { exact: true })).toBeVisible();

  await card.getByRole("button", { name: "删除", exact: true }).click();
  // 确认弹窗内的删除按钮：限定在弹窗内点击
  await page.getByRole("dialog").getByRole("button", { name: "删除", exact: true }).click();
  await expect(page.getByText("Demo Gemini", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/1 个模型/)).toBeVisible();
});

test("一键测试并发驱动全部模型测试", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { testResult: testResultSuccess() },
  });

  await page.getByRole("button", { name: "一键测试 (2)" }).click();
  await expect(page.getByText("输出 22 t/s · 首响 24.6 s")).toHaveCount(2);
  await expect(page.getByRole("button", { name: /一键测试 \(2\)/ })).toBeVisible();
});

test("批量编辑面板：余额测试与保存流程", async ({ page }) => {
  await openSupplierPage(page, {
    plan: { balance: supportedBalance({ currency: "CNY", total: 50, remaining: 30 }) },
  });

  await page.getByRole("button", { name: "编辑供应商" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("heading", { name: "编辑供应商" })).toBeVisible();

  await dialog.getByRole("button", { name: "测试余额", exact: true }).click();
  // 断言限定在对话框内，避免与页头余额 chip 重复匹配
  await expect(dialog.getByText("余额 ¥30.00", { exact: true })).toBeVisible();
  await expect(dialog.getByText(/总额 ¥50\.00/)).toBeVisible();

  // 取消不保存
  await dialog.getByRole("button", { name: "取消", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("Demo GPT", { exact: true })).toBeVisible();
  await expect(page.getByText("Demo Gemini", { exact: true })).toBeVisible();
});
