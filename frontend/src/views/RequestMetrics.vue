<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { fetchRecentRequestMetrics, fetchRecentRequestMetricsCount } from "@/services/clientApi";
import { appState, reloadUserConfig } from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const rows = ref([]);
const loading = ref(false);
const error = ref("");

// 服务端分页
const page = ref(1);
const pageSize = ref(50);
const pageSizeOptions = [20, 50, 100];
const totalCount = ref(0);
const totalPages = computed(() => Math.max(1, Math.ceil(totalCount.value / pageSize.value)));
const pageRangeLabel = computed(() => {
  const start = totalCount.value === 0 ? 0 : (page.value - 1) * pageSize.value + 1;
  const end = Math.min(page.value * pageSize.value, totalCount.value);
  return `${start} / ${totalCount.value}`;
});
const pageNumbers = computed(() => {
  const result = [];
  const tp = totalPages.value;
  const cur = page.value;
  if (tp <= 7) {
    for (let i = 1; i <= tp; i++) result.push(i);
  } else {
    result.push(1);
    if (cur > 3) result.push("...");
    for (let i = Math.max(2, cur - 1); i <= Math.min(tp - 1, cur + 1); i++) result.push(i);
    if (cur < tp - 2) result.push("...");
    result.push(tp);
  }
  return result;
});
function goToPage(p) {
  if (p < 1 || p > totalPages.value || p === page.value) return;
  page.value = p;
}
const pagedItems = computed(() => rows.value);

const formatTime = (value) => (value ? new Date(value).toLocaleString() : "-");
const formatRate = (value) => (value == null ? "-" : `${(Number(value) * 100).toFixed(1)}%`);
const formatNumber = (value) => Number(value || 0).toLocaleString();

// 请求明细只展示 provider_call；兼容旧数据中缺少 kind 的记录。
const visibleRows = computed(() => rows.value.filter((row) => {
  const kind = String(row?.kind || "").trim();
  return kind === "provider_call" || kind === "";
}));

// 异常请求：provider 没有返回 usage 或明确报错，通常是流中断/取消/provider 错误
function isAbnormalRow(row) {
  const status = String(row?.status || "").trim();
  if (status === "provider_error" || status === "no_usage") return true;
  if (!row?.usagePresent) {
    const total =
      Number(row?.inputTokens || 0) +
      Number(row?.outputTokens || 0) +
      Number(row?.cacheReadTokens || 0) +
      Number(row?.cacheWriteTokens || 0);
    return total === 0;
  }
  return false;
}

const abnormalCount = computed(() => visibleRows.value.filter(isAbnormalRow).length);

function statusTone(row) {
  if (isAbnormalRow(row)) {
    const status = String(row?.status || "").trim();
    if (status === "provider_error") return { bg: "#3a1414", text: "#fca5a5", label: "错误" };
    return { bg: "#3a2a14", text: "#fbbf24", label: "无用量" };
  }
  return { bg: "#143524", text: "#86efac", label: normalizeUsageStatusLabel(row?.status) };
}

const hasUsageRows = computed(() => visibleRows.value.some((row) => row.usagePresent));

// 预构建 model+provider → pricing 的 Map，避免每行都线性扫描 appState.modelAdapters
const pricingLookup = computed(() => {
  const map = new Map();
  for (const adapter of appState.modelAdapters) {
    const model = String(adapter.modelID || "").trim();
    if (!model) continue;
    const provider = String(adapter.type || "").trim().toLowerCase();
    const key = `${model}\n${provider}`;
    if (!map.has(key) && adapter.pricing) map.set(key, adapter.pricing);
  }
  return map;
});

// 反查请求实际命中的模型渠道（中转供应商），用 groupName 优先、否则展示 host；
// 同一 model+provider 命中多个渠道时合并提示，避免笼统只显示协议品牌。
const supplierLookup = computed(() => {
  const map = new Map();
  for (const adapter of appState.modelAdapters) {
    const model = String(adapter.modelID || "").trim();
    if (!model) continue;
    const provider = String(adapter.type || "").trim().toLowerCase();
    const key = `${model}\n${provider}`;
    const label = String(adapter.groupName || "").trim() || formatHost(adapter.baseURL);
    if (!label) continue;
    if (map.has(key)) {
      map.get(key).labels.add(label);
    } else {
      map.set(key, { labels: new Set([label]) });
    }
  }
  return map;
});

