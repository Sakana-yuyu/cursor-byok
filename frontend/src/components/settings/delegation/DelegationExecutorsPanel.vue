<script setup>
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Switch from "@/components/ui/Switch.vue";
import DelegationIconButton from "@/components/settings/delegation/DelegationIconButton.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import { computed, nextTick, reactive, ref } from "vue";

const props = defineProps({
  executors: { type: Array, default: () => [] },
  snapshots: { type: Array, default: () => [] },
  busy: { type: Boolean, default: false },
  error: { type: String, default: "" },
});

const emit = defineEmits(["refresh", "toggle", "priority", "save-custom"]);
const priorityDrafts = reactive({});
const customVisible = ref(false);
const customError = ref("");
const customDraft = reactive({ id: "", displayName: "", executable: "", probeTimeoutSeconds: "5", executionTimeoutSeconds: "120" });
const executableInput = ref(null);

const rows = computed(() => {
  const configByID = new Map(props.executors.map((item) => [String(item?.id || ""), item]));
  const snapshotByID = new Map(props.snapshots.map((item) => [String(item?.id || ""), item]));
  const ids = [...new Set([...configByID.keys(), ...snapshotByID.keys()])].filter(Boolean);
  return ids.map((id) => ({ ...configByID.get(id), ...snapshotByID.get(id) }));
});

function stateLabel(row) {
  if (row?.id === "cursor-agent" && row?.editorAvailable && !row?.agentExecutionAvailable) return "仅编辑器";
  if (row?.diagnosticCode === "custom_not_configured") return "未配置";
  if (row?.state === "ready") return "可用";
  if (row?.state === "not_installed") return "未安装";
  if (row?.state === "action_required") return "需要操作";
  if (row?.state === "unhealthy") return "异常";
  return "未检查";
}

function stateClass(row) {
  if (row?.state === "ready") return "text-[#6ee7a5]";
  if (row?.state === "unhealthy") return "text-[#fca5a5]";
  if (row?.state === "action_required") return "text-[#facc15]";
  return "text-[#a3a3a3]";
}

function priorityValue(row) {
  const id = String(row?.id || "");
  if (Object.prototype.hasOwnProperty.call(priorityDrafts, id)) return priorityDrafts[id];
  return String(row?.priority ?? 0);
}

function updatePriority(row, value) {
  priorityDrafts[String(row.id)] = value;
}

function flushPriority(row) {
  const id = String(row?.id || "");
  const parsed = Number.parseInt(priorityDrafts[id] ?? row?.priority, 10);
  const priority = Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
  priorityDrafts[id] = String(priority);
  emit("priority", { id, priority });
}

function openCustom(row) {
  Object.assign(customDraft, {
    id: String(row?.id || ""),
    displayName: String(row?.displayName || row?.id || ""),
    executable: String(row?.executable || ""),
    probeTimeoutSeconds: String(row?.probeTimeoutSeconds || 5),
    executionTimeoutSeconds: String(row?.executionTimeoutSeconds || 120),
  });
  customError.value = "";
  customVisible.value = true;
  void nextTick(() => executableInput.value?.$el?.focus?.());
}

function closeCustom() {
  customVisible.value = false;
  customError.value = "";
}

function saveCustom() {
  if (!String(customDraft.executable || "").trim()) {
    customError.value = "请输入可执行文件";
    return;
  }
  emit("save-custom", {
    id: customDraft.id,
    displayName: String(customDraft.displayName || "").trim(),
    executable: String(customDraft.executable || "").trim(),
    probeTimeoutSeconds: Number.parseInt(customDraft.probeTimeoutSeconds, 10) || 5,
    executionTimeoutSeconds: Number.parseInt(customDraft.executionTimeoutSeconds, 10) || 120,
  });
  closeCustom();
}
</script>

