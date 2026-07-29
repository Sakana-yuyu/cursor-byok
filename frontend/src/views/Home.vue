<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import HomeMetricsCard from "@/components/HomeMetricsCard.vue";
import StationSpendCard from "@/components/StationSpendCard.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { appState, appViewState, openModelConfigWindow, openMetricsDetailWindow, openRequestMetricsWindow, openLocalLogsDirectory, exportLogsAction, saveRoutingMode, syncServiceState, toUserError, toggleService } from "@/state/appState";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const message = useMessage();
const directModeEnabled = computed(() => appState.routingMode === "upstream");
const exportingLogs = ref(false);
async function showActionError(title, error) { await showModal({ title, content: String(error || "服务错误").trim() || "服务错误" }); }
async function handleToggleService() { const result = await toggleService(); if (!result.ok) await showActionError("服务操作失败", result.error); }
async function handleOpenMetricsDetail() { if (isBrowserPreview) return router.push("/metrics-detail"); try { await openMetricsDetailWindow(); } catch (error) { await showActionError("打开失败", toUserError(error)); } }
async function handleOpenRequestMetrics() { if (isBrowserPreview) return router.push("/request-metrics"); try { await openRequestMetricsWindow(); } catch (error) { await showActionError("打开失败", toUserError(error)); } }
async function handleOpenModelConfig() { if (isBrowserPreview) return router.push("/model-config"); try { await openModelConfigWindow(); } catch (error) { await showActionError("打开失败", toUserError(error)); } }
async function handleExportLogs() { if (exportingLogs.value) return; exportingLogs.value = true; try { const result = await exportLogsAction(); if (!result.ok) { await showActionError("导出失败", result.error); return; } if (await showModal({ title: "导出成功", content: `日志已导出到：${result.path}`, confirmText: "打开目录", cancelText: "关闭" })) await openLocalLogsDirectory(); } finally { exportingLogs.value = false; } }
async function handleDirectModeChange(enabled) { if (enabled && !(await showModal({ title: "开启直连模式", content: "直连模式会绕过本地代理服务，Cursor 将直接连接官方服务，可能产生官方账号计费。确定开启吗？", confirmText: "开启直连", cancelText: "取消" }))) return; const result = await saveRoutingMode(enabled ? "upstream" : "local"); if (!result.ok) { await showActionError("切换失败", result.error); return; } message.success(enabled ? "已切换到直连 Cursor 模式" : "已切换到本地服务模式"); }
onMounted(() => { void syncServiceState().catch(() => {}); });
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overscroll-contain p-4 pt-0 text-[#e5e5e5]">
    <Card><div class="flex flex-col gap-4"><div class="flex flex-wrap items-center justify-between gap-3"><div class="center-row min-w-0 flex-wrap gap-2"><span class="size-2.5 shrink-0 rounded-full" :class="appState.serviceRunning ? 'bg-[#22c55e]' : 'bg-[#737373]'" /><h2 class="text-base font-semibold text-white">{{ appViewState.serviceStatusText }}</h2><span class="rounded-full border border-[#3f3f3f] px-2 py-0.5 text-xs text-[#a3a3a3]">{{ directModeEnabled ? "直连 Cursor" : "本地服务" }}</span><span v-if="appState.serviceRunning && appState.proxyListenAddr" class="center-row gap-1 text-xs text-[#737373]"><span class="icon-[mdi--lan-connect] text-[13px]" />{{ appState.proxyListenAddr }}</span></div><div class="center-row flex-wrap justify-end gap-2"><div class="min-w-[190px] rounded-[7px] border border-[#343434] bg-[#252525]/70 px-2.5 py-1.5"><div class="flex items-center justify-between gap-3"><div class="flex min-w-0 items-center gap-1.5"><span class="text-[12px] font-medium text-white">直连模式</span><button type="button" aria-label="直连模式说明" title="直连模式：绕过本地服务并直接连接官方，可能产生官方账号计费。" class="center-row h-[18px] w-[18px] shrink-0 cursor-help rounded-full text-[#858585] transition-colors hover:text-[#d4d4d4] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/45" @click.stop><span class="icon-[mdi--information-outline] text-[14px]" aria-hidden="true"></span></button></div><Switch compact label="" :enabled="directModeEnabled" :busy="appState.configSaving" :disabled="appState.configSaving" @change="handleDirectModeChange" /></div></div><Button variant="default" :disabled="exportingLogs" @click="handleExportLogs">{{ exportingLogs ? "导出中..." : "导出日志" }}</Button><Button variant="default" @click="handleOpenRequestMetrics">请求明细</Button><Button variant="default" @click="handleOpenModelConfig">模型配置</Button><Button variant="primary" :disabled="appState.serviceBusy" @click="handleToggleService"><span class="icon-[mdi--pause] text-[16px]" v-if="appState.serviceRunning" /><span class="icon-[mdi--play] text-[16px]" v-else /><span> {{ appViewState.serviceButtonText }}</span></Button></div></div><div v-if="appState.serviceLastError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">{{ appState.serviceLastError }}</div><div class="border-t border-[#343434] pt-4"><HomeMetricsCard /><div class="mt-4"><StationSpendCard /></div></div></div></Card>
  </div>
</template>
