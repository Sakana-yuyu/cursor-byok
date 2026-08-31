import { test, expect } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

test("助手不再提供自研 Goal 页面和设置", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");
  await expect(page.getByRole("button", { name: "目标执行", exact: true })).toHaveCount(0);

  await page.goto("/goal");
  await expect(page.getByRole("heading", { name: "目标执行", exact: true })).toHaveCount(0);

  await page.goto("/settings?category=advanced");
  await expect(page.getByText("启用 Goal 命令", { exact: true })).toHaveCount(0);
  await expect(page.getByText("开启后 /goal 与 /goal --strict 由系统识别并进入 Goal 执行；关闭时当作普通对话。", { exact: true })).toHaveCount(0);
});
