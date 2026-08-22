<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import { cancelAgentRun, exportSanitizedAgentRunReport, getAgentRuns, prepareAgentRunRetry } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
const runs = ref([]);
const loading = ref(true);

const STATUS_STYLE = {
  running: "border-[#2f4a6b] bg-[#152238] text-[#93c5fd]",
  succeeded: "border-[#2f6b49] bg-[#1f3a2c] text-[#7dd3a0]",
  failed: "border-[#5c2b2b] bg-[#2a1515] text-[#fca5a5]",
  cancelled: "border-[#4a4a4a] bg-[#2a2a2a] text-[#a3a3a3]",
};

function statusClass(status) {
  const key = String(status || "").toLowerCase();
  return STATUS_STYLE[key] || "border-[#343434] bg-[#252525] text-[#a3a3a3]";
}

async function load() {
  loading.value = true;
  try {
    const page = await getAgentRuns({ limit: 50 });
    runs.value = page?.items || [];
  } catch (error) {
    message.error(error?.message || "加载 Agent 运行失败");
  } finally {
    loading.value = false;
  }
}

async function cancel(runId) {
  try {
    await cancelAgentRun(runId);
    message.success("已请求取消");
    await load();
  } catch (error) {
    message.error(error?.message || "取消失败");
  }
}

async function retry(runId) {
  try {
    const prepared = await prepareAgentRunRetry(runId);
    if (!prepared.originalInputAlive) {
      message.error("原始输入不在当前进程中，无法重试");
      return;
    }
    message.info("重试已准备，请在确认流程中继续");
  } catch (error) {
    message.error(error?.message || "不可重试");
  }
}

async function exportReport(runId) {
  try {
    const result = await exportSanitizedAgentRunReport(runId);
    message.success(`已导出 ${result.path}`);
  } catch (error) {
    message.error(error?.message || "导出失败");
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <div v-if="loading" class="py-10 text-center text-sm text-[#8a8a8a]">
      <span class="icon-[mdi--loading] animate-spin text-[20px]" /> 加载运行记录…
    </div>

    <div
      v-else-if="!runs.length"
      class="flex flex-1 flex-col items-center justify-center rounded-[8px] border border-dashed border-[#3f3f3f] px-6 py-12 text-center"
    >
      <span class="icon-[mdi--robot-outline] text-[40px] text-[#4a4a4a]" aria-hidden="true" />
      <p class="mt-3 text-sm text-[#a3a3a3]">暂无 Agent 运行记录</p>
      <p class="mt-1 max-w-md text-xs text-[#737373]">委派任务开始执行后，可在此查看状态、取消运行或导出脱敏报告。</p>
      <Button class="mt-4" @click="load">刷新</Button>
    </div>

    <template v-else>
      <ControlCenterSection title="运行列表" :description="`共 ${runs.length} 条最近运行。`" icon="icon-[mdi--robot-outline]">
        <template #actions>
          <Button variant="text" @click="load">刷新</Button>
        </template>
      </ControlCenterSection>

      <div class="grid gap-3">
        <Card v-for="run in runs" :key="run.runId">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="center-row gap-2">
                <span class="truncate text-sm font-medium text-white">{{ run.modelName || "未命名运行" }}</span>
                <span class="rounded-full border px-2 py-0.5 text-[10px]" :class="statusClass(run.status)">{{ run.status || "未知" }}</span>
              </div>
              <div class="mt-2 flex flex-wrap gap-3 text-xs text-[#a3a3a3]">
                <span>工具调用 {{ run.toolCallCount ?? 0 }}</span>
                <span>尝试 {{ run.attemptCount ?? 0 }}</span>
                <span v-if="run.runId" class="font-mono text-[#737373]">{{ run.runId.slice(0, 8) }}…</span>
              </div>
            </div>
            <div class="center-row flex-wrap gap-2">
              <Button v-if="run.cancelable" variant="danger" @click="cancel(run.runId)">取消</Button>
              <Button v-if="run.retryable" @click="retry(run.runId)">准备重试</Button>
              <Button @click="exportReport(run.runId)">导出报告</Button>
            </div>
          </div>
        </Card>
      </div>
    </template>
  </div>
</template>
