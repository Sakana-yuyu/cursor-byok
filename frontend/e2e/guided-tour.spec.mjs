import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

// 交互式新手引导：用户在首页点击「使用引导」触发；advanceOn="click" 的步骤由
// 真实点击目标元素驱动（放行元素原有行为），非目标区域点击被拦截。全程浏览器预览 mock。

test("使用引导：交互式全流程（点击驱动跨页推进并完成）", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  await page.getByRole("button", { name: "使用引导", exact: true }).click();
  await expect(page.getByText("欢迎使用 Cursor助手")).toBeVisible();
  await page.getByRole("button", { name: "下一步" }).click();

  // 步骤 2：点击驱动的侧边栏「模型」入口 → 真实导航到模型配置页
  await page.getByText("点击左侧「模型」，进入模型配置页。").waitFor();
  await page.locator("[data-tour-nav='/model-config']").click();
  await expect(page).toHaveURL(/#?\/model-config/);
  await expect(page.getByText("在这里添加模型")).toBeVisible();

  // 步骤 3：按钮驱动，高亮「新增模型」按钮
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("第二步：启动服务")).toBeVisible();

  // 步骤 4：点击「启动服务」→ 服务真实启动（mock）→ 自动进入地址步骤
  await page.getByRole("button", { name: "启动服务" }).click();
  await expect(page.getByText("本地代理地址")).toBeVisible({ timeout: 10000 });

  // 服务运行后首页按钮变为「停止服务」，此时应继续推进而非再次点击
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("点击「设置」，可以调整界面语言、调试日志等偏好。")).toBeVisible();

  // 步骤 6：点击驱动的侧边栏「设置」→ 完成卡片（就地显示）
  await page.locator("[data-tour-nav='/settings']").click();
  await expect(page.getByText("引导完成")).toBeVisible();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await expect(page.getByText("引导完成")).toHaveCount(0);
  const completed = await page.evaluate(() => localStorage.getItem("cursor-byok.guided-tour.completed"));
  expect(completed).toBe("true");
});

test("使用引导：点击非目标区域被拦截且不推进", async ({ page }) => {
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  await page.getByRole("button", { name: "使用引导", exact: true }).click();
  await page.getByText("欢迎使用 Cursor助手").waitFor();
  await page.getByRole("button", { name: "下一步" }).click();
  await expect(page.getByText("点击左侧「模型」，进入模型配置页。")).toBeVisible();

  // 引导中点击页面其他按钮（「启动 Cursor」）应被拦截：引导不动、无副作用
  await page.getByRole("button", { name: "启动 Cursor", exact: true }).click();
  await expect(page.getByText("点击左侧「模型」，进入模型配置页。")).toBeVisible();
  await expect(page).toHaveURL(/#?\/$/);
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
