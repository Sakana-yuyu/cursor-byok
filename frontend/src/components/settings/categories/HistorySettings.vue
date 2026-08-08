<script setup>
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { clearHistory, deleteHistoryDebugLogs, deleteHistorySessions, getHistorySessions } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import copyTextToClipboard from "copy-text-to-clipboard";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const message = useMessage();
const router = useRouter();
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
  if (["provider_error", "failed"].includes(status)) {
    return {
      label: status === "failed" ? "失败" : "错误",
      badge: "border-red-400/15 bg-red-400/[0.07] text-red-300/80",
      row: "border-red-400/10 bg-red-400/[0.025]",
    };
  }
  if (status === "canceled") {
    return {
      label: "已取消",
      badge: "border-white/10 bg-white/[0.04] text-[#858585]",
      row: "border-white/[0.06] bg-white/[0.015]",
    };
  }
  if (["running", "waiting_tool"].includes(status)) {
    return {
      label: "进行中",
      badge: "border-sky-400/15 bg-sky-400/[0.07] text-sky-300/80",
      row: "border-sky-400/10 bg-sky-400/[0.025]",
    };
  }
  return {
    label: "",
    badge: "",
    row: "border-white/[0.06] bg-white/[0.018]",
  };
}

function copyID(value, label) {
  const text = String(value || "").trim();
  if (!text) return;
  copyTextToClipboard(text);
  message.success(`已复制${label}`);
}

