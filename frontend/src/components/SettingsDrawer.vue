<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import Select from "@/components/ui/Select.vue";
import PromptPreviewModal from "@/components/PromptPreviewModal.vue";
import DelegationRuntimePanel from "@/components/DelegationRuntimePanel.vue";
import DelegationSettingsCard from "@/components/DelegationSettingsCard.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  appState,
  getCursorManualPath,
  getStatsOverlayPreferences,
  openConfigWindow,
  saveRoutingMode,
  setStatsOverlayPreferences,
  setCursorManualPath,
  showStatsOverlay,
  hideStatsOverlay,
  toUserError,
} from "@/state/appState";
import {
  getPromptInjectionSettings,
  refreshPromptInjection,
  refreshPromptInjectionCatalog,
  savePromptInjectionSettings,
  getSkillsMCPScanSnapshot,
  refreshSkillsMCPScan,
  saveSkillsMCPScanConfig,
  detectCursorPath,
} from "@/services/clientApi";
import { computed, onMounted, reactive, ref, watch } from "vue";

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
const closeActionOptions = [
  { value: "tray", label: "隐藏到托盘" },
  { value: "quit", label: "直接退出应用" },
];

const promptInjection = reactive({ enabled: false, softwareChineseEnabled: false, customEnabled: false, customContent: "", mode: "replace", repo: "yynxxxxx/Codex-X", ref: "main", selectedTemplate: "gpt5.5-unrestricted.md", localContent: "", cacheContent: "", cacheAvailable: false, lastUpdated: "", lastError: "", templates: [] });
const promptInjectionBusy = ref(false);
const promptInjectionLoaded = ref(false);
const promptInjectionExpanded = ref(false);
const promptPreview = ref(null);
const overlayPreferences = reactive({ style: "card", alwaysOnTop: true, visible: false, snapCollapse: true, dockLocked: false, closeAction: "tray" });
const cursorLaunch = reactive({ manualPath: "", detectedPath: "", busy: false, error: "" });
watch(() => appState.statsOverlayPreferences, (next) => {
  if (next) Object.assign(overlayPreferences, next);
}, { deep: true, flush: "sync" });
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
async function refreshCursorPath() {
  cursorLaunch.busy = true;
  cursorLaunch.error = "";
  try {
    cursorLaunch.detectedPath = await detectCursorPath(cursorLaunch.manualPath) || "";
  } catch (error) {
    cursorLaunch.detectedPath = "";
    cursorLaunch.error = toUserError(error);
  } finally {
    cursorLaunch.busy = false;
  }
}
async function saveCursorPath() {
  cursorLaunch.manualPath = setCursorManualPath(cursorLaunch.manualPath);
  await refreshCursorPath();
  if (cursorLaunch.manualPath && !cursorLaunch.detectedPath) {
    cursorLaunch.error = "手动指定的 Cursor.exe 路径无效，请检查文件是否存在。";
    return;
  }
  message.success(cursorLaunch.manualPath ? "Cursor 路径已保存" : "已恢复自动检测 Cursor");
}
function clearCursorPath() {
  cursorLaunch.manualPath = setCursorManualPath("");
  void refreshCursorPath();
}
async function handleOpenConfig() { try { await openConfigWindow(); } catch (error) { await showModal({ title: "打开设置文件夹失败", content: toUserError(error) }); } }

