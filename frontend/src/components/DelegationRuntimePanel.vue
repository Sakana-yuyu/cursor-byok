<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import DelegationIconButton from "@/components/settings/delegation/DelegationIconButton.vue";
import { useMessage } from "@/composables/useMessage";
import { usePolling } from "@/composables/usePolling";
import { useWorkspaceRoot } from "@/composables/useWorkspaceRoot";
import {
  cancelDelegationTask,
  cancelMCPRuntimeConnection,
  connectMCPRuntimeServer,
  disconnectMCPRuntimeServer,
  getDelegationTaskSnapshots,
  getMCPRuntimeServers,
} from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, reactive } from "vue";

const props = defineProps({
  compact: { type: Boolean, default: false },
  framed: { type: Boolean, default: true },
});

const message = useMessage();
const { workspaceRoot, initializeWorkspaceRoot, updateWorkspaceRoot } = useWorkspaceRoot();
const taskState = reactive({ busy: false, error: "", items: [] });
const mcpState = reactive({ busy: false, error: "", items: [] });
const cancelingTasks = reactive({});
const mcpActions = reactive({});
const mcpAttempts = reactive({});
const canceledMCPAttempts = reactive({});
const cancelingMCPAttempts = reactive({});
const refreshBusy = computed(() => taskState.busy || mcpState.busy);
let attemptSequence = 0;
let taskGeneration = 0;
let mcpWorkspaceGeneration = 0;
let mcpRefreshSequence = 0;
let activeMCPWorkspaceRoot = workspaceRoot.value.trim();
let disposed = false;

const taskItems = computed(() => [...taskState.items].sort((left, right) => {
  return Number(right.queuedAtUnixMs || 0) - Number(left.queuedAtUnixMs || 0);
}));

const taskStatusLabels = {
  queued: "等待中",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  timed_out: "超时",
};

const mcpStatusLabels = {
  disconnected: "未连接",
  connecting: "连接中...",
  connected: "已连接",
  degraded: "已降级",
  error: "错误",
};

const mcpCapabilityLabels = {
  disconnected: "未检查",
  connecting: "检查中",
  connected: "正常",
  degraded: "降级",
  error: "异常",
};

const executorNames = {
  "claude-code": "Claude Code",
  "codex-cli": "Codex CLI",
  "gemini-cli": "Gemini CLI",
  "kiro-cli": "Kiro CLI",
  "cursor-agent": "Cursor Agent",
  "local-byok": "本地 BYOK",
};

const attemptStatusLabels = { running: "运行中", completed: "已完成", failed: "失败", canceled: "已取消", timed_out: "超时" };

function statusClass(status) {
  if (status === "completed" || status === "connected") return "text-[#6ee7a5]";
  if (status === "failed" || status === "error" || status === "timed_out") return "text-[#fca5a5]";
  if (status === "running" || status === "connecting" || status === "degraded") return "text-[#facc15]";
  return "text-[#a3a3a3]";
}

function supervisionClass(phase) {
  if (phase === "completed") return "text-[#6ee7a5]";
  if (phase === "failed" || phase === "canceled" || phase === "circuit_open") return "text-[#fca5a5]";
  if (phase === "reviewing" || phase === "correcting" || phase === "retrying" || phase === "reassigning" || phase === "checkpointing" || phase === "running" || phase === "escalated") {
    return "text-[#facc15]";
  }
  return "text-[#a3a3a3]";
}

function mcpScopeLabel(server) {
  if (String(server?.runtimeScope || "").startsWith("workspace:") || server?.scope === "workspace") return "工作区范围";
  return "用户范围";
}

