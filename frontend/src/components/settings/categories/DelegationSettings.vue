<script setup>
import DelegationRuntimePanel from "@/components/DelegationRuntimePanel.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import DelegationGroupEditor from "@/components/settings/delegation/DelegationGroupEditor.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import ModelTreeSelect from "@/components/ui/ModelTreeSelect.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { showModal } from "@/composables/useModal";
import { saveDelegationConfig } from "@/services/runtimeControlApi";
import { appState, toUserError } from "@/state/appState";
// 委派配置的纯函数与默认常量已归位 utils/delegationSettings.js，此处 import 保持调用零改动。
import {
  DEFAULT_SUPERVISION,
  clearStateError,
  cloneConfigValue,
  createDraftState,
  createImmediateState,
  groupImmediateAutosaveKey,
  groupNameAutosaveKey,
  normalizeMaxConcurrencyValue,
  normalizeSubagentProfileRows,
  normalizeSupervision,
  normalizeSupervisionLimit,
  normalizeVisionDelegation,
  reconcileSavedObject,
  retryState,
  toggleModel,
  togglePermission,
} from "@/utils/delegationSettings";
import { computed, reactive, ref, watch } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const DELEGATION_ENABLED_KEY = "delegation.enabled";
const DELEGATION_MAX_CONCURRENCY_KEY = "delegation.max-concurrency";
const DELEGATION_SUPERVISION_KEY = "delegation.supervision";
const DELEGATION_VISION_KEY = "delegation.vision";

const DEFAULT_VISION_DELEGATION = {
  enabled: false,
  visionModelID: "",
  mode: "auto",
};

const visionModeOptions = [
  { value: "auto", label: "描述 + OCR（推荐）" },
  { value: "describe", label: "仅画面描述" },
  { value: "ocr", label: "仅文字抄录" },
];

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

const supervisionLoadState = reactive({ busy: false, error: "", retry: null });
const supervisionSaveState = reactive({
  error: "",
  success: false,
});
const supervisionFieldStates = reactive({});
const supervisionConfig = reactive({ ...DEFAULT_SUPERVISION });
const supervisionNumberDrafts = reactive({
  maxCorrections: String(DEFAULT_SUPERVISION.maxCorrections),
  maxRetries: String(DEFAULT_SUPERVISION.maxRetries),
  maxRounds: String(DEFAULT_SUPERVISION.maxRounds),
});
const supervisionLoaded = ref(false);

const visionLoadState = reactive({ busy: false, error: "", retry: null });
const visionFieldStates = reactive({});
const visionConfig = reactive({ ...DEFAULT_VISION_DELEGATION });
const visionLoaded = ref(false);

const groupStates = reactive({});
const groupNameDrafts = reactive({});
const expandedGroupStates = reactive({});
const maxConcurrencyDraft = ref("");
const groupCreating = ref(false);
// 创建组 ID 的递增序号，避免同毫秒内多次点击产生 Date.now() 碰撞。
let groupCreateSeq = 0;

let delegationSaveTail = Promise.resolve();
let maxConcurrencyDraftRevision = 0;
const groupNameDraftRevisions = new Map();

const workerGroupOptions = computed(() => [
  { value: "", label: "自动选择委派组" },
  ...appState.delegation.groups.map((group) => ({
    value: group.id,
    label: group.name || group.id,
  })),
]);

const supervisionSaveError = computed(() => (
  supervisionSaveState.error
  || Object.values(supervisionFieldStates).map((state) => state.error).find(Boolean)
  || ""
));

function supervisionFieldBusy(field) {
  const state = ensureSupervisionFieldState(field);
  return supervisionLoadState.busy || state.busy || state.queued;
}

function ensureSupervisionFieldState(field) {
  if (!supervisionFieldStates[field]) {
    supervisionFieldStates[field] = reactive({ busy: false, queued: false, error: "", retry: null, lastValue: null, revision: 0 });
  }
  return supervisionFieldStates[field];
}

watch(
  () => appState.delegation.maxConcurrency,
  (value) => {
    if (!maxConcurrencyState.busy && !maxConcurrencyState.queued) {
      maxConcurrencyDraft.value = String(value || "");
    }
  },
  { immediate: true },
);

function ensureGroupState(groupID) {
  if (!groupStates[groupID]) {
    groupStates[groupID] = reactive({
      immediate: createImmediateState(),
      name: createDraftState(),
    });
  }

  return groupStates[groupID];
}

function currentGroupIndex(groupID) {
  return appState.delegation.groups.findIndex((group) => group.id === groupID);
}

function getGroupByID(groupID) {
  return appState.delegation.groups.find((group) => group.id === groupID) || null;
}

function normalizeGroupNameDraft(groupID, value) {
  const fallbackIndex = currentGroupIndex(groupID) + 1;
  return String(value || "").trim() || String(`委派模型组 ${fallbackIndex > 0 ? fallbackIndex : 1}`);
}

function groupNameDraftValue(groupID) {
  if (Object.prototype.hasOwnProperty.call(groupNameDrafts, groupID)) {
    return groupNameDrafts[groupID];
  }
  return getGroupByID(groupID)?.name || "";
}

