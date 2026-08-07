<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Select from "@/components/ui/Select.vue";
import { getGoals, startGoal, stopGoal } from "@/services/clientApi";
import { appState } from "@/state/appState";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();

const goalText = ref("");
const modelID = ref("");
const goals = ref([]);
const starting = ref(false);
const stopping = ref(new Set());
const loadError = ref("");
const actionError = ref("");

const modelOptions = computed(() =>
  appState.modelAdapters.map((adapter) => ({
    label: adapter.displayName || adapter.modelID || adapter.id,
    value: adapter.id,
  })),
);

const canStart = computed(() => goalText.value.trim() !== "" && modelID.value !== "" && !starting.value);

function statusLabel(status) {
  return (
    {
      running: "执行中",
      completed: "已完成",
      failed: "失败",
      budget_exceeded: "预算超限",
      stopped: "已停止",
    }[status] || status || "未知"
  );
}

async function refreshGoals() {
  try {
    goals.value = (await getGoals()) || [];
    loadError.value = "";
  } catch (error) {
    loadError.value = String(error?.message || error);
  }
}

async function handleStart() {
  const text = goalText.value.trim();
  if (!text || !modelID.value) return;
  starting.value = true;
  actionError.value = "";
  try {
    await startGoal(text, modelID.value);
    goalText.value = "";
    await refreshGoals();
  } catch (error) {
    actionError.value = String(error?.message || error);
  } finally {
    starting.value = false;
  }
}

async function handleStop(conversationID) {
  stopping.value.add(conversationID);
  actionError.value = "";
  try {
    await stopGoal(conversationID);
    await refreshGoals();
  } catch (error) {
    actionError.value = String(error?.message || error);
  } finally {
    stopping.value.delete(conversationID);
  }
}

let pollTimer = null;
onMounted(() => {
  void refreshGoals();
  pollTimer = window.setInterval(() => void refreshGoals(), 3000);
});
onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer);
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-white">目标执行（Goal）</h1>
        <p class="text-sm text-[#8f8f8f]">让 Agent 带目标循环执行，直到任务真正完成；进度与结果实时可见。</p>
      </div>
      <div class="center-row gap-2">
        <Button variant="default" @click="router.push('/settings?category=goal')">Goal 设置</Button>
        <Button variant="primary" @click="router.push('/')">返回首页</Button>
      </div>
    </div>

    <Card class="shrink-0">
      <div class="flex flex-col gap-3">
        <h2 class="text-sm font-medium text-white">发起 Goal</h2>
        <textarea
          v-model="goalText"
          rows="3"
          class="w-full resize-none rounded-[6px] border border-[#3f3f3f] bg-[#232323] p-3 text-sm text-[#e5e5e5] outline-none transition-colors focus:border-[#10AD5D]"
          placeholder="输入目标，例如：跑通全部测试并修复失败用例"
        />
        <div class="flex flex-wrap items-center gap-2">
          <Select v-model="modelID" :options="modelOptions" placeholder="选择执行模型" class="min-w-[220px]" />
          <Button variant="primary" :disabled="!canStart" @click="handleStart">开始执行</Button>
        </div>
        <p v-if="actionError" class="text-sm text-red-400">{{ actionError }}</p>
      </div>
    </Card>

    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <p v-if="loadError" class="mb-2 text-sm text-red-400">{{ loadError }}</p>
      <div v-if="goals.length === 0" class="flex min-h-[160px] items-center justify-center rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] text-sm text-[#a3a3a3]">
        暂无 goal 记录，发起后这里会实时显示进度。
      </div>
      <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(320px,1fr))]">
        <Card v-for="goal in goals" :key="goal.conversationId">
          <div class="flex flex-col gap-2">
            <div class="flex items-center justify-between gap-3">
              <span class="rounded-[6px] border border-[#343434] px-2 py-1 text-xs" :class="goal.status === 'running' ? 'border-[#10AD5D]/40 text-[#10d06f]' : 'text-[#a3a3a3]'">
                {{ statusLabel(goal.status) }}
              </span>
              <span class="shrink-0 text-xs text-[#777]">{{ goal.startedAt }}</span>
            </div>
            <p class="text-sm font-medium text-white">{{ goal.goalText }}</p>
            <p class="text-xs text-[#a3a3a3]">
              pass {{ goal.providerPasses }} · 工具调用 {{ goal.toolCalls }} · 自检 {{ goal.selfChecks }} · 费用 ${{ Number(goal.costEstimateUsd ?? 0).toFixed(4) }}
            </p>
            <p v-if="goal.lastProgress" class="truncate text-xs text-[#8f8f8f]">最近进展：{{ goal.lastProgress }}</p>
            <p v-if="goal.completionText" class="max-h-24 overflow-y-auto whitespace-pre-wrap text-xs text-[#a3a3a3]">{{ goal.completionText }}</p>
            <p v-if="goal.stopReason" class="text-xs text-[#f59e0b]">{{ goal.stopReason }}</p>
            <div v-if="goal.status === 'running'" class="mt-1">
              <Button variant="danger" :disabled="stopping.has(goal.conversationId)" @click="handleStop(goal.conversationId)">停止</Button>
            </div>
          </div>
        </Card>
      </div>
    </div>
  </div>
</template>