function formatLastChecked(value) {
  if (!value) return "未检查";
  const checkedAt = new Date(value);
  if (Number.isNaN(checkedAt.getTime()) || checkedAt.getUTCFullYear() <= 1) return "未检查";
  return new Intl.DateTimeFormat(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(checkedAt);
}

function shortConfigFingerprint(value) {
  return String(value || "").replace(/^sha256:/, "").slice(0, 12);
}

function currentMCPWorkspaceRoot() {
  return activeMCPWorkspaceRoot;
}

function mcpServerStateKey(server) {
  const scope = String(server?.runtimeScope || server?.scope || "user").trim();
  return `${scope}\u0000${String(server?.identifier || "").trim().toLowerCase()}`;
}

function captureMCPAction(server) {
  return {
    identifier: String(server?.identifier || "").trim(),
    runtimeScope: String(server?.runtimeScope || server?.scope || "user").trim(),
    workspaceRoot: currentMCPWorkspaceRoot(),
    workspaceGeneration: mcpWorkspaceGeneration,
    key: mcpServerStateKey(server),
  };
}

function isCurrentMCPAction(action) {
  return action.workspaceGeneration === mcpWorkspaceGeneration && action.workspaceRoot === currentMCPWorkspaceRoot();
}

function clearReactiveRecord(record) {
  for (const key of Object.keys(record)) delete record[key];
}

function formatDuration(milliseconds) {
  const value = Math.max(0, Number(milliseconds || 0));
  if (value < 1000) return `${value} ms`;
  if (value < 60000) return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} s`;
  return `${Math.floor(value / 60000)} min ${Math.floor((value % 60000) / 1000)} s`;
}

function taskSupervisionMeta(task) {
  const parts = [];
  if (task?.workerRole) parts.push(`role:${task.workerRole}`);
  if (task?.supervisionPhase) parts.push(`phase:${task.supervisionPhase}`);
  if (task?.supervisionRound) parts.push(`r:${task.supervisionRound}`);
  if (Number.isFinite(task?.correctionCount)) parts.push(`c:${task.correctionCount}`);
  if (Number.isFinite(task?.retryCount)) parts.push(`rt:${task.retryCount}`);
  if (Number.isFinite(task?.reassignCount)) parts.push(`ra:${task.reassignCount}`);
  if (Number.isFinite(task?.escalateCount)) parts.push(`e:${task.escalateCount}`);
  return parts;
}

function taskStateLabel(task) {
  return task?.supervisionPhase || taskStatusLabels[task?.status] || task?.status;
}

function taskStateClass(task) {
  return task?.supervisionPhase ? supervisionClass(task.supervisionPhase) : statusClass(task?.status);
}

function executionModeLabel(mode) {
  return { auto: "自动选择", cursor: "Cursor 子会话", local: "本地子代理", vision: "视觉委派" }[mode] || mode || "自动选择";
}

function executorLabel(id) {
  const value = String(id || "").trim();
  return executorNames[value] || value;
}

function attemptPath(task) {
  return (Array.isArray(task?.attempts) ? task.attempts : []).map((attempt) => executorLabel(attempt?.executorId)).filter(Boolean).join(" → ");
}

function attemptDuration(attempt) {
  const start = Number(attempt?.startedAtUnixMs || 0);
  const finish = Number(attempt?.finishedAtUnixMs || Date.now());
  return start > 0 ? formatDuration(Math.max(0, finish - start)) : "";
}

function replaceMCPServer(next, expectedKey = "") {
  if (!next?.identifier) return;
  const nextKey = mcpServerStateKey(next);
  if (expectedKey && nextKey !== expectedKey) return;
  const index = mcpState.items.findIndex((item) => mcpServerStateKey(item) === nextKey);
  if (index >= 0) mcpState.items.splice(index, 1, next);
  else mcpState.items.push(next);
}

async function refreshTasks(silent = false) {
  if (disposed || taskState.busy) return;
  const generation = taskGeneration;
  taskState.busy = true;
  if (!silent) taskState.error = "";
  try {
    const items = await getDelegationTaskSnapshots();
    if (disposed || generation !== taskGeneration) return;
    taskState.items = Array.isArray(items) ? items : [];
    taskState.error = "";
  } catch (error) {
    if (disposed) return;
    taskState.error = toUserError(error);
  } finally {
    if (disposed) return;
    taskState.busy = false;
  }
}

async function refreshMCPServers(silent = false) {
  if (disposed) return;
  const workspaceGeneration = mcpWorkspaceGeneration;
  const requestedWorkspaceRoot = currentMCPWorkspaceRoot();
  const refreshSequence = mcpRefreshSequence += 1;
  mcpState.busy = true;
  if (!silent) mcpState.error = "";
  try {
    const items = await getMCPRuntimeServers(requestedWorkspaceRoot);
    if (
      disposed
      || refreshSequence !== mcpRefreshSequence
      || workspaceGeneration !== mcpWorkspaceGeneration
      || requestedWorkspaceRoot !== currentMCPWorkspaceRoot()
    ) return;
    const current = new Map(mcpState.items.map((item) => [mcpServerStateKey(item), item]));
    mcpState.items = (Array.isArray(items) ? items : []).map((item) => {
      const key = mcpServerStateKey(item);
      return mcpActions[key] ? current.get(key) || item : item;
    });
    mcpState.error = "";
  } catch (error) {
    if (
      !disposed
      && refreshSequence === mcpRefreshSequence
      && workspaceGeneration === mcpWorkspaceGeneration
      && requestedWorkspaceRoot === currentMCPWorkspaceRoot()
    ) {
      mcpState.error = toUserError(error);
    }
  } finally {
    if (!disposed && refreshSequence === mcpRefreshSequence) mcpState.busy = false;
  }
}

async function refreshRuntime(silent = false) {
  await Promise.all([refreshTasks(silent), refreshMCPServers(silent)]);
}

async function handleCancelTask(task) {
  if (disposed) return;
  if (!task?.id || cancelingTasks[task.id]) return;
  cancelingTasks[task.id] = true;
  taskGeneration += 1;
  taskState.error = "";
  try {
    const canceled = await cancelDelegationTask(task.id);
    if (!canceled) throw new Error("任务已结束或不存在");
    message.success("已取消");
    taskGeneration += 1;
    await refreshTasks(true);
  } catch (error) {
    taskState.error = toUserError(error);
  } finally {
    delete cancelingTasks[task.id];
  }
}

async function handleConnectMCP(server) {
  if (disposed) return;
  const action = captureMCPAction(server);
  if (!action.identifier || mcpActions[action.key]) return;
  const attemptID = `mcp-connect-${Date.now()}-${attemptSequence += 1}`;
  mcpActions[action.key] = { type: "connecting", token: attemptID };
  mcpAttempts[action.key] = { ...action, attemptID };
  mcpState.error = "";
  try {
    replaceMCPServer({ ...server, status: "connecting", capabilityStatus: "connecting", lastError: "" }, action.key);
    const snapshot = await connectMCPRuntimeServer(action.identifier, attemptID, action.workspaceRoot);
    if (isCurrentMCPAction(action) && mcpServerStateKey(snapshot) === action.key) {
      replaceMCPServer(snapshot, action.key);
      message.success("连接成功");
    }
  } catch (error) {
    if (isCurrentMCPAction(action) && canceledMCPAttempts[action.key] !== attemptID) mcpState.error = toUserError(error);
  } finally {
    const refreshCurrentScope = isCurrentMCPAction(action);
    if (mcpActions[action.key]?.token === attemptID) delete mcpActions[action.key];
    if (mcpAttempts[action.key]?.attemptID === attemptID) delete mcpAttempts[action.key];
    if (cancelingMCPAttempts[action.key] === attemptID) delete cancelingMCPAttempts[action.key];
    if (canceledMCPAttempts[action.key] === attemptID) delete canceledMCPAttempts[action.key];
    if (refreshCurrentScope) await refreshMCPServers(true);
  }
}

async function handleCancelMCP(server) {
  if (disposed) return;
  const key = mcpServerStateKey(server);
  const attempt = mcpAttempts[key];
  if (!attempt?.identifier || !attempt.attemptID || cancelingMCPAttempts[key]) return;
  cancelingMCPAttempts[key] = attempt.attemptID;
  try {
    const canceled = await cancelMCPRuntimeConnection(attempt.identifier, attempt.attemptID);
    if (!canceled) throw new Error("操作失败");
    canceledMCPAttempts[key] = attempt.attemptID;
    if (isCurrentMCPAction(attempt)) message.success("已取消");
  } catch (error) {
    if (isCurrentMCPAction(attempt)) mcpState.error = toUserError(error);
  } finally {
    const refreshCurrentScope = isCurrentMCPAction(attempt);
    if (cancelingMCPAttempts[key] === attempt.attemptID) delete cancelingMCPAttempts[key];
    if (mcpActions[key]?.token === attempt.attemptID) delete mcpActions[key];
    if (mcpAttempts[key]?.attemptID === attempt.attemptID) delete mcpAttempts[key];
    if (refreshCurrentScope) await refreshMCPServers(true);
  }
}

async function handleDisconnectMCP(server) {
  if (disposed) return;
  const action = captureMCPAction(server);
  if (!action.identifier || mcpActions[action.key]) return;
  const actionID = `mcp-disconnect-${Date.now()}-${attemptSequence += 1}`;
  mcpActions[action.key] = { type: "disconnecting", token: actionID };
  mcpState.error = "";
  try {
    const snapshot = await disconnectMCPRuntimeServer(action.identifier, action.workspaceRoot);
    if (isCurrentMCPAction(action) && mcpServerStateKey(snapshot) === action.key) {
      replaceMCPServer(snapshot, action.key);
      message.success("已断开连接");
    }
  } catch (error) {
    if (isCurrentMCPAction(action)) mcpState.error = toUserError(error);
  } finally {
    const refreshCurrentScope = isCurrentMCPAction(action);
    if (mcpActions[action.key]?.token === actionID) delete mcpActions[action.key];
    if (refreshCurrentScope) await refreshMCPServers(true);
  }
}

function handleWorkspaceRootChange() {
  if (disposed) return;
  activeMCPWorkspaceRoot = updateWorkspaceRoot(workspaceRoot.value);
  mcpWorkspaceGeneration += 1;
  mcpRefreshSequence += 1;
  for (const attempt of Object.values(mcpAttempts)) {
    void cancelMCPRuntimeConnection(attempt.identifier, attempt.attemptID);
  }
  clearReactiveRecord(mcpActions);
  clearReactiveRecord(mcpAttempts);
  clearReactiveRecord(canceledMCPAttempts);
  clearReactiveRecord(cancelingMCPAttempts);
  mcpState.items = [];
  mcpState.error = "";
  mcpState.busy = false;
  void refreshMCPServers();
}

const taskPolling = usePolling(() => refreshTasks(true), { intervalMs: 1500, immediate: false, autostart: false });
const mcpPolling = usePolling(() => refreshMCPServers(true), { intervalMs: 5000, immediate: false, autostart: false });

onMounted(async () => {
  activeMCPWorkspaceRoot = await initializeWorkspaceRoot();
  if (disposed) return;
  await refreshRuntime();
  if (disposed) return;
  taskPolling.start();
  mcpPolling.start();
});

onUnmounted(() => {
  disposed = true;
  taskGeneration += 1;
  mcpWorkspaceGeneration += 1;
  mcpRefreshSequence += 1;
  for (const attempt of Object.values(mcpAttempts)) {
    void cancelMCPRuntimeConnection(attempt.identifier, attempt.attemptID);
  }
});
</script>

<template>
  <div class="grid gap-4" :class="props.compact ? 'grid-cols-1' : 'xl:grid-cols-2'">
    <component :is="props.framed ? Card : 'section'">
      <div class="flex h-full min-w-0 flex-col gap-3">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="text-base font-medium text-white">委派任务</h2>
            <div class="mt-1 text-sm text-[#a3a3a3]">查看当前任务状态，并按任务粒度取消正在运行的委派。</div>
          </div>
          <DelegationIconButton
            icon="icon-[mdi--refresh]"
            label="刷新运行时状态"
            :disabled="refreshBusy"
            :spinning="refreshBusy"
            @click="refreshRuntime()"
          />
        </div>
        <div v-if="taskState.error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ taskState.error }}</div>
        <div v-if="!taskItems.length" class="rounded-[6px] border border-dashed border-[#444] px-3 py-5 text-center text-xs text-[#858585]">暂无数据</div>
        <div v-else class="max-h-[320px] space-y-2 overflow-y-auto pr-1">
          <article v-for="task in taskItems" :key="task.id" class="min-w-0 rounded-[6px] border border-white/10 bg-black/15 p-3">
            <div class="flex min-w-0 items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-center gap-2 text-xs">
                  <span class="truncate font-medium text-white" :title="task.modelName || task.modelId">{{ task.modelName || task.modelId || "模型" }}</span>
                  <span class="shrink-0" :class="taskStateClass(task)">{{ taskStateLabel(task) }}</span>
                </div>
                <div class="mt-1 truncate text-[11px] text-[#858585]" :title="task.description || task.id">{{ task.description || task.id }}</div>
                <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-[#a3a3a3]">
                  <span>{{ executionModeLabel(task.executionMode) }}</span>
                  <span v-if="task.executorId">{{ executorLabel(task.executorId) }}</span>
                  <span>{{ formatDuration(task.durationMs) }}</span>
                  <span>{{ task.toolCallCount || 0 }} 工具</span>
                </div>
                <div v-if="task.isSupervised" class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-[#858585]">
                  <span v-for="item in taskSupervisionMeta(task)" :key="item">{{ item }}</span>
                </div>
                <div v-if="task.issueCategory" class="mt-2 line-clamp-2 break-words text-[11px] text-[#facc15]" :title="task.issueCategory">{{ task.issueCategory }}</div>
                <div v-if="task.progressSummary" class="mt-2 line-clamp-3 break-words text-[11px] text-[#a3a3a3]" :title="task.progressSummary">{{ task.progressSummary }}</div>
                <div v-if="task.error" class="mt-2 line-clamp-2 break-words text-[11px] text-[#fca5a5]" :title="task.error">{{ task.error }}</div>
                <div v-if="task.attempts?.length" class="mt-2 border-t border-white/10 pt-2">
                  <div class="truncate text-[11px] text-[#d4d4d4]" :title="attemptPath(task)">{{ attemptPath(task) }}</div>
                  <div class="mt-1 space-y-1">
                    <div v-for="attempt in task.attempts" :key="`${attempt.attempt}-${attempt.executorId}`" class="flex min-w-0 items-start gap-2 text-[11px]">
                      <span class="w-4 shrink-0 text-right text-[#666]">{{ attempt.attempt }}</span>
                      <span class="min-w-0 flex-1 truncate text-[#a3a3a3]" :title="attempt.error || attempt.diagnosticCode">{{ executorLabel(attempt.executorId) }} · {{ attemptStatusLabels[attempt.status] || attempt.status }}<template v-if="attemptDuration(attempt)"> · {{ attemptDuration(attempt) }}</template><template v-if="attempt.error"> · <span class="text-[#fca5a5]">{{ attempt.error }}</span></template></span>
                    </div>
                  </div>
                </div>
              </div>
              <DelegationIconButton
                v-if="task.cancelable"
                icon="icon-[mdi--close]"
                :label="cancelingTasks[task.id] ? '取消任务中' : '取消任务'"
                :disabled="Boolean(cancelingTasks[task.id])"
                :spinning="Boolean(cancelingTasks[task.id])"
                danger
                @click="handleCancelTask(task)"
              />
            </div>
          </article>
        </div>
      </div>
    </component>

    <component :is="props.framed ? Card : 'section'">
      <div class="flex h-full min-w-0 flex-col gap-3">
        <div>
          <h2 class="text-base font-medium text-white">MCP 运行时</h2>
          <div class="mt-1 text-sm text-[#a3a3a3]">状态、能力检查与连接控制。</div>
        </div>
        <label class="text-xs text-[#a3a3a3]">工作区目录（可选）<input v-model="workspaceRoot" class="mt-1 h-8 w-full rounded-[6px] border border-white/10 bg-black/20 px-2 text-xs text-white" placeholder="E:\\workspace" @change="handleWorkspaceRootChange" /></label>
        <div v-if="mcpState.error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ mcpState.error }}</div>
        <div v-if="!mcpState.items.length" class="rounded-[6px] border border-dashed border-[#444] px-3 py-5 text-center text-xs text-[#858585]">暂无数据</div>
        <div v-else class="max-h-[320px] space-y-2 overflow-y-auto pr-1">
          <article v-for="server in mcpState.items" :key="mcpServerStateKey(server)" class="min-w-0 rounded-[6px] border border-white/10 bg-black/15 p-3">
            <div class="flex min-w-0 items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-center gap-2 text-xs">
                  <span class="truncate font-medium text-white" :title="server.identifier">{{ server.name || server.identifier }}</span>
                  <span class="shrink-0" :class="statusClass(server.status)">{{ mcpStatusLabels[server.status] || server.status }}</span>
                </div>
                <div class="mt-1 truncate text-[11px] text-[#858585]">{{ server.transport || "stdio" }} · {{ server.sourceLabel || server.source }}</div>
                <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-[#a3a3a3]">
                  <span>{{ server.toolCount || 0 }} 工具</span>
                  <span>{{ mcpScopeLabel(server) }}</span>
                  <span :class="statusClass(server.capabilityStatus || server.status)">能力：{{ mcpCapabilityLabels[server.capabilityStatus || server.status] || "未检查" }}</span>
                  <span>检查：{{ formatLastChecked(server.lastCheckedAt) }}</span>
                  <span v-if="shortConfigFingerprint(server.configFingerprint)" class="font-mono" :title="server.configFingerprint">配置：{{ shortConfigFingerprint(server.configFingerprint) }}</span>
                </div>
                <div v-if="server.lastError" class="mt-2 line-clamp-2 break-words text-[11px] text-[#fca5a5]" :title="server.lastError">{{ server.lastError }}</div>
              </div>
              <DelegationIconButton
                v-if="mcpActions[mcpServerStateKey(server)]?.type === 'connecting'"
                icon="icon-[mdi--close]"
                :label="cancelingMCPAttempts[mcpServerStateKey(server)] ? '取消连接中' : '取消连接'"
                :disabled="Boolean(cancelingMCPAttempts[mcpServerStateKey(server)])"
                :spinning="Boolean(cancelingMCPAttempts[mcpServerStateKey(server)])"
                danger
                @click="handleCancelMCP(server)"
              />
              <Button
                v-else-if="server.status === 'connected'"
                variant="default"
                :disabled="Boolean(mcpActions[mcpServerStateKey(server)])"
                @click="handleDisconnectMCP(server)"
              >
                {{ mcpActions[mcpServerStateKey(server)]?.type === "disconnecting" ? "断开中..." : "断开" }}
              </Button>
              <Button
                v-else
                variant="default"
                :disabled="Boolean(mcpActions[mcpServerStateKey(server)]) || !server.enabled"
                @click="handleConnectMCP(server)"
              >
                连接
              </Button>
            </div>
          </article>
        </div>
      </div>
    </component>
  </div>
</template>
