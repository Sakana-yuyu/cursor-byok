<script setup>
import { isBrowserPreview, runtimeBrowser, runtimeWindow } from "@/services/runtimeAdapter";
import LocaleSelect from "@/components/LocaleSelect.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  getFooterAuthorInfo,
  openFooterAuthorHome,
} from "@/services/clientApi";
import {
  appState,
  checkForAppUpdates,
  getStatsOverlayPreferences,
  refreshDebugLogUsage,
  syncProxyState,
  updateViewState,
} from "@/state/appState";
import { closeApplication as closeApplicationNative } from "@/services/clientApi";
import { isWindows } from "@/utils/isWindows";
import { isMacOS } from "@/utils/isMacOS";
import { safeErrorLogAttributes } from "@/utils/errorContract";
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { usePolling } from "@/composables/usePolling";
import Logo from "@/assets/logo.png";
import AppSidebar from "@/components/layout/AppSidebar.vue";

const route = useRoute();
const message = useMessage();
const showIcon = computed(() => route.meta.showIcon !== false);
const directlyClose = computed(() => route.meta.directlyClose === true);
const mainCloseAction = computed(() => appState.statsOverlayPreferences.closeAction === "quit" ? "quit" : "tray");
const showFooter = computed(() => route.path === "/");
const footerAuthorInfo = ref(null);

const localizedAuthorInfo = computed(() => {
  if (!footerAuthorInfo.value) return null;
  // 后端返回中文源文本；这里提供与后端一致的本地化文案，由静态扫描器收录并翻译，
  // 不再按 locale 分支硬编码多语言。
  return {
    buttonText: "作者",
    dialogTitle: "作者寄语",
    dialogContent:
      "本软件是纯免费软件，如果你被收费，那大概率就是被骗了。\n欢迎点击访问作者主页 https://space.bilibili.com/311706663/upload/video\n查看更多更新动态、使用分享和后续内容。",
    dialogConfirmText: "访问主页",
    dialogCancelText: "关闭",
  };
});
const usageDocsURL = "https://docs.leokun.cn";
const githubRepoURL = "https://github.com/Sakana-yuyu/cursor-byok";
const proxyStatePollIntervalMs = 10000;
const netProxyEndpoint = computed(
  () => appState.netProxyHttps || appState.netProxyHttp || "",
);
const proxyBadgeText = computed(() => {
  if (appState.netProxyUsingSystem) {
    return "已识别系统代理";
  }
  return "";
});
const proxyBadgeTitle = computed(() => {
  if (appState.netProxyUsingSystem) {
    return netProxyEndpoint.value
      ? `当前出站请求使用系统代理：${netProxyEndpoint.value}`
      : "当前出站请求使用系统代理";
  }
  if (appState.netProxyUsingEnv) {
    return netProxyEndpoint.value
      ? `当前出站请求使用环境变量代理：${netProxyEndpoint.value}`
      : "当前出站请求使用环境变量代理";
  }
  if (appState.netProxyPacIgnored) {
    return "检测到系统 PAC/自动代理，当前版本按直连处理";
  }
  return "当前出站请求未使用系统代理";
});

const isMaximised = ref(false);

const SIDEBAR_COLLAPSED_KEY = "cursor-byok.sidebarCollapsed";
const sidebarCollapsed = ref(localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "1");
function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value;
  localStorage.setItem(SIDEBAR_COLLAPSED_KEY, sidebarCollapsed.value ? "1" : "0");
}
const sidebarWidthClass = computed(() => (sidebarCollapsed.value ? "left-[48px]" : "left-[180px]"));

async function syncMaximiseState() {
  try {
    isMaximised.value = Boolean(await runtimeWindow.IsMaximised());
  } catch {
    // ignore
  }
}

async function minimizeWindow() {
  await runtimeWindow.Minimise();
}

async function toggleMaximiseWindow() {
  await runtimeWindow.ToggleMaximise();
  await syncMaximiseState();
}

async function closeWindow() {
  if (directlyClose.value) {
    await runtimeWindow.Close();
    return;
  }
  if (mainCloseAction.value === "quit") {
    await closeApplicationNative();
    return;
  }
  await runtimeWindow.Hide();
}

async function handleCheckForUpdates() {
  if (updateViewState.footerBusy || updateViewState.footerDownloading) {
    return;
  }
  const loadingMessageID = message.loading("检查更新中...");
  try {
    await checkForAppUpdates();
  } finally {
    if (loadingMessageID) {
      message.remove(loadingMessageID);
    }
  }
}

async function loadFooterAuthorInfo() {
  try {
    footerAuthorInfo.value = await getFooterAuthorInfo();
  } catch (error) {
    console.error("[MainLayout] 加载作者信息失败", safeErrorLogAttributes(error, { operation: "mainLayout.loadFooterAuthorInfo" }));
  }
}

