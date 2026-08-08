import { Browser as WailsBrowser, Events as WailsEvents, Window as WailsWindow } from "@wailsio/runtime";

const browserPreviewFlag = String(import.meta.env?.VITE_BROWSER_PREVIEW || "").toLowerCase();

// 浏览器 mock 必须由构建模式显式开启。Wails v3 不保证暴露
// window.runtime，使用它探测会把真实桌面包误判为浏览器预览。
function detectBrowserPreview() {
  if (import.meta.env?.MODE === "browser-preview") return true;
  return browserPreviewFlag === "true" || browserPreviewFlag === "1";
}

export const isBrowserPreview = detectBrowserPreview();

const noopUnsubscribe = () => {};
const noopEvent = {
  On: () => noopUnsubscribe,
  Off: () => {},
  Emit: () => {},
};

export const runtimeEvents = isBrowserPreview ? noopEvent : WailsEvents;

const noopWindow = {
  Minimise: () => Promise.resolve(),
  Maximise: () => Promise.resolve(),
  UnMaximise: () => Promise.resolve(),
  ToggleMaximise: () => Promise.resolve(),
  IsMaximised: () => Promise.resolve(false),
  Close: () => Promise.resolve(),
  Hide: () => Promise.resolve(),
};

const noopBrowser = {
  OpenURL: (url) => {
    if (isBrowserPreview && typeof window !== "undefined" && url) {
      window.open(url, "_blank", "noopener,noreferrer");
    }
    return Promise.resolve();
  },
};

export const runtimeWindow = isBrowserPreview ? noopWindow : WailsWindow;
export const runtimeBrowser = isBrowserPreview ? noopBrowser : WailsBrowser;
export const runtimeIsWindows = isBrowserPreview
  ? false
  : typeof navigator !== "undefined" && /Windows/i.test(navigator.userAgent);

export function browserPreviewMockProxyState() {
  return {
    serviceRunning: false,
    backendRunning: false,
    proxyRunning: false,
    serviceLastError: "",
    serviceListenAddr: "127.0.0.1:8788",
    backendListenAddr: "127.0.0.1:8787",
    proxyListenAddr: "127.0.0.1:8788",
    configBackendListenAddr: "127.0.0.1:8787",
    configProxyListenAddr: "127.0.0.1:8788",
    cursorSettingsApplied: false,
    netProxySource: "",
    netProxyActive: false,
    netProxyUsingSystem: false,
    netProxyUsingEnv: false,
    netProxyHttp: "",
    netProxyHttps: "",
    netProxyPacIgnored: false,
    netProxyDescription: "浏览器预览模式",
  };
}

export function browserPreviewMockMetrics() {
  return {
    totalRequests: 0,
    successfulRequests: 0,
    failedRequests: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    inputTokens: 0,
    outputTokens: 0,
    cacheHitRate: 0,
    includeCacheWriteInHitRate: false,
  };
}