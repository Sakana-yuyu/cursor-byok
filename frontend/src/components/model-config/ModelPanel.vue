<script setup>
// 模型详情面板：主从布局右栏的「选中模型」形态。
// 只读信息密集展示 + 高频操作（测试/编辑/复制/删除）；编辑跳窗口内路由 /model-editor。
import Button from "@/components/ui/Button.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  deleteModelAdapterAt,
  formatModelAdapterTestSummary,
  getModelAdapterTestResultByID,
  reloadUserConfig,
  runModelAdapterTest,
  toUserError,
} from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { stashModelEditorSeed } from "@/utils/modelEditorSeed";
import { computed } from "vue";
import { useRouter } from "vue-router";

const props = defineProps({
  adapter: { type: Object, default: null },
});

const emit = defineEmits(["deleted"]);

const router = useRouter();

const testResult = computed(() => (props.adapter ? getModelAdapterTestResultByID(props.adapter.id) : null));
const isTesting = computed(() => testResult.value?.status === "running");
const testSummary = computed(() => {
  const result = testResult.value;
  if (!result || !result.status || result.status === "running") return "";
  try {
    return formatModelAdapterTestSummary(result) || "";
  } catch {
    return "";
  }
});

const infoRows = computed(() => {
  const a = props.adapter;
  if (!a) return [];
  const rows = [
    { label: "模型 ID", value: a.modelID || "-" },
    { label: "协议", value: providerLabel(a.type) },
    { label: "接口地址", value: a.baseURL || "-" },
    { label: "分组", value: a.groupName || "默认分组" },
    { label: "思考强度", value: a.reasoningEffort || "-" },
    { label: "Fast", value: a.fastMode ? "开启" : "关闭" },
  ];
  if (a.contextWindowTokens) {
    rows.push({ label: "上下文窗口", value: `${a.contextWindowTokens} tokens` });
  }
  if (a.maxCompletionTokens) {
    rows.push({ label: "最大补全", value: `${a.maxCompletionTokens} tokens` });
  }
  if (a.pricing) {
    const parts = [];
    if (a.pricing.inputPerMillion != null) parts.push(`输入 ¥${a.pricing.inputPerMillion}/M`);
    if (a.pricing.outputPerMillion != null) parts.push(`输出 ¥${a.pricing.outputPerMillion}/M`);
    if (parts.length) rows.push({ label: "定价", value: parts.join(" · ") });
  }
  if (String(a.tooltipData || "").trim() && a.tooltipData !== "备注") {
    rows.push({ label: "备注", value: a.tooltipData });
  }
  return rows;
});

function index() {
  return appState.modelAdapters.indexOf(props.adapter);
}

async function handleTest() {
  if (!props.adapter || isTesting.value) return;
  try {
    await runModelAdapterTest(props.adapter);
    await reloadUserConfig({ modelAdaptersOnly: true });
  } catch (error) {
    await showModal({ title: "测试失败", content: toUserError(error) || "操作失败" });
  }
}

async function handleEdit() {
  if (!props.adapter) return;
  await router.push({ path: "/model-editor", query: { index: String(index()) } });
}

async function handleDuplicate() {
  if (!props.adapter) return;
  stashModelEditorSeed({
    ...props.adapter,
    id: "",
    displayName: `${props.adapter.displayName || props.adapter.modelID}-副本`,
  });
  await router.push({ path: "/model-editor", query: { index: "-1" } });
}

async function handleDelete() {
  const target = props.adapter;
  if (!target) return;
  const confirmed = await showModal({
    title: "删除模型",
    content: `确定删除「${target.displayName || target.modelID}」吗？`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  const at = index();
  if (at < 0) return;
  const result = await deleteModelAdapterAt(at);
  if (!result.ok) {
    await showModal({ title: "删除失败", content: String(result.error || "操作失败").trim() });
    return;
  }
  emit("deleted");
}
</script>

<template>
  <div v-if="adapter" class="flex h-full min-h-0 flex-col">
    <div class="flex items-start justify-between gap-3 border-b border-[#343434] pb-3">
      <div class="flex min-w-0 items-center gap-3">
        <span :class="providerIcon(adapter.type)" class="shrink-0 text-[28px] text-[#a3a3a3]" aria-hidden="true"></span>
        <div class="min-w-0">
          <h2 class="truncate text-base font-medium text-white">{{ adapter.displayName || adapter.modelID }}</h2>
          <div class="truncate text-xs text-[#8f8f8f]">{{ adapter.modelID }}</div>
        </div>
      </div>
      <div class="center-row shrink-0 gap-2">
        <Button variant="default" :disabled="isTesting" @click="handleTest">
          {{ isTesting ? "测试中..." : "测试" }}
        </Button>
        <Button variant="default" @click="handleEdit">编辑</Button>
        <Button variant="default" @click="handleDuplicate">复制</Button>
        <Button variant="danger" @click="handleDelete">删除</Button>
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div
        v-if="testResult && testResult.status && testResult.status !== 'running'"
        class="mt-3 rounded-[8px] border px-3 py-2 text-xs"
        :class="testResult.status === 'success'
          ? 'border-[#10AD5D]/40 bg-[#10AD5D]/10 text-[#6ee7a5]'
          : 'border-[#f87171]/40 bg-[#f87171]/10 text-[#fca5a5]'"
      >
        <div class="font-medium">{{ testResult.status === "success" ? "最近测试通过" : "最近测试失败" }}</div>
        <div v-if="testSummary" class="mt-1 whitespace-pre-wrap break-all text-[#a3a3a3]">{{ testSummary }}</div>
      </div>

      <dl class="mt-3 grid grid-cols-[max-content_1fr] items-baseline gap-x-4 gap-y-2 text-[13px]">
        <template v-for="row in infoRows" :key="row.label">
          <dt class="shrink-0 whitespace-nowrap text-[12px] text-[#8f8f8f]">{{ row.label }}</dt>
          <dd class="min-w-0 break-all text-[#e5e5e5]">{{ row.value }}</dd>
        </template>
      </dl>

      <div v-if="adapter.openAIExtraParamsEnabled || adapter.customHeadersEnabled || adapter.anthropicExtraParamsEnabled" class="mt-4 border-t border-[#242424] pt-3 text-xs text-[#a3a3a3]">
        <div v-if="adapter.openAIExtraParamsEnabled" class="truncate">OpenAI 扩展参数：{{ adapter.openAIExtraParamsJSON }}</div>
        <div v-if="adapter.anthropicExtraParamsEnabled" class="truncate">Anthropic 扩展参数：{{ adapter.anthropicExtraParamsJSON }}</div>
        <div v-if="adapter.customHeadersEnabled" class="truncate">自定义请求头：{{ adapter.customHeadersJSON }}</div>
      </div>
    </div>
  </div>

  <div v-else class="flex h-full items-center justify-center text-sm text-[#6f6f6f]">
    左侧选择一个供应商或模型查看详情
  </div>
</template>
