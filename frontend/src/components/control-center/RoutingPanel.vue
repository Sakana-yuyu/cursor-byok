<script setup>
import Button from "@/components/ui/Button.vue";
import Switch from "@/components/ui/Switch.vue";
import { getRoutingDecisionHistory, getRoutingPolicy, previewRoutingDecision, saveRoutingPolicy } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
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

async function load() {
  try {
    policy.value = { ...policy.value, ...(await getRoutingPolicy()) };
    const page = await getRoutingDecisionHistory({ limit: 50 });
    history.value = page?.items || [];
  } catch (error) {
    message.error(error?.message || "加载路由策略失败");
  }
}

async function save() {
  try {
    policy.value = await saveRoutingPolicy(policy.value);
    message.success("路由策略已保存");
  } catch (error) {
    message.error(error?.message || "保存失败");
  }
}

async function previewDecision() {
  try {
    preview.value = await previewRoutingDecision({ modelId: modelId.value });
  } catch (error) {
    message.error(error?.message || "预览失败");
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <Switch label="启用自适应路由" :enabled="policy.enabled" compact @change="policy.enabled = $event" />
    <label class="text-sm text-[#a3a3a3]">
      策略
      <select v-model="policy.strategy" class="ml-2 rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white">
        <option value="manual">手动顺序</option>
        <option value="balanced">均衡</option>
        <option value="latency">延迟</option>
        <option value="cost">成本</option>
        <option value="stability">稳定性</option>
      </select>
    </label>
    <div class="grid grid-cols-2 gap-2 text-xs text-[#a3a3a3] md:grid-cols-4">
      <label>延迟权重 <input v-model.number="policy.latencyWeight" type="number" min="0" max="100" class="w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
      <label>成本权重 <input v-model.number="policy.costWeight" type="number" min="0" max="100" class="w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
      <label>可靠权重 <input v-model.number="policy.reliabilityWeight" type="number" min="0" max="100" class="w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
      <label>余额权重 <input v-model.number="policy.balanceWeight" type="number" min="0" max="100" class="w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
    </div>
    <label class="text-xs text-[#a3a3a3]">故障切换次数 <input v-model.number="policy.maxFailoverAttempts" type="number" min="0" max="5" class="ml-2 w-20 rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
    <Button variant="primary" @click="save">保存策略</Button>
    <div class="flex items-end gap-2">
      <label class="flex-1 text-xs text-[#a3a3a3]">预览模型 ID <input v-model="modelId" class="mt-1 w-full rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-1 text-white" /></label>
      <Button @click="previewDecision">只读预览</Button>
    </div>
    <div v-if="preview" class="rounded-[8px] border border-[#343434] bg-[#292929] p-3 text-xs">
      <div v-for="item in preview.candidates" :key="item.channelId">{{ item.channelId }} · 分数 {{ item.score }} · {{ (item.reasonCodes || []).join(",") }}</div>
    </div>
    <div class="text-xs text-[#8a8a8a]">决策历史 {{ history.length }} 条，不含请求正文。</div>
  </div>
</template>
