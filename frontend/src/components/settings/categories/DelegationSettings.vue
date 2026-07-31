<script setup>
import DelegationRuntimePanel from "@/components/DelegationRuntimePanel.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import DelegationGroupEditor from "@/components/settings/delegation/DelegationGroupEditor.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Switch from "@/components/ui/Switch.vue";
import { showModal } from "@/composables/useModal";
import { appState, persistUserConfig, toUserError } from "@/state/appState";
import { reactive, ref, watch } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const DELEGATION_ENABLED_KEY = "delegation.enabled";
const DELEGATION_MAX_CONCURRENCY_KEY = "delegation.max-concurrency";

const modeOptions = [
  { value: "auto", label: "自动选择" },
  { value: "cursor", label: "Cursor 子会话" },
  { value: "local", label: "本地子代理" },
];

const permissionGroups = [
  { key: "read", label: "读取与搜索", description: "Read / Grep / Glob / Ls / ReadLints", tools: ["Read", "Grep", "Glob", "Ls", "ReadLints"] },
  { key: "write", label: "写入与编辑", description: "Write / PatchEdit / Delete", tools: ["Write", "PatchEdit", "Delete"] },
  { key: "shell", label: "终端命令", description: "Shell / AwaitShell / WriteShellStdin / ForceBackgroundShell", tools: ["Shell", "AwaitShell", "WriteShellStdin", "ForceBackgroundShell"] },
  { key: "mcp", label: "MCP 工具", description: "CallMcpTool / ListMcpResources / FetchMcpResource", tools: ["CallMcpTool", "ListMcpResources", "FetchMcpResource"] },
  { key: "task", label: "继续委派", description: "Task", tools: ["Task"] },
];

const delegationEnabledState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const maxConcurrencyState = reactive({
  busy: false,
  queued: false,
  error: "",
});

const groupStates = reactive({});
const maxConcurrencyDraft = ref("");

let delegationSaveTail = Promise.resolve();

watch(
  () => appState.delegation.maxConcurrency,
  (value) => {
    if (!maxConcurrencyState.busy && !maxConcurrencyState.queued) {
      maxConcurrencyDraft.value = String(value || "");
    }
  },
  { immediate: true },
);

function createImmediateState() {
  return {
    busy: false,
    error: "",
    retry: null,
  };
}

function createDraftState() {
  return {
    busy: false,
    queued: false,
    error: "",
  };
}

function ensureGroupState(groupID) {
  if (!groupStates[groupID]) {
    groupStates[groupID] = reactive({
      immediate: createImmediateState(),
      name: createDraftState(),
    });
  }

  return groupStates[groupID];
}

function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

function currentGroupIndex(groupID) {
  return appState.delegation.groups.findIndex((group) => group.id === groupID);
}

function getGroupByID(groupID) {
  return appState.delegation.groups.find((group) => group.id === groupID) || null;
}

function groupNameAutosaveKey(groupID) {
  return `delegation.group.${groupID}.name`;
}

function groupImmediateAutosaveKey(groupID, action) {
  return `delegation.group.${groupID}.${action}`;
}

function normalizeGroupName(groupID) {
  const group = getGroupByID(groupID);
  if (!group) {
    return "";
  }

  const fallbackIndex = currentGroupIndex(groupID) + 1;
  const normalizedName = String(group.name || "").trim() || `委派模型组 ${fallbackIndex > 0 ? fallbackIndex : 1}`;
  group.name = normalizedName;
  return normalizedName;
}

function normalizeMaxConcurrencyDraft() {
  const parsed = Number.parseInt(String(maxConcurrencyDraft.value || "").trim(), 10);
  const currentValue = Number(appState.delegation.maxConcurrency || 0);
  const fallback = currentValue > 0 ? currentValue : 4;
  const normalized = Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  appState.delegation.maxConcurrency = normalized;
  maxConcurrencyDraft.value = String(normalized);
  return normalized;
}

function togglePermission(group, permission, enabled) {
  const next = { ...(group.toolPermissions || {}) };
  for (const tool of permission.tools) {
    if (enabled) {
      delete next[tool];
    } else {
      next[tool] = false;
    }
  }
  group.toolPermissions = next;
}

function toggleModel(group, modelID, enabled) {
  const next = new Set(group.modelIDs || []);
  if (enabled) {
    next.add(modelID);
  } else {
    next.delete(modelID);
  }
  group.modelIDs = [...next];
  if (!group.modelIDs.includes(group.defaultModelID)) {
    group.defaultModelID = group.modelIDs[0] || "";
  }
  if (group.modelIDs.length === 0) {
    group.enabled = false;
  }
}

function clearStateError(state) {
  state.error = "";
}

function clearGroupErrors(groupID) {
  const state = ensureGroupState(groupID);
  state.immediate.error = "";
  state.name.error = "";
}

async function serializeDelegationSave() {
  const pendingSave = delegationSaveTail.catch(() => {}).then(async () => {
    const result = await persistUserConfig();
    if (!result?.ok) {
      throw new Error(result?.error || "保存失败");
    }
  });
  delegationSaveTail = pendingSave.catch(() => {});
  return pendingSave;
}

async function runImmediateSave(key, state) {
  clearStateError(state);
  state.busy = true;
  try {
    await props.autosave.run(key, async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

async function handleEnabledChange(enabled) {
  appState.delegation.enabled = Boolean(enabled);
  delegationEnabledState.retry = () => handleEnabledChange(appState.delegation.enabled);
  await runImmediateSave(DELEGATION_ENABLED_KEY, delegationEnabledState);
}

function queueMaxConcurrencySave() {
  maxConcurrencyState.error = "";
  maxConcurrencyState.queued = true;
  props.autosave.schedule(
    DELEGATION_MAX_CONCURRENCY_KEY,
    async () => {
      maxConcurrencyState.queued = false;
      maxConcurrencyState.busy = true;
      try {
        normalizeMaxConcurrencyDraft();
        await serializeDelegationSave();
      } catch (error) {
        maxConcurrencyState.error = toUserError(error);
        throw error;
      } finally {
        maxConcurrencyState.busy = false;
      }
    },
    { debounceMs: 500 },
  );
}

function handleMaxConcurrencyInput(value) {
  maxConcurrencyDraft.value = value;
  maxConcurrencyState.error = "";
  const parsed = Number.parseInt(String(value || "").trim(), 10);
  if (Number.isFinite(parsed) && parsed > 0) {
    appState.delegation.maxConcurrency = parsed;
  }
  queueMaxConcurrencySave();
}

async function flushMaxConcurrency() {
  try {
    await props.autosave.flush(DELEGATION_MAX_CONCURRENCY_KEY);
  } catch (_error) {
    // callback error is already surfaced inline and through the page coordinator
  } finally {
    maxConcurrencyState.queued = false;
  }
}

async function retryMaxConcurrency() {
  maxConcurrencyState.queued = false;
  maxConcurrencyState.error = "";
  try {
    await props.autosave.retry(DELEGATION_MAX_CONCURRENCY_KEY);
  } catch (_error) {
    // retried callback surfaces its own error state
  }
}

async function persistGroupImmediate(groupID, action, retryAction = null) {
  const state = ensureGroupState(groupID).immediate;
  state.retry = retryAction || (() => persistGroupImmediate(groupID, action));
  await runImmediateSave(groupImmediateAutosaveKey(groupID, action), state);
}

function queueGroupNameSave(groupID) {
  const state = ensureGroupState(groupID).name;
  state.error = "";
  state.queued = true;
  props.autosave.schedule(
    groupNameAutosaveKey(groupID),
    async () => {
      state.queued = false;
      state.busy = true;
      try {
        normalizeGroupName(groupID);
        await serializeDelegationSave();
      } catch (error) {
        state.error = toUserError(error);
        throw error;
      } finally {
        state.busy = false;
      }
    },
    { debounceMs: 500 },
  );
}

function handleGroupNameInput(groupID, value) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  group.name = value;
  ensureGroupState(groupID).name.error = "";
  queueGroupNameSave(groupID);
}

async function flushGroupName(groupID) {
  try {
    await props.autosave.flush(groupNameAutosaveKey(groupID));
  } catch (_error) {
    // callback error is already surfaced inline and through the page coordinator
  } finally {
    ensureGroupState(groupID).name.queued = false;
  }
}

async function retryGroupName(groupID) {
  const state = ensureGroupState(groupID).name;
  state.queued = false;
  state.error = "";
  try {
    await props.autosave.retry(groupNameAutosaveKey(groupID));
  } catch (_error) {
    // retried callback surfaces its own error state
  }
}

function handleAddGroup() {
  const nextIndex = appState.delegation.groups.length + 1;
  const group = {
    id: `delegation-group-${Date.now()}`,
    name: `委派模型组 ${nextIndex}`,
    enabled: true,
    modelIDs: [],
    defaultModelID: "",
    executionMode: "auto",
    toolPermissions: {},
  };
  appState.delegation.groups.push(group);
  clearGroupErrors(group.id);
  void persistGroupImmediate(group.id, "create");
}

function handleGroupEnabledChange(groupID, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  group.enabled = Boolean(enabled);
  clearGroupErrors(groupID);
  void persistGroupImmediate(groupID, "enabled");
}

function handleExecutionModeChange(groupID, value) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  group.executionMode = value === "cursor" || value === "local" ? value : "auto";
  clearGroupErrors(groupID);
  void persistGroupImmediate(groupID, "execution-mode");
}

function handleDefaultModelChange(groupID, value) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  group.defaultModelID = String(value || "");
  clearGroupErrors(groupID);
  void persistGroupImmediate(groupID, "default-model");
}

function handleToggleModel(groupID, modelID, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  toggleModel(group, modelID, enabled);
  clearGroupErrors(groupID);
  void persistGroupImmediate(groupID, "models");
}

function handleTogglePermission(groupID, permission, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  togglePermission(group, permission, enabled);
  clearGroupErrors(groupID);
  void persistGroupImmediate(groupID, `permission-${permission.key}`);
}

async function handleMoveGroup(groupID, direction) {
  const fromIndex = currentGroupIndex(groupID);
  const toIndex = direction === "up" ? fromIndex - 1 : fromIndex + 1;
  if (fromIndex < 0 || toIndex < 0 || toIndex >= appState.delegation.groups.length) {
    return;
  }

  const snapshot = [...appState.delegation.groups];
  const moved = appState.delegation.groups.splice(fromIndex, 1)[0];
  appState.delegation.groups.splice(toIndex, 0, moved);
  clearGroupErrors(groupID);
  const state = ensureGroupState(groupID).immediate;
  state.retry = () => handleMoveGroup(groupID, direction);
  clearStateError(state);
  state.busy = true;
  try {
    await props.autosave.run(groupImmediateAutosaveKey(groupID, `move-${direction}`), async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    appState.delegation.groups = snapshot;
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

async function handleDeleteGroup(groupID) {
  const groupIndex = currentGroupIndex(groupID);
  const group = getGroupByID(groupID);
  if (groupIndex < 0 || !group) {
    return;
  }

  const confirmed = await showModal({
    title: "删除模型组",
    content: `确定删除“${group.name || `委派模型组 ${groupIndex + 1}`}”吗？`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) {
    return;
  }

  const snapshot = [...appState.delegation.groups];
  appState.delegation.groups.splice(groupIndex, 1);
  const state = ensureGroupState(groupID).immediate;
  state.retry = () => handleDeleteGroup(groupID);
  clearStateError(state);
  state.busy = true;
  try {
    await props.autosave.run(groupImmediateAutosaveKey(groupID, "delete"), async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    appState.delegation.groups = snapshot;
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

function groupBusyState(groupID) {
  const state = ensureGroupState(groupID);
  return state.immediate.busy || state.name.busy;
}

function groupQueuedState(groupID) {
  return ensureGroupState(groupID).name.queued;
}

function groupErrorState(groupID) {
  const state = ensureGroupState(groupID);
  return state.immediate.error || state.name.error;
}

function retryGroup(groupID) {
  const state = ensureGroupState(groupID);
  if (state.name.error) {
    void retryGroupName(groupID);
    return;
  }
  retryState(state.immediate);
}
</script>

<template>
  <div class="space-y-8">
    <SettingsSection
      title="委派配置"
      description="全局开关和并发限制会立即写回本地配置，页面顶部会显示统一的保存状态。"
    >
      <SettingsRow
        label="启用 Multitask 委派"
        description="使用已配置模型并行处理子任务，失败的子任务不会阻塞其他任务。"
        :busy="delegationEnabledState.busy"
        :error="delegationEnabledState.error"
        @retry="retryState(delegationEnabledState)"
      >
        <Switch
          compact
          label=""
          enabled-text="已开启"
          disabled-text="已关闭"
          :enabled="appState.delegation.enabled"
          :disabled="delegationEnabledState.busy"
          aria-label="启用 Multitask 委派"
          @change="handleEnabledChange"
        />
      </SettingsRow>

      <SettingsRow
        label="最大并发数"
        description="限制同一时刻可运行的委派任务数量。输入后 500ms 自动保存，失焦或回车会立即提交。"
        :busy="maxConcurrencyState.busy || maxConcurrencyState.queued"
        :error="maxConcurrencyState.error"
        @retry="retryMaxConcurrency"
      >
        <div class="w-[220px] max-w-full">
          <Input
            :model-value="maxConcurrencyDraft"
            type="number"
            min="1"
            aria-label="最大并发数"
            @update:model-value="handleMaxConcurrencyInput"
            @blur="flushMaxConcurrency"
            @keydown.enter.prevent="flushMaxConcurrency"
          />
        </div>
      </SettingsRow>
    </SettingsSection>

    <SettingsSection
      title="模型组"
      description="模型组用于划分委派模型、默认模型、执行模式和工具权限。新增、排序、开关和删除会立即保存。"
    >
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="text-sm text-[#8f8f8f]">
          当前共 {{ appState.delegation.groups.length }} 个模型组
        </div>
        <Button variant="default" @click="handleAddGroup">
          新增模型组
        </Button>
      </div>

      <div
        v-if="!appState.delegation.groups.length"
        class="rounded-[8px] border border-dashed border-[#444] px-3 py-5 text-sm text-[#858585]"
      >
        尚未配置委派模型组，请先在模型配置中添加模型适配器。
      </div>

      <div v-else class="space-y-4">
        <DelegationGroupEditor
          v-for="(group, groupIndex) in appState.delegation.groups"
          :key="group.id"
          :group="group"
          :group-index="groupIndex"
          :total-groups="appState.delegation.groups.length"
          :model-adapters="appState.modelAdapters"
          :mode-options="modeOptions"
          :permission-groups="permissionGroups"
          :busy="groupBusyState(group.id)"
          :queued="groupQueuedState(group.id)"
          :error="groupErrorState(group.id)"
          @update:name="(value) => handleGroupNameInput(group.id, value)"
          @flush:name="flushGroupName(group.id)"
          @toggle:enabled="(value) => handleGroupEnabledChange(group.id, value)"
          @change:execution-mode="(value) => handleExecutionModeChange(group.id, value)"
          @change:default-model="(value) => handleDefaultModelChange(group.id, value)"
          @toggle:model="({ modelID, enabled }) => handleToggleModel(group.id, modelID, enabled)"
          @toggle:permission="({ permission, enabled }) => handleTogglePermission(group.id, permission, enabled)"
          @move:up="handleMoveGroup(group.id, 'up')"
          @move:down="handleMoveGroup(group.id, 'down')"
          @delete="handleDeleteGroup(group.id)"
          @retry="retryGroup(group.id)"
        />
      </div>
    </SettingsSection>

    <SettingsSection
      title="运行时状态"
      description="运行中的委派任务和 MCP 运行时连接状态独立轮询，不会阻塞上方的配置保存。"
    >
      <DelegationRuntimePanel :framed="false" />
    </SettingsSection>
  </div>
</template>
