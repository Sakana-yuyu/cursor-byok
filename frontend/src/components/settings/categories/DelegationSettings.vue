<script setup>
import DelegationRuntimePanel from "@/components/DelegationRuntimePanel.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import DelegationGlobalControlsPanel from "@/components/settings/delegation/DelegationGlobalControlsPanel.vue";
import DelegationExecutorsPanel from "@/components/settings/delegation/DelegationExecutorsPanel.vue";
import DelegationGroupEditor from "@/components/settings/delegation/DelegationGroupEditor.vue";
import DelegationSupervisionPanel from "@/components/settings/delegation/DelegationSupervisionPanel.vue";
import DelegationVisionPanel from "@/components/settings/delegation/DelegationVisionPanel.vue";
import SubagentProfilesPanel from "@/components/settings/delegation/SubagentProfilesPanel.vue";
import Button from "@/components/ui/Button.vue";
import { showModal } from "@/composables/useModal";
import { getDelegationExecutorSnapshots, refreshDelegationExecutorProbes } from "@/services/clientApi";
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
import { computed, onMounted, reactive, ref, watch } from "vue";

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
const executorState = reactive({ busy: false, error: "", items: [] });
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

async function loadExecutorSnapshots(force = false) {
  if (executorState.busy) return;
  executorState.busy = true;
  executorState.error = "";
  try {
    const items = force ? await refreshDelegationExecutorProbes() : await getDelegationExecutorSnapshots();
    executorState.items = Array.isArray(items) ? items : [];
  } catch (error) {
    executorState.error = toUserError(error);
  } finally {
    executorState.busy = false;
  }
}

function executorConfigIndex(id) {
  return (appState.delegation.executors || []).findIndex((item) => item.id === id);
}

async function saveExecutorPatch(id, patch) {
  const index = executorConfigIndex(id);
  if (index < 0) return;
  const previous = cloneConfigValue(appState.delegation.executors[index]);
  appState.delegation.executors.splice(index, 1, { ...previous, ...patch });
  try {
    await serializeDelegationSave();
    await loadExecutorSnapshots(false);
  } catch (error) {
    const currentIndex = executorConfigIndex(id);
    if (currentIndex >= 0) appState.delegation.executors.splice(currentIndex, 1, previous);
    executorState.error = toUserError(error);
  }
}

function handleExecutorToggle({ id, enabled }) {
  void saveExecutorPatch(id, { enabled: Boolean(enabled) });
}

function handleExecutorPriority({ id, priority }) {
  void saveExecutorPatch(id, { priority });
}

function handleCustomExecutorSave(value) {
  void saveExecutorPatch(value.id, value);
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

onMounted(() => {
  void loadExecutorSnapshots(false);
});
</script>

<template>
  <div class="space-y-8">
    <DelegationGlobalControlsPanel
      :enabled="appState.delegation.enabled"
      :enabled-state="delegationEnabledState"
      :max-concurrency-draft="maxConcurrencyDraft"
      :max-concurrency-state="maxConcurrencyState"
      :config-ready="appState.configReady"
      @change:enabled="handleEnabledChange"
      @retry:enabled="retryState(delegationEnabledState)"
      @update:max-concurrency="handleMaxConcurrencyInput"
      @flush:max-concurrency="flushMaxConcurrency"
      @retry:max-concurrency="retryMaxConcurrency"
    />

    <DelegationExecutorsPanel
      :executors="appState.delegation.executors || []"
      :snapshots="executorState.items"
      :busy="executorState.busy"
      :error="executorState.error"
      @refresh="loadExecutorSnapshots(true)"
      @toggle="handleExecutorToggle"
      @priority="handleExecutorPriority"
      @save-custom="handleCustomExecutorSave"
    />

    <SettingsSection
      title="高级委派"
      description="监督策略和视觉委派属于低频高级配置，默认收起；需要调整时点击此处展开。"
      collapsible
      :default-expanded="false"
    >
      <div class="space-y-8">
        <DelegationSupervisionPanel
          :config="supervisionConfig"
          :number-drafts="supervisionNumberDrafts"
          :field-states="supervisionFieldStates"
          :load-state="supervisionLoadState"
          :save-state="supervisionSaveState"
          :loaded="supervisionLoaded"
          :model-adapters="appState.modelAdapters"
          :worker-group-options="workerGroupOptions"
          @toggle-field="(field, value) => handleSupervisionToggle(field, value)"
          @select-field="(field, value) => handleSupervisionSelect(field, value)"
          @update-limit-draft="(field, value) => handleSupervisionLimitInput(field, value)"
          @queue-limit="(field) => queueSupervisionLimitSave(field)"
          @flush-limit="(field) => flushSupervisionLimit(field)"
          @retry-field="(field) => retrySupervisionField(field)"
          @retry="retrySupervision"
        />

    <DelegationVisionPanel
      :config="visionConfig"
      :field-states="visionFieldStates"
      :load-state="visionLoadState"
      :loaded="visionLoaded"
      :model-adapters="appState.modelAdapters"
      :mode-options="visionModeOptions"
      @toggle-field="(field, value) => handleVisionToggle(field, value)"
      @select-field="(field, value) => handleVisionSelect(field, value)"
      @retry-field="(field) => retryVisionField(field)"
    />
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

    <SubagentProfilesPanel
      :rows="subagentProfileRows"
      :disabled="!appState.configReady"
      @add="addSubagentProfileRow"
      @remove="(index) => removeSubagentProfileRow(index)"
      @flush="flushSubagentProfileRow"
    />

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
