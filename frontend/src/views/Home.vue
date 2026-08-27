<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import ActionMenu from "@/components/ui/ActionMenu.vue";
import HomeMetricsCard from "@/components/HomeMetricsCard.vue";
import StationSpendCard from "@/components/StationSpendCard.vue";
import DelegationTaskStrip from "@/components/DelegationTaskStrip.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { appState, appViewState, DEBUG_LOG_WARNING_BYTES, getCursorManualPath, openLocalLogsDirectory, exportLogsAction, repairProxyAction, restartCursorAction, isCursorRunningAction, repairCACorruptionAction, offerDefenderExclusionAction, getDefenderExclusionStateAction, dismissDefenderExclusionAction, saveRoutingMode, syncServiceState, refreshDebugLogUsage, toUserError, toggleService } from "@/state/appState";
import { purgeAllHistoryDebugLogs } from "@/services/runtimeControlApi";
import { getCARepairStatus, launchCursor } from "@/services/clientApi";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const message = useMessage();
const directModeEnabled = computed(() => appState.routingMode === "upstream");
const exportingLogs = ref(false);
const repairingProxy = ref(false);
const repairingCA = ref(false);
const launchingCursor = ref(false);
const restartingCursor = ref(false);
const stationSpendCard = ref(null);
const debugLogsClearing = ref(false);
const debugUsageRetrying = ref(false);
const caAutoRepaired = ref(false); // 本次启动 CA 被自动修复（cert/key 失配重建）
const caRepairDismissed = ref(false);
const defenderPromptBusy = ref(false); // 一键添加 Defender 排除项进行中
const debugLogsWarningVisible = computed(() => appState.debugLogBytes >= DEBUG_LOG_WARNING_BYTES);

