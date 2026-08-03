<script setup>
import Card from "@/components/ui/Card.vue";
import { fetchProviderSpendSummary, queryAllProviderBalances } from "@/services/clientApi";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

// 全量历史按中转站聚合花费，给首页一眼可见「哪个中转站用了多少额度」。
const rows = ref([]);
const loading = ref(false);
const error = ref("");

// 已配置余额查询的模型通道余额，供首页查看余额消耗。
const balances = ref([]);
const balancesLoading = ref(false);
const balancesError = ref("");
let timer = null;

const currencyTotals = computed(() => {
  const totals = new Map();
  for (const row of rows.value) {
    const cost = Number(row?.estimatedCostUsd);
    const currency = String(row?.currency || "").trim();
    if (!currency || !Number.isFinite(cost)) continue;
    totals.set(currency, (totals.get(currency) || 0) + cost);
  }
  return [...totals.entries()].map(([currency, total]) => ({ currency, total }));
});
const hasCost = computed(() => currencyTotals.value.length > 0);

// 有余额展示价值的条目：成功查询的余额，或明确失败的提示。
const balanceItems = computed(() => balances.value.filter((item) => item?.balance?.supported || item?.balance?.message));

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
function formatTotalCost(value) {
  const cost = Number(value);
  if (!Number.isFinite(cost)) return "—";
  // 零值不能走科学计数法，否则会显示为 0.00e+0。
  if (cost === 0) return "0.00";
  return Math.abs(cost) < 0.000001 ? cost.toExponential(2) : cost.toFixed(2);
}
function pricingSourceLabel(source) {
  const labels = { official: "官方价", catalog: "中转站探测价", configured: "手动配置", average: "均价估算" };
  return labels[String(source || "").trim()] || "";
}

// formatBalanceAmount 按币种格式化余额数值；% 币种不带小数。
function formatBalanceAmount(value, currency) {
  const amount = Number(value);
  if (!Number.isFinite(amount)) return "—";
  if (String(currency || "").trim() === "%") return String(Math.round(amount));
  return formatTotalCost(amount);
}

