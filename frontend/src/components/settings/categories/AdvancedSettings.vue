<script setup>
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Input from "@/components/ui/Input.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  appState,
  saveLocalResponseCacheEnabled,
  saveLocalResponseCacheSettings,
  saveRoutingMode,
  toUserError,
} from "@/state/appState";
import { computed, reactive, ref, watch } from "vue";

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
  </div>
</template>
