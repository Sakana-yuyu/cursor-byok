import { expect, test } from "@playwright/test";
import { seedPreviewTestPlan } from "./helpers.mjs";

async function readExternalAPI(page) {
  return page.evaluate(async () => {
    const { LoadUserConfig } = await import("/src/services/browserBindings.js");
    return (await LoadUserConfig()).externalApi;
  });
}

test("本机 API 显式保存、启停和更换 Key 使用最新配置", async ({ page }) => {
  await seedPreviewTestPlan(page, {});
  await page.goto("/settings?category=cursor-service");
  const input = page.getByRole("textbox", { name: "本机 External API Key", exact: true });
  const save = page.getByRole("button", { name: "保存 External API", exact: true });
  const toggle = page.getByRole("switch", { name: "启用本机 External API", exact: true });
  await input.fill("external-api-test-key-one");
  await save.click();
  await expect.poll(() => readExternalAPI(page)).toEqual({ enabled: false, apiKey: "external-api-test-key-one" });
  await toggle.click();
  await expect.poll(() => readExternalAPI(page)).toEqual({ enabled: true, apiKey: "external-api-test-key-one" });
  await input.fill("external-api-test-key-two");
  await save.click();
  await expect.poll(() => readExternalAPI(page)).toEqual({ enabled: true, apiKey: "external-api-test-key-two" });
  await toggle.click();
  await expect.poll(() => readExternalAPI(page)).toEqual({ enabled: false, apiKey: "external-api-test-key-two" });
  const cache = await page.evaluate(() => localStorage.getItem("cursor-client:runtime-state:v2") || "");
  expect(cache).not.toContain("external-api-test-key");
});
