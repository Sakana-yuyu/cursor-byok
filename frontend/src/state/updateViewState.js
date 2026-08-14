import { computed, reactive } from "vue";
import { localized, localizedTemplate } from "@/i18n/runtime";
import { formatReleaseDate } from "@/utils/valueCast";

export const GENERIC_SERVICE_ERROR = localized("6ae23d6d7cb18592", "服务错误");

function localizeUpdateMessage(msg, appState) {
  if (!msg) return "";
  if (/当前已是最新版本/u.test(msg)) {
    const match = msg.match(/v?([0-9]+\.[0-9]+\.[0-9]+)/);
    const version = match ? match[1] : appState.appVersion || "...";
    return localizedTemplate("a1a038dfa16c3ede", "当前已是最新版本（v{0}）。", [version]).toString();
  }
  return msg;
}

function localizeReadyContent(appState) {
  const version = appState.updateVersion || appState.appVersion || "...";
  const date = formatReleaseDate(appState.updateReleaseDate);
  const notes = appState.updateReleaseNotes || "";
  return [
    localizedTemplate("dc82c5e8fb2ab777", "版本：v{0}", [version]).toString(),
    localizedTemplate("3ea83f9f55062582", "发布时间：{0}", [date]).toString(),
    "",
    notes || localized("02216368edc68816", "无更新说明").toString(),
  ].join("\n");
}

/**
 * Update prompt/footer view-model extracted from appState.js.
 */
export function createUpdateViewState(appState) {
  return reactive({
    footerDownloading: computed(() => appState.updateState === "downloading"),
    footerBusy: computed(() => ["checking", "installing"].includes(appState.updateState)),
    footerVersionLabel: computed(() => `v${appState.appVersion || "..."}`),
    footerProgressText: computed(() => `${Math.round(appState.updateProgressPercent || 0)}%`),
    footerProgressStyle: computed(() => ({
      width: `${Math.max(0, Math.min(100, appState.updateProgressPercent || 0))}%`,
    })),
    promptTitle: computed(() => {
      switch (appState.updatePromptKind) {
        case "ready":
          return localized("ac217e4d1ca410f1", "发现新版本");
        case "error":
          return localized("ec99e5c45d648fd6", "更新失败");
        default:
          return localized("7f68ebad19ba6bcd", "检查更新");
      }
    }),
    promptContent: computed(() => {
      switch (appState.updatePromptKind) {
        case "ready":
          return localizeReadyContent(appState);
        case "error":
          return appState.updateError || localizeUpdateMessage(appState.updateMessage, appState) || GENERIC_SERVICE_ERROR;
        default:
          return (
            localizeUpdateMessage(appState.updateMessage, appState)
            || localizedTemplate("a1a038dfa16c3ede", "当前已是最新版本（v{0}）。", [appState.appVersion || "..."]).toString()
          );
      }
    }),
    promptConfirmText: computed(() => {
      if (appState.updatePromptKind === "ready") {
        return localized("37d23612f78a2e63", "立即重启更新");
      }
      return localized("fac2a67ad87807c4", "确定");
    }),
    promptCancelText: computed(() => {
      if (appState.updatePromptKind === "ready") {
        return localized("9d2ca261281a158a", "稍后");
      }
      return localized("2cd0f3be8738a86c", "取消");
    }),
    promptShowCancel: computed(() => appState.updatePromptKind === "ready"),
  });
}