<template>
  <SettingsSection
    title="Agent 执行器"
    description="自动模式按优先级选择可用执行器；不可安全重试的失败会立即停止。"
  >
    <div class="overflow-hidden rounded-[6px] border border-white/10 bg-black/10">
      <div class="flex items-center justify-between gap-3 border-b border-white/10 px-3 py-2">
        <div class="text-xs text-[#8f8f8f]">{{ rows.length }} 个执行器</div>
        <DelegationIconButton icon="icon-[mdi--refresh]" label="刷新执行器状态" :disabled="busy" :spinning="busy" @click="emit('refresh')" />
      </div>
      <div v-if="error" class="border-b border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#fca5a5]">{{ error }}</div>
      <div v-if="!rows.length" class="px-3 py-5 text-center text-xs text-[#858585]">暂无执行器</div>
      <div v-else class="divide-y divide-white/10">
        <div v-for="row in rows" :key="row.id" class="flex min-w-0 items-center gap-3 px-3 py-2.5">
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 items-center gap-2">
              <span class="truncate text-sm font-medium text-white">{{ row.displayName || row.id }}</span>
              <span class="shrink-0 text-[11px]" :class="stateClass(row)">{{ stateLabel(row) }}</span>
            </div>
            <div class="mt-0.5 flex min-w-0 flex-wrap gap-x-2 text-[11px] text-[#777]">
              <span v-if="row.version">{{ row.version }}</span>
              <span v-if="row.diagnosticText" class="max-w-full truncate" :title="row.diagnosticText">{{ row.diagnosticText }}</span>
            </div>
          </div>
          <div class="ml-auto flex shrink-0 items-end gap-2">
            <label class="w-14 text-[11px] text-[#8f8f8f] sm:w-20">优先级
              <input :value="priorityValue(row)" type="number" min="0" :aria-label="`${row.displayName || row.id} 优先级`" class="mt-0.5 h-7 w-full rounded-[5px] border border-white/10 bg-black/20 px-2 text-xs text-white outline-none focus:border-[#10AD5D]" @input="updatePriority(row, $event.target.value)" @blur="flushPriority(row)" />
            </label>
            <Switch compact :enabled="Boolean(row.enabled)" :disabled="busy" :aria-label="`启用 ${row.displayName || row.id}`" @change="emit('toggle', { id: row.id, enabled: $event })" />
            <DelegationIconButton v-if="row.kind === 'custom'" icon="icon-[mdi--cog-outline]" :label="`配置 ${row.displayName || row.id}`" @click="openCustom(row)" />
            <span v-else class="h-8 w-8"></span>
          </div>
        </div>
      </div>
    </div>
  </SettingsSection>

  <Teleport to="body">
    <div v-if="customVisible" class="fixed inset-0 z-[100000] flex items-center justify-center bg-black/60 p-4" @click.self="closeCustom">
      <section role="dialog" aria-modal="true" :aria-label="`配置 ${customDraft.displayName}`" class="w-full max-w-[480px] rounded-[8px] border border-white/15 bg-[#292929] p-5 shadow-2xl">
        <div class="mb-4 flex items-center justify-between gap-3"><h3 class="text-base font-medium text-white">配置 {{ customDraft.displayName }}</h3><DelegationIconButton icon="icon-[mdi--close]" label="关闭配置" @click="closeCustom" /></div>
        <div class="space-y-3">
          <label class="block text-xs text-[#a3a3a3]">可执行文件<Input ref="executableInput" v-model="customDraft.executable" class="mt-1" aria-label="可执行文件" placeholder="grok" /></label>
          <div class="grid grid-cols-2 gap-3">
            <label class="block text-xs text-[#a3a3a3]">探测超时（秒）<Input v-model="customDraft.probeTimeoutSeconds" type="number" class="mt-1" /></label>
            <label class="block text-xs text-[#a3a3a3]">执行超时（秒）<Input v-model="customDraft.executionTimeoutSeconds" type="number" class="mt-1" /></label>
          </div>
          <div v-if="customError" class="text-xs text-[#fca5a5]">{{ customError }}</div>
        </div>
        <div class="mt-5 flex justify-end gap-2"><Button variant="default" @click="closeCustom">取消</Button><Button variant="primary" @click="saveCustom">保存配置</Button></div>
      </section>
    </div>
  </Teleport>
</template>
