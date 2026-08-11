<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import { getMirrorCaptureStatus, openMirrorCaptureDirectory } from "@/services/clientApi";
import {
  appState,
  repairProxyAction,
  saveDebugLogEnabled,
  saveGoalSettings,
  saveLocalResponseCacheEnabled,
  saveLocalResponseCacheSettings,
  saveMirrorCaptureEnabled,
  saveRoutingMode,
  startService,
  toUserError,
} from "@/state/appState";
import { computed, onMounted, reactive, ref, watch } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const cacheFieldAutosaveKey = (field) => `advanced.${field}`;

const message = useMessage();

const directModeDraft = ref(appState.routingMode === "upstream");
const cacheEnabledDraft = ref(Boolean(appState.localResponseCache?.enabled));
const cachePersistDraft = ref(appState.localResponseCache?.persist !== false);
const ttlSecondsDraft = ref("");
const maxEntriesDraft = ref("");

const debugLogDraft = ref(Boolean(appState.debugLogEnabled));
const mirrorCaptureDraft = ref(Boolean(appState.mirrorCaptureEnabled));

const goalEnabledDraft = ref(Boolean(appState.goal?.enabled));

const debugLogState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const mirrorCaptureState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const mirrorCaptureStatusState = reactive({
  loading: false,
  error: "",
  enabled: false,
  backendRunning: false,
  proxyRunning: false,
  cursorSettingsApplied: false,
  ready: false,
  recordPath: "",
  fileExists: false,
  sizeBytes: 0,
  modifiedAtUnixMs: 0,
});

const mirrorCaptureActionState = reactive({
  starting: false,
  repairing: false,
  openingDirectory: false,
});

const goalEnabledState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const directModeState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const cacheEnabledState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const cachePersistState = reactive({
  busy: false,
  error: "",
  retry: null,
});

const ttlSecondsState = reactive({
  busy: false,
  queued: false,
  error: "",
});

const maxEntriesState = reactive({
  busy: false,
  queued: false,
  error: "",
});

const cacheEnabled = computed(() => cacheEnabledDraft.value);
const directModeBusy = computed(() => directModeState.busy || appState.configSaving);
const cacheEnabledBusy = computed(() => cacheEnabledState.busy || appState.configSaving);
const cachePersistBusy = computed(() => cachePersistState.busy || appState.configSaving);
const goalEnabledBusy = computed(() => goalEnabledState.busy || appState.configSaving);
const mirrorCaptureBusy = computed(() => mirrorCaptureState.busy || appState.configSaving);
const mirrorCaptureStatusBusy = computed(() => mirrorCaptureStatusState.loading || mirrorCaptureActionState.starting || mirrorCaptureActionState.repairing);
const mirrorCaptureStatusLabel = computed(() => {
  if (mirrorCaptureStatusState.loading) return "正在检查";
  if (!mirrorCaptureStatusState.enabled) return "未启用";
  if (!mirrorCaptureStatusState.backendRunning || !mirrorCaptureStatusState.proxyRunning) return "等待本地服务";
  if (!mirrorCaptureStatusState.cursorSettingsApplied) return "Cursor 未接入";
  if (mirrorCaptureStatusState.fileExists) return "已记录";
  return "等待官方模型请求";
});
const mirrorCaptureStatusDescription = computed(() => {
  if (!mirrorCaptureStatusState.enabled) return "开启镜像记录后，系统会检查本地服务、Cursor 代理接入和实际记录情况。";
  if (!mirrorCaptureStatusState.backendRunning || !mirrorCaptureStatusState.proxyRunning) return "本地服务未完整运行，暂时无法接收 Cursor 的官方模型请求。";
  if (!mirrorCaptureStatusState.cursorSettingsApplied) return "Cursor 尚未配置为通过本地代理访问，修复后请按提示重启 Cursor。";
  if (!mirrorCaptureStatusState.fileExists) return "已具备抓包条件，等待 Cursor 发起一次官方模型请求。";
  return `已写入 ${formatMirrorCaptureSize(mirrorCaptureStatusState.sizeBytes)}，最后更新于 ${formatMirrorCaptureTime(mirrorCaptureStatusState.modifiedAtUnixMs)}。`;
});
const mirrorCaptureNeedsService = computed(() => mirrorCaptureStatusState.enabled && (!mirrorCaptureStatusState.backendRunning || !mirrorCaptureStatusState.proxyRunning));
const mirrorCaptureNeedsProxyRepair = computed(() => mirrorCaptureStatusState.enabled && mirrorCaptureStatusState.backendRunning && mirrorCaptureStatusState.proxyRunning && !mirrorCaptureStatusState.cursorSettingsApplied);
const ttlBusy = computed(() => ttlSecondsState.busy || ttlSecondsState.queued || appState.configSaving);
const maxEntriesBusy = computed(() => maxEntriesState.busy || maxEntriesState.queued || appState.configSaving);