function restoreGroupIDOrder(previousIDs) {
  const groups = appState.delegation.groups;
  const currentByID = new Map(groups.map((group) => [group.id, group]));
  const restored = [];

  for (const groupID of previousIDs) {
    const group = currentByID.get(groupID);
    if (!group) continue;
    restored.push(group);
    currentByID.delete(groupID);
  }

  for (const group of groups) {
    if (!currentByID.has(group.id)) continue;
    restored.push(group);
    currentByID.delete(group.id);
  }

  groups.splice(0, groups.length, ...restored);
}

function reinsertDeletedGroup(group, previousIndex, previousGroupID, nextGroupID) {
  const groups = appState.delegation.groups;
  if (groups.some((item) => item.id === group.id)) return;

  const nextIndex = nextGroupID ? groups.findIndex((item) => item.id === nextGroupID) : -1;
  if (nextIndex >= 0) {
    groups.splice(nextIndex, 0, group);
    return;
  }

  const previousIndexNow = previousGroupID ? groups.findIndex((item) => item.id === previousGroupID) : -1;
  if (previousIndexNow >= 0) {
    groups.splice(previousIndexNow + 1, 0, group);
    return;
  }

  groups.splice(Math.min(previousIndex, groups.length), 0, group);
}

function clearGroupErrors(groupID) {
  const state = ensureGroupState(groupID);
  state.immediate.error = "";
}

function applySupervision(value) {
  const normalized = normalizeSupervision(value);
  Object.assign(supervisionConfig, normalized);
  supervisionNumberDrafts.maxCorrections = String(normalized.maxCorrections);
  supervisionNumberDrafts.maxRetries = String(normalized.maxRetries);
  supervisionNumberDrafts.maxRounds = String(normalized.maxRounds);
  if (appState.delegation) {
    appState.delegation.supervision = { ...normalized };
  }
}

async function loadSupervisionConfig() {
  if (!appState.configReady) {
    return;
  }
  supervisionLoadState.busy = true;
  supervisionLoadState.error = "";
  supervisionLoadState.retry = loadSupervisionConfig;
  try {
    applySupervision(appState.delegation?.supervision);
    supervisionLoaded.value = true;
  } catch (error) {
    supervisionLoadState.error = toUserError(error);
  } finally {
    supervisionLoadState.busy = false;
  }
}

function supervisionForPersistence() {
  const next = { ...supervisionConfig };
  if (
    next.workerGroupID
    && !appState.delegation.groups.some((group) => group.id === next.workerGroupID)
  ) {
    next.workerGroupID = "";
  }
  return next;
}

function delegationSnapshot(value = appState.delegation) {
  const source = value && typeof value === "object" ? value : {};
  return {
    ...cloneConfigValue(source),
    enabled: Boolean(source.enabled),
    maxConcurrency: normalizeMaxConcurrencyValue(source.maxConcurrency, 4),
    groups: Array.isArray(source.groups) ? cloneConfigValue(source.groups) : [],
    supervision: normalizeSupervision(source.supervision),
    visionDelegation: normalizeVisionDelegation(source.visionDelegation),
    subagentProfiles: normalizeSubagentProfileRows(source.subagentProfiles),
  };
}

function reconcileSavedDelegation(savedValue, submitted, current) {
  const saved = delegationSnapshot(savedValue);
  const reconciled = reconcileSavedObject(saved, submitted, current);
  reconciled.groups = Array.isArray(reconciled.groups) ? reconciled.groups : [];
  reconciled.supervision = reconcileSavedObject(
    saved.supervision,
    submitted.supervision,
    current.supervision,
  );
  reconciled.visionDelegation = reconcileSavedObject(
    saved.visionDelegation,
    submitted.visionDelegation,
    current.visionDelegation,
  );
  reconciled.subagentProfiles = normalizeSubagentProfileRows(reconciled.subagentProfiles);
  return delegationSnapshot(reconciled);
}

// --- 子代理角色（本地委派角色覆盖，subagentType → 角色片段） ---
// 覆盖优先于内置注册表；片段留空 = 禁用该类型注入。仅影响本地委派（BYOK worker）路径，
// Cursor 原生子代理由客户端管理。
const subagentProfileRows = ref([]);

function syncSubagentProfileRowsFromState() {
  subagentProfileRows.value = normalizeSubagentProfileRows(appState.delegation?.subagentProfiles);
}

// 初始化时从 appState 同步一次；编辑期 rows 是唯一编辑源（persistSubagentProfiles 写回 appState），
// 不监听外部变化重载，避免用户编辑中其他字段保存触发 reconcile 时输入被重置。
syncSubagentProfileRowsFromState();

function addSubagentProfileRow() {
  subagentProfileRows.value.push({ subagentType: "", promptFragment: "" });
}

function removeSubagentProfileRow(index) {
  subagentProfileRows.value.splice(index, 1);
  void persistSubagentProfiles();
}

// 编辑后保存：把当前行写回 appState 并走既有委派保存通道（autosave + 串行队列）。
async function persistSubagentProfiles() {
  appState.delegation.subagentProfiles = normalizeSubagentProfileRows(subagentProfileRows.value);
  await props.autosave.run("delegation.subagent-profiles", async () => {
    await serializeDelegationSave();
  });
}

