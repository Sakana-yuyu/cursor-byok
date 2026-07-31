<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import {
  appState,
  getStatsOverlayPreferences,
  hideStatsOverlay,
  setStatsOverlayPreferences,
  showStatsOverlay,
  toUserError,
} from "@/state/appState";
import { computed, onMounted, reactive } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const overlayStyleOptions = [
  { value: "card", label: "卡片式" },
  { value: "engine", label: "引擎仪表" },
  { value: "orb", label: "球形" },
];

const stateByKey = {
  style: reactive({ busy: false, error: "", retry: null }),
  visible: reactive({ busy: false, error: "", retry: null }),
  alwaysOnTop: reactive({ busy: false, error: "", retry: null }),
  snapCollapse: reactive({ busy: false, error: "", retry: null }),
  dockLocked: reactive({ busy: false, error: "", retry: null }),
};

const overlayPreferences = computed(() => appState.statsOverlayPreferences);

function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

async function saveOverlayPreference(key, action) {
  const state = stateByKey[key];
  state.error = "";
  state.busy = true;
  try {
    await props.autosave.run(`overlay.${key}`, action);
  } catch (error) {
    state.error = toUserError(error);
  } finally {
    state.busy = false;
  }
}

function updateStyle(value) {
  const nextValue = value === "engine" || value === "orb" ? value : "card";
  stateByKey.style.retry = () => updateStyle(nextValue);
  void saveOverlayPreference("style", () => setStatsOverlayPreferences({ style: nextValue }));
}

function updateVisibility(visible) {
  stateByKey.visible.retry = () => updateVisibility(visible);
  void saveOverlayPreference("visible", () => (visible ? showStatsOverlay() : hideStatsOverlay()));
}

function updateBooleanPreference(key, value) {
  const nextValue = Boolean(value);
  stateByKey[key].retry = () => updateBooleanPreference(key, nextValue);
  void saveOverlayPreference(key, () => setStatsOverlayPreferences({ [key]: nextValue }));
}

onMounted(() => {
  getStatsOverlayPreferences();
});
</script>

<template>
  <div class="space-y-8">
    <SettingsSection title="浮窗偏好">
      <SettingsRow
        label="浮窗样式"
        :busy="stateByKey.style.busy"
        :error="stateByKey.style.error"
        @retry="retryState(stateByKey.style)"
      >
        <div class="w-[220px] max-w-full">
          <Select
            :model-value="overlayPreferences.style"
            :options="overlayStyleOptions"
            aria-label="浮窗样式"
            :disabled="stateByKey.style.busy"
            @change="updateStyle"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="显示浮窗"
        description="在桌面显示请求统计浮窗"
        :busy="stateByKey.visible.busy"
        :error="stateByKey.visible.error"
        @retry="retryState(stateByKey.visible)"
      >
        <Switch
          compact
          label=""
          :enabled="overlayPreferences.visible"
          :busy="stateByKey.visible.busy"
          :disabled="stateByKey.visible.busy"
          aria-label="显示浮窗"
          @change="updateVisibility"
        />
      </SettingsRow>

      <SettingsRow
        label="窗口置顶"
        description="让浮窗保持在其他窗口上方"
        :busy="stateByKey.alwaysOnTop.busy"
        :error="stateByKey.alwaysOnTop.error"
        @retry="retryState(stateByKey.alwaysOnTop)"
      >
        <Switch
          compact
          label=""
          :enabled="overlayPreferences.alwaysOnTop"
          :busy="stateByKey.alwaysOnTop.busy"
          :disabled="stateByKey.alwaysOnTop.busy"
          aria-label="窗口置顶"
          @change="(value) => updateBooleanPreference('alwaysOnTop', value)"
        />
      </SettingsRow>

      <SettingsRow
        label="贴边自动收缩"
        description="靠近屏幕边缘时收缩为胶囊"
        :busy="stateByKey.snapCollapse.busy"
        :error="stateByKey.snapCollapse.error"
        @retry="retryState(stateByKey.snapCollapse)"
      >
        <Switch
          compact
          label=""
          :enabled="overlayPreferences.snapCollapse"
          :busy="stateByKey.snapCollapse.busy"
          :disabled="stateByKey.snapCollapse.busy"
          aria-label="贴边自动收缩"
          @change="(value) => updateBooleanPreference('snapCollapse', value)"
        />
      </SettingsRow>

      <SettingsRow
        label="锁定浮窗"
        description="锁定为收缩胶囊且不可拖动"
        :busy="stateByKey.dockLocked.busy"
        :error="stateByKey.dockLocked.error"
        @retry="retryState(stateByKey.dockLocked)"
      >
        <Switch
          compact
          label=""
          :enabled="overlayPreferences.dockLocked"
          :busy="stateByKey.dockLocked.busy"
          :disabled="stateByKey.dockLocked.busy"
          aria-label="锁定浮窗"
          @change="(value) => updateBooleanPreference('dockLocked', value)"
        />
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
