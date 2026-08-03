<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import FullScreenModal from "@/components/ui/FullScreenModal.vue";
import { useMessage } from "@/composables/useMessage";
import { cancelDelegationTask, getDelegationTaskSnapshots } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";

const state = reactive({ items: [], error: "", canceling: {} });
const message = useMessage();
let refreshTimer = 0;
let refreshBusy = false;
let generation = 0;

// 轮换展示：最多保留 4 条，一次显示一条，定时轮换。
const visibleItems = computed(() =>
  [...state.items]
    .sort((left, right) => {
      if (Boolean(left.cancelable) !== Boolean(right.cancelable)) return left.cancelable ? -1 : 1;
      return Number(right.queuedAtUnixMs || 0) - Number(left.queuedAtUnixMs || 0);
    })
    .slice(0, 4),
);
const currentItem = computed(() => visibleItems.value[currentIndex.value] || null);
const activeCount = computed(() => state.items.filter((item) => item.cancelable).length);
const currentIndex = ref(0);
const rotating = ref(true);
const detailVisible = ref(false);
const detailItem = ref(null);
let rotateTimer = 0;
const ROTATE_INTERVAL_MS = 4000;

// 任务列表变化时重置轮换索引，避免越界或停留在已消失的任务上。
watch(visibleItems, () => {
  if (currentIndex.value >= visibleItems.value.length) {
    currentIndex.value = 0;
  }
});

function startRotating() {
  stopRotating();
  if (!rotating.value || visibleItems.value.length <= 1) return;
  rotateTimer = window.setInterval(() => {
    if (visibleItems.value.length > 1) {
      currentIndex.value = (currentIndex.value + 1) % visibleItems.value.length;
    }
  }, ROTATE_INTERVAL_MS);
}
function stopRotating() {
  if (rotateTimer) {
    window.clearInterval(rotateTimer);
    rotateTimer = 0;
  }
}

function rotatePrev() {
  if (visibleItems.value.length <= 1) return;
  currentIndex.value = (currentIndex.value - 1 + visibleItems.value.length) % visibleItems.value.length;
}

function rotateNext() {
  if (visibleItems.value.length <= 1) return;
  currentIndex.value = (currentIndex.value + 1) % visibleItems.value.length;
}

function openDetail(item) {
  detailItem.value = item;
  detailVisible.value = true;
}
function closeDetail() {
  detailVisible.value = false;
  detailItem.value = null;
}

const statusLabels = {
  queued: "等待中",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  timed_out: "超时",
};

function taskStateLabel(item) {
  if (item?.cancelable || item?.status === "queued" || item?.status === "running") {
    return statusLabels[item?.status] || item?.status;
  }
  return item?.supervisionPhase || statusLabels[item?.status] || item?.status;
}

function taskStateClass(item) {
  if (item?.cancelable || item?.status === "queued" || item?.status === "running") {
    return "text-[#facc15]";
  }
  const phase = item?.supervisionPhase;
  if (phase === "completed") return "text-[#6ee7a5]";
  if (phase === "failed" || phase === "canceled" || phase === "circuit_open") return "text-[#fca5a5]";
  if (["reviewing", "correcting", "retrying", "reassigning", "checkpointing", "running", "escalated"].includes(phase)) {
    return "text-[#facc15]";
  }
  return "text-[#a3a3a3]";
}

// 卡片左侧状态竖条与徽章圆点的状态色。
function statusAccentClass(item) {
  if (item?.cancelable || item?.status === "queued" || item?.status === "running") return "bg-[#facc15]";
  const phase = item?.supervisionPhase;
  if (phase === "completed" || item?.status === "completed") return "bg-[#4ade80]";
  if (phase === "failed" || phase === "canceled" || phase === "circuit_open" || item?.status === "failed" || item?.status === "timed_out") return "bg-[#f87171]";
  return "bg-[#525252]";
}

// 状态徽章的边框与底色。
function statusPillClass(item) {
  if (item?.cancelable || item?.status === "queued" || item?.status === "running") return "border-[#4b3a14] bg-[#2a2313] text-[#facc15]";
  const phase = item?.supervisionPhase;
  if (phase === "completed" || item?.status === "completed") return "border-[#14432c] bg-[#12291d] text-[#6ee7a5]";
  if (phase === "failed" || phase === "canceled" || phase === "circuit_open" || item?.status === "failed" || item?.status === "timed_out") return "border-[#4b1d1d] bg-[#2a1313] text-[#fca5a5]";
  return "border-white/10 bg-white/5 text-[#a3a3a3]";
}

