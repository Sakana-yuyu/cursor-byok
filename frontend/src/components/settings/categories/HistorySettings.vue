<script setup>
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { clearHistory, deleteHistoryDebugLogs, deleteHistorySessions, getCursorProtocolSessions, getHistorySessions } from "@/services/runtimeControlApi";
import { appState, toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref, watch } from "vue";
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
const cursorProtocolSessions = ref([]);
const historySource = ref("local");
const expandedProtocolSessions = reactive(new Set());
const loading = ref(false);
const error = ref("");
const selectedIDs = reactive(new Set());
const deleting = ref(false);
const clearing = ref(false);
const cleaningDebug = ref(false);
const collapsed = reactive(new Set());
const visibleSessionCounts = reactive(new Map());
const manuallyToggledGroups = new Set();
const HISTORY_VIEW_MODE_KEY = "cursor-byok.history.view-mode";
const DETAILS_PAGE_SIZE = 50;

function readHistoryViewMode() {
  try {
    return localStorage.getItem(HISTORY_VIEW_MODE_KEY) === "details" ? "details" : "icons";
  } catch {
    return "icons";
  }
}

const historyViewMode = ref(readHistoryViewMode());
const historyPath = ref([]);

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
  const countSessions = (itemsMap) =>
    [...itemsMap.values()].reduce((sum, value) => (
      sum + (Array.isArray(value) ? value.length : countSessions(value))
    ), 0);
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

const currentYear = computed(() =>
  groupedTree.value.find((year) => year.key === historyPath.value[0]),
);
const currentMonth = computed(() =>
  currentYear.value?.months.find((month) => month.key === historyPath.value[1]),
);
const currentDay = computed(() =>
  currentMonth.value?.days.find((day) => day.key === historyPath.value[2]),
);

const historyBreadcrumbs = computed(() => {
  const items = [{ key: "root", label: "历史记录", depth: 0 }];
  if (currentYear.value) items.push({ key: currentYear.value.key, label: currentYear.value.label, depth: 1 });
  if (currentMonth.value) items.push({ key: currentMonth.value.key, label: formatMonthLabel(currentMonth.value.label), depth: 2 });
  if (currentDay.value) items.push({ key: currentDay.value.key, label: formatDayLabel(currentDay.value.label), depth: 3 });
  return items;
});

const currentIconItems = computed(() => {
  if (historyPath.value.length === 0) {
    return groupedTree.value.map((year) => ({
      kind: "folder",
      key: year.key,
      label: year.label,
      sessionCount: year.sessionCount,
    }));
  }
  if (historyPath.value.length === 1) {
    return (currentYear.value?.months || []).map((month) => ({
      kind: "folder",
      key: month.key,
      label: formatMonthLabel(month.label),
      sessionCount: month.sessionCount,
    }));
  }
  if (historyPath.value.length === 2) {
    return (currentMonth.value?.days || []).map((day) => ({
      kind: "folder",
      key: day.key,
      label: formatDayLabel(day.label),
      sessionCount: day.sessionCount,
    }));
  }
  return (currentDay.value?.sessions || []).map((session) => ({
    kind: "session",
    key: session.id,
    label: sessionTitle(session),
    session,
  }));
});

const selectedCount = computed(() => selectedIDs.size);
const allSessionIDs = computed(() => sessions.value.map((session) => session.id).filter(Boolean));
const allSelected = computed(() =>
  allSessionIDs.value.length > 0 && allSessionIDs.value.every((id) => selectedIDs.has(id)),
);
const statusCounts = computed(() => sessions.value.reduce((counts, session) => {
  const status = String(session.status || "").toLowerCase();
  if (["running", "waiting_tool"].includes(status)) counts.active += 1;
  if (["provider_error", "failed"].includes(status)) counts.failed += 1;
  if (status === "interrupted") counts.interrupted += 1;
  return counts;
}, { active: 0, failed: 0, interrupted: 0 }));

function selectAllSessions() {
  for (const id of allSessionIDs.value) {
    selectedIDs.add(id);
  }
}

function clearSelection() {
  selectedIDs.clear();
}

function toggleSelectAll() {
  if (allSelected.value) {
    clearSelection();
    return;
  }
  selectAllSessions();
}

function toggleCollapsed(key) {
  manuallyToggledGroups.add(key);
  if (collapsed.has(key)) {
    collapsed.delete(key);
  } else {
    collapsed.add(key);
  }
}

function isCollapsed(key) {
  return collapsed.has(key);
}

