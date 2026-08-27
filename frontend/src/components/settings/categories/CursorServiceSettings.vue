<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import { useMessage } from "@/composables/useMessage";
import {
  ROUTE_MODE_OPTIONS,
  COMPUTER_USE_MODE_OPTIONS,
  appState,
  getCursorManualPath,
  persistUserConfig,
  saveRoutingMode,
  saveComputerUse,
  setCursorManualPath,
  toUserError,
} from "@/state/appState";
import { autoMatchContextWindows, applyTerminalEnvironment, detectCursorPath, getTerminalEnvironmentStatus, installTerminalDependency, TERMINAL_INSTALL_PROGRESS_EVENT } from "@/services/clientApi";
import { isBrowserPreview, runtimeEvents } from "@/services/runtimeAdapter";
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const MANUAL_PATH_AUTOSAVE_KEY = "cursor-service.manual-path";

const router = useRouter();
const message = useMessage();

const routeModeDraft = ref(appState.routingMode);
const manualPath = ref("");
const detectedPath = ref("");
const terminalEnvironment = ref(null);

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

const contextMatchState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const terminalEnvironmentState = reactive({
  busy: false,
  error: "",
  retry: null,
});

// 依赖安装状态：独立跟踪 PowerShell / Python 两个目标的安装进度。
// busy target 为 "" 表示空闲，"powershell"/"python" 表示正在安装该目标。
const dependencyInstallState = reactive({
  busy: "",
  stage: "",
  message: "",
  error: "",
});

// 缺 PowerShell 7（含未检测到、或仅 5.1）时显示安装按钮。
const needsPowerShell7 = computed(() => {
  const env = terminalEnvironment.value;
  if (!env) return false;
  return env.shellName !== "PowerShell 7";
});

