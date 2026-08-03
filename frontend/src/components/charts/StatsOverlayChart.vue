<script setup>
import { fetchMetricsTokenBuckets } from "@/services/clientApi";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { LineChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([LineChart, GridComponent, TooltipComponent, CanvasRenderer]);

const props = defineProps({
  refreshKey: {
    type: Number,
    default: 0,
  },
});

const RANGE_MS = 24 * 60 * 60 * 1000;
const BUCKET_HOURS = 2;

const chartElement = ref(null);
const buckets = ref([]);
const hasData = computed(() => buckets.value.some((bucket) => (
  Number(bucket.requestCount || 0) > 0
  || Number(bucket.totalTokens || 0) > 0
  || Number(bucket.inputTokens || 0) > 0
  || Number(bucket.outputTokens || 0) > 0
  || Number(bucket.cacheReadTokens || 0) > 0
  || Number(bucket.cacheWriteTokens || 0) > 0
)));

let chartInstance = null;
let resizeObserver = null;
let loadSequence = 0;

function normalizeBucket(bucket) {
  const at = new Date(bucket?.at).getTime();
  if (!Number.isFinite(at)) return null;
  return {
    at,
    requestCount: Number(bucket?.requestCount || 0),
    totalTokens: Number(bucket?.totalTokens || 0),
    inputTokens: Number(bucket?.inputTokens || 0),
    outputTokens: Number(bucket?.outputTokens || 0),
    cacheReadTokens: Number(bucket?.cacheReadTokens || 0),
    cacheWriteTokens: Number(bucket?.cacheWriteTokens || 0),
    cacheRate: bucket?.cacheRate == null ? null : Number(bucket.cacheRate) * 100,
  };
}

function formatBucketLabel(timestamp) {
  const date = new Date(timestamp);
  return `${String(date.getHours()).padStart(2, "0")}:00`;
}

function formatNumber(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? Math.round(number).toLocaleString("zh-CN") : "0";
}

function formatRate(value) {
  if (value == null) return "—";
  const number = Number(value);
  return Number.isFinite(number) ? `${number.toFixed(1)}%` : "—";
}

function formatBucketTime(timestamp) {
  const date = new Date(timestamp);
  const parts = [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ];
  return `${parts.join("-")} ${String(date.getHours()).padStart(2, "0")}:00`;
}

function clamp(value, minimum, maximum) {
  return Math.min(Math.max(value, minimum), Math.max(minimum, maximum));
}

function positionTooltip(point, _params, _dom, _rect, size) {
  const chartBounds = chartElement.value?.getBoundingClientRect();
  if (!chartBounds) return point;

  const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
  const viewportHeight = document.documentElement.clientHeight || window.innerHeight;
  const tooltipWidth = size.contentSize[0];
  const tooltipHeight = size.contentSize[1];
  const margin = 8;
  const gap = 8;
  const left = clamp(
    chartBounds.left + point[0] - tooltipWidth / 2,
    margin,
    viewportWidth - tooltipWidth - margin,
  );
  const above = chartBounds.top + point[1] - tooltipHeight - gap;
  const below = chartBounds.top + point[1] + gap;
  const top = above >= margin
    ? above
    : clamp(below, margin, viewportHeight - tooltipHeight - margin);

  // ECharts 的 viewSize 只有图表高度，不能直接用 confine，否则完整内容会被限制在图表内。
  return [left - chartBounds.left, top - chartBounds.top];
}

function buildOption() {
  const rows = buckets.value;
  const labels = rows.map((bucket) => formatBucketLabel(bucket.at));
  return {
    animation: true,
    animationDuration: 350,
    backgroundColor: "transparent",
    grid: { left: 2, right: 2, top: 4, bottom: 4, containLabel: false },
    tooltip: {
      trigger: "axis",
      confine: false,
      appendToBody: true,
      appendTo: "body",
      position: positionTooltip,
      axisPointer: { type: "line", lineStyle: { color: "rgba(110,231,165,.45)" } },
      backgroundColor: "rgba(22,28,27,.97)",
      borderColor: "rgba(110,231,165,.55)",
      borderWidth: 1,
      borderRadius: 7,
      padding: [7, 9],
      extraCssText: "box-shadow: 0 8px 24px rgba(0,0,0,.46); line-height: 1.45; pointer-events: none;",
      textStyle: { color: "#e5e5e5", fontSize: 10 },
      formatter(params) {
        const index = params?.[0]?.dataIndex;
        const row = rows[index];
        if (!row) return "";
        return [
          `${formatBucketTime(row.at)} · 近 24 小时`,
          `请求数：${formatNumber(row.requestCount)}`,
          `总 Token：${formatNumber(row.totalTokens)}`,
          `输入 Token：${formatNumber(row.inputTokens)}`,
          `输出 Token：${formatNumber(row.outputTokens)}`,
          `缓存读取：${formatNumber(row.cacheReadTokens)}`,
          `缓存写入：${formatNumber(row.cacheWriteTokens)}`,
          `缓存率：${formatRate(row.cacheRate)}`,
        ].join("<br>");
      },
    },
    xAxis: {
      type: "category",
      data: labels,
      boundaryGap: false,
      show: false,
    },
    yAxis: [
      { type: "value", show: false, min: 0 },
      { type: "value", show: false, min: 0, max: 100 },
    ],
    series: [
      {
        name: "总 Token",
        type: "line",
        yAxisIndex: 0,
        data: rows.map((bucket) => bucket.totalTokens),
        smooth: true,
        symbol: "none",
        connectNulls: true,
        lineStyle: { width: 1.7, color: "#6ee7a5" },
        areaStyle: { color: "rgba(110,231,165,.12)" },
      },
      {
        name: "缓存率",
        type: "line",
        yAxisIndex: 1,
        data: rows.map((bucket) => bucket.cacheRate),
        smooth: true,
        symbol: "none",
        connectNulls: true,
        lineStyle: { width: 1.2, color: "#22d3ee", type: "dashed" },
      },
    ],
  };
}

function renderChart() {
  if (!chartElement.value) return;
  if (!chartInstance) {
    chartInstance = echarts.init(chartElement.value, null, { renderer: "canvas" });
  }
  chartInstance.setOption(buildOption(), true);
  chartInstance.resize();
}

function resizeChart() {
  chartInstance?.resize();
}

async function loadBuckets() {
  const sequence = ++loadSequence;
  const end = Date.now();
  const start = end - RANGE_MS;
  try {
    const result = await fetchMetricsTokenBuckets(start, end, "", BUCKET_HOURS);
    if (sequence !== loadSequence) return;
    buckets.value = (Array.isArray(result) ? result : [])
      .map(normalizeBucket)
      .filter(Boolean)
      .sort((left, right) => left.at - right.at);
    await nextTick();
    renderChart();
  } catch (_) {
    if (sequence !== loadSequence) return;
    buckets.value = [];
    await nextTick();
    renderChart();
  }
}

watch(() => props.refreshKey, (next, previous) => {
  if (next !== previous) void loadBuckets();
});

onMounted(async () => {
  await loadBuckets();
  if (typeof ResizeObserver !== "undefined" && chartElement.value) {
    resizeObserver = new ResizeObserver(resizeChart);
    resizeObserver.observe(chartElement.value);
  }
  window.addEventListener("resize", resizeChart);
});

onUnmounted(() => {
  window.removeEventListener("resize", resizeChart);
  resizeObserver?.disconnect();
  chartInstance?.dispose();
  chartInstance = null;
});
</script>

<template>
  <div
    class="stats-overlay-chart"
    :class="{ 'is-empty': !hasData }"
    role="img"
    aria-label="近 24 小时 Token 趋势"
  >
    <div ref="chartElement" class="stats-overlay-chart__canvas" />
    <span v-if="!hasData" class="stats-overlay-chart__empty">暂无趋势数据</span>
    <div class="stats-overlay-chart__legend" aria-hidden="true">
      <span><i class="stats-overlay-chart__dot stats-overlay-chart__dot--token" />Token</span>
      <span><i class="stats-overlay-chart__dot stats-overlay-chart__dot--rate" />缓存率</span>
    </div>
  </div>
</template>

<style scoped>
.stats-overlay-chart {
  position: relative;
  width: 100%;
  height: 58px;
  min-width: 0;
  flex: 0 0 58px;
  overflow: visible;
  border: 1px solid rgba(110, 231, 165, 0.28);
  border-radius: 8px;
  background: rgba(110, 231, 165, 0.05);
}

.stats-overlay-chart__canvas {
  position: absolute;
  inset: 0;
}

.stats-overlay-chart__empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: rgba(190, 220, 205, 0.52);
  font-size: 9px;
  pointer-events: none;
}

.stats-overlay-chart__legend {
  position: absolute;
  top: 3px;
  right: 5px;
  display: flex;
  gap: 6px;
  color: rgba(190, 220, 205, 0.68);
  font-size: 8px;
  line-height: 1;
  pointer-events: none;
}

.stats-overlay-chart__legend span {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.stats-overlay-chart__dot {
  width: 4px;
  height: 4px;
  border-radius: 50%;
}

.stats-overlay-chart__dot--token {
  background: #6ee7a5;
}

.stats-overlay-chart__dot--rate {
  background: #22d3ee;
}
</style>