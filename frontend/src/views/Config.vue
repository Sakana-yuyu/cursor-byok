<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import LocaleSelect from "@/components/LocaleSelect.vue";
import Select from "@/components/ui/Select.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  openModelConfigWindow,
  persistUserConfig,
  reloadUserConfig,
  ROUTE_MODE_OPTIONS,
  toUserError,
} from "@/state/appState";
import { onMounted } from "vue";

const routeModeOptions = ROUTE_MODE_OPTIONS;

// 本地响应缓存配置：已纳入 appState 归一化白名单（normalizeConfig/buildConfigPayload），
// 因此直接绑定 appState.localResponseCache，随 persistUserConfig 一并持久化，
// 不再需要单独的透传保存，避免与 appState 状态不同步导致被后续保存清空。
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
        :enabled="appState.localResponseCache.enabled"
        label="本地响应缓存"
        description="对完全相同的请求复用上次响应，减少 token 支出。默认关闭；仅精确匹配命中，不影响 agent 正确性。"
        @change="(value) => (appState.localResponseCache.enabled = value)"
      />
      <div
        v-if="appState.localResponseCache.enabled"
        class="mt-3 grid grid-cols-1 gap-3 border-t border-[#343434] pt-3 sm:grid-cols-2"
      >
        <label class="flex flex-col gap-1">
          <span class="text-xs text-[#a3a3a3]">缓存有效期（秒）</span>
          <input
            v-model.number="appState.localResponseCache.ttlSeconds"
            type="number"
            min="0"
            placeholder="留空/0 = 默认 900"
            class="h-9 rounded-[8px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-[#a3a3a3]">最多缓存条数</span>
          <input
            v-model.number="appState.localResponseCache.maxEntries"
            type="number"
            min="0"
            placeholder="留空/0 = 默认 256"
            class="h-9 rounded-[8px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]"
          />
        </label>
      </div>
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
