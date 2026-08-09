<script setup>
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import { useWorkspaceRoot } from "@/composables/useWorkspaceRoot";
import {
  getSkillsMCPScanSnapshot,
  refreshSkillsMCPScan,
  saveSkillsMCPScanConfig,
  readSkillFile,
  saveSkillFile,
  generateSkillSummary,
} from "@/services/clientApi";
import {
  connectMCPRuntimeServer,
  disconnectMCPRuntimeServer,
} from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { applySkillsMCPScanSnapshot, buildSkillsMCPScanConfig } from "@/utils/skillsMcpScanConfig";
import { computed, defineAsyncComponent, onMounted, reactive, ref } from "vue";

// md-editor-v3 体积大（含完整编辑器内核与语法高亮），只在真正打开编辑器弹窗时
// 才异步加载，避免进入 Skills/MCP 设置页就下载解析 2MB+ 依赖。
const MarkdownEditorModal = defineAsyncComponent(async () => {
  const [mod] = await Promise.all([
    import("@/components/ui/MarkdownEditorModal.vue"),
    import("md-editor-v3/lib/style.css"),
  ]);
  return mod;
});

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const message = useMessage();
const { workspaceRoot, initializeWorkspaceRoot } = useWorkspaceRoot();

const TAB_OPTIONS = [
  { id: "skills", label: "Skills" },
  { id: "mcp", label: "MCP" },
];

const STATUS_FILTER_OPTIONS = [
  { value: "all", label: "全部" },
  { value: "enabled", label: "已启用" },
  { value: "disabled", label: "已停用" },
  { value: "error", label: "错误" },
];

const TAB_ID_PREFIX = "skills-mcp-tab";
const TAB_PANEL_ID_PREFIX = "skills-mcp-panel";

const snapshotState = reactive({
  loaded: false,
  loading: false,
  refreshing: false,
  error: "",
  retry: null,
});

const scanEnabledState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const state = reactive({
  activeTab: "skills",
  query: "",
  statusFilter: "all",
  enabled: true,
  skills: [],
  mcpServers: [],
  skillSources: {},
  mcpSources: {},
  enabledSkills: {},
  disabledMcpServers: {},
  skillSummaries: {},
  mcpSummaries: {},
});

const editorState = reactive({
  visible: false,
  mode: "", // "skill-file" | "mcp-summary"
  key: "",
  title: "",
  content: "",
  busy: false,
});

const summaryBatchState = reactive({
  busy: false,
  done: 0,
  total: 0,
  error: "",
});

const skillItemStates = reactive({});
const mcpItemStates = reactive({});
const tabRefs = ref([]);

let configMutationTail = Promise.resolve();
let mcpAttemptSequence = 0;

function setTabRef(element, index) {
  if (element) {
    tabRefs.value[index] = element;
    return;
  }
  delete tabRefs.value[index];
}

function focusTab(index) {
  tabRefs.value[index]?.focus();
}

function tabID(tabID) {
  return `${TAB_ID_PREFIX}-${tabID}`;
}

function tabPanelID(tabID) {
  return `${TAB_PANEL_ID_PREFIX}-${tabID}`;
}

function handleTabKeydown(event, index) {
  if (!TAB_OPTIONS.length) {
    return;
  }

  if (event.key === "ArrowRight") {
    event.preventDefault();
    const nextIndex = (index + 1) % TAB_OPTIONS.length;
    state.activeTab = TAB_OPTIONS[nextIndex].id;
    focusTab(nextIndex);
    return;
  }

  if (event.key === "ArrowLeft") {
    event.preventDefault();
    const nextIndex = (index - 1 + TAB_OPTIONS.length) % TAB_OPTIONS.length;
    state.activeTab = TAB_OPTIONS[nextIndex].id;
    focusTab(nextIndex);
    return;
  }

  if (event.key === "Home") {
    event.preventDefault();
    state.activeTab = TAB_OPTIONS[0].id;
    focusTab(0);
    return;
  }

  if (event.key === "End") {
    event.preventDefault();
    const lastIndex = TAB_OPTIONS.length - 1;
    state.activeTab = TAB_OPTIONS[lastIndex].id;
    focusTab(lastIndex);
  }
}

function normalizeConfigKey(value) {
  return String(value || "").trim().toLowerCase();
}

