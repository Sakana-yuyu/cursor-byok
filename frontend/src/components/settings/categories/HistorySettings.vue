<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { clearHistory, deleteHistoryDebugLogs, deleteHistorySessions, getHistorySessions } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref } from "vue";
import copyTextToClipboard from "copy-text-to-clipboard";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const message = useMessage();
const sessions = ref([]);
const loading = ref(false);
const error = ref("");
const selectedIDs = reactive(new Set());
const deleting = ref(false);
const clearing = ref(false);
const cleaningDebug = ref(false);
const collapsed = reactive(new Set());

const totalSizeBytes = computed(() =>
  sessions.value.reduce((total, session) => total + Number(session.sizeBytes || 0), 0),
);
const totalDebugSizeBytes = computed(() =>
  sessions.value.reduce((total, session) => total + Number(session.debugSizeBytes || 0), 0),
);

const groupedTree = computed(() => {
  const years = new Map();
  for (const session of sessions.value) {
    const timestamp = Number(session.createdAtUnixMs || session.updatedAtUnixMs || 0);
    const date = timestamp > 0 ? new Date(timestamp) : null;
    const yearKey = date ? String(date.getFullYear()) : "未知";
    const monthKey = date ? `${yearKey}-${String(date.getMonth() + 1).padStart(2, "0")}` : "未知";
    const dayKey = date ? `${monthKey}-${String(date.getDate()).padStart(2, "0")}` : "未知";
    if (!years.has(yearKey)) years.set(yearKey, new Map());
    const months = years.get(yearKey);
    if (!months.has(monthKey)) months.set(monthKey, new Map());
    const days = months.get(monthKey);
    if (!days.has(dayKey)) days.set(dayKey, []);
    days.get(dayKey).push(session);
  }
  const sortedYears = [...years.entries()].sort(([left], [right]) => right.localeCompare(left));
  const countSessions = (daysMap) =>
    [...daysMap.values()].reduce((sum, list) => sum + list.length, 0);
  return sortedYears.map(([year, monthsMap]) => ({
    key: year,
    label: year,
    sessionCount: countSessions(monthsMap),
    months: [...monthsMap.entries()]
      .sort(([left], [right]) => right.localeCompare(left))
      .map(([month, daysMap]) => ({
        key: month,
        label: month,
        sessionCount: countSessions(daysMap),
        days: [...daysMap.entries()]
          .sort(([left], [right]) => right.localeCompare(left))
          .map(([day, items]) => ({
            key: day,
            label: day,
            sessionCount: items.length,
            sessions: items.sort((left, right) =>
              Number(right.updatedAtUnixMs || 0) - Number(left.updatedAtUnixMs || 0),
            ),
          })),
      })),
  }));
});

const selectedCount = computed(() => selectedIDs.size);

function toggleCollapsed(key) {
  if (collapsed.has(key)) {
    collapsed.delete(key);
  } else {
    collapsed.add(key);
  }
}

function isCollapsed(key) {
  return collapsed.has(key);
}