async function showActionError(title, error) {
  await showModal({
    title,
    content: String(error || "操作失败").trim() || "操作失败",
    confirmText: "确定",
    showCancel: false,
  });
}

async function handleOpenAuthorHome() {
  if (!localizedAuthorInfo.value) {
    return;
  }
  const confirmed = await showModal({
    title: localizedAuthorInfo.value.dialogTitle,
    content: localizedAuthorInfo.value.dialogContent,
    confirmText: localizedAuthorInfo.value.dialogConfirmText,
    cancelText: localizedAuthorInfo.value.dialogCancelText,
    showCancel: true,
  });
  if (!confirmed) {
    return;
  }
  try {
    await openFooterAuthorHome();
  } catch (error) {
    await showActionError("打开主页失败", error);
  }
}

async function handleOpenUsageDocs() {
  try {
    await runtimeBrowser.OpenURL(usageDocsURL);
  } catch (error) {
    await showActionError("打开使用教程失败", error);
  }
}

async function handleOpenGitHubRepo() {
  try {
    await runtimeBrowser.OpenURL(githubRepoURL);
  } catch (error) {
    await showActionError("打开 GitHub 失败", error);
  }
}

onMounted(() => {
  Object.assign(appState.statsOverlayPreferences, getStatsOverlayPreferences());
  void loadFooterAuthorInfo();
  void syncMaximiseState();
});
// 首页轮询：代理状态 10s（另有 proxy:state 推送兜底）；磁盘占用统计变化慢，60s 才刷一次
usePolling(
  () => {
    if (showFooter.value) {
      return syncProxyState().catch(() => {});
    }
    return undefined;
  },
  { intervalMs: proxyStatePollIntervalMs },
);
usePolling(
  () => {
    if (showFooter.value) {
      return refreshDebugLogUsage().catch(() => {});
    }
    return undefined;
  },
  { intervalMs: 60000 },
);
</script>

