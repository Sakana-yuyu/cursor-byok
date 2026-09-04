import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

// 新手使用引导（guided tour）：入口由用户在首页点击触发，分步高亮跨页推进。
// 全程走浏览器预览 mock，不触真实供应商请求。

test("使用引导：入口点击后跨页推进并可完成", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  await page.getByRole("button", { name: "使用引导", exact: true }).click();
  await expect(page.getByText("欢迎使用 Cursor助手")).toBeVisible();

  // 步骤 2：高亮侧边栏「模型」入口
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("第一步：配置模型")).toBeVisible();

  // 步骤 3：自动跳转模型配置页
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page).toHaveURL(/#?\/model-config/);
  await expect(page.getByText("管理供应商与模型")).toBeVisible();

  // 步骤 4-6：回到首页（启动服务 → 代理地址 → 设置入口）
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("第二步：启动服务")).toBeVisible();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("更多设置")).toBeVisible();

  // 步骤 7：完成并持久化标记
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("引导完成")).toBeVisible();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await expect(page.getByText("引导完成")).toHaveCount(0);
  const completed = await page.evaluate(() => localStorage.getItem("cursor-byok.guided-tour.completed"));
  expect(completed).toBe("true");
});

test("使用引导：跳过不写完成标记", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  await page.getByRole("button", { name: "使用引导", exact: true }).click();
  await expect(page.getByText("欢迎使用 Cursor助手")).toBeVisible();
  await page.getByRole("button", { name: "跳过引导" }).click();
  await expect(page.getByText("欢迎使用 Cursor助手")).toHaveCount(0);
  const completed = await page.evaluate(() => localStorage.getItem("cursor-byok.guided-tour.completed"));
  expect(completed).toBeNull();
});

test("使用引导：Esc 键跳过", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  await page.getByRole("button", { name: "使用引导", exact: true }).click();
  await expect(page.getByText("欢迎使用 Cursor助手")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByText("欢迎使用 Cursor助手")).toHaveCount(0);
});
