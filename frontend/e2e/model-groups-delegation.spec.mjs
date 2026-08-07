import { expect, test } from "@playwright/test";
import { basePreviewConfig, openDelegationSettingsPage, readStoredPreviewConfig } from "./helpers.mjs";

function groupCard(page, name) {
  return page.locator("article").filter({ hasText: name });
}

async function openGroupsSection(page) {
  await page.getByRole("button", { name: /^模型组/ }).click();
  await expect(page.getByRole("button", { name: "新增模型组" })).toBeVisible();
}

test("模型组：创建、编辑名称、选择委派模型并设置默认模型", async ({ page }) => {
  await openDelegationSettingsPage(page, { config: basePreviewConfig() });
  await openGroupsSection(page);

  await page.getByRole("button", { name: "新增模型组" }).click();
  await expect(groupCard(page, "委派模型组 1")).toBeVisible();
  const card = page.locator("article").filter({ has: page.getByRole("textbox", { name: "模型组 1 名称" }) });

  const nameInput = card.getByRole("textbox", { name: "模型组 1 名称" });
  await nameInput.fill("E2E 代码模型组");
  await nameInput.blur();

  await card.getByRole("button", { name: "可用模型" }).click();
  await page.getByRole("option", { name: "Demo GPT" }).click();
  await page.keyboard.press("Escape");

  await card.getByRole("button", { name: "默认委派模型" }).click();
  await page.getByRole("listbox").first().getByRole("option", { name: "Demo GPT" }).click();

  await expect(card).toContainText("E2E 代码模型组");
  await expect(card).toContainText("Demo GPT");
  await expect.poll(async () => {
    const group = (await readStoredPreviewConfig(page))?.delegation?.groups?.[0];
    return { name: group?.name, modelIDs: group?.modelIDs, defaultModelID: group?.defaultModelID };
  }).toEqual({ name: "E2E 代码模型组", modelIDs: ["preview-demo-openai"], defaultModelID: "preview-demo-openai" });
});

test("模型组：删除确认取消不改变列表，确认后删除并持久化", async ({ page }) => {
  const base = basePreviewConfig();
  const config = basePreviewConfig({
    delegation: {
      ...base.delegation,
      groups: [{ id: "preview-group-1", name: "待删除模型组", enabled: true, modelIDs: [], defaultModelID: "", executionMode: "auto", toolPermissions: {} }],
    },
  });
  await openDelegationSettingsPage(page, { config });
  await openGroupsSection(page);

  const card = groupCard(page, "待删除模型组");
  await expect(card).toBeVisible();
  await card.getByRole("button", { name: "删除 待删除模型组" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "取消" }).click();
  await expect(card).toBeVisible();

  await card.getByRole("button", { name: "删除 待删除模型组" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "删除" }).click();
  await expect(card).toHaveCount(0);
  await expect.poll(async () => (await readStoredPreviewConfig(page))?.delegation?.groups?.length).toBe(0);
});

test("模型组：保存失败时保留草稿并展示重试入口，重试后恢复", async ({ page }) => {
  await openDelegationSettingsPage(page, { plan: { delegationSaveFailure: true }, config: basePreviewConfig() });
  await openGroupsSection(page);

  await page.getByRole("button", { name: "新增模型组" }).click();
  await expect(page.getByText("E2E 注入：委派配置保存失败")).toBeVisible();
  await expect(page.getByText("委派模型组 1", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible();

  await page.evaluate((key) => localStorage.setItem(key, JSON.stringify({})), "cursor-byok.browser-preview.test-plan");
  await page.getByRole("button", { name: "重试" }).click();
  await expect.poll(async () => (await readStoredPreviewConfig(page))?.delegation?.groups?.length).toBe(1);
});

test("监督委派：已配置模型组可在监督策略中选择执行模型组", async ({ page }) => {
  const base = basePreviewConfig();
  const config = basePreviewConfig({
    delegation: {
      ...base.delegation,
      groups: [{ id: "preview-group-1", name: "监督工作组", enabled: true, modelIDs: ["preview-demo-openai"], defaultModelID: "preview-demo-openai", executionMode: "local", toolPermissions: {} }],
    },
  });
  await openDelegationSettingsPage(page, { config });

  await page.getByRole("button", { name: "高级委派" }).click();
  await page.getByRole("switch", { name: "启用监督委派" }).click();
  const workerSelect = page.getByRole("button", { name: "监督执行模型组" });
  await workerSelect.click();
  await page.getByRole("option", { name: "监督工作组" }).click();

  await expect(workerSelect).toContainText("监督工作组");
  await expect.poll(async () => (await readStoredPreviewConfig(page))?.delegation?.supervision?.workerGroupID).toBe("preview-group-1");
});
