<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ModelAdapterTestCard from "@/components/ModelAdapterTestCard.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  deleteModelAdapterAt,
  getModelAdapterTestResultByID,
  openModelEditorWindow,
  reloadUserConfig,
  runModelAdapterTest,
  startModelAdapterTest,
  toUserError,
} from "@/state/appState";
import { computed, onBeforeUnmount, onMounted, reactive } from "vue";

const BATCH_TEST_CONCURRENCY = 10;
const createEmptyModelAdapter = () => ({
  id: "",
  displayName: "",
  groupName: "",
  type: "openai",
  baseURL: "",
  apiKey: "",
  tooltipData: "",
  modelID: "",
  reasoningEffort: "medium",
  openAIEndpoint: "/v1/responses",
  openAIExtraParamsEnabled: false,
  openAIExtraParamsJSON: "{\n}",
  customHeadersEnabled: false,
  customHeadersJSON: "{\n}",
  anthropicExtraParamsEnabled: false,
  anthropicExtraParamsJSON: "{\n}",
  contextWindowTokens: 0,
  maxCompletionTokens: 0,
  anthropicMaxTokens: 0,
  anthropicThinkingEffort: "xhigh",
  thinkingBudgetTokens: 0,
  pricing: null,
  fastMode: false,
  openAIServiceTier: "",
});
const providers = [
  { value: "openai", label: "OpenAI / OAI", icon: "icon-[bxl--openai]" },
  { value: "anthropic", label: "Anthropic / A社", icon: "icon-[logos--claude-icon]" },
];
const providerRuns = reactive(Object.fromEntries(providers.map(({ value }) => [value, {
  testing: false,
  stopping: false,
  total: 0,
  completed: 0,
  calls: new Set(),
  stopRequested: false,
}])));

const providerAdapters = computed(() => Object.fromEntries(providers.map(({ value }) => [
  value,
  appState.modelAdapters.filter((adapter) => adapter.type === value),
])));

const providerGroups = computed(() => Object.fromEntries(providers.map(({ value }) => {
  const groups = new Map();
  for (const adapter of providerAdapters.value[value]) {
    const groupName = String(adapter.groupName || "").trim() || "默认分组";
    if (!groups.has(groupName)) groups.set(groupName, []);
    groups.get(groupName).push(adapter);
  }
  return [value, Array.from(groups, ([name, adapters]) => ({ name, adapters }))];
})));

const providerFailedAdapters = computed(() => Object.fromEntries(providers.map(({ value }) => [
  value,
  providerAdapters.value[value].filter((adapter) => testResult(adapter)?.status === "error"),
])));

function providerLabel(type) {
  return providers.find((provider) => provider.value === type)?.label || type;
}

function typeLabel(type) {
  return providerLabel(type).split(" /")[0];
}

