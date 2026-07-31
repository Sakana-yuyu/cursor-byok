<script setup>
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import {
  getSkillsMCPScanSnapshot,
  refreshSkillsMCPScan,
  saveSkillsMCPScanConfig,
} from "@/services/clientApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const message = useMessage();

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

const WORKSPACE_ROOT = "";
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
  disabledSkills: {},
  disabledMcpServers: {},
});

const skillItemStates = reactive({});
const mcpItemStates = reactive({});
const tabRefs = ref([]);

let configMutationTail = Promise.resolve();

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

function buildScanConfig() {
  return {
    enabled: state.enabled,
    disabledSkills: { ...state.disabledSkills },
    disabledMcpServers: { ...state.disabledMcpServers },
  };
}

function applySnapshot(snapshot) {
  const config = snapshot?.config || {};
  state.skills = Array.isArray(snapshot?.skills) ? snapshot.skills : [];
  state.mcpServers = Array.isArray(snapshot?.mcpServers) ? snapshot.mcpServers : [];
  state.enabled = config.enabled !== false;
  state.disabledSkills = { ...(config.disabledSkills || {}) };
  state.disabledMcpServers = { ...(config.disabledMcpServers || {}) };
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
        ? await refreshSkillsMCPScan(WORKSPACE_ROOT)
        : await getSkillsMCPScanSnapshot(WORKSPACE_ROOT);
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
  return !state.disabledSkills[normalizeConfigKey(name)];
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
  state.disabledSkills = setDisabledMapEntry(state.disabledSkills, itemKey, !enabled);
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => handleSkillToggle(skill, enabled);

  await runConfigMutation(async () => {
    try {
      await persistScanConfig(`skills-mcp.skill.${itemKey}`);
      itemState.error = "";
    } catch (error) {
      state.disabledSkills = setDisabledMapEntry(state.disabledSkills, itemKey, !previousValue);
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
  itemState.busy = true;
  itemState.error = "";
  itemState.retry = () => handleMcpToggle(server, enabled);

  await runConfigMutation(async () => {
    try {
      await persistScanConfig(`skills-mcp.mcp.${itemKey}`);
      itemState.error = "";
    } catch (error) {
      state.disabledMcpServers = setDisabledMapEntry(state.disabledMcpServers, itemKey, !previousValue);
      itemState.error = toUserError(error);
      throw error;
    } finally {
      itemState.busy = false;
    }
  }).catch(() => {});
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
    error: itemState.error,
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

onMounted(() => {
  void loadSnapshot();
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
            class="divide-y divide-[#343434]"
          >
            <article
              v-for="skill in filteredSkills"
              :key="skill.fullPath || skill.name"
              class="grid gap-4 px-4 py-4 md:grid-cols-[minmax(0,1fr)_auto]"
            >
              <div class="min-w-0 space-y-1">
                <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <h3 class="min-w-0 text-sm font-medium text-white" :title="skill.name">
                    {{ skill.name }}
                  </h3>
                  <span class="rounded-full border border-[#3b3b3b] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
                    {{ skill.source || "other" }}
                  </span>
                </div>
                <p class="break-words text-xs leading-5 text-[#8f8f8f]">
                  {{ skill.description || "暂无描述" }}
                </p>
                <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[#737373]">
                  <span class="truncate" :title="skill.fullPath">{{ skill.fullPath || "未提供路径" }}</span>
                  <span>{{ isSkillEnabled(skill.name) ? "已启用" : "已停用" }}</span>
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

              <div class="flex items-start justify-start md:justify-end">
                <Switch
                  compact
                  label=""
                  :enabled="isSkillEnabled(skill.name)"
                  :busy="ensureSkillState(normalizeConfigKey(skill.name)).busy"
                  :disabled="ensureSkillState(normalizeConfigKey(skill.name)).busy || snapshotState.refreshing"
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
            class="divide-y divide-[#343434]"
          >
            <article
              v-for="server in filteredMcpServers"
              :key="server.identifier || server.name"
              class="grid gap-4 px-4 py-4 md:grid-cols-[minmax(0,1fr)_auto]"
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

              <div class="flex items-start justify-start md:justify-end">
                <Switch
                  compact
                  label=""
                  :enabled="isMcpEnabled(server.identifier || server.name)"
                  :busy="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy"
                  :disabled="ensureMcpState(normalizeConfigKey(server.identifier || server.name)).busy || snapshotState.refreshing"
                  :aria-label="`切换 MCP ${server.name || server.identifier}`"
                  @change="(value) => handleMcpToggle(server, value)"
                />
              </div>
            </article>
          </div>
        </div>
      </div>
    </SettingsSection>
  </div>
</template>
