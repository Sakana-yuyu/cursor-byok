<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import {
  ROUTE_MODE_OPTIONS,
  appState,
  getCursorManualPath,
  openModelConfigWindow,
  saveRoutingMode,
  setCursorManualPath,
  toUserError,
} from "@/state/appState";
import { detectCursorPath } from "@/services/clientApi";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const MANUAL_PATH_AUTOSAVE_KEY = "cursor-service.manual-path";

const router = useRouter();

const routeModeDraft = ref(appState.routingMode);
const manualPath = ref("");
const detectedPath = ref("");

const routeModeState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const manualPathState = reactive({
  busy: false,
  queued: false,
  error: "",
});

const detectState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const modelConfigState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const configuredAdapterCount = computed(() => appState.modelAdapters.length);
const routeModeBusy = computed(() => routeModeState.busy || appState.configSaving);
const manualPathBusy = computed(() => manualPathState.busy || manualPathState.queued);

watch(
  () => appState.routingMode,
  (value) => {
    if (!routeModeState.busy) {
      routeModeDraft.value = value;
    }
  },
  { immediate: true },
);

function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

async function saveRouteMode(value) {
  const nextValue = value === "upstream" ? "upstream" : "local";
  const previousValue = routeModeDraft.value;
  routeModeDraft.value = nextValue;
  routeModeState.retry = () => saveRouteMode(nextValue);
  routeModeState.error = "";
  routeModeState.busy = true;
  try {
    await props.autosave.run("cursor-service.route-mode", async () => {
      const result = await saveRoutingMode(nextValue);
      if (!result?.ok) {
        throw new Error(result?.error || "切换失败");
      }
    });
  } catch (error) {
    routeModeDraft.value = appState.routingMode || previousValue;
    routeModeState.error = toUserError(error);
  } finally {
    routeModeState.busy = false;
  }
}

function clearManualPathErrors() {
  manualPathState.error = "";
  detectState.error = "";
}

function queueManualPathSave() {
  manualPathState.error = "";
  manualPathState.queued = true;
  props.autosave.schedule(
    MANUAL_PATH_AUTOSAVE_KEY,
    persistAndValidateManualPath,
    { debounceMs: 500 },
  );
}

async function persistAndValidateManualPath() {
  manualPathState.queued = false;
  manualPathState.busy = true;
  manualPathState.error = "";
  try {
    manualPath.value = setCursorManualPath(manualPath.value);
    const nextDetectedPath = await detectCursorPath(manualPath.value) || "";
    detectedPath.value = nextDetectedPath;
    if (manualPath.value && !nextDetectedPath) {
      throw new Error("手动指定的 Cursor.exe 路径无效，请检查文件是否存在。");
    }
  } catch (error) {
    detectedPath.value = "";
    manualPathState.error = toUserError(error);
    throw error;
  } finally {
    manualPathState.busy = false;
  }
}

async function flushManualPath() {
  try {
    await props.autosave.flush(MANUAL_PATH_AUTOSAVE_KEY);
  } catch (_error) {
    // error state is already surfaced on the row
  } finally {
    manualPathState.queued = false;
  }
}

async function retryManualPath() {
  manualPathState.queued = false;
  manualPathState.error = "";
  try {
    await props.autosave.retry(MANUAL_PATH_AUTOSAVE_KEY);
  } catch (_error) {
    // the replayed callback updates the row and coordinator error state
  }
}

function handleManualPathInput(value) {
  manualPath.value = value;
  clearManualPathErrors();
  queueManualPathSave();
}

async function handleDetectCursorPath() {
  detectState.retry = handleDetectCursorPath;
  detectState.error = "";
  detectState.busy = true;
  try {
    await flushManualPath();
    const nextDetectedPath = await detectCursorPath(manualPath.value) || "";
    detectedPath.value = nextDetectedPath;
    if (manualPath.value && !nextDetectedPath) {
      detectState.error = "手动指定的 Cursor.exe 路径无效，请检查文件是否存在。";
    }
  } catch (error) {
    detectedPath.value = "";
    detectState.error = toUserError(error);
  } finally {
    detectState.busy = false;
  }
}

async function handleOpenModelConfig() {
  modelConfigState.retry = handleOpenModelConfig;
  modelConfigState.error = "";
  modelConfigState.busy = true;
  try {
    if (isBrowserPreview) {
      await router.push("/model-config");
      return;
    }
    await openModelConfigWindow();
  } catch (error) {
    modelConfigState.error = toUserError(error);
  } finally {
    modelConfigState.busy = false;
  }
}

onMounted(() => {
  manualPath.value = getCursorManualPath();
  void handleDetectCursorPath();
});
</script>

<template>
  <div class="space-y-8">
    <SettingsSection title="Cursor 启动">
      <SettingsRow
        label="手动指定 Cursor.exe 路径"
        description="留空则自动检测"
        :busy="manualPathBusy"
        :error="manualPathState.error"
        @retry="retryManualPath"
      >
        <div class="w-full max-w-[460px] space-y-2">
          <Input
            :model-value="manualPath"
            placeholder="留空则自动检测"
            spellcheck="false"
            @update:model-value="handleManualPathInput"
            @blur="flushManualPath"
            @keydown.enter.prevent="flushManualPath"
          />
          <p v-if="detectedPath" class="break-all text-xs text-[#8ddcb3]">
            当前使用：{{ detectedPath }}
          </p>
          <p v-else class="text-xs text-[#8f8f8f]">
            未检测到 Cursor，可填写完整的 Cursor.exe 路径。
          </p>
        </div>
      </SettingsRow>

      <SettingsRow
        label="自动检测"
        description="重新检测当前可用的 Cursor 安装路径"
        :busy="detectState.busy"
        :error="detectState.error"
        @retry="retryState(detectState)"
      >
        <Button
          variant="default"
          :disabled="detectState.busy"
          @click="handleDetectCursorPath"
        >
          自动检测
        </Button>
      </SettingsRow>
    </SettingsSection>

    <SettingsSection title="本地配置">
      <SettingsRow
        label="运行模式"
        description="控制白名单主链路请求走本地服务，还是回到原始 Cursor 上游地址"
        :busy="routeModeBusy"
        :error="routeModeState.error"
        @retry="retryState(routeModeState)"
      >
        <div class="w-[220px] max-w-full">
          <Select
            :model-value="routeModeDraft"
            :options="ROUTE_MODE_OPTIONS"
            placeholder="选择模式"
            aria-label="运行模式"
            :disabled="routeModeBusy"
            @change="saveRouteMode"
          />
        </div>
      </SettingsRow>

      <SettingsRow
        label="模型配置"
        :description="`已配置 ${configuredAdapterCount} 个模型适配器`"
        :busy="modelConfigState.busy"
        :error="modelConfigState.error"
        @retry="retryState(modelConfigState)"
      >
        <Button
          variant="default"
          :disabled="modelConfigState.busy"
          @click="handleOpenModelConfig"
        >
          打开模型配置
        </Button>
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