function createItemState() {
  return reactive({
    busy: false,
    error: "",
    retry: null,
  });
}

function ensureSkillState(key) {
  if (!skillItemStates[key]) {
    skillItemStates[key] = createItemState();
  }
  return skillItemStates[key];
}

function ensureMcpState(key) {
  if (!mcpItemStates[key]) {
    mcpItemStates[key] = createItemState();
  }
  return mcpItemStates[key];
}

function setDisabledMapEntry(map, key, disabled) {
  const next = { ...(map || {}) };
  if (disabled) {
    next[key] = true;
  } else {
    delete next[key];
  }
  return next;
}

function setEnabledMapEntry(map, key, enabled) {
  const next = { ...(map || {}) };
  if (enabled) {
    next[key] = true;
  } else {
    delete next[key];
  }
  return next;
}

function buildScanConfig() {
  return buildSkillsMCPScanConfig(state);
}

function applySnapshot(snapshot) {
  applySkillsMCPScanSnapshot(state, snapshot);
}

async function loadSnapshot({ refresh = false } = {}) {
  snapshotState.retry = () => loadSnapshot({ refresh });
  snapshotState.error = "";
  if (refresh) {
    snapshotState.refreshing = true;
  } else {
    snapshotState.loading = true;
  }

  try {
    const load = async () => {
      const snapshot = refresh
        ? await refreshSkillsMCPScan(workspaceRoot.value)
        : await getSkillsMCPScanSnapshot(workspaceRoot.value);
      applySnapshot(snapshot);
    };
    if (refresh) {
      await runConfigMutation(load);
    } else {
      await load();
    }
    if (refresh) {
      message.success("已重新扫描技能与 MCP 配置");
    }
  } catch (error) {
    snapshotState.error = toUserError(error);
  } finally {
    snapshotState.loaded = true;
    snapshotState.loading = false;
    snapshotState.refreshing = false;
  }
}

function runConfigMutation(task) {
  const queuedTask = configMutationTail.catch(() => {}).then(task);
  configMutationTail = queuedTask.catch(() => {});
  return queuedTask;
}

async function persistScanConfig(autosaveKey) {
  await props.autosave.run(autosaveKey, async () => {
    const result = await saveSkillsMCPScanConfig(buildScanConfig());
    if (result?.ok === false) {
      throw new Error(result.error || "保存失败");
    }
  });
}

async function handleScanEnabledChange(enabled) {
  const previousValue = state.enabled;
  state.enabled = Boolean(enabled);
  scanEnabledState.busy = true;
  scanEnabledState.error = "";
  scanEnabledState.retry = () => handleScanEnabledChange(enabled);

  await runConfigMutation(async () => {
    try {
      await persistScanConfig("skills-mcp.scan-enabled");
      scanEnabledState.error = "";
    } catch (error) {
      state.enabled = previousValue;
      scanEnabledState.error = toUserError(error);
      throw error;
    } finally {
      scanEnabledState.busy = false;
    }
  }).catch(() => {});
}

function isSkillEnabled(name) {
  return Boolean(state.enabledSkills[normalizeConfigKey(name)]);
}

function isMcpEnabled(identifier) {
  return !state.disabledMcpServers[normalizeConfigKey(identifier)];
}

async function handleSkillToggle(skill, enabled) {
  const itemKey = normalizeConfigKey(skill?.name);
  if (!itemKey) {
    return;
  }

  const itemState = ensureSkillState(itemKey);
  const previousValue = isSkillEnabled(skill?.name);
  state.enabledSkills = setEnabledMapEntry(state.enabledSkills, itemKey, enabled);
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => handleSkillToggle(skill, enabled);

  await runConfigMutation(async () => {
    try {
      await persistScanConfig(`skills-mcp.skill.${itemKey}`);
      itemState.error = "";
    } catch (error) {
      state.enabledSkills = setEnabledMapEntry(state.enabledSkills, itemKey, previousValue);
      itemState.error = toUserError(error);
      throw error;
    } finally {
      itemState.busy = false;
    }
  }).catch(() => {});
}

