<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import Select from "@/components/ui/Select.vue";
import PromptPreviewModal from "@/components/PromptPreviewModal.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  appState,
  getStatsOverlayPreferences,
  openConfigWindow,
  saveRoutingMode,
  setStatsOverlayPreferences,
  showStatsOverlay,
  hideStatsOverlay,
  toUserError,
} from "@/state/appState";
import {
  getPromptInjectionSettings,
  refreshPromptInjection,
  refreshPromptInjectionCatalog,
  savePromptInjectionSettings,
} from "@/services/clientApi";
import { computed, onMounted, reactive, ref } from "vue";

const emit = defineEmits(["close"]);
const message = useMessage();
const promptInjectionModeOptions = [
  { value: "replace", label: "替换原提示词" },
  { value: "append", label: "追加受管区块" },
];
const overlayStyleOptions = [
  { value: "card", label: "卡片式" },
  { value: "engine", label: "引擎仪表" },
  { value: "orb", label: "球形" },
];

const promptInjection = reactive({ enabled: false, softwareChineseEnabled: false, customEnabled: false, customContent: "", mode: "replace", repo: "yynxxxxx/Codex-X", ref: "main", selectedTemplate: "gpt5.5-unrestricted.md", localContent: "", cacheContent: "", cacheAvailable: false, lastUpdated: "", lastError: "", templates: [] });
const promptInjectionBusy = ref(false);
const promptInjectionLoaded = ref(false);
const promptInjectionExpanded = ref(true);
const promptPreview = ref(null);
const overlayPreferences = reactive({ style: "card", alwaysOnTop: true, visible: false });
const directModeEnabled = computed(() => appState.routingMode === "upstream");
const promptInjectionSummary = computed(() => {
  const enabled = [];
  if (promptInjection.enabled) enabled.push("Codex-X");
  if (promptInjection.customEnabled) enabled.push("自定义");
  if (promptInjection.softwareChineseEnabled) enabled.push("中文化");
  return enabled.length ? `已启用：${enabled.join(" / ")}` : "未启用";
});

function openPromptPreview(template) {
  const content = template?.content || promptInjection.localContent || promptInjection.cacheContent || "";
  if (!content.trim()) return;
  promptPreview.value = { templateName: template?.name || promptInjection.selectedTemplate, repo: promptInjection.repo, refName: promptInjection.ref, content };
}
function applyPromptInjectionStatus(status) {
  const value = status?.config || status || {};
  Object.assign(promptInjection, { enabled: Boolean(value.enabled), softwareChineseEnabled: Boolean(value.softwareChineseEnabled), customEnabled: Boolean(value.customEnabled), customContent: value.customContent || "", mode: value.mode === "append" ? "append" : "replace", repo: value.repo || "yynxxxxx/Codex-X", ref: value.ref || "main", selectedTemplate: value.selectedTemplate || "gpt5.5-unrestricted.md", localContent: value.localContent || value.cacheContent || "", cacheContent: value.cacheContent || value.localContent || "", cacheAvailable: Boolean(status?.cacheAvailable || value.cacheContent || value.localContent), lastUpdated: value.lastUpdated || "", lastError: value.lastError || "", templates: Array.isArray(value.templates) ? value.templates : [] });
}
async function loadPromptInjection() {
  try { applyPromptInjectionStatus(await getPromptInjectionSettings()); } catch (error) { promptInjection.lastError = toUserError(error); } finally { promptInjectionLoaded.value = true; }
}
function buildPromptInjectionConfig() { return { enabled: promptInjection.enabled, softwareChineseEnabled: promptInjection.softwareChineseEnabled, customEnabled: promptInjection.customEnabled, customContent: promptInjection.customContent, mode: promptInjection.mode, repo: promptInjection.repo, ref: promptInjection.ref, selectedTemplate: promptInjection.selectedTemplate, localContent: promptInjection.localContent, cacheContent: promptInjection.cacheContent, templates: promptInjection.templates }; }
async function savePromptInjection() {
  promptInjectionBusy.value = true;
  try { applyPromptInjectionStatus(await savePromptInjectionSettings(buildPromptInjectionConfig())); } catch (error) { promptInjection.lastError = toUserError(error); } finally { promptInjectionBusy.value = false; }
}
async function handleRefreshPromptInjection() {
  promptInjectionBusy.value = true;
  promptInjection.lastError = "";
  try { applyPromptInjectionStatus(await refreshPromptInjectionCatalog()); message.success("提示词清单已更新"); } catch (error) {
    try { applyPromptInjectionStatus(await refreshPromptInjection()); message.success("提示词已更新"); } catch (fallbackError) { await showModal({ title: "拉取提示词失败", content: toUserError(fallbackError || error) }); }
  } finally { promptInjectionBusy.value = false; }
}
async function togglePromptTemplate(template, enabled) { promptInjection.templates = promptInjection.templates.map((item) => item.name === template.name ? { ...item, enabled } : item); await savePromptInjection(); }
async function handleDirectModeChange(enabled) {
  if (enabled && !(await showModal({ title: "开启直连模式", content: "直连模式会绕过本地代理服务，Cursor 将直接连接官方服务，可能产生官方账号计费。确定开启吗？", confirmText: "开启直连", cancelText: "取消" }))) return;
  const result = await saveRoutingMode(enabled ? "upstream" : "local");
  if (!result.ok) { await showModal({ title: "切换失败", content: result.error }); return; }
  message.success(enabled ? "已切换到直连 Cursor 模式" : "已切换到本地服务模式");
}
async function updateOverlay(next) {
  try {
    const preferences = await setStatsOverlayPreferences(next);
    Object.assign(overlayPreferences, preferences);
  } catch (error) {
    await showModal({ title: "浮窗设置失败", content: toUserError(error) });
  }
}
async function updateOverlayVisibility(visible) {
  try {
    const preferences = visible ? await showStatsOverlay() : await hideStatsOverlay();
    Object.assign(overlayPreferences, preferences);
  } catch (error) {
    await showModal({ title: "浮窗设置失败", content: toUserError(error) });
  }
}
async function handleOpenConfig() { try { await openConfigWindow(); } catch (error) { await showModal({ title: "打开设置文件夹失败", content: toUserError(error) }); } }

