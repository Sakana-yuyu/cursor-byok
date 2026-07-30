<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { useMessage } from "@/composables/useMessage";
import { cancelDelegationTask, getDelegationTaskSnapshots } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, reactive } from "vue";

const state = reactive({ items: [], error: "", canceling: {} });
const message = useMessage();
let refreshTimer = 0;
let refreshBusy = false;
let generation = 0;

const visibleItems = computed(() => [...state.items]
  .sort((left, right) => {
    if (Boolean(left.cancelable) !== Boolean(right.cancelable)) return left.cancelable ? -1 : 1;
    return Number(right.queuedAtUnixMs || 0) - Number(left.queuedAtUnixMs || 0);
  })
  .slice(0, 4));
const activeCount = computed(() => state.items.filter((item) => item.cancelable).length);

const statusLabels = {
  queued: "等待中",
  running: "运行中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
  timed_out: "超时",
};

function statusClass(status) {
  if (status === "completed") return "text-[#6ee7a5]";
  if (status === "failed" || status === "timed_out") return "text-[#fca5a5]";
  if (status === "running") return "text-[#facc15]";
  return "text-[#a3a3a3]";
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
});

onUnmounted(() => window.clearInterval(refreshTimer));
</script>

<template>
  <Card v-if="visibleItems.length || state.error">
    <div class="min-w-0 space-y-3">
      <div class="flex items-center justify-between gap-3">
        <h2 class="text-sm font-medium text-white">Multitask 委派</h2>
        <span class="text-xs text-[#858585]">{{ activeCount }} 运行中</span>
      </div>
      <div v-if="state.error" class="break-words text-xs text-[#fca5a5]">{{ state.error }}</div>
      <div v-if="visibleItems.length" class="grid gap-2 md:grid-cols-2 xl:grid-cols-4">
        <div v-for="item in visibleItems" :key="item.id" class="flex min-w-0 items-center gap-2 rounded-[6px] border border-white/10 bg-black/15 px-3 py-2">
          <span class="size-2 shrink-0 rounded-full" :class="item.cancelable ? 'bg-[#facc15]' : 'bg-[#525252]'" />
          <div class="min-w-0 flex-1">
            <div class="truncate text-xs text-white" :title="item.modelName || item.modelId">{{ item.modelName || item.modelId || "模型" }}</div>
            <div class="mt-0.5 text-[11px]" :class="statusClass(item.status)">{{ statusLabels[item.status] || item.status }}</div>
          </div>
          <Button v-if="item.cancelable" variant="text" :disabled="Boolean(state.canceling[item.id])" @click="handleCancel(item)">{{ state.canceling[item.id] ? "取消中..." : "取消" }}</Button>
        </div>
      </div>
    </div>
  </Card>
</template>
