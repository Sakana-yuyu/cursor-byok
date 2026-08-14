import { computed, reactive } from "vue";
import { localized } from "@/i18n/runtime";

/**
 * Service card view-model extracted from appState.js.
 * Depends on the shared appState reactive singleton.
 */
export function createServiceViewState(appState) {
  return reactive({
    serviceStatusText: computed(() => {
      if (appState.proxyRunning && appState.backendRunning) {
        return localized("86df7ec743047234", "服务运行中");
      }
      if (appState.backendRunning) {
        return localized("65cc5fd2e6ce6e75", "后端已启动，代理未启动");
      }
      if (appState.proxyRunning) {
        return localized("bf57374339c9684c", "代理已启动，后端未启动");
      }
      return localized("26a3855aed1d8d17", "服务未启动");
    }),
    serviceStatusClass: computed(() =>
      appState.serviceRunning ? "text-[#22c55e]" : "text-[#f59e0b]",
    ),
    serviceButtonText: computed(() => {
      const anyRunning = appState.serviceRunning || appState.servicePartiallyRunning;
      if (appState.serviceBusy) {
        return anyRunning
          ? localized("fb7a4c81729ed0ca", "关闭中...")
          : localized("ca00a39fcea70dc6", "启动中...");
      }
      return anyRunning
        ? localized("f474a4108aba4c4c", "关闭服务")
        : localized("18b7312022cd1840", "启动服务");
    }),
  });
}
