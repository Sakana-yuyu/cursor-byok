<script setup>
import Card from "@/components/ui/Card.vue";
import { fetchProviderSpendSummary } from "@/services/clientApi";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

// 全量历史按中转站聚合花费，给首页一眼可见「哪个中转站用了多少额度」。
const rows = ref([]);
const loading = ref(false);
const error = ref("");
let timer = null;

const totalCost = computed(() =>
  rows.value.reduce((sum, row) => {
    const cost = Number(row?.estimatedCostUsd);
    return sum + (Number.isFinite(cost) ? cost : 0);
  }, 0),
);
const hasCost = computed(() => rows.value.some((row) => Number.isFinite(Number(row?.estimatedCostUsd))));
const currency = computed(() => {
  const hit = rows.value.find((row) => row && row.currency);
  return hit ? hit.currency : "USD";
});

function formatNumber(value) {
  return Number(value || 0).toLocaleString();
}
function formatCost(value) {
  const cost = Number(value);
  if (!Number.isFinite(cost)) return "未计价";
  if (cost === 0) return "0";
  // 避免极小花费被截断为 0，自适应精度
  const decimals = cost >= 1 ? 2 : cost >= 0.01 ? 4 : 6;
  return cost.toFixed(decimals);
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    rows.value = await fetchProviderSpendSummary(0, 0);
  } catch (cause) {
    error.value = String(cause?.message || cause || "读取站点消耗失败");
  } finally {
    loading.value = false;
  }
}

// 每分钟刷新一次，与首页其余统计节奏一致
const REFRESH_INTERVAL_MS = 60_000;
function startAutoRefresh() {
  stopAutoRefresh();
  timer = setInterval(load, REFRESH_INTERVAL_MS);
}
function stopAutoRefresh() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

onMounted(() => {
  void load();
  startAutoRefresh();
});
onBeforeUnmount(stopAutoRefresh);

defineExpose({ refresh: load });
</script>

<template>
  <Card>
    <div class="flex flex-col gap-3">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-medium text-white">站点消耗</h2>
          <div class="text-xs text-[#8f8f8f]">
            按中转站汇总全量历史用量与花费 · 仅计入已配置价格的请求
          </div>
        </div>
        <div class="text-right">
          <div class="text-[10px] uppercase tracking-wide text-[#737373]">合计花费</div>
          <div class="font-semibold text-[#6ee7a5]" style="font-family: var(--font-num)">
            {{ hasCost ? `${currency} ${totalCost < 0.000001 ? totalCost.toExponential(2) : totalCost.toFixed(2)}` : "—" }}
          </div>
        </div>
      </div>

      <div
        v-if="error"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]"
      >
        {{ error }}
      </div>

      <div v-if="loading && !rows.length" class="py-6 text-center text-xs text-[#777]">
        正在读取站点消耗…
      </div>

      <div v-else-if="!rows.length" class="py-6 text-center text-xs text-[#777]">
        暂无站点消耗数据
      </div>

      <div v-else class="flex flex-col gap-1.5">
        <div
          v-for="(row, index) in rows.slice(0, 6)"
          :key="`${row.station || ''}-${row.provider || ''}-${index}`"
          class="grid grid-cols-[1fr_auto_auto] items-center gap-3 rounded-[6px] bg-[#232323] px-3 py-2 text-xs"
        >
          <div class="min-w-0">
            <div class="truncate font-medium text-[#e5e5e5]">{{ row.station || "未知站点" }}</div>
            <div class="mt-0.5 text-[10px] text-[#737373]">
              {{ row.provider || "—" }} · {{ formatNumber(row.providerCalls) }} 次
            </div>
          </div>
          <div class="text-right text-[#a3a3a3]" style="font-family: var(--font-num)">
            {{ formatNumber(row.totalTokens) }}
            <div class="text-[10px] text-[#666]">tokens</div>
          </div>
          <div class="min-w-[64px] text-right" style="font-family: var(--font-num)">
            <span :class="Number.isFinite(Number(row.estimatedCostUsd)) ? 'text-[#6ee7a5]' : 'text-[#666]'">
              {{ formatCost(row.estimatedCostUsd) }}
            </span>
          </div>
        </div>
        <div v-if="rows.length > 6" class="pt-1 text-center text-[11px] text-[#737373]">
          还有 {{ rows.length - 6 }} 个站点，详见「会话分析」
        </div>
      </div>
    </div>
  </Card>
</template>
