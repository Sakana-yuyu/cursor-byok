import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

function adapter(id, groupName, baseURL) {
  return {
    ...basePreviewConfig().modelAdapters[0],
    id,
    displayName: id,
    modelID: id,
    groupName,
    baseURL,
  };
}

test("名称分组将具名供应商分开显示", async ({ page }) => {
  const config = basePreviewConfig();
  config.modelAdapters = [
    adapter("tiger-model", "虎哥", "https://gw.example.com/v1"),
    adapter("opencode-model", "Opencode", "https://opencode.example.com/v1"),
    adapter("cpa-model", "CPA", "https://cpa.example.com/v1"),
    adapter("ld-model", "LD", "https://ld.example.com/v1"),
  ];
  await seedPreviewTestPlan(page, {}, config);
  await page.goto("/model-config");

  await page.getByRole("button", { name: "名称分组" }).click();
  await expect(page.getByText("虎哥", { exact: true })).toBeVisible();
  await expect(page.getByText("Opencode", { exact: true })).toBeVisible();
  await expect(page.getByText("CPA", { exact: true })).toBeVisible();
  await expect(page.getByText("LD", { exact: true })).toBeVisible();
  await expect(page.getByText("默认分组", { exact: true })).toHaveCount(0);
});
