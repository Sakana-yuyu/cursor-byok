<script setup>
import Button from "@/components/ui/Button.vue";
import { useLocale } from "@/i18n/runtime";
import { getProviderDiagnostics } from "@/services/clientApi";
import { toUserError } from "@/state/appState";
import {
  formatDiagnosticTimestamp,
  formatProviderCooldown,
  normalizeProviderDiagnostics,
} from "@/utils/providerDiagnostics";
import { computed, onMounted, onUnmounted, ref } from "vue";

const rawSnapshot = ref(null);
const loading = ref(false);
const loaded = ref(false);
const error = ref("");
const stale = ref(false);
const now = ref(Date.now());
const { locale } = useLocale();
let clock;

// 渠道数不超过该阈值时全部展开（小配置保持一眼全览的旧观感）；
// 超过后按端点分组折叠，只自动展开含冷却渠道的分组。
const EXPAND_ALL_THRESHOLD = 12;

const hasSnapshot = computed(() => rawSnapshot.value !== null);
const diagnostics = computed(() => normalizeProviderDiagnostics(rawSnapshot.value, now.value));

// 渠道筛选：按显示名 / 模型 / 分组 / 供应商 / 端点模糊匹配。
const searchText = ref("");
// 用户显式展开/收起的分组覆盖（key=端点）；未覆盖时按默认规则展开。
const groupState = ref({});

function channelMatchesFilter(channel, filter) {
  if (!filter) return true;
  return `${channel.displayName} ${channel.modelId} ${channel.groupName} ${channel.provider} ${channel.endpoint}`
    .toLowerCase()
    .includes(filter);
}

// groupedChannels 按安全端点给渠道分组（同一中转站/官方端点的渠道聚在一张卡里，
// 避免上百个渠道逐条平铺刷屏）；含冷却渠道的分组排在最前，其余按渠道数降序。
const groupedChannels = computed(() => {
  const filter = searchText.value.trim().toLowerCase();
  const groups = new Map();
  for (const channel of diagnostics.value.channels) {
    if (!channelMatchesFilter(channel, filter)) continue;
    const endpoint = channel.endpoint || "";
    if (!groups.has(endpoint)) {
      groups.set(endpoint, { key: endpoint, endpoint, channels: [], ready: 0, cooldown: 0 });
    }
    const group = groups.get(endpoint);
    group.channels.push(channel);
    if (channel.healthState === "cooldown") group.cooldown += 1;
    else group.ready += 1;
  }
  return Array.from(groups.values()).sort((a, b) => {
    const byCooldown = (b.cooldown > 0 ? 1 : 0) - (a.cooldown > 0 ? 1 : 0);
    if (byCooldown !== 0) return byCooldown;
    return b.channels.length - a.channels.length;
  });
});

const filteredChannelCount = computed(() =>
  groupedChannels.value.reduce((total, group) => total + group.channels.length, 0),
);

function isGroupExpanded(group) {
  if (Object.prototype.hasOwnProperty.call(groupState.value, group.key)) {
    return Boolean(groupState.value[group.key]);
  }
  if (searchText.value.trim()) return true;
  if (diagnostics.value.channels.length <= EXPAND_ALL_THRESHOLD) return true;
  return group.cooldown > 0;
}

function toggleGroup(group) {
  groupState.value = { ...groupState.value, [group.key]: !isGroupExpanded(group) };
}

function setAllGroups(expanded) {
  const next = {};
  for (const group of groupedChannels.value) next[group.key] = expanded;
  groupState.value = next;
}

async function load() {
  if (loading.value) return;
  loading.value = true;
  error.value = "";
  try {
    const snapshot = await getProviderDiagnostics();
    if (!snapshot || typeof snapshot !== "object") throw new Error("供应商诊断未返回有效快照");
    rawSnapshot.value = snapshot;
    stale.value = false;
  } catch (err) {
    error.value = toUserError(err);
    stale.value = hasSnapshot.value;
  } finally {
    loading.value = false;
    loaded.value = true;
  }
}

