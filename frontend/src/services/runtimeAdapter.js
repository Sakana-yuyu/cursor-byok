import { Browser as WailsBrowser, Events as WailsEvents, Window as WailsWindow } from "@wailsio/runtime";

const browserPreviewFlag = String(import.meta.env?.VITE_BROWSER_PREVIEW || "").toLowerCase();
export const isBrowserPreview = import.meta.env?.MODE === "browser-preview"
  || browserPreviewFlag === "true"
  || browserPreviewFlag === "1";

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

export function browserPreviewMockConfig() {
  return {
    modelAdapters: [],
    backendListenAddr: "127.0.0.1:8787",
    proxyListenAddr: "127.0.0.1:8788",
    routing: { mode: "local" },
    homeMetrics: { includeCacheWriteInHitRate: false },
  };
}

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