function formatHost(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  try { return new URL(text).host || text; } catch { return text.replace(/^https?:\/\//, ""); }
}

function maskSecret(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  if (text.length <= 8) return `${"*".repeat(Math.max(text.length - 2, 0))}${text.slice(-2)}`;
  return `${text.slice(0, 4)}****${text.slice(-4)}`;
}

async function showActionError(title, error) {
  await showModal({ title, content: String(error || "服务错误").trim() || "服务错误" });
}

function groupIndex(adapter) {
  return appState.modelAdapters.indexOf(adapter);
}

async function openEditor(providerType, index = -1, groupName = "", initialAdapter = null) {
  const seed = providerAdapters.value[providerType]?.find((item) =>
    (String(item.groupName || "").trim() || "默认分组") === groupName,
  ) || providerAdapters.value[providerType]?.[0] || {};
  const adapter = initialAdapter || (index >= 0
    ? appState.modelAdapters[index]
    : {
      ...createEmptyModelAdapter(),
      ...seed,
      id: "",
      displayName: "",
      modelID: "",
      type: providerType,
      groupName: groupName === "默认分组" ? "" : groupName,
    });
  try { await openModelEditorWindow(index, adapter); }
  catch (error) { await showActionError("打开失败", toUserError(error)); }
}

async function deleteAdapter(adapter) {
  const index = groupIndex(adapter);
  if (index < 0) return;
  const result = await deleteModelAdapterAt(index);
  if (!result.ok) await showActionError("删除失败", result.error);
}

async function deleteFailedAdapters(providerType) {
  const failed = providerFailedAdapters.value[providerType];
  if (!failed.length) return;
  const confirmed = await showModal({
    title: "删除测试失败的模型",
    content: `将删除 ${failed.length} 个测试失败的 ${providerLabel(providerType)} 模型，确定继续吗？`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  const indices = failed.map(groupIndex).filter((i) => i >= 0).sort((a, b) => b - a);
  for (const index of indices) {
    const result = await deleteModelAdapterAt(index);
    if (!result.ok) {
      await showActionError("删除失败", result.error);
      return;
    }
  }
}

async function duplicateAdapter(adapter) {
  const groupName = String(adapter.groupName || "").trim() || "默认分组";
  const displayName = String(adapter.displayName || adapter.modelID || "模型").trim();
  await openEditor(adapter.type, -1, groupName, {
    ...adapter,
    id: "",
    displayName: `${displayName}-副本`,
  });
}

function testResult(adapter) { return getModelAdapterTestResultByID(adapter?.id); }
function isTesting(adapter) { return testResult(adapter)?.status === "running"; }

async function testAdapter(adapter) {
  try { await runModelAdapterTest(adapter); } catch (_error) { /* card displays result */ }
}

async function stopProvider(providerType) {
  const run = providerRuns[providerType];
  if (!run.testing || run.stopping) return;
  run.stopRequested = true;
  run.stopping = true;
  await Promise.allSettled(Array.from(run.calls).map((call) =>
    typeof call?.cancel === "function" ? call.cancel("batch-stop") : undefined));
}

async function testProvider(providerType) {
  const run = providerRuns[providerType];
  if (run.testing) { await stopProvider(providerType); return; }
  const adapters = providerAdapters.value[providerType].slice();
  if (!adapters.length) return;
  run.stopRequested = false;
  run.testing = true;
  run.stopping = false;
  run.total = adapters.length;
  run.completed = 0;
  let next = 0;
  try {
    const workers = Array.from({ length: Math.min(BATCH_TEST_CONCURRENCY, adapters.length) }, async () => {
      while (!run.stopRequested) {
        const index = next++;
        if (index >= adapters.length) return;
        const call = startModelAdapterTest(adapters[index]);
        run.calls.add(call);
        try { await call; } catch (_error) { /* continue testing this provider */ }
        finally { run.calls.delete(call); run.completed += 1; }
      }
    });
    await Promise.allSettled(workers);
  } finally {
    run.calls.clear();
    run.stopRequested = false;
    run.testing = false;
    run.stopping = false;
  }
}

function batchText(providerType) {
  const run = providerRuns[providerType];
  if (run.stopping) return "停止中...";
  if (!run.testing) return "测试全部";
  return `停止测试 ${run.completed}/${run.total}`;
}

onMounted(() => { void reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {}); });
onBeforeUnmount(() => { providers.forEach(({ value }) => { void stopProvider(value); }); });
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div class="flex flex-col gap-5 pb-2">
        <section v-for="provider in providers" :key="provider.value" class="flex flex-col gap-3">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[#343434] pb-3">
            <div class="center-row gap-2">
              <span :class="[provider.icon, 'text-[20px]']"></span>
              <div>
                <h2 class="text-base font-medium text-white">{{ provider.label }}</h2>
                <div class="text-xs text-[#8f8f8f]">{{ providerAdapters[provider.value].length }} 个模型 · {{ providerGroups[provider.value].length }} 个分组</div>
              </div>
            </div>
            <div class="center-row gap-2">
              <Button
                variant="default"
                :disabled="appState.configSaving || (!providerRuns[provider.value].testing && !providerAdapters[provider.value].length)"
                @click="testProvider(provider.value)"
              >{{ batchText(provider.value) }}</Button>
              <Button
                v-if="providerFailedAdapters[provider.value].length && !providerRuns[provider.value].testing"
                variant="default"
                :disabled="appState.configSaving"
                @click="deleteFailedAdapters(provider.value)"
              >删除 {{ providerFailedAdapters[provider.value].length }} 个失败</Button>
              <Button
                variant="primary"
                :disabled="appState.configSaving || providerRuns[provider.value].testing"
                @click="openEditor(provider.value)"
              >新增 {{ typeLabel(provider.value) }} 模型</Button>
            </div>
          </div>

          <div v-if="!providerAdapters[provider.value].length" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
            当前还没有配置 {{ providerLabel(provider.value) }} 模型。
          </div>
          <div v-else class="flex flex-col gap-4">
            <section v-for="group in providerGroups[provider.value]" :key="`${provider.value}:${group.name}`" class="flex flex-col gap-3 rounded-[8px] border border-[#343434] bg-[#202020] p-3">
              <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[#343434] pb-2">
                <div>
                  <h3 class="text-sm font-medium text-white">{{ group.name }}</h3>
                  <div class="text-xs text-[#8f8f8f]">{{ group.adapters.length }} 个模型</div>
                </div>
                <Button
                  variant="default"
                  :disabled="appState.configSaving || providerRuns[provider.value].testing"
                  @click="openEditor(provider.value, -1, group.name)"
                >在此分组新增</Button>
              </div>
              <div class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(250px,1fr))]">
                <Card v-for="adapter in group.adapters" :key="adapter.id || `${adapter.baseURL}-${adapter.modelID}`">
                  <div class="flex h-full min-h-[154px] flex-col justify-between gap-3">
                    <div class="flex flex-col gap-2.5">
                      <div class="flex items-start justify-between gap-3">
                        <div class="min-w-0 flex-1">
                          <div class="truncate text-base font-medium text-white">{{ adapter.displayName }}</div>
                          <div class="mt-1 truncate text-sm text-[#8f8f8f]">{{ adapter.modelID }}</div>
                          <div v-if="adapter.type === 'openai'" class="mt-0.5 truncate text-xs text-[#737373]">{{ adapter.openAIEndpoint || "/v1/responses" }}</div>
                        </div>
                        <span class="shrink-0 rounded-[8px] border border-[#3f3f3f] px-2 py-1 text-[12px] text-[#cfcfcf]">{{ provider.label }}</span>
                      </div>
                      <div class="grid grid-cols-2 gap-2 text-sm text-[#a3a3a3]">
                        <div class="rounded-[8px] bg-[#232323] px-3 py-2"><div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">Host</div><div class="mt-1 truncate text-[#d4d4d4]">{{ formatHost(adapter.baseURL) }}</div></div>
                        <div class="rounded-[8px] bg-[#232323] px-3 py-2"><div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">API Key</div><div class="mt-1 truncate text-[#d4d4d4]">{{ maskSecret(adapter.apiKey) }}</div></div>
                      </div>
                      <ModelAdapterTestCard compact title="测试" empty-text="未测试" :result="testResult(adapter)" />
                    </div>
                    <div class="center-row flex-wrap justify-end gap-2 border-t border-[#343434] pt-3">
                      <Button variant="default" :disabled="appState.configSaving || providerRuns[provider.value].testing || isTesting(adapter)" @click="testAdapter(adapter)">{{ isTesting(adapter) ? "测试中..." : "测试" }}</Button>
                      <Button variant="default" :disabled="appState.configSaving" @click="openEditor(provider.value, groupIndex(adapter), group.name)">编辑</Button>
                      <Button variant="default" :disabled="appState.configSaving" @click="duplicateAdapter(adapter)">复制</Button>
                      <Button variant="text" :disabled="appState.configSaving" @click="deleteAdapter(adapter)">删除</Button>
                    </div>
                  </div>
                </Card>
              </div>
            </section>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>