function formatTokens(value) {
  const number = Number(value || 0);
  return number > 0 ? Math.round(number).toLocaleString(locale.value) : "—";
}

// channelMeta 汇总渠道「供应商 · 协议（· 自定义头）」一行文本，行内与 title 复用。
function channelMeta(channel) {
  const meta = `${channel.provider || "—"} · ${channel.protocolGroup || channel.protocolMode || "—"}`;
  return channel.customHeadersConfigured ? `${meta} · 自定义头` : meta;
}

// channelTitle 渠道名提示：显示名与模型 ID 不同时附带模型 ID，避免紧凑行丢失该信息。
function channelTitle(channel) {
  if (channel.modelId && channel.modelId !== channel.displayName) {
    return `${channel.displayName}（${channel.modelId}）`;
  }
  return channel.displayName;
}

function snapshotLabel() {
  return `快照：${formatDiagnosticTimestamp(diagnostics.value.generatedAtUnixMs, locale.value)}`;
}

function cacheTTLLabel() {
  return `目录 TTL：${diagnostics.value.modelCatalogCache.ttlSeconds || 0} 秒`;
}

function cacheExpiryLabel() {
  return `下次缓存过期：${formatDiagnosticTimestamp(diagnostics.value.modelCatalogCache.nextExpiryAtUnixMs, locale.value)}`;
}

function routerStateLabel() {
  if (diagnostics.value.state === "error") return "解析失败";
  if (diagnostics.value.routerAvailable) return "已连接";
  return "未就绪";
}

function diagnosticGuidance() {
  switch (diagnostics.value.errorCode) {
    case "channel_resolution_failed":
      return "渠道配置解析失败，无法生成运行状态快照。请检查模型配置后刷新。";
    case "diagnostics_resolver_unavailable":
      return "供应商诊断解析器不可用。请重启本地服务或应用后重试。";
    case "provider_module_unavailable":
    case "provider_gateway_unavailable":
      return "供应商运行模块尚未就绪。请重启本地服务后重试。";
    case "backend_not_running":
    case "backend_host_unavailable":
    case "client_service_unavailable":
    case "bridge_service_unavailable":
      return "内置 Router 尚未就绪。启动本地服务后可查看实时渠道状态。";
    default:
      return diagnostics.value.state === "error"
        ? "无法生成供应商运行状态快照，请刷新或重启本地服务。"
        : "内置 Router 尚未就绪。启动本地服务后可查看实时渠道状态。";
  }
}

function statusAnnouncement() {
  if (loading.value) return "正在读取供应商状态";
  if (error.value && !hasSnapshot.value) return "供应商状态读取失败";
  if (!hasSnapshot.value) return "";
  if (stale.value) return "供应商状态刷新失败，继续显示上次快照";
  if (diagnostics.value.state !== "ready") return diagnosticGuidance();
  return `供应商状态已更新：${diagnostics.value.readyCount} 个可用，${diagnostics.value.cooldownCount} 个冷却`;
}

onMounted(() => {
  void load();
  clock = window.setInterval(() => {
    now.value = Date.now();
  }, 30_000);
});

onUnmounted(() => {
  if (clock) window.clearInterval(clock);
});
</script>

