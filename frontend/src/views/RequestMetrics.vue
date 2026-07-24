<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { fetchRecentRequestMetrics } from "@/services/clientApi";
import { appState, reloadUserConfig } from "@/state/appState";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const rows = ref([]);
const loading = ref(false);
const error = ref("");
const formatTime = (value) => value ? new Date(value).toLocaleString() : "-";
const formatRate = (value) => value == null ? "-" : `${(Number(value) * 100).toFixed(1)}%`;
const formatNumber = (value) => Number(value || 0).toLocaleString();
const visibleRows = computed(() => rows.value.filter((row) => row.kind !== "turn_finalized"));

function pricingForRow(row) {
  if (row?.pricing?.known) return row.pricing;
  const model = String(row?.model || "").trim();
  const provider = String(row?.provider || "").trim().toLowerCase();
  return appState.modelAdapters.find((adapter) =>
    adapter.modelID === model && (!provider || adapter.type === provider),
  )?.pricing || null;
}

function formatCost(row) {
  const pricing = pricingForRow(row);
  if (!pricing?.known) return "未配置";
  const rates = [pricing.input, pricing.output, pricing.cacheRead, pricing.cacheWrite];
  if (rates.some((rate) => rate == null)) return "未配置";
  const cost = (
    Number(row.inputTokens || 0) * pricing.input
    + Number(row.outputTokens || 0) * pricing.output
    + Number(row.cacheReadTokens || 0) * pricing.cacheRead
    + Number(row.cacheWriteTokens || 0) * pricing.cacheWrite
  ) / 1_000_000;
  const currency = pricing.currency || "USD";
  return `${currency} ${cost.toFixed(6)}`;
}

async function refresh() {
  loading.value = true;
  error.value = "";
  try {
    rows.value = await fetchRecentRequestMetrics(200);
  } catch (cause) {
    error.value = String(cause?.message || cause || "读取请求明细失败");
  } finally {
    loading.value = false;
  }
}

onMounted(async () => {
  await Promise.allSettled([refresh(), reloadUserConfig({ modelAdaptersOnly: true })]);
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-white">缓存上下文</h1>
        <p class="text-sm text-[#8f8f8f]">按请求查看 token 分类、缓存命中率与计费信息。</p>
      </div>
      <div class="center-row gap-2">
        <Button variant="default" :disabled="loading" @click="refresh">{{ loading ? "读取中..." : "刷新" }}</Button>
        <Button variant="primary" @click="router.push('/')">返回首页</Button>
      </div>
    </div>

    <Card>
      <div class="flex flex-wrap items-center gap-4 text-xs text-[#a3a3a3]">
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#60a5fa]" />输入</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#f59e0b]" />输出</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#34d399]" />缓存读取</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#c084fc]" />缓存写入</span>
        <span class="text-[#777]">缓存率 = 缓存读取 ÷ (输入 + 缓存读取)</span>
      </div>
    </Card>

    <Card v-if="error"><div class="text-sm text-[#fca5a5]">{{ error }}</div></Card>

    <div class="min-h-0 flex-1 overflow-auto rounded-[8px] border border-[#343434] bg-[#232323]">
      <table class="w-full min-w-[1260px] border-collapse text-left text-sm">
        <thead class="sticky top-0 bg-[#292929] text-xs text-[#8f8f8f]">
          <tr>
            <th class="p-3">状态</th>
            <th class="p-3">时间</th>
            <th class="p-3">模型</th>
            <th class="p-3 text-[#60a5fa]">输入</th>
            <th class="p-3 text-[#f59e0b]">输出</th>
            <th class="p-3 text-[#34d399]">缓存读取</th>
            <th class="p-3 text-[#c084fc]">缓存写入</th>
            <th class="p-3">总 tokens</th>
            <th class="p-3">缓存率</th>
            <th class="p-3">请求费用</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!loading && visibleRows.length === 0">
            <td colspan="10" class="p-8 text-center text-[#777]">暂无已记录请求</td>
          </tr>
          <tr v-for="row in visibleRows" :key="row.eventId" class="border-t border-[#343434] text-[#d4d4d4]">
            <td class="p-3"><span :class="row.usagePresent ? 'text-[#86efac]' : 'text-[#a3a3a3]'">{{ row.status || '已记录' }}</span></td>
            <td class="p-3 text-[#a3a3a3]">{{ formatTime(row.at) }}</td>
            <td class="max-w-[220px] truncate p-3" :title="row.model">{{ row.model || '-' }}</td>
            <td class="p-3 font-medium text-[#60a5fa]">{{ formatNumber(row.inputTokens) }}</td>
            <td class="p-3 font-medium text-[#f59e0b]">{{ formatNumber(row.outputTokens) }}</td>
            <td class="p-3 font-medium text-[#34d399]">{{ formatNumber(row.cacheReadTokens) }}</td>
            <td class="p-3 font-medium text-[#c084fc]">{{ formatNumber(row.cacheWriteTokens) }}</td>
            <td class="p-3">{{ formatNumber(row.totalTokens) }}</td>
            <td class="p-3">{{ formatRate(row.cacheRate) }}</td>
            <td class="p-3">{{ formatCost(row) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>