// 缺 Python 3 时显示安装按钮。
const needsPython3 = computed(() => {
  const env = terminalEnvironment.value;
  if (!env) return false;
  return !env.pythonPath;
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

// ComputerUse 执行模式（桌面/浏览器）配置。
const computerUseModeDraft = ref(appState.computerUseMode);
const browserStartURLDraft = ref(appState.computerUseBrowserStartURL || "");
const computerUseState = reactive({ busy: false, error: "", retry: null });
const computerUseModeBusy = computed(() => computerUseState.busy || appState.configSaving);

watch(() => appState.computerUseMode, (value) => {
  if (!computerUseState.busy) {
    computerUseModeDraft.value = value;
  }
});

watch(() => appState.computerUseBrowserStartURL, (value) => {
  if (!computerUseState.busy) {
    browserStartURLDraft.value = value || "";
  }
});

async function saveComputerUseMode(value) {
  const nextValue = String(value || "desktop");
  const previousValue = computerUseModeDraft.value;
  computerUseModeDraft.value = nextValue;
  computerUseState.retry = () => saveComputerUseMode(nextValue);
  computerUseState.error = "";
  computerUseState.busy = true;
  try {
    const result = await saveComputerUse({ mode: nextValue });
    if (result?.ok === false) {
      throw new Error(result.error || "保存失败");
    }
  } catch (error) {
    computerUseModeDraft.value = appState.computerUseMode || previousValue;
    computerUseState.error = toUserError(error);
  } finally {
    computerUseState.busy = false;
  }
}

// browserStartURL 用 debounce 保存（类似 manualPath 的 schedule 模式）。
function queueBrowserStartURLSave() {
  computerUseState.error = "";
  props.autosave.schedule(
    "cursor-service.browser-start-url",
    async () => {
      computerUseState.retry = null;
      computerUseState.busy = true;
      try {
        const url = String(browserStartURLDraft.value || "").trim() || "about:blank";
        const result = await saveComputerUse({ browserStartURL: url });
        if (result?.ok === false) {
          throw new Error(result.error || "保存失败");
        }
      } catch (error) {
        computerUseState.error = toUserError(error);
        computerUseState.retry = () => queueBrowserStartURLSave();
      } finally {
        computerUseState.busy = false;
      }
    },
    { debounceMs: 500 },
  );
}

function flushBrowserStartURLSave() {
  props.autosave.flush("cursor-service.browser-start-url");
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

function applyDetectedCursorPath(nextDetectedPath, persistAutoDetected = false) {
  detectedPath.value = nextDetectedPath;
  if (!persistAutoDetected || manualPath.value || !nextDetectedPath) {
    return;
  }
  manualPath.value = setCursorManualPath(nextDetectedPath);
}

async function handleDetectCursorPath() {
  detectState.retry = handleDetectCursorPath;
  detectState.error = "";
  detectState.busy = true;
  try {
    await flushManualPath();
    const nextDetectedPath = await detectCursorPath(manualPath.value) || "";
    applyDetectedCursorPath(nextDetectedPath, true);
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
    await router.push("/model-config");
  } catch (error) {
    modelConfigState.error = toUserError(error);
  } finally {
    modelConfigState.busy = false;
  }
}

function retryContextMatch() {
  if (typeof contextMatchState.retry === "function") {
    void contextMatchState.retry();
  }
}

async function handleContextMatchEnabledChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = Boolean(appState.autoMatchContextWindow);
  appState.autoMatchContextWindow = nextValue;
  contextMatchState.busy = true;
  contextMatchState.error = "";
  contextMatchState.retry = () => handleContextMatchEnabledChange(nextValue);
  try {
    await props.autosave.run("cursor-service.context-match-enabled", async () => {
      const result = await persistUserConfig();
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
    });
  } catch (error) {
    appState.autoMatchContextWindow = previousValue;
    contextMatchState.error = toUserError(error);
  } finally {
    contextMatchState.busy = false;
  }
}

async function handleContextMatchNow() {
  contextMatchState.busy = true;
  contextMatchState.error = "";
  contextMatchState.retry = handleContextMatchNow;
  try {
    // force=true：手动触发不受 autoMatchContextWindow 开关限制。
    const result = await autoMatchContextWindows(true);
    if (!result?.enabled) {
      throw new Error("请先开启自动配对上下文窗口。");
    }
    const forcedHint = result.switchEnabled === false ? "（自动配对开关未开启，本次为手动强制对齐）" : "";
    message.success(`上下文配对完成：共 ${result.total} 个，已更新 ${result.changed} 个。${forcedHint}`);
  } catch (error) {
    contextMatchState.error = toUserError(error);
  } finally {
    contextMatchState.busy = false;
  }
}

async function refreshTerminalEnvironment() {
  terminalEnvironmentState.busy = true;
  terminalEnvironmentState.error = "";
  terminalEnvironmentState.retry = refreshTerminalEnvironment;
  try {
    terminalEnvironment.value = await getTerminalEnvironmentStatus();
  } catch (error) {
    terminalEnvironmentState.error = toUserError(error);
  } finally {
    terminalEnvironmentState.busy = false;
  }
}

async function handleApplyTerminalEnvironment() {
  terminalEnvironmentState.busy = true;
  terminalEnvironmentState.error = "";
  terminalEnvironmentState.retry = handleApplyTerminalEnvironment;
  try {
    terminalEnvironment.value = await applyTerminalEnvironment();
    message.success("终端与 Python 环境已写入 Cursor，重新打开终端后生效。");
  } catch (error) {
    terminalEnvironmentState.error = toUserError(error);
  } finally {
    terminalEnvironmentState.busy = false;
  }
}

// handleInstallDependency 触发 winget 安装；RPC 立即返回，进度走事件回调刷新。
// winget 安装系统级软件会弹 UAC，属正常行为。
async function handleInstallDependency(target) {
  if (dependencyInstallState.busy) {
    return;
  }
  dependencyInstallState.busy = target;
  dependencyInstallState.stage = "pending";
  dependencyInstallState.message = target === "powershell" ? "准备安装 PowerShell 7..." : "准备安装 Python 3...";
  dependencyInstallState.error = "";
  try {
    await installTerminalDependency(target);
  } catch (error) {
    dependencyInstallState.busy = "";
    dependencyInstallState.error = toUserError(error);
  }
}

// 安装进度事件回调：更新阶段文案；done 时刷新检测，error 时记录。
function handleInstallProgressEvent(payload) {
  const data = payload?.data ?? payload;
  if (!data || typeof data !== "object") return;
  dependencyInstallState.stage = data.stage || dependencyInstallState.stage;
  if (typeof data.message === "string" && data.message) {
    dependencyInstallState.message = data.message;
  }
  if (data.stage === "done") {
    if (data.status) {
      terminalEnvironment.value = data.status;
    }
    message.success(data.message || "安装完成。");
    dependencyInstallState.busy = "";
    // done 后仍异步刷新一次，确保检测口径最新（winget 装完后可能需重新探测）。
    void refreshTerminalEnvironment();
  } else if (data.stage === "error") {
    dependencyInstallState.error = data.message || "安装失败。";
    dependencyInstallState.busy = "";
  }
}

let unsubscribeInstallProgress = null;

onMounted(() => {
  manualPath.value = getCursorManualPath();
  void handleDetectCursorPath();
  void refreshTerminalEnvironment();
  unsubscribeInstallProgress = runtimeEvents.On(TERMINAL_INSTALL_PROGRESS_EVENT, handleInstallProgressEvent);
});

onUnmounted(() => {
  if (typeof unsubscribeInstallProgress === "function") {
    unsubscribeInstallProgress();
    unsubscribeInstallProgress = null;
  }
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

    <SettingsSection title="终端环境">
      <SettingsRow
        label="Cursor 终端与 Python 3"
        description="自动选择当前系统可用的现代终端，并固定 Python 3 解释器路径。Windows 会优先使用 PowerShell 7。"
        :busy="terminalEnvironmentState.busy"
        :error="terminalEnvironmentState.error"
        @retry="retryState(terminalEnvironmentState)"
      >
        <div class="w-full max-w-[460px] space-y-2">
          <div v-if="terminalEnvironment" class="rounded-[6px] border border-white/10 bg-black/15 px-3 py-2 text-xs text-[#a3a3a3]">
            <p><span class="text-[#737373]">终端：</span>{{ terminalEnvironment.shellName || "未检测到" }}<span v-if="terminalEnvironment.shellVersion"> · {{ terminalEnvironment.shellVersion }}</span></p>
            <p class="mt-1 break-all"><span class="text-[#737373]">Python：</span>{{ terminalEnvironment.pythonPath || "未检测到 Python 3" }}<span v-if="terminalEnvironment.pythonVersion"> · {{ terminalEnvironment.pythonVersion }}</span></p>
            <p v-if="terminalEnvironment.configurationNotice" class="mt-1 text-[#737373]">{{ terminalEnvironment.configurationNotice }}</p>
            <p v-if="terminalEnvironment.upgradeRecommended" class="mt-2 text-[#f0c674]">{{ terminalEnvironment.upgradeMessage }}</p>
          </div>
          <p v-else class="text-xs text-[#737373]">正在检测本机终端和 Python 3。</p>

          <!-- 一键安装：缺失时显示，安装中显示进度，winget 会弹 UAC -->
          <div v-if="needsPowerShell7 || needsPython3" class="space-y-2 rounded-[6px] border border-[#3f3f3f] bg-[#1b1b1b] px-3 py-2">
            <div class="flex flex-wrap items-center gap-2">
              <Button
                v-if="needsPowerShell7"
                variant="default"
                :disabled="Boolean(dependencyInstallState.busy)"
                @click="handleInstallDependency('powershell')"
              >
                安装 PowerShell 7
              </Button>
              <Button
                v-if="needsPython3"
                variant="default"
                :disabled="Boolean(dependencyInstallState.busy)"
                @click="handleInstallDependency('python')"
              >
                安装 Python 3
              </Button>
              <span v-if="dependencyInstallState.busy" class="text-xs text-[#10d06f]">{{ dependencyInstallState.message }}</span>
            </div>
            <p v-if="dependencyInstallState.error" class="break-all text-xs text-[#f87171]">{{ dependencyInstallState.error }}</p>
            <p class="text-[11px] leading-4 text-[#777]">通过系统 winget 安装，安装时会弹出 UAC 提权确认，请点击「是」继续。</p>
          </div>
        </div>
        <div class="flex shrink-0 gap-2">
          <Button variant="default" :disabled="terminalEnvironmentState.busy" @click="refreshTerminalEnvironment">重新检测</Button>
          <Button variant="primary" :disabled="terminalEnvironmentState.busy || !terminalEnvironment?.shellPath" @click="handleApplyTerminalEnvironment">应用到 Cursor</Button>
        </div>
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
        label="ComputerUse 执行模式"
        description="控制 AI 的屏幕操作工具由谁执行。桌面模式操作真实屏幕；浏览器模式驱动 headless 浏览器，适合前端 UI 验证。"
        :busy="computerUseModeBusy"
        :error="computerUseState.error"
        @retry="retryState(computerUseState)"
      >
        <div class="w-full max-w-[460px] space-y-2">
          <div class="w-[220px] max-w-full">
            <Select
              :model-value="computerUseModeDraft"
              :options="COMPUTER_USE_MODE_OPTIONS"
              placeholder="选择模式"
              aria-label="ComputerUse 执行模式"
              :disabled="computerUseModeBusy"
              @change="saveComputerUseMode"
            />
          </div>
          <div v-if="computerUseModeDraft === 'browser'" class="w-full max-w-[420px]">
            <Input
              :model-value="browserStartURLDraft"
              :disabled="computerUseModeBusy"
              placeholder="浏览器初始地址，如 http://localhost:5173"
              aria-label="浏览器初始地址"
              @input="(value) => { browserStartURLDraft = value; queueBrowserStartURLSave(); }"
              @blur="flushBrowserStartURLSave"
              @keyup.enter="flushBrowserStartURLSave"
            />
          </div>
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

      <SettingsRow
        label="自动配对上下文窗口"
        description="按模型目录与服务端能力自动匹配上下文窗口，避免手动填写过大或过小。"
        :busy="contextMatchState.busy"
        :error="contextMatchState.error"
        @retry="retryContextMatch"
      >
        <div class="flex flex-wrap items-center justify-end gap-2">
          <Switch
            compact
            label=""
            :enabled="Boolean(appState.autoMatchContextWindow)"
            :busy="contextMatchState.busy"
            :disabled="contextMatchState.busy"
            aria-label="自动配对上下文窗口"
            @change="handleContextMatchEnabledChange"
          />
          <Button
            variant="default"
            :disabled="contextMatchState.busy || !appState.autoMatchContextWindow"
            @click="handleContextMatchNow"
          >
            一键配对
          </Button>
        </div>
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
