<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import {
  PROVIDER_BALANCES_SYNCED_EVENT,
  openModelConfig,
  queryAllProviderBalances,
  syncProviderBalancesAfterAccountChange,
} from "@/services/clientApi";
import { runtimeEvents } from "@/services/runtimeAdapter";
import { appState } from "@/state/appState";
import { normalizeSupplierBaseURL } from "@/utils/supplierGrouping";
import { formatMoney } from "@/utils/format";
import { onAccountSync } from "@/utils/accountSync";
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

const balances = ref([]);
const loading = ref(false);
const syncing = ref(false);
const error = ref("");

const billingQueryEnabled = computed(() => appState.billingQuery?.enabled !== false);

// 余额按连接（规范化 baseURL）去重分组：同一中转站的全部模型通道共享同一份
// 余额查询结果（查询本身也按 URL+凭据缓存），逐通道平铺会把同一站点渲染成
// 几十上百张重复卡片。
const balanceGroups = computed(() => {
  const map = new Map();
  for (const item of balances.value) {
    const baseURL = normalizeSupplierBaseURL(item?.baseURL);
    const key = baseURL || `adapter::${item?.adapterId || map.size}`;
    if (!map.has(key)) {
      map.set(key, {
        key,
        baseURL,
        host: supplierHost(item?.baseURL),
        groupName: String(item?.groupName || "").trim(),
        items: [],
      });
    }
    map.get(key).items.push(item);
  }
  return Array.from(map.values());
});

const supportedCount = computed(() => balanceGroups.value.filter((group) => groupRepresentative(group)?.balance?.supported).length);

// loadSeq 丢弃过期响应：手动同步与账号变更事件并发触发时，
// 后返回的旧结果不得覆盖新结果（否则错误提示会闪烁或乱序）。
let loadSeq = 0;

async function load() {
  // 无论走哪个分支都先作废在飞响应，避免关闭计费查询后旧结果回填。
  const seq = ++loadSeq;
  if (!billingQueryEnabled.value) {
    balances.value = [];
    loading.value = false;
    return;
  }
  loading.value = true;
  error.value = "";
  try {
    const list = (await queryAllProviderBalances()) || [];
    if (seq === loadSeq) balances.value = list;
  } catch (cause) {
    if (seq === loadSeq) error.value = String(cause?.message || cause || "读取厂商余额失败");
  } finally {
    if (seq === loadSeq) loading.value = false;
  }
}

// 开关计费查询后立即按新状态重载，避免停留在旧数据或空列表。
watch(billingQueryEnabled, () => {
  void load();
});

async function handleSync() {
  if (syncing.value || loading.value) return;
  syncing.value = true;
  let syncError = "";
  try {
    try {
      await syncProviderBalancesAfterAccountChange();
    } catch (cause) {
      syncError = String(cause?.message || cause || "同步厂商余额失败");
    }
    // 同步失败也读一次快照，尽量展示最近一次可用数据。
    await load();
    if (syncError) error.value = syncError;
  } finally {
    syncing.value = false;
  }
}

let stopAccountSync = () => {};
let stopSyncedEvent = () => {};
onMounted(() => {
  void load();
  stopAccountSync = onAccountSync(() => {
    // 后端已在账号变更钩子中完成全量同步，这里只读快照，避免重复上游查询。
    void load();
  });
  // 后端每轮余额同步结束（含提前返回）都会发该事件，收到即快照已定论，安全重载。
  stopSyncedEvent = runtimeEvents.On(PROVIDER_BALANCES_SYNCED_EVENT, () => {
    void load();
  });
});
onBeforeUnmount(() => {
  stopAccountSync();
  stopSyncedEvent();
});

// supplierHost 从 baseURL 提取 host，作为分组卡片的连接标识。
function supplierHost(baseURL) {
  const url = String(baseURL || "").trim();
  if (!url) return "";
  try {
    return new URL(url).hostname.toLowerCase().replace(/^www\./, "");
  } catch {
    return url;
  }
}

// groupRepresentative 取组内代表条目：优先可用余额，否则首条（展示失败原因）。
function groupRepresentative(group) {
  return group.items.find((item) => item?.balance?.supported) || group.items[0] || null;
}

