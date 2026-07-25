<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  deleteModelAdaptersBySupplier,
  openModelEditorWindow,
  reloadUserConfig,
  OPENAI_REQUEST_GROUP_RESPONSES,
  PROTOCOL_MODE_AUTO,
  toUserError,
} from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();

// 按 baseURL + groupName 组合提取供应商列表
const suppliers = computed(() => {
  const map = new Map();
  for (const adapter of appState.modelAdapters) {
    const baseURL = String(adapter.baseURL || "").trim();
    const groupName = String(adapter.groupName || "").trim();
    const key = `${baseURL}::${groupName}`;
    if (!map.has(key)) {
      map.set(key, {
        key,
        baseURL,
        groupName: groupName || "默认分组",
        type: adapter.type,
        apiKey: adapter.apiKey,
        customHeadersEnabled: adapter.customHeadersEnabled,
        customHeadersJSON: adapter.customHeadersJSON,
        models: [],
      });
    }
    map.get(key).models.push(adapter);
  }
  return Array.from(map.values());
});

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

function openSupplier(supplier) {
  router.push({
    path: "/supplier",
    query: {
      baseURL: supplier.baseURL,
      groupName: supplier.groupName === "默认分组" ? "" : supplier.groupName,
    },
  });
}

function createEmptyModelAdapter() {
  return {
    id: "",
    displayName: "",
    groupName: "",
    type: "openai",
    protocolMode: PROTOCOL_MODE_AUTO,
    protocolGroup: OPENAI_REQUEST_GROUP_RESPONSES,
    baseURL: "",
    apiKey: "",
    tooltipData: "",
    modelID: "",
    reasoningEffort: "medium",
    openAIEndpoint: "/v1/responses",
    openAIRequestGroup: OPENAI_REQUEST_GROUP_RESPONSES,
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
  };
}

async function openEditor() {
  try {
    await openModelEditorWindow(-1, { ...createEmptyModelAdapter(), type: "openai" });
  } catch (error) {
    await showActionError("打开失败", toUserError(error));
  }
}

const deletingSupplierKey = ref("");

async function handleDeleteSupplier(supplier) {
  if (deletingSupplierKey.value) return;
  const confirmed = await showModal({
    title: "删除供应商",
    content: `确定删除「${supplier.groupName}」下的全部 ${supplier.models.length} 个模型吗？此操作不可撤销。`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  deletingSupplierKey.value = supplier.key;
  try {
    const result = await deleteModelAdaptersBySupplier(
      supplier.baseURL,
      supplier.groupName === "默认分组" ? "" : supplier.groupName,
    );
    if (!result.ok) {
      await showActionError("删除失败", result.error);
    }
  } finally {
    deletingSupplierKey.value = "";
  }
}

onMounted(() => { void reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {}); });
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div class="flex flex-col gap-4 pb-2">
        <!-- 顶部操作栏 -->
        <div class="flex items-center justify-between gap-3 border-b border-[#343434] pb-3">
          <div>
            <h2 class="text-base font-medium text-white">模型配置</h2>
            <div class="text-xs text-[#8f8f8f]">{{ suppliers.length }} 个供应商 · {{ appState.modelAdapters.length }} 个模型</div>
          </div>
          <Button variant="primary" :disabled="appState.configSaving" @click="openEditor">新增模型</Button>
        </div>

        <!-- 供应商列表 -->
        <div v-if="!suppliers.length" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
          当前还没有配置任何模型，点击右上角"新增模型"开始添加。
        </div>

        <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(320px,1fr))]">
          <Card
            v-for="supplier in suppliers"
            :key="supplier.key"
            class="cursor-pointer transition-colors hover:border-[#10AD5D]/40"
            @click="openSupplier(supplier)"
          >
            <div class="flex h-full min-h-[120px] flex-col justify-between gap-3">
              <div class="flex flex-col gap-2.5">
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-base font-medium text-white">{{ supplier.groupName }}</div>
                    <div class="mt-1 truncate text-sm text-[#8f8f8f]">{{ formatHost(supplier.baseURL) }}</div>
                  </div>
                  <span :class="[providerIcon(supplier.type), 'text-[20px] shrink-0']"></span>
                </div>
                <div class="grid grid-cols-2 gap-2 text-sm text-[#a3a3a3]">
                  <div class="rounded-[8px] bg-[#232323] px-3 py-2">
                    <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">模型数</div>
                    <div class="mt-1 text-[#d4d4d4]">{{ supplier.models.length }}</div>
                  </div>
                  <div class="rounded-[8px] bg-[#232323] px-3 py-2">
                    <div class="text-[12px] uppercase tracking-[0.08em] text-[#666]">API Key</div>
                    <div class="mt-1 truncate text-[#d4d4d4]">{{ maskSecret(supplier.apiKey) }}</div>
                  </div>
                </div>
              </div>
              <div class="center-row justify-between border-t border-[#343434] pt-3">
                <span class="rounded-[8px] border border-[#3f3f3f] px-2 py-1 text-[12px] text-[#cfcfcf]">{{ providerLabel(supplier.type) }}</span>
                <div class="center-row gap-2">
                  <Button
                    variant="text"
                    class="text-[#f87171] hover:text-[#fca5a5]"
                    :disabled="appState.configSaving || deletingSupplierKey === supplier.key"
                    @click.stop="handleDeleteSupplier(supplier)"
                  >
                    {{ deletingSupplierKey === supplier.key ? "删除中..." : "删除" }}
                  </Button>
                  <span class="text-xs text-[#6ee7a5]">点击进入 -></span>
                </div>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  </div>
</template>