// blur/change 时保存对应行（长文本片段避免逐键触发）。
function flushSubagentProfileRow() {
  void persistSubagentProfiles();
}

async function persistDelegationConfig() {
  const previousSupervision = normalizeSupervision(appState.delegation.supervision);
  const previousVision = normalizeVisionDelegation(appState.delegation.visionDelegation);
  appState.delegation.supervision = supervisionForPersistence();
  appState.delegation.visionDelegation = visionForPersistence();
  const submitted = delegationSnapshot();
  try {
    const saved = await saveDelegationConfig(submitted);
    const current = delegationSnapshot();
    current.supervision = supervisionForPersistence();
    current.visionDelegation = visionForPersistence();
    const reconciled = reconcileSavedDelegation(saved, submitted, current);
    appState.delegation = reconciled;
    Object.assign(supervisionConfig, reconciled.supervision);
    Object.assign(visionConfig, reconciled.visionDelegation);
    return reconciled;
  } catch (error) {
    const current = delegationSnapshot();
    current.supervision = supervisionForPersistence();
    current.visionDelegation = visionForPersistence();
    appState.delegation = {
      ...current,
      supervision: normalizeSupervision(reconcileSavedObject(
        previousSupervision,
        submitted.supervision,
        current.supervision,
      )),
      visionDelegation: normalizeVisionDelegation(reconcileSavedObject(
        previousVision,
        submitted.visionDelegation,
        current.visionDelegation,
      )),
    };
    throw error;
  }
}

async function saveSupervisionField(field, value) {
  const state = ensureSupervisionFieldState(field);
  const previous = supervisionConfig[field];
  const revision = state.revision + 1;
  supervisionConfig[field] = value;
  state.revision = revision;
  state.lastValue = value;
  state.retry = () => saveSupervisionField(field, state.lastValue);
  state.error = "";
  supervisionSaveState.error = "";
  supervisionSaveState.success = false;
  state.busy = true;
  try {
    await props.autosave.run(`${DELEGATION_SUPERVISION_KEY}.${field}`, async () => {
      await serializeDelegationSave();
    });
    supervisionSaveState.success = true;
  } catch (error) {
    if (state.revision === revision) {
      supervisionConfig[field] = previous;
      // 不要用 supervisionConfig 整体覆盖——它仍持有其他字段的未提交草稿，
      // 会抹掉 persistDelegationConfig catch 中已完成的精确对账。只回退本字段。
      appState.delegation.supervision = { ...appState.delegation.supervision, [field]: previous };
    }
    state.error = toUserError(error);
    supervisionSaveState.error = state.error;
  } finally {
    state.busy = false;
  }
}

function handleSupervisionToggle(field, value) {
  void saveSupervisionField(field, Boolean(value));
}

function handleSupervisionSelect(field, value) {
  void saveSupervisionField(field, String(value || ""));
}

function handleSupervisionLimitInput(field, value) {
  supervisionNumberDrafts[field] = value;
  supervisionSaveState.error = "";
  supervisionSaveState.success = false;
}

function queueSupervisionLimitSave(field) {
  const state = ensureSupervisionFieldState(field);
  state.queued = true;
  props.autosave.schedule(
    `${DELEGATION_SUPERVISION_KEY}.${field}`,
    async () => {
      state.queued = false;
      const next = normalizeSupervisionLimit(field, supervisionNumberDrafts[field]);
      supervisionNumberDrafts[field] = String(next);
      await saveSupervisionField(field, next);
    },
    { debounceMs: 350 },
  );
}

async function flushSupervisionLimit(field) {
  try {
    await props.autosave.flush(`${DELEGATION_SUPERVISION_KEY}.${field}`);
  } catch (_error) {
    // 错误已由监督策略区域展示。
  }
}

function retrySupervision(field = "") {
  if (supervisionLoadState.error && typeof supervisionLoadState.retry === "function") {
    void supervisionLoadState.retry();
    return;
  }
  if (field) {
    retrySupervisionField(field);
    return;
  }
  const failedField = Object.keys(supervisionFieldStates).find((field) => supervisionFieldStates[field].error);
  if (failedField) {
    const state = supervisionFieldStates[failedField];
    if (typeof state.retry === "function") {
      void state.retry();
    }
  }
}

function supervisionFieldError(field) {
  return ensureSupervisionFieldState(field).error;
}