// groupSubtitle 生成卡片副标题：连接 host + 通道数（多通道时）。
function groupSubtitle(group) {
  const host = group.host || "未设置 URL";
  return group.items.length > 1 ? `${host} · ${group.items.length} 个通道` : host;
}

function balanceLabel(item) {
  const balance = item?.balance || {};
  if (!balance.supported) return balance.message || "余额不可用";
  if (balance.unlimited) return "不限额度";
  // 按币种判断展示形态（token_plan 可能同时带百分比窗口与真实金额，
  // 金额存在时 currency 为真实币种，优先展示金额，窗口进度由徽章承载）。
  if (balance.currency === "%") {
    return `已用 ${Number(balance.used || 0).toFixed(0)}%`;
  }
  return formatMoney(balance.remaining, balance.currency);
}
</script>

<template>
  <Card>
    <ControlCenterSection
      title="厂商余额同步"
      description="账号切换或登录后会自动刷新各中转站余额，供首页与自适应路由评分使用。"
      icon="icon-[mdi--wallet-outline]"
    >
      <template #actions>
        <div class="center-row gap-2">
          <Button
            variant="text"
            @click="openModelConfig"
          >
            模型配置
          </Button>
          <Button
            :disabled="loading || syncing || !billingQueryEnabled"
            @click="handleSync"
          >
            <span
              v-if="syncing || loading"
              class="icon-[mdi--loading] animate-spin text-[14px]"
            />
            {{ syncing ? "同步中…" : "立即同步" }}
          </Button>
        </div>
      </template>

      <div
        v-if="!billingQueryEnabled"
        class="rounded-[8px] border border-[#3f3f3f] bg-[#252525]/70 px-3 py-3 text-sm text-[#a3a3a3]"
      >
        计费查询已在设置中关闭，不会向厂商拉取余额。
      </div>
      <div
        v-else-if="error"
        class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]"
      >
        {{ error }}
      </div>
      <div
        v-else-if="loading && !balances.length"
        class="rounded-[8px] border border-[#343434] bg-[#252525]/70 px-3 py-6 text-center text-sm text-[#8a8a8a]"
      >
        <span class="icon-[mdi--loading] animate-spin text-[18px]" /> 正在读取厂商余额…
      </div>
      <div
        v-else-if="!balances.length"
        class="rounded-[8px] border border-dashed border-[#3f3f3f] bg-[#252525]/40 px-3 py-6 text-center text-sm text-[#8a8a8a]"
      >
        暂无已配置余额查询的模型通道。请在模型配置中为供应商填写余额凭据。
      </div>
      <div
        v-else
        class="flex flex-col gap-2"
      >
        <div class="text-[11px] text-[#737373]">
          已同步 {{ supportedCount }} / {{ balanceGroups.length }} 个中转站（共 {{ balances.length }} 个通道）
        </div>
        <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          <div
            v-for="group in balanceGroups"
            :key="group.key"
            class="rounded-[8px] border border-[#343434] bg-[#252525]/70 px-3 py-2.5"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="truncate text-sm text-white">
                  {{ group.groupName || group.host || "未设置 URL" }}
                </div>
                <div
                  class="mt-0.5 truncate text-[11px] text-[#737373]"
                  :title="group.baseURL || group.host"
                >
                  {{ groupSubtitle(group) }}
                </div>
              </div>
              <span
                class="shrink-0 rounded-full px-2 py-0.5 text-[10px]"
                :class="groupRepresentative(group)?.balance?.supported ? 'border border-[#2f6b49] text-[#7dd3a0]' : 'border border-[#4b3b3b] text-[#a3a3a3]'"
              >
                {{ groupRepresentative(group)?.balance?.supported ? "可用" : "不可用" }}
              </span>
            </div>
            <div
              class="mt-2 text-sm font-medium tabular-nums"
              :class="groupRepresentative(group)?.balance?.supported ? 'text-[#6ee7a5]' : 'text-[#737373]'"
            >
              {{ balanceLabel(groupRepresentative(group)) }}
            </div>
          </div>
        </div>
      </div>
    </ControlCenterSection>
  </Card>
</template>