watch(
  () => appState.routingMode,
  (value) => {
    if (!directModeState.busy) {
      directModeDraft.value = value === "upstream";
    }
  },
  { immediate: true },
);

watch(
  () => appState.debugLogEnabled,
  (value) => {
    if (!debugLogState.busy) {
      debugLogDraft.value = Boolean(value);
    }
  },
  { immediate: true },
);

onMounted(() => {
  void refreshMirrorCaptureStatus();
});

watch(
  () => appState.mirrorCaptureEnabled,
  (value) => {
    if (!mirrorCaptureState.busy) {
      mirrorCaptureDraft.value = Boolean(value);
    }
  },
  { immediate: true },
);

watch(
  () => appState.goal,
  (value) => {
    if (!goalEnabledState.busy) {
      goalEnabledDraft.value = Boolean(value?.enabled);
    }
  },
  { immediate: true },
);

watch(
  () => appState.localResponseCache,
  (value) => {
    const next = value || {};
    if (!cacheEnabledState.busy) {
      cacheEnabledDraft.value = Boolean(next.enabled);
    }
    if (!cachePersistState.busy) {
      cachePersistDraft.value = next.persist !== false;
    }
    if (!ttlSecondsState.busy && !ttlSecondsState.queued) {
      ttlSecondsDraft.value = next.ttlSeconds ? String(next.ttlSeconds) : "";
    }
    if (!maxEntriesState.busy && !maxEntriesState.queued) {
      maxEntriesDraft.value = next.maxEntries ? String(next.maxEntries) : "";
    }
  },
  { immediate: true, deep: true },
);

function retryState(state) {
  if (typeof state.retry === "function") {
    void state.retry();
  }
}

async function handleDirectModeChange(enabled) {
  const nextValue = Boolean(enabled);
  const targetMode = nextValue ? "upstream" : "local";
  const previousValue = directModeDraft.value;
  directModeDraft.value = nextValue;
  directModeState.retry = () => handleDirectModeChange(nextValue);
  directModeState.error = "";
  if (nextValue) {
    const confirmed = await showModal({
      title: "开启直连模式",
      content: "直连模式会绕过本地代理服务，Cursor 将直接连接官方服务，可能产生官方账号计费。确定开启吗？",
      confirmText: "开启直连",
      cancelText: "取消",
    });
    if (!confirmed) {
      directModeDraft.value = previousValue;
      return;
    }
  }

  directModeState.busy = true;
  try {
    await props.autosave.run("advanced.direct-mode", async () => {
      const result = await saveRoutingMode(targetMode);
      if (!result?.ok) {
        throw new Error(result?.error || "切换失败");
      }
    });
    message.success(nextValue ? "已切换到直连 Cursor 模式" : "已切换到本地服务模式");
  } catch (error) {
    directModeDraft.value = appState.routingMode === "upstream";
    directModeState.error = toUserError(error);
  } finally {
    directModeState.busy = false;
  }
}

