<script setup>
import Button from "@/components/ui/Button.vue";
import { fetchProviderEvents, fetchProviderSpendSummary } from "@/services/clientApi";
import { formatCompactInteger } from "@/utils/numberFormat";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import * as echarts from "echarts/core";
import { LineChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
  DataZoomComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([
  LineChart,
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
  DataZoomComponent,
  CanvasRenderer,
]);

const router = useRouter();

// turn_finalized 会携带与 provider_call 重复的 token，只能用于轮次，不能进入 token 图
function isProviderCallEvent(ev) {
  const kind = String(ev?.kind || "").trim();
  return !kind || kind === "provider_call";
}

// --- 数据加载 ---
const allEvents = ref([]);
const loading = ref(false);
const error = ref("");

async function loadEvents() {
  loading.value = true;
  error.value = "";
  try {
    const data = await fetchProviderEvents(rangeStart.value, rangeEnd.value, "");
    const rows = Array.isArray(data) ? data : [];
    allEvents.value = rows.filter(isProviderCallEvent);
  } catch (e) {
    error.value = String(e?.message || e || "加载失败");
  } finally {
    loading.value = false;
  }
}

// --- 站点消耗（按中转站聚合的用量与花费） ---
const spendRows = ref([]);
const spendLoading = ref(false);
const spendError = ref("");

async function loadSpend() {
  spendLoading.value = true;
  spendError.value = "";
  try {
    const data = await fetchProviderSpendSummary(rangeStart.value, rangeEnd.value);
    spendRows.value = Array.isArray(data) ? data : [];
  } catch (e) {
    spendError.value = String(e?.message || e || "加载失败");
    spendRows.value = [];
  } finally {
    spendLoading.value = false;
  }
}

function formatSpend(row) {
  const cost = row?.estimatedCostUsd;
  if (cost == null || !Number.isFinite(Number(cost))) return "未计价";
  const value = Number(cost);
  const currency = String(row?.currency || "USD").trim() || "USD";
  const decimals = value !== 0 && Math.abs(value) < 0.01 ? 4 : 2;
  return `${currency} ${value.toFixed(decimals)}`;
}

function pricingSourceLabel(source) {
  const labels = { official: "官方价", catalog: "中转站探测价", configured: "手动配置", average: "均价估算" };
  return labels[String(source || "").trim()] || "";
}

// --- 时间范围 ---
const timeRanges = [
  { key: "today", label: "当日" },
  { key: "24h", label: "近24小时" },
  { key: "3d", label: "近3天" },
  { key: "7d", label: "近7天" },
  { key: "30d", label: "近30天" },
  { key: "all", label: "全部" },
  { key: "custom", label: "自定义" },
];
const selectedRange = ref("24h");
const customStart = ref("");
const customEnd = ref("");

const chipBaseClass =
  "rounded-[6px] px-3 py-1.5 text-xs transition-colors border";
const chipActiveClass = "border-[#10AD5D] bg-[#10AD5D] text-white";
const chipIdleClass =
  "border-[#3f3f3f] bg-[#232323] text-[#a0a0a0] hover:border-[#4a4a4a] hover:text-white";

const rangeStart = computed(() => {
  const now = new Date();
  switch (selectedRange.value) {
    case "today": {
      const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
      return d.getTime();
    }
    case "24h":
      return now.getTime() - 24 * 3600_000;
    case "3d":
      return now.getTime() - 3 * 86400_000;
    case "7d":
      return now.getTime() - 7 * 86400_000;
    case "30d":
      return now.getTime() - 30 * 86400_000;
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
    return new Date(customEnd.value).getTime() + 86400_000; // 含当天
  }
  return Date.now();
});

// --- 模型筛选 ---
const selectedModel = ref(""); // "" = 全部
const modelList = computed(() => {
  const set = new Set();
  for (const ev of allEvents.value) {
    const m = String(ev.model || "").trim();
    if (m) set.add(m);
  }
  return Array.from(set).sort();
});

// --- 过滤后的事件（已在加载时去掉 turn_finalized） ---
const filteredEvents = computed(() => {
  const start = rangeStart.value;
  const end = rangeEnd.value;
  const model = selectedModel.value;
  return allEvents.value.filter((ev) => {
    const ts = new Date(ev.at).getTime();
    if (!Number.isFinite(ts) || ts < start || ts >= end) return false;
    if (model && String(ev.model || "").trim() !== model) return false;
    return true;
  });
});

// --- 汇总数字 ---
const summary = computed(() => {
  let input = 0, output = 0, cacheRead = 0, cacheWrite = 0, total = 0, count = 0;
  for (const ev of filteredEvents.value) {
    input += ev.inputTokens || 0;
    output += ev.outputTokens || 0;
    cacheRead += ev.cacheReadTokens || 0;
    cacheWrite += ev.cacheWriteTokens || 0;
    total += ev.totalTokens || 0;
    count++;
  }
  const cacheRate = input + cacheRead > 0 ? cacheRead / (input + cacheRead) : null;
  return { input, output, cacheRead, cacheWrite, total, count, cacheRate };
});

// --- 按小时分桶 ---
const bucketSizeHours = computed(() => {
  const span = rangeEnd.value - rangeStart.value;
  if (span <= 48 * 3600_000) return 1;   // ≤48h → 每小时
  if (span <= 7 * 86400_000) return 6;   // ≤7d → 每6小时
  if (span <= 30 * 86400_000) return 24; // ≤30d → 每天
  return 24;
});

const chartData = computed(() => {
  const bucketMs = bucketSizeHours.value * 3600_000;
  const start = Math.floor(rangeStart.value / bucketMs) * bucketMs;
  const end = rangeEnd.value;
  const buckets = new Map();
  for (let ts = start; ts < end; ts += bucketMs) {
    buckets.set(ts, { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0, count: 0 });
  }
  for (const ev of filteredEvents.value) {
    const ts = Math.floor(new Date(ev.at).getTime() / bucketMs) * bucketMs;
    const b = buckets.get(ts);
    if (!b) continue;
    b.input += ev.inputTokens || 0;
    b.output += ev.outputTokens || 0;
    b.cacheRead += ev.cacheReadTokens || 0;
    b.cacheWrite += ev.cacheWriteTokens || 0;
    b.total += ev.totalTokens || 0;
    b.count++;
  }
  const sorted = Array.from(buckets.entries()).sort((a, b) => a[0] - b[0]);
  return {
    timestamps: sorted.map(([ts]) => ts),
    input: sorted.map(([, v]) => v.input),
    output: sorted.map(([, v]) => v.output),
    cacheRead: sorted.map(([, v]) => v.cacheRead),
    cacheWrite: sorted.map(([, v]) => v.cacheWrite),
    total: sorted.map(([, v]) => v.total),
    cacheRate: sorted.map(([, v]) => {
      const denom = v.input + v.cacheRead;
      return denom > 0 ? +(v.cacheRead / denom * 100).toFixed(2) : 0;
    }),
  };
});

function formatBucketLabel(ts) {
  const d = new Date(ts);
  const pad = (n) => String(n).padStart(2, "0");
  if (bucketSizeHours.value >= 24) {
    return `${d.getMonth() + 1}/${d.getDate()}`;
  }
  return `${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:00`;
}

// --- ECharts ---
const chartContainer = ref(null);
let chartInstance = null;

function buildChartOption() {
  const cd = chartData.value;
  const xLabels = cd.timestamps.map(formatBucketLabel);
  return {
    backgroundColor: "transparent",
    tooltip: {
      trigger: "axis",
      backgroundColor: "#1e1e1e",
      borderColor: "#3a3a3a",
      textStyle: { color: "#e0e0e0", fontSize: 12 },
    },
    legend: {
      data: ["输入", "输出", "缓存读取", "缓存写入", "总消耗", "缓存率(%)"],
      textStyle: { color: "#a0a0a0", fontSize: 11 },
      top: 0,
    },
    grid: { left: 60, right: 60, top: 40, bottom: 50 },
    xAxis: {
      type: "category",
      data: xLabels,
      axisLabel: { color: "#888", fontSize: 10, rotate: 30 },
      axisLine: { lineStyle: { color: "#3a3a3a" } },
    },
    yAxis: [
      {
        type: "value",
        name: "Tokens",
        nameTextStyle: { color: "#888", fontSize: 10 },
        axisLabel: {
          color: "#888",
          fontSize: 10,
          formatter: (val) => formatCompactInteger(val),
        },
        splitLine: { lineStyle: { color: "#2a2a2a" } },
      },
      {
        type: "value",
        name: "缓存率(%)",
        nameTextStyle: { color: "#888", fontSize: 10 },
        min: 0,
        max: 100,
        axisLabel: { color: "#888", fontSize: 10, formatter: "{value}%" },
        splitLine: { show: false },
      },
    ],
    dataZoom: [
      { type: "inside", start: 0, end: 100 },
      { type: "slider", start: 0, end: 100, height: 16, bottom: 8, borderColor: "#333", fillerColor: "rgba(16,173,93,0.1)" },
    ],
    series: [
      { name: "输入", type: "line", smooth: true, symbol: "none", itemStyle: { color: "#60a5fa" }, data: cd.input },
      { name: "输出", type: "line", smooth: true, symbol: "none", itemStyle: { color: "#34d399" }, data: cd.output },
      { name: "缓存读取", type: "line", smooth: true, symbol: "none", itemStyle: { color: "#fbbf24" }, data: cd.cacheRead },
      { name: "缓存写入", type: "line", smooth: true, symbol: "none", itemStyle: { color: "#f87171" }, data: cd.cacheWrite },
      { name: "总消耗", type: "line", smooth: true, symbol: "none", itemStyle: { color: "#a78bfa" }, data: cd.total },
      { name: "缓存率(%)", type: "line", smooth: true, symbol: "none", yAxisIndex: 1, itemStyle: { color: "#22d3ee" }, areaStyle: { opacity: 0.08 }, data: cd.cacheRate },
    ],
  };
}

function renderChart() {
  if (!chartContainer.value) return;
  if (!chartInstance) {
    chartInstance = echarts.init(chartContainer.value, null, { renderer: "canvas" });
  }
  chartInstance.setOption(buildChartOption(), true);
}

function resizeChart() {
  chartInstance?.resize();
}

watch([chartData, selectedModel, selectedRange], () => {
  renderChart();
});

// 站点消耗与事件图表复用相同时间范围，范围变化时同步重载
watch([rangeStart, rangeEnd], () => {
  loadSpend();
  loadEvents();
});

onMounted(async () => {
  await Promise.all([loadEvents(), loadSpend()]);
  renderChart();
  window.addEventListener("resize", resizeChart);
});
onUnmounted(() => {
  window.removeEventListener("resize", resizeChart);
  chartInstance?.dispose();
  chartInstance = null;
});

function formatRate(v) {
  if (v == null) return "-";
  return `${(v * 100).toFixed(2)}%`;
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div class="flex flex-col gap-4 pb-2">
        <!-- 顶部：返回 + 标题 -->
        <div class="flex items-center justify-end gap-3 border-b border-[#343434] pb-3">
          <Button variant="default" :disabled="loading" @click="() => { loadEvents(); loadSpend(); }">{{ loading ? "刷新中..." : "刷新数据" }}</Button>
        </div>

        <div v-if="error" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">{{ error }}</div>

        <!-- 时间范围选择 + 模型筛选 -->
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex flex-wrap gap-1">
            <button
              v-for="r in timeRanges"
              :key="r.key"
              type="button"
              :class="[chipBaseClass, selectedRange === r.key ? chipActiveClass : chipIdleClass]"
              @click="selectedRange = r.key"
            >{{ r.label }}</button>
          </div>
          <div v-if="selectedRange === 'custom'" class="flex items-center gap-1 text-xs text-[#a0a0a0]">
            <input v-model="customStart" type="date" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none focus:border-[#10AD5D]" />
            <span>~</span>
            <input v-model="customEnd" type="date" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none focus:border-[#10AD5D]" />
          </div>
          <select v-model="selectedModel" class="h-8 rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-2 text-xs text-white outline-none focus:border-[#10AD5D]">
            <option value="">全部模型</option>
            <option v-for="m in modelList" :key="m" :value="m">{{ m }}</option>
          </select>
        </div>

        <!-- 汇总卡片 -->
        <div class="grid grid-cols-3 gap-3 md:grid-cols-6">
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">输入</div>
            <div class="mt-1 text-lg text-white" style="font-family: var(--font-num)">{{ formatCompactInteger(summary.input) }}</div>
          </div>
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">输出</div>
            <div class="mt-1 text-lg text-white" style="font-family: var(--font-num)">{{ formatCompactInteger(summary.output) }}</div>
          </div>
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">缓存读取</div>
            <div class="mt-1 text-lg text-white" style="font-family: var(--font-num)">{{ formatCompactInteger(summary.cacheRead) }}</div>
          </div>
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">缓存写入</div>
            <div class="mt-1 text-lg text-white" style="font-family: var(--font-num)">{{ formatCompactInteger(summary.cacheWrite) }}</div>
          </div>
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">总消耗</div>
            <div class="mt-1 text-lg text-white" style="font-family: var(--font-num)">{{ formatCompactInteger(summary.total) }}</div>
          </div>
          <div class="rounded-[8px] border border-[#343434] bg-gradient-to-b from-[#242424] to-[#1d1d1d] px-3 py-2.5">
            <div class="text-[11px] uppercase tracking-wider text-[#666]">缓存率</div>
            <div class="mt-1 text-lg" :class="summary.cacheRate != null && summary.cacheRate > 0.3 ? 'text-[#6ee7a5]' : 'text-white'" style="font-family: var(--font-num)">{{ formatRate(summary.cacheRate) }}</div>
          </div>
        </div>

        <!-- ECharts 折线图 -->
        <div class="rounded-[8px] border border-[#343434] bg-[#1a1a1a] p-3">
          <div ref="chartContainer" class="h-[380px] w-full"></div>
        </div>

        <div class="rounded-[8px] border border-[#343434] bg-[#1a1a1a] px-4 py-3">
          <div class="mb-2 text-xs text-[#8f8f8f]">图例说明</div>
          <div class="grid grid-cols-2 gap-2 text-xs text-[#d4d4d4] md:grid-cols-3">
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#60a5fa]" />输入 Token（用户发送的内容）</span>
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#34d399]" />输出 Token（模型生成的内容）</span>
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#fbbf24]" />缓存读取（从缓存复用的内容）</span>
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#f87171]" />缓存写入（写入缓存的内容）</span>
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#a78bfa]" />总消耗（以上四项之和）</span>
            <span class="center-row gap-1.5"><i class="size-2.5 rounded-full bg-[#22d3ee]" />缓存率（缓存读取占输入比例）</span>
          </div>
        </div>

        <!-- 站点消耗（按中转站聚合） -->
        <div class="rounded-[8px] border border-[#343434] bg-[#1a1a1a] px-4 py-3">
          <div class="mb-3 flex items-center justify-between">
            <div class="text-sm font-medium text-white">站点消耗</div>
            <div v-if="spendLoading" class="text-xs text-[#8f8f8f]">加载中...</div>
          </div>
          <div v-if="spendError" class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">{{ spendError }}</div>
          <div v-else-if="spendRows.length === 0" class="py-6 text-center text-xs text-[#8f8f8f]">暂无站点消耗数据</div>
          <table v-else class="w-full text-xs">
            <thead>
              <tr class="border-b border-[#2f2f2f] text-left text-[11px] uppercase tracking-wider text-[#666]">
                <th class="py-2 pr-3 font-normal">站点</th>
                <th class="py-2 pr-3 font-normal">渠道</th>
                <th class="py-2 pr-3 text-right font-normal">请求数</th>
                <th class="py-2 pr-3 text-right font-normal">总 tokens</th>
                <th class="py-2 text-right font-normal">花费</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in spendRows" :key="i" class="border-b border-[#262626] text-[#d4d4d4] last:border-0">
                <td class="py-2 pr-3 text-white">{{ row.station || "-" }}</td>
                <td class="py-2 pr-3 text-[#a0a0a0]">{{ row.provider || "-" }}</td>
                <td class="py-2 pr-3 text-right" style="font-family: var(--font-num)">{{ formatCompactInteger(row.providerCalls || 0) }}</td>
                <td class="py-2 pr-3 text-right" style="font-family: var(--font-num)">{{ formatCompactInteger(row.totalTokens || 0) }}</td>
                <td class="py-2 text-right" :class="row.estimatedCostUsd == null ? 'text-[#8f8f8f]' : 'text-[#6ee7a5]'" style="font-family: var(--font-num)">
                  <div>{{ formatSpend(row) }}</div>
                  <div v-if="row.pricingSource" class="mt-0.5 text-[10px] text-[#8f8f8f]">{{ pricingSourceLabel(row.pricingSource) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 请求明细数 -->
        <div class="text-xs text-[#8f8f8f]">
          范围内 {{ summary.count }} 次请求 · 分桶粒度 {{ bucketSizeHours }} 小时/桶
        </div>
      </div>
    </div>
  </div>
</template>
