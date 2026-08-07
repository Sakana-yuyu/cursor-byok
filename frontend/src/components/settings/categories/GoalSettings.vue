<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Input from "@/components/ui/Input.vue";
import Switch from "@/components/ui/Switch.vue";
import { appState, saveGoalSettings, toUserError } from "@/state/appState";
import { reactive, ref, watch } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const goalDraft = reactive({
  enabled: false,
  maxProviderPasses: 30,
  maxDurationSeconds: 0,
  maxCostUsd: 0,
  selfCheckPasses: 2,
});

// 从 appState 同步初始值；配置加载完成后再次同步。
function syncFromState() {
  const source = appState.goal && typeof appState.goal === "object" ? appState.goal : {};
  goalDraft.enabled = Boolean(source.enabled);
  goalDraft.maxProviderPasses = Number(source.maxProviderPasses ?? 30) || 30;
  goalDraft.maxDurationSeconds = Number(source.maxDurationSeconds ?? 0) || 0;
  goalDraft.maxCostUsd = Number(source.maxCostUsd ?? 0) || 0;
  goalDraft.selfCheckPasses = Number(source.selfCheckPasses ?? 2) || 2;
}
syncFromState();
watch(() => appState.goal, syncFromState);

const goalState = reactive({ busy: false, error: "", retry: null });

async function saveGoal(next) {
  const previous = { ...goalDraft };
  Object.assign(goalDraft, next);
  goalState.retry = () => saveGoal(next);
  goalState.error = "";
  goalState.busy = true;
  try {
    await props.autosave.run("goal.config", async () => {
      const result = await saveGoalSettings({ ...goalDraft });
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
    });
  } catch (error) {
    Object.assign(goalDraft, previous);
    goalState.error = toUserError(error);
  } finally {
    goalState.busy = false;
  }
}

function setEnabled(value) {
  return saveGoal({ enabled: Boolean(value) });
}

function setMaxPasses(value) {
  return saveGoal({ maxProviderPasses: Number(value) || 0 });
}

function setMaxDuration(value) {
  return saveGoal({ maxDurationSeconds: Number(value) || 0 });
}

function setMaxCost(value) {
  return saveGoal({ maxCostUsd: Number(value) || 0 });
}

function setSelfCheckPasses(value) {
  return saveGoal({ selfCheckPasses: Number(value) || 0 });
}
</script>

<template>
  <SettingsSection title="Goal 循环执行" description="配置 goal 模式的预算与完成判定">
    <SettingsRow :label="goalDraft.enabled ? '已启用前端面板发起' : '启用前端面板发起'">
      <Switch :model-value="goalDraft.enabled" :disabled="goalState.busy" @update:model-value="setEnabled" />
    </SettingsRow>
    <SettingsRow label="最大 provider pass 数">
      <Input type="number" :model-value="goalDraft.maxProviderPasses" @update:model-value="setMaxPasses" />
    </SettingsRow>
    <SettingsRow label="最大时长（秒，0=不限）">
      <Input type="number" :model-value="goalDraft.maxDurationSeconds" @update:model-value="setMaxDuration" />
    </SettingsRow>
    <SettingsRow label="费用上限（USD，0=不限）">
      <Input type="number" :model-value="goalDraft.maxCostUsd" @update:model-value="setMaxCost" />
    </SettingsRow>
    <SettingsRow label="完成自检轮数">
      <Input type="number" :model-value="goalDraft.selfCheckPasses" @update:model-value="setSelfCheckPasses" />
    </SettingsRow>
    <p v-if="goalState.error" class="mt-2 text-sm text-red-400">{{ goalState.error }}</p>
  </SettingsSection>
</template>