async function handleDebugLogChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = debugLogDraft.value;
  debugLogDraft.value = nextValue;
  debugLogState.retry = () => handleDebugLogChange(nextValue);
  debugLogState.error = "";
  debugLogState.busy = true;
  try {
    await props.autosave.run("advanced.debug-log", async () => {
      const result = await saveDebugLogEnabled(nextValue);
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
      message.success(nextValue ? "已开启调试日志" : "已关闭调试日志（即时生效）");
    });
  } catch (error) {
    debugLogDraft.value = previousValue;
    debugLogState.error = toUserError(error);
  } finally {
    debugLogState.busy = false;
  }
}

async function handleMirrorCaptureChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = mirrorCaptureDraft.value;
  mirrorCaptureDraft.value = nextValue;
  mirrorCaptureState.retry = () => handleMirrorCaptureChange(nextValue);
  mirrorCaptureState.error = "";
  mirrorCaptureState.busy = true;
  try {
    await props.autosave.run("advanced.mirror-capture", async () => {
      const result = await saveMirrorCaptureEnabled(nextValue);
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
      message.success(nextValue ? "已开启镜像记录（即时生效）" : "已关闭镜像记录（即时生效）");
      await refreshMirrorCaptureStatus();
    });
  } catch (error) {
    mirrorCaptureDraft.value = previousValue;
    mirrorCaptureState.error = toUserError(error);
  } finally {
    mirrorCaptureState.busy = false;
  }
}

function applyMirrorCaptureStatus(status) {
  const source = status && typeof status === "object" ? status : {};
  mirrorCaptureStatusState.enabled = Boolean(source.enabled);
  mirrorCaptureStatusState.backendRunning = Boolean(source.backendRunning);
  mirrorCaptureStatusState.proxyRunning = Boolean(source.proxyRunning);
  mirrorCaptureStatusState.cursorSettingsApplied = Boolean(source.cursorSettingsApplied);
  mirrorCaptureStatusState.ready = Boolean(source.ready);
  mirrorCaptureStatusState.recordPath = String(source.recordPath || "");
  mirrorCaptureStatusState.fileExists = Boolean(source.fileExists);
  mirrorCaptureStatusState.sizeBytes = Number(source.sizeBytes) || 0;
  mirrorCaptureStatusState.modifiedAtUnixMs = Number(source.modifiedAtUnixMs) || 0;
}

async function refreshMirrorCaptureStatus() {
  if (mirrorCaptureStatusState.loading) return;
  mirrorCaptureStatusState.loading = true;
  mirrorCaptureStatusState.error = "";
  try {
    applyMirrorCaptureStatus(await getMirrorCaptureStatus());
  } catch (error) {
    mirrorCaptureStatusState.error = toUserError(error);
  } finally {
    mirrorCaptureStatusState.loading = false;
  }
}

async function handleStartMirrorCaptureService() {
  mirrorCaptureActionState.starting = true;
  mirrorCaptureStatusState.error = "";
  try {
    const result = await startService();
    if (!result?.ok) throw new Error(result?.error || "启动本地服务失败");
    message.success("本地服务已启动，正在重新检查抓包状态");
  } catch (error) {
    mirrorCaptureStatusState.error = toUserError(error);
  } finally {
    mirrorCaptureActionState.starting = false;
    await refreshMirrorCaptureStatus();
  }
}

async function handleRepairMirrorCaptureProxy() {
  const confirmed = await showModal({
    title: "修复 Cursor 代理",
    content: "将重新写入并校验 Cursor 的本地代理配置。修复完成后需要重启 Cursor，是否继续？",
    confirmText: "修复代理",
    cancelText: "取消",
  });
  if (!confirmed) return;

  mirrorCaptureActionState.repairing = true;
  mirrorCaptureStatusState.error = "";
  try {
    const result = await repairProxyAction();
    if (!result?.ok) throw new Error(result?.error || "修复代理失败");
    message.success("已修复 Cursor 代理配置，请重启 Cursor 后再发起模型请求");
  } catch (error) {
    mirrorCaptureStatusState.error = toUserError(error);
  } finally {
    mirrorCaptureActionState.repairing = false;
    await refreshMirrorCaptureStatus();
  }
}

