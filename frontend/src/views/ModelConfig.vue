<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { showModal } from "@/composables/useModal";
import {
  appState,
  createEmptyModelAdapter,
  deleteModelAdaptersBySupplier,
  openModelEditorWindow,
  reloadUserConfig,
  toUserError,
} from "@/state/appState";
import { providerIcon, providerLabel } from "@/utils/providerMeta";
import { getModelAdapterTestResultByID } from "@/state/appState";
import { queryProviderBalance } from "@/services/clientApi";
import {
  SUPPLIER_GROUP_MODE_CONNECTION,
  SUPPLIER_GROUP_MODE_NAME,
  groupModelAdaptersAsSuppliers,
  loadSupplierGroupMode,
  saveSupplierGroupMode,
  supplierToRouteQuery,
} from "@/utils/supplierGrouping";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

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
});

const allSuppliers = computed(() =>
  groupModelAdaptersAsSuppliers(appState.modelAdapters, groupMode.value),
);

const searchQuery = ref("");

const suppliers = computed(() => {
  let list = allSuppliers.value;
  if (focusedGroupKey.value) {
    const matched = list.filter((supplier) => supplier.key === focusedGroupKey.value);
    if (matched.length) list = matched;
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

function maskSecret(value) {
  const text = String(value || "").trim();
  if (!text) return "-";
  if (text.length <= 8) return `${"*".repeat(Math.max(text.length - 2, 0))}${text.slice(-2)}`;
  return `${text.slice(0, 4)}****${text.slice(-4)}`;
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
    const names = [
      ...new Set(
        (supplier.models || [])
          .map((m) => String(m.groupName || "").trim() || "默认分组"),
      ),
    ];
    if (names.length <= 1) return supplier.groupName;
    return `${names[0]} 等 ${names.length} 个名称`;
  }
  return supplier.groupName;
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

// 按供应商懒加载余额（点击查询，结果缓存于组件状态，避免重复请求）
const balanceBySupplier = ref({}); // key -> { loading, loaded, data }

function balanceEntry(key) {
  return balanceBySupplier.value[key] || null;
}

function currencySymbol(currency) {
  const code = String(currency || "").toUpperCase();
  if (code === "USD") return "$";
  if (code === "CNY" || code === "RMB") return "¥";
  if (code === "EUR") return "€";
  return "";
}

function formatMoney(value, currency) {
  if (value == null || !Number.isFinite(Number(value))) return "—";
  const symbol = currencySymbol(currency);
  const num = Number(value).toFixed(2);
  return symbol ? `${symbol}${num}` : `${num} ${String(currency || "").toUpperCase()}`.trim();
}

function balanceSourceLabel(source) {
  if (source === "openai_billing") return "openai billing";
  if (source === "sub2api_usage") return "sub2api usage";
  if (source === "newapi") return "newapi";
  if (source === "configured") return "自定义查询";
  if (source === "deepseek") return "DeepSeek";
  if (source === "stepfun") return "阶跃星辰";
  if (source === "siliconflow") return "SiliconFlow";
  if (source === "openrouter") return "OpenRouter";
  if (source === "novita") return "Novita";
  return String(source || "").trim();
}

function balanceTooltip(key) {
  const data = balanceEntry(key)?.data;
  if (!data || !data.supported) return "";
  const source = balanceSourceLabel(data.source);
  const hasUsedTotal =
    (data.used != null && Number.isFinite(Number(data.used))) ||
    (data.total != null && Number.isFinite(Number(data.total)));
  let text = "";
  if (hasUsedTotal) {
    text = `已用 ${formatMoney(data.used, data.currency)} / 总额 ${formatMoney(data.total, data.currency)}`;
  }
  if (source) text += `${text ? " · " : ""}来源: ${source}`;
  return text;
}

function balanceMessage(key) {
  const data = balanceEntry(key)?.data;
  return (data && data.message) || "余额不可用";
}

async function loadSupplierBalance(supplier, forceRefresh = false) {
  const key = supplier.key;
  const existing = balanceBySupplier.value[key];
  if (existing && existing.loading) return;
  balanceBySupplier.value = {
    ...balanceBySupplier.value,
    [key]: { loading: true, loaded: false, data: existing?.data || null },
  };
  const rep = (supplier.models && supplier.models[0]) || supplier;
  const prevData = existing?.data || null;
  const hasLastGood = Boolean(prevData && prevData.supported);
  let data = null;
  try {
    const request = {
      type: supplier.type,
      baseURL: rep.baseURL || supplier.baseURL,
      apiKey: rep.apiKey || supplier.apiKey,
    };
    if (forceRefresh) request.forceRefresh = true;
    data = await queryProviderBalance(request);
  } catch (_e) {
    // invoke 层异常按瞬时处理：有上次成功值则保留（keep-last-good），否则置不可用。
    data = hasLastGood ? prevData : { supported: false, message: "查询失败" };
  }
  const normalized = data || { supported: false, message: "无返回结果" };
  // 瞬时失败且已有上次成功值：保留旧值（keep-last-good），不透出「余额不可用」。
  const nextData =
    !normalized.supported && normalized.transient && hasLastGood ? prevData : normalized;
  balanceBySupplier.value = {
    ...balanceBySupplier.value,
    [key]: { loading: false, loaded: true, data: nextData },
  };
}

async function showActionError(title, error) {
  await showModal({ title, content: String(error || "服务错误").trim() || "服务错误" });
}

function openSupplier(supplier) {
  router.push({
    path: "/supplier",
    query: supplierToRouteQuery(supplier),
  });
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
      baseURL: supplier.baseURL,
      groupName: supplier.groupNameRaw ?? (supplier.groupName === "默认分组" ? "" : supplier.groupName),
    });
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
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-[#343434] pb-3">
          <div class="min-w-0 text-sm text-[#a3a3a3]">
            <template v-if="searchQuery.trim()">筛选出 <span class="text-white">{{ suppliers.length }}</span>/{{ allSuppliers.length }} 个供应商</template>
            <template v-else><span class="text-white">{{ suppliers.length }}</span> 个供应商 · <span class="text-white">{{ appState.modelAdapters.length }}</span> 个模型</template>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <div class="relative">
              <span class="icon-[mdi--magnify] pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-[16px] text-[#737373]"></span>
              <input
                v-model="searchQuery"
                type="text"
                placeholder="搜索供应商 / 模型 / host"
                class="h-8 w-52 rounded-[8px] border border-[#3f3f3f] bg-[#232323] pl-7 pr-7 text-[12px] text-[#e5e5e5] outline-none focus:border-[#10AD5D]"
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
            <div
              class="inline-flex rounded-[8px] border border-[#3f3f3f] bg-[#232323] p-0.5 text-[12px]"
              role="group"
              aria-label="供应商分组方式"
            >
              <button
                type="button"
                class="rounded-[6px] px-2.5 py-1 transition-colors"
                :class="groupMode === SUPPLIER_GROUP_MODE_NAME
                  ? 'bg-[#10AD5D]/25 text-[#6ee7a5]'
                  : 'text-[#a3a3a3] hover:text-white'"
                @click="groupMode = SUPPLIER_GROUP_MODE_NAME"
              >
                名称分组
              </button>
              <button
                type="button"
                class="rounded-[6px] px-2.5 py-1 transition-colors"
                :class="groupMode === SUPPLIER_GROUP_MODE_CONNECTION
                  ? 'bg-[#10AD5D]/25 text-[#6ee7a5]'
                  : 'text-[#a3a3a3] hover:text-white'"
                @click="groupMode = SUPPLIER_GROUP_MODE_CONNECTION"
              >
                连接分组
              </button>
            </div>
            <Button variant="primary" :disabled="appState.configSaving" @click="openEditor">新增模型</Button>
          </div>
        </div>

        <!-- 分组聚焦提示条：来自「模型分组」页跳转 -->
        <div
          v-if="focusedGroupKey"
          class="flex items-center justify-between gap-3 rounded-[8px] border border-[#10AD5D]/40 bg-[#10AD5D]/10 px-3 py-2 text-xs text-[#6ee7a5]"
        >
          <span class="min-w-0 truncate">
            正在查看分组「{{ focusedGroupLabel || focusedGroupKey }}」
          </span>
          <button
            type="button"
            class="shrink-0 rounded-[6px] border border-[#10AD5D]/40 px-2 py-0.5 text-[#6ee7a5] transition-colors hover:bg-[#10AD5D]/20"
            @click="clearGroupFocus"
          >
            查看全部
          </button>
        </div>

        <!-- 供应商列表 -->
        <div v-if="!suppliers.length && searchQuery.trim()" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
          没有匹配「{{ searchQuery.trim() }}」的供应商或模型。
        </div>
        <div v-else-if="!suppliers.length" class="rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] px-4 py-8 text-center text-sm text-[#a3a3a3]">
          当前还没有配置任何模型，点击右上角"新增模型"开始添加。
        </div>

        <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(300px,1fr))]">
          <Card
            v-for="supplier in suppliers"
            :key="supplier.key"
            class="group cursor-pointer transition-colors hover:border-[#10AD5D]/40"
            @click="openSupplier(supplier)"
          >
            <div class="flex h-full flex-col gap-3">
              <div class="flex items-start gap-3">
                <span class="center-row size-9 shrink-0 justify-center rounded-[8px] bg-[#232323]">
                  <span :class="[providerIcon(supplier.type), 'text-[20px]']"></span>
                </span>
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-semibold text-white">{{ nameSummary(supplier) }}</div>
                  <div class="mt-0.5 truncate text-xs text-[#8f8f8f]">{{ hostSummary(supplier) }}</div>
                </div>
                <span class="shrink-0 rounded-full border border-[#3f3f3f] px-2 py-0.5 text-[11px] text-[#a3a3a3]">{{ providerLabel(supplier.type) }}</span>
              </div>

              <div class="flex flex-col gap-1 text-xs">
                <div class="center-row justify-start gap-1.5 text-[#a3a3a3]">
                  <span class="text-[#d4d4d4]">{{ supplier.models.length }} 个模型</span>
                  <span
                    v-if="healthSummary(supplier).tested > 0"
                    class="rounded-full px-1.5 py-0.5 text-[11px]"
                    :class="healthSummary(supplier).fail > 0 ? 'bg-[#f87171]/15 text-[#fca5a5]' : 'bg-[#10AD5D]/15 text-[#6ee7a5]'"
                    :title="`已测 ${healthSummary(supplier).tested}/${healthSummary(supplier).total}，可用 ${healthSummary(supplier).ok}，失败 ${healthSummary(supplier).fail}`"
                  >{{ healthSummary(supplier).ok }}/{{ healthSummary(supplier).total }} 可用</span>
                </div>
                <div class="truncate text-[#737373]">Key {{ maskSecret(supplier.apiKey) }}</div>
                <!-- 余额（懒加载：点击查询，结果缓存） -->
                <div class="center-row justify-start gap-1.5 text-[11px]">
                  <template v-if="balanceEntry(supplier.key)">
                    <span v-if="balanceEntry(supplier.key).loading" class="center-row gap-1 text-[#8f8f8f]">
                      <span class="icon-[mdi--loading] animate-spin text-[12px]"></span>查询余额…
                    </span>
                    <template v-else-if="balanceEntry(supplier.key).data && balanceEntry(supplier.key).data.supported">
                      <span class="text-[#6ee7a5]" :title="balanceEntry(supplier.key).data.unlimited ? '该账户额度不限' : balanceTooltip(supplier.key)">
                        {{ balanceEntry(supplier.key).data.unlimited ? "余额 不限额" : `余额 ${formatMoney(balanceEntry(supplier.key).data.remaining, balanceEntry(supplier.key).data.currency)}` }}
                      </span>
                      <button
                        type="button"
                        class="center-row text-[#737373] transition-colors hover:text-white"
                        title="刷新余额"
                        @click.stop="loadSupplierBalance(supplier, true)"
                      >
                        <span class="icon-[mdi--refresh] text-[12px]"></span>
                      </button>
                    </template>
                    <template v-else>
                      <span class="text-[#737373]" :title="balanceMessage(supplier.key)">余额不可用</span>
                      <button
                        type="button"
                        class="center-row text-[#737373] transition-colors hover:text-white"
                        title="重试"
                        @click.stop="loadSupplierBalance(supplier, true)"
                      >
                        <span class="icon-[mdi--refresh] text-[12px]"></span>
                      </button>
                    </template>
                  </template>
                  <button
                    v-else
                    type="button"
                    class="center-row gap-0.5 text-[#8f8f8f] transition-colors hover:text-[#6ee7a5]"
                    title="查询该供应商余额"
                    @click.stop="loadSupplierBalance(supplier)"
                  >
                    <span class="icon-[mdi--wallet-outline] text-[12px]"></span>查询余额
                  </button>
                </div>
              </div>

              <div class="center-row mt-auto justify-end gap-3 border-t border-[#343434] pt-2.5">
                <button
                  type="button"
                  class="center-row justify-center text-[#8f8f8f] transition-colors hover:text-[#f87171] disabled:opacity-50"
                  :disabled="appState.configSaving || deletingSupplierKey === supplier.key"
                  :title="deletingSupplierKey === supplier.key ? '删除中...' : '删除该供应商'"
                  @click.stop="handleDeleteSupplier(supplier)"
                >
                  <span class="icon-[mdi--trash-can-outline] text-[16px]"></span>
                </button>
                <span class="center-row gap-0.5 text-xs text-[#6ee7a5]">进入<span class="icon-[mdi--arrow-right] text-[14px]"></span></span>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  </div>
</template>