import { browserPreviewMockMetrics, browserPreviewMockProxyState } from "@/services/runtimeAdapter";

const previewConfig = {
  modelAdapters: [{
    id: "preview-demo-openai",
    displayName: "Demo GPT",
    groupName: "浏览器预览示例",
    type: "openai",
    baseURL: "https://api.openai.com/v1",
    apiKey: "sk-browser-preview-demo",
    tooltipData: "浏览器预览示例模型",
    modelID: "gpt-4.1-mini",
    reasoningEffort: "medium",
    openAIEndpoint: "/v1/responses",
    openAIExtraParamsEnabled: false,
    openAIExtraParamsJSON: "{\n}",
    customHeadersEnabled: false,
    customHeadersJSON: "{\n}",
    anthropicExtraParamsEnabled: false,
    anthropicExtraParamsJSON: "{\n}",
    contextWindowTokens: 0,
    maxCompletionTokens: 0,
    anthropicMaxTokens: 0,
    anthropicThinkingEffort: "xhigh",
    thinkingBudgetTokens: 0,
    pricing: null,
    fastMode: false,
    openAIServiceTier: "",
  }],
  backendListenAddr: "127.0.0.1:8787",
  proxyListenAddr: "127.0.0.1:8788",
  routing: { mode: "local" },
  homeMetrics: { includeCacheWriteInHitRate: false },
};
let editorContext = { index: -1, adapterJSON: "{}" };

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function nextID() {
  return `preview-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export const IsWindows = () => Promise.resolve(false);
export const GetState = () => Promise.resolve(browserPreviewMockProxyState());
export const LoadUserConfig = () => Promise.resolve(clone(previewConfig));
export const SaveUserConfig = (value) => {
  const next = value && typeof value === "object" ? clone(value) : {};
  previewConfig.modelAdapters = Array.isArray(next.modelAdapters)
    ? next.modelAdapters.map((adapter) => ({ ...adapter, id: adapter.id || nextID() }))
    : [];
  Object.assign(previewConfig, next, { modelAdapters: previewConfig.modelAdapters });
  return Promise.resolve(clone(previewConfig));
};
export const StartProxy = () => Promise.resolve(browserPreviewMockProxyState());
export const StopProxy = () => Promise.resolve(browserPreviewMockProxyState());
export const GetAdRuntime = () => Promise.resolve({ available: false, slots: [], window: {} });
export const OpenExternalURL = () => Promise.resolve();
export const GetHomeMetricsSummary = () => Promise.resolve(browserPreviewMockMetrics());
export const CheckForUpdates = () => Promise.resolve();
export const GetAppVersion = () => Promise.resolve("Browser Preview");
export const GetFooterAuthorInfo = () => Promise.resolve(null);
export const InstallReadyUpdate = () => Promise.resolve();
export const GetModelEditorContext = () => Promise.resolve(clone(editorContext));
export const OpenConfigWindow = () => Promise.resolve();
export const OpenFooterAuthorHome = () => Promise.resolve();
export const OpenHistoryWindow = () => Promise.resolve();
export const OpenModelConfigWindow = () => Promise.resolve();
export const OpenModelEditorWindow = (index, adapterJSON) => {
  editorContext = { index: Number.isInteger(index) ? index : -1, adapterJSON: String(adapterJSON || "{}") };
  return Promise.resolve();
};
export const TestModelAdapter = (adapter) => Promise.resolve({ status: "success", adapterID: adapter?.id || "preview", summaryText: "浏览器预览模式：未发起请求" });
export const GetModelAdapterTestResults = () => Promise.resolve([]);
export const FetchModelCatalog = () => Promise.resolve({ models: [] });
export const GetRecentRequestMetrics = () => Promise.resolve([]);
export const GetPromptInjectionSettings = () => Promise.resolve({});
export const SavePromptInjectionSettings = (value) => Promise.resolve(value);
export const RefreshPromptInjection = () => Promise.resolve();
export const RefreshPromptInjectionCatalog = () => Promise.resolve();