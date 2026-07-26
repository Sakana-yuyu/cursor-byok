<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import LocaleSelect from "@/components/LocaleSelect.vue";
import Select from "@/components/ui/Select.vue";
import { showModal } from "@/composables/useModal";
import { loadUserConfig, saveUserConfig } from "@/services/clientApi";
import {
  appState,
  openModelConfigWindow,
  persistUserConfig,
  reloadUserConfig,
  ROUTE_MODE_OPTIONS,
  toUserError,
} from "@/state/appState";
import { onMounted, reactive } from "vue";

const routeModeOptions = ROUTE_MODE_OPTIONS;

// 本地响应缓存开关：appState 的 normalizeConfig 不覆盖该字段，
// 因此这里直接读写原始用户配置，保存时透传其余字段避免被清空。
const localResponseCache = reactive({
  enabled: false,
  ttlSeconds: 0,
  maxEntries: 0,
});

function readLocalResponseCache(rawConfig) {
  const raw = rawConfig && typeof rawConfig === "object" ? rawConfig : {};
  const cache = raw.localResponseCache && typeof raw.localResponseCache === "object"
    ? raw.localResponseCache
    : {};
  const toPositive = (value) => {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
  };
  return {
    enabled: Boolean(cache.enabled),
    ttlSeconds: toPositive(cache.ttlSeconds),
    maxEntries: toPositive(cache.maxEntries),
  };
}

async function loadLocalResponseCache() {
  try {
    const raw = await loadUserConfig();
    const parsed = readLocalResponseCache(raw);
    localResponseCache.enabled = parsed.enabled;
    localResponseCache.ttlSeconds = parsed.ttlSeconds;
    localResponseCache.maxEntries = parsed.maxEntries;
  } catch (_error) {
    // 读取失败时保留默认（关闭）
  }
}

// 直接透传原始配置，仅覆盖 localResponseCache.enabled，
// 保留 ttl/maxEntries 及后端其余字段，避免 saveUserConfig 整体替换时丢失。
async function saveLocalResponseCache() {
  try {
    const raw = await loadUserConfig();
    const base = raw && typeof raw === "object" ? raw : {};
    const existing = base.localResponseCache && typeof base.localResponseCache === "object"
      ? base.localResponseCache
      : {};
    const payload = {
      ...base,
      localResponseCache: {
        ...existing,
        enabled: Boolean(localResponseCache.enabled),
      },
    };
    await saveUserConfig(payload);
    return { ok: true, error: "" };
  } catch (error) {
    return { ok: false, error: toUserError(error) };
  }
}

async function showActionError(title, error) {
  await showModal({
    title,
    content: String(error || "服务错误").trim() || "服务错误",
  });
}

async function handleSaveConfig() {
  const result = await persistUserConfig();
  if (!result.ok) {
    await showActionError("保存失败", result.error);
    return;
  }
  // persistUserConfig 走 appState 归一化路径，不含 localResponseCache，
  // 因此紧接着单独持久化缓存开关（透传其余字段）。
  const cacheResult = await saveLocalResponseCache();
  if (!cacheResult.ok) {
    await showActionError("保存失败", cacheResult.error);
    return;
  }
  await showModal({
    title: "提示",
    content: "本地配置已保存",
  });
}

async function handleOpenModelConfig() {
  try {
    await openModelConfigWindow();
  } catch (error) {
    await showActionError("打开失败", toUserError(error));
  }
}

onMounted(async () => {
  await reloadUserConfig().catch(() => {});
  await loadLocalResponseCache();
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4 pt-0 text-[#e5e5e5]">
    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">本地配置</h2>
          <div class="text-sm text-[#a3a3a3]">
            可配置运行模式和模型渠道；运行日志位于 <code>~/.cursor-local-assistant-v2/logs/</code>
          </div>
        </div>
        <Button variant="primary" :disabled="appState.configSaving" @click="handleSaveConfig">
          {{ appState.configSaving ? "保存中..." : "保存配置" }}
        </Button>
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">运行模式</h2>
          <div class="text-sm text-[#a3a3a3]">
            控制白名单主链路请求走本地服务，还是回到原始 Cursor 上游地址
          </div>
        </div>
        <div class="w-[220px] max-w-full">
          <Select
            v-model="appState.routingMode"
            :options="routeModeOptions"
            placeholder="选择模式"
          />
        </div>
      </div>
    </Card>

    <Card>
      <Switch
        :enabled="localResponseCache.enabled"
        label="本地响应缓存"
        description="对完全相同的请求复用上次响应，减少 token 支出。默认关闭；仅精确匹配命中，不影响 agent 正确性。"
        @change="(value) => (localResponseCache.enabled = value)"
      />
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">界面语言</h2>
          <div class="text-sm text-[#a3a3a3]">
            切换当前界面显示语言，设置会立即生效并保存在本机
          </div>
        </div>
        <LocaleSelect wrapper-class="w-[220px] max-w-full" />
      </div>
    </Card>

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">模型配置</h2>
          <div class="text-sm text-[#a3a3a3]">
            已配置 {{ appState.modelAdapters.length }} 个模型适配器
          </div>
        </div>
        <Button variant="primary" @click="handleOpenModelConfig">打开模型配置</Button>
      </div>
    </Card>
  </div>
</template>
