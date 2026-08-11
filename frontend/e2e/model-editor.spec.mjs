import { expect, test } from "@playwright/test";
import {
  openModelEditorPage,
  readStoredPreviewConfig,
  testResultSuccess,
} from "./helpers.mjs";

const EDIT_ADAPTER = {
  id: "preview-demo-openai",
  displayName: "Demo GPT",
  groupName: "浏览器预览示例",
  type: "openai",
  supplierID: "custom",
  baseURL: "https://api.openai.com/v1",
  apiKey: "browser-preview-demo-key",
  modelID: "gpt-4.1-mini",
  reasoningEffort: "medium",
  openAIEndpoint: "/v1/responses",
  openAIRequestGroup: "responses",
  protocolMode: "auto",
  protocolGroup: "responses",
  contextWindowTokens: 1047576,
  maxCompletionTokens: 0,
  pricing: null,
  balanceProfile: "general",
};

// 输入框均位于 label 内（无 aria-label），以 label 文本定位。
function fieldInput(page, labelText, index = 0) {
  return page.locator("label").filter({ hasText: labelText }).locator("input").nth(index);
}

test("快捷添加：填写连接信息后进入模型目录按钮可用", async ({ page }) => {
  await openModelEditorPage(page);

  await expect(page.getByText("快捷添加")).toBeVisible();
  const goCatalog = page.getByRole("button", { name: "拉取模型" });
  await expect(goCatalog).toBeDisabled();

  await fieldInput(page, "接口地址").fill("https://api.example.com/v1");
  await fieldInput(page, "访问密钥").fill("sk-test-e2e");

  await expect(goCatalog).toBeEnabled();
  await goCatalog.click();
  await expect(page).toHaveURL(/\/model-catalog/);
});

test("手动添加：填写身份字段后保存，配置写入本地存储并跳转模型配置页", async ({ page }) => {
  await openModelEditorPage(page);

  await page.getByRole("button", { name: "手动添加", exact: true }).click();
  await expect(page.getByRole("button", { name: "保存", exact: true })).toBeVisible();

  await fieldInput(page, "显示名称").fill("E2E 手动模型");
  await fieldInput(page, "模型标识").fill("e2e-manual-model");
  await fieldInput(page, "接口地址").fill("https://api.example.com/v1");
  await fieldInput(page, "访问密钥").fill("sk-test-e2e");

  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page).toHaveURL(/\/model-config/);

  const stored = await readStoredPreviewConfig(page);
  const saved = stored?.modelAdapters?.find((adapter) => adapter.modelID === "e2e-manual-model");
  expect(saved).toBeTruthy();
  expect(saved.displayName).toBe("E2E 手动模型");
});

test("手动添加：保存失败时展示错误信息且不跳转", async ({ page }) => {
  await openModelEditorPage(page, {
    plan: { saveConfigFailure: true },
  });

  await page.getByRole("button", { name: "手动添加", exact: true }).click();
  await fieldInput(page, "显示名称").fill("E2E 失败模型");
  await fieldInput(page, "模型标识").fill("e2e-fail-model");
  await fieldInput(page, "接口地址").fill("https://api.example.com/v1");
  await fieldInput(page, "访问密钥").fill("sk-test-e2e");

  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByText("服务发生异常，请重试或导出诊断信息")).toBeVisible();
  await expect(page).toHaveURL(/\/model-editor/);
});

test("编辑既有模型：加载草稿并支持保存并测试", async ({ page }) => {
  await openModelEditorPage(page, {
    plan: {
      editorContext: { index: 0, adapterJSON: JSON.stringify(EDIT_ADAPTER) },
      testResult: testResultSuccess(),
    },
  });

  await expect(page.getByRole("button", { name: "保存", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "保存并测试", exact: true })).toBeVisible();

  await expect(fieldInput(page, "模型标识")).toHaveValue("gpt-4.1-mini");
  await expect(fieldInput(page, "显示名称")).toHaveValue("Demo GPT");

  // 行为字段按类型显示
  await expect(page.getByText("最大输出 Token", { exact: true })).toBeVisible();
  await expect(page.getByText("推理强度上限", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "保存并测试", exact: true }).click();
  await expect(page.getByText("总生成 61 t/s | 正文 22 t/s | 首响应 24.6 s | 首字 27.2 s")).toBeVisible();
  await expect(page.getByText("实际推理强度", { exact: true })).toBeVisible();
  await expect(page.getByText("medium", { exact: true })).toBeVisible();
});

test("编辑模式：保存后跳转模型配置页并持久化修改", async ({ page }) => {
  await openModelEditorPage(page, {
    plan: { editorContext: { index: 0, adapterJSON: JSON.stringify(EDIT_ADAPTER) } },
  });

  await fieldInput(page, "模型标识").fill("gpt-4.1-mini-renamed");

  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page).toHaveURL(/\/model-config/);

  const stored = await readStoredPreviewConfig(page);
  const saved = stored?.modelAdapters?.find((adapter) => adapter.id === "preview-demo-openai");
  expect(saved).toBeTruthy();
  expect(saved.modelID).toBe("gpt-4.1-mini-renamed");
});