async function handleOpenMirrorCaptureDirectory() {
  mirrorCaptureActionState.openingDirectory = true;
  mirrorCaptureStatusState.error = "";
  try {
    await openMirrorCaptureDirectory();
  } catch (error) {
    mirrorCaptureStatusState.error = toUserError(error);
  } finally {
    mirrorCaptureActionState.openingDirectory = false;
  }
}

function formatMirrorCaptureSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatMirrorCaptureTime(timestamp) {
  if (!timestamp) return "未知时间";
  return new Date(timestamp).toLocaleString();
}

async function handleGoalEnabledChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = goalEnabledDraft.value;
  goalEnabledDraft.value = nextValue;
  goalEnabledState.retry = () => handleGoalEnabledChange(nextValue);
  goalEnabledState.error = "";
  goalEnabledState.busy = true;
  try {
    await props.autosave.run("advanced.goal-enabled", async () => {
      const result = await saveGoalSettings({ enabled: nextValue });
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
      message.success(nextValue ? "已启用 Goal 命令" : "已关闭 Goal 命令（/goal 按普通对话处理）");
    });
  } catch (error) {
    goalEnabledDraft.value = Boolean(appState.goal?.enabled ?? previousValue);
    goalEnabledState.error = toUserError(error);
  } finally {
    goalEnabledState.busy = false;
  }
}

async function handleCacheEnabledChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = cacheEnabledDraft.value;
  cacheEnabledDraft.value = nextValue;
  cacheEnabledState.retry = () => handleCacheEnabledChange(nextValue);
  cacheEnabledState.error = "";
  cacheEnabledState.busy = true;
  try {
    await props.autosave.run("advanced.cache-enabled", async () => {
      const result = await saveLocalResponseCacheEnabled(nextValue);
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
    });
  } catch (error) {
    cacheEnabledDraft.value = appState.localResponseCache?.enabled ?? previousValue;
    cacheEnabledState.error = toUserError(error);
  } finally {
    cacheEnabledState.busy = false;
  }
}

async function handleCachePersistChange(enabled) {
  const nextValue = Boolean(enabled);
  const previousValue = cachePersistDraft.value;
  cachePersistDraft.value = nextValue;
  cachePersistState.retry = () => handleCachePersistChange(nextValue);
  cachePersistState.error = "";
  cachePersistState.busy = true;
  try {
    await props.autosave.run("advanced.cache-persist", async () => {
      const result = await saveLocalResponseCacheSettings({ persist: nextValue });
      if (!result?.ok) {
        throw new Error(result?.error || "保存失败");
      }
    });
  } catch (error) {
    cachePersistDraft.value = appState.localResponseCache?.persist !== false;
    cachePersistState.error = toUserError(error);
  } finally {
    cachePersistState.busy = false;
  }
}

function queueCacheFieldSave(field, state, valueRef) {
  state.error = "";
  state.queued = true;
  props.autosave.schedule(
    cacheFieldAutosaveKey(field),
    async () => {
      state.queued = false;
      state.busy = true;
      try {
        const result = await saveLocalResponseCacheSettings({ [field]: valueRef.value });
        if (!result?.ok) {
          throw new Error(result?.error || "保存失败");
        }
      } catch (error) {
        state.error = toUserError(error);
        throw error;
      } finally {
        state.busy = false;
      }
    },
    { debounceMs: 500 },
  );
}

async function flushCacheField(field, state) {
  try {
    await props.autosave.flush(cacheFieldAutosaveKey(field));
  } catch (_error) {
    // error state is already surfaced on the row
  } finally {
    state.queued = false;
  }
}