<template>
  <section
    id="provider-runtime"
    class="rounded-[10px] border border-white/10 bg-black/20 p-5"
    aria-labelledby="provider-runtime-title"
    :aria-busy="loading"
    data-testid="provider-diagnostics-panel"
  >
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2
          id="provider-runtime-title"
          class="text-sm font-medium"
        >
          供应商运行状态
        </h2>
        <p class="mt-1 text-xs text-[#858585]">
          只读查看实际渠道、协议和冷却状态；不会发起模型请求或改变路由顺序。
        </p>
      </div>
      <Button
        variant="default"
        :disabled="loading"
        @click="load"
      >
        {{ loading ? "刷新中..." : "刷新状态" }}
      </Button>
    </div>

    <div
      class="sr-only"
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      {{ statusAnnouncement() }}
    </div>

    <div
      v-if="error"
      class="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]"
    >
      <span class="min-w-0">{{ error }}<span v-if="stale">（显示上次快照）</span></span>
      <Button
        variant="default"
        :disabled="loading"
        @click="load"
      >
        {{ loading ? "重试中..." : "重试" }}
      </Button>
    </div>

    <div
      v-if="loading && !loaded"
      class="rounded-[8px] border border-white/10 bg-black/15 px-4 py-6 text-center text-sm text-[#a3a3a3]"
    >
      正在读取供应商状态...
    </div>

    <template v-else-if="hasSnapshot">
      <div class="mb-4 grid gap-2 sm:grid-cols-4">
        <div class="rounded-[7px] border border-white/10 bg-[#1b1b1b] px-3 py-2">
          <div class="text-[11px] text-[#a3a3a3]">
            Router
          </div>
          <div
            class="mt-1 text-sm font-medium"
            :class="diagnostics.routerAvailable ? 'text-[#6ee7a5]' : 'text-[#fca5a5]'"
          >
            {{ routerStateLabel() }}
          </div>
        </div>
        <div class="rounded-[7px] border border-white/10 bg-[#1b1b1b] px-3 py-2">
          <div class="text-[11px] text-[#a3a3a3]">
            可直接使用
          </div>
          <div
            class="mt-1 text-sm font-medium text-white"
            style="font-family: var(--font-num)"
          >
            {{ diagnostics.readyCount }}
          </div>
        </div>
        <div class="rounded-[7px] border border-white/10 bg-[#1b1b1b] px-3 py-2">
          <div class="text-[11px] text-[#a3a3a3]">
            冷却降权
          </div>
          <div
            class="mt-1 text-sm font-medium text-[#fcd34d]"
            style="font-family: var(--font-num)"
          >
            {{ diagnostics.cooldownCount }}
          </div>
        </div>
        <div class="rounded-[7px] border border-white/10 bg-[#1b1b1b] px-3 py-2">
          <div class="text-[11px] text-[#a3a3a3]">
            模型目录缓存
          </div>
          <div
            class="mt-1 text-sm font-medium text-white"
            style="font-family: var(--font-num)"
          >
            {{ diagnostics.modelCatalogCache.entryCount }}
          </div>
        </div>
      </div>

      <div class="mb-3 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[#a3a3a3]">
        <span>{{ snapshotLabel() }}</span>
        <span>{{ cacheTTLLabel() }}</span>
        <span v-if="diagnostics.modelCatalogCache.nextExpiryAtUnixMs">{{ cacheExpiryLabel() }}</span>
      </div>

      <div
        v-if="diagnostics.state === 'error'"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-4 py-4 text-sm text-[#fca5a5]"
      >
        {{ diagnosticGuidance() }}
      </div>
      <div
        v-else-if="!diagnostics.routerAvailable"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-4 py-4 text-sm text-[#fca5a5]"
      >
        {{ diagnosticGuidance() }}
      </div>
      <div
        v-else-if="diagnostics.channels.length === 0"
        class="rounded-[8px] border border-white/10 bg-black/15 px-4 py-6 text-center text-sm text-[#858585]"
      >
        当前没有可诊断的模型渠道
      </div>
      <template v-else>
        <div class="mb-2 flex flex-wrap items-center gap-2">
          <input
            v-model="searchText"
            type="text"
            placeholder="筛选渠道：模型 / 分组 / 端点"
            class="min-w-0 flex-1 rounded-[6px] border border-white/10 bg-black/30 px-3 py-1.5 text-xs text-white outline-none focus:border-[#10AD5D]/60 sm:max-w-72"
          >
          <span
            class="shrink-0 text-[11px] text-[#858585]"
            style="font-family: var(--font-num)"
          >
            {{ filteredChannelCount }} / {{ diagnostics.channels.length }} 个渠道
          </span>
          <template v-if="groupedChannels.length > 1">
            <Button
              variant="text"
              @click="setAllGroups(true)"
            >
              全部展开
            </Button>
            <Button
              variant="text"
              @click="setAllGroups(false)"
            >
              全部收起
            </Button>
          </template>
        </div>

        <div
          v-if="groupedChannels.length === 0"
          class="rounded-[8px] border border-white/10 bg-black/15 px-4 py-6 text-center text-sm text-[#858585]"
        >
          没有匹配「{{ searchText.trim() }}」的渠道
        </div>
        <div
          v-else
          class="flex flex-col gap-2"
        >
          <div
            v-for="group in groupedChannels"
            :key="group.key"
            class="min-w-0 overflow-hidden rounded-[8px] border bg-[#1b1b1b]"
            :class="group.cooldown > 0 ? 'border-amber-800/60' : 'border-white/10'"
          >
            <button
              type="button"
              class="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-white/5"
              :aria-expanded="isGroupExpanded(group)"
              @click="toggleGroup(group)"
            >
              <span
                class="icon-[mdi--chevron-right] shrink-0 text-[14px] text-[#858585] transition-transform"
                :class="isGroupExpanded(group) ? 'rotate-90' : ''"
              />
              <span
                class="min-w-0 flex-1 truncate text-xs text-[#c7c7c7]"
                :title="group.endpoint"
              >
                {{ group.endpoint || "无有效端点" }}
              </span>
              <span class="shrink-0 text-[10px] text-[#858585]">
                {{ group.channels.length }} 个渠道<template v-if="group.cooldown > 0"> · <span class="text-[#fcd34d]">{{ group.cooldown }} 冷却</span></template>
              </span>
            </button>
            <div
              v-show="isGroupExpanded(group)"
              class="divide-y divide-white/5 border-t border-white/5"
            >
              <div
                v-for="channel in group.channels"
                :key="channel.channelId"
                class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 px-3 py-1.5 sm:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_auto]"
                data-testid="provider-diagnostic-channel"
              >
                <div class="flex min-w-0 items-center gap-2">
                  <span
                    class="size-1.5 shrink-0 rounded-full"
                    :class="channel.healthState === 'cooldown' ? 'bg-[#fcd34d]' : 'bg-[#6ee7a5]'"
                  />
                  <span
                    class="min-w-0 truncate text-xs text-white"
                    :title="channelTitle(channel)"
                  >
                    {{ channel.displayName }}
                  </span>
                  <span
                    v-if="!channel.credentialConfigured"
                    class="shrink-0 text-[10px] text-[#fcd34d]"
                  >
                    未配置凭据
                  </span>
                </div>
                <div
                  class="hidden min-w-0 truncate text-[11px] text-[#858585] sm:block"
                  :title="channelMeta(channel)"
                >
                  {{ channelMeta(channel) }}
                </div>
                <div class="flex shrink-0 items-center justify-end gap-2">
                  <span
                    class="hidden text-[11px] tabular-nums text-[#858585] md:inline"
                    :title="`上下文 / 输出：${formatTokens(channel.contextWindowTokens)} / ${formatTokens(channel.maxCompletionTokens)}`"
                  >
                    {{ formatTokens(channel.contextWindowTokens) }} / {{ formatTokens(channel.maxCompletionTokens) }}
                  </span>
                  <span
                    v-if="channel.healthState === 'cooldown'"
                    class="whitespace-nowrap text-[10px] text-[#fcd34d]"
                  >
                    {{ formatProviderCooldown(channel.cooldownUntilUnixMs, now) }}
                  </span>
                  <span
                    class="shrink-0 rounded-full border px-2 py-0.5 text-[10px]"
                    :class="channel.healthState === 'cooldown' ? 'border-amber-700/70 bg-amber-950/40 text-[#fcd34d]' : 'border-emerald-800/70 bg-emerald-950/30 text-[#a7f3d0]'"
                  >
                    {{ channel.healthState === "cooldown" ? "冷却降权" : "可用" }}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </template>
  </section>
</template>