async function handleMcpToggle(server, enabled) {
  const identifier = server?.identifier || server?.name;
  const itemKey = normalizeConfigKey(identifier);
  if (!itemKey) {
    return;
  }

  const itemState = ensureMcpState(itemKey);
  const previousValue = isMcpEnabled(identifier);
  state.disabledMcpServers = setDisabledMapEntry(state.disabledMcpServers, itemKey, !enabled);
  // 启用任一 MCP server 隐含开启扫描总开关，避免 UI 显示已启用而后端扫描仍全部禁用。
  const scanWasOff = !state.enabled;
  if (enabled && scanWasOff) {
    state.enabled = true;
  }
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => handleMcpToggle(server, enabled);

  await runConfigMutation(async () => {
    try {
      await persistScanConfig(`skills-mcp.mcp.${itemKey}`);
      itemState.error = "";
    } catch (error) {
      state.disabledMcpServers = setDisabledMapEntry(state.disabledMcpServers, itemKey, !previousValue);
      if (scanWasOff) {
        state.enabled = false;
      }
      itemState.error = toUserError(error);
      throw error;
    } finally {
      itemState.busy = false;
    }
  }).catch(() => {});
}

async function handleMcpConnection(server) {
  const identifier = String(server?.identifier || server?.name || "").trim();
  const itemKey = normalizeConfigKey(identifier);
  if (!itemKey || !isMcpEnabled(identifier)) {
    return;
  }

  const itemState = ensureMcpState(itemKey);
  if (itemState.busy) {
    return;
  }
  const disconnect = server?.status === "connected";
  const attemptID = `mcp-connect-${Date.now()}-${mcpAttemptSequence += 1}`;
  // 连接隐含开启扫描总开关：快照刷新会用当前 settings 重建 runtime，总开关关闭时刚建立的连接会被清掉。
  const scanWasOff = !state.enabled;
  if (!disconnect && scanWasOff) {
    state.enabled = true;
  }
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => handleMcpConnection(server);

  try {
    if (scanWasOff && !disconnect) {
      await persistScanConfig(`skills-mcp.connect-${itemKey}`);
    }
    if (disconnect) {
      await disconnectMCPRuntimeServer(identifier, workspaceRoot.value);
      message.success("已断开连接");
    } else {
      await connectMCPRuntimeServer(identifier, attemptID, workspaceRoot.value);
      message.success("连接成功");
    }
  } catch (error) {
    if (scanWasOff && !disconnect) {
      state.enabled = false;
    }
    itemState.error = toUserError(error);
  } finally {
    itemState.busy = false;
    await loadSnapshot();
  }
}

function matchesQuery(values) {
  const query = state.query.trim().toLowerCase();
  if (!query) {
    return true;
  }
  return values.some((value) => String(value || "").toLowerCase().includes(query));
}

function matchesStatusFilter({ enabled, error }) {
  if (state.statusFilter === "enabled") {
    return enabled;
  }
  if (state.statusFilter === "disabled") {
    return !enabled;
  }
  if (state.statusFilter === "error") {
    return Boolean(error);
  }
  return true;
}

function formatMcpStatus(status) {
  if (status === "connected") {
    return "已连接";
  }
  if (status === "connecting") {
    return "连接中";
  }
  if (status === "disconnecting") {
    return "断开中";
  }
  return "未连接";
}

// ---------- 编辑与简介 ----------

// skillSummaryOf 返回技能的中文简介（无则空串）。
function skillSummaryOf(name) {
  return state.skillSummaries[normalizeConfigKey(name)] || "";
}

// skillDiagnosticMessage 返回技能的首个诊断消息（无则空串）。
function skillDiagnosticMessage(skill) {
  const first = Array.isArray(skill?.diagnostics) ? skill.diagnostics[0] : null;
  return first?.message || "";
}

// skillInvalidReason 无效技能（目录缺失 SKILL.md 或 manifest 校验失败）的原因文本。
function skillInvalidReason(skill) {
  if (skill?.valid !== false) {
    return "";
  }
  return skillDiagnosticMessage(skill) || "SKILL.md 缺失或无效";
}

// mcpSummaryOf 返回 MCP server 的中文简介（无则空串）。
function mcpSummaryOf(server) {
  return state.mcpSummaries[normalizeConfigKey(server?.identifier || server?.name)] || "";
}

