<script setup>
// 模型配置：主从布局。左栏供应商/模型树，右栏详情面板（SupplierPanel / ModelPanel），
// 供应商管理与编辑不再跳页，加模型的完整链路收敛在本页 + 窗口内编辑器路由。
import Button from "@/components/ui/Button.vue";
import ModelPanel from "@/components/model-config/ModelPanel.vue";
import SupplierPanel from "@/components/model-config/SupplierPanel.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  createEmptyModelAdapter,
  deleteModelAdaptersBySupplier,
  getModelAdapterTestResultByID,
  reloadUserConfig,
  setAutoDisableFailedModels,
  toUserError,
} from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { diagnoseModelAdapters, applyDiagnosticFixes, autoMatchContextWindows } from "@/services/clientApi";
import { stashModelEditorSeed } from "@/utils/modelEditorSeed";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  groupModelAdaptersAsSuppliers,
  loadSupplierGroupMode,
  saveSupplierGroupMode,
  supplierToRouteQuery,
  SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT,
} from "@/utils/supplierGrouping";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { onAccountSync } from "@/utils/accountSync";

const router = useRouter();
const route = useRoute();

const groupMode = ref(loadSupplierGroupMode());

// 来自「模型分组」页的分组跳转：?group=<supplier.key>，进入后仅聚焦该分组
const focusedGroupKey = ref(String(route.query.group || ""));
watch(
  () => route.query.group,
  (value) => { focusedGroupKey.value = String(value || ""); },
);

function clearGroupFocus() {
  focusedGroupKey.value = "";
  if (route.query.group != null) router.replace({ path: "/model-config", query: {} });
}

watch(groupMode, (mode) => {
  saveSupplierGroupMode(mode);
  // 切换分组模式后，旧模式的分组 key（带 name::/connection:: 前缀）在新模式下不再匹配，
  // 必须清掉聚焦，否则 suppliers 过滤会把列表滤空（表现为不显示名称/分组消失）。
  if (focusedGroupKey.value) {
    focusedGroupKey.value = "";
    if (route.query.group != null) {
      router.replace({ path: "/model-config", query: {} });
    }
  }
});

const allSuppliers = computed(() =>
  groupModelAdaptersAsSuppliers(appState.modelAdapters, groupMode.value),
);

const searchQuery = ref("");

const suppliers = computed(() => {
  let list = allSuppliers.value;
  if (focusedGroupKey.value) {
    // 仅在当前模式下确实命中时才聚焦；命中为空（如刚切换了模式）则回退全量，避免列表被滤空。
    const matched = list.filter((supplier) => supplier.key === focusedGroupKey.value);
    if (matched.length) list = matched;
    else focusedGroupKey.value = "";
  }
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return list;
  return list.filter((supplier) => {
    const name = String(nameSummary(supplier) || "").toLowerCase();
    const host = String(hostSummary(supplier) || "").toLowerCase();
    const models = (supplier.models || [])
      .map((m) => `${m.displayName || ""} ${m.modelID || ""} ${m.baseURL || ""}`)
      .join(" ")
      .toLowerCase();
    return name.includes(q) || host.includes(q) || models.includes(q);
  });
});

// 聚焦分组时展示的名称（用于顶部提示条）
const focusedGroupLabel = computed(() => {
  if (!focusedGroupKey.value) return "";
  const supplier = allSuppliers.value.find((item) => item.key === focusedGroupKey.value);
  return supplier ? String(nameSummary(supplier) || hostSummary(supplier) || "") : "";
});