function formatSize(bytes) {
  const value = Number(bytes || 0);
  if (value >= 1024 * 1024 * 1024) return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

// handleClearDebugLogs 一次后端调用清理全部调试日志。目录遍历放在后端，
// 才能覆盖非 UUID 会话目录与无会话归属的孤儿日志——前端枚举会话会漏掉它们。
async function handleClearDebugLogs() {
  if (debugLogsClearing.value) return;
  debugLogsClearing.value = true;
  try {
    const freed = await purgeAllHistoryDebugLogs();
    await refreshDebugLogUsage();
    message.success(freed > 0 ? `已清理调试日志，释放 ${formatSize(freed)}` : "没有可清理的调试日志");
  } catch (error) {
    await showActionError("清理失败", toUserError(error));
  } finally {
    debugLogsClearing.value = false;
  }
}
// handleRetryDebugUsage 重试调试日志占用统计。统计失败时保留上次的值，
// 这里只需重新读一次；成功后横幅自动消失，失败则把原因直接告诉用户。
async function handleRetryDebugUsage() {
  if (debugUsageRetrying.value) return;
  debugUsageRetrying.value = true;
  try {
    await refreshDebugLogUsage();
    if (appState.debugLogUsageError) {
      await showActionError("读取失败", appState.debugLogUsageError);
      return;
    }
    message.success("已重新统计调试日志占用");
  } catch (error) {
    // 与同处横幅的 handleClearDebugLogs 对齐：refreshDebugLogUsage 抛错时
    // 不能让 rejection 直接冒泡到点击处理器，需提示用户。
    await showActionError("读取失败", toUserError(error));
  } finally {
    debugUsageRetrying.value = false;
  }
}

function handleMetricsRefresh() {
  void stationSpendCard.value?.refresh?.();
}

async function showActionError(title, error) { await showModal({ title, content: String(error || "服务错误").trim() || "服务错误" }); }
async function handleToggleService() { const result = await toggleService(); if (!result.ok) await showActionError("服务操作失败", result.error); }
// handleExportLogs 导出日志 ZIP。空路径不算成功：真实后端成功时一定带路径，
// 只有不支持导出的环境（如浏览器预览）才会返回空，弹「导出成功」会骗人。
async function handleExportLogs() {
  if (exportingLogs.value) return;
  exportingLogs.value = true;
  try {
    const result = await exportLogsAction();
    if (!result.ok) {
      await showActionError("导出失败", result.error);
      return;
    }
    if (!result.path) {
      await showActionError("导出不可用", "当前环境不支持导出日志 ZIP，请在桌面客户端中操作。");
      return;
    }
    if (await showModal({ title: "导出成功", content: `日志已导出到：${result.path}`, confirmText: "打开目录", cancelText: "关闭" })) await openLocalLogsDirectory();
  } finally {
    exportingLogs.value = false;
  }
}
async function handleOpenLogsDirectory() { try { await openLocalLogsDirectory(); } catch (error) { await showActionError("打开失败", toUserError(error)); } }
async function handleDirectModeChange(enabled) { if (enabled && !(await showModal({ title: "开启官方上游模式", content: "Cursor relay 请求将直通官方服务，可能产生官方账号计费。若本地服务正在运行，官方模型 API 仍会经过本地 MITM，以便镜像记录。确定开启吗？", confirmText: "开启官方上游", cancelText: "取消" }))) return; const result = await saveRoutingMode(enabled ? "upstream" : "local"); if (!result.ok) { await showActionError("切换失败", result.error); return; }
  // 切直连时后端已恢复官方登录态（state.vscdb），但运行中的 Cursor 不会重新读取
  // 状态库，需重启才生效。对齐「修复代理后重启」交互：检测到运行中则引导重启。
  if (enabled) {
    const probe = await isCursorRunningAction();
    if (probe.ok && probe.running) {
      if (await showModal({ title: "已恢复官方登录态", content: "已切换到官方上游模式并恢复 Cursor 官方登录态。检测到 Cursor 正在运行，重启后官方登录态才会完全生效。立即重启 Cursor 吗？", confirmText: "立即重启", cancelText: "稍后重启" })) await handleRestartCursor({ skipConfirm: true });
      return;
    }
  }
  message.success(enabled ? "已切换到官方上游模式" : "已切换到本地服务模式"); }
async function handleLaunchCursor() {
  if (launchingCursor.value) return;
  launchingCursor.value = true;
  try {
    await launchCursor("", getCursorManualPath());
    message.success("Cursor 启动成功");
  } catch (error) {
    await showActionError("启动失败", toUserError(error));
  } finally {
    launchingCursor.value = false;
  }
}

// handleRestartCursor 重启 Cursor。skipConfirm 供已经确认过的调用方（如修复代理后
// 直接选择「立即重启」）使用，避免连续弹两次相同的确认框。
async function handleRestartCursor({ skipConfirm = false } = {}) {
  if (restartingCursor.value) return;
  if (!skipConfirm) {
    const confirmed = await showModal({
      title: "重启 Cursor",
      content: "重启将关闭所有 Cursor 窗口，未保存的内容可能丢失。确认要重启 Cursor 吗？",
      confirmText: "重启",
      cancelText: "取消",
    });
    if (!confirmed) return;
  }
  restartingCursor.value = true;
  try {
    const result = await restartCursorAction(getCursorManualPath());
    if (!result.ok) {
      await showActionError("重启失败", result.error);
      return;
    }
    const r = result.result || {};
    const detailLines = Array.isArray(r.details) && r.details.length ? r.details.join("\n") : "";
    let content = r.relaunched ? "Cursor 已重新启动。" : "重启未完全成功，请检查详情。";
    if (detailLines) content += `\n\n${detailLines}`;
    await showModal({ title: "重启完成", content, confirmText: "好的", showCancel: false });
  } finally {
    restartingCursor.value = false;
  }
}

// handleRepairCA 一键修复 CA 异常：备份残留文件并重新生成 CA，热重载内存状态后自动启动代理。
// 修复后无需重启应用（热重载），但需重启 Cursor 使新 CA 生效。
async function handleRepairCA() {
  if (repairingCA.value) return;
  const confirmed = await showModal({
    title: "修复 CA 证书",
    content: "本地 CA 异常（证书/密钥缺失其一或无法读取），本地代理已停用。修复将备份残留文件并重新生成 CA，完成后自动启动本地代理。继续吗？",
    confirmText: "一键修复",
    cancelText: "取消",
  });
  if (!confirmed) return;
  repairingCA.value = true;
  try {
    const result = await repairCACorruptionAction();
    if (!result.ok) {
      await showActionError("修复失败", result.error);
      return;
    }
    const r = result.result || {};
    await syncServiceState().catch(() => {});
    let content;
    if (r.repaired) {
      content = "已备份残留文件并重新生成 CA。";
      if (r.backupPath) content += `\n\n备份文件：${r.backupPath}`;
      // 热重载已清 caIncomplete，尝试自动启动代理（修复前处于停用态）。
      const startResult = await toggleService();
      await syncServiceState().catch(() => {});
      if (!startResult.ok) {
        content += `\n\n本地代理自动启动失败：${startResult.error}。请稍后手动点击「启动服务」。`;
      } else {
        content += "\n\n本地代理已自动启动。请重启 Cursor 使新 CA 生效。";
      }
    } else {
      content = r.detail || "CA 材料完整，无需修复。";
    }
    await showModal({ title: "修复完成", content, confirmText: "知道了", showCancel: false });
  } finally {
    repairingCA.value = false;
  }
}

// maybePromptDefenderExclusion 启动时「仅一次」引导用户把应用目录加入杀软排除项，
// 防止杀软误删 CA 私钥（ca.key）导致本地代理降级。
// 策略（A + B 结合）：
//   - Windows Defender 活动时：弹窗提供「一键添加排除项」按钮，点击后触发 UAC 提权。
//   - 非 Defender（第三方杀软 / 非 Windows）：弹窗展示应用数据目录路径，引导用户手动添加。
// 用户点「跳过」或操作完成后标记已提示（持久化），不再重复弹窗。
async function maybePromptDefenderExclusion() {
  if (isBrowserPreview) return; // 浏览器预览模式无后端，跳过。
  const stateResult = await getDefenderExclusionStateAction();
  if (!stateResult.ok) return; // 查询失败不阻塞首页，静默跳过。
  const st = stateResult.result || {};
  // 仅一次：已提示过 / 已排除 / 平台不支持 -> 不弹。
  if (st.offered || st.alreadyExcluded || !st.supported) return;

  const appPath = st.path || "应用数据目录";

  if (st.defenderActive) {
    // A：Defender 一键添加。
    const confirmed = await showModal({
      title: "防止杀软误删",
      content: `检测到 Windows Defender 正在运行。为确保本地 CA 私钥（ca.key）不被误杀导致本地代理停用，建议把应用数据目录加入 Defender 排除项：\n\n${appPath}\n\n点击「一键添加」将弹出管理员授权（UAC），授权后自动完成。`,
      confirmText: "一键添加",
      cancelText: "跳过",
    });
    if (!confirmed) {
      // 用户跳过：标记已提示，不再弹窗。
      await dismissDefenderExclusionAction().catch(() => {});
      return;
    }
    // 触发 UAC 提权添加排除项。
    defenderPromptBusy.value = true;
    try {
      const addResult = await offerDefenderExclusionAction();
      const r = addResult.result || {};
      let content;
      if (!addResult.ok) {
        content = `添加 Defender 排除项失败：${addResult.error}\n\n可手动在 Windows 安全中心 → 病毒和威胁防护 → 排除项中添加：\n${appPath}`;
      } else if (r.cancelled) {
        // 用户在 UAC 中取消：不标记为已提示，下次启动再引导。
        await showModal({ title: "已取消", content: "未授予管理员权限，排除项未添加。下次启动会再次提醒。", confirmText: "知道了", showCancel: false });
        return;
      } else if (r.added) {
        content = `已成功把以下目录加入 Windows Defender 排除项：\n\n${appPath}\n\n本地 CA 私钥不会再被误删。`;
      } else if (r.alreadyExcluded) {
        content = `该目录已在 Defender 排除项中，无需重复添加：\n\n${appPath}`;
      } else if (r.error) {
        content = `添加 Defender 排除项失败：${r.error}\n\n可手动在 Windows 安全中心 → 病毒和威胁防护 → 排除项中添加：\n${appPath}`;
      } else {
        content = `操作已完成。如未添加成功，可手动在 Windows 安全中心排除项中添加：\n\n${appPath}`;
      }
      await showModal({ title: "排除项引导", content, confirmText: "知道了", showCancel: false });
    } finally {
      defenderPromptBusy.value = false;
    }
  } else {
    // B：非 Defender（第三方杀软 / 非 Windows）引导说明。
    await showModal({
      title: "防止杀软误删",
      content: `为确保本地 CA 私钥（ca.key）不被杀毒软件误删导致本地代理停用，建议在您的杀毒软件中把应用数据目录加入排除/白名单：\n\n${appPath}\n\n（未检测到 Windows Defender；若使用第三方杀软，请在其设置中手动添加排除项。）`,
      confirmText: "知道了",
      showCancel: false,
    });
    // 标记已提示，实现「仅一次」（用户已知悉，下次启动不再弹窗）。
    await dismissDefenderExclusionAction().catch(() => {});
  }
}

async function handleRepairProxy() {
  if (repairingProxy.value) return;
  repairingProxy.value = true;
  try {
    const result = await repairProxyAction();
    if (!result.ok) {
      await showActionError("修复失败", result.error);
      return;
    }
    const r = result.result || {};
    const detailLines = Array.isArray(r.details) && r.details.length ? r.details.join("\n") : "";
    const proxyText = r.proxyURL || appState.proxyListenAddr || "";
    await syncServiceState().catch(() => {});
    // settingsApplied 是后端读回 settings.json 的校验结果。校验没过就不能说「修复成功」，
    // 否则用户带着「已经修好了」的预期回去，遇到的还是同一个连不上的问题。
    if (!r.settingsApplied) {
      let content = proxyText
        ? `代理配置写入后校验未通过，Cursor 仍未指向 ${proxyText}。`
        : "代理配置写入后校验未通过。";
      if (detailLines) content += `\n\n${detailLines}`;
      if (r.settingsPath) content += `\n\n配置文件：${r.settingsPath}`;
      content += "\n\n可先完全退出 Cursor 再重试修复；若仍失败，请导出日志 ZIP 反馈。";
      await showModal({ title: "修复未完成", content, confirmText: "好的", showCancel: false });
      return;
    }
    let content = proxyText ? `已重新写入并校验 Cursor 代理配置：${proxyText}` : "已重新写入并校验 Cursor 代理配置";
    if (detailLines) content += `\n\n${detailLines}`;
    // Cursor 正在运行时配置不会被读到，直接把「重启 Cursor」作为下一步操作给出来，
    // 而不是只留一句提示让用户自己去找入口。
    if (r.cursorRunning) {
      content += "\n\n检测到 Cursor 正在运行，需要完全退出后重新打开才能生效。";
      if (await showModal({ title: "修复成功", content, confirmText: "立即重启 Cursor", cancelText: "稍后手动重启" })) {
        await handleRestartCursor({ skipConfirm: true });
      }
      return;
    }
    content += "\n\n配置已生效，重新打开 Cursor 即可读取自定义模型。";
    if (await showModal({ title: "修复成功", content, confirmText: "启动 Cursor", cancelText: "关闭" })) {
      await handleLaunchCursor();
    }
  } finally {
    repairingProxy.value = false;
  }
}
onMounted(() => { void syncServiceState().catch(() => {}); });
// 查询本次启动是否发生过 CA 自动修复（cert/key 失配重建），是则提示重启 Cursor。
void getCARepairStatus().then((status) => { if (status?.repaired) caAutoRepaired.value = true; }).catch(() => {});
// 启动时「仅一次」引导用户把应用目录加入杀软排除项（防止 ca.key 被误删）。
// 延迟一拍，避免与 CA 修复横幅/服务状态查询同时弹出，让用户先看到首页主体。
void maybePromptDefenderExclusion().catch(() => {});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col text-[#e5e5e5]">
    <div class="mx-auto w-full max-w-[1400px] shrink-0 px-4 pb-4">
      <Card><div class="flex flex-col gap-4"><div class="flex flex-wrap items-center justify-between gap-3"><div class="center-row min-w-0 flex-wrap gap-2"><span class="size-2.5 shrink-0 rounded-full" :class="appState.serviceRunning ? 'bg-[#22c55e]' : (appState.servicePartiallyRunning ? 'bg-[#f59e0b]' : 'bg-[#737373]')" /><h2 class="text-base font-semibold text-white">{{ appViewState.serviceStatusText }}</h2><span class="rounded-full border border-[#3f3f3f] px-2 py-0.5 text-xs text-[#a3a3a3]">{{ directModeEnabled ? "官方上游" : "本地服务" }}</span><span v-if="appState.proxyRunning && appState.proxyListenAddr" class="center-row gap-1 text-xs text-[#737373]"><span class="icon-[mdi--lan-connect] text-[13px]" />{{ appState.proxyListenAddr }}</span></div><div class="center-row flex-wrap justify-end gap-2"><div class="min-w-[190px] rounded-[7px] border border-[#343434] bg-[#252525]/70 px-2.5 py-1.5"><div class="flex items-center justify-between gap-3"><div class="flex min-w-0 items-center gap-1.5"><span class="text-[12px] font-medium text-white">官方上游模式</span><button type="button" aria-label="官方上游模式说明" title="官方上游模式：Cursor relay 请求直通官方；本地服务运行时，模型 API 仍会经过本地 MITM 以便镜像记录。" class="center-row h-[18px] w-[18px] shrink-0 cursor-help rounded-full text-[#858585] transition-colors hover:text-[#d4d4d4] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/45" @click.stop><span class="icon-[mdi--information-outline] text-[14px]" aria-hidden="true"></span></button></div><Switch compact label="" :enabled="directModeEnabled" :busy="appState.configSaving" :disabled="appState.configSaving" @change="handleDirectModeChange" /></div></div><ActionMenu match-trigger><template #trigger><div class="inline-flex shrink-0 cursor-pointer items-stretch gap-[2px] rounded-[6px] bg-[linear-gradient(to_bottom,#656565_0%,#3A3A3A_10px,#3A3A3A_100%)] !px-[1px] active:scale-105 transition-transform duration-150"><button type="button" :disabled="launchingCursor" class="relative z-10 inline-flex min-h-[24px] items-center gap-[2px] rounded-l-[5px] bg-gradient-to-b from-[#2a2a2a] to-[#1f1f1f] px-[7px] py-[3px] text-sm text-white transition-colors hover:from-[#303030] hover:to-[#252525] disabled:cursor-not-allowed disabled:opacity-60" @click.stop="handleLaunchCursor"><span class="icon-[mdi--cursor-default-outline] text-[16px]" />{{ launchingCursor ? "启动中..." : "启动 Cursor" }}</button><button type="button" :disabled="restartingCursor" aria-label="Cursor 操作" class="relative z-10 inline-flex min-h-[24px] items-center rounded-r-[5px] bg-gradient-to-b from-[#2a2a2a] to-[#1f1f1f] px-[5px] py-[3px] text-[#a3a3a3] transition-colors hover:from-[#303030] hover:to-[#252525] disabled:cursor-not-allowed disabled:opacity-60"><span class="icon-[mdi--chevron-down] text-[13px]" /></button></div></template><template #items="{ close }"><button type="button" class="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-sm text-[#d4d4d4] transition-colors hover:bg-white/5 disabled:cursor-not-allowed disabled:text-[#5f5f5f]" :disabled="restartingCursor" role="menuitem" @click="() => { close(); void handleRestartCursor(); }"><span class="icon-[mdi--restart] text-[15px] text-[#a3a3a3]" /><span>{{ restartingCursor ? "重启中..." : "重启 Cursor" }}</span></button></template></ActionMenu><Button variant="primary" :disabled="appState.serviceBusy" @click="handleToggleService"><span class="icon-[mdi--pause] text-[16px]" v-if="appState.serviceRunning" /><span class="icon-[mdi--play] text-[16px]" v-else /><span> {{ appViewState.serviceButtonText }}</span></Button><ActionMenu><template #trigger><Button variant="default">更多<span class="icon-[mdi--chevron-down] text-[13px]" /></Button></template><template #items="{ close }"><button type="button" class="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-sm text-[#d4d4d4] transition-colors hover:bg-white/5 disabled:cursor-not-allowed disabled:text-[#5f5f5f]" :disabled="repairingProxy" role="menuitem" @click="() => { close(); void handleRepairProxy(); }"><span class="icon-[mdi--wrench-outline] text-[15px] text-[#a3a3a3]" />{{ repairingProxy ? "修复中..." : "修复代理" }}</button><button type="button" class="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-sm text-[#d4d4d4] transition-colors hover:bg-white/5 disabled:cursor-not-allowed disabled:text-[#5f5f5f]" :disabled="exportingLogs" role="menuitem" @click="() => { close(); void handleExportLogs(); }"><span class="icon-[mdi--archive-arrow-down-outline] text-[15px] text-[#a3a3a3]" />{{ exportingLogs ? "导出中..." : "导出日志 ZIP" }}</button><button type="button" class="flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-sm text-[#d4d4d4] transition-colors hover:bg-white/5" role="menuitem" @click="() => { close(); void handleOpenLogsDirectory(); }"><span class="icon-[mdi--folder-open-outline] text-[15px] text-[#a3a3a3]" />打开日志目录</button></template></ActionMenu></div></div><div v-if="appState.serviceLastError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">{{ appState.serviceLastError }}</div><div v-if="appState.caIncomplete" class="flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"><span class="center-row gap-2"><span class="icon-[mdi--shield-alert-outline] text-[16px]" />本地 CA 异常，本地代理已停用{{ appState.caError ? `：${appState.caError}` : "。" }}</span><Button variant="default" :disabled="repairingCA" @click="handleRepairCA">{{ repairingCA ? "修复中..." : "一键修复" }}</Button></div><div v-if="caAutoRepaired && !caRepairDismissed" class="flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#6b4f1a] bg-[#302512] px-3 py-2 text-sm text-[#f2c66d]"><span class="center-row gap-2"><span class="icon-[mdi--shield-refresh-outline] text-[16px]" />检测到本地 CA 异常，已自动修复，请重启 Cursor 使连接生效。</span><div class="flex items-center gap-2"><Button variant="default" :disabled="restartingCursor" @click="handleRestartCursor({ skipConfirm: true })">{{ restartingCursor ? "重启中..." : "重启 Cursor" }}</Button><button type="button" class="cursor-pointer text-xs text-[#a3a3a3] hover:text-white" @click="caRepairDismissed = true">关闭</button></div></div><div v-if="debugLogsWarningVisible" class="flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#6b4f1a] bg-[#302512] px-3 py-2 text-sm text-[#f2c66d]"><span class="center-row gap-2"><span class="icon-[mdi--database-alert-outline] text-[16px]" />调试日志占用 {{ formatSize(appState.debugLogBytes) }}，建议及时清理。</span><Button variant="default" :disabled="debugLogsClearing" @click="handleClearDebugLogs">{{ debugLogsClearing ? "清理中..." : "清理调试日志" }}</Button></div><div v-if="appState.debugLogUsageError" class="flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b3a1d] bg-[#2a2113] px-3 py-2 text-sm text-[#e0b96a]"><span class="center-row gap-2"><span class="icon-[mdi--alert-circle-outline] text-[16px]" />无法读取调试日志占用：{{ appState.debugLogUsageError }}</span><Button variant="default" :disabled="debugUsageRetrying" @click="handleRetryDebugUsage">{{ debugUsageRetrying ? "重试中..." : "重试" }}</Button></div></div></Card>
    </div>
    <div class="mx-auto min-h-0 w-full max-w-[1400px] flex-1 overflow-y-auto overscroll-contain px-4 pb-4">
      <div class="flex flex-col gap-3">
        <Card><HomeMetricsCard @refresh="handleMetricsRefresh" /></Card>
        <StationSpendCard ref="stationSpendCard" />
      </div>
      <div class="mt-3">
        <DelegationTaskStrip />
      </div>
    </div>
  </div>
</template>

