<script setup>
import CacheHitRateChart from "@/components/charts/CacheHitRateChart.vue";
import Switch from "@/components/ui/Switch.vue";
import Tooltip from "@/components/ui/Tooltip.vue";
import { fetchMetricsRangeSummary, fetchRecentRequestMetrics, resetUsageMetrics } from "@/services/clientApi";
import { appState, saveIncludeCacheWriteInHitRate, saveLocalResponseCacheEnabled } from "@/state/appState";
import { formatCompactInteger, formatInteger } from "@/utils/numberFormat";
import { safeErrorLogAttributes, toUserError } from "@/utils/errorContract";

import { usePolling } from "@/composables/usePolling";
import { useSharedHomeMetricsRefresh } from "@/composables/useSharedHomeMetricsRefresh";
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";

const emit = defineEmits(["refresh", "open-ad"]);
const router = useRouter();
const AUTO_REFRESH_INTERVAL_MS = 5000; // 5秒自动刷新
const { localCacheStats: sharedLocalCacheStats, refresh: refreshSharedHomeMetrics } = useSharedHomeMetricsRefresh({
  intervalMs: AUTO_REFRESH_INTERVAL_MS,
});

async function handleOpenDetail() {
  await router.push("/metrics-detail");
}

const props = defineProps({
  loading: {
    type: Boolean,
    default: false,
  },
  error: {
    type: String,
    default: "",
  },
  homeAd: {
    type: Object,
    default: null,
  },
  homeAds: {
    type: Array,
    default: () => [],
  },
});

// --- 时间范围 ---
const timeRanges = [
  { key: "today", label: "当日" },
  { key: "24h", label: "24小时" },
  { key: "3d", label: "3天" },
  { key: "7d", label: "一周" },
  { key: "30d", label: "一月" },
  { key: "all", label: "全部" },
  { key: "custom", label: "自定义" },
];
const selectedRange = ref("24h");
const customStart = ref("");
const customEnd = ref("");
const rangeNow = ref(Date.now());

// 与控制中心 tab 一致的胶囊选中态（active-bg/active-text），避免高饱和实心绿突兀
const chipBaseClass = "rounded-full px-3 py-1 text-xs transition-colors";
const chipActiveClass = "bg-[var(--active-bg)] text-[var(--active-text)]";
const chipIdleClass = "text-[#9a9a9a] hover:bg-[var(--bg-hover)] hover:text-[#e5e5e5]";

const rangeStart = computed(() => {
  const now = new Date(rangeNow.value);
  switch (selectedRange.value) {
    case "today": {
      const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
      return d.getTime();
    }
    case "24h":
      return rangeNow.value - 24 * 3600_000;
    case "3d":
      return rangeNow.value - 3 * 86400_000;
    case "7d":
      return rangeNow.value - 7 * 86400_000;
    case "30d":
      return rangeNow.value - 30 * 86400_000;
    case "all":
      return 0;
    case "custom":
      return customStart.value ? new Date(customStart.value).getTime() : 0;
    default:
      return 0;
  }
});

const rangeEnd = computed(() => {
  if (selectedRange.value === "custom" && customEnd.value) {
    return new Date(customEnd.value).getTime() + 86400_000;
  }
  return rangeNow.value;
});

// --- 数据加载 ---
const allEvents = ref([]);
const rangeSummary = ref(null);
const eventsLoading = ref(false);
const eventsError = ref("");
let loadEventsSeq = 0;

// 本地（进程内）响应缓存命中统计，与 provider prompt-cache 分开
const localCacheStats = ref({ hits: 0, misses: 0, savedInputTokens: 0, savedOutputTokens: 0 });