async function retryCacheField(field, state) {
  state.queued = false;
  state.error = "";
  try {
    await props.autosave.retry(cacheFieldAutosaveKey(field));
  } catch (_error) {
    // the replayed callback updates the row and coordinator error state
  }
}

function handleCacheFieldInput(field, state, valueRef, value) {
  valueRef.value = value;
  state.error = "";
  queueCacheFieldSave(field, state, valueRef);
}
</script>

<template>
  <div class="space-y-8">
    <SettingsSection title="高级连接">
      <SettingsRow
        label="直连模式"
        description="绕过本地服务并直接连接官方，可能产生官方账号计费。"
        :busy="directModeBusy"
        :error="directModeState.error"
        @retry="retryState(directModeState)"
      >
        <Switch
          compact
          label=""
          :enabled="directModeDraft"
          :busy="directModeBusy"
          :disabled="directModeBusy"
          aria-label="直连模式"
          @change="handleDirectModeChange"
        />
      </SettingsRow>
    </SettingsSection>

    <SettingsSection title="调试日志">
      <SettingsRow
        label="记录调试日志"
        description="把对话级调试日志写入磁盘（history 目录，单个 jsonl 文件上限 50MB）。关闭后立即停止写入，不再产生大体积日志。"
        :busy="debugLogState.busy"
        :error="debugLogState.error"
        @retry="debugLogState.retry?.()"
      >
        <Switch
          compact
          label=""
          :enabled="debugLogDraft"
          :busy="debugLogState.busy"
          :disabled="debugLogState.busy"
          aria-label="记录调试日志"
          @change="handleDebugLogChange"
        />
      </SettingsRow>
    </SettingsSection>

    <SettingsSection title="官方请求镜像记录（调试）">
      <SettingsRow
        label="镜像记录官方请求"
        description="抓取 Cursor 直连官方模型 API 的请求和响应明文，用于测试和排障。默认关闭；开启后立即生效，记录在 history/_debug/mirror/official.raw.jsonl，可能包含提示词、模型回复和工作区上下文等敏感内容。"
        :busy="mirrorCaptureBusy"
        :error="mirrorCaptureState.error"
        @retry="mirrorCaptureState.retry?.()"
      >
        <Switch
          compact
          label=""
          :enabled="mirrorCaptureDraft"
          :busy="mirrorCaptureBusy"
          :disabled="mirrorCaptureBusy"
          aria-label="镜像记录官方请求"
          @change="handleMirrorCaptureChange"
        />
      </SettingsRow>

      <SettingsRow
        label="抓包状态"
        :description="mirrorCaptureStatusDescription"
        :busy="mirrorCaptureStatusBusy"
        :error="mirrorCaptureStatusState.error"
        @retry="refreshMirrorCaptureStatus"
      >
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <span
            class="inline-flex min-h-[24px] items-center rounded-[5px] border px-2 text-xs"
            :class="mirrorCaptureStatusState.fileExists ? 'border-[#1b6b45] bg-[#123524] text-[#8ce0af]' : (mirrorCaptureStatusState.enabled ? 'border-[#6b4f1a] bg-[#302512] text-[#f2c66d]' : 'border-[#3f3f3f] bg-[#292929] text-[#a3a3a3]')"
          >
            {{ mirrorCaptureStatusLabel }}
          </span>
          <Button variant="default" :disabled="mirrorCaptureStatusBusy" @click="refreshMirrorCaptureStatus">
            <span class="icon-[mdi--refresh] text-[15px]" aria-hidden="true" />刷新
          </Button>
          <Button v-if="mirrorCaptureNeedsService" variant="default" :disabled="mirrorCaptureStatusBusy" @click="handleStartMirrorCaptureService">
            <span class="icon-[mdi--play] text-[15px]" aria-hidden="true" />启动本地服务
          </Button>
          <Button v-if="mirrorCaptureNeedsProxyRepair" variant="default" :disabled="mirrorCaptureStatusBusy" @click="handleRepairMirrorCaptureProxy">
            <span class="icon-[mdi--wrench-outline] text-[15px]" aria-hidden="true" />修复代理
          </Button>
          <Button variant="default" :disabled="mirrorCaptureActionState.openingDirectory" @click="handleOpenMirrorCaptureDirectory">
            <span class="icon-[mdi--folder-open-outline] text-[15px]" aria-hidden="true" />打开记录目录
          </Button>
        </div>
      </SettingsRow>
    </SettingsSection>

    <SettingsSection>
      <SettingsRow
        label="本地响应缓存"
        description="对完全相同的请求复用上次响应，减少 token 支出。默认关闭；仅精确匹配命中，不影响 agent 正确性。"
        :busy="cacheEnabledBusy"
        :error="cacheEnabledState.error"
        @retry="retryState(cacheEnabledState)"
      >
        <Switch
          compact
          label=""
          :enabled="cacheEnabled"
          :busy="cacheEnabledBusy"
          :disabled="cacheEnabledBusy"
          aria-label="本地响应缓存"
          @change="handleCacheEnabledChange"
        />
      </SettingsRow>

      <template v-if="cacheEnabled">
        <SettingsRow
          label="持久化到磁盘"
          description="把缓存条目写入本地磁盘，重启应用后仍可命中。关闭后仅保留内存缓存，重启即清空。"
          :busy="cachePersistBusy"
          :error="cachePersistState.error"
          @retry="retryState(cachePersistState)"
        >
          <Switch
            compact
            label=""
            :enabled="cachePersistDraft"
            :busy="cachePersistBusy"
            :disabled="cachePersistBusy"
            aria-label="持久化到磁盘"
            @change="handleCachePersistChange"
          />
        </SettingsRow>

        <SettingsRow
          label="缓存有效期（秒）"
          description="缓存条目多久未访问则失效（0 = 默认 30 天）。"
          :busy="ttlBusy"
          :error="ttlSecondsState.error"
          @retry="retryCacheField('ttlSeconds', ttlSecondsState)"
        >
          <div class="w-[220px] max-w-full">
            <Input
              :model-value="ttlSecondsDraft"
              type="number"
              min="0"
              placeholder="留空/0 = 默认 30 天"
              @update:model-value="(value) => handleCacheFieldInput('ttlSeconds', ttlSecondsState, ttlSecondsDraft, value)"
              @blur="flushCacheField('ttlSeconds', ttlSecondsState)"
              @keydown.enter.prevent="flushCacheField('ttlSeconds', ttlSecondsState)"
            />
          </div>
        </SettingsRow>

        <SettingsRow
          label="最多缓存条数"
          :busy="maxEntriesBusy"
          :error="maxEntriesState.error"
          @retry="retryCacheField('maxEntries', maxEntriesState)"
        >
          <div class="w-[220px] max-w-full">
            <Input
              :model-value="maxEntriesDraft"
              type="number"
              min="0"
              placeholder="留空/0 = 默认 2048"
              @update:model-value="(value) => handleCacheFieldInput('maxEntries', maxEntriesState, maxEntriesDraft, value)"
              @blur="flushCacheField('maxEntries', maxEntriesState)"
              @keydown.enter.prevent="flushCacheField('maxEntries', maxEntriesState)"
            />
          </div>
        </SettingsRow>
      </template>
    </SettingsSection>

    <SettingsSection title="Goal 命令">
      <SettingsRow
        label="启用 Goal 命令"
        description="开启后 /goal 与 /goal --strict 由系统识别并进入 Goal 执行；关闭时当作普通对话。"
        :busy="goalEnabledBusy"
        :error="goalEnabledState.error"
        @retry="retryState(goalEnabledState)"
      >
        <Switch
          compact
          label=""
          :enabled="goalEnabledDraft"
          :busy="goalEnabledBusy"
          :disabled="goalEnabledBusy"
          aria-label="启用 Goal 命令"
          @change="handleGoalEnabledChange"
        />
      </SettingsRow>
    </SettingsSection>
  </div>
</template>