<template>
  <div class="flex h-screen w-screen overflow-hidden">
    <div
      class="fixed top-0 h-[40px] z-9999"
      :class="sidebarWidthClass"
      style="--wails-draggable: drag"
    />

    <AppSidebar
      :collapsed="sidebarCollapsed"
      @toggle="toggleSidebar"
    />

    <div class="flex h-full min-w-0 flex-1 flex-col overflow-hidden">
      <header
        class="relative flex h-[40px] min-h-0 w-full shrink-0 items-center justify-between px-[20px]"
        style="--wails-draggable: drag"
        :class="{ '!justify-center': !isWindows, 'px-[76px] pr-[52px]': isMacOS }"
      >
        <div
          class="center-row min-w-0 gap-2"
          :class="isMacOS ? 'pr-0' : 'pr-[124px]'"
          style="font-family: var(--font-num);"
        >
          <img
            v-if="showIcon"
            :src="Logo"
            class="h-[16px] w-[16px] shrink-0 opacity-90"
          >
        </div>
        <div
          v-if="isWindows || (isBrowserPreview && !isMacOS)"
          class="absolute right-[10px] top-[7px] z-99999 flex items-center gap-[1px] rounded-full border border-[#333] bg-[#242424]/90 px-[3px] py-[3px] shadow-[0_1px_4px_rgba(0,0,0,0.35)] backdrop-blur-sm"
          style="--wails-draggable: no-drag"
        >
          <button
            type="button"
            aria-label="最小化窗口"
            title="最小化"
            class="flex h-[20px] w-[26px] cursor-pointer items-center justify-center rounded-full text-[#9a9a9a] transition-colors duration-150 hover:bg-[#3a3a3a] hover:text-[#f0f0f0]"
            @click="minimizeWindow"
          >
            <svg
              class="h-[14px] w-[14px]"
              viewBox="0 0 16 16"
              aria-hidden="true"
              fill="none"
            >
              <path
                d="M3.5 8H12.5"
                stroke="currentColor"
                stroke-width="1.4"
                stroke-linecap="round"
              />
            </svg>
          </button>
          <button
            type="button"
            aria-label="最大化窗口"
            :title="isMaximised ? '还原' : '最大化'"
            class="flex h-[20px] w-[26px] cursor-pointer items-center justify-center rounded-full text-[#9a9a9a] transition-colors duration-150 hover:bg-[#3a3a3a] hover:text-[#f0f0f0]"
            @click="toggleMaximiseWindow"
          >
            <svg
              v-if="!isMaximised"
              class="h-[13px] w-[13px]"
              viewBox="0 0 16 16"
              aria-hidden="true"
              fill="none"
            >
              <rect
                x="3.5"
                y="3.5"
                width="9"
                height="9"
                rx="1.6"
                stroke="currentColor"
                stroke-width="1.4"
              />
            </svg>
            <svg
              v-else
              class="h-[13px] w-[13px]"
              viewBox="0 0 16 16"
              aria-hidden="true"
              fill="none"
            >
              <rect
                x="5"
                y="5"
                width="7.5"
                height="7.5"
                rx="1.4"
                stroke="currentColor"
                stroke-width="1.4"
              />
              <path
                d="M4 10.5V4.5C4 3.95 4.45 3.5 5 3.5H11"
                stroke="currentColor"
                stroke-width="1.4"
                stroke-linecap="round"
              />
            </svg>
          </button>
          <button
            type="button"
            aria-label="关闭窗口"
            :title="directlyClose || mainCloseAction === 'quit' ? '关闭' : '隐藏到托盘'"
            class="flex h-[20px] w-[26px] cursor-pointer items-center justify-center rounded-full text-[#9a9a9a] transition-colors duration-150 hover:bg-[#b23b3b] hover:text-white"
            @click="closeWindow"
          >
            <svg
              class="h-[14px] w-[14px]"
              viewBox="0 0 16 16"
              aria-hidden="true"
              fill="none"
            >
              <path
                d="M4 4L12 12M12 4L4 12"
                stroke="currentColor"
                stroke-width="1.4"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>
      </header>

      <main class="flex-1 min-h-0 overflow-hidden flex flex-col w-full">
        <router-view />
      </main>

      <footer
        v-if="showFooter"
        class="flex !pr-1 h-[30px] shrink-0 items-center gap-[8px] border-t border-[#242424] px-[14px] text-[12px] text-[#8f8f8f]"
      >
        <div
          v-if="proxyBadgeText"
          class="center-row  border-none gap-[2px]  border-none  px-[0px] py-[3px] leading-none "
          aria-live="polite"
        >
          <span class="icon-[mdi--wifi] text-[15px]" />
          <span class="truncate">{{ proxyBadgeText }}</span>
        </div>
        <button
          v-if="!updateViewState.footerDownloading"
          type="button"
          class="center-row shrink-0 gap-[6px] cursor-pointer rounded-[6px] px-[6px] py-[3px] transition-colors duration-150 hover:bg-[#1f1f1f] hover:text-[#e5e5e5]"
          :disabled="updateViewState.footerBusy"
          @click="handleCheckForUpdates"
        >
          <span>{{ updateViewState.footerVersionLabel }}</span>
          <span>检查更新</span>
        </button>
        <button
          type="button"
          class="center-row shrink-0 gap-[2px]  cursor-pointer rounded-[6px] px-[6px] py-[3px] transition-colors duration-150 hover:bg-[#1f1f1f] hover:text-[#e5e5e5]"
          @click="handleOpenUsageDocs"
        >
          <span class="icon-[mdi--file-document-outline] text-[15px]" />
          <span>使用教程</span>
        </button>
        <button
          v-if="localizedAuthorInfo"
          type="button"
          class="center-row shrink-0 gap-[6px] cursor-pointer rounded-[6px] px-[6px] py-[3px] transition-colors duration-150 hover:bg-[#1f1f1f] hover:text-[#e5e5e5]"
          @click="handleOpenAuthorHome"
        >
          <span class="icon-[ant-design--bilibili-outlined] text-[14px]" />
          <span>{{ localizedAuthorInfo.buttonText }}</span>
        </button>
        <button
          type="button"
          title="GitHub"
          class="center-row shrink-0 gap-[6px] cursor-pointer rounded-[6px] px-[6px] py-[3px] transition-colors duration-150 hover:bg-[#1f1f1f] hover:text-[#e5e5e5]"
          @click="handleOpenGitHubRepo"
        >
          <span class="icon-[mdi--github] text-[15px]" />
          <span>GitHub</span>
        </button>
        <div
          v-if="updateViewState.footerDownloading"
          class="flex min-w-0 flex-1 items-center gap-[10px]"
        >
          <span class="shrink-0">{{ updateViewState.footerVersionLabel }}</span>
          <div class="center-row min-w-0 gap-[8px]">
            <div
              class="h-[6px] w-[120px] overflow-hidden rounded-full bg-[#1f1f1f]"
            >
              <div
                class="h-full rounded-full bg-gradient-to-r from-[#10AD5D] to-[#29c776]"
                :style="updateViewState.footerProgressStyle"
              />
            </div>
            <span class="shrink-0 text-[#d4d4d4]">{{
              updateViewState.footerProgressText
            }}</span>
          </div>
        </div>
        <div class="ml-auto flex shrink-0 items-center gap-[8px]">
          <LocaleSelect
            :border="false"
            aria-label="界面语言"
            wrapper-class="w-auto"
            button-class="h-[24px] bg-transparent px-1.5 text-[12px] !text-[#8f8f8f] !hover:text-[#e5e5e5]"
            menu-class="text-[12px]"
          />
        </div>
      </footer>
    </div>
  </div>
</template>