function formatHost(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  try { return new URL(text).host || text; } catch { return text.replace(/^https?:\/\//, ""); }
}

function hostSummary(supplier) {
  if (groupMode.value === SUPPLIER_GROUP_MODE_NAME) {
    const hosts = [
      ...new Set(
        (supplier.models || []).map((m) => formatHost(m.baseURL)).filter((h) => h && h !== "-"),
      ),
    ];
    if (hosts.length === 0) return "-";
    if (hosts.length === 1) return hosts[0];
    return `${hosts[0]} 等 ${hosts.length} 个连接`;
  }
  return formatHost(supplier.baseURL);
}

function nameSummary(supplier) {
  if (groupMode.value === SUPPLIER_GROUP_MODE_CONNECTION) {
    // 连接分组：聚合该连接下所有分组名，统一基于 models 现算，避免与 bucket.groupName 数据源不一致。
    const names = [
      ...new Set(
        (supplier.models || [])
          .map((m) => String(m.groupName || "").trim() || "默认分组"),
      ),
    ];
    if (names.length === 0) return "默认分组";
    if (names.length === 1) return names[0];
    return `${names[0]} 等 ${names.length} 个名称`;
  }
  return supplier.groupName || "默认分组";
}

function supplierTitle(supplier) {
  if (supplier.source === SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT) {
    return "Cursor 账户模型";
  }
  const isNameMode = groupMode.value === SUPPLIER_GROUP_MODE_NAME;
  return isNameMode ? nameSummary(supplier) : hostSummary(supplier);
}

function supplierSubtitle(supplier) {
  if (supplier.source === SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT) {
    return "账户通道待验证";
  }
  const isNameMode = groupMode.value === SUPPLIER_GROUP_MODE_NAME;
  return isNameMode ? hostSummary(supplier) : nameSummary(supplier);
}

function healthSummary(supplier) {
  const models = supplier.models || [];
  let ok = 0;
  let fail = 0;
  let tested = 0;
  for (const model of models) {
    const result = getModelAdapterTestResultByID(model.id);
    if (!result || !result.status) continue;
    tested += 1;
    if (result.status === "success") ok += 1;
    else if (result.status === "error") fail += 1;
  }
  return { ok, fail, tested, total: models.length, untested: models.length - tested };
}

function modelTestState(adapter) {
  const result = getModelAdapterTestResultByID(adapter.id);
  if (!result || !result.status) return "untested";
  if (result.status === "success") return "ok";
  if (result.status === "running") return "running";
  return "fail";
}

function isCursorAccountSupplier(supplier) {
  return supplier?.source === SUPPLIER_MODEL_SOURCE_CURSOR_ACCOUNT;
}

// ─── 主从选择状态 ────────────────────────────────────────────────────────────
// selection: { type: "supplier", key } | { type: "model", id }
const selection = ref(null);
const expandedSupplierKeys = ref(new Set());

const selectedSupplier = computed(() => {
  if (selection.value?.type !== "supplier") return null;
  return suppliers.value.find((s) => s.key === selection.value.key) || null;
});
const selectedAdapter = computed(() => {
  if (selection.value?.type !== "model") return null;
  const id = selection.value.id;
  return appState.modelAdapters.find((a) => a.id === id) || null;
});
const selectedSupplierOfModel = computed(() => {
  if (!selectedAdapter.value) return null;
  return allSuppliers.value.find((s) => (s.models || []).some((m) => m.id === selectedAdapter.value.id)) || null;
});
const selectedSupplierIdentity = computed(() =>
  selectedSupplier.value ? supplierToRouteQuery(selectedSupplier.value) : { mode: groupMode.value },
);
const autoExpandEdit = ref(false);

// 选中项被删除/分组切换后回退到合理默认
watch([suppliers, () => appState.modelAdapters.length], () => {
  if (selection.value?.type === "supplier" && !suppliers.value.some((s) => s.key === selection.value.key)) {
    selection.value = null;
  }
  if (selection.value?.type === "model" && !appState.modelAdapters.some((a) => a.id === selection.value.id)) {
    selection.value = null;
  }
});

function toggleExpanded(key) {
  const next = new Set(expandedSupplierKeys.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  expandedSupplierKeys.value = next;
}

function selectSupplier(supplier) {
  autoExpandEdit.value = false;
  selection.value = { type: "supplier", key: supplier.key };
  if (!expandedSupplierKeys.value.has(supplier.key)) toggleExpanded(supplier.key);
}

function selectModel(supplier, adapter) {
  autoExpandEdit.value = false;
  selection.value = { type: "model", id: adapter.id };
  if (!expandedSupplierKeys.value.has(supplier.key)) toggleExpanded(supplier.key);
}

function backToList() {
  selection.value = null;
  autoExpandEdit.value = false;
}

async function openEditor(index, seed) {
  if (index < 0 && seed) {
    stashModelEditorSeed(seed);
  }
  await router.push({ path: "/model-editor", query: { index: String(index) } });
}

async function openNewModel() {
  await openEditor(-1, { ...createEmptyModelAdapter(), type: "openai" });
}

function openNewModelInSupplier(supplier) {
  const seed = supplier.models?.[0] || null;
  const draft = seed
    ? { ...createEmptyModelAdapter(), ...seed, id: "", displayName: "", modelID: "" }
    : { ...createEmptyModelAdapter(), type: "openai" };
  void openEditor(-1, draft);
}

const diagnosing = ref(false);

// 一键诊断优化：并行执行「协议诊断」+「上下文对齐」。
//  - 协议诊断：扫描已导入模型的协议配置，发现 claude/gemini 被误配为 openai 等问题，
//    弹窗确认后一键修正为原生协议（anthropic/gemini）。
//  - 上下文对齐：force 触发 autoMatchContextWindows（无视 autoMatchContextWindow 开关），
//    目录命中仅下调为真实窗口（如 gpt-5.6-luna=272K，用户手动设置的更小窗口保留），
//    目录未命中则探测 provider /models 回填。
// 两项都完成后刷新配置并汇总展示。
async function handleDiagnose() {
  if (diagnosing.value) return;
  diagnosing.value = true;
  try {
    const [diagResult, alignResult] = await Promise.all([
      diagnoseModelAdapters(),
      autoMatchContextWindows(true),
    ]);

    // 汇总上下文对齐结果
    let alignSummary = "";
    if (alignResult?.enabled) {
      alignSummary = `上下文对齐完成：共 ${alignResult.total ?? 0} 个，目录对齐 ${alignResult.fromCatalog ?? 0} 个，探测对齐 ${alignResult.fromProbe ?? 0} 个。`;
      if (alignResult.switchEnabled === false) {
        alignSummary += "\n（自动配对开关未开启，本次为手动强制对齐）";
      }
    } else {
      alignSummary = `上下文对齐未执行：共 ${alignResult?.total ?? 0} 个模型。`;
    }

    const issues = diagResult?.issues || [];
    // 区分两类问题：协议不匹配可一键修正；目录未覆盖只能提示（能力未知，交给用户补填）
    const mismatchIssues = issues.filter((i) => i.category === "provider_mismatch");
    const uncoveredIssues = issues.filter((i) => i.category === "catalog_uncovered");

    if (issues.length === 0) {
      await showModal({ title: "诊断完成", content: `已检查 ${diagResult?.total ?? 0} 个模型，未发现问题。\n\n${alignSummary}` });
      return;
    }

    let content = "";
    if (uncoveredIssues.length > 0) {
      const uncoveredSample = uncoveredIssues.slice(0, 5).map((i) => `· ${i.modelID}`).join("\n");
      const uncoveredMore = uncoveredIssues.length > 5 ? `\n……等共 ${uncoveredIssues.length} 个` : "";
      content += `检测到 ${uncoveredIssues.length} 个模型不在内置能力目录中（能力未知，图片不会直传；可在模型编辑页手动补充能力）：\n\n${uncoveredSample}${uncoveredMore}\n\n`;
    }
    if (mismatchIssues.length > 0) {
      const sample = mismatchIssues.slice(0, 5).map((i) => `· ${i.modelID}：${i.currentValue} → ${i.suggestedValue}`).join("\n");
      const more = mismatchIssues.length > 5 ? `\n……等共 ${mismatchIssues.length} 个问题` : "";
      content += `检测到 ${mismatchIssues.length} 个模型的协议配置可能不匹配（如 Claude/Gemini 被配为 OpenAI 协议，导致缓存失效）：\n\n${sample}${more}`;
    }
    content += `\n\n${alignSummary}`;

    if (mismatchIssues.length === 0) {
      await showModal({ title: "诊断完成", content });
      return;
    }
    const confirmed = await showModal({
      title: "发现协议配置问题",
      content: content + "\n\n是否一键修正为原生协议？",
      confirmText: "一键修正",
      cancelText: "取消",
    });
    if (!confirmed) return;
    await applyDiagnosticFixes(mismatchIssues.map((i) => i.channelId));
    await reloadUserConfig({ modelAdaptersOnly: true });
    await showModal({ title: "修正完成", content: `已修正 ${mismatchIssues.length} 个模型的协议配置。\n\n${alignSummary}` });
  } catch (error) {
    await showModal({ title: "诊断失败", content: toUserError(error) || "服务错误" });
  } finally {
    diagnosing.value = false;
  }
}

const deletingSupplierKey = ref("");

async function handleDeleteSupplier(supplier) {
  if (deletingSupplierKey.value) return;
  const label =
    groupMode.value === SUPPLIER_GROUP_MODE_CONNECTION
      ? formatHost(supplier.baseURL)
      : supplier.groupName;
  const confirmed = await showModal({
    title: "删除供应商",
    content: `确定删除「${label}」下的全部 ${supplier.models.length} 个模型吗？此操作不可撤销。`,
    confirmText: "删除",
    cancelText: "取消",
  });
  if (!confirmed) return;
  deletingSupplierKey.value = supplier.key;
  try {
    const result = await deleteModelAdaptersBySupplier({
      mode: supplier.mode || groupMode.value,
      source: supplier.source,
      baseURL: supplier.baseURL,
      groupName: supplier.groupNameRaw ?? (supplier.groupName === "默认分组" ? "" : supplier.groupName),
    });
    if (!result.ok) {
      await showModal({ title: "删除失败", content: String(result.error || "服务错误").trim() || "服务错误" });
    }
  } finally {
    deletingSupplierKey.value = "";
  }
}

onMounted(() => {
  void reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {});
  stopAccountSync = onAccountSync(() => {
    void reloadUserConfig({ modelAdaptersOnly: true }).catch(() => {});
  });
});
let stopAccountSync = () => {};
onBeforeUnmount(() => {
  stopAccountSync();
});
</script>

<template>
  <div class="flex h-full min-h-0 text-[#e5e5e5]">
    <!-- 左栏：供应商 + 模型树 -->
    <div class="flex w-[280px] shrink-0 flex-col border-r border-[#242424]">
      <div class="flex flex-col gap-2 border-b border-[#242424] p-3">
        <div class="flex items-center justify-between gap-2">
          <div class="min-w-0 truncate text-[12px] text-[#8f8f8f]">
            <span class="text-white">{{ suppliers.length }}</span> 供应商 ·
            <span class="text-white">{{ appState.modelAdapters.length }}</span> 模型
          </div>
          <button
            type="button"
            aria-label="新增模型"
            :title="$ls('e552c2accdbf5178')"
            class="flex h-[24px] w-[24px] shrink-0 cursor-pointer items-center justify-center rounded-[6px] bg-gradient-to-b from-[#10AD5D] to-[#0F8A4C] text-white transition-transform duration-150 hover:from-[#12b966] hover:to-[#119a55] active:scale-105"
            @click="openNewModel"
          >
            <span class="icon-[mdi--plus] shrink-0 text-[16px]"></span>
          </button>
        </div>
        <label class="flex items-start gap-2 rounded-[8px] border border-[#343434] bg-[#232323] px-2 py-1.5 text-[11px]">
          <input
            type="checkbox"
            class="mt-0.5 size-3.5 accent-[#10AD5D]"
            :checked="appState.autoDisableFailedModels"
            @change="setAutoDisableFailedModels($event.target.checked)"
          />
          <span class="min-w-0 text-[#a3a3a3]">
            <span class="block text-[#d4d4d4]">测试失败自动停用</span>
            <span class="block leading-4">失败模型不会进入 Cursor 列表</span>
          </span>
        </label>
        <div class="relative">
          <span class="icon-[mdi--magnify] pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-[16px] text-[#737373]"></span>
          <input
            v-model="searchQuery"
            type="text"
            placeholder="搜索供应商 / 模型 / host"
            class="h-8 w-full rounded-[8px] border border-[#3f3f3f] bg-[#232323] pl-7 pr-7 text-[12px] text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
          />
          <button
            v-if="searchQuery"
            type="button"
            class="absolute right-2 top-1/2 -translate-y-1/2 text-[#737373] hover:text-white"
            @click="searchQuery = ''"
          >
            <span class="icon-[mdi--close-circle] text-[14px]"></span>
          </button>
        </div>
        <div class="flex items-center gap-2">
          <div
            class="inline-flex flex-1 rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]"
            role="group"
            aria-label="供应商分组方式"
          >
            <button
              type="button"
              class="flex-1 rounded-[6px] px-2 py-1 transition-colors"
              :class="groupMode === SUPPLIER_GROUP_MODE_NAME ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
              @click="groupMode = SUPPLIER_GROUP_MODE_NAME"
            >
              名称分组
            </button>
            <button
              type="button"
              class="flex-1 rounded-[6px] px-2 py-1 transition-colors"
              :class="groupMode === SUPPLIER_GROUP_MODE_CONNECTION ? 'bg-[#10AD5D]/25 text-[#6ee7a5]' : 'text-[#a3a3a3] hover:text-white'"
              @click="groupMode = SUPPLIER_GROUP_MODE_CONNECTION"
            >
              连接分组
            </button>
          </div>
          <button
            type="button"
            class="center-row h-[26px] shrink-0 justify-center rounded-[8px] border border-[#3f3f3f] bg-[#232323] px-2 text-[#a3a3a3] transition-colors hover:text-white"
            :title="diagnosing ? '诊断中...' : '一键诊断优化'"
            :disabled="diagnosing"
            @click="handleDiagnose"
          >
            <span :class="diagnosing ? 'icon-[mdi--loading] animate-spin' : 'icon-[mdi--stethoscope]'" class="text-[15px]"></span>
          </button>
        </div>
        <div
          v-if="focusedGroupKey"
          class="flex items-center justify-between gap-2 rounded-[8px] border border-[#10AD5D]/40 bg-[#10AD5D]/10 px-2 py-1.5 text-[11px] text-[#6ee7a5]"
        >
          <span class="min-w-0 truncate">分组「{{ focusedGroupLabel || focusedGroupKey }}」</span>
          <button type="button" class="shrink-0 underline-offset-2 hover:underline" @click="clearGroupFocus">查看全部</button>
        </div>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <div v-if="!suppliers.length && searchQuery.trim()" class="px-2 py-6 text-center text-[12px] text-[#8f8f8f]">
          没有匹配「{{ searchQuery.trim() }}」的结果。
        </div>
        <div v-else-if="!suppliers.length" class="px-2 py-6 text-center text-[12px] text-[#8f8f8f]">
          还没有配置任何模型，点击「新增模型」开始添加。
        </div>

        <div v-for="supplier in suppliers" :key="supplier.key" class="mb-0.5">
          <div
            class="group flex h-[30px] cursor-pointer items-center gap-1.5 rounded-[6px] px-1.5 text-[13px] transition-colors"
            :class="selection?.type === 'supplier' && selection.key === supplier.key ? 'bg-[#1f3a2c] text-[#4ade80]' : 'text-[#d4d4d4] hover:bg-[#252525]'"
            @click="selectSupplier(supplier)"
          >
            <button
              type="button"
              class="center-row h-[20px] w-[16px] shrink-0 justify-center text-[#737373]"
              :title="expandedSupplierKeys.has(supplier.key) ? '收起' : '展开'"
              @click.stop="toggleExpanded(supplier.key)"
            >
              <span
                class="text-[14px] transition-transform"
                :class="[expandedSupplierKeys.has(supplier.key) ? 'icon-[mdi--chevron-down]' : 'icon-[mdi--chevron-right]']"
              ></span>
            </button>
            <span :class="providerIcon(supplier.type)" class="shrink-0 text-[16px]" :title="providerLabel(supplier.type)" aria-hidden="true"></span>
            <span class="min-w-0 flex-1 truncate">{{ supplierTitle(supplier) }}</span>
            <span
              v-if="healthSummary(supplier).tested > 0"
              class="shrink-0 rounded-full px-1.5 text-[10px] leading-[16px]"
              :class="healthSummary(supplier).fail > 0 ? 'bg-[#f87171]/15 text-[#fca5a5]' : 'bg-[#10AD5D]/15 text-[#6ee7a5]'"
              :title="`已测 ${healthSummary(supplier).tested}/${healthSummary(supplier).total}，可用 ${healthSummary(supplier).ok}，失败 ${healthSummary(supplier).fail}`"
            >{{ healthSummary(supplier).ok }}/{{ healthSummary(supplier).total }}</span>
            <span class="shrink-0 text-[11px] text-[#6f6f6f]">{{ supplier.models.length }}</span>
          </div>

          <div v-if="expandedSupplierKeys.has(supplier.key) || (selection?.type === 'model' && selectedSupplierOfModel?.key === supplier.key)">
            <button
              v-for="model in supplier.models"
              :key="model.id"
              type="button"
              class="flex h-[26px] w-full cursor-pointer items-center gap-2 rounded-[6px] pl-[38px] pr-2 text-[12px] transition-colors"
              :class="selection?.type === 'model' && selection.id === model.id ? 'bg-[#1f3a2c] text-[#4ade80]' : 'text-[#a3a3a3] hover:bg-[#252525] hover:text-[#e5e5e5]'"
              @click="selectModel(supplier, model)"
            >
              <span
                class="h-[6px] w-[6px] shrink-0 rounded-full"
                :class="{
                  'bg-[#10AD5D]': modelTestState(model) === 'ok',
                  'bg-[#f87171]': modelTestState(model) === 'fail',
                  'bg-[#38bdf8] animate-pulse': modelTestState(model) === 'running',
                  'bg-[#4b4b4b]': modelTestState(model) === 'untested',
                }"
                :title="{ ok: '可用', fail: '测试失败', running: '测试中', untested: '未测试' }[modelTestState(model)]"
              ></span>
              <span class="min-w-0 flex-1 truncate">{{ model.displayName || model.modelID }}</span>
              <span
                v-if="model.fastMode"
                class="shrink-0 rounded-full bg-[#67e8f9]/15 px-1 text-[10px] leading-[16px] text-[#67e8f9]"
                title="Fast 模式"
              >F</span>
            </button>
            <button
              v-if="!isCursorAccountSupplier(supplier)"
              type="button"
              class="flex h-[26px] w-full cursor-pointer items-center gap-2 rounded-[6px] pl-[38px] pr-2 text-[12px] text-[#6f6f6f] transition-colors hover:text-[#6ee7a5]"
              @click="openNewModelInSupplier(supplier)"
            >
              <span class="icon-[mdi--plus] text-[14px]"></span>
              <span>在此供应商下新增</span>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 右栏：详情面板 -->
    <div class="min-h-0 min-w-0 flex-1 overflow-hidden">
      <div class="h-full min-h-0 overflow-y-auto">
        <SupplierPanel
          v-if="selectedSupplier"
          :key="`supplier-${selectedSupplier.key}`"
          :identity="selectedSupplierIdentity"
          :auto-expand-edit="autoExpandEdit"
          @back="backToList"
        >
          <template #actions>
            <button
              type="button"
              class="center-row gap-1 text-xs text-[#a3a3a3] transition-colors hover:text-[#f87171]"
              :disabled="deletingSupplierKey === selectedSupplier.key"
              :title="deletingSupplierKey === selectedSupplier.key ? '删除中...' : '删除该供应商'"
              @click="handleDeleteSupplier(selectedSupplier)"
            >
              <span class="icon-[mdi--trash-can-outline] text-[16px]"></span>删除
            </button>
          </template>
        </SupplierPanel>
        <div v-else-if="selectedAdapter" class="h-full p-4 pt-0">
          <ModelPanel
            :adapter="selectedAdapter"
            @deleted="backToList"
          />
        </div>
        <div v-else class="flex h-full flex-col items-center justify-center gap-3 text-[#6f6f6f]">
          <span class="icon-[mdi--layers-triple-outline] text-[40px] opacity-50" aria-hidden="true"></span>
          <div class="text-sm">左侧选择供应商或模型查看详情</div>
          <Button variant="primary" @click="openNewModel">新增模型</Button>
        </div>
      </div>
    </div>
  </div>
</template>