function formatHost(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  try {
    return new URL(text).host || text;
  } catch {
    return text.replace(/^https?:\/\//, "");
  }
}

function supplierMetaForRow(row) {
  // 优先事件自带 groupName / baseUrl（失败写入也会带上），再回退本地 model+provider 反查
  const direct =
    String(row?.groupName || "").trim() ||
    formatHost(row?.baseUrl) ||
    formatHost(row?.baseURL);
  if (direct) return { label: direct, title: direct };

  const model = String(row?.model || "").trim();
  const provider = String(row?.provider || "").trim().toLowerCase();
  const entry = supplierLookup.value.get(`${model}\n${provider}`);
  if (!entry) return { label: "", title: "" };
  const list = [...entry.labels].filter(Boolean);
  if (list.length === 0) return { label: "", title: "" };
  if (list.length === 1) return { label: list[0], title: list[0] };
  return { label: `${list[0]} 等 ${list.length} 个`, title: list.join("、") };
}

function errorCodeForRow(row) {
  const code = String(row?.errorCode || "").trim();
  if (code) return code;
  // 旧数据无 errorCode：错误态用 status 作兜底展示
  const status = String(row?.status || "").trim();
  if (status === "provider_error" || status === "no_usage") return status;
  return "";
}

// usage 事件状态 -> 中文展示
const USAGE_STATUS_LABELS = {
  completed: "已完成",
  provider_error: "错误",
  no_usage: "无用量",
};
function normalizeUsageStatusLabel(status) {
  const key = String(status || "").trim();
  return USAGE_STATUS_LABELS[key] || key || "已完成";
}

function pricingForRow(row) {
  if (row?.pricing?.known) return row.pricing;
  const model = String(row?.model || "").trim();
  const provider = String(row?.provider || "").trim().toLowerCase();
  return pricingLookup.value.get(`${model}\n${provider}`) || null;
}

function formatCost(row) {
  // 优先使用后端计算的成本（后端已按命中的实际渠道定价核算）
  if (row?.pricingKnown === true && row?.costUsd != null) {
    const amount = Number(row.costUsd);
    if (Number.isFinite(amount)) {
      return `${row.currency || "USD"} ${amount.toFixed(6)}`;
    }
  }
  // 后端未知时回退到本地配置价格计算，避免已配置价格却无法展示
  const pricing = pricingForRow(row);
  if (!pricing?.known) return "未配置";
  const rates = [pricing.input, pricing.output, pricing.cacheRead, pricing.cacheWrite];
  if (rates.some((rate) => rate == null)) return "未配置";
  const cost =
    (Number(row.inputTokens || 0) * pricing.input +
      Number(row.outputTokens || 0) * pricing.output +
      Number(row.cacheReadTokens || 0) * pricing.cacheRead +
      Number(row.cacheWriteTokens || 0) * pricing.cacheWrite) /
    1_000_000;
  const currency = pricing.currency || "USD";
  return `${currency} ${cost.toFixed(6)}`;
}

// 预计算当前页每行的展示元数据（tone/cost），避免模板内每行重复调用 statusTone/formatCost
const displayRows = computed(() =>
  pagedItems.value.map((row) => ({
    row,
    tone: statusTone(row),
    cost: formatCost(row),
    supplier: supplierMetaForRow(row),
    errorCode: errorCodeForRow(row),
  })),
);

function rateTone(rate) {
  const value = Number(rate);
  if (!Number.isFinite(value)) return "text-[#a3a3a3]";
  if (value >= 0.5) return "text-[#6ee7a5]";
  if (value >= 0.2) return "text-[#fbbf24]";
  return "text-[#d4d4d4]";
}

// 服务端分页加载：只拉当前页的数据
async function refresh({ keepPage = false } = {}) {
  loading.value = true;
  error.value = "";
  try {
    const count = await fetchRecentRequestMetricsCount();
    totalCount.value = count;
    if (!keepPage) page.value = 1;
    if (page.value > totalPages.value) page.value = totalPages.value;
    const offset = (page.value - 1) * pageSize.value;
    rows.value = await fetchRecentRequestMetrics(pageSize.value, offset);
  } catch (cause) {
    error.value = String(cause?.message || cause || "读取请求明细失败");
  } finally {
    loading.value = false;
  }
}

// 翻页或改 pageSize 时重新加载
watch([page, pageSize], () => {
  void refresh({ keepPage: true });
});

// 自动刷新：页面可见时定时同步使用信息，避免手动点刷新
const AUTO_REFRESH_INTERVAL_MS = 5000;
let autoRefreshTimer = null;

function autoRefresh() {
  if (document.hidden || loading.value) return;
  void refresh({ keepPage: true });
}

function startAutoRefresh() {
  stopAutoRefresh();
  autoRefreshTimer = setInterval(autoRefresh, AUTO_REFRESH_INTERVAL_MS);
}

function stopAutoRefresh() {
  if (autoRefreshTimer) {
    clearInterval(autoRefreshTimer);
    autoRefreshTimer = null;
  }
}

function handleVisibilityChange() {
  if (!document.hidden) autoRefresh();
}

onMounted(async () => {
  await Promise.allSettled([refresh(), reloadUserConfig({ modelAdaptersOnly: true })]);
  startAutoRefresh();
  document.addEventListener("visibilitychange", handleVisibilityChange);
});

onUnmounted(() => {
  stopAutoRefresh();
  document.removeEventListener("visibilitychange", handleVisibilityChange);
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-4">
      <div class="min-w-0">
        <div class="center-row gap-2">
          <h1 class="text-lg font-semibold text-white">请求明细</h1>
          <span class="rounded-full border border-[#3a3a3a] bg-[#1f1f1f] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
            共 {{ totalCount }} 条
          </span>
        </div>
        <p class="mt-1 text-sm text-[#8f8f8f]">
          按请求查看 token 分类、缓存命中率与计费信息。
          <span v-if="abnormalCount > 0" class="text-[#fbbf24]">异常 {{ abnormalCount }} 条</span>
          <span v-else-if="hasUsageRows" class="text-[#6ee7a5]">已记录 usage</span>
        </p>
      </div>
      <div class="center-row gap-2">
        <Button variant="default" :disabled="loading" @click="refresh">
          {{ loading ? "读取中..." : "刷新" }}
        </Button>
      </div>
    </div>

    <Card>
      <div class="flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-[#a3a3a3]">
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#60a5fa]" />输入</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#f59e0b]" />输出</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#34d399]" />缓存读取</span>
        <span class="center-row gap-1.5"><i class="size-2 rounded-full bg-[#c084fc]" />缓存写入</span>
        <span class="text-[#666]">缓存率 = 缓存读取 ÷ (输入 + 缓存读取)</span>
      </div>
    </Card>

    <div
      v-if="error"
      class="shrink-0 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
    >
      {{ error }}
    </div>

    <div class="min-h-0 flex-1 overflow-auto rounded-[10px] border border-[#343434] bg-[#1e1e1e] shadow-[inset_0_1px_0_rgba(255,255,255,0.03)]">
      <table class="w-full min-w-[1360px] border-collapse text-left text-sm">
        <thead class="sticky top-0 z-10 bg-[#262626]/95 text-xs text-[#8f8f8f] backdrop-blur-sm">
          <tr class="border-b border-[#343434]">
            <th class="p-3 font-medium">状态</th>
            <th class="p-3 font-medium">时间</th>
            <th class="p-3 font-medium">供应商</th>
            <th class="p-3 font-medium">错误码</th>
            <th class="p-3 font-medium">模型</th>
            <th class="p-3 font-medium text-[#60a5fa]">输入</th>
            <th class="p-3 font-medium text-[#f59e0b]">输出</th>
            <th class="p-3 font-medium text-[#34d399]">缓存读取</th>
            <th class="p-3 font-medium text-[#c084fc]">缓存写入</th>
            <th class="p-3 font-medium">总 tokens</th>
            <th class="p-3 font-medium">缓存率</th>
            <th class="p-3 font-medium">请求费用</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading && totalCount === 0">
            <td colspan="12" class="p-10 text-center text-[#777]">正在读取请求明细…</td>
          </tr>
          <tr v-else-if="!loading && totalCount === 0">
            <td colspan="12" class="p-10 text-center text-[#777]">暂无已记录请求</td>
          </tr>
          <tr
            v-for="item in displayRows"
            :key="item.row.eventId"
            class="border-t border-[#2f2f2f] text-[#d4d4d4] transition-colors hover:bg-[#262626]/80"
          >
            <td class="p-3">
              <span
                class="inline-flex items-center rounded-full px-2 py-0.5 text-[11px]"
                :style="{ backgroundColor: item.tone.bg, color: item.tone.text }"
              >
                {{ item.tone.label }}
              </span>
            </td>
            <td class="whitespace-nowrap p-3 text-[#a3a3a3]" style="font-family: var(--font-num)">
              {{ formatTime(item.row.at) }}
            </td>
            <td class="p-3">
              <span
                class="inline-flex items-center gap-1.5 rounded-[6px] border border-[#3a3a3a] bg-[#252525] px-2 py-1 text-xs text-[#e5e5e5]"
                :title="item.supplier.title || providerLabel(item.row.provider)"
              >
                <span v-if="providerIcon(item.row.provider)" :class="[providerIcon(item.row.provider), 'text-[14px]']" />
                {{ item.supplier.label || providerLabel(item.row.provider) || "-" }}
              </span>
            </td>
            <td class="p-3">
              <span
                v-if="item.errorCode"
                class="inline-flex max-w-[160px] truncate rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-2 py-0.5 text-[11px] text-[#fca5a5]"
                :title="item.errorCode"
              >
                {{ item.errorCode }}
              </span>
              <span v-else class="text-[#666]">-</span>
            </td>
            <td class="max-w-[240px] truncate p-3" :title="item.row.model">{{ item.row.model || "-" }}</td>
            <td class="p-3 font-medium text-[#60a5fa]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.inputTokens) }}
            </td>
            <td class="p-3 font-medium text-[#f59e0b]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.outputTokens) }}
            </td>
            <td class="p-3 font-medium text-[#34d399]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.cacheReadTokens) }}
            </td>
            <td class="p-3 font-medium text-[#c084fc]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.cacheWriteTokens) }}
            </td>
            <td class="p-3" style="font-family: var(--font-num)">{{ formatNumber(item.row.totalTokens) }}</td>
            <td class="p-3 font-medium" :class="rateTone(item.row.cacheRate)" style="font-family: var(--font-num)">
              {{ formatRate(item.row.cacheRate) }}
            </td>
            <td class="p-3 text-[#cfcfcf]" style="font-family: var(--font-num)">{{ item.cost }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="flex shrink-0 flex-wrap items-center justify-between gap-3 rounded-[8px] border border-[#343434] bg-[#202020] px-3 py-2 text-xs text-[#a3a3a3]">
      <div class="center-row gap-2">
        <span>每页</span>
        <select
          v-model.number="pageSize"
          class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none transition-colors focus:border-[#10AD5D]"
        >
          <option v-for="size in pageSizeOptions" :key="size" :value="size">{{ size }}</option>
        </select>
        <span>条 · 显示 {{ pageRangeLabel }}</span>
      </div>
      <div class="center-row gap-1">
        <button
          type="button"
          class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2.5 text-white transition-colors hover:border-[#4a4a4a] hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="page <= 1 || loading"
          @click="goToPage(page - 1)"
        >
          上一页
        </button>
        <template v-for="(num, index) in pageNumbers" :key="num">
          <span v-if="index > 0 && num - pageNumbers[index - 1] > 1" class="px-1 text-[#666]">…</span>
          <button
            type="button"
            class="h-8 min-w-[32px] rounded-[6px] border px-2 transition-colors"
            :class="
              num === page
                ? 'border-[#10AD5D] bg-[#10AD5D] text-white shadow-[0_0_0_1px_rgba(16,173,93,0.35)]'
                : 'border-[#3f3f3f] bg-[#232323] text-white hover:border-[#4a4a4a]'
            "
            :disabled="loading"
            @click="goToPage(num)"
          >
            {{ num }}
          </button>
        </template>
        <button
          type="button"
          class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2.5 text-white transition-colors hover:border-[#4a4a4a] hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="page >= totalPages || loading"
          @click="goToPage(page + 1)"
        >
          下一页
        </button>
      </div>
    </div>
  </div>
</template>