// openSkillEditor 读取技能 SKILL.md 全文并打开编辑弹窗。
async function openSkillEditor(skill) {
  const itemKey = normalizeConfigKey(skill?.name);
  if (!itemKey) {
    return;
  }
  const itemState = ensureSkillState(itemKey);
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => openSkillEditor(skill);

  try {
    const file = await readSkillFile(skill.name, workspaceRoot.value);
    editorState.mode = "skill-file";
    editorState.key = itemKey;
    editorState.title = `编辑 ${skill.name}`;
    editorState.content = file?.content || "";
    editorState.visible = true;
  } catch (error) {
    itemState.error = toUserError(error);
  } finally {
    itemState.busy = false;
  }
}

// saveSkillEditor 将编辑后的 SKILL.md 正文写回原文件，并触发重新扫描。
async function saveSkillEditor() {
  editorState.busy = true;
  try {
    await saveSkillFile(editorState.key, editorState.content, workspaceRoot.value);
    editorState.visible = false;
    message.success("已保存到 SKILL.md");
    await loadSnapshot({ refresh: true });
  } catch (error) {
    message.error(toUserError(error));
  } finally {
    editorState.busy = false;
  }
}

// openMcpSummaryEditor 打开 MCP 简介编辑弹窗（简介存 config，不改工具配置文件）。
function openMcpSummaryEditor(server) {
  const itemKey = normalizeConfigKey(server?.identifier || server?.name);
  if (!itemKey) {
    return;
  }
  editorState.mode = "mcp-summary";
  editorState.key = itemKey;
  editorState.title = `编辑 MCP 简介：${server?.name || server?.identifier}`;
  editorState.content = mcpSummaryOf(server);
  editorState.visible = true;
}

// saveMcpSummaryEditor 将 MCP 简介写回配置持久化。
async function saveMcpSummaryEditor() {
  editorState.busy = true;
  try {
    const next = { ...state.mcpSummaries, [editorState.key]: editorState.content.trim() };
    if (!next[editorState.key]) {
      delete next[editorState.key];
    }
    state.mcpSummaries = next;
    editorState.visible = false;
    await persistScanConfig(`skills-mcp.summary.${editorState.key}`);
    message.success("简介已保存");
  } catch (error) {
    message.error(toUserError(error));
  } finally {
    editorState.busy = false;
  }
}

// handleEditorSave 根据编辑模式分发保存逻辑。
async function handleEditorSave() {
  if (editorState.mode === "skill-file") {
    await saveSkillEditor();
  } else if (editorState.mode === "mcp-summary") {
    await saveMcpSummaryEditor();
  }
}

// generateSummary 为单个 skill/MCP 生成中文简介（后端自动持久化到 config）。
async function generateSummary(kind, key) {
  const itemKey = normalizeConfigKey(key);
  if (!itemKey || summaryBatchState.busy) {
    return;
  }
  const itemState = kind === "skill" ? ensureSkillState(itemKey) : ensureMcpState(itemKey);
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => generateSummary(kind, key);

  try {
    const summary = await generateSkillSummary(kind, key, workspaceRoot.value);
    if (kind === "skill") {
      state.skillSummaries = { ...state.skillSummaries, [itemKey]: summary };
    } else {
      state.mcpSummaries = { ...state.mcpSummaries, [itemKey]: summary };
    }
    message.success("简介已生成并保存");
  } catch (error) {
    itemState.error = toUserError(error);
  } finally {
    itemState.busy = false;
  }
}

// generateAllSummaries 为当前 tab 下所有条目批量生成简介，逐条持久化。
async function generateAllSummaries() {
  if (summaryBatchState.busy) {
    return;
  }
  const kind = state.activeTab === "mcp" ? "mcp" : "skill";
  // 无效技能（目录缺 SKILL.md 或 manifest 无效）无法生成简介，跳过并在结果中提示。
  const items = kind === "mcp"
    ? filteredMcpServers.value
    : filteredSkills.value.filter((skill) => skill?.valid !== false);
  const skippedCount = filteredSkills.value.length - items.length;
  if (!items.length) {
    return;
  }
  summaryBatchState.busy = true;
  summaryBatchState.done = 0;
  summaryBatchState.total = items.length;
  summaryBatchState.error = "";

  try {
    for (const item of items) {
      const key = kind === "mcp" ? (item.identifier || item.name) : item.name;
      const summary = await generateSkillSummary(kind, key, workspaceRoot.value);
      const itemKey = normalizeConfigKey(key);
      if (kind === "skill") {
        state.skillSummaries = { ...state.skillSummaries, [itemKey]: summary };
      } else {
        state.mcpSummaries = { ...state.mcpSummaries, [itemKey]: summary };
      }
      summaryBatchState.done += 1;
    }
    message.success(
      skippedCount > 0
        ? `已为 ${items.length} 个条目生成简介（跳过 ${skippedCount} 个无效技能）`
        : `已为 ${items.length} 个条目生成简介`,
    );
  } catch (error) {
    summaryBatchState.error = toUserError(error);
    message.error(summaryBatchState.error);
  } finally {
    summaryBatchState.busy = false;
  }
}