onMounted(() => { Object.assign(overlayPreferences, getStatsOverlayPreferences()); void loadPromptInjection(); });
</script>

<template>
  <Teleport to="body">
    <Transition enter-active-class="transition duration-200 ease-out" enter-from-class="translate-x-full" enter-to-class="translate-x-0" leave-active-class="transition duration-150 ease-in" leave-from-class="translate-x-0" leave-to-class="translate-x-full">
      <div v-if="true" class="fixed inset-0 z-[10000] bg-black/45" @click.self="emit('close')">
        <aside class="absolute right-0 top-0 flex h-full w-[min(440px,calc(100vw-16px))] flex-col border-l border-[#383838] bg-[#202020] text-[#e5e5e5] shadow-[-18px_0_40px_rgba(0,0,0,0.35)]" role="dialog" aria-modal="true" aria-label="设置" style="--wails-draggable: no-drag">
          <div class="flex shrink-0 items-center justify-between border-b border-[#363636] px-4 py-3" style="--wails-draggable: no-drag">
            <div><h2 class="text-base font-semibold text-white">设置</h2><div class="mt-0.5 text-xs text-[#858585]">应用偏好与提示词配置</div></div>
            <button type="button" aria-label="关闭设置" title="关闭" class="center-row h-8 w-8 cursor-pointer rounded-[6px] text-xl text-[#999] hover:bg-[#333] hover:text-white" @click="emit('close')">×</button>
          </div>
          <div class="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
            <Card><div class="space-y-3"><h3 class="text-sm font-medium text-white">浮窗偏好</h3><Select v-model="overlayPreferences.style" :options="overlayStyleOptions" aria-label="浮窗样式" @change="(value) => updateOverlay({ style: value })" /><Switch label="显示浮窗" description="在桌面显示请求统计浮窗" :enabled="overlayPreferences.visible" @change="updateOverlayVisibility" /><Switch label="窗口置顶" description="让浮窗保持在其他窗口上方" :enabled="overlayPreferences.alwaysOnTop" @change="(value) => updateOverlay({ alwaysOnTop: value })" /></div></Card>
            <Card><div class="space-y-3"><button type="button" class="flex w-full items-center justify-between text-left" @click="promptInjectionExpanded = !promptInjectionExpanded"><div><h3 class="text-sm font-medium text-white">提示词与本地化</h3><div class="mt-1 text-xs text-[#858585]">{{ promptInjectionSummary }}</div></div><div class="flex items-center gap-2"><Switch compact label="" :enabled="promptInjection.enabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.enabled = value; void savePromptInjection(); }" /><span class="icon-[mdi--chevron-right] text-lg text-[#737373] transition-transform" :class="{ 'rotate-90': promptInjectionExpanded }"></span></div></button><template v-if="promptInjectionExpanded"><div v-if="promptInjection.templates.length" class="grid gap-2"> <div v-for="template in promptInjection.templates" :key="template.name" class="border-t border-white/10 pt-2"><div class="flex items-center justify-between gap-2"><span class="truncate text-xs text-white">{{ template.name }}</span><Switch compact label="" :enabled="Boolean(template.enabled)" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => togglePromptTemplate(template, value)" /></div><div class="mt-1 max-h-20 overflow-y-auto whitespace-pre-wrap text-[11px] text-[#858585]">{{ template.content || "暂无内容" }}</div><Button variant="default" :disabled="!template.content || promptInjectionBusy" @click="openPromptPreview(template)">查看提示词</Button></div></div><div class="grid grid-cols-2 gap-2"><label class="text-xs text-[#a3a3a3]">提示词模式<Select v-model="promptInjection.mode" :options="promptInjectionModeOptions" aria-label="提示词模式" button-class="h-8 text-xs" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label><label class="text-xs text-[#a3a3a3]">模板文件名<input v-model="promptInjection.selectedTemplate" class="h-8 w-full rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label><label class="text-xs text-[#a3a3a3]">仓库<input v-model="promptInjection.repo" class="h-8 w-full rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label><label class="text-xs text-[#a3a3a3]">Ref<input v-model="promptInjection.ref" class="h-8 w-full rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label></div><div class="flex items-center justify-between gap-2 text-xs text-[#858585]"><span>{{ promptInjection.cacheAvailable ? `本地提示词已缓存${promptInjection.lastUpdated ? ` · ${promptInjection.lastUpdated}` : ''}` : '尚未缓存提示词' }}</span><div class="center-row gap-2"><Button v-if="!promptInjection.templates.length && (promptInjection.localContent || promptInjection.cacheContent)" variant="default" :disabled="promptInjectionBusy" @click="openPromptPreview()">查看提示词</Button><Button variant="default" :disabled="promptInjectionBusy || !promptInjectionLoaded" @click="handleRefreshPromptInjection">拉取最新提示词</Button></div></div><textarea v-model="promptInjection.customContent" class="h-24 w-full resize-y rounded border border-white/10 bg-black/20 p-2 text-xs text-white" placeholder="输入自定义注入内容，留空则不注入..." :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="savePromptInjection"></textarea><Switch label="自定义注入" :enabled="promptInjection.customEnabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.customEnabled = value; void savePromptInjection(); }" /><Switch label="软件使用中文化" :enabled="promptInjection.softwareChineseEnabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.softwareChineseEnabled = value; void savePromptInjection(); }" /><div v-if="promptInjection.lastError" class="rounded border border-[#4b1d1d] bg-[#2a1313] px-2 py-1 text-xs text-[#fca5a5]">{{ promptInjection.lastError }}</div></template></div></Card>
            <Card><div class="space-y-3"><h3 class="text-sm font-medium text-white">高级连接</h3><Switch label="直连模式" description="绕过本地服务并直接连接官方，可能产生官方账号计费。" :enabled="directModeEnabled" :busy="appState.configSaving" :disabled="appState.configSaving" @change="handleDirectModeChange" /></div></Card>
            <Card><div class="flex items-center justify-between gap-3"><div><h3 class="text-sm font-medium text-white">设置文件夹</h3><div class="mt-1 text-xs text-[#858585]">打开本地配置文件所在目录</div></div><Button variant="default" @click="handleOpenConfig">打开</Button></div></Card>
          </div>
        </aside>
      </div>
    </Transition>
  </Teleport>
  <PromptPreviewModal :visible="Boolean(promptPreview)" :template-name="promptPreview?.templateName || ''" :repo="promptPreview?.repo || ''" :ref-name="promptPreview?.refName || ''" :content="promptPreview?.content || ''" @close="promptPreview = null" />
</template>