function syncCollapsedGroups(tree) {
  const validKeys = new Set();
  for (const year of tree) {
    validKeys.add(year.key);
    for (const month of year.months) {
      validKeys.add(month.key);
      for (const day of month.days) validKeys.add(day.key);
    }
  }
  for (const key of collapsed) {
    if (!validKeys.has(key)) collapsed.delete(key);
  }
  for (const key of manuallyToggledGroups) {
    if (!validKeys.has(key)) manuallyToggledGroups.delete(key);
  }
  for (const key of visibleSessionCounts.keys()) {
    if (!validKeys.has(key)) visibleSessionCounts.delete(key);
  }
  const recentYear = tree[0];
  const recentMonth = recentYear?.months[0];
  const recentDay = recentMonth?.days[0];
  const recentKeys = new Set([recentYear?.key, recentMonth?.key, recentDay?.key].filter(Boolean));
  for (const year of tree) {
    if (!manuallyToggledGroups.has(year.key)) {
      if (recentKeys.has(year.key)) collapsed.delete(year.key);
      else collapsed.add(year.key);
    }
    for (const month of year.months) {
      if (!manuallyToggledGroups.has(month.key)) {
        if (recentKeys.has(month.key)) collapsed.delete(month.key);
        else collapsed.add(month.key);
      }
      for (const day of month.days) {
        if (!manuallyToggledGroups.has(day.key)) {
          if (recentKeys.has(day.key)) collapsed.delete(day.key);
          else collapsed.add(day.key);
        }
      }
    }
  }
}

function visibleSessions(day) {
  const count = visibleSessionCounts.get(day.key) || DETAILS_PAGE_SIZE;
  return day.sessions.slice(0, count);
}

function hasMoreSessions(day) {
  return visibleSessions(day).length < day.sessions.length;
}

