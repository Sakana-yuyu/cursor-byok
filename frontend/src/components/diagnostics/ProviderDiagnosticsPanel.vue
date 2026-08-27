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

const hasSnapshot = computed(() => rawSnapshot.value !== null);
const diagnostics = computed(() => normalizeProviderDiagnostics(rawSnapshot.value, now.value));

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
      <div
        v-else
        class="grid gap-2 sm:grid-cols-2"
      >
        <article
          v-for="channel in diagnostics.channels"
          :key="channel.channelId"
          class="min-w-0 rounded-[8px] border bg-[#1b1b1b] px-3 py-3"
          :class="channel.healthState === 'cooldown' ? 'border-amber-800/60' : 'border-white/10'"
          data-testid="provider-diagnostic-channel"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <h3
                class="truncate text-sm font-medium text-white"
                :title="channel.displayName"
              >
                {{ channel.displayName }}
              </h3>
              <p
                class="mt-0.5 truncate text-[11px] text-[#858585]"
                :title="channel.modelId"
              >
                {{ channel.modelId || "未设置模型" }}
              </p>
            </div>
            <span
              class="shrink-0 rounded-full border px-2 py-0.5 text-[10px]"
              :class="channel.healthState === 'cooldown' ? 'border-amber-700/70 bg-amber-950/40 text-[#fcd34d]' : 'border-emerald-800/70 bg-emerald-950/30 text-[#a7f3d0]'"
            >
              {{ channel.healthState === "cooldown" ? "冷却降权" : "可用" }}
            </span>
          </div>

          <dl class="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-[11px]">
            <dt class="text-[#a3a3a3]">
              供应商 / 协议
            </dt>
            <dd class="min-w-0 truncate text-[#c7c7c7]">
              {{ channel.provider || "—" }} · {{ channel.protocolGroup || channel.protocolMode || "—" }}
            </dd>
            <dt class="text-[#a3a3a3]">
              安全端点
            </dt>
            <dd
              class="min-w-0 truncate text-[#c7c7c7]"
              :title="channel.endpoint"
            >
              {{ channel.endpoint || "无有效端点" }}
            </dd>
            <dt class="text-[#a3a3a3]">
              上下文 / 输出
            </dt>
            <dd
              class="text-[#c7c7c7]"
              style="font-family: var(--font-num)"
            >
              {{ formatTokens(channel.contextWindowTokens) }} / {{ formatTokens(channel.maxCompletionTokens) }}
            </dd>
            <dt class="text-[#a3a3a3]">
              凭据
            </dt>
            <dd class="text-[#c7c7c7]">
              {{ channel.credentialConfigured ? "已配置" : "未配置" }}<span v-if="channel.customHeadersConfigured"> · 自定义请求头</span>
            </dd>
          </dl>

          <div
            v-if="channel.healthState === 'cooldown'"
            class="mt-3 rounded-[6px] bg-amber-950/30 px-2 py-1.5 text-[11px] text-[#fcd34d]"
          >
            {{ formatProviderCooldown(channel.cooldownUntilUnixMs, now) }} · {{ formatDiagnosticTimestamp(channel.cooldownUntilUnixMs, locale) }}
          </div>
        </article>
      </div>
    </template>
  </section>
</template>