function openInDiagnostics(sessionID) {
  const id = String(sessionID || "").trim();
  if (!id) return;
  void router.push({ path: "/diagnostics", query: { session: id } });
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
    const freed = await deleteHistoryDebugLogs([...selectedIDs]);
    message.success(freed > 0 ? `已清理所选调试日志，释放 ${formatSize(freed)}` : "所选会话没有调试日志");
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
    <div class="flex h-[min(42rem,calc(100dvh-14rem))] min-h-0 min-w-0 flex-col">
      <header class="flex flex-wrap items-start justify-between gap-4 border-b border-white/[0.07] pb-4">
        <div class="min-w-0">
          <div class="flex items-center gap-2.5">
            <span class="grid size-8 shrink-0 place-items-center rounded-[8px] bg-[#10AD5D]/10 text-[#65d99b]">
              <span class="icon-[mdi--history] text-[18px]" aria-hidden="true" />
            </span>
            <div>
              <h2 class="text-sm font-semibold tracking-wide text-[#f2f2f2]">历史与日志</h2>
              <p class="mt-0.5 text-[11px] text-[#737373]">管理本地会话记录与调试数据</p>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 pl-[42px] text-[11px] text-[#858585]">
            <span><strong class="font-medium tabular-nums text-[#d4d4d4]">{{ sessions.length }}</strong> 个会话</span>
            <span>会话历史 <strong class="font-medium tabular-nums text-[#d4d4d4]">{{ formatSize(totalSizeBytes) }}</strong></span>
            <span>调试日志 <strong class="font-medium tabular-nums text-[#d4d4d4]">{{ formatSize(totalDebugSizeBytes) }}</strong></span>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            type="button"
            class="center-row h-8 gap-1.5 rounded-[7px] border border-white/[0.09] bg-white/[0.035] px-3 text-xs text-[#b5b5b5] transition-colors hover:border-white/15 hover:bg-white/[0.07] hover:text-white disabled:cursor-not-allowed disabled:opacity-45"
            :disabled="loading"
            @click="refresh"
          >
            <span class="icon-[mdi--refresh] text-[15px]" :class="loading ? 'animate-spin' : ''" aria-hidden="true" />
            {{ loading ? "刷新中" : "刷新" }}
          </button>
          <button
            type="button"
            class="center-row h-8 gap-1.5 rounded-[7px] border border-red-400/10 bg-transparent px-3 text-xs text-red-300/65 transition-colors hover:border-red-400/20 hover:bg-red-400/[0.06] hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-35"
            :disabled="clearing || sessions.length === 0"
            @click="handleClearAll"
          >
            <span class="icon-[mdi--delete-sweep-outline] text-[15px]" aria-hidden="true" />
            {{ clearing ? "清理中" : "清空全部" }}
          </button>
        </div>
      </header>

      <div v-if="error" class="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-red-400/15 bg-red-400/[0.05] px-3 py-2 text-xs text-red-300/90">
        <span class="min-w-0">{{ error }}</span>
        <button type="button" class="font-medium text-red-200 hover:text-white" :disabled="loading" @click="refresh">
          {{ loading ? "重试中" : "重试" }}
        </button>
      </div>

      <div v-if="!loading && !error && sessions.length === 0" class="grid min-h-0 flex-1 place-items-center py-10 text-center">
        <div>
          <span class="icon-[mdi--history] text-[32px] text-[#4a4a4a]" aria-hidden="true" />
          <p class="mt-2 text-sm text-[#858585]">暂无历史记录</p>
          <p class="mt-1 text-[11px] text-[#5f5f5f]">完成对话后，记录会显示在这里</p>
        </div>
      </div>

      <div v-else class="mt-3 min-h-0 flex-1 overflow-hidden rounded-[9px] border border-white/[0.07] bg-black/[0.12]">
        <div class="h-full space-y-1 overflow-y-auto overscroll-contain p-1.5 pr-1">
          <div v-for="year in groupedTree" :key="year.key" class="overflow-hidden rounded-[7px]">
            <button
              type="button"
              class="group flex w-full items-center gap-2 rounded-[6px] px-2.5 py-2 text-left text-xs font-medium text-[#e5e5e5] transition-colors hover:bg-white/[0.045]"
              @click="toggleCollapsed(year.key)"
            >
              <span
                class="icon-[mdi--chevron-down] text-[16px] text-[#666] transition-transform duration-150"
                :class="isCollapsed(year.key) ? '-rotate-90' : ''"
                aria-hidden="true"
              />
              <span class="icon-[mdi--calendar-blank-outline] text-[15px] text-[#929292]" aria-hidden="true" />
              <span class="min-w-0 truncate">{{ year.label }}</span>
              <span class="ml-auto shrink-0 rounded-full bg-white/[0.045] px-2 py-0.5 text-[10px] font-normal tabular-nums text-[#737373]">{{ year.sessionCount }}</span>
            </button>
            <div v-if="!isCollapsed(year.key)" class="ml-[17px] border-l border-white/[0.07] pl-2">
              <div v-for="month in year.months" :key="month.key">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 rounded-[6px] px-2 py-1.5 text-left text-xs text-[#b8b8b8] transition-colors hover:bg-white/[0.04] hover:text-[#e5e5e5]"
                  @click="toggleCollapsed(month.key)"
                >
                  <span
                    class="icon-[mdi--chevron-down] text-[15px] text-[#5f5f5f] transition-transform duration-150"
                    :class="isCollapsed(month.key) ? '-rotate-90' : ''"
                    aria-hidden="true"
                  />
                  <span class="min-w-0 truncate">{{ formatMonthLabel(month.label) }}</span>
                  <span class="ml-auto shrink-0 text-[10px] tabular-nums text-[#5f5f5f]">{{ month.sessionCount }}</span>
                </button>
                <div v-if="!isCollapsed(month.key)" class="ml-[15px] border-l border-white/[0.06] pl-2">
                  <div v-for="day in month.days" :key="day.key">
                    <button
                      type="button"
                      class="flex w-full items-center gap-2 rounded-[6px] px-2 py-1.5 text-left text-[11px] text-[#858585] transition-colors hover:bg-white/[0.035] hover:text-[#b8b8b8]"
                      @click="toggleCollapsed(day.key)"
                    >
                      <span
                        class="icon-[mdi--chevron-down] text-[14px] text-[#555] transition-transform duration-150"
                        :class="isCollapsed(day.key) ? '-rotate-90' : ''"
                        aria-hidden="true"
                      />
                      <span class="min-w-0 truncate">{{ formatDayLabel(day.label) }}</span>
                      <span class="ml-auto shrink-0 text-[10px] tabular-nums text-[#555]">{{ day.sessionCount }}</span>
                    </button>
                    <div v-if="!isCollapsed(day.key)" class="space-y-1 pb-1 pl-1">
                      <label
                        v-for="session in day.sessions"
                        :key="session.id"
                        class="group flex cursor-pointer items-center gap-2 rounded-[7px] border px-2.5 py-2 transition-colors hover:border-white/[0.12] hover:bg-white/[0.045]"
                        :class="[
                          statusInfo(session).row,
                          selectedIDs.has(session.id) ? '!border-[#10AD5D]/25 !bg-[#10AD5D]/[0.055]' : '',
                        ]"
                      >
                        <input
                          type="checkbox"
                          class="size-3.5 shrink-0 accent-[#10AD5D]"
                          :checked="selectedIDs.has(session.id)"
                          @change="toggleSession(session.id)"
                        />
                        <div class="min-w-0 flex-1">
                          <div class="flex min-w-0 items-center gap-2">
                            <span class="icon-[mdi--message-text-outline] shrink-0 text-[14px] text-[#666] transition-colors group-hover:text-[#929292]" aria-hidden="true" />
                            <span class="min-w-0 flex-1 truncate text-xs font-medium text-[#dedede]" :title="sessionTitle(session)">
                              {{ sessionTitle(session) }}
                            </span>
                            <span
                              v-if="session.subagentType"
                              class="shrink-0 rounded-[4px] border border-white/[0.07] bg-white/[0.035] px-1.5 py-0.5 text-[9px] text-[#858585]"
                            >
                              {{ session.subagentType }}
                            </span>
                            <span class="hidden shrink-0 text-[10px] tabular-nums text-[#737373] xl:inline">{{ formatSize(session.sizeBytes) }}</span>
                            <span v-if="session.debugSizeBytes" class="hidden shrink-0 text-[10px] tabular-nums text-amber-300/65 xl:inline">日志 {{ formatSize(session.debugSizeBytes) }}</span>
                          </div>
                          <div class="mt-1 flex min-w-0 items-center gap-2 pl-[22px] text-[10px] text-[#666]">
                            <span class="shrink-0 tabular-nums">{{ formatTime(session.updatedAtUnixMs || session.createdAtUnixMs) }}</span>
                            <span v-if="statusInfo(session).label" class="shrink-0 rounded-full border px-1.5 py-px text-[9px]" :class="statusInfo(session).badge">{{ statusInfo(session).label }}</span>
                            <span v-if="session.hasDebug" class="shrink-0 rounded-full bg-amber-300/[0.07] px-1.5 py-px text-[9px] text-amber-300/65">
                              调试日志
                            </span>
                            <button type="button" class="center-row min-w-0 gap-1 truncate transition-colors hover:text-[#c7c7c7]" :title="`复制会话 ID：${session.id}`" @click.stop="copyID(session.id, '会话 ID')"><span class="truncate">{{ shortID(session.id) }}</span><span class="icon-[mdi--content-copy] shrink-0 text-[11px]" /></button>
                            <button v-if="session.hasDebug" type="button" class="center-row shrink-0 gap-1 transition-colors hover:text-[#c7c7c7]" title="在诊断中打开该会话的调试日志" @click.stop="openInDiagnostics(session.id)"><span class="icon-[mdi--bug-outline] text-[11px]" /><span>诊断</span></button>
                            <button v-if="session.requestId" type="button" class="center-row min-w-0 gap-1 truncate transition-colors hover:text-[#c7c7c7]" :title="`复制请求 ID：${session.requestId}`" @click.stop="copyID(session.requestId, '请求 ID')"><span class="truncate">请求 {{ shortID(session.requestId) }}</span><span class="icon-[mdi--content-copy] shrink-0 text-[11px]" /></button>
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
      </div>

      <footer class="mt-3 flex shrink-0 flex-wrap items-center gap-2 border-t border-white/[0.07] pt-3">
        <div class="mr-auto flex min-w-0 items-center gap-2 text-[11px]">
          <span
            class="grid size-5 place-items-center rounded-full text-[10px] font-semibold tabular-nums transition-colors"
            :class="selectedCount > 0 ? 'bg-[#10AD5D]/15 text-[#65d99b]' : 'bg-white/[0.045] text-[#666]'"
          >
            {{ selectedCount }}
          </span>
          <span :class="selectedCount > 0 ? 'text-[#b8b8b8]' : 'text-[#666]'">
            {{ selectedCount > 0 ? "个会话已选中" : "选择会话后可批量操作" }}
          </span>
        </div>
        <button
          type="button"
          class="center-row h-8 gap-1.5 rounded-[7px] border border-white/[0.08] bg-white/[0.03] px-3 text-xs text-[#a3a3a3] transition-colors hover:border-white/15 hover:bg-white/[0.06] hover:text-[#dedede] disabled:cursor-not-allowed disabled:opacity-35"
          :disabled="selectedCount === 0 || cleaningDebug"
          @click="handleCleanSelectedDebug"
        >
          <span class="icon-[mdi--bug-outline] text-[14px]" aria-hidden="true" />
          {{ cleaningDebug ? "清理中" : "清理调试日志" }}
        </button>
        <button
          type="button"
          class="center-row h-8 gap-1.5 rounded-[7px] border border-red-400/10 bg-red-400/[0.035] px-3 text-xs text-red-300/70 transition-colors hover:border-red-400/20 hover:bg-red-400/[0.075] hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-30"
          :disabled="selectedCount === 0 || deleting"
          @click="handleDeleteSelected"
        >
          <span class="icon-[mdi--trash-can-outline] text-[14px]" aria-hidden="true" />
          {{ deleting ? "删除中" : "删除所选" }}
        </button>
      </footer>
    </div>
  </Card>
</template>