function formatSize(bytes) {
  const value = Number(bytes || 0);
  if (value <= 0) return "0 B";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`;
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function formatDayLabel(dayKey) {
  const parts = dayKey.split("-");
  return parts.length === 3 ? `${parts[0]}年${Number(parts[1])}月${Number(parts[2])}日` : dayKey;
}

function formatMonthLabel(monthKey) {
  const parts = monthKey.split("-");
  return parts.length === 2 ? `${parts[0]}年${Number(parts[1])}月` : monthKey;
}

function formatTime(unixMs) {
  const value = Number(unixMs || 0);
  if (value <= 0) return "";
  const date = new Date(value);
  return `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

function sessionTitle(session) {
  return session.title || `对话 ${String(session.id).slice(0, 8)}`;
}

function statusInfo(session) {
  const status = String(session.status || "").toLowerCase();
  if (status === "provider_error") return { label: "错误", class: "border-[#7f1d1d] bg-[#451a1a] text-[#fca5a5]", row: "border-[#7f1d1d] bg-[#2a1313]" };
  if (status === "failed") return { label: "失败", class: "border-[#7f1d1d] bg-[#451a1a] text-[#fca5a5]", row: "border-[#7f1d1d] bg-[#2a1313]" };
  if (status === "canceled") return { label: "已取消", class: "border-[#525252] bg-[#303030] text-[#a3a3a3]", row: "border-[#404040] bg-[#202020]" };
  if (["running", "waiting_tool"].includes(status)) return { label: "进行中", class: "border-[#1d4ed8] bg-[#172554] text-[#93c5fd]", row: "border-[#1e3a8a] bg-[#111c36]" };
  return { label: "", class: "", row: "border-white/5 bg-black/15" };
}

function copyID(value, label) {
  const text = String(value || "").trim();
  if (!text) return;
  copyTextToClipboard(text);
  message.success(`已复制${label}`);
}

function shortID(value) {
  const text = String(value || "");
  return text.length > 12 ? `${text.slice(0, 8)}…${text.slice(-4)}` : text;
}

function toggleSession(id) {
  if (selectedIDs.has(id)) {
    selectedIDs.delete(id);
  } else {
    selectedIDs.add(id);
  }
}

async function refresh() {
  if (loading.value) return;
  loading.value = true;
  error.value = "";
  try {
    sessions.value = await getHistorySessions();
  } catch (loadError) {
    error.value = toUserError(loadError);
  } finally {
    loading.value = false;
  }
}

async function handleDeleteSelected() {
  if (selectedCount.value === 0 || deleting.value) return;
  const confirmed = await showModal({
    title: "删除历史",
    content: `确定删除选中的 ${selectedCount.value} 条历史记录吗？此操作不可恢复。`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  deleting.value = true;
  try {
    await deleteHistorySessions([...selectedIDs]);
    message.success("已删除");
    selectedIDs.clear();
    await refresh();
  } catch (deleteError) {
    error.value = toUserError(deleteError);
  } finally {
    deleting.value = false;
  }
}

async function handleCleanSelectedDebug() {
  if (selectedCount.value === 0 || cleaningDebug.value) return;
  cleaningDebug.value = true;
  try {
    await deleteHistoryDebugLogs([...selectedIDs]);
    message.success("已清理所选调试日志");
    selectedIDs.clear();
    await refresh();
  } catch (cleanError) {
    error.value = toUserError(cleanError);
  } finally {
    cleaningDebug.value = false;
  }
}

async function handleClearAll() {
  if (clearing.value) return;
  const confirmed = await showModal({
    title: "一键清理",
    content: `确定清空全部历史记录吗？将删除 ${sessions.value.length} 个会话、孤儿调试数据并重置用量统计，此操作不可恢复。`,
    confirmText: "全部清理",
    cancelText: "取消",
  });
  if (!confirmed) return;
  clearing.value = true;
  try {
    const removed = await clearHistory();
    message.success(`已清理 ${removed} 个会话`);
    selectedIDs.clear();
    await refresh();
  } catch (clearError) {
    error.value = toUserError(clearError);
  } finally {
    clearing.value = false;
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <Card>
    <div class="min-w-0 space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="text-sm font-medium text-white">历史与日志</h2>
          <p class="mt-1 text-xs text-[#858585]">
            {{ sessions.length }} 个会话 · 会话历史 {{ formatSize(totalSizeBytes) }} · 调试日志 {{ formatSize(totalDebugSizeBytes) }}
          </p>
          <p class="mt-1 text-[11px] text-[#666]">会话历史是对话记录；调试日志是用于错误排查的原始数据，可单独清理。</p>
        </div>
        <div class="flex items-center gap-2">
          <Button variant="default" :disabled="loading" @click="refresh">
            {{ loading ? "刷新中..." : "刷新" }}
          </Button>
          <Button
            variant="danger"
            :disabled="clearing || sessions.length === 0"
            @click="handleClearAll"
          >
            {{ clearing ? "清理中..." : "一键清理" }}
          </Button>
        </div>
      </div>

      <div v-if="error" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
        {{ error }}
      </div>

      <div v-if="!loading && sessions.length === 0" class="rounded-[8px] border border-white/10 bg-black/15 px-4 py-6 text-center text-sm text-[#858585]">
        暂无历史记录
      </div>

      <div v-else class="space-y-3">
        <div v-for="year in groupedTree" :key="year.key" class="rounded-[8px] border border-white/10 bg-black/15">
          <button
            type="button"
            class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm font-medium text-white transition-colors hover:bg-white/5"
            @click="toggleCollapsed(year.key)"
          >
            <span
              class="icon-[mdi--folder] text-[18px] text-[#d4a34a]"
              :class="isCollapsed(year.key) ? 'icon-[mdi--folder]' : 'icon-[mdi--folder-open]'"
              aria-hidden="true"
            />
            <span class="min-w-0 truncate">{{ year.label }}</span>
            <span class="ml-auto shrink-0 text-[11px] tabular-nums text-[#666]">{{ year.sessionCount }}</span>
          </button>
          <div v-if="!isCollapsed(year.key)" class="space-y-2 border-t border-white/10 px-3 py-2">
            <div v-for="month in year.months" :key="month.key" class="rounded-[6px] bg-white/[0.03]">
              <button
                type="button"
                class="flex w-full items-center gap-2.5 py-1.5 pl-6 pr-3 text-left text-xs text-[#d4d4d4] transition-colors hover:bg-white/5"
                @click="toggleCollapsed(month.key)"
              >
                <span
                  class="icon-[mdi--folder] text-[16px] text-[#d4a34a]"
                  :class="isCollapsed(month.key) ? 'icon-[mdi--folder]' : 'icon-[mdi--folder-open]'"
                  aria-hidden="true"
                />
                <span class="min-w-0 truncate">{{ formatMonthLabel(month.label) }}</span>
                <span class="ml-auto shrink-0 text-[10px] tabular-nums text-[#666]">{{ month.sessionCount }}</span>
              </button>
              <div v-if="!isCollapsed(month.key)" class="space-y-1.5 px-2 pb-2">
                <div v-for="day in month.days" :key="day.key" class="rounded-[6px] bg-white/[0.03]">
                  <button
                    type="button"
                    class="flex w-full items-center gap-2.5 py-1.5 pl-10 pr-3 text-left text-xs text-[#a3a3a3] transition-colors hover:bg-white/5"
                    @click="toggleCollapsed(day.key)"
                  >
                    <span
                      class="icon-[mdi--folder] text-[14px] text-[#d4a34a]"
                      :class="isCollapsed(day.key) ? 'icon-[mdi--folder]' : 'icon-[mdi--folder-open]'"
                      aria-hidden="true"
                    />
                    <span class="min-w-0 truncate">{{ formatDayLabel(day.label) }}</span>
                    <span class="ml-auto shrink-0 text-[10px] tabular-nums text-[#666]">{{ day.sessionCount }}</span>
                  </button>
                  <div v-if="!isCollapsed(day.key)" class="space-y-1 px-1 pb-1.5">
                    <label
                      v-for="session in day.sessions"
                      :key="session.id"
                      class="flex cursor-pointer items-center gap-2.5 rounded-[6px] border py-2 pl-14 pr-3 hover:border-white/15"
                      :class="statusInfo(session).row"
                    >
                      <input
                        type="checkbox"
                        class="shrink-0 accent-[#10AD5D]"
                        :checked="selectedIDs.has(session.id)"
                        @change="toggleSession(session.id)"
                      />
                      <span class="icon-[mdi--file-document-outline] shrink-0 text-[15px] text-[#737373]" aria-hidden="true" />
                      <div class="min-w-0 flex-1">
                        <div class="flex min-w-0 items-center gap-2">
                          <span class="min-w-0 flex-1 truncate text-xs text-white" :title="sessionTitle(session)">
                            {{ sessionTitle(session) }}
                          </span>
                          <span
                            v-if="session.subagentType"
                            class="shrink-0 rounded-[4px] bg-white/10 px-1.5 py-0.5 text-[10px] text-[#a3a3a3]"
                          >
                            {{ session.subagentType }}
                          </span>
                          <span class="shrink-0 text-[11px] text-[#858585]">历史 {{ formatSize(session.sizeBytes) }}</span><span v-if="session.debugSizeBytes" class="shrink-0 text-[11px] text-[#d4a34a]">日志 {{ formatSize(session.debugSizeBytes) }}</span>
                        </div>
                        <div class="mt-0.5 flex items-center gap-2 text-[11px] text-[#858585]">
                          <span>{{ formatTime(session.updatedAtUnixMs || session.createdAtUnixMs) }}</span>
                          <span v-if="statusInfo(session).label" class="rounded-[4px] border px-1.5 py-0.5 text-[10px]" :class="statusInfo(session).class">{{ statusInfo(session).label }}</span>
                          <span v-if="session.hasDebug" class="rounded-[4px] bg-[#3a2c14] px-1.5 py-0.5 text-[10px] text-[#d4a34a]">
                            调试
                          </span>
                          <button type="button" class="center-row min-w-0 gap-1 truncate text-[#5f5f5f] hover:text-[#d4d4d4]" :title="`复制会话 ID：${session.id}`" @click.stop="copyID(session.id, '会话 ID')"><span class="truncate">{{ shortID(session.id) }}</span><span class="icon-[mdi--content-copy] shrink-0 text-[12px]" /></button>
                          <button v-if="session.requestId" type="button" class="center-row min-w-0 gap-1 truncate text-[#5f5f5f] hover:text-[#d4d4d4]" :title="`复制请求 ID：${session.requestId}`" @click.stop="copyID(session.requestId, '请求 ID')"><span>请求 {{ shortID(session.requestId) }}</span><span class="icon-[mdi--content-copy] shrink-0 text-[12px]" /></button>
                        </div>
                      </div>
                    </label>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div v-if="selectedCount > 0" class="flex items-center justify-between gap-3 rounded-[8px] border border-[#3a2c14] bg-[#2a2413] px-3 py-2">
        <span class="text-xs text-[#d4a34a]">已选中 {{ selectedCount }} 条</span>
        <Button variant="danger" :disabled="deleting" @click="handleDeleteSelected">
          {{ deleting ? "删除中..." : `删除所选 (${selectedCount})` }}
        </Button>
        <Button variant="default" :disabled="cleaningDebug" @click="handleCleanSelectedDebug">
          {{ cleaningDebug ? "清理中..." : `清理调试日志 (${selectedCount})` }}
        </Button>
      </div>
    </div>
  </Card>
</template>