// balanceSubText 生成余额条目的辅助说明：套餐名 / 已用与总额。
function balanceSubText(item) {
  const balance = item?.balance || {};
  if (!balance.supported) return balance.message || "余额不可用";
  const parts = [];
  if (balance.planName) parts.push(balance.planName);
  if (balance.unlimited) {
    parts.push("不限额度");
    return parts.join(" · ");
  }
  if (Number.isFinite(Number(balance.used))) {
    parts.push(`已用 ${balance.currency} ${formatBalanceAmount(balance.used, balance.currency)}`);
    if (Number.isFinite(Number(balance.total))) {
      parts.push(`总 ${balance.currency} ${formatBalanceAmount(balance.total, balance.currency)}`);
    }
  } else if (Number.isFinite(Number(balance.total))) {
    parts.push(`总额 ${balance.currency} ${formatBalanceAmount(balance.total, balance.currency)}`);
  }
  return parts.join(" · ");
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

async function loadBalances() {
  balancesLoading.value = true;
  balancesError.value = "";
  try {
    balances.value = (await queryAllProviderBalances()) || [];
  } catch (cause) {
    balancesError.value = String(cause?.message || cause || "读取站点余额失败");
  } finally {
    balancesLoading.value = false;
  }
}

// 每分钟刷新一次，与首页其余统计节奏一致
const REFRESH_INTERVAL_MS = 60_000;
function startAutoRefresh() {
  stopAutoRefresh();
  timer = setInterval(() => {
    void load();
    void loadBalances();
  }, REFRESH_INTERVAL_MS);
}
function stopAutoRefresh() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

onMounted(() => {
  void load();
  void loadBalances();
  startAutoRefresh();
});
onBeforeUnmount(stopAutoRefresh);

defineExpose({ refresh: load, refreshBalances: loadBalances });
</script>

<template>
  <Card>
    <div class="flex h-[280px] min-h-0 min-w-0 flex-col gap-3 max-h-[calc(100vh-260px)]">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-sm font-medium text-white">站点消耗</h2>
          <div class="text-[11px] text-[#8f8f8f]">
            按中转站汇总全量历史用量与花费 · 仅计入已配置价格的请求
          </div>
        </div>
        <div class="text-right">
          <div class="text-[10px] uppercase tracking-wide text-[#737373]">合计花费</div>
          <div class="text-sm font-semibold text-[#6ee7a5]" style="font-family: var(--font-num)">
            <template v-if="hasCost">
              <span v-for="item in currencyTotals" :key="item.currency" class="ml-2 first:ml-0">
                {{ item.currency }} {{ formatTotalCost(item.total) }}
              </span>
            </template>
            <span v-else>—</span>
          </div>
        </div>
      </div>

      <div
        v-if="error"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]"
      >
        {{ error }}
      </div>

      <div class="grid min-h-0 flex-1 grid-cols-1 grid-rows-2 gap-3 overflow-hidden md:grid-cols-2 md:grid-rows-1">
        <section class="flex min-h-0 min-w-0 flex-col overflow-hidden rounded-[6px] bg-[#252525]/20 p-2">
          <div class="mb-2 flex shrink-0 items-center justify-between gap-2">
            <div class="text-[10px] uppercase tracking-wide text-[#8d8d8d]">站点余额</div>
            <div v-if="balanceItems.length" class="text-[10px] text-[#666]">{{ balanceItems.length }} 个站点</div>
          </div>
          <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
        <div v-if="balancesLoading && !balanceItems.length" class="py-4 text-center text-xs text-[#777]">
          正在读取站点余额…
        </div>
        <div v-else-if="balancesError && !balanceItems.length" class="text-xs text-[#fca5a5]">
          {{ balancesError }}
        </div>
        <div v-else-if="balanceItems.length" class="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          <div
            v-for="item in balanceItems"
            :key="item.adapterId || item.displayName || item.modelID"
            class="flex min-h-[50px] min-w-0 items-center gap-2 rounded-[5px] border border-[#2d2d2d] bg-[#202020]/40 px-2 py-1.5 transition-colors hover:border-[#10AD5D]/20 hover:bg-[#202020]/65"
          >
            <span class="size-2 shrink-0 rounded-full" :class="item.balance.supported ? 'bg-[#6ee7a5]' : 'bg-[#fca5a5]'" />
            <div class="min-w-0 flex-1">
              <div class="truncate text-xs text-white" :title="item.displayName || item.adapterId || item.modelID">
                {{ item.displayName || item.adapterId || item.modelID }}
              </div>
              <div class="mt-0.5 truncate text-[11px] text-[#858585]" :title="balanceSubText(item)">
                {{ balanceSubText(item) }}
              </div>
            </div>
            <div class="shrink-0 text-right" style="font-family: var(--font-num)">
              <template v-if="item.balance.supported">
                <div class="text-sm font-semibold text-[#6ee7a5]">
                  <template v-if="item.balance.unlimited">不限额度</template>
                  <template v-else-if="Number.isFinite(Number(item.balance.remaining))">
                    {{ item.balance.currency }} {{ formatBalanceAmount(item.balance.remaining, item.balance.currency) }}
                  </template>
                  <template v-else>—</template>
                </div>
                <div class="mt-0.5 text-[10px] text-[#737373]">剩余</div>
              </template>
              <div v-else class="text-[11px] text-[#fca5a5]">不可用</div>
            </div>
          </div>
        </div>
          </div>
        </section>

        <section class="flex min-h-0 min-w-0 flex-col overflow-hidden rounded-[6px] bg-[#252525]/20 p-2">
          <div class="mb-2 flex shrink-0 items-center justify-between gap-2">
            <div class="text-[10px] uppercase tracking-wide text-[#8d8d8d]">站点消耗</div>
            <div v-if="rows.length" class="text-[10px] text-[#666]">{{ rows.length }} 个站点</div>
          </div>
          <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
        <div v-if="loading && !rows.length" class="py-4 text-center text-xs text-[#777]">
          正在读取站点消耗…
        </div>
        <div v-else-if="!rows.length" class="py-4 text-center text-xs text-[#777]">
          暂无站点消耗数据
        </div>
        <div v-else class="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          <div
            v-for="(row, index) in rows.slice(0, 8)"
            :key="`${row.station || ''}-${row.provider || ''}-${index}`"
            class="flex min-h-[50px] min-w-0 items-center gap-2 rounded-[5px] border border-[#2d2d2d] bg-[#202020]/40 px-2 py-1.5 transition-colors hover:border-[#10AD5D]/20 hover:bg-[#202020]/65"
          >
            <span class="size-2 shrink-0 rounded-full bg-[#525252]" />
            <div class="min-w-0 flex-1">
              <div class="truncate text-xs font-medium text-[#e5e5e5]" :title="row.station || '未知站点'">
                {{ row.station || "未知站点" }}
              </div>
              <div class="mt-0.5 truncate text-[11px] text-[#858585]" :title="`${row.provider || '—'} · ${formatNumber(row.providerCalls)} 次 · ${formatNumber(row.totalTokens)} tokens`">
                {{ row.provider || "—" }} · {{ formatNumber(row.providerCalls) }} 次 · {{ formatNumber(row.totalTokens) }} tokens
              </div>
            </div>
            <div class="shrink-0 text-right" style="font-family: var(--font-num)">
              <div :class="Number.isFinite(Number(row.estimatedCostUsd)) ? 'text-sm font-semibold text-[#6ee7a5]' : 'text-xs text-[#666]'">
                {{ row.currency ? `${row.currency} ${formatCost(row.estimatedCostUsd)}` : formatCost(row.estimatedCostUsd) }}
              </div>
              <div v-if="row.pricingSource" class="mt-0.5 text-[10px] text-[#737373]">
                {{ pricingSourceLabel(row.pricingSource) }}
              </div>
            </div>
          </div>
          <div v-if="rows.length > 8" class="pt-1 text-center text-[11px] text-[#737373]">
            还有 {{ rows.length - 8 }} 个站点，详见「会话分析」
          </div>
        </div>
          </div>
        </section>
      </div>
    </div>
  </Card>
</template>