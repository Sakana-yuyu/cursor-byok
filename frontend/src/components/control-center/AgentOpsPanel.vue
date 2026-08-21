<script setup>
import Button from "@/components/ui/Button.vue";
import { cancelAgentRun, exportSanitizedAgentRunReport, getAgentRuns, prepareAgentRunRetry } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
const runs = ref([]);

async function load() {
  try {
    const page = await getAgentRuns({ limit: 50 });
    runs.value = page?.items || [];
  } catch (error) {
    message.error(error?.message || "加载 Agent 运行失败");
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
  <div class="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto">
    <div v-if="!runs.length" class="text-sm text-[#8a8a8a]">暂无运行中的 Agent 任务。</div>
    <div v-for="run in runs" :key="run.runId" class="rounded-[8px] border border-[#343434] bg-[#292929] p-3 text-xs">
      <div class="text-sm text-white">{{ run.modelName || "未命名运行" }} · {{ run.status }}</div>
      <div class="mt-1 text-[#a3a3a3]">工具 {{ run.toolCallCount }} · 尝试 {{ run.attemptCount }}</div>
      <div class="mt-2 flex gap-2">
        <Button v-if="run.cancelable" @click="cancel(run.runId)">取消</Button>
        <Button v-if="run.retryable" @click="retry(run.runId)">准备重试</Button>
        <Button @click="exportReport(run.runId)">导出报告</Button>
      </div>
    </div>
  </div>
</template>
