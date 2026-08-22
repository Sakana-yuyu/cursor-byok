<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import Switch from "@/components/ui/Switch.vue";
import { getRoutingDecisionHistory, getRoutingPolicy, previewRoutingDecision, saveRoutingPolicy } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { computed, onMounted, ref } from "vue";

const message = useMessage();
const loading = ref(true);
const saving = ref(false);
const previewing = ref(false);
const policy = ref({
  enabled: false,
  strategy: "manual",
  sessionAffinity: false,
  maxFailoverAttempts: 0,
  latencyWeight: 25,
  costWeight: 25,
  reliabilityWeight: 25,
  balanceWeight: 25,
});
const modelId = ref("");
const preview = ref(null);
const history = ref([]);

const weightTotal = computed(() =>
  Number(policy.value.latencyWeight || 0)
  + Number(policy.value.costWeight || 0)
  + Number(policy.value.reliabilityWeight || 0)
  + Number(policy.value.balanceWeight || 0),
);

const STRATEGY_LABELS = {
  manual: "手动顺序",
  balanced: "均衡",
  latency: "延迟优先",
  cost: "成本优先",
  stability: "稳定性优先",
};

async function load() {
  loading.value = true;
  try {
    policy.value = { ...policy.value, ...(await getRoutingPolicy()) };
    const page = await getRoutingDecisionHistory({ limit: 50 });
    history.value = page?.items || [];
  } catch (error) {
    message.error(error?.message || "加载路由策略失败");
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    policy.value = await saveRoutingPolicy(policy.value);
    message.success("路由策略已保存");
  } catch (error) {
    message.error(error?.message || "保存失败");
  } finally {
    saving.value = false;
  }
}

async function previewDecision() {
  if (!modelId.value.trim()) {
    message.error("请输入模型 ID");
    return;
  }
  previewing.value = true;
  try {
    preview.value = await previewRoutingDecision({ modelId: modelId.value.trim() });
  } catch (error) {
    message.error(error?.message || "预览失败");
  } finally {
    previewing.value = false;
  }
}

function formatTime(unixMs) {
  const value = Number(unixMs || 0);
  if (!value) return "—";
  return new Date(value).toLocaleString();
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <div v-if="loading" class="py-10 text-center text-sm text-[#8a8a8a]">
      <span class="icon-[mdi--loading] animate-spin text-[20px]" /> 加载路由策略…
    </div>

    <template v-else>
      <Card>
        <ControlCenterSection
          title="策略开关"
          description="开启后，实时请求会按评分重排通道并在失败时尝试故障切换。"
          icon="icon-[mdi--routes]"
        >
          <Switch label="启用自适应路由" :enabled="policy.enabled" compact @change="policy.enabled = $event" />
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
              策略模式
              <select v-model="policy.strategy" class="rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-2 text-sm text-white outline-none focus:border-[#10AD5D]">
                <option v-for="(label, key) in STRATEGY_LABELS" :key="key" :value="key">{{ label }}</option>
              </select>
            </label>
            <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
              最大故障切换次数
              <input v-model.number="policy.maxFailoverAttempts" type="number" min="0" max="5" class="rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-2 text-sm text-white outline-none focus:border-[#10AD5D]" />
            </label>
          </div>
        </ControlCenterSection>
      </Card>

      <Card>
        <ControlCenterSection title="评分权重" description="权重按原始值参与评分（不归一化），建议四项合计为 100；全部为 0 时保存会重置为默认 25/25/25/25。" icon="icon-[mdi--scale-balance]">
          <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
            <label v-for="item in [
              { key: 'latencyWeight', label: '延迟' },
              { key: 'costWeight', label: '成本' },
              { key: 'reliabilityWeight', label: '可靠' },
              { key: 'balanceWeight', label: '余额' },
            ]" :key="item.key" class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
              {{ item.label }}
              <input v-model.number="policy[item.key]" type="number" min="0" max="100" class="rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-2 text-sm text-white outline-none focus:border-[#10AD5D]" />
            </label>
          </div>
          <div class="mt-2 text-[11px]" :class="weightTotal === 100 ? 'text-[#6ee7a5]' : 'text-[#fcd34d]'">
            当前合计 {{ weightTotal }} / 100
          </div>
          <div class="mt-3">
            <Button variant="primary" :disabled="saving" @click="save">
              {{ saving ? "保存中…" : "保存策略" }}
            </Button>
          </div>
        </ControlCenterSection>
      </Card>

      <Card>
        <ControlCenterSection title="只读预览" description="输入模型 ID，查看当前策略下的候选排序（不发起真实请求）。" icon="icon-[mdi--eye-outline]">
          <div class="flex flex-col gap-2 sm:flex-row sm:items-end">
            <label class="min-w-0 flex-1 text-xs text-[#a3a3a3]">
              模型 ID
              <input v-model="modelId" placeholder="例如 gpt-4.1" class="mt-1 w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-2 text-sm text-white outline-none focus:border-[#10AD5D]" />
            </label>
            <Button :disabled="previewing" @click="previewDecision">{{ previewing ? "预览中…" : "预览排序" }}</Button>
          </div>
          <div v-if="preview?.candidates?.length" class="mt-3 overflow-hidden rounded-[8px] border border-[#343434]">
            <div
              v-for="(item, index) in preview.candidates"
              :key="item.channelId"
              class="flex items-center justify-between gap-3 border-b border-[#343434] px-3 py-2 text-xs last:border-b-0"
            >
              <div class="min-w-0">
                <span class="mr-2 inline-flex h-5 w-5 items-center justify-center rounded-full bg-[#1f3a2c] text-[11px] text-[#7dd3a0]">{{ index + 1 }}</span>
                <span class="text-white">{{ item.channelId }}</span>
              </div>
              <div class="shrink-0 text-[#a3a3a3]">
                分数 {{ item.score }}
                <span v-if="item.reasonCodes?.length"> · {{ item.reasonCodes.join(", ") }}</span>
              </div>
            </div>
          </div>
        </ControlCenterSection>
      </Card>

      <Card>
        <ControlCenterSection title="决策历史" :description="`最近 ${history.length} 条路由决策，不含请求正文。`" icon="icon-[mdi--history]">
          <div v-if="!history.length" class="rounded-[8px] border border-dashed border-[#3f3f3f] px-3 py-6 text-center text-sm text-[#8a8a8a]">
            暂无决策记录。启用路由并产生请求后会出现在这里。
          </div>
          <div v-else class="max-h-[280px] overflow-y-auto rounded-[8px] border border-[#343434]">
            <div
              v-for="item in history"
              :key="item.decisionId || `${item.modelId}-${item.timestampUnixMs}`"
              class="border-b border-[#343434] px-3 py-2 text-xs last:border-b-0"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <span class="text-white">{{ item.modelId || "未知模型" }}</span>
                <span class="text-[#737373]">{{ formatTime(item.timestampUnixMs) }}</span>
              </div>
              <div class="mt-1 text-[#a3a3a3]">
                选中 {{ item.selectedChannelId || "—" }}
                <span v-if="item.attemptCount != null"> · 尝试 {{ item.attemptCount }}</span>
              </div>
            </div>
          </div>
        </ControlCenterSection>
      </Card>
    </template>
  </div>
</template>
