<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import { queryAllProviderBalances, syncProviderBalancesAfterAccountChange } from "@/services/clientApi";
import { appState } from "@/state/appState";
import { formatMoney } from "@/utils/format";
import { onAccountSync } from "@/utils/accountSync";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const balances = ref([]);
const loading = ref(false);
const syncing = ref(false);
const error = ref("");

const billingQueryEnabled = computed(() => appState.billingQuery?.enabled !== false);
const supportedCount = computed(() => balances.value.filter((item) => item?.balance?.supported).length);

// loadSeq 丢弃过期响应：手动同步与账号变更事件并发触发时，
// 后返回的旧结果不得覆盖新结果（否则错误提示会闪烁或乱序）。
let loadSeq = 0;

async function load() {
  if (!billingQueryEnabled.value) {
    balances.value = [];
    return;
  }
  const seq = ++loadSeq;
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

async function handleSync() {
  if (syncing.value || loading.value) return;
  syncing.value = true;
  try {
    await syncProviderBalancesAfterAccountChange();
    await load();
  } finally {
    syncing.value = false;
  }
}

function openModelConfig() {
  void router.push("/model-config");
}

function balanceLabel(item) {
  const balance = item?.balance || {};
  if (!balance.supported) return balance.message || "余额不可用";
  if (balance.unlimited) return "不限额度";
  if (balance.source === "token_plan" || balance.currency === "%") {
    return `已用 ${Number(balance.used || 0).toFixed(0)}%`;
  }
  return formatMoney(balance.remaining, balance.currency);
}

let stopAccountSync = () => {};
onMounted(() => {
  void load();
  stopAccountSync = onAccountSync(() => {
    // 后端已在账号变更钩子中完成全量同步，这里只读快照，避免重复上游查询。
    void load();
  });
});
onBeforeUnmount(() => {
  stopAccountSync();
});
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
          <Button variant="text" @click="openModelConfig">模型配置</Button>
          <Button :disabled="loading || syncing || !billingQueryEnabled" @click="handleSync">
            <span v-if="syncing || loading" class="icon-[mdi--loading] animate-spin text-[14px]" />
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
      <div v-else class="flex flex-col gap-2">
        <div class="text-[11px] text-[#737373]">
          已同步 {{ supportedCount }} / {{ balances.length }} 个通道
        </div>
        <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          <div
            v-for="item in balances"
            :key="item.adapterId"
            class="rounded-[8px] border border-[#343434] bg-[#252525]/70 px-3 py-2.5"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="truncate text-sm text-white">{{ item.groupName || item.displayName || item.modelID }}</div>
                <div class="mt-0.5 truncate text-[11px] text-[#737373]">{{ item.displayName || item.modelID }}</div>
              </div>
              <span
                class="shrink-0 rounded-full px-2 py-0.5 text-[10px]"
                :class="item.balance?.supported ? 'border border-[#2f6b49] text-[#7dd3a0]' : 'border border-[#4b3b3b] text-[#a3a3a3]'"
              >
                {{ item.balance?.supported ? "可用" : "不可用" }}
              </span>
            </div>
            <div
              class="mt-2 text-sm font-medium tabular-nums"
              :class="item.balance?.supported ? 'text-[#6ee7a5]' : 'text-[#737373]'"
            >
              {{ balanceLabel(item) }}
            </div>
          </div>
        </div>
      </div>
    </ControlCenterSection>
  </Card>
</template>
