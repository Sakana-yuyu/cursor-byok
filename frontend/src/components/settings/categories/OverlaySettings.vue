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

// 浮窗外观：透明度（0.3-1）、磨砂、主题色
const accentOptions = [
  { value: "mint", label: "薄荷", color: "#6ee7a5" },
  { value: "cyan", label: "青碧", color: "#5eead4" },
  { value: "amber", label: "琥珀", color: "#fcd34d" },
  { value: "violet", label: "紫罗兰", color: "#c4b5fd" },
  { value: "rose", label: "玫瑰", color: "#fda4af" },
  { value: "blue", label: "天蓝", color: "#93c5fd" },
  { value: "rainbow", label: "流动炫彩", color: "conic-gradient(from 0deg, #f87171, #fbbf24, #34d399, #60a5fa, #a78bfa, #f472b6, #f87171)" },
];

const stateByKey = {
  style: reactive({ busy: false, error: "", retry: null }),
  visible: reactive({ busy: false, error: "", retry: null }),
  alwaysOnTop: reactive({ busy: false, error: "", retry: null }),
  snapCollapse: reactive({ busy: false, error: "", retry: null }),
  dockLocked: reactive({ busy: false, error: "", retry: null }),
  opacity: reactive({ busy: false, error: "", retry: null }),
  frosted: reactive({ busy: false, error: "", retry: null }),
  frostBlur: reactive({ busy: false, error: "", retry: null }),
  accent: reactive({ busy: false, error: "", retry: null }),
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

function updateOpacity(event) {
  const value = Number(event?.target?.value);
  if (!Number.isFinite(value)) return;
  const nextValue = Math.min(1, Math.max(0.3, value));
  stateByKey.opacity.retry = () => updateOpacity({ target: { value: nextValue } });
  void saveOverlayPreference("opacity", () => setStatsOverlayPreferences({ opacity: nextValue }));
}

function updateFrostBlur(event) {
  const value = Number(event?.target?.value);
  if (!Number.isFinite(value)) return;
  const nextValue = Math.min(30, Math.max(0, value));
  stateByKey.frostBlur.retry = () => updateFrostBlur({ target: { value: nextValue } });
  // 磨砂程度与开关字段保持一致：0 视为关闭，其余视为开启。
  void saveOverlayPreference("frostBlur", () =>
    setStatsOverlayPreferences({ frostBlur: nextValue, frosted: nextValue > 0 }),
  );
}

function updateAccent(value) {
  const nextValue = accentOptions.some((option) => option.value === value) ? value : "mint";
  stateByKey.accent.retry = () => updateAccent(nextValue);
  void saveOverlayPreference("accent", () => setStatsOverlayPreferences({ accent: nextValue }));
}

function updateAccentCustom(event) {
  const value = String(event?.target?.value || "").trim();
  if (!/^#[0-9a-fA-F]{6}$/.test(value)) return;
  stateByKey.accent.retry = () => updateAccentCustom({ target: { value } });
  void saveOverlayPreference("accent", () => setStatsOverlayPreferences({ accent: "custom", accentCustom: value }));
}

onMounted(() => {
  getStatsOverlayPreferences();
});
</script>

<style scoped>
/* 区块标题与下方配置行保持明显间距 */
:deep(header) {
  margin-bottom: 18px;
  padding-bottom: 10px;
  border-bottom: 1px solid #343434;
}
:deep(header h2) {
  font-size: 16px;
  font-weight: 600;
  letter-spacing: 0.01em;
}

/* 两列布局下 SettingsRow 的标签列收窄，控件列保持可用宽度 */
@media (min-width: 1280px) {
  :deep(.settings-row) {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
    gap: 12px;
  }
}

/* 深色主题 range 滑块 */
.overlay-range {
  appearance: none;
  -webkit-appearance: none;
  height: 4px;
  border-radius: 9999px;
  background: linear-gradient(to right, #10AD5D var(--range-fill, 85%), #3a3a3a var(--range-fill, 85%));
  outline: none;
  cursor: pointer;
}
.overlay-range::-webkit-slider-thumb {
  appearance: none;
  -webkit-appearance: none;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #10AD5D;
  border: 2px solid #d9fbe9;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.5);
  cursor: pointer;
}
.overlay-range::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #10AD5D;
  border: 2px solid #d9fbe9;
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.5);
  cursor: pointer;
}
</style>

<template>
  <div class="grid grid-cols-1 gap-8 xl:grid-cols-2 xl:items-start">
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

    <SettingsSection title="浮窗外观">
      <SettingsRow
        label="背景透明度"
        description="调节浮窗背景的不透明度（30% - 100%），数值越小越透明"
        :busy="stateByKey.opacity.busy"
        :error="stateByKey.opacity.error"
        @retry="retryState(stateByKey.opacity)"
      >
        <div class="flex w-full max-w-[240px] items-center gap-3">
          <input
            type="range"
            min="0.3"
            max="1"
            step="0.05"
            class="overlay-range flex-1"
            :style="{ '--range-fill': `${Math.round((Number(overlayPreferences.opacity) || 0.85) * 100)}%` }"
            :value="Number(overlayPreferences.opacity) || 0.85"
            aria-label="背景透明度"
            @input="updateOpacity"
          />
          <span class="w-[44px] shrink-0 text-right text-xs tabular-nums text-[#a3a3a3]">
            {{ Math.round((Number(overlayPreferences.opacity) || 0.85) * 100) }}%
          </span>
        </div>
      </SettingsRow>

      <SettingsRow
        label="磨砂程度"
        description="调节背景模糊强度（0 = 关闭磨砂，最高 30px）"
        :busy="stateByKey.frostBlur.busy"
        :error="stateByKey.frostBlur.error"
        @retry="retryState(stateByKey.frostBlur)"
      >
        <div class="flex w-full max-w-[240px] items-center gap-3">
          <input
            type="range"
            min="0"
            max="30"
            step="1"
            class="overlay-range flex-1"
            :style="{ '--range-fill': `${Math.round((Number(overlayPreferences.frostBlur) || 0) / 30 * 100)}%` }"
            :value="Number(overlayPreferences.frostBlur) || 0"
            aria-label="磨砂程度"
            @input="updateFrostBlur"
          />
          <span class="w-[44px] shrink-0 text-right text-xs tabular-nums text-[#a3a3a3]">
            {{ Number(overlayPreferences.frostBlur) > 0 ? `${Math.round(Number(overlayPreferences.frostBlur))}px` : "关闭" }}
          </span>
        </div>
      </SettingsRow>

      <SettingsRow
        label="数据配色"
        description="切换浮窗内数值、进度环与装饰的主题色"
        :busy="stateByKey.accent.busy"
        :error="stateByKey.accent.error"
        @retry="retryState(stateByKey.accent)"
      >
        <div class="flex flex-wrap items-center gap-2">
          <button
            v-for="option in accentOptions"
            :key="option.value"
            type="button"
            class="h-7 w-7 rounded-full border-2 transition-all"
            :class="overlayPreferences.accent === option.value
              ? 'border-white ring-2 ring-white/25'
              : 'border-transparent hover:scale-110'"
            :style="{ backgroundColor: option.color, backgroundImage: option.value === 'rainbow' ? option.color : undefined }"
            :title="option.label"
            :aria-label="`主题色：${option.label}`"
            @click="updateAccent(option.value)"
          />
          <label
            class="relative h-7 w-7 cursor-pointer rounded-full border-2 transition-all"
            :class="overlayPreferences.accent === 'custom'
              ? 'border-white ring-2 ring-white/25'
              : 'border-transparent hover:scale-110'"
            :title="'自定义颜色'"
            :style="{ backgroundColor: overlayPreferences.accentCustom || '#10AD5D' }"
          >
            <input
              type="color"
              class="absolute inset-0 h-full w-full cursor-pointer opacity-0"
              :value="overlayPreferences.accentCustom || '#10AD5D'"
              aria-label="自定义颜色"
              @input="updateAccentCustom"
            />
          </label>
        </div>
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
