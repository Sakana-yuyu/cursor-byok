<script setup>
// 年度使用热力日历（GitHub 风格）：最近 53 周 × 7 天，按天聚合 provider_call 的
// token 消耗总量分档着色。数据由父组件传入（不受页面时间范围 chips 影响），
// 组件自身无状态、不拉数据，浏览器预览下同样可用。
import { computed } from "vue";
import { formatCompactInteger } from "@/utils/numberFormat";

const props = defineProps({
  events: { type: Array, default: () => [] },
});

const WEEK_COUNT = 53;

// dayKey 返回本地时区的 YYYY-MM-DD，跨时区统计以用户所在日历为准。
function dayKey(date) {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

// daily 按天聚合：{ "2026-08-23": { tokens, count } }
const daily = computed(() => {
  const map = new Map();
  for (const ev of props.events || []) {
    const ts = new Date(ev.at).getTime();
    if (!Number.isFinite(ts)) continue;
    const key = dayKey(new Date(ts));
    const item = map.get(key) || { tokens: 0, count: 0 };
    item.tokens += ev.totalTokens || 0;
    item.count += 1;
    map.set(key, item);
  }
  return map;
});

// weeks: 53 列，每列 7 行（周一为第一行）。首列从 today - 364 天所在周的周一开始，
// 保证最后一列对齐本周，列数恒定、宽度可预期。
const weeks = computed(() => {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const start = new Date(today);
  start.setDate(start.getDate() - (WEEK_COUNT - 1) * 7);
  // 对齐到周一（getDay(): 0=周日 ... 6=周六）
  const dow = (start.getDay() + 6) % 7;
  start.setDate(start.getDate() - dow);
  const cols = [];
  const cursor = new Date(start);
  for (let w = 0; w < WEEK_COUNT + 1 && cursor <= today; w++) {
    const col = [];
    for (let d = 0; d < 7; d++) {
      const cellDate = new Date(cursor);
      cellDate.setDate(cellDate.getDate() + d);
      const beyond = cellDate > today;
      const key = dayKey(cellDate);
      const item = beyond ? null : daily.value.get(key) || { tokens: 0, count: 0 };
      col.push({
        key,
        beyond,
        tokens: item ? item.tokens : 0,
        count: item ? item.count : 0,
        date: cellDate,
      });
    }
    cols.push(col);
    cursor.setDate(cursor.getDate() + 7);
  }
  return cols;
});

// 分档阈值取非零天 token 的分位数（P50/P75/P90），数据量小或全零时退化为固定档，
// 保证着色随真实使用强度自适应而不是写死绝对值。
const levelThresholds = computed(() => {
  const tokens = Array.from(daily.value.values()).map((v) => v.tokens).filter((t) => t > 0).sort((a, b) => a - b);
  if (tokens.length < 4) return [1, 1000, 10000, 100000];
  const pick = (p) => tokens[Math.min(tokens.length - 1, Math.floor(tokens.length * p))];
  return [pick(0.25), pick(0.5), pick(0.75), pick(0.9)];
});

function levelOf(tokens) {
  if (tokens <= 0) return 0;
  const t = levelThresholds.value;
  if (tokens <= t[0]) return 1;
  if (tokens <= t[1]) return 2;
  if (tokens <= t[2]) return 3;
  if (tokens <= t[3]) return 4;
  return 5;
}

const LEVEL_COLORS = ["#1c1c1c", "#0f4429", "#146c3d", "#10AD5D", "#2bc36a", "#4bdc85"];

function cellColor(cell) {
  if (cell.beyond) return "transparent";
  return LEVEL_COLORS[levelOf(cell.tokens)];
}

function cellTitle(cell) {
  if (cell.beyond) return "";
  const y = cell.date.getFullYear();
  const m = cell.date.getMonth() + 1;
  const d = cell.date.getDate();
  if (cell.count === 0) return `${y}-${m}-${d} 无使用`;
  return `${y}-${m}-${d} · ${cell.count} 次调用 · ${formatCompactInteger(cell.tokens)} tokens`;
}

// 月份标签：只在该列包含某月 1 号且前一列不含时标注，避免重复。
const monthLabels = computed(() =>
  weeks.value.map((col, i) => {
    const first = col.find((c) => !c.beyond);
    if (!first) return "";
    const m = first.date.getMonth();
    const prev = i > 0 ? weeks.value[i - 1].find((c) => !c.beyond) : null;
    if (!prev || prev.date.getMonth() !== m) return `${m + 1}月`;
    return "";
  }),
);

const WEEKDAY_LABELS = ["一", "", "三", "", "五", "", "日"];

const summary = computed(() => {
  let activeDays = 0;
  let totalTokens = 0;
  let totalCount = 0;
  for (const col of weeks.value) {
    for (const cell of col) {
      if (cell.beyond) continue;
      totalTokens += cell.tokens;
      totalCount += cell.count;
      if (cell.count > 0) activeDays++;
    }
  }
  return { activeDays, totalTokens, totalCount };
});
</script>

<template>
  <div class="rounded-[8px] border border-[#343434] bg-[#1a1a1a] px-4 py-3">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div class="text-sm font-medium text-white">年度使用热力图</div>
      <div class="text-xs text-[#8f8f8f]">
        {{ summary.activeDays }} 个活跃日 · {{ formatCompactInteger(summary.totalCount) }} 次调用 ·
        {{ formatCompactInteger(summary.totalTokens) }} tokens
      </div>
    </div>

    <div class="overflow-x-auto">
      <div class="min-w-max">
        <div class="flex gap-[3px] pl-[22px]">
          <div
            v-for="(label, i) in monthLabels"
            :key="i"
            class="w-[11px] text-[9px] leading-4 text-[#8f8f8f]"
          >{{ label }}</div>
        </div>
        <div class="flex gap-[3px]">
          <div class="mr-1 flex w-[18px] flex-col gap-[3px]">
            <div
              v-for="(label, i) in WEEKDAY_LABELS"
              :key="i"
              class="h-[11px] text-[9px] leading-[11px] text-[#8f8f8f]"
            >{{ label }}</div>
          </div>
          <div class="flex gap-[3px]">
            <div
              v-for="(col, i) in weeks"
              :key="i"
              class="flex flex-col gap-[3px]"
            >
              <div
                v-for="cell in col"
                :key="cell.key"
                class="size-[11px] rounded-[2px]"
                :style="{ backgroundColor: cellColor(cell) }"
                :title="cellTitle(cell)"
              />
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="mt-2 flex items-center justify-end gap-1 text-[10px] text-[#8f8f8f]">
      <span>少</span>
      <div
        v-for="(color, i) in LEVEL_COLORS"
        :key="i"
        class="size-[10px] rounded-[2px]"
        :style="{ backgroundColor: color }"
      />
      <span>多</span>
    </div>
  </div>
</template>
