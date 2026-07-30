<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import {
  cancelDelegationTask,
  cancelMCPRuntimeConnection,
  connectMCPRuntimeServer,
  disconnectMCPRuntimeServer,
  getDelegationTaskSnapshots,
  getMCPRuntimeServers,
} from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";

defineProps({ compact: { type: Boolean, default: false } });

const message = useMessage();
const taskState = reactive({ busy: false, error: "", items: [] });
const mcpState = reactive({ busy: false, error: "", items: [] });
const cancelingTasks = reactive({});
const mcpActions = reactive({});
const mcpAttempts = reactive({});
const canceledMCPAttempts = reactive({});
const cancelingMCPAttempts = reactive({});
const workspaceRoot = ref(window.localStorage.getItem("cursor-byok-mcp-workspace-root") || "");
const refreshBusy = computed(() => taskState.busy || mcpState.busy);
let taskRefreshTimer = 0;
let mcpRefreshTimer = 0;
let attemptSequence = 0;
let taskGeneration = 0;
let mcpGeneration = 0;

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

function statusClass(status) {
  if (status === "completed" || status === "connected") return "text-[#6ee7a5]";
  if (status === "failed" || status === "error" || status === "timed_out") return "text-[#fca5a5]";
  if (status === "running" || status === "connecting" || status === "degraded") return "text-[#facc15]";
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

function formatDuration(milliseconds) {
  const value = Math.max(0, Number(milliseconds || 0));
  if (value < 1000) return `${value} ms`;
  if (value < 60000) return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} s`;
  return `${Math.floor(value / 60000)} min ${Math.floor((value % 60000) / 1000)} s`;
}

function replaceMCPServer(next) {
  if (!next?.identifier) return;
  const index = mcpState.items.findIndex((item) => item.identifier === next.identifier);
  if (index >= 0) mcpState.items.splice(index, 1, next);
  else mcpState.items.push(next);
}

async function refreshTasks(silent = false) {
  if (taskState.busy) return;
  const generation = taskGeneration;
  taskState.busy = true;
  if (!silent) taskState.error = "";
  try {
    const items = await getDelegationTaskSnapshots();
    if (generation !== taskGeneration) return;
    taskState.items = Array.isArray(items) ? items : [];
    taskState.error = "";
  } catch (error) {
    taskState.error = toUserError(error);
  } finally {
    taskState.busy = false;
  }
}

async function refreshMCPServers(silent = false) {
  if (mcpState.busy) return;
  const generation = mcpGeneration;
  mcpState.busy = true;
  if (!silent) mcpState.error = "";
  try {
    const items = await getMCPRuntimeServers(workspaceRoot.value);
    if (generation !== mcpGeneration) return;
    const current = new Map(mcpState.items.map((item) => [item.identifier, item]));
    mcpState.items = (Array.isArray(items) ? items : []).map((item) => mcpActions[item.identifier] ? current.get(item.identifier) || item : item);
    mcpState.error = "";
  } catch (error) {
    mcpState.error = toUserError(error);
  } finally {
    mcpState.busy = false;
  }
}

async function refreshRuntime(silent = false) {
  await Promise.all([refreshTasks(silent), refreshMCPServers(silent)]);
}

async function handleCancelTask(task) {
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
  const identifier = server?.identifier;
  if (!identifier || mcpActions[identifier]) return;
  const attemptID = `mcp-connect-${Date.now()}-${attemptSequence += 1}`;
  mcpGeneration += 1;
  mcpActions[identifier] = "connecting";
  mcpAttempts[identifier] = attemptID;
  mcpState.error = "";
  try {
    replaceMCPServer({ ...server, status: "connecting", lastError: "" });
    replaceMCPServer(await connectMCPRuntimeServer(identifier, attemptID, workspaceRoot.value));
    message.success("连接成功");
  } catch (error) {
    if (!canceledMCPAttempts[identifier]) mcpState.error = toUserError(error);
    await refreshMCPServers(true);
  } finally {
    mcpGeneration += 1;
    delete mcpActions[identifier];
    delete mcpAttempts[identifier];
    delete canceledMCPAttempts[identifier];
  }
}

async function handleCancelMCP(server) {
  const identifier = server?.identifier;
  const attemptID = mcpAttempts[identifier];
  if (!identifier || !attemptID || cancelingMCPAttempts[identifier]) return;
  cancelingMCPAttempts[identifier] = true;
  try {
    const canceled = await cancelMCPRuntimeConnection(identifier, attemptID);
    if (!canceled) throw new Error("操作失败");
    canceledMCPAttempts[identifier] = true;
    message.success("已取消");
  } catch (error) {
    mcpState.error = toUserError(error);
  } finally {
    delete cancelingMCPAttempts[identifier];
  }
}

async function handleDisconnectMCP(server) {
  const identifier = server?.identifier;
  if (!identifier || mcpActions[identifier]) return;
  mcpGeneration += 1;
  mcpActions[identifier] = "disconnecting";
  mcpState.error = "";
  try {
    replaceMCPServer(await disconnectMCPRuntimeServer(identifier, workspaceRoot.value));
    message.success("已断开连接");
  } catch (error) {
    mcpState.error = toUserError(error);
  } finally {
    mcpGeneration += 1;
    delete mcpActions[identifier];
  }
}

function handleWorkspaceRootChange() {
  window.localStorage.setItem("cursor-byok-mcp-workspace-root", workspaceRoot.value.trim());
  mcpGeneration += 1;
  void refreshMCPServers();
}

onMounted(() => {
  void refreshRuntime();
  taskRefreshTimer = window.setInterval(() => void refreshTasks(true), 1500);
  mcpRefreshTimer = window.setInterval(() => void refreshMCPServers(true), 5000);
});

onUnmounted(() => {
  window.clearInterval(taskRefreshTimer);
  window.clearInterval(mcpRefreshTimer);
  for (const [identifier, attemptID] of Object.entries(mcpAttempts)) {
    void cancelMCPRuntimeConnection(identifier, attemptID);
  }
});
</script>

<template>
  <div class="grid gap-4" :class="compact ? 'grid-cols-1' : 'xl:grid-cols-2'">
    <Card>
      <div class="flex h-full min-w-0 flex-col gap-3">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="text-base font-medium text-white">Multitask 委派</h2>
            <div class="mt-1 text-sm text-[#a3a3a3]">使用已配置模型并行处理子任务，失败的子任务不会阻塞其他任务。</div>
          </div>
          <Button variant="default" :disabled="refreshBusy" @click="refreshRuntime()">刷新</Button>
        </div>
        <div v-if="taskState.error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ taskState.error }}</div>
        <div v-if="!taskItems.length" class="rounded-[6px] border border-dashed border-[#444] px-3 py-5 text-center text-xs text-[#858585]">暂无数据</div>
        <div v-else class="max-h-[320px] space-y-2 overflow-y-auto pr-1">
          <article v-for="task in taskItems" :key="task.id" class="min-w-0 rounded-[6px] border border-white/10 bg-black/15 p-3">
            <div class="flex min-w-0 items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-center gap-2 text-xs">
                  <span class="truncate font-medium text-white" :title="task.modelName || task.modelId">{{ task.modelName || task.modelId || "模型" }}</span>
                  <span class="shrink-0" :class="statusClass(task.status)">{{ taskStatusLabels[task.status] || task.status }}</span>
                </div>
                <div class="mt-1 truncate text-[11px] text-[#858585]" :title="task.description || task.id">{{ task.description || task.id }}</div>
                <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-[#a3a3a3]">
                  <span>{{ task.executionMode === "cursor" ? "Cursor 子会话" : "本地子代理" }}</span>
                  <span>{{ formatDuration(task.durationMs) }}</span>
                  <span>{{ task.toolCallCount || 0 }} 工具</span>
                </div>
                <div v-if="task.error" class="mt-2 line-clamp-2 break-words text-[11px] text-[#fca5a5]" :title="task.error">{{ task.error }}</div>
              </div>
              <Button v-if="task.cancelable" variant="default" :disabled="Boolean(cancelingTasks[task.id])" @click="handleCancelTask(task)">取消</Button>
            </div>
          </article>
        </div>
      </div>
    </Card>

    <Card>
      <div class="flex h-full min-w-0 flex-col gap-3">
        <div>
          <h2 class="text-base font-medium text-white">MCP Servers</h2>
          <div class="mt-1 text-sm text-[#a3a3a3]">状态、工具与连接控制</div>
        </div>
        <label class="text-xs text-[#a3a3a3]">工作区目录（可选）<input v-model="workspaceRoot" class="mt-1 h-8 w-full rounded-[6px] border border-white/10 bg-black/20 px-2 text-xs text-white" placeholder="E:\\workspace" @change="handleWorkspaceRootChange" /></label>
        <div v-if="mcpState.error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ mcpState.error }}</div>
        <div v-if="!mcpState.items.length" class="rounded-[6px] border border-dashed border-[#444] px-3 py-5 text-center text-xs text-[#858585]">暂无数据</div>
        <div v-else class="max-h-[320px] space-y-2 overflow-y-auto pr-1">
          <article v-for="server in mcpState.items" :key="server.identifier" class="min-w-0 rounded-[6px] border border-white/10 bg-black/15 p-3">
            <div class="flex min-w-0 items-start gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-center gap-2 text-xs">
                  <span class="truncate font-medium text-white" :title="server.identifier">{{ server.name || server.identifier }}</span>
                  <span class="shrink-0" :class="statusClass(server.status)">{{ mcpStatusLabels[server.status] || server.status }}</span>
                </div>
                <div class="mt-1 truncate text-[11px] text-[#858585]" :title="server.command || server.url || server.source">{{ server.transport || "stdio" }} · {{ server.sourceLabel || server.source }}</div>
                <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-[#a3a3a3]">
                  <span>{{ server.toolCount || 0 }} 工具</span>
                  <span>{{ mcpScopeLabel(server) }}</span>
                  <span :class="statusClass(server.capabilityStatus || server.status)">能力：{{ mcpCapabilityLabels[server.capabilityStatus || server.status] || "未检查" }}</span>
                  <span>检查：{{ formatLastChecked(server.lastCheckedAt) }}</span>
                  <span v-if="shortConfigFingerprint(server.configFingerprint)" class="font-mono" :title="server.configFingerprint">配置：{{ shortConfigFingerprint(server.configFingerprint) }}</span>
                </div>
                <div v-if="server.lastError" class="mt-2 line-clamp-2 break-words text-[11px] text-[#fca5a5]" :title="server.lastError">{{ server.lastError }}</div>
              </div>
              <Button v-if="mcpActions[server.identifier] === 'connecting'" variant="default" :disabled="Boolean(cancelingMCPAttempts[server.identifier])" @click="handleCancelMCP(server)">{{ cancelingMCPAttempts[server.identifier] ? "取消中..." : "取消" }}</Button>
              <Button v-else-if="server.status === 'connected'" variant="default" :disabled="Boolean(mcpActions[server.identifier])" @click="handleDisconnectMCP(server)">{{ mcpActions[server.identifier] === "disconnecting" ? "断开中..." : "断开" }}</Button>
              <Button v-else variant="default" :disabled="Boolean(mcpActions[server.identifier]) || !server.enabled" @click="handleConnectMCP(server)">连接</Button>
            </div>
          </article>
        </div>
      </div>
    </Card>
  </div>
</template>
