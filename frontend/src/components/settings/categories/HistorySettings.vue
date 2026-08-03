<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { clearHistory, deleteHistorySessions, getHistorySessions } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref } from "vue";

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
const collapsed = reactive(new Set());

const totalSizeBytes = computed(() =>
  sessions.value.reduce((total, session) => total + Number(session.sizeBytes || 0), 0),
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
  return sortedYears.map(([year, monthsMap]) => ({
    key: year,
    label: year,
    months: [...monthsMap.entries()]
      .sort(([left], [right]) => right.localeCompare(left))
      .map(([month, daysMap]) => ({
        key: month,
        label: month,
        days: [...daysMap.entries()]
          .sort(([left], [right]) => right.localeCompare(left))
          .map(([day, items]) => ({
            key: day,
            label: day,
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
            {{ sessions.length }} 个会话 · 占用 {{ formatSize(totalSizeBytes) }}
          </p>
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
            class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium text-white"
            @click="toggleCollapsed(year.key)"
          >
            <span
              class="icon-[mdi--chevron-down] text-[14px] text-[#858585] transition-transform"
              :class="isCollapsed(year.key) ? '-rotate-90' : ''"
              aria-hidden="true"
            />
            {{ year.label }}
          </button>
          <div v-if="!isCollapsed(year.key)" class="space-y-2 border-t border-white/10 px-3 py-2">
            <div v-for="month in year.months" :key="month.key" class="rounded-[6px] bg-white/[0.03]">
              <button
                type="button"
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-[#d4d4d4]"
                @click="toggleCollapsed(month.key)"
              >
                <span
                  class="icon-[mdi--chevron-down] text-[13px] text-[#858585] transition-transform"
                  :class="isCollapsed(month.key) ? '-rotate-90' : ''"
                  aria-hidden="true"
                />
                {{ formatMonthLabel(month.label) }}
              </button>
              <div v-if="!isCollapsed(month.key)" class="space-y-1.5 px-2 pb-2">
                <div v-for="day in month.days" :key="day.key" class="rounded-[6px] bg-white/[0.03]">
                  <button
                    type="button"
                    class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-[#a3a3a3]"
                    @click="toggleCollapsed(day.key)"
                  >
                    <span
                      class="icon-[mdi--chevron-down] text-[13px] text-[#858585] transition-transform"
                      :class="isCollapsed(day.key) ? '-rotate-90' : ''"
                      aria-hidden="true"
                    />
                    {{ formatDayLabel(day.label) }}
                  </button>
                  <div v-if="!isCollapsed(day.key)" class="space-y-1 px-1 pb-1.5">
                    <label
                      v-for="session in day.sessions"
                      :key="session.id"
                      class="flex cursor-pointer items-center gap-2 rounded-[6px] border border-white/5 bg-black/15 px-3 py-2 hover:border-white/15"
                    >
                      <input
                        type="checkbox"
                        class="shrink-0 accent-[#10AD5D]"
                        :checked="selectedIDs.has(session.id)"
                        @change="toggleSession(session.id)"
                      />
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
                          <span class="shrink-0 text-[11px] text-[#858585]">{{ formatSize(session.sizeBytes) }}</span>
                        </div>
                        <div class="mt-0.5 flex items-center gap-2 text-[11px] text-[#858585]">
                          <span>{{ formatTime(session.updatedAtUnixMs || session.createdAtUnixMs) }}</span>
                          <span v-if="session.hasDebug" class="rounded-[4px] bg-[#3a2c14] px-1.5 py-0.5 text-[10px] text-[#d4a34a]">
                            调试
                          </span>
                          <span class="truncate text-[#5f5f5f]" :title="session.id">{{ session.id }}</span>
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
      </div>
    </div>
  </Card>
</template>