function supervisionParts(item) {
  const parts = [];
  if (item?.workerRole) parts.push(item.workerRole);
  if (item?.supervisionRound) parts.push(`r${item.supervisionRound}`);
  if (Number.isFinite(item?.correctionCount)) parts.push(`c${item.correctionCount}`);
  if (Number.isFinite(item?.retryCount)) parts.push(`rt${item.retryCount}`);
  if (Number.isFinite(item?.reassignCount)) parts.push(`ra${item.reassignCount}`);
  if (Number.isFinite(item?.escalateCount)) parts.push(`e${item.escalateCount}`);
  return parts;
}

function compactSupervision(item) {
  return supervisionParts(item).join(" · ");
}

function taskDescription(item) {
  return item?.description || item?.progressSummary || item?.issueCategory || "委派任务";
}

function taskModel(item) {
  return item?.modelName || item?.modelId || "模型";
}

function executionModeLabel(item) {
  if (item?.executionMode === "cursor") return "Cursor 子会话";
  if (item?.executionMode === "vision") return "视觉委派";
  return "本地子代理";
}

function executionModeIcon(item) {
  if (item?.executionMode === "cursor") return "icon-[mdi--cursor-default-outline]";
  if (item?.executionMode === "vision") return "icon-[mdi--eye-outline]";
  return "icon-[mdi--account-outline]";
}

