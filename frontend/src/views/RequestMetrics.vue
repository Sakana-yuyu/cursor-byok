<script setup>
import Button from "@/components/ui/Button.vue";
import { fetchRecentRequestMetrics, fetchRecentRequestMetricsCount, fetchRecentRequestMetricsAbnormalCount, fetchRecentRequestMetricsDegradedCount } from "@/services/clientApi";
import { appState, reloadUserConfig } from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { usePolling } from "@/composables/usePolling";
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
  return `${start}-${end} / ${totalCount.value}`;
});
const pageNumbers = computed(() => {
  const result = [];
  const tp = totalPages.value;
  const cur = page.value;
  if (tp <= 7) {
    for (let i = 1; i <= tp; i++) result.push(i);
  } else {
    result.push(1);
    for (let i = Math.max(2, cur - 1); i <= Math.min(tp - 1, cur + 1); i++) {
      result.push(i);
    }
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
const formatDuration = (value) => {
  const ms = Number(value || 0);
  if (ms <= 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};
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

const abnormalCount = ref(0);

// 降级（degraded）原因 -> 中文说明；请求成功返回但体验/可信度降级（移植自 cursor2api 的 degraded 诊断）。
const DEGRADED_LABELS = {
  synthetic_shell_result: "shell 命令超时/断流，注入合成结果，命令可能未完整执行",
};
function degradedForRow(row) {
  const key = String(row?.degraded || "").trim();
  return key ? { key, label: DEGRADED_LABELS[key] || key } : null;
}

// 全量降级计数（跨分页，服务端口径：degraded 只写在正常完成的调用上），
// 与全量异常计数同口径展示，刷新时随数据一起加载。
const degradedCount = ref(0);

function statusTone(row) {
  if (isAbnormalRow(row)) {
    const status = String(row?.status || "").trim();
    if (status === "provider_error") return { bg: "#3a1414", text: "#fca5a5", label: "错误", reason: "" };
    return { bg: "#3a2a14", text: "#fbbf24", label: "无用量", reason: "" };
  }
  const degraded = degradedForRow(row);
  if (degraded) return { bg: "#3a3a14", text: "#fde047", label: "降级", reason: degraded.label };
  return { bg: "#143524", text: "#86efac", label: normalizeUsageStatusLabel(row?.status), reason: "" };
}

const hasUsageRows = computed(() => visibleRows.value.some((row) => row.usagePresent));

const pageSummary = computed(() => {
  let inputTokens = 0;
  let outputTokens = 0;
  let cacheReadTokens = 0;
  let cacheWriteTokens = 0;
  let totalTokens = 0;
  let knownCostUsd = 0;
  let knownCostCount = 0;

  for (const row of visibleRows.value) {
    inputTokens += Number(row?.inputTokens || 0);
    outputTokens += Number(row?.outputTokens || 0);
    cacheReadTokens += Number(row?.cacheReadTokens || 0);
    cacheWriteTokens += Number(row?.cacheWriteTokens || 0);
    totalTokens += Number(row?.totalTokens || 0);
    if (row?.pricingKnown === true && Number.isFinite(Number(row?.costUsd))) {
      knownCostUsd += Number(row.costUsd);
      knownCostCount += 1;
    }
  }

  const cacheDenominator = inputTokens + cacheReadTokens;
  return {
    inputTokens,
    outputTokens,
    cacheReadTokens,
    cacheWriteTokens,
    totalTokens,
    cacheRate: cacheDenominator > 0 ? cacheReadTokens / cacheDenominator : null,
    cost: knownCostCount > 0 ? `USD ${knownCostUsd.toFixed(4)}` : "未配置",
  };
});

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

function pricingSourceLabel(source) {
  const labels = { official: "官方价", catalog: "中转站探测价", configured: "手动配置", average: "均价估算" };
  return labels[String(source || "").trim()] || "";
}

function roleLabel(role) {
  const labels = {
    parent: "父请求",
    worker: "委派 worker",
    supervisor: "监督模型",
    reviewer: "审查模型",
  };
  return labels[String(role || "").trim()] || String(role || "").trim();
}

function modelMetaForRow(row) {
  const providerModel = String(row?.providerModel || "").trim();
  const displayModel = providerModel || String(row?.model || "").trim() || String(row?.logicalModel || "").trim();
  const logicalModel = String(row?.logicalModel || "").trim();
  const role = roleLabel(row?.role);
  const parts = [];
  if (role) parts.push(role);
  if (logicalModel && logicalModel !== displayModel) parts.push("逻辑模型 " + logicalModel);
  return {
    displayModel: displayModel || "-",
    secondary: parts.join(" · "),
    title: [displayModel, role, logicalModel && logicalModel !== displayModel ? "逻辑模型 " + logicalModel : ""].filter(Boolean).join("\n"),
  };
}

// 预计算当前页每行的展示元数据（tone/cost），避免模板内每行重复调用 statusTone/formatCost
const displayRows = computed(() =>
  pagedItems.value.map((row) => ({
    row,
    tone: statusTone(row),
    cost: formatCost(row),
    pricingSource: pricingSourceLabel(row?.pricingSource),
    supplier: supplierMetaForRow(row),
    errorCode: errorCodeForRow(row),
    model: modelMetaForRow(row),
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
    const [count, abnormal, degraded] = await Promise.all([
      fetchRecentRequestMetricsCount(),
      fetchRecentRequestMetricsAbnormalCount(),
      fetchRecentRequestMetricsDegradedCount(),
    ]);
    totalCount.value = count;
    abnormalCount.value = abnormal;
    degradedCount.value = degraded;
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

function autoRefresh() {
  if (document.hidden || loading.value) return;
  void refresh({ keepPage: true });
}

const { start: startAutoRefresh, stop: stopAutoRefresh } = usePolling(autoRefresh, {
  intervalMs: AUTO_REFRESH_INTERVAL_MS,
  immediate: false,
  autostart: false,
});

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
  <div class="flex h-full min-h-0 flex-col gap-2 overflow-hidden p-3 pt-0 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-3">
      <div class="center-row min-w-0 gap-2">
          <h1 class="text-lg font-semibold text-white">请求明细</h1>
          <span class="rounded-full border border-[#3a3a3a] bg-[#1f1f1f] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
            共 {{ totalCount }} 条
          </span>
      </div>
      <button
        type="button"
        class="center-row size-7 shrink-0 justify-center rounded-[6px] border border-[#3f3f3f] bg-[#232323] text-[#d4d4d4] transition-colors hover:border-[#525252] hover:text-white disabled:cursor-not-allowed disabled:opacity-45"
        :disabled="loading"
        :aria-label="loading ? '读取中' : '刷新请求明细'"
        :title="loading ? '读取中' : '刷新请求明细'"
        @click="refresh"
      >
        <span :class="loading ? 'icon-[mdi--loading] animate-spin text-[15px]' : 'icon-[mdi--refresh] text-[15px]'" aria-hidden="true" />
      </button>
    </div>

    <div class="grid shrink-0 grid-cols-2 overflow-hidden rounded-[7px] border border-[#343434] bg-[#202020] text-xs sm:grid-cols-4">
      <div class="border-b border-r border-[#343434] px-3 py-2 sm:border-b-0">
        <div class="text-[#777]">当前页汇总</div>
        <div class="mt-0.5 font-medium text-white" style="font-family: var(--font-num)">{{ formatNumber(pageSummary.totalTokens) }} tokens</div>
      </div>
      <div class="border-b border-[#343434] px-3 py-2 sm:border-b-0 sm:border-r">
        <div class="text-[#777]">缓存命中率</div>
        <div class="mt-0.5 font-medium" :class="rateTone(pageSummary.cacheRate)" style="font-family: var(--font-num)">{{ formatRate(pageSummary.cacheRate) }}</div>
      </div>
      <div class="border-r border-[#343434] px-3 py-2">
        <div class="text-[#777]">请求费用</div>
        <div class="mt-0.5 font-medium text-white" style="font-family: var(--font-num)">{{ pageSummary.cost }}</div>
      </div>
      <div class="px-3 py-2">
        <div class="text-[#777]">请求状态</div>
        <div class="mt-0.5 truncate">
          <span v-if="abnormalCount > 0" class="text-[#fbbf24]">异常 {{ abnormalCount }}</span>
          <span v-else-if="degradedCount > 0" class="text-[#fde047]">降级 {{ degradedCount }}</span>
          <span v-else-if="hasUsageRows" class="text-[#6ee7a5]">Usage 正常</span>
          <span v-else class="text-[#8f8f8f]">等待记录</span>
        </div>
      </div>
    </div>

    <div
      v-if="error"
      class="flex shrink-0 flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
    >
      <span class="min-w-0">{{ error }}</span>
      <Button variant="default" :disabled="loading" @click="refresh({ keepPage: true })">
        {{ loading ? "重试中..." : "重试" }}
      </Button>
    </div>

    <div class="min-h-0 flex-1 overflow-auto rounded-[7px] border border-[#343434] bg-[#1e1e1e] shadow-[inset_0_1px_0_rgba(255,255,255,0.03)]">
      <table class="w-full min-w-[960px] border-collapse text-left text-sm">
        <thead class="sticky top-0 z-10 bg-[#262626]/95 text-xs text-[#8f8f8f] backdrop-blur-sm">
          <tr class="border-b border-[#343434]">
            <th class="px-3 py-2 font-medium">模型 / 供应商</th>
            <th class="px-3 py-2 font-medium">状态</th>
            <th class="px-3 py-2 font-medium text-[#60a5fa]">输入</th>
            <th class="px-3 py-2 font-medium text-[#f59e0b]">输出</th>
            <th class="px-3 py-2 font-medium">缓存率</th>
            <th class="px-3 py-2 font-medium">总 tokens</th>
            <th class="px-3 py-2 font-medium">费用</th>
            <th class="px-3 py-2 font-medium">时间 / 耗时</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading && displayRows.length === 0">
            <td colspan="8" class="p-10 text-center text-[#777]">正在读取请求明细…</td>
          </tr>
          <tr v-else-if="!loading && displayRows.length === 0">
            <td colspan="8" class="p-10 text-center text-[#777]">
              暂无已记录请求
              <span v-if="totalCount > 0" class="block pt-1 text-xs text-[#555]">记录可能刚被清空或刷新，可点击右上角「刷新」重试</span>
            </td>
          </tr>
          <tr
            v-for="item in displayRows"
            :key="item.row.eventId"
            class="border-t border-[#2f2f2f] text-[#d4d4d4] transition-colors hover:bg-[#262626]/80"
          >
            <td class="max-w-[300px] px-3 py-2" :title="item.model.title">
              <div class="truncate text-[#e5e5e5]">{{ item.model.displayModel }}</div>
              <div class="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-[#8f8f8f]">
                <span v-if="providerIcon(item.row.provider)" :class="[providerIcon(item.row.provider), 'shrink-0 text-[12px]']" />
                <span class="truncate" :title="item.supplier.title || providerLabel(item.row.provider)">{{ item.supplier.label || providerLabel(item.row.provider) || "-" }}</span>
                <span v-if="item.model.secondary" class="truncate text-[#666]">· {{ item.model.secondary }}</span>
              </div>
            </td>
            <td class="px-3 py-2">
              <span
                class="inline-flex items-center rounded-full px-2 py-0.5 text-[11px]"
                :style="{ backgroundColor: item.tone.bg, color: item.tone.text }"
                :title="item.tone.reason || undefined"
              >
                {{ item.tone.label }}
              </span>
              <div v-if="item.errorCode" class="mt-1 max-w-[140px] truncate text-[10px] text-[#fca5a5]" :title="item.errorCode">{{ item.errorCode }}</div>
            </td>
            <td class="px-3 py-2 font-medium text-[#60a5fa]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.inputTokens) }}
            </td>
            <td class="px-3 py-2 font-medium text-[#f59e0b]" style="font-family: var(--font-num)">
              {{ formatNumber(item.row.outputTokens) }}
            </td>
            <td class="px-3 py-2 font-medium" :class="rateTone(item.row.cacheRate)" style="font-family: var(--font-num)" :title="`缓存读取 ${formatNumber(item.row.cacheReadTokens)} · 缓存写入 ${formatNumber(item.row.cacheWriteTokens)}`">
              <div>{{ formatRate(item.row.cacheRate) }}</div>
              <div class="mt-0.5 text-[10px] text-[#34d399]">读 {{ formatNumber(item.row.cacheReadTokens) }}</div>
            </td>
            <td class="px-3 py-2" style="font-family: var(--font-num)">{{ formatNumber(item.row.totalTokens) }}</td>
            <td class="px-3 py-2 text-[#cfcfcf]" style="font-family: var(--font-num)">
              <div>{{ item.cost }}</div>
              <div v-if="item.pricingSource" class="mt-0.5 text-[10px] text-[#8f8f8f]">{{ item.pricingSource }}</div>
            </td>
            <td class="whitespace-nowrap px-3 py-2 text-[#a3a3a3]" style="font-family: var(--font-num)">
              <div>{{ formatTime(item.row.at) }}</div>
              <template v-if="item.row.durationMs > 0">
                <span class="text-[10px]" :title="`首字延迟 ${formatDuration(item.row.ttftMs)}`">首字 {{ formatDuration(item.row.ttftMs) }}</span>
                <span class="text-[#666]"> · </span>
                <span class="text-[10px]" :title="`整体耗时 ${formatDuration(item.row.durationMs)}`">总 {{ formatDuration(item.row.durationMs) }}</span>
              </template>
              <span v-else class="text-[10px] text-[#666]">-</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="flex shrink-0 flex-wrap items-center justify-between gap-2 rounded-[7px] border border-[#343434] bg-[#202020] px-3 py-1.5 text-xs text-[#a3a3a3]">
      <div class="center-row gap-2">
        <span>每页</span>
        <select
          v-model.number="pageSize"
          class="h-7 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none transition-colors focus:border-[#10AD5D]"
        >
          <option v-for="size in pageSizeOptions" :key="size" :value="size">{{ size }}</option>
        </select>
        <span>条 · 显示 {{ pageRangeLabel }}</span>
      </div>
      <div class="center-row gap-1">
        <button
          type="button"
          class="h-7 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-white transition-colors hover:border-[#4a4a4a] hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="page <= 1 || loading"
          @click="goToPage(page - 1)"
        >
          上一页
        </button>
        <template v-for="(num, index) in pageNumbers" :key="num">
          <span v-if="index > 0 && num - pageNumbers[index - 1] > 1" class="px-1 text-[#666]">…</span>
          <button
            type="button"
            class="h-7 min-w-[28px] rounded-[6px] border px-2 transition-colors"
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
          class="h-7 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-white transition-colors hover:border-[#4a4a4a] hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="page >= totalPages || loading"
          @click="goToPage(page + 1)"
        >
          下一页
        </button>
      </div>
    </div>
  </div>
</template>