// Skills & MCP 跨工具扫描：汇总主流编码工具的技能与 MCP 配置，按工具分类展示并可逐项开关。
// 注意：依赖 wails 自动生成的 binding，新增方法需在下次 wails dev/build 后才生效。
const skillsMcp = reactive({ enabled: true, expanded: false, busy: false, loaded: false, skills: [], mcpServers: [], disabledSkills: {}, disabledMcpServers: {}, error: "" });
const skillsMcpSummary = computed(() => {
  if (!skillsMcp.loaded) return "加载中…";
  const skillCount = (skillsMcp.skills || []).length;
  const mcpCount = (skillsMcp.mcpServers || []).length;
  if (skillCount === 0 && mcpCount === 0) return "未发现技能或 MCP";
  return `已发现 ${skillCount} 个技能、${mcpCount} 个 MCP server`;
});
function groupSkillsBySource(skills) {
  const groups = {};
  for (const skill of skills || []) {
    const source = skill.source || "other";
    if (!groups[source]) groups[source] = [];
    groups[source].push(skill);
  }
  return groups;
}
async function loadSkillsMcp() {
  skillsMcp.busy = true; skillsMcp.error = "";
  try {
    const snapshot = await getSkillsMCPScanSnapshot("");
    skillsMcp.skills = Array.isArray(snapshot.skills) ? snapshot.skills : [];
    skillsMcp.mcpServers = Array.isArray(snapshot.mcpServers) ? snapshot.mcpServers : [];
    const cfg = snapshot.config || {};
    skillsMcp.enabled = cfg.enabled !== false;
    skillsMcp.disabledSkills = cfg.disabledSkills || {};
    skillsMcp.disabledMcpServers = cfg.disabledMcpServers || {};
  } catch (error) {
    skillsMcp.error = toUserError(error);
  } finally { skillsMcp.busy = false; skillsMcp.loaded = true; }
}
async function refreshSkillsMcpList() {
  skillsMcp.busy = true; skillsMcp.error = "";
  try {
    const snapshot = await refreshSkillsMCPScan("");
    skillsMcp.skills = Array.isArray(snapshot.skills) ? snapshot.skills : [];
    skillsMcp.mcpServers = Array.isArray(snapshot.mcpServers) ? snapshot.mcpServers : [];
    message.success("已重新扫描技能与 MCP 配置");
  } catch (error) { skillsMcp.error = toUserError(error); } finally { skillsMcp.busy = false; }
}
async function persistSkillsMcpConfig() {
  try {
    await saveSkillsMCPScanConfig({ enabled: skillsMcp.enabled, disabledSkills: skillsMcp.disabledSkills, disabledMcpServers: skillsMcp.disabledMcpServers });
  } catch (error) { skillsMcp.error = toUserError(error); }
}
function toggleSkill(name, enabled) {
  const key = String(name || "").toLowerCase();
  if (!key) return;
  skillsMcp.disabledSkills = { ...skillsMcp.disabledSkills, [key]: !enabled };
  void persistSkillsMcpConfig();
}
function toggleMcpServer(identifier, enabled) {
  const key = String(identifier || "").toLowerCase();
  if (!key) return;
  skillsMcp.disabledMcpServers = { ...skillsMcp.disabledMcpServers, [key]: !enabled };
  void persistSkillsMcpConfig();
}
function isSkillEnabled(name) { return !skillsMcp.disabledSkills[String(name || "").toLowerCase()]; }
function isMcpEnabled(identifier) { return !skillsMcp.disabledMcpServers[String(identifier || "").toLowerCase()]; }

onMounted(() => {
  Object.assign(overlayPreferences, getStatsOverlayPreferences());
  cursorLaunch.manualPath = getCursorManualPath();
  void refreshCursorPath();
  void loadPromptInjection();
  void loadSkillsMcp();
});
</script>

<template>
  <Teleport to="body">
    <Transition enter-active-class="transition duration-200 ease-out" enter-from-class="translate-x-full" enter-to-class="translate-x-0" leave-active-class="transition duration-150 ease-in" leave-from-class="translate-x-0" leave-to-class="translate-x-full">
      <div v-if="true" class="fixed inset-0 z-[10000] bg-black/45" @click.self="emit('close')">
        <aside class="absolute right-0 top-0 flex h-full w-[min(440px,calc(100vw-16px))] flex-col overflow-hidden border-l border-[#383838] bg-[#202020] text-[#e5e5e5] shadow-[-18px_0_40px_rgba(0,0,0,0.35)]" role="dialog" aria-modal="true" aria-label="设置" style="--wails-draggable: no-drag">
          <div class="flex shrink-0 items-center justify-between border-b border-[#363636] px-4 py-3" style="--wails-draggable: no-drag">
            <div><h2 class="text-base font-semibold text-white">设置</h2><div class="mt-0.5 text-xs text-[#858585]">应用偏好与提示词配置</div></div>
            <button type="button" aria-label="关闭设置" title="关闭" class="center-row h-8 w-8 cursor-pointer rounded-[6px] text-xl text-[#999] hover:bg-[#333] hover:text-white" @click="emit('close')">×</button>
          </div>
          <div class="min-h-0 min-w-0 flex-1 space-y-3 overflow-x-hidden overflow-y-auto p-3">
            <Card><div class="space-y-3"><h3 class="text-sm font-medium text-white">浮窗偏好</h3><Select v-model="overlayPreferences.style" :options="overlayStyleOptions" aria-label="浮窗样式" @change="(value) => updateOverlay({ style: value })" /><Select v-model="overlayPreferences.closeAction" :options="closeActionOptions" aria-label="主窗口关闭行为" @change="(value) => updateOverlay({ closeAction: value })" /><Switch label="显示浮窗" description="在桌面显示请求统计浮窗" :enabled="overlayPreferences.visible" @change="updateOverlayVisibility" /><Switch label="窗口置顶" description="让浮窗保持在其他窗口上方" :enabled="overlayPreferences.alwaysOnTop" @change="(value) => updateOverlay({ alwaysOnTop: value })" /><Switch label="贴边自动收缩" description="靠近屏幕边缘时收缩为胶囊" :enabled="overlayPreferences.snapCollapse" @change="(value) => updateOverlay({ snapCollapse: value })" /><Switch label="锁定浮窗" description="锁定为收缩胶囊且不可拖动" :enabled="overlayPreferences.dockLocked" @change="(value) => updateOverlay({ dockLocked: value })" /></div></Card>
            <Card><div class="min-w-0 space-y-3"><h3 class="text-sm font-medium text-white">Cursor 启动</h3><label class="block min-w-0 text-xs text-[#a3a3a3]">手动指定 Cursor.exe 路径<input v-model="cursorLaunch.manualPath" class="mt-1 h-8 w-full min-w-0 rounded border border-white/10 bg-black/20 px-2 text-xs text-white" placeholder="留空则自动检测" /></label><div class="flex flex-wrap items-center gap-2"><Button variant="default" :disabled="cursorLaunch.busy" @click="refreshCursorPath">自动检测</Button><Button variant="default" :disabled="cursorLaunch.busy" @click="saveCursorPath">保存路径</Button><Button variant="default" :disabled="cursorLaunch.busy || !cursorLaunch.manualPath" @click="clearCursorPath">清空手动路径</Button></div><p v-if="cursorLaunch.detectedPath" class="break-all text-xs text-[#6ee7a5]">当前使用：{{ cursorLaunch.detectedPath }}</p><p v-else class="text-xs text-[#858585]">未检测到 Cursor，可填写完整的 Cursor.exe 路径。</p><p v-if="cursorLaunch.error" class="break-words text-xs text-[#fca5a5]">{{ cursorLaunch.error }}</p></div></Card>
            <DelegationSettingsCard />
            <DelegationRuntimePanel compact />
            <Card><div class="min-w-0 space-y-3">
              <div class="flex min-w-0 items-center gap-3">
                <button type="button" class="flex min-w-0 flex-1 items-center gap-3 rounded-[6px] text-left outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35" :aria-expanded="promptInjectionExpanded" @click="promptInjectionExpanded = !promptInjectionExpanded">
                  <span class="icon-[mdi--chevron-right] shrink-0 text-lg text-[#737373] transition-transform" :class="{ 'rotate-90': promptInjectionExpanded }"></span>
                  <span class="min-w-0 flex-1">
                    <span class="block text-sm font-medium text-white">提示词与本地化</span>
                    <span class="mt-1 block truncate text-xs" :class="promptInjection.enabled ? 'text-[#6ee7a5]' : 'text-[#858585]'">{{ promptInjectionSummary }}</span>
                  </span>
                </button>
                <div class="shrink-0 rounded-[8px] border border-white/10 bg-black/20 px-2.5 py-2">
                  <Switch compact label="注入" :enabled="promptInjection.enabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.enabled = value; void savePromptInjection(); }" />
                </div>
              </div>

              <template v-if="promptInjectionExpanded">
                <div v-if="promptInjection.templates.length" class="grid min-w-0 gap-2">
                  <article v-for="template in promptInjection.templates" :key="template.name" class="min-w-0 rounded-[8px] border border-white/10 bg-black/15 p-3">
                    <div class="flex min-w-0 items-start gap-3">
                      <div class="min-w-0 flex-1">
                        <div class="truncate text-xs font-medium text-white" :title="template.name">{{ template.name }}</div>
                        <div class="mt-1 line-clamp-3 whitespace-pre-wrap break-words text-[11px] leading-5 text-[#8f8f8f]">{{ template.content || "暂无内容" }}</div>
                      </div>
                      <div class="shrink-0 rounded-[7px] border border-white/10 bg-[#242424] px-2 py-1.5">
                        <Switch compact label="" :enabled="Boolean(template.enabled)" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => togglePromptTemplate(template, value)" />
                      </div>
                    </div>
                    <div class="mt-2 flex justify-end">
                      <Button variant="default" :disabled="!template.content || promptInjectionBusy" @click="openPromptPreview(template)">查看提示词</Button>
                    </div>
                  </article>
                </div>

                <div class="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
                  <label class="min-w-0 text-xs text-[#a3a3a3]">提示词模式<Select v-model="promptInjection.mode" :options="promptInjectionModeOptions" aria-label="提示词模式" button-class="h-8 w-full min-w-0 text-xs" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label>
                  <label class="min-w-0 text-xs text-[#a3a3a3]">模板文件名<input v-model="promptInjection.selectedTemplate" class="h-8 w-full min-w-0 rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label>
                  <label class="min-w-0 text-xs text-[#a3a3a3]">仓库<input v-model="promptInjection.repo" class="h-8 w-full min-w-0 rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label>
                  <label class="min-w-0 text-xs text-[#a3a3a3]">Ref<input v-model="promptInjection.ref" class="h-8 w-full min-w-0 rounded border border-white/10 bg-black/20 px-2 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" /></label>
                </div>

                <div class="flex min-w-0 flex-col gap-2 text-xs text-[#858585] sm:flex-row sm:items-center sm:justify-between">
                  <span class="min-w-0 break-words">{{ promptInjection.cacheAvailable ? `本地提示词已缓存${promptInjection.lastUpdated ? ` · ${promptInjection.lastUpdated}` : ''}` : '尚未缓存提示词' }}</span>
                  <div class="flex shrink-0 flex-wrap items-center gap-2">
                    <Button v-if="!promptInjection.templates.length && (promptInjection.localContent || promptInjection.cacheContent)" variant="default" :disabled="promptInjectionBusy" @click="openPromptPreview()">查看提示词</Button>
                    <Button variant="default" :disabled="promptInjectionBusy || !promptInjectionLoaded" @click="handleRefreshPromptInjection">拉取最新提示词</Button>
                  </div>
                </div>

                <textarea v-model="promptInjection.customContent" class="h-24 w-full min-w-0 resize-y rounded border border-white/10 bg-black/20 p-2 text-xs text-white" placeholder="输入自定义注入内容，留空则不注入..." :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="savePromptInjection"></textarea>
                <div class="grid gap-2 rounded-[8px] border border-white/10 bg-black/10 p-3">
                  <Switch label="自定义注入" :enabled="promptInjection.customEnabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.customEnabled = value; void savePromptInjection(); }" />
                  <Switch label="软件使用中文化" :enabled="promptInjection.softwareChineseEnabled" :busy="promptInjectionBusy || !promptInjectionLoaded" :disabled="promptInjectionBusy || !promptInjectionLoaded" @change="(value) => { promptInjection.softwareChineseEnabled = value; void savePromptInjection(); }" />
                </div>
                <div v-if="promptInjection.lastError" class="break-words rounded border border-[#4b1d1d] bg-[#2a1313] px-2 py-1 text-xs text-[#fca5a5]">{{ promptInjection.lastError }}</div>
              </template>
            </div></Card>
            <Card><div class="min-w-0 space-y-3">
              <div class="flex min-w-0 items-center gap-3">
                <button type="button" class="flex min-w-0 flex-1 items-center gap-3 rounded-[6px] text-left outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35" :aria-expanded="skillsMcp.expanded" @click="skillsMcp.expanded = !skillsMcp.expanded">
                  <span class="icon-[mdi--chevron-right] shrink-0 text-lg text-[#737373] transition-transform" :class="{ 'rotate-90': skillsMcp.expanded }"></span>
                  <span class="min-w-0 flex-1">
                    <span class="block text-sm font-medium text-white">技能 &amp; MCP（跨工具扫描）</span>
                    <span class="mt-1 block truncate text-xs" :class="skillsMcp.enabled ? 'text-[#6ee7a5]' : 'text-[#858585]'">{{ skillsMcpSummary }}</span>
                  </span>
                </button>
                <div class="shrink-0 rounded-[8px] border border-white/10 bg-black/20 px-2.5 py-2">
                  <Switch compact label="扫描" :enabled="skillsMcp.enabled" :busy="skillsMcp.busy || !skillsMcp.loaded" :disabled="skillsMcp.busy || !skillsMcp.loaded" @change="(value) => { skillsMcp.enabled = value; void persistSkillsMcpConfig(); }" />
                </div>
              </div>

              <template v-if="skillsMcp.expanded">
                <div class="text-[11px] leading-5 text-[#8f8f8f]">自动扫描 Cursor / Claude Code / Codex / ZCode / .agents 等主流工具的技能与 MCP 配置，合并注入到模型上下文，还原原生调用。</div>
                <div class="flex justify-end">
                  <Button variant="default" :disabled="skillsMcp.busy || !skillsMcp.loaded" @click="refreshSkillsMcpList">重新扫描</Button>
                </div>

                <div v-if="skillsMcp.error" class="break-words rounded border border-[#4b1d1d] bg-[#2a1313] px-2 py-1 text-xs text-[#fca5a5]">{{ skillsMcp.error }}</div>

                <div v-if="(skillsMcp.skills || []).length" class="space-y-3">
                  <div class="text-xs font-medium text-[#a3a3a3]">技能</div>
                  <div v-for="(skillsInGroup, source) in groupSkillsBySource(skillsMcp.skills)" :key="source" class="space-y-2">
                    <div class="text-[11px] uppercase tracking-wide text-[#737373]">{{ source }}</div>
                    <article v-for="skill in skillsInGroup" :key="skill.fullPath || skill.name" class="flex min-w-0 items-start gap-3 rounded-[8px] border border-white/10 bg-black/15 p-3">
                      <div class="min-w-0 flex-1">
                        <div class="truncate text-xs font-medium text-white" :title="skill.name">{{ skill.name }}</div>
                        <div class="mt-1 line-clamp-2 break-words text-[11px] leading-5 text-[#8f8f8f]">{{ skill.description || "暂无描述" }}</div>
                      </div>
                      <div class="shrink-0 rounded-[7px] border border-white/10 bg-[#242424] px-2 py-1.5">
                        <Switch compact label="" :enabled="isSkillEnabled(skill.name)" :busy="skillsMcp.busy" :disabled="skillsMcp.busy" @change="(value) => toggleSkill(skill.name, value)" />
                      </div>
                    </article>
                  </div>
                </div>

                <div v-if="(skillsMcp.mcpServers || []).length" class="space-y-2">
                  <div class="text-xs font-medium text-[#a3a3a3]">MCP Servers</div>
                  <article v-for="server in skillsMcp.mcpServers" :key="server.identifier || server.name" class="flex min-w-0 items-start gap-3 rounded-[8px] border border-white/10 bg-black/15 p-3">
                    <div class="min-w-0 flex-1">
                      <div class="truncate text-xs font-medium text-white" :title="server.identifier">{{ server.name || server.identifier }}</div>
                      <div class="mt-1 text-[11px] leading-5 text-[#8f8f8f]">{{ server.transport || "stdio" }}<span v-if="server.command"> · {{ server.command }}</span><span v-if="server.url"> · {{ server.url }}</span></div>
                    </div>
                    <div class="shrink-0 rounded-[7px] border border-white/10 bg-[#242424] px-2 py-1.5">
                      <Switch compact label="" :enabled="isMcpEnabled(server.identifier || server.name)" :busy="skillsMcp.busy" :disabled="skillsMcp.busy" @change="(value) => toggleMcpServer(server.identifier || server.name, value)" />
                    </div>
                  </article>
                </div>

                <div v-if="skillsMcp.loaded && !(skillsMcp.skills || []).length && !(skillsMcp.mcpServers || []).length && !skillsMcp.error" class="text-[11px] leading-5 text-[#8f8f8f]">未发现任何技能或 MCP 配置。请确认对应工具目录存在（如 ~/.cursor/skills、~/.cursor/mcp.json）。</div>
              </template>
            </div></Card>
            <Card><div class="space-y-3"><h3 class="text-sm font-medium text-white">高级连接</h3><Switch label="直连模式" description="绕过本地服务并直接连接官方，可能产生官方账号计费。" :enabled="directModeEnabled" :busy="appState.configSaving" :disabled="appState.configSaving" @change="handleDirectModeChange" /></div></Card>
            <Card><div class="flex items-center justify-between gap-3"><div><h3 class="text-sm font-medium text-white">设置文件夹</h3><div class="mt-1 text-xs text-[#858585]">打开本地配置文件所在目录</div></div><Button variant="default" @click="handleOpenConfig">打开</Button></div></Card>
          </div>
        </aside>
      </div>
    </Transition>
  </Teleport>
  <PromptPreviewModal :visible="Boolean(promptPreview)" :template-name="promptPreview?.templateName || ''" :repo="promptPreview?.repo || ''" :ref-name="promptPreview?.refName || ''" :content="promptPreview?.content || ''" @close="promptPreview = null" />
</template>
