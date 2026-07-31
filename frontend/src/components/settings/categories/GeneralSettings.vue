<script setup>
import LocaleSelect from "@/components/LocaleSelect.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Select from "@/components/ui/Select.vue";
import { appState, getStatsOverlayPreferences, openConfigWindow, setStatsOverlayPreferences, toUserError } from "@/state/appState";
import { computed, onMounted, reactive } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const closeActionOptions = [
  { value: "tray", label: "隐藏到托盘" },
  { value: "quit", label: "直接退出应用" },
];

const closeActionState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const configActionState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const currentCloseAction = computed(() => appState.statsOverlayPreferences.closeAction);

function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

async function saveCloseAction(value) {
  const nextValue = value === "quit" ? "quit" : "tray";
  closeActionState.retry = () => saveCloseAction(nextValue);
  closeActionState.error = "";
  closeActionState.busy = true;
  try {
    await props.autosave.run("general.close-action", () => setStatsOverlayPreferences({ closeAction: nextValue }));
  } catch (error) {
    closeActionState.error = toUserError(error);
  } finally {
    closeActionState.busy = false;
  }
}

async function handleOpenConfigWindow() {
  configActionState.retry = handleOpenConfigWindow;
  configActionState.error = "";
  configActionState.busy = true;
  try {
    await openConfigWindow();
  } catch (error) {
    configActionState.error = toUserError(error);
  } finally {
    configActionState.busy = false;
  }
}

onMounted(() => {
  getStatsOverlayPreferences();
});
</script>

<template>
  <div class="space-y-8">
    <SettingsSection>
      <SettingsRow
        label="界面语言"
        description="切换当前界面显示语言，设置会立即生效并保存在本机"
      >
        <LocaleSelect
          wrapper-class="w-[220px] max-w-full"
          aria-label="界面语言"
        />
      </SettingsRow>

      <SettingsRow
        label="主窗口关闭行为"
        description="关闭主窗口时隐藏到托盘，或直接退出应用"
        :busy="closeActionState.busy"
        :error="closeActionState.error"
        @retry="retryState(closeActionState)"
      >
        <div class="w-[220px] max-w-full">
          <Select
            :model-value="currentCloseAction"
            :options="closeActionOptions"
            aria-label="主窗口关闭行为"
            :disabled="closeActionState.busy"
            @change="saveCloseAction"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="设置文件夹"
        description="打开本地配置文件所在目录"
        :busy="configActionState.busy"
        :error="configActionState.error"
        @retry="retryState(configActionState)"
      >
        <Button
          variant="default"
          :disabled="configActionState.busy"
          @click="handleOpenConfigWindow"
        >
          打开
        </Button>
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