function loadMoreSessions(day) {
  const current = visibleSessionCounts.get(day.key) || DETAILS_PAGE_SIZE;
  visibleSessionCounts.set(day.key, Math.min(day.sessions.length, current + DETAILS_PAGE_SIZE));
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

function setHistoryViewMode(mode) {
  historyViewMode.value = mode === "details" ? "details" : "icons";
}

function enterHistoryFolder(item) {
  if (item?.kind !== "folder") return;
  historyPath.value = [...historyPath.value, item.key];
}

function navigateHistoryPath(depth) {
  historyPath.value = historyPath.value.slice(0, Math.max(0, Number(depth) || 0));
}

function navigateHistoryUp() {
  navigateHistoryPath(historyPath.value.length - 1);
}

function handleIconSessionDoubleClick(session) {
  if (session?.hasDebug) openInDiagnostics(session.id);
}

function formatModifiedTime(unixMs) {
  const value = Number(unixMs || 0);
  if (value <= 0) return "-";
  const date = new Date(value);
  return `${date.getFullYear()}/${String(date.getMonth() + 1).padStart(2, "0")}/${String(date.getDate()).padStart(2, "0")} ${formatTime(value)}`;
}

function protocolSessionLabel(session) {
  return String(session?.requestIdHash || "").trim() || "未知协议会话";
}

function protocolEventDetails(event) {
  return [
    event.clientMessageKind,
    event.clientDetailKind,
    event.clientResultKind,
    event.serverMessageKind,
    event.serverDetailKind,
    event.execMessageKind,
    event.streamContentKind,
    event.subagentAction,
    event.decodeError,
  ].filter(Boolean);
}

function toggleProtocolSession(requestIDHash) {
  if (expandedProtocolSessions.has(requestIDHash)) {
    expandedProtocolSessions.delete(requestIDHash);
  } else {
    expandedProtocolSessions.add(requestIDHash);
  }
}

function selectHistorySource(source) {
  const next = source === "protocol" ? "protocol" : "local";
  if (historySource.value === next) return;
  historySource.value = next;
  void refresh();
}

function sessionType(session) {
  return String(session.subagentType || session.mode || "agent");
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
  if (status === "interrupted") {
    return {
      label: "已中断",
      badge: "border-orange-300/15 bg-orange-300/[0.07] text-orange-200/80",
      row: "border-orange-300/10 bg-orange-300/[0.025]",
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

// 协议时间线只有在「镜像记录 + 协议保真」都开启时才会写盘，
// 所以空列表要区分「没开开关」和「开了但还没抓到」，而不是一律显示暂无记录。
const protocolCaptureReady = computed(() => (
  Boolean(appState.mirrorCaptureEnabled) && Boolean(appState.mirrorCaptureProtocolFidelity)
));

function openProtocolCaptureSettings() {
  void router.push({ path: "/settings", query: { category: "advanced" } });
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

watch(historyViewMode, (mode) => {
  try {
    localStorage.setItem(HISTORY_VIEW_MODE_KEY, mode);
  } catch {
    // The view remains usable when browser storage is unavailable.
  }
});

async function refresh() {
  if (loading.value) return;
  loading.value = true;
  error.value = "";
  try {
    if (historySource.value === "protocol") {
      cursorProtocolSessions.value = await getCursorProtocolSessions();
    } else {
      const loadedSessions = await getHistorySessions();
      sessions.value = loadedSessions.map((session) => ({
        ...session,
        statusPresentation: statusInfo(session),
      }));
      syncCollapsedGroups(groupedTree.value);
    }
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
  <Card class="h-full min-h-0 overflow-hidden [&>div]:min-h-0 [&>div]:overflow-hidden">
    <div data-testid="history-panel" class="flex h-full min-h-[34rem] min-w-0 flex-col">
      <header class="grid gap-3 border-b border-white/[0.07] pb-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
        <div class="min-w-0">
          <div class="flex items-center gap-2.5">
            <span class="grid size-8 shrink-0 place-items-center rounded-[8px] bg-[#10AD5D]/10 text-[#65d99b]">
              <span class="icon-[mdi--history] text-[18px]" aria-hidden="true" />
            </span>
            <div>
              <h2 class="text-sm font-semibold tracking-wide text-[#f2f2f2]">历史与日志</h2>
              <p class="mt-0.5 text-[11px] text-[#737373]">管理本地会话记录、终态与调试数据</p>
            </div>
          </div>
          <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 pl-[42px] text-[11px] text-[#858585]">
            <template v-if="historySource === 'local'">
              <span><strong class="font-medium tabular-nums text-[#d4d4d4]">{{ sessions.length }}</strong> 个会话</span>
              <span>会话历史 <strong class="font-medium tabular-nums text-[#d4d4d4]">{{ formatSize(totalSizeBytes) }}</strong></span>
              <span>调试日志 <strong class="font-medium tabular-nums text-[#d4d4d4]">{{ formatSize(totalDebugSizeBytes) }}</strong></span>
            </template>
            <template v-else>
              <span><strong class="font-medium tabular-nums text-[#d4d4d4]">{{ cursorProtocolSessions.length }}</strong> 个协议会话</span>
              <span>仅显示脱敏结构时间线</span>
            </template>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2 lg:justify-end">
          <div class="grid grid-cols-3 overflow-hidden rounded-[8px] border border-white/[0.07] bg-white/[0.025] text-center text-[10px]">
            <div class="min-w-[58px] border-r border-white/[0.06] px-2 py-1.5">
              <div class="tabular-nums text-sky-300/90">{{ statusCounts.active }}</div>
              <div class="mt-0.5 text-[#6f6f6f]">进行中</div>
            </div>
            <div class="min-w-[58px] border-r border-white/[0.06] px-2 py-1.5">
              <div class="tabular-nums text-red-300/90">{{ statusCounts.failed }}</div>
              <div class="mt-0.5 text-[#6f6f6f]">错误</div>
            </div>
            <div class="min-w-[58px] px-2 py-1.5">
              <div class="tabular-nums text-orange-200/90">{{ statusCounts.interrupted }}</div>
              <div class="mt-0.5 text-[#6f6f6f]">已中断</div>
            </div>
          </div>
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
            v-if="historySource === 'local'"
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

      <div class="mt-2.5 flex h-8 shrink-0 items-center gap-1 border-b border-white/[0.07]" role="tablist" aria-label="历史来源">
        <button type="button" role="tab" :aria-selected="historySource === 'local'" class="h-full border-b-2 px-3 text-xs transition-colors" :class="historySource === 'local' ? 'border-[#10AD5D] text-[#dcefe4]' : 'border-transparent text-[#777] hover:text-[#bdbdbd]'" @click="selectHistorySource('local')">本地会话</button>
        <button type="button" role="tab" :aria-selected="historySource === 'protocol'" class="h-full border-b-2 px-3 text-xs transition-colors" :class="historySource === 'protocol' ? 'border-[#10AD5D] text-[#dcefe4]' : 'border-transparent text-[#777] hover:text-[#bdbdbd]'" @click="selectHistorySource('protocol')">Cursor 协议</button>
      </div>

      <div v-if="error" class="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-red-400/15 bg-red-400/[0.05] px-3 py-2 text-xs text-red-300/90">
        <span class="min-w-0">{{ error }}</span>
        <button type="button" class="font-medium text-red-200 hover:text-white" :disabled="loading" @click="refresh">
          {{ loading ? "重试中" : "重试" }}
        </button>
      </div>

      <div v-if="historySource === 'local' && sessions.length > 0" class="mt-2.5 flex flex-wrap items-center gap-2 rounded-[7px] border border-white/[0.07] bg-white/[0.025] px-2.5 py-1.5">
        <button
          type="button"
          class="center-row h-7 gap-1.5 rounded-[6px] border border-white/[0.09] bg-white/[0.04] px-2.5 text-[11px] text-[#b9b9b9] transition-colors hover:border-[#10AD5D]/30 hover:bg-[#10AD5D]/[0.08] hover:text-white"
          @click="toggleSelectAll"
        >
          <span class="icon-[mdi--checkbox-multiple-marked-outline] text-[14px]" aria-hidden="true" />
          {{ allSelected ? "取消全选" : "全选会话" }}
        </button>
        <button
          v-if="selectedCount > 0"
          type="button"
          class="center-row h-7 gap-1.5 rounded-[6px] px-2.5 text-[11px] text-[#8f8f8f] transition-colors hover:bg-white/[0.06] hover:text-white"
          @click="clearSelection"
        >
          <span class="icon-[mdi--close] text-[13px]" aria-hidden="true" />
          取消选择
        </button>
        <span class="h-4 w-px bg-white/[0.09]" aria-hidden="true" />
        <span class="text-[11px] text-[#777]">
          {{ selectedCount > 0 ? `已选择 ${selectedCount} 个会话` : "勾选会话后进行批量清理或删除" }}
        </span>
        <div class="ml-auto flex flex-wrap items-center gap-2">
          <div class="flex h-7 items-center overflow-hidden rounded-[6px] border border-white/[0.09] bg-black/[0.12]" role="group" aria-label="历史视图">
            <button
              type="button"
              class="grid size-7 place-items-center transition-colors hover:text-white"
              :class="historyViewMode === 'icons' ? 'bg-white/[0.09] text-[#e5e5e5]' : 'text-[#777]'"
              :aria-pressed="historyViewMode === 'icons'"
              title="图标视图"
              aria-label="图标视图"
              @click="setHistoryViewMode('icons')"
            >
              <span class="icon-[mdi--view-grid-outline] text-[15px]" aria-hidden="true" />
            </button>
            <button
              type="button"
              class="grid size-7 place-items-center border-l border-white/[0.07] transition-colors hover:text-white"
              :class="historyViewMode === 'details' ? 'bg-white/[0.09] text-[#e5e5e5]' : 'text-[#777]'"
              :aria-pressed="historyViewMode === 'details'"
              title="详细信息视图"
              aria-label="详细信息视图"
              @click="setHistoryViewMode('details')"
            >
              <span class="icon-[mdi--view-list-outline] text-[16px]" aria-hidden="true" />
            </button>
          </div>
          <button
            type="button"
            class="center-row h-7 gap-1.5 rounded-[6px] border border-white/[0.08] bg-white/[0.03] px-2.5 text-[11px] text-[#9d9d9d] transition-colors hover:border-white/15 hover:bg-white/[0.06] hover:text-white disabled:cursor-not-allowed disabled:opacity-35"
            :disabled="selectedCount === 0 || cleaningDebug"
            @click="handleCleanSelectedDebug"
          >
            <span class="icon-[mdi--bug-outline] text-[13px]" aria-hidden="true" />
            {{ cleaningDebug ? "清理中" : "清理调试日志" }}
          </button>
          <button
            type="button"
            class="center-row h-7 gap-1.5 rounded-[6px] border border-red-400/10 bg-red-400/[0.035] px-2.5 text-[11px] text-red-300/70 transition-colors hover:border-red-400/20 hover:bg-red-400/[0.075] hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-30"
            :disabled="selectedCount === 0 || deleting"
            @click="handleDeleteSelected"
          >
            <span class="icon-[mdi--trash-can-outline] text-[13px]" aria-hidden="true" />
            {{ deleting ? "删除中" : "删除所选" }}
          </button>
        </div>
      </div>

      <div v-if="historySource === 'local' && !loading && !error && sessions.length === 0" class="grid min-h-0 flex-1 place-items-center py-10 text-center">
        <div>
          <span class="icon-[mdi--history] text-[32px] text-[#4a4a4a]" aria-hidden="true" />
          <p class="mt-2 text-sm text-[#858585]">暂无历史记录</p>
          <p class="mt-1 text-[11px] text-[#5f5f5f]">完成对话后，记录会显示在这里</p>
        </div>
      </div>

      <div
        v-else-if="historySource === 'protocol'"
        data-testid="cursor-protocol-history"
        class="mt-2.5 min-h-0 flex-1 overflow-auto overscroll-contain rounded-[8px] border border-white/[0.08] bg-[#202124]"
      >
        <div v-if="!loading && !error && cursorProtocolSessions.length === 0" class="grid min-h-full place-items-center py-10 text-center">
          <div v-if="protocolCaptureReady" class="max-w-[30rem] px-6">
            <span class="icon-[mdi--source-branch] text-[32px] text-[#4a4a4a]" aria-hidden="true" />
            <p class="mt-2 text-sm text-[#858585]">暂无 Cursor 协议记录</p>
            <p class="mt-1 text-[11px] leading-5 text-[#5f5f5f]">协议保真记录已开启。需要在「官方上游模式」下由 Cursor 实际发起一次官方请求，脱敏的上下行结构才会出现在这里。</p>
          </div>
          <div v-else class="max-w-[30rem] px-6">
            <span class="icon-[mdi--toggle-switch-off-outline] text-[32px] text-[#4a4a4a]" aria-hidden="true" />
            <p class="mt-2 text-sm text-[#858585]">协议记录未开启</p>
            <p class="mt-1 text-[11px] leading-5 text-[#5f5f5f]">
              该页面读取协议保真记录写出的结构时间线。请到 设置 → 高级 依次开启「镜像记录官方请求」和「协议保真记录」，并切换到「官方上游模式」。保真记录会把完整协议帧原始字节落盘（含对话内容与工作区上下文），排障结束后请及时关闭。
            </p>
            <button
              type="button"
              class="center-row mx-auto mt-3 h-7 gap-1.5 rounded-[6px] border border-white/[0.09] bg-white/[0.04] px-2.5 text-[11px] text-[#b9b9b9] transition-colors hover:border-[#10AD5D]/30 hover:bg-[#10AD5D]/[0.08] hover:text-white"
              @click="openProtocolCaptureSettings"
            >
              <span class="icon-[mdi--tools] text-[14px]" aria-hidden="true" />
              前往高级设置
            </button>
          </div>
        </div>
        <div v-else class="min-w-[720px] text-[11px] text-[#b8b8b8]">
          <div class="sticky top-0 z-10 grid grid-cols-[minmax(220px,1fr)_80px_80px_100px_108px_72px] border-b border-white/[0.09] bg-[#292a2d] text-[#9a9a9a] shadow-[0_1px_0_rgba(0,0,0,0.35)]">
            <div class="px-3 py-2">匿名请求哈希</div><div class="border-l border-white/[0.06] px-3 py-2">事件</div><div class="border-l border-white/[0.06] px-3 py-2">方向</div><div class="border-l border-white/[0.06] px-3 py-2">模式</div><div class="border-l border-white/[0.06] px-3 py-2">最后活动</div><div class="border-l border-white/[0.06] px-3 py-2 text-right">终态</div>
          </div>
          <div v-for="protocolSession in cursorProtocolSessions" :key="protocolSession.requestIdHash" class="border-b border-white/[0.05]">
            <div class="grid grid-cols-[minmax(220px,1fr)_80px_80px_100px_108px_72px] items-center hover:bg-white/[0.035]">
              <div class="min-w-0 px-3 py-2.5"><div class="flex min-w-0 items-center gap-2"><span class="icon-[mdi--source-branch] shrink-0 text-[15px] text-[#75b7e8]" aria-hidden="true" /><span class="truncate font-mono text-xs text-[#e0e0e0]">{{ protocolSessionLabel(protocolSession) }}</span><span v-if="protocolSession.multitask" class="shrink-0 rounded-[4px] border border-sky-300/15 bg-sky-300/[0.07] px-1.5 py-0.5 text-[9px] text-sky-200/80">Multitask</span></div><div class="mt-1 flex flex-wrap gap-x-2 text-[9px] text-[#696969]"><span v-if="protocolSession.subagentActions.length">子代理 {{ protocolSession.subagentActions.join('、') }}</span><span v-if="protocolSession.decodeErrors.length" class="text-orange-200/75">解析 {{ protocolSession.decodeErrors.join('、') }}</span></div></div>
              <div class="border-l border-white/[0.045] px-3 py-2.5 tabular-nums">{{ protocolSession.eventCount }}</div><div class="border-l border-white/[0.045] px-3 py-2.5 text-[10px]"><span>上行 {{ protocolSession.upstreamCount }}</span><span class="ml-1.5">下行 {{ protocolSession.downstreamCount }}</span></div><div class="border-l border-white/[0.045] px-3 py-2.5 truncate text-[10px] text-[#8d8d8d]">{{ protocolSession.agentMode || '-' }}</div><div class="border-l border-white/[0.045] px-3 py-2.5 tabular-nums text-[10px] text-[#8d8d8d]">{{ formatModifiedTime(protocolSession.lastSeenAtUnixMs) }}</div>
              <div class="flex items-center justify-end gap-2 border-l border-white/[0.045] px-3 py-2.5"><span :class="protocolSession.terminal ? 'text-[#74d5a1]' : 'text-[#8d8d8d]'">{{ protocolSession.terminal ? '已收口' : '进行中' }}</span><button type="button" class="grid size-6 place-items-center rounded-[4px] text-[#8a8a8a] transition-colors hover:bg-white/[0.08] hover:text-white" :aria-expanded="expandedProtocolSessions.has(protocolSession.requestIdHash)" :aria-label="expandedProtocolSessions.has(protocolSession.requestIdHash) ? '收起协议事件' : '展开协议事件'" @click="toggleProtocolSession(protocolSession.requestIdHash)"><span class="icon-[mdi--chevron-down] text-[15px] transition-transform" :class="expandedProtocolSessions.has(protocolSession.requestIdHash) ? '' : '-rotate-90'" aria-hidden="true" /></button></div>
            </div>
            <div v-if="expandedProtocolSessions.has(protocolSession.requestIdHash)" class="border-t border-white/[0.045] bg-black/[0.1] px-3 py-2"><div v-for="event in protocolSession.events" :key="[event.timestampUnixMs, event.sequence, event.eventKind].join('-')" class="grid grid-cols-[78px_64px_160px_minmax(0,1fr)_64px] gap-2 border-b border-white/[0.04] py-1.5 last:border-b-0"><span class="tabular-nums text-[#777]">{{ formatTime(event.timestampUnixMs) }}</span><span :class="event.direction === 'request' ? 'text-sky-200/80' : 'text-[#74d5a1]'">{{ event.direction === 'request' ? '上行' : '下行' }}</span><span class="font-mono text-[#d3d3d3]">{{ event.eventKind }}</span><span class="min-w-0 truncate text-[#777]">{{ protocolEventDetails(event).join(' · ') || '-' }}</span><span class="text-right text-[#777]">{{ event.terminal ? '终态' : '' }}</span></div></div>
          </div>
        </div>
      </div>

      <div
        v-else-if="historyViewMode === 'icons'"
        data-testid="history-list-viewport"
        class="mt-2.5 flex min-h-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-white/[0.08] bg-[#202124]"
      >
        <div class="flex min-h-10 shrink-0 items-center gap-2 border-b border-white/[0.08] bg-[#292a2d] px-2">
          <button
            type="button"
            class="grid size-7 shrink-0 place-items-center rounded-[5px] text-[#8a8a8a] transition-colors hover:bg-white/[0.06] hover:text-white disabled:opacity-30"
            :disabled="historyPath.length === 0"
            title="返回上级"
            aria-label="返回上级"
            @click="navigateHistoryUp"
          >
            <span class="icon-[mdi--arrow-up] text-[16px]" aria-hidden="true" />
          </button>
          <nav aria-label="历史路径" class="flex min-w-0 items-center overflow-hidden rounded-[5px] border border-white/[0.07] bg-black/[0.1] px-1 py-0.5">
            <template v-for="(crumb, index) in historyBreadcrumbs" :key="crumb.key">
              <span v-if="index > 0" class="icon-[mdi--chevron-right] shrink-0 text-[14px] text-[#555]" aria-hidden="true" />
              <button
                type="button"
                class="max-w-[180px] truncate rounded-[4px] px-2 py-1 text-[11px] transition-colors hover:bg-white/[0.06] hover:text-white"
                :class="index === historyBreadcrumbs.length - 1 ? 'text-[#d0d0d0]' : 'text-[#858585]'"
                @click="navigateHistoryPath(crumb.depth)"
              >
                {{ crumb.label }}
              </button>
            </template>
          </nav>
        </div>

        <ul
          role="list"
          aria-label="历史图标视图"
          class="grid min-h-0 flex-1 auto-rows-[76px] grid-cols-1 content-start gap-x-6 gap-y-2 overflow-y-auto overscroll-contain p-3 sm:grid-cols-2"
        >
          <li
            v-for="item in currentIconItems"
            :key="item.key"
            class="min-w-0"
            :aria-selected="item.kind === 'session' ? (selectedIDs.has(item.session.id) ? 'true' : 'false') : undefined"
            :tabindex="item.kind === 'session' ? 0 : undefined"
            @click="item.kind === 'session' ? toggleSession(item.session.id) : undefined"
            @dblclick="item.kind === 'session' ? handleIconSessionDoubleClick(item.session) : undefined"
            @keydown.space.prevent="item.kind === 'session' ? toggleSession(item.session.id) : undefined"
          >
            <button
              v-if="item.kind === 'folder'"
              type="button"
              class="group flex h-[76px] w-full min-w-0 items-center gap-3 rounded-[5px] px-3 text-left transition-colors hover:bg-white/[0.055] focus-visible:bg-white/[0.055] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[#10AD5D]/40"
              :aria-label="`${item.label}，${item.sessionCount} 个会话`"
              @dblclick="enterHistoryFolder(item)"
            >
              <span class="relative grid size-12 shrink-0 place-items-center text-[#e5b93f]">
                <span class="icon-[mdi--folder] text-[44px] drop-shadow-[0_2px_1px_rgba(0,0,0,0.35)]" aria-hidden="true" />
              </span>
              <span class="min-w-0">
                <span class="block truncate text-xs text-[#e3e3e3]">{{ item.label }}</span>
                <span class="mt-1 block text-[10px] tabular-nums text-[#6f6f6f]">{{ item.sessionCount }} 个会话</span>
              </span>
            </button>

            <div
              v-else
              class="group relative flex h-[76px] min-w-0 cursor-pointer items-center gap-3 rounded-[5px] border px-3 transition-colors"
              :class="selectedIDs.has(item.session.id) ? 'border-[#10AD5D]/35 bg-[#10AD5D]/[0.12]' : 'border-transparent hover:border-white/[0.08] hover:bg-white/[0.05]'"
            >
              <input
                type="checkbox"
                class="absolute left-1.5 top-1.5 size-3.5 accent-[#10AD5D]"
                :checked="selectedIDs.has(item.session.id)"
                :aria-label="`选择会话：${item.label}`"
                @click.stop
                @change="toggleSession(item.session.id)"
              />
              <span class="grid size-12 shrink-0 place-items-center text-[#77aee8]">
                <span class="icon-[mdi--file-document-outline] text-[40px]" aria-hidden="true" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block truncate text-xs text-[#e3e3e3]" :title="item.label">{{ item.label }}</span>
                <span class="mt-1 flex min-w-0 items-center gap-2 text-[10px] text-[#707070]">
                  <span v-if="item.session.statusPresentation.label" class="shrink-0">{{ item.session.statusPresentation.label }}</span>
                  <span class="truncate tabular-nums">{{ formatModifiedTime(item.session.updatedAtUnixMs || item.session.createdAtUnixMs) }}</span>
                  <span v-if="item.session.hasDebug" class="icon-[mdi--bug-outline] shrink-0 text-[12px] text-amber-300/65" title="包含调试日志" aria-label="包含调试日志" />
                </span>
              </span>
            </div>
          </li>
        </ul>
      </div>

      <div
        v-else
        data-testid="history-list-viewport"
        class="mt-2.5 min-h-0 flex-1 overflow-auto overscroll-contain rounded-[8px] border border-white/[0.08] bg-[#202124]"
      >
        <div role="grid" aria-label="历史详细信息列表" class="min-w-[920px] text-[11px] text-[#b8b8b8]">
          <div role="row" class="sticky top-0 z-10 grid grid-cols-[minmax(320px,1fr)_90px_138px_112px_112px_86px] border-b border-white/[0.09] bg-[#292a2d] text-[#9a9a9a] shadow-[0_1px_0_rgba(0,0,0,0.35)]">
            <div role="columnheader" class="border-r border-white/[0.06] px-3 py-2">名称</div>
            <div role="columnheader" class="border-r border-white/[0.06] px-3 py-2">状态</div>
            <div role="columnheader" class="border-r border-white/[0.06] px-3 py-2">修改日期</div>
            <div role="columnheader" class="border-r border-white/[0.06] px-3 py-2">类型</div>
            <div role="columnheader" class="border-r border-white/[0.06] px-3 py-2">调试日志</div>
            <div role="columnheader" class="px-3 py-2 text-right">大小</div>
          </div>

          <div v-for="year in groupedTree" :key="year.key" role="rowgroup">
            <div role="row" class="border-b border-white/[0.055] bg-white/[0.018]">
              <div role="gridcell" class="col-span-full">
                <button type="button" class="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-medium text-[#e3e3e3] hover:bg-white/[0.04]" :aria-expanded="!isCollapsed(year.key)" @click="toggleCollapsed(year.key)">
                  <span class="icon-[mdi--chevron-down] text-[15px] text-[#777] transition-transform" :class="isCollapsed(year.key) ? '-rotate-90' : ''" aria-hidden="true" />
                  <span class="icon-[mdi--folder-outline] text-[15px] text-[#d6b35f]" aria-hidden="true" />
                  <span>{{ year.label }}</span>
                  <span class="ml-1 text-[10px] font-normal tabular-nums text-[#686868]">{{ year.sessionCount }} 项</span>
                </button>
              </div>
            </div>
            <template v-if="!isCollapsed(year.key)">
              <div v-for="month in year.months" :key="month.key" role="rowgroup">
                <div role="row" class="border-b border-white/[0.05]">
                  <div role="gridcell" class="col-span-full">
                    <button type="button" class="flex w-full items-center gap-2 py-1.5 pl-7 pr-3 text-left text-[#c0c0c0] hover:bg-white/[0.035]" :aria-expanded="!isCollapsed(month.key)" @click="toggleCollapsed(month.key)">
                      <span class="icon-[mdi--chevron-down] text-[14px] text-[#666] transition-transform" :class="isCollapsed(month.key) ? '-rotate-90' : ''" aria-hidden="true" />
                      <span class="icon-[mdi--folder-outline] text-[14px] text-[#b99a52]" aria-hidden="true" />
                      <span>{{ formatMonthLabel(month.label) }}</span>
                      <span class="ml-1 text-[10px] tabular-nums text-[#626262]">{{ month.sessionCount }} 项</span>
                    </button>
                  </div>
                </div>
                <template v-if="!isCollapsed(month.key)">
                  <div v-for="day in month.days" :key="day.key" role="rowgroup">
                    <div role="row" class="border-b border-white/[0.045]">
                      <div role="gridcell" class="col-span-full">
                        <button type="button" class="flex w-full items-center gap-2 py-1.5 pl-11 pr-3 text-left text-[#8e8e8e] hover:bg-white/[0.03]" :aria-expanded="!isCollapsed(day.key)" @click="toggleCollapsed(day.key)">
                          <span class="icon-[mdi--chevron-down] text-[13px] text-[#5e5e5e] transition-transform" :class="isCollapsed(day.key) ? '-rotate-90' : ''" aria-hidden="true" />
                          <span>{{ formatDayLabel(day.label) }}</span>
                          <span class="ml-1 text-[10px] tabular-nums text-[#595959]">{{ day.sessionCount }} 项</span>
                        </button>
                      </div>
                    </div>
                    <label
                      v-for="session in !isCollapsed(day.key) ? visibleSessions(day) : []"
                      :key="session.id"
                      role="row"
                      :aria-selected="selectedIDs.has(session.id) ? 'true' : 'false'"
                      class="group grid cursor-pointer grid-cols-[minmax(320px,1fr)_90px_138px_112px_112px_86px] border-b border-white/[0.045] transition-colors hover:bg-white/[0.045]"
                      :class="selectedIDs.has(session.id) ? 'bg-[#10AD5D]/[0.12] hover:bg-[#10AD5D]/[0.15]' : session.statusPresentation.row"
                    >
                      <div role="gridcell" class="flex min-w-0 items-center gap-2 border-r border-white/[0.045] py-2 pl-12 pr-3">
                        <input type="checkbox" class="size-3.5 shrink-0 accent-[#10AD5D]" :checked="selectedIDs.has(session.id)" @change="toggleSession(session.id)" />
                        <span class="icon-[mdi--message-text-outline] shrink-0 text-[14px] text-[#777]" aria-hidden="true" />
                        <div class="min-w-0">
                          <div class="truncate text-xs text-[#e0e0e0]" :title="sessionTitle(session)">{{ sessionTitle(session) }}</div>
                          <div class="mt-0.5 flex min-w-0 items-center gap-2 text-[9px] text-[#626262]">
                            <button type="button" class="min-w-0 truncate hover:text-[#bdbdbd]" :title="`复制会话 ID：${session.id}`" @click.stop="copyID(session.id, '会话 ID')">{{ shortID(session.id) }}</button>
                            <button v-if="session.requestId" type="button" class="min-w-0 truncate hover:text-[#bdbdbd]" :title="`复制请求 ID：${session.requestId}`" @click.stop="copyID(session.requestId, '请求 ID')">请求 {{ shortID(session.requestId) }}</button>
                          </div>
                        </div>
                      </div>
                      <div role="gridcell" class="flex items-center border-r border-white/[0.045] px-3">
                        <span v-if="session.statusPresentation.label" class="rounded-[4px] border px-1.5 py-0.5 text-[9px]" :class="session.statusPresentation.badge">{{ session.statusPresentation.label }}</span>
                        <span v-else class="text-[#6f6f6f]">已完成</span>
                      </div>
                      <div role="gridcell" class="flex items-center border-r border-white/[0.045] px-3 tabular-nums text-[#8f8f8f]">{{ formatModifiedTime(session.updatedAtUnixMs || session.createdAtUnixMs) }}</div>
                      <div role="gridcell" class="flex min-w-0 items-center border-r border-white/[0.045] px-3"><span class="truncate" :title="sessionType(session)">{{ sessionType(session) }}</span></div>
                      <div role="gridcell" class="flex items-center border-r border-white/[0.045] px-3">
                        <button v-if="session.hasDebug" type="button" class="flex items-center gap-1 text-amber-300/70 hover:text-amber-200" title="在诊断中打开该会话的调试日志" @click.prevent.stop="openInDiagnostics(session.id)"><span class="icon-[mdi--bug-outline] text-[12px]" /><span>{{ formatSize(session.debugSizeBytes) }}</span></button>
                        <span v-else class="text-[#5e5e5e]">-</span>
                      </div>
                      <div role="gridcell" class="flex items-center justify-end px-3 tabular-nums text-[#8b8b8b]">{{ formatSize(session.sizeBytes) }}</div>
                    </label>
                    <div v-if="!isCollapsed(day.key) && hasMoreSessions(day)" role="row" class="border-b border-white/[0.045]">
                      <div role="gridcell" class="col-span-full flex justify-center py-2">
                        <button type="button" class="px-3 py-1 text-[11px] text-[#9ecfb5] hover:text-[#c8f2da]" @click="loadMoreSessions(day)">加载更多</button>
                      </div>
                    </div>
                  </div>
                </template>
              </div>
            </template>
          </div>
        </div>
      </div>

    </div>
  </Card>
</template>