function formatDuration(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return "—";
  if (value < 1000) return `${value}ms`;
  const seconds = Math.floor(value / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

async function refresh() {
  if (refreshBusy) return;
  const currentGeneration = generation;
  refreshBusy = true;
  try {
    const items = await getDelegationTaskSnapshots();
    if (currentGeneration !== generation) return;
    state.items = Array.isArray(items) ? items : [];
    state.error = "";
  } catch (error) {
    state.error = toUserError(error);
  } finally {
    refreshBusy = false;
  }
}

async function handleCancel(item) {
  if (!item?.id || state.canceling[item.id]) return;
  state.canceling[item.id] = true;
  generation += 1;
  try {
    const canceled = await cancelDelegationTask(item.id);
    if (!canceled) throw new Error("任务已结束或不存在");
    message.success("已取消");
    generation += 1;
    await refresh();
  } catch (error) {
    state.error = toUserError(error);
  } finally {
    delete state.canceling[item.id];
  }
}

onMounted(() => {
  void refresh();
  refreshTimer = window.setInterval(() => void refresh(), 1500);
  startRotating();
});

onUnmounted(() => {
  window.clearInterval(refreshTimer);
  stopRotating();
});
</script>

<template>
  <Card v-if="visibleItems.length || state.error">
    <div class="flex min-w-0 flex-col gap-3">
      <div class="flex items-center justify-between gap-3">
        <div class="center-row min-w-0 gap-2">
          <span class="icon-[mdi--progress-clock] shrink-0 text-[15px] text-[#a3a3a3]" aria-hidden="true" />
          <h2 class="text-sm font-medium text-white">委派任务</h2>
        </div>
        <span
          class="center-row shrink-0 gap-1.5 rounded-full border border-white/10 bg-black/15 px-2.5 py-1 text-xs text-[#a3a3a3]"
          :title="activeCount ? '有正在运行的任务' : '当前没有运行中的任务'"
        >
          <span class="size-1.5 rounded-full transition-colors" :class="activeCount ? 'animate-pulse bg-[#22c55e]' : 'bg-[#525252]'" />
          {{ activeCount }} 运行中
        </span>
      </div>
      <div v-if="state.error" class="break-words rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ state.error }}</div>
      <div v-if="visibleItems.length" class="flex min-w-0 items-stretch gap-2">
        <button
          type="button"
          class="flex w-7 shrink-0 cursor-pointer items-center justify-center rounded-[7px] border border-white/10 bg-black/15 text-[#a3a3a3] transition-all hover:border-[#10AD5D]/40 hover:bg-black/25 hover:text-white active:scale-95 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-white/10 disabled:hover:bg-black/15 disabled:hover:text-[#a3a3a3]"
          :disabled="visibleItems.length <= 1"
          aria-label="上一个任务"
          title="上一个任务"
          @click="rotatePrev"
        >
          <span class="icon-[mdi--chevron-left] text-[16px]" aria-hidden="true" />
        </button>

        <div
          class="relative flex h-[136px] min-w-0 flex-1 cursor-pointer flex-col overflow-hidden rounded-[7px] border border-white/10 bg-black/15 transition-colors hover:border-[#10AD5D]/35 hover:bg-black/25"
          @mouseenter="rotating = false; stopRotating()"
          @mouseleave="rotating = true; startRotating()"
          @click="openDetail(currentItem)"
          :title="'点击查看任务详情'"
        >
          <span
            class="absolute inset-y-0 left-0 w-[3px] transition-colors"
            :class="currentItem ? statusAccentClass(currentItem) : 'bg-[#3f3f3f]'"
            aria-hidden="true"
          />
          <template v-if="currentItem">
            <div :key="currentItem.id" class="task-strip-in flex min-w-0 flex-1 flex-col pl-3.5 pr-3 pt-2.5">
              <div class="flex min-w-0 items-center gap-2">
                <div class="min-w-0 flex-1 truncate text-[13px] font-medium text-white" :title="taskDescription(currentItem)">
                  {{ taskDescription(currentItem) }}
                </div>
                <span
                  class="center-row shrink-0 gap-1 rounded-[5px] border border-white/10 bg-white/5 px-1.5 py-0.5 text-[11px] text-[#a3a3a3]"
                  :title="taskModel(currentItem)"
                >
                  <span class="icon-[mdi--robot-outline] text-[12px] text-[#737373]" aria-hidden="true" />
                  <span class="max-w-[160px] truncate">{{ taskModel(currentItem) }}</span>
                </span>
                <Button
                  v-if="currentItem.cancelable"
                  variant="text"
                  class="!text-[12px]"
                  :disabled="Boolean(state.canceling[currentItem.id])"
                  @click.stop="handleCancel(currentItem)"
                >
                  {{ state.canceling[currentItem.id] ? "取消中..." : "取消" }}
                </Button>
              </div>
              <div class="mt-1.5 flex min-w-0 items-center gap-2">
                <span
                  class="center-row shrink-0 gap-1.5 rounded-full border px-2 py-[2px] text-[11px] font-medium"
                  :class="statusPillClass(currentItem)"
                  :title="taskStateLabel(currentItem)"
                >
                  <span class="size-1.5 rounded-full" :class="statusAccentClass(currentItem)" aria-hidden="true" />
                  {{ taskStateLabel(currentItem) }}
                </span>
                <span class="center-row shrink-0 gap-1 text-[11px] text-[#737373]" :title="executionModeLabel(currentItem)">
                  <span :class="executionModeIcon(currentItem)" class="text-[12px]" aria-hidden="true" />
                  {{ executionModeLabel(currentItem) }}
                </span>
              </div>
              <div v-if="currentItem.isSupervised" class="mt-1.5 flex min-w-0 flex-wrap items-center gap-1">
                <span
                  v-for="part in supervisionParts(currentItem)"
                  :key="part"
                  class="shrink-0 rounded-[4px] bg-white/5 px-1.5 py-[2px] text-[10px] text-[#858585]"
                >
                  {{ part }}
                </span>
              </div>
              <div
                v-if="currentItem.progressSummary"
                class="mt-1.5 line-clamp-2 break-words text-[11px] leading-[15px] text-[#858585]"
                :title="currentItem.progressSummary"
              >
                {{ currentItem.progressSummary }}
              </div>
            </div>
          </template>
          <div class="mt-auto flex shrink-0 items-center justify-center gap-1.5 pb-2 pt-1.5">
            <button
              v-for="(item, index) in visibleItems"
              :key="item.id"
              type="button"
              class="h-2 rounded-full transition-all duration-300"
              :class="index === currentIndex ? 'w-5 bg-[#10AD5D]' : 'w-2 bg-[#3f3f3f] hover:bg-[#5a5a5a]'"
              :aria-label="`查看任务 ${index + 1}`"
              @click.stop="currentIndex = index"
            />
            <span class="ml-1 text-[10px] tabular-nums text-[#666]">{{ currentIndex + 1 }}/{{ visibleItems.length }}</span>
          </div>
        </div>

        <button
          type="button"
          class="flex w-7 shrink-0 cursor-pointer items-center justify-center rounded-[7px] border border-white/10 bg-black/15 text-[#a3a3a3] transition-all hover:border-[#10AD5D]/40 hover:bg-black/25 hover:text-white active:scale-95 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-white/10 disabled:hover:bg-black/15 disabled:hover:text-[#a3a3a3]"
          :disabled="visibleItems.length <= 1"
          aria-label="下一个任务"
          title="下一个任务"
          @click="rotateNext"
        >
          <span class="icon-[mdi--chevron-right] text-[16px]" aria-hidden="true" />
        </button>
      </div>
    </div>
  </Card>

  <FullScreenModal
    :visible="detailVisible"
    title="委派任务详情"
    max-width="640px"
    @close="closeDetail"
  >
    <div v-if="detailItem" class="flex flex-col gap-4 p-5">
      <div>
        <div class="text-[11px] uppercase tracking-wide text-[#737373]">任务描述</div>
        <div class="mt-1 text-sm text-white">{{ taskDescription(detailItem) }}</div>
      </div>
      <div class="grid grid-cols-2 gap-3 text-xs sm:grid-cols-3">
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">状态</div>
          <div class="mt-1 font-medium" :class="taskStateClass(detailItem)">{{ taskStateLabel(detailItem) }}</div>
        </div>
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">执行模型</div>
          <div class="mt-1 break-words text-[#d4d4d4]">{{ taskModel(detailItem) }}</div>
        </div>
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">执行方式</div>
          <div class="mt-1 text-[#d4d4d4]">{{ executionModeLabel(detailItem) }}</div>
        </div>
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">耗时</div>
          <div class="mt-1 text-[#d4d4d4]">{{ formatDuration(detailItem.durationMs) }}</div>
        </div>
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">工具调用</div>
          <div class="mt-1 text-[#d4d4d4]">{{ detailItem.toolCallCount || 0 }} 次</div>
        </div>
        <div class="rounded-[6px] border border-white/10 bg-black/15 p-2.5">
          <div class="text-[10px] uppercase tracking-wide text-[#666]">任务 ID</div>
          <div class="mt-1 break-all font-mono text-[11px] text-[#a3a3a3]">{{ detailItem.id }}</div>
        </div>
      </div>
      <div v-if="detailItem.isSupervised" class="rounded-[6px] border border-white/10 bg-black/15 p-2.5 text-xs">
        <div class="text-[10px] uppercase tracking-wide text-[#666]">监督信息</div>
        <div class="mt-1 break-words text-[#d4d4d4]">{{ compactSupervision(detailItem) }}</div>
      </div>
      <div v-if="detailItem.issueCategory" class="rounded-[6px] border border-[#4b3a14] bg-[#2a2313] p-2.5 text-xs">
        <div class="text-[10px] uppercase tracking-wide text-[#facc15]">问题分类</div>
        <div class="mt-1 break-words text-[#facc15]">{{ detailItem.issueCategory }}</div>
      </div>
      <div v-if="detailItem.progressSummary" class="rounded-[6px] border border-white/10 bg-black/15 p-2.5 text-xs">
        <div class="text-[10px] uppercase tracking-wide text-[#666]">进度</div>
        <div class="mt-1 whitespace-pre-wrap break-words text-[#d4d4d4]">{{ detailItem.progressSummary }}</div>
      </div>
      <div v-if="detailItem.error" class="rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] p-2.5 text-xs">
        <div class="text-[10px] uppercase tracking-wide text-[#fca5a5]">错误</div>
        <div class="mt-1 whitespace-pre-wrap break-words text-[#fca5a5]">{{ detailItem.error }}</div>
      </div>
      <div class="flex justify-end gap-2">
        <Button v-if="detailItem.cancelable" variant="default" :disabled="Boolean(state.canceling[detailItem.id])" @click="handleCancel(detailItem)">
          {{ state.canceling[detailItem.id] ? "取消中..." : "取消任务" }}
        </Button>
        <Button variant="primary" @click="closeDetail">关闭</Button>
      </div>
    </div>
  </FullScreenModal>
</template>

<style scoped>
@keyframes task-strip-in {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
.task-strip-in {
  animation: task-strip-in 0.22s ease-out;
}
</style>