async function loadEvents() {
  const seq = ++loadEventsSeq;
  rangeNow.value = Date.now();
  const start = rangeStart.value;
  const end = rangeEnd.value;
  eventsLoading.value = true;
  eventsError.value = "";
  try {
    const [data, summaryData] = await Promise.all([
      fetchRecentRequestMetrics(0),
      fetchMetricsRangeSummary(start, end).catch(() => null),
    ]);
    if (seq !== loadEventsSeq) return;
    allEvents.value = Array.isArray(data) ? data : [];
    rangeSummary.value = summaryData && typeof summaryData === "object" ? summaryData : null;
    if (sharedLocalCacheStats.value && typeof sharedLocalCacheStats.value === "object") {
      localCacheStats.value = {
        hits: Number(sharedLocalCacheStats.value.hits || 0),
        misses: Number(sharedLocalCacheStats.value.misses || 0),
        savedInputTokens: Number(sharedLocalCacheStats.value.savedInputTokens || 0),
        savedOutputTokens: Number(sharedLocalCacheStats.value.savedOutputTokens || 0),
      };
    }
  } catch (e) {
    if (seq !== loadEventsSeq) return;
    eventsError.value = String(e?.message || e || "加载失败");
    allEvents.value = [];
  } finally {
    if (seq === loadEventsSeq) {
      eventsLoading.value = false;
    }
  }
}

// --- 事件过滤 ---
const filteredEvents = computed(() => {
  const start = rangeStart.value;
  const end = rangeEnd.value;
  return allEvents.value.filter((ev) => {
    const ts = new Date(ev.at).getTime();
    if (!Number.isFinite(ts)) return false;
    if (start > 0 && ts < start) return false;
    if (ts >= end) return false;
    return true;
  });
});

// --- 聚合 ---
const summary = computed(() => {
  let turnsTotal = 0;
  let validTurnsTotal = 0;
  let invalidTurnsTotal = 0;
  let requestTokensTotal = 0;
  let promptTokensTotal = 0;
  let cacheReadTokens = 0;
  let cacheWriteTokens = 0;
  for (const ev of filteredEvents.value) {
    const kind = String(ev.kind || "").trim();
    if (kind === "turn_finalized") {
      turnsTotal++;
      if (String(ev.status || "").trim() === "completed") validTurnsTotal++;
      else invalidTurnsTotal++;
      continue;
    }
    if (kind !== "provider_call" && kind !== "") continue;
    requestTokensTotal += Number(ev.totalTokens || 0);
    promptTokensTotal += Number(ev.inputTokens || 0) + Number(ev.cacheReadTokens || 0) + Number(ev.cacheWriteTokens || 0);
    cacheReadTokens += Number(ev.cacheReadTokens || 0);
    cacheWriteTokens += Number(ev.cacheWriteTokens || 0);
  }
  const provider = rangeSummary.value;
  return {
    turnsTotal: provider ? Number(provider.turnsTotal || 0) : turnsTotal,
    validTurnsTotal: provider ? Number(provider.validTurnsTotal || 0) : validTurnsTotal,
    invalidTurnsTotal: provider ? Number(provider.invalidTurnsTotal || 0) : invalidTurnsTotal,
    requestTokensTotal: provider ? Number(provider.totalTokens || 0) : requestTokensTotal,
    promptTokensTotal: provider
      ? Number(provider.inputTokens || 0) + Number(provider.cacheReadTokens || 0) + Number(provider.cacheWriteTokens || 0)
      : promptTokensTotal,
    cacheReadTokens: provider ? Number(provider.cacheReadTokens || 0) : cacheReadTokens,
    cacheWriteTokens: provider ? Number(provider.cacheWriteTokens || 0) : cacheWriteTokens,
  };
});

// --- 派生指标 ---
const homeMetricsConfigSaving = ref(false);
const homeMetricsConfigError = ref("");

function normalizeNumber(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return 0;
  return Math.round(number);
}

function formatMetricValue(value) {
  const full = formatInteger(value);
  const compact = formatCompactInteger(value);
  return full === compact ? full : `${full} (${compact})`;
}

function formatRateLabel(value) {
  const rate = Number(value);
  if (!Number.isFinite(rate)) return "暂无数据";
  return `${(Math.max(0, Math.min(1, rate)) * 100).toFixed(2)}%`;
}

function calculateRate(numerator, denominator) {
  const top = normalizeNumber(numerator);
  const bottom = normalizeNumber(denominator);
  if (bottom <= 0) return null;
  return top / bottom;
}

function formatCost(value, currency = "USD") {
  const amount = Number(value);
  const unit = String(currency || "USD").trim() || "USD";
  if (!Number.isFinite(amount)) return `${unit} 0.00`;
  if (amount > 0 && amount < 0.01) return `${unit} <0.01`;
  return `${unit} ${amount.toFixed(2)}`;
}
function pricingSourceLabel(source) {
  const labels = { official: "官方价", catalog: "中转站探测价", configured: "手动配置", average: "均价估算" };
  return labels[String(source || "").trim()] || "";
}

const cacheReadTokensTotal = computed(() => normalizeNumber(summary.value.cacheReadTokens));
const cacheWriteTokensTotal = computed(() => normalizeNumber(summary.value.cacheWriteTokens));

const inputTokensTotal = computed(() => {
  const promptTokensTotal = normalizeNumber(summary.value.promptTokensTotal);
  return Math.max(0, promptTokensTotal - cacheReadTokensTotal.value - cacheWriteTokensTotal.value);
});

const defaultCacheHitRate = computed(() =>
  calculateRate(cacheReadTokensTotal.value, cacheReadTokensTotal.value + inputTokensTotal.value),
);

const cacheReuseRate = computed(() =>
  calculateRate(
    cacheReadTokensTotal.value,
    cacheReadTokensTotal.value + cacheWriteTokensTotal.value + inputTokensTotal.value,
  ),
);

const includeCacheWriteInHitRate = computed(() => appState.includeCacheWriteInHitRate);

const selectedCacheHitRate = computed(() =>
  includeCacheWriteInHitRate.value ? cacheReuseRate.value : defaultCacheHitRate.value,
);

const selectedCacheRateModeLabel = computed(() =>
  includeCacheWriteInHitRate.value ? "计入缓存创建" : "默认口径",
);

const validTurnsRate = computed(() => {
  const turnsTotal = normalizeNumber(summary.value.turnsTotal);
  if (turnsTotal <= 0) return null;
  return normalizeNumber(summary.value.validTurnsTotal) / turnsTotal;
});

const completionTokensTotal = computed(() => {
  const requestTokensTotal = normalizeNumber(summary.value.requestTokensTotal);
  const promptTokensTotal = normalizeNumber(summary.value.promptTokensTotal);
  return Math.max(0, requestTokensTotal - promptTokensTotal);
});

// 价值估算改用后端逐请求成本（costUsd），仅统计区间内已配置价格的 provider_call。
const rangeCostSummary = computed(() => {
  let total = 0;
  let pricedRows = 0;
  let unpricedRows = 0;
  const totals = new Map();
  const sources = new Set();
  for (const ev of filteredEvents.value) {
    if (String(ev.kind || "").trim() !== "provider_call" && String(ev.kind || "").trim() !== "") continue;
    if (ev.pricingKnown === true && ev.costUsd != null) {
      const amount = Number(ev.costUsd);
      if (Number.isFinite(amount)) {
        const currency = String(ev.currency || "USD").trim() || "USD";
        totals.set(currency, (totals.get(currency) || 0) + amount);
        if (ev.pricingSource) sources.add(String(ev.pricingSource));
        total += amount;
        pricedRows++;
        continue;
      }
    }
    unpricedRows++;
  }
  return { total, pricedRows, unpricedRows, totals: [...totals.entries()].map(([currency, total]) => ({ currency, total })), sources: [...sources], hasPriced: pricedRows > 0 };
});

// 展示值：无任何已计价请求时显示占位符，避免伪造数字
const estimatedCostDisplay = computed(() =>
  rangeCostSummary.value.hasPriced
    ? rangeCostSummary.value.totals.map((item) => formatCost(item.total, item.currency)).join(" · ")
    : "—",
);
const estimatedCostSourceDisplay = computed(() => rangeCostSummary.value.sources.map(pricingSourceLabel).filter(Boolean).join(" · "));

const cacheTooltipContent = computed(() => {
  const formula = includeCacheWriteInHitRate.value
    ? "缓存读取 /（缓存读取 + 缓存创建 + 非缓存输入）"
    : "缓存读取 /（缓存读取 + 非缓存输入）";
  return [
    `当前：${formatRateLabel(selectedCacheHitRate.value)}`,
    `公式：${formula}`,
    `默认 ${formatRateLabel(defaultCacheHitRate.value)} / 计入创建 ${formatRateLabel(cacheReuseRate.value)}`,
  ].join("\n");
});

const turnsTooltipContent = computed(() =>
  [
    "按会话回合统计，区分有效与异常。",
    "",
    `总轮次：${formatMetricValue(summary.value.turnsTotal)}`,
    `有效轮次：${formatMetricValue(summary.value.validTurnsTotal)}`,
    `异常轮次：${formatMetricValue(summary.value.invalidTurnsTotal)}`,
    `有效占比：${formatRateLabel(validTurnsRate.value)}`,
  ].join("\n"),
);

const tokensTooltipContent = computed(() =>
  [
    "总请求 Token 包含 Prompt 和模型输出。",
    "",
    `总请求：${formatMetricValue(summary.value.requestTokensTotal)}`,
    `Prompt：${formatMetricValue(summary.value.promptTokensTotal)}`,
    `输出推算：${formatMetricValue(completionTokensTotal.value)}`,
    `非缓存输入：${formatMetricValue(inputTokensTotal.value)}`,
    `缓存读取：${formatMetricValue(cacheReadTokensTotal.value)}`,
    `缓存写入：${formatMetricValue(cacheWriteTokensTotal.value)}`,
    "",
    "缓存读写已计入 Prompt 侧统计。",
  ].join("\n"),
);

const costTooltipContent = computed(() => {
  const { pricedRows, unpricedRows, hasPriced } = rangeCostSummary.value;
  const lines = [
    "估算 = 该区间内已知价格模型的花费合计，按币种分别展示。",
    "价格优先级：手动配价 > 中转站探测 > 内置官方价。",
    "",
    hasPriced
      ? `合计：${rangeCostSummary.value.totals.map((item) => formatCost(item.total, item.currency)).join(" · ")}（已计价 ${formatMetricValue(pricedRows)} 条）`
      : "区间内暂无可计价请求。",
  ];
  if (unpricedRows > 0) {
    lines.push(`${formatMetricValue(unpricedRows)} 条未计价（未知模型）`);
  }
  return lines.join("\n");
});

// 本地响应缓存命中：与 provider 缓存命中率区分展示
const localCacheHitRate = computed(() =>
  calculateRate(localCacheStats.value.hits, localCacheStats.value.hits + localCacheStats.value.misses),
);

const localCacheHasData = computed(
  () => normalizeNumber(localCacheStats.value.hits) + normalizeNumber(localCacheStats.value.misses) > 0,
);

const localCacheLine = computed(() => {
  const enabled = appState.localResponseCache && appState.localResponseCache.enabled;
  if (!localCacheHasData.value) {
    return enabled ? "本地缓存：已启用（等待命中）" : "本地缓存：未启用";
  }
  return `本地缓存命中 ${formatCompactInteger(localCacheStats.value.hits)} · ${formatRateLabel(localCacheHitRate.value)}`;
});

const localCacheTooltipContent = computed(() => {
  if (!localCacheHasData.value) {
    return [
      "本地缓存命中（应用自身的精确匹配响应缓存）",
      "",
      "尚未启用或暂无命中记录。",
      "与上方“缓存命中率”（provider 提示词缓存）不同。",
    ].join("\n");
  }
  return [
    "本地缓存命中（应用自身的精确匹配响应缓存，可直接省下 token 花费）",
    "与上方“缓存命中率”（provider 提示词缓存）是两回事。",
    "",
    `命中：${formatMetricValue(localCacheStats.value.hits)}`,
    `未命中：${formatMetricValue(localCacheStats.value.misses)}`,
    `命中率：${formatRateLabel(localCacheHitRate.value)}`,
    `已省输入：${formatMetricValue(localCacheStats.value.savedInputTokens)}`,
    `已省输出：${formatMetricValue(localCacheStats.value.savedOutputTokens)}`,
  ].join("\n");
});

async function toggleIncludeCacheWriteInHitRate(value) {
  const nextValue = Boolean(value);
  homeMetricsConfigSaving.value = true;
  homeMetricsConfigError.value = "";
  try {
    const result = await saveIncludeCacheWriteInHitRate(nextValue);
    if (!result?.ok) {
      homeMetricsConfigError.value = result?.error || "保存失败";
    }
  } catch (error) {
    homeMetricsConfigError.value = error?.message || "保存失败";
  } finally {
    homeMetricsConfigSaving.value = false;
  }
}

const localCacheToggleSaving = ref(false);
const localCacheToggleError = ref("");
async function toggleLocalResponseCache(value) {
  const nextValue = Boolean(value);
  localCacheToggleSaving.value = true;
  localCacheToggleError.value = "";
  try {
    const result = await saveLocalResponseCacheEnabled(nextValue);
    if (!result?.ok) {
      localCacheToggleError.value = result?.error || "保存失败";
    }
  } catch (error) {
    localCacheToggleError.value = error?.message || "保存失败";
  } finally {
    localCacheToggleSaving.value = false;
  }
}

async function handleRefresh() {
  await Promise.all([loadEvents(), refreshSharedHomeMetrics()]);
  emit("refresh");
}

const clearing = ref(false);
async function handleClear() {
  if (clearing.value) return;
  if (!window.confirm("确定清空所有会话统计？此操作不可恢复，将重置 Token 消耗、对话轮次、缓存命中率等全部历史数据。")) {
    return;
  }
  clearing.value = true;
  try {
    await resetUsageMetrics();
    await loadEvents();
    emit("refresh");
  } catch (e) {
    eventsError.value = toUserError(e);
    console.error("[HomeMetricsCard] clear usage metrics failed", safeErrorLogAttributes(e, { operation: "homeMetrics.clearUsage" }));
  } finally {
    clearing.value = false;
  }
}

watch([selectedRange, customStart, customEnd], () => {
  void loadEvents();
});

watch(
  sharedLocalCacheStats,
  (cacheStats) => {
    if (!cacheStats || typeof cacheStats !== "object") {
      return;
    }
    localCacheStats.value = {
      hits: Number(cacheStats.hits || 0),
      misses: Number(cacheStats.misses || 0),
      savedInputTokens: Number(cacheStats.savedInputTokens || 0),
      savedOutputTokens: Number(cacheStats.savedOutputTokens || 0),
    };
  },
  { immediate: true },
);

// 自动刷新：窗口隐藏时跳过，避免最小化后仍每 5s 发 4 次 IPC
usePolling(() => {
  if (!document.hidden) {
    loadEvents();
  }
}, { intervalMs: AUTO_REFRESH_INTERVAL_MS });
</script>

<template>
  <div>
    <div class="flex flex-col gap-4">
      <!-- 标题行 + 刷新/详情按钮 -->
      <div class="flex items-center justify-between gap-4 h-[36px]">
        <div class="flex flex-col gap-1 w-[200px] shrink-0">
          <h2 class="text-[14px] font-medium text-white/80">会话统计</h2>
        </div>
        <div class="flex-1 center-row justify-end shrink-0 gap-2 text-xs text-[#6f6f6f] pr-4 w-[200px]">
          <button
            type="button"
            class="center-row justify-center h-[24px] px-2 rounded-[6px] border border-[#3b3b3b] bg-[#242424] text-[#9d9d9d] transition-colors duration-150 hover:border-[#4c4c4c] hover:text-white"
            title="会话分析"
            @click="handleOpenDetail"
          >
            <span class="icon-[mdi--chart-line] text-[14px]"></span>
            <span class="ml-1 text-xs">详情</span>
          </button>
          <button
            type="button"
            class="center-row justify-center h-[24px] px-2 rounded-[6px] border border-[#3b3b3b] bg-[#242424] text-[#9d9d9d] transition-colors duration-150 hover:border-[#6b3b3b] hover:text-[#f87171] disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="clearing"
            :title="clearing ? '清空中...' : '清空所有会话统计（不可恢复）'"
            @click="handleClear"
          >
            <span class="icon-[mdi--trash-can-outline] text-[14px]" :class="{ '!animate-spin': clearing }"></span>
            <span class="ml-1 text-xs">清空</span>
          </button>
          <span>刷新统计</span>
          <button
            type="button"
            class="center-row justify-center h-[24px] w-[24px] rounded-[6px] border border-[#3b3b3b] bg-[#242424] text-[#9d9d9d] transition-colors duration-150 hover:border-[#4c4c4c] hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
            :disabled="eventsLoading"
            :title="eventsLoading ? '刷新中' : '刷新统计'"
            @click="handleRefresh"
          >
            <span class="icon-[mdi--refresh] text-[14px]" :class="{ '!animate-spin': eventsLoading }"></span>
          </button>
        </div>
      </div>

      <!-- 时间范围选择器 -->
      <div class="flex flex-wrap items-center gap-1.5 -mt-2">
        <button
          v-for="r in timeRanges"
          :key="r.key"
          type="button"
          :class="[chipBaseClass, selectedRange === r.key ? chipActiveClass : chipIdleClass]"
          @click="selectedRange = r.key"
        >
          {{ r.label }}
        </button>
        <div v-if="selectedRange === 'custom'" class="flex items-center gap-1 text-xs text-[#a0a0a0] ml-1">
          <input v-model="customStart" type="date" class="h-7 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none focus:border-[#10AD5D]" />
          <span>~</span>
          <input v-model="customEnd" type="date" class="h-7 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none focus:border-[#10AD5D]" />
        </div>
      </div>

      <div v-if="eventsError" class="text-xs text-[#f87171]">{{ eventsError }}</div>

      <!-- 4 个指标卡片 -->
      <div
        class="mt-[-4px] grid grid-cols-4 gap-0 rounded-[8px] border border-[#343434] bg-[var(--bg-card,#232323)] min-h-[110px]"
      >
        <!-- 缓存命中率 -->
        <div class="min-w-0 px-3 py-2.5 flex flex-col justify-between">
          <div class="center-row justify-start gap-1 text-xs text-[#7f7f7f]">
            <span>缓存命中率</span>
            <Tooltip>
              <div class="w-[280px] space-y-3">
                <div class="border-b border-[#343434] pb-3">
                  <Switch
                    compact
                    label="计入缓存创建"
                    description="开启后把缓存创建纳入分母"
                    enabled-text="当前按复用率口径显示"
                    disabled-text="当前按默认命中率口径显示"
                    :enabled="includeCacheWriteInHitRate"
                    :busy="homeMetricsConfigSaving"
                    :disabled="homeMetricsConfigSaving"
                    @change="toggleIncludeCacheWriteInHitRate"
                  />
                </div>
                <div class="whitespace-pre-wrap">{{ cacheTooltipContent }}</div>
                <div v-if="homeMetricsConfigError" class="text-[11px] text-[#f87171]">
                  {{ homeMetricsConfigError }}
                </div>
              </div>
            </Tooltip>
          </div>
          <CacheHitRateChart :rate="selectedCacheHitRate" />
          <div class="center-row justify-start gap-1 text-[11px] leading-4 text-[#6f6f6f]">
            <span class="whitespace-nowrap" :class="{ 'text-[#7f7f7f]': localCacheHasData }" :title="localCacheLine">
              {{ localCacheLine }}
            </span>
            <Tooltip :content="localCacheTooltipContent" />
            <button
              type="button"
              role="switch"
              :aria-checked="!!appState.localResponseCache?.enabled"
              :disabled="localCacheToggleSaving"
              class="ml-auto relative inline-flex h-[16px] w-[28px] shrink-0 cursor-pointer rounded-full outline-none transition-all duration-200 ease-out disabled:cursor-not-allowed disabled:opacity-55"
              :class="appState.localResponseCache?.enabled ? 'bg-[#10AD5D]' : 'bg-[rgba(255,255,255,0.22)]'"
              :title="appState.localResponseCache?.enabled ? '本地缓存已启用 · 点击关闭' : '本地缓存未启用 · 点击启用'"
              @click="toggleLocalResponseCache(!appState.localResponseCache?.enabled)"
            >
              <span
                class="absolute left-[2px] top-[2px] inline-flex h-[12px] w-[12px] rounded-full bg-white shadow-[0_1px_3px_rgba(0,0,0,0.3)] transition-all duration-200 ease-out"
                :class="appState.localResponseCache?.enabled ? 'translate-x-[12px]' : 'translate-x-0'"
              />
            </button>
          </div>
        </div>

        <!-- 对话轮次 -->
        <div class="min-w-0 border-l border-[#343434] px-3 py-2.5 flex flex-col justify-between">
          <div class="center-row justify-start gap-1 text-xs text-[#7f7f7f]">
            <span>对话轮次</span>
            <Tooltip :content="turnsTooltipContent" />
          </div>
          <div>
            <div
              class="text-[24px] leading-none text-white"
              style="font-family: var(--font-num)"
              :title="formatInteger(summary.turnsTotal)"
            >
              {{ formatCompactInteger(summary.turnsTotal) }}
            </div>
            <div class="mt-3 text-xs leading-5 text-[#8c8c8c]">
              <span v-if="estimatedCostSourceDisplay" class="mr-2 text-[#737373]">{{ estimatedCostSourceDisplay }}</span>
              有效
              <span :title="formatInteger(summary.validTurnsTotal)">
                {{ formatCompactInteger(summary.validTurnsTotal) }}
              </span>
              / 异常
              <span :title="formatInteger(summary.invalidTurnsTotal)">
                {{ formatCompactInteger(summary.invalidTurnsTotal) }}
              </span>
            </div>
          </div>
        </div>

        <!-- Token 消耗 -->
        <div class="min-w-0 border-l border-[#343434] px-3 py-2.5 flex flex-col justify-between">
          <div class="center-row justify-start gap-1 text-xs text-[#7f7f7f]">
            <span>Token 消耗</span>
            <Tooltip :content="tokensTooltipContent" />
          </div>
          <div>
            <div
              class="truncate text-[24px] leading-none text-white"
              style="font-family: var(--font-num)"
              :title="formatInteger(summary.requestTokensTotal)"
            >
              {{ formatCompactInteger(summary.requestTokensTotal) }}
            </div>
            <div class="mt-3 text-xs leading-5 text-[#8c8c8c]">
              Prompt
              <span :title="formatInteger(summary.promptTokensTotal)">
                {{ formatCompactInteger(summary.promptTokensTotal) }}
              </span>
            </div>
          </div>
        </div>

        <!-- 价值估算 -->
        <div class="min-w-0 border-l border-[#343434] px-3 py-2.5 flex flex-col justify-between">
          <div class="center-row justify-start gap-1 text-xs text-[#7f7f7f]">
            <span>价值估算</span>
            <Tooltip :content="costTooltipContent" />
          </div>
          <div>
            <div
              class="truncate text-[24px] leading-none text-white"
              style="font-family: var(--font-num)"
              :title="estimatedCostDisplay"
            >
              {{ estimatedCostDisplay }}
            </div>
            <div class="mt-3 text-xs leading-5 text-[#8c8c8c]">
              <span v-if="rangeCostSummary.unpricedRows > 0" class="text-[#c99a4a]">
                {{ formatCompactInteger(rangeCostSummary.unpricedRows) }} 条未计价
              </span>
              <span v-else-if="rangeCostSummary.hasPriced">
                已计价 {{ formatCompactInteger(rangeCostSummary.pricedRows) }} 条
              </span>
              <span v-else>未知模型不计价</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped></style>