function retrySupervisionField(field) {
  const state = ensureSupervisionFieldState(field);
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

// ---- 视觉委派（vision delegation）----

function applyVisionDelegation(value) {
  const normalized = normalizeVisionDelegation(value);
  Object.assign(visionConfig, normalized);
  if (appState.delegation) {
    appState.delegation.visionDelegation = { ...normalized };
  }
}

async function loadVisionDelegationConfig() {
  if (!appState.configReady) {
    return;
  }
  visionLoadState.busy = true;
  visionLoadState.error = "";
  visionLoadState.retry = loadVisionDelegationConfig;
  try {
    applyVisionDelegation(appState.delegation?.visionDelegation);
    visionLoaded.value = true;
  } catch (error) {
    visionLoadState.error = toUserError(error);
  } finally {
    visionLoadState.busy = false;
  }
}

function visionForPersistence() {
  const next = { ...visionConfig };
  if (next.visionModelID && !appState.modelAdapters.some((adapter) => adapter.id === next.visionModelID)) {
    next.visionModelID = "";
    next.enabled = false;
  }
  return next;
}

function ensureVisionFieldState(field) {
  if (!visionFieldStates[field]) {
    visionFieldStates[field] = reactive({ busy: false, error: "", retry: null, lastValue: null, revision: 0 });
  }
  return visionFieldStates[field];
}

function visionFieldBusy(field) {
  const state = ensureVisionFieldState(field);
  return visionLoadState.busy || state.busy;
}

function visionFieldError(field) {
  return ensureVisionFieldState(field).error;
}

async function saveVisionField(field, value) {
  const state = ensureVisionFieldState(field);
  const previous = visionConfig[field];
  const revision = state.revision + 1;
  visionConfig[field] = value;
  state.revision = revision;
  state.lastValue = value;
  state.retry = () => saveVisionField(field, state.lastValue);
  state.error = "";
  state.busy = true;
  try {
    await props.autosave.run(`${DELEGATION_VISION_KEY}.${field}`, async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    if (state.revision === revision) {
      visionConfig[field] = previous;
      // 与 saveSupervisionField 同理：仅回退本字段，避免抹掉 persistDelegationConfig
      // catch 中的精确对账结果。
      appState.delegation.visionDelegation = { ...appState.delegation.visionDelegation, [field]: previous };
    }
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

function handleVisionToggle(field, value) {
  void saveVisionField(field, Boolean(value));
}

function handleVisionSelect(field, value) {
  void saveVisionField(field, String(value || ""));
}

function retryVisionField(field) {
  const state = ensureVisionFieldState(field);
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

async function serializeDelegationSave(save = persistDelegationConfig) {
  const pendingSave = delegationSaveTail.catch(() => {}).then(save);
  delegationSaveTail = pendingSave.catch(() => {});
  return pendingSave;
}

async function runImmediateSave(key, state, rollback = null) {
  clearStateError(state);
  state.busy = true;
  try {
    await props.autosave.run(key, async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    if (typeof rollback === "function") {
      rollback();
    }
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

async function handleEnabledChange(enabled) {
  if (!appState.configReady) {
    return;
  }
  const previous = Boolean(appState.delegation.enabled);
  const next = Boolean(enabled);
  appState.delegation.enabled = next;
  delegationEnabledState.retry = () => handleEnabledChange(next);
  await runImmediateSave(DELEGATION_ENABLED_KEY, delegationEnabledState, () => {
    appState.delegation.enabled = previous;
  });
}

function queueMaxConcurrencySave() {
  maxConcurrencyState.error = "";
  maxConcurrencyState.queued = true;
  props.autosave.schedule(
    DELEGATION_MAX_CONCURRENCY_KEY,
    async () => {
      maxConcurrencyState.queued = false;
      maxConcurrencyState.busy = true;
      const draftValue = maxConcurrencyDraft.value;
      const draftRevision = maxConcurrencyDraftRevision;
      try {
        await serializeDelegationSave(async () => {
          const previousValue = appState.delegation.maxConcurrency;
          const nextValue = normalizeMaxConcurrencyValue(draftValue, previousValue);
          appState.delegation.maxConcurrency = nextValue;
          try {
            await persistDelegationConfig();
            if (maxConcurrencyDraftRevision === draftRevision) {
              maxConcurrencyDraft.value = String(nextValue);
            }
          } catch (error) {
            appState.delegation.maxConcurrency = previousValue;
            throw error;
          }
        });
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
  if (!appState.configReady) {
    return;
  }
  maxConcurrencyDraft.value = value;
  maxConcurrencyDraftRevision += 1;
  maxConcurrencyState.error = "";
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

async function persistGroupImmediate(groupID, action, retryAction = null, rollback = null) {
  const state = ensureGroupState(groupID).immediate;
  state.retry = retryAction || (() => persistGroupImmediate(groupID, action));
  await runImmediateSave(groupImmediateAutosaveKey(groupID, action), state, rollback);
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
      const draftValue = groupNameDraftValue(groupID);
      const draftRevision = groupNameDraftRevisions.get(groupID) || 0;
      try {
        await serializeDelegationSave(async () => {
          const group = getGroupByID(groupID);
          if (!group) return;

          const previousName = group.name;
          const nextName = normalizeGroupNameDraft(groupID, draftValue);
          group.name = nextName;
          try {
            await persistDelegationConfig();
            if ((groupNameDraftRevisions.get(groupID) || 0) === draftRevision) {
              groupNameDrafts[groupID] = nextName;
            }
          } catch (error) {
            const currentGroup = getGroupByID(groupID);
            if (currentGroup) {
              currentGroup.name = previousName;
            }
            throw error;
          }
        });
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
  if (!getGroupByID(groupID)) return;

  groupNameDrafts[groupID] = value;
  groupNameDraftRevisions.set(groupID, (groupNameDraftRevisions.get(groupID) || 0) + 1);
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
  if (!appState.configReady || groupCreating.value) {
    return;
  }
  groupCreating.value = true;
  // 序号递增确保即使同毫秒内多次调用也得到唯一 ID。
  const id = `delegation-group-${Date.now()}-${++groupCreateSeq}`;
  const nextIndex = appState.delegation.groups.length + 1;
  const group = {
    id,
    name: String(`委派模型组 ${nextIndex}`),
    enabled: true,
    modelIDs: [],
    defaultModelID: "",
    executionMode: "auto",
    toolPermissions: {},
  };
  appState.delegation.groups.push(group);
  expandedGroupStates[group.id] = true;
  clearGroupErrors(group.id);
  void persistGroupImmediate(group.id, "create").finally(() => {
    groupCreating.value = false;
  });
}

function handleGroupEnabledChange(groupID, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  const previous = Boolean(group.enabled);
  const next = Boolean(enabled);
  group.enabled = next;
  clearGroupErrors(groupID);
  void persistGroupImmediate(
    groupID,
    "enabled",
    () => handleGroupEnabledChange(groupID, next),
    () => {
      const current = getGroupByID(groupID);
      if (current) current.enabled = previous;
    },
  );
}

function handleExecutionModeChange(groupID, value) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  const previous = group.executionMode;
  const next = value === "cursor" || value === "local" ? value : "auto";
  group.executionMode = next;
  clearGroupErrors(groupID);
  void persistGroupImmediate(
    groupID,
    "execution-mode",
    () => handleExecutionModeChange(groupID, next),
    () => {
      const current = getGroupByID(groupID);
      if (current) current.executionMode = previous;
    },
  );
}

function handleDefaultModelChange(groupID, value) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  const previous = group.defaultModelID;
  const next = String(value || "");
  group.defaultModelID = next;
  clearGroupErrors(groupID);
  void persistGroupImmediate(
    groupID,
    "default-model",
    () => handleDefaultModelChange(groupID, next),
    () => {
      const current = getGroupByID(groupID);
      if (current) current.defaultModelID = previous;
    },
  );
}

function handleToggleModel(groupID, modelID, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  const previousModelIDs = [...group.modelIDs];
  const previousDefaultModelID = group.defaultModelID;
  const next = Boolean(enabled);
  toggleModel(group, modelID, next);
  clearGroupErrors(groupID);
  void persistGroupImmediate(
    groupID,
    "models",
    () => handleToggleModel(groupID, modelID, next),
    () => {
      const current = getGroupByID(groupID);
      if (!current) return;
      current.modelIDs = [...previousModelIDs];
      current.defaultModelID = previousDefaultModelID;
    },
  );
}

function handleTogglePermission(groupID, permission, enabled) {
  const group = getGroupByID(groupID);
  if (!group) {
    return;
  }

  const previousPermissions = { ...(group.toolPermissions || {}) };
  const next = Boolean(enabled);
  togglePermission(group, permission, next);
  clearGroupErrors(groupID);
  void persistGroupImmediate(
    groupID,
    `permission-${permission.key}`,
    () => handleTogglePermission(groupID, permission, next),
    () => {
      const current = getGroupByID(groupID);
      if (current) current.toolPermissions = { ...previousPermissions };
    },
  );
}

async function handleMoveGroup(groupID, direction) {
  const fromIndex = currentGroupIndex(groupID);
  const toIndex = direction === "up" ? fromIndex - 1 : fromIndex + 1;
  if (fromIndex < 0 || toIndex < 0 || toIndex >= appState.delegation.groups.length) {
    return;
  }

  const previousIDs = appState.delegation.groups.map((group) => group.id);
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
    restoreGroupIDOrder(previousIDs);
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
    content: `确定删除“${group.name || String(`委派模型组 ${groupIndex + 1}`)}”吗？`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) {
    return;
  }

  const previousGroupID = appState.delegation.groups[groupIndex - 1]?.id || "";
  const nextGroupID = appState.delegation.groups[groupIndex + 1]?.id || "";
  const deletedGroup = appState.delegation.groups.splice(groupIndex, 1)[0];
  delete expandedGroupStates[groupID];
  const state = ensureGroupState(groupID).immediate;
  state.retry = () => handleDeleteGroup(groupID);
  clearStateError(state);
  state.busy = true;
  try {
    await props.autosave.run(groupImmediateAutosaveKey(groupID, "delete"), async () => {
      await serializeDelegationSave();
    });
  } catch (error) {
    reinsertDeletedGroup(deletedGroup, groupIndex, previousGroupID, nextGroupID);
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

function isGroupExpanded(groupID) {
  return expandedGroupStates[groupID] === true;
}

function toggleGroupExpanded(groupID) {
  expandedGroupStates[groupID] = !isGroupExpanded(groupID);
}

function retryGroup(groupID) {
  const state = ensureGroupState(groupID);
  if (state.name.error) {
    void retryGroupName(groupID);
    return;
  }
  retryState(state.immediate);
}

watch(
  () => appState.configReady,
  (ready) => {
    if (ready && !supervisionLoaded.value) {
      void loadSupervisionConfig();
    }
    if (ready && !visionLoaded.value) {
      void loadVisionDelegationConfig();
    }
  },
  { immediate: true },
);
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
          :disabled="delegationEnabledState.busy || !appState.configReady"
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
            :disabled="maxConcurrencyState.busy || !appState.configReady"
            aria-label="最大并发数"
            @update:model-value="handleMaxConcurrencyInput"
            @blur="flushMaxConcurrency"
            @keydown.enter.prevent="flushMaxConcurrency"
          />
        </div>
      </SettingsRow>
    </SettingsSection>

    <SettingsSection
      title="高级委派"
      description="监督策略和视觉委派属于低频高级配置，默认收起；需要调整时点击此处展开。"
      collapsible
      :default-expanded="false"
    >
      <div class="space-y-8">
        <SettingsSection
          title="监督策略"
          description="由更强的模型负责规划、检查和纠偏，委派模型负责执行。仅对 Multitask 生效，关闭后保持原有委派流程。"
        >
      <SettingsRow
        label="启用监督委派"
        description="监督模型会检查子任务进度，在发现循环、偏离范围或缺少证据时进行纠偏。"
        :busy="supervisionFieldBusy('enabled')"
        :error="supervisionLoadState.error || supervisionFieldError('enabled')"
        @retry="retrySupervision('enabled')"
      >
        <Switch
          compact
          label=""
          enabled-text="已开启"
          disabled-text="已关闭"
          :enabled="supervisionConfig.enabled"
          :disabled="supervisionFieldBusy('enabled') || !supervisionLoaded"
          aria-label="启用监督委派"
          @change="(value) => handleSupervisionToggle('enabled', value)"
        />
      </SettingsRow>

      <SettingsRow
        label="监督模型"
        description="默认跟随主模型；也可以指定一个更强的已配置模型作为顾问。"
        :busy="supervisionFieldBusy('supervisorModelID')"
        :error="supervisionFieldError('supervisorModelID')"
        @retry="retrySupervisionField('supervisorModelID')"
      >
        <div class="w-[280px] max-w-full">
          <ModelTreeSelect
            :model-value="supervisionConfig.supervisorModelID"
            :adapters="appState.modelAdapters"
            :fallback-option="{ value: '', label: '跟随主模型' }"
            :disabled="supervisionFieldBusy('supervisorModelID') || !supervisionConfig.enabled"
            aria-label="监督模型"
            @change="(value) => handleSupervisionSelect('supervisorModelID', value)"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="复核模型"
        description="监督模型完成初审后使用的复核模型，默认跟随监督模型。"
        :busy="supervisionFieldBusy('reviewerModelID')"
        :error="supervisionFieldError('reviewerModelID')"
        @retry="retrySupervisionField('reviewerModelID')"
      >
        <div class="w-[280px] max-w-full">
          <ModelTreeSelect
            :model-value="supervisionConfig.reviewerModelID"
            :adapters="appState.modelAdapters"
            :fallback-option="{ value: '', label: '跟随 Supervisor' }"
            :disabled="supervisionFieldBusy('reviewerModelID') || !supervisionConfig.enabled"
            aria-label="复核模型"
            @change="(value) => handleSupervisionSelect('reviewerModelID', value)"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="执行模型组"
        description="指定监督模式优先使用的执行组；留空时按现有委派组选择逻辑运行。"
        :busy="supervisionFieldBusy('workerGroupID')"
        :error="supervisionFieldError('workerGroupID')"
        @retry="retrySupervisionField('workerGroupID')"
      >
        <div class="w-[280px] max-w-full">
          <Select
            :model-value="supervisionConfig.workerGroupID"
            :options="workerGroupOptions"
            :disabled="supervisionFieldBusy('workerGroupID') || !supervisionConfig.enabled"
            aria-label="监督执行模型组"
            @change="(value) => handleSupervisionSelect('workerGroupID', value)"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="监督上限"
        description="限制单个子任务可纠偏、重试和循环监督的次数，防止异常任务长期占用资源。"
      >
        <div class="grid w-full max-w-[520px] grid-cols-1 gap-3 lg:grid-cols-3">
          <div class="min-w-0 space-y-1">
            <label class="block space-y-1 text-xs text-[#8f8f8f]">
              <span>最大纠偏</span>
              <Input
                :model-value="supervisionNumberDrafts.maxCorrections"
                type="number"
                min="1"
                :disabled="supervisionFieldBusy('maxCorrections') || !supervisionConfig.enabled"
                aria-label="最大纠偏次数"
                @update:model-value="(value) => handleSupervisionLimitInput('maxCorrections', value)"
                @blur="queueSupervisionLimitSave('maxCorrections'); flushSupervisionLimit('maxCorrections')"
              />
            </label>
            <button
              v-if="supervisionFieldError('maxCorrections')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('maxCorrections')"
            >
              {{ supervisionFieldError('maxCorrections') }} · 重试
            </button>
          </div>
          <div class="min-w-0 space-y-1">
            <label class="block space-y-1 text-xs text-[#8f8f8f]">
              <span>最大重试</span>
              <Input
                :model-value="supervisionNumberDrafts.maxRetries"
                type="number"
                min="1"
                :disabled="supervisionFieldBusy('maxRetries') || !supervisionConfig.enabled"
                aria-label="最大重试次数"
                @update:model-value="(value) => handleSupervisionLimitInput('maxRetries', value)"
                @blur="queueSupervisionLimitSave('maxRetries'); flushSupervisionLimit('maxRetries')"
              />
            </label>
            <button
              v-if="supervisionFieldError('maxRetries')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('maxRetries')"
            >
              {{ supervisionFieldError('maxRetries') }} · 重试
            </button>
          </div>
          <div class="min-w-0 space-y-1">
            <label class="block space-y-1 text-xs text-[#8f8f8f]">
              <span>最大监督轮次</span>
              <Input
                :model-value="supervisionNumberDrafts.maxRounds"
                type="number"
                min="1"
                :disabled="supervisionFieldBusy('maxRounds') || !supervisionConfig.enabled"
                aria-label="最大监督轮次"
                @update:model-value="(value) => handleSupervisionLimitInput('maxRounds', value)"
                @blur="queueSupervisionLimitSave('maxRounds'); flushSupervisionLimit('maxRounds')"
              />
            </label>
            <button
              v-if="supervisionFieldError('maxRounds')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('maxRounds')"
            >
              {{ supervisionFieldError('maxRounds') }} · 重试
            </button>
          </div>
        </div>
      </SettingsRow>

      <SettingsRow
        label="监督处置"
        description="允许监督模型在执行偏离时改派模型、升级复核，或在监督服务不可用时阻止任务继续。"
      >
        <div class="grid w-full max-w-[560px] grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
            <Switch
              compact
              class="w-full"
              label="允许改派"
              :enabled="supervisionConfig.allowReassign"
              :disabled="supervisionFieldBusy('allowReassign') || !supervisionConfig.enabled"
              aria-label="允许监督模型改派任务"
              @change="(value) => handleSupervisionToggle('allowReassign', value)"
            />
            <button
              v-if="supervisionFieldError('allowReassign')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('allowReassign')"
            >
              {{ supervisionFieldError('allowReassign') }} · 重试
            </button>
          </div>
          <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
            <Switch
              compact
              class="w-full"
              label="允许升级"
              :enabled="supervisionConfig.allowEscalate"
              :disabled="supervisionFieldBusy('allowEscalate') || !supervisionConfig.enabled"
              aria-label="允许监督模型升级复核"
              @change="(value) => handleSupervisionToggle('allowEscalate', value)"
            />
            <button
              v-if="supervisionFieldError('allowEscalate')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('allowEscalate')"
            >
              {{ supervisionFieldError('allowEscalate') }} · 重试
            </button>
          </div>
          <div class="min-h-[68px] min-w-0 rounded-[6px] border border-white/8 bg-black/10 px-3 py-2.5">
            <Switch
              compact
              class="w-full"
              label="严格不可用处理"
              :enabled="supervisionConfig.strictUnavailable"
              :disabled="supervisionFieldBusy('strictUnavailable') || !supervisionConfig.enabled"
              aria-label="监督模型不可用时停止任务"
              @change="(value) => handleSupervisionToggle('strictUnavailable', value)"
            />
            <button
              v-if="supervisionFieldError('strictUnavailable')"
              type="button"
              class="text-left text-xs leading-5 text-[#f2a7a7]"
              @click="retrySupervisionField('strictUnavailable')"
            >
              {{ supervisionFieldError('strictUnavailable') }} · 重试
            </button>
          </div>
        </div>
      </SettingsRow>

      <div v-if="supervisionSaveState.success && !supervisionSaveError" class="mt-3 text-xs text-[#10AD5D]">
        监督策略已保存
      </div>
    </SettingsSection>

    <SettingsSection
      title="视觉委派（统一读图入口）"
      description="当主模型不支持识图时，自动把图片转发给已配置的识图模型，返回画面描述和文字（OCR），让纯文本模型也能“看图”。启用后会自动把识图模型的网关地址 / API Key / 模型名同步到读图 MCP（vision-reader）作为兜底：委派失败或不可用时，纯文本主模型仍可通过该 MCP 工具读取图片；无需在单个模型中重复配置读图 MCP。"
    >
      <SettingsRow
        label="启用视觉委派"
        description="开启后，主模型不支持图片输入时，后端会把每张图片委派给下方识图模型，并把识图结果注入回对话；识图失败时自动回退为带图片路径的占位说明，模型可调用已同步的读图 MCP（vision-reader）兜底读取。不需要为当前主模型单独配置读图 MCP。"
        :busy="visionFieldBusy('enabled')"
        :error="visionFieldError('enabled')"
      >
        <Switch
          compact
          label=""
          enabled-text="已开启"
          disabled-text="已关闭"
          :enabled="visionConfig.enabled"
          :disabled="visionFieldBusy('enabled') || !visionLoaded || !visionConfig.visionModelID"
          aria-label="启用视觉委派"
          @change="(value) => handleVisionToggle('enabled', value)"
        />
      </SettingsRow>

      <SettingsRow
        label="识图模型"
        description="选择一个已配置且明确支持视觉输入的模型适配器。连接地址、API Key 和模型标识都来自模型配置页；未选择时视觉委派自动关闭。"
        :busy="visionFieldBusy('visionModelID')"
        :error="visionFieldError('visionModelID')"
        @retry="retryVisionField('visionModelID')"
      >
        <div class="w-[280px] max-w-full">
          <ModelTreeSelect
            :model-value="visionConfig.visionModelID"
            :adapters="appState.modelAdapters"
            :fallback-option="{ value: '', label: '未配置（回退占位文字）' }"
            :disabled="visionFieldBusy('visionModelID') || !visionLoaded"
            aria-label="识图模型"
            @change="(value) => handleVisionSelect('visionModelID', value)"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="识图模式"
        description="识图模型返回内容的形式。描述 + OCR 最通用；仅文字抄录适合票据 / 截图。"
        :busy="visionFieldBusy('mode')"
        :error="visionFieldError('mode')"
        @retry="retryVisionField('mode')"
      >
        <div class="w-[280px] max-w-full">
          <Select
            :model-value="visionConfig.mode"
            :options="visionModeOptions"
            :disabled="visionFieldBusy('mode') || !visionLoaded"
            aria-label="识图模式"
            @change="(value) => handleVisionSelect('mode', value)"
          />
        </div>
      </SettingsRow>
        </SettingsSection>
      </div>
    </SettingsSection>

    <SettingsSection
      title="模型组"
      description="模型组用于划分委派模型、默认模型、执行模式和工具权限。新增、排序、开关和删除会立即保存；需要调整时展开本区。"
      collapsible
      :default-expanded="false"
    >
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="text-sm text-[#8f8f8f]">
          当前共 {{ appState.delegation.groups.length }} 个模型组
        </div>
        <Button variant="default" :disabled="!appState.configReady || groupCreating" @click="handleAddGroup">
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
          :name-draft="groupNameDraftValue(group.id)"
          :total-groups="appState.delegation.groups.length"
          :model-adapters="appState.modelAdapters"
          :mode-options="modeOptions"
          :permission-groups="permissionGroups"
          :busy="!appState.configReady || groupBusyState(group.id)"
          :queued="groupQueuedState(group.id)"
          :error="groupErrorState(group.id)"
          :expanded="isGroupExpanded(group.id)"
          @update:name="(value) => handleGroupNameInput(group.id, value)"
          @flush:name="flushGroupName(group.id)"
          @toggle:enabled="(value) => handleGroupEnabledChange(group.id, value)"
          @toggle:expanded="toggleGroupExpanded(group.id)"
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
      title="子代理角色"
      description="本地委派（BYOK worker）按 subagent_type 注入的角色约束：自定义片段覆盖内置角色，留空表示对该类型禁用注入；仅影响本地委派路径，Cursor 原生子代理由客户端管理。编辑失焦或增删时自动保存。"
      collapsible
      :default-expanded="false"
    >
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="text-sm text-[#8f8f8f]">
          内置角色：<span class="text-[#a3a3a3]">explore</span>（代码库探索）、<span class="text-[#a3a3a3]">generalPurpose</span>（通用编码）、<span class="text-[#a3a3a3]">browserUse</span>（浏览器自动化）——配置后覆盖内置。
        </div>
        <Button variant="default" :disabled="!appState.configReady" @click="addSubagentProfileRow">
          新增角色
        </Button>
      </div>

      <div
        v-if="!subagentProfileRows.length"
        class="rounded-[8px] border border-dashed border-[#444] px-3 py-5 text-sm text-[#858585]"
      >
        尚未配置自定义子代理角色，本地委派使用内置角色。
      </div>

      <div v-else class="space-y-3">
        <div
          v-for="(row, index) in subagentProfileRows"
          :key="index"
          class="rounded-[8px] border border-[#343434] bg-[#232323] p-3"
        >
          <div class="flex items-center justify-between gap-3">
            <label class="w-48 shrink-0 text-xs text-[#a3a3a3]">
              子代理类型
              <input
                v-model="row.subagentType"
                class="mt-1 h-8 w-full rounded-[6px] border border-[#3f3f3f] bg-[#1e1e1e] px-2 text-xs text-white outline-none transition-colors focus:border-[#10AD5D]"
                placeholder="如 explore / generalPurpose"
                @change="flushSubagentProfileRow"
              />
            </label>
            <Button variant="text" class="shrink-0" @click="removeSubagentProfileRow(index)">
              删除
            </Button>
          </div>
          <label class="mt-2 block text-xs text-[#a3a3a3]">
            角色片段（留空 = 禁用注入）
            <textarea
              v-model="row.promptFragment"
              rows="3"
              class="mt-1 w-full resize-y rounded-[6px] border border-[#3f3f3f] bg-[#1e1e1e] px-2 py-1.5 text-xs leading-5 text-white outline-none transition-colors focus:border-[#10AD5D]"
              placeholder="描述该类型子代理的工作方式与约束，将拼接到 Task prompt 之后"
              @change="flushSubagentProfileRow"
            ></textarea>
          </label>
        </div>
      </div>
    </SettingsSection>

    <SettingsSection
      title="运行时状态"
      description="运行中的委派任务和 MCP 运行时连接状态独立轮询，不会阻塞上方的配置保存；需要查看时展开。"
      collapsible
      :default-expanded="false"
    >
      <DelegationRuntimePanel :framed="false" />
    </SettingsSection>
  </div>
</template>
