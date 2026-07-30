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
import { autoMatchContextWindows } from "@/services/clientApi";
import { onMounted, ref } from "vue";

const routeModeOptions = ROUTE_MODE_OPTIONS;
const delegationModeOptions = [
  { value: "auto", label: "自动选择" },
  { value: "cursor", label: "Cursor 子会话" },
  { value: "local", label: "本地子代理" },
];

function addDelegationGroup() {
  const index = appState.delegation.groups.length + 1;
  appState.delegation.groups.push({
    id: `delegation-group-${Date.now()}`,
    name: `委派模型组 ${index}`,
    enabled: true,
    modelIDs: [],
    defaultModelID: "",
    executionMode: "auto",
    toolPermissions: {},
  });
}

function removeDelegationGroup(index) {
  appState.delegation.groups.splice(index, 1);
}

function toggleDelegationModel(group, modelID, enabled) {
  const next = new Set(group.modelIDs || []);
  if (enabled) next.add(modelID);
  else next.delete(modelID);
  group.modelIDs = [...next];
  if (!group.modelIDs.includes(group.defaultModelID)) group.defaultModelID = group.modelIDs[0] || "";
}

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

const autoMatchBusy = ref(false);

async function handleAutoMatchContextWindows() {
  if (autoMatchBusy.value) return;
  autoMatchBusy.value = true;
  try {
    const result = await autoMatchContextWindows();
    if (!result || !result.enabled) {
      await showModal({ title: "提示", content: "自动配对上下文窗口开关已关闭，可在下方「上下文配对」中开启。" });
      return;
    }
    // 配对可能改写了已存储的适配器，重新拉取一份配置刷新本地状态。
    await reloadUserConfig().catch(() => {});
    const detail = `共 ${result.total} 个：目录命中 ${result.fromCatalog}，探测命中 ${result.fromProbe}，未变 ${result.unchanged}。${result.changed ? "已更新。" : "无需更新。"}`;
    await showModal({ title: "自动配对完成", content: detail });
  } catch (error) {
    await showActionError("自动配对失败", toUserError(error));
  } finally {
    autoMatchBusy.value = false;
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
      <div class="flex flex-col gap-3">
        <div class="flex items-center justify-between gap-4">
          <div>
            <h2 class="text-base font-medium text-white">模型配置</h2>
            <div class="text-sm text-[#a3a3a3]">
              已配置 {{ appState.modelAdapters.length }} 个模型适配器
            </div>
          </div>
          <div class="flex items-center gap-2">
            <Button variant="default" :disabled="autoMatchBusy" @click="handleAutoMatchContextWindows">
              {{ autoMatchBusy ? "配对中..." : "一键配对上下文" }}
            </Button>
            <Button variant="primary" @click="handleOpenModelConfig">打开模型配置</Button>
          </div>
        </div>
      </div>
    </Card>

    <Card>
      <Switch
        :enabled="appState.autoMatchContextWindow"
        label="自动配对上下文窗口"
        description="开启后，启动时与点击「一键配对上下文」时，自动按内置目录为模型配对正确的上下文窗口（目录命中则覆盖，目录未命中则探测供应商 /models 回填）。可避免误填过大窗口导致 context_length_exceeded。"
        @change="(value) => (appState.autoMatchContextWindow = value)"
      />
    </Card>

    <Card>
      <div class="flex flex-col gap-3">
        <div class="flex items-center justify-between gap-3">
          <div>
            <h2 class="text-base font-medium text-white">Multitask 委派</h2>
            <div class="text-sm text-[#a3a3a3]">使用已配置模型并行处理子任务，失败的子任务不会阻塞其他任务。</div>
          </div>
          <Switch label="启用" :enabled="appState.delegation.enabled" @change="(value) => (appState.delegation.enabled = value)" />
        </div>
        <label class="flex max-w-[220px] flex-col gap-1 text-xs text-[#a3a3a3]">
          <span>最大并发数</span>
          <input v-model.number="appState.delegation.maxConcurrency" type="number" min="1" class="h-9 rounded-[8px] border border-[#3f3f3f] bg-[#232323] px-3 text-sm text-white outline-none focus:border-[#10AD5D]" />
        </label>
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs font-medium text-[#a3a3a3]">模型组</span>
          <Button variant="default" @click="addDelegationGroup">新增模型组</Button>
        </div>
        <div v-if="!appState.delegation.groups.length" class="rounded-[8px] border border-dashed border-[#3f3f3f] px-3 py-4 text-xs text-[#858585]">尚未配置委派模型组，请先在模型配置中添加模型。</div>
        <div v-for="(group, groupIndex) in appState.delegation.groups" :key="group.id" class="space-y-3 rounded-[8px] border border-white/10 bg-black/15 p-3">
          <div class="flex items-center gap-2">
            <input v-model="group.name" class="h-8 min-w-0 flex-1 rounded border border-white/10 bg-black/20 px-2 text-sm text-white" aria-label="委派模型组名称" />
            <Switch compact label="启用" :enabled="group.enabled" @change="(value) => (group.enabled = value)" />
            <button type="button" class="shrink-0 text-xs text-[#fca5a5] hover:text-white" title="删除模型组" @click="removeDelegationGroup(groupIndex)">删除</button>
          </div>
          <Select v-model="group.executionMode" :options="delegationModeOptions" aria-label="委派执行模式" />
          <Select v-model="group.defaultModelID" :options="appState.modelAdapters.filter((item) => group.modelIDs.includes(item.id)).map((item) => ({ value: item.id, label: item.displayName || item.modelID }))" aria-label="默认委派模型" :disabled="!group.modelIDs.length" />
          <div class="grid gap-2 sm:grid-cols-2">
            <label v-for="adapter in appState.modelAdapters" :key="adapter.id" class="flex min-w-0 items-center gap-2 text-xs text-[#cfcfcf]">
              <input type="checkbox" :checked="group.modelIDs.includes(adapter.id)" @change="(event) => toggleDelegationModel(group, adapter.id, event.target.checked)" />
              <span class="min-w-0 truncate" :title="adapter.displayName || adapter.modelID">{{ adapter.displayName || adapter.modelID }}</span>
            </label>
          </div>
        </div>
      </div>
    </Card>
  </div>
</template>