const filteredSkills = computed(() => state.skills.filter((skill) => {
  const itemState = ensureSkillState(normalizeConfigKey(skill?.name));
  const enabled = isSkillEnabled(skill?.name);
  return matchesQuery([
    skill?.name,
    skill?.source,
    skill?.description,
    skill?.fullPath,
  ]) && matchesStatusFilter({
    enabled,
    // 无效技能（目录缺 SKILL.md 等）归入「错误」筛选，便于定位坏条目。
    error: itemState.error || skillInvalidReason(skill),
  });
}));

const filteredMcpServers = computed(() => state.mcpServers.filter((server) => {
  const identifier = server?.identifier || server?.name;
  const itemState = ensureMcpState(normalizeConfigKey(identifier));
  const enabled = isMcpEnabled(identifier);
  return matchesQuery([
    server?.name,
    server?.identifier,
    server?.source,
    server?.transport,
    server?.command,
    server?.url,
  ]) && matchesStatusFilter({
    enabled,
    error: itemState.error || server?.lastError,
  });
}));

const activeItems = computed(() => (
  state.activeTab === "mcp" ? filteredMcpServers.value : filteredSkills.value
));

const hasResults = computed(() => activeItems.value.length > 0);

onMounted(async () => {
  await initializeWorkspaceRoot();
  await loadSnapshot();
});
</script>

<template>
  <div class="space-y-8">
    <SettingsSection
      title="Skills 与 MCP"
      description="扫描各工具的技能与 MCP 配置，按当前列表即时启用或停用。"
    >
      <div class="space-y-4">
        <div class="flex flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525]/60 p-3">
          <div class="flex flex-wrap items-center gap-3">
            <div
              class="inline-flex rounded-[8px] border border-[#343434] bg-[#202020] p-1"
              role="tablist"
              aria-orientation="horizontal"
              aria-label="Skills 与 MCP 标签"
            >
              <button
                v-for="(tab, index) in TAB_OPTIONS"
                :key="tab.id"
                :ref="(element) => setTabRef(element, index)"
                :id="tabID(tab.id)"
                type="button"
                role="tab"
                class="min-w-[88px] rounded-[6px] px-3 py-1.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
                :class="state.activeTab === tab.id ? 'bg-[#10AD5D]/15 text-[#8ddcb3]' : 'text-[#a3a3a3] hover:text-white'"
                :aria-selected="state.activeTab === tab.id"
                :aria-controls="tabPanelID(tab.id)"
                :tabindex="state.activeTab === tab.id ? 0 : -1"
                @click="state.activeTab = tab.id"
                @keydown="handleTabKeydown($event, index)"
              >
                {{ tab.label }}
              </button>
            </div>

            <div class="ml-auto flex items-center gap-3">
              <button
                type="button"
                class="shrink-0 rounded-[8px] border border-[#343434] bg-[#202020] px-3 py-1.5 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="summaryBatchState.busy || snapshotState.refreshing || snapshotState.loading || !hasResults"
                :title="'为当前列表的所有条目生成中文简介（自动保存到配置）'"
                @click="generateAllSummaries"
              >
                <span v-if="summaryBatchState.busy">
                  生成中 {{ summaryBatchState.done }}/{{ summaryBatchState.total }}...
                </span>
                <span v-else>一键生成简介</span>
              </button>

              <div class="rounded-[8px] border border-[#343434] bg-[#202020] px-3 py-1.5">
                <Switch
                  compact
                  label="扫描"
                  enabled-text="已启用"
                  disabled-text="已停用"
                  :enabled="state.enabled"
                  :busy="scanEnabledState.busy"
                  :disabled="scanEnabledState.busy || snapshotState.refreshing"
                  aria-label="启用扫描"
                  @change="handleScanEnabledChange"
                />
              </div>

              <button
                type="button"
                class="flex h-9 w-9 items-center justify-center rounded-[8px] border border-[#343434] bg-[#202020] text-[#a3a3a3] transition-colors hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="snapshotState.refreshing || snapshotState.loading"
                aria-label="重新扫描"
                title="重新扫描"
                @click="loadSnapshot({ refresh: true })"
              >
                <span
                  class="icon-[mdi--refresh] text-[18px]"
                  :class="snapshotState.refreshing ? 'animate-spin' : ''"
                  aria-hidden="true"
                ></span>
              </button>
            </div>
          </div>

          <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
            <div class="relative min-w-0">
              <span class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[#6f6f6f]">
                <span class="icon-[mdi--magnify] text-[17px]" aria-hidden="true"></span>
              </span>
              <Input
                :model-value="state.query"
                placeholder="搜索名称、来源、标识符或描述"
                class="pl-9"
                aria-label="搜索 Skills 与 MCP"
                @update:model-value="(value) => { state.query = value; }"
              />
            </div>

            <Select
              :model-value="state.statusFilter"
              :options="STATUS_FILTER_OPTIONS"
              aria-label="状态筛选"
              @change="(value) => { state.statusFilter = value; }"
            />
          </div>

          <div
            v-if="scanEnabledState.error || snapshotState.error"
            class="flex flex-wrap items-center gap-3 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#f2a7a7]"
          >
            <span class="min-w-0 flex-1 break-words">
              {{ scanEnabledState.error || snapshotState.error }}
            </span>
            <button
              v-if="scanEnabledState.error"
              type="button"
              class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
              @click="scanEnabledState.retry?.()"
            >
              重试
            </button>
            <button
              v-else-if="snapshotState.error"
              type="button"
              class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
              @click="snapshotState.retry?.()"
            >
              重试
            </button>
          </div>
        </div>

        <p
          v-if="state.activeTab === 'skills'"
          class="border-l-2 border-[#10AD5D] bg-[#173022]/55 px-3 py-2 text-xs leading-5 text-[#a7d8bd]"
        >
          技能默认关闭。启用后，技能只会进入 BYOK 候选池，并不会在每次请求中全部注入；系统会根据当前任务的相关性稀疏激活并注入少量技能，以减少扫描和提示词开销。此开关只控制 BYOK 扫描；Cursor 客户端显式附带的技能仍可能生效。
        </p>

        <div
          class="min-h-[240px] rounded-[8px] border border-[#343434] bg-[#252525]/40"
          :aria-busy="snapshotState.loading || snapshotState.refreshing"
        >
          <div v-if="!snapshotState.loaded || snapshotState.loading" class="flex min-h-[240px] items-center justify-center px-6 text-sm text-[#8f8f8f]">
            正在加载{{ state.activeTab === "mcp" ? " MCP" : " Skills" }} 列表...
          </div>

          <div v-else-if="!hasResults" class="flex min-h-[240px] items-center justify-center px-6 text-center text-sm text-[#8f8f8f]">
            {{ snapshotState.error ? "加载失败，请重试。" : "当前筛选条件下没有可显示的条目。" }}
          </div>

          <div
            v-else-if="state.activeTab === 'skills'"
            :id="tabPanelID('skills')"
            role="tabpanel"
            :aria-labelledby="tabID('skills')"
            tabindex="0"
            class="grid max-h-[480px] grid-cols-1 gap-3 overflow-y-auto overscroll-contain p-3 sm:grid-cols-2"
          >
            <article
              v-for="skill in filteredSkills"
              :key="skill.fullPath || skill.name"
              class="flex min-w-0 flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525]/60 p-3 transition-colors hover:border-[#3f3f3f]"
            >
              <div class="min-w-0 space-y-1">
                <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <h3 class="min-w-0 text-sm font-medium text-white" :title="skill.name">
                    {{ skill.name }}
                  </h3>
                  <span
                    v-if="skill.valid === false"
                    class="rounded-full border border-[#4b1d1d] bg-[#2a1313] px-2 py-0.5 text-[11px] text-[#f2a7a7]"
                    title="该目录缺少有效的 SKILL.md，不会被技能扫描启用"
                  >
                    无效
                  </span>
                  <span class="rounded-full border border-[#3b3b3b] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
                    {{ skill.source || "other" }}
                  </span>
                </div>
                <p class="break-words text-xs leading-5 text-[#8f8f8f]">
                  {{ skill.description || "暂无描述" }}
                </p>
                <div
                  v-if="skillInvalidReason(skill)"
                  class="rounded-[6px] border border-[#4b1d1d] bg-[#2a1313]/60 px-2.5 py-1.5 text-xs leading-5 text-[#f2a7a7]"
                >
                  <span class="mr-1.5 font-medium">无效原因</span>
                  {{ skillInvalidReason(skill) }}
                </div>
                <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[#737373]">
                  <span class="truncate" :title="skill.fullPath">{{ skill.fullPath || "未提供路径" }}</span>
                  <span>{{ isSkillEnabled(skill.name) ? "已启用" : "已停用" }}</span>
                </div>
                <div
                  v-if="skillSummaryOf(skill.name)"
                  class="rounded-[6px] border border-[#2f4a3a] bg-[#16261d]/60 px-2.5 py-1.5 text-xs leading-5 text-[#8ddcb3]"
                >
                  <span class="mr-1.5 font-medium text-[#6ee7a5]">简介</span>
                  {{ skillSummaryOf(skill.name) }}
                </div>
                <div
                  v-if="ensureSkillState(normalizeConfigKey(skill.name)).error"
                  class="flex flex-wrap items-center gap-3 text-xs text-[#f2a7a7]"
                >
                  <span class="min-w-0 flex-1 break-words">
                    {{ ensureSkillState(normalizeConfigKey(skill.name)).error }}
                  </span>
                  <button
                    type="button"
                    class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
                    @click="ensureSkillState(normalizeConfigKey(skill.name)).retry?.()"
                  >
                    重试
                  </button>
                </div>
              </div>

              <div class="mt-auto flex items-center justify-end gap-2">
                <button
                  type="button"
                  class="shrink-0 rounded-[6px] border border-[#3b3b3b] bg-[#202020] px-2.5 py-1 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="skill.valid === false || ensureSkillState(normalizeConfigKey(skill.name)).busy || snapshotState.refreshing"
                  :title="skill.valid === false ? '该目录缺少有效的 SKILL.md，无法编辑' : `编辑技能 ${skill.name}`"
                  :aria-label="`编辑技能 ${skill.name}`"
                  @click="openSkillEditor(skill)"
                >
                  编辑
                </button>
                <button
                  type="button"
                  class="shrink-0 rounded-[6px] border border-[#3b3b3b] bg-[#202020] px-2.5 py-1 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="skill.valid === false || ensureSkillState(normalizeConfigKey(skill.name)).busy || snapshotState.refreshing"
                  :title="skill.valid === false ? '该目录缺少有效的 SKILL.md，无法生成简介' : `为技能 ${skill.name} 生成简介`"
                  :aria-label="`为技能 ${skill.name} 生成简介`"
                  @click="generateSummary('skill', skill.name)"
                >
                  {{ ensureSkillState(normalizeConfigKey(skill.name)).busy ? "生成中..." : "翻译" }}
                </button>
                <Switch
                  compact
                  label=""
                  :enabled="isSkillEnabled(skill.name)"
                  :busy="ensureSkillState(normalizeConfigKey(skill.name)).busy"
                  :disabled="skill.valid === false || ensureSkillState(normalizeConfigKey(skill.name)).busy || snapshotState.refreshing"
                  :aria-label="`切换技能 ${skill.name}`"
                  @change="(value) => handleSkillToggle(skill, value)"
                />
              </div>
            </article>
          </div>

          <div
            v-else
            :id="tabPanelID('mcp')"
            role="tabpanel"
            :aria-labelledby="tabID('mcp')"
            tabindex="0"
            class="grid max-h-[480px] grid-cols-1 gap-3 overflow-y-auto overscroll-contain p-3 sm:grid-cols-2"
          >
            <article
              v-for="server in filteredMcpServers"
              :key="server.identifier || server.name"
              class="flex min-w-0 flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#252525]/60 p-3 transition-colors hover:border-[#3f3f3f]"
            >
              <div class="min-w-0 space-y-1">
                <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <h3 class="min-w-0 text-sm font-medium text-white" :title="server.name || server.identifier">
                    {{ server.name || server.identifier }}
                  </h3>
                  <span class="rounded-full border border-[#3b3b3b] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
                    {{ server.transport || "stdio" }}
                  </span>
                </div>
                <p class="break-all text-xs leading-5 text-[#8f8f8f]">
                  {{ server.identifier || "未提供标识符" }}
                </p>
                <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[#737373]">
                  <span v-if="server.command" class="break-all" :title="server.command">命令：{{ server.command }}</span>
                  <span v-else-if="server.url" class="break-all" :title="server.url">地址：{{ server.url }}</span>
                  <span>{{ formatMcpStatus(server.status) }}</span>
                  <span v-if="typeof server.toolCount === 'number'">{{ server.toolCount }} 工具</span>
                  <span>{{ isMcpEnabled(server.identifier || server.name) ? "已启用" : "已停用" }}</span>
                </div>
                <div
                  v-if="mcpSummaryOf(server)"
                  class="rounded-[6px] border border-[#2f4a3a] bg-[#16261d]/60 px-2.5 py-1.5 text-xs leading-5 text-[#8ddcb3]"
                >
                  <span class="mr-1.5 font-medium text-[#6ee7a5]">简介</span>
                  {{ mcpSummaryOf(server) }}
                </div>
                <div
                  v-if="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).error || server.lastError"
                  class="flex flex-wrap items-center gap-3 text-xs text-[#f2a7a7]"
                >
                  <span class="min-w-0 flex-1 break-words">
                    {{ ensureMcpState(normalizeConfigKey(server.identifier || server.name)).error || server.lastError }}
                  </span>
                  <button
                    v-if="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).error"
                    type="button"
                    class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
                    @click="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).retry?.()"
                  >
                    重试
                  </button>
                </div>
              </div>

              <div class="mt-auto flex items-center justify-end gap-2">
                <button
                  type="button"
                  class="shrink-0 rounded-[6px] border border-[#3b3b3b] bg-[#202020] px-2.5 py-1 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy || snapshotState.refreshing"
                  :aria-label="`编辑 MCP ${server.name || server.identifier} 简介`"
                  @click="openMcpSummaryEditor(server)"
                >
                  编辑
                </button>
                <button
                  type="button"
                  class="shrink-0 rounded-[6px] border border-[#3b3b3b] bg-[#202020] px-2.5 py-1 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy || snapshotState.refreshing"
                  :aria-label="`为 MCP ${server.name || server.identifier} 生成简介`"
                  @click="generateSummary('mcp', server.identifier || server.name)"
                >
                  {{ ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy ? "生成中..." : "翻译" }}
                </button>
                <Switch
                  compact
                  label=""
                  :enabled="isMcpEnabled(server.identifier || server.name)"
                  :busy="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy"
                  :disabled="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy || snapshotState.refreshing"
                  :aria-label="`切换 MCP ${server.name || server.identifier}`"
                  @change="(value) => handleMcpToggle(server, value)"
                />
                <button
                  type="button"
                  class="shrink-0 rounded-[6px] border border-[#3b3b3b] bg-[#202020] px-2.5 py-1 text-xs text-[#c7c7c7] transition-colors hover:border-[#10AD5D]/60 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-50"
                  :disabled="!isMcpEnabled(server.identifier || server.name) || ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy || snapshotState.refreshing"
                  :aria-label="server.status === 'connected' ? `断开 MCP ${server.identifier || server.name}` : `连接 MCP ${server.identifier || server.name}`"
                  @click="handleMcpConnection(server)"
                >
                  {{ ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy ? (server.status === "connected" ? "断开中..." : "连接中...") : (server.status === "connected" ? "断开" : "连接") }}
                </button>
              </div>
            </article>
          </div>
        </div>
      </div>
    </SettingsSection>

    <MarkdownEditorModal v-if="editorState.visible"
      v-model:visible="editorState.visible"
      v-model="editorState.content"
      :title="editorState.title"
      :save-busy="editorState.busy"
      :save-text="editorState.mode === 'skill-file' ? '保存到文件' : '保存'"
      placeholder="支持 Markdown 语法，可切换到预览查看效果。"
      @save="handleEditorSave"
    />
  </div>
</template>
