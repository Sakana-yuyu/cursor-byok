<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import Switch from "@/components/ui/Switch.vue";
import Select from "@/components/ui/Select.vue";
import HomeMetricsCard from "@/components/HomeMetricsCard.vue";
import PromptPreviewModal from "@/components/PromptPreviewModal.vue";
import { useMessage } from "@/composables/useMessage";
import { showModal } from "@/composables/useModal";
import {
  appState,
  appViewState,
  openConfigWindow,
  openModelConfigWindow,
  saveRoutingMode,
  syncHomeMetrics,
  syncServiceState,
  toUserError,
  toggleService,
} from "@/state/appState";
import {
  getPromptInjectionSettings,
  refreshPromptInjection,
  refreshPromptInjectionCatalog,
  savePromptInjectionSettings,
} from "@/services/clientApi";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";

const router = useRouter();
const directModeEnabled = computed(() => appState.routingMode === "upstream");
const message = useMessage();
const promptInjectionModeOptions = [
  { value: "replace", label: "替换原提示词" },
  { value: "append", label: "追加受管区块" },
];

async function showActionError(title, error) {
  await showModal({
    title,
    content: String(error || "服务错误").trim() || "服务错误",
  });
}

async function handleToggleService() {
  const result = await toggleService();
  if (!result.ok) {
    await showActionError("服务操作失败", result.error);
  }
}

async function handleRefreshState() {
  const [serviceStateResult] = await Promise.allSettled([
    syncServiceState(),
    syncHomeMetrics(),
  ]);
  if (serviceStateResult.status === "rejected") {
    await showActionError("刷新失败", toUserError(serviceStateResult.reason));
  }
}

async function handleRefreshMetrics() {
  await syncHomeMetrics().catch(() => {});
}

async function handleOpenConfig() {
  try {
    await openConfigWindow();
  } catch (error) {
    await showActionError("打开失败", toUserError(error));
  }
}

async function handleOpenRequestMetrics() {
  await router.push("/request-metrics");
}

async function handleOpenModelConfig() {
  if (isBrowserPreview) {
    await router.push("/model-config");
    return;
  }
  try {
    await openModelConfigWindow();
  } catch (error) {
    await showActionError("打开失败", toUserError(error));
  }
}

const promptInjection = reactive({
  enabled: false,
  softwareChineseEnabled: false,
  mode: "replace",
  repo: "yynxxxxx/Codex-X",
  ref: "main",
  selectedTemplate: "gpt5.5-unrestricted.md",
  localContent: "",
  cacheContent: "",
  cacheAvailable: false,
  lastUpdated: "",
  lastError: "",
  templates: [],
});
const promptInjectionBusy = ref(false);
const promptInjectionLoaded = ref(false);
const promptPreview = ref(null);

function openPromptPreview(template) {
  const content = template?.content || promptInjection.localContent || promptInjection.cacheContent || "";
  if (!content.trim()) {
    return;
  }
  promptPreview.value = {
    templateName: template?.name || promptInjection.selectedTemplate,
    repo: promptInjection.repo,
    refName: promptInjection.ref,
    content,
  };
}

function closePromptPreview() {
  promptPreview.value = null;
}

function applyPromptInjectionStatus(status) {
  const value = status?.config || status || {};
  Object.assign(promptInjection, {
    enabled: Boolean(value.enabled),
    softwareChineseEnabled: Boolean(value.softwareChineseEnabled),
    mode: value.mode === "append" ? "append" : "replace",
    repo: value.repo || "yynxxxxx/Codex-X",
    ref: value.ref || "main",
    selectedTemplate: value.selectedTemplate || "gpt5.5-unrestricted.md",
    localContent: value.localContent || value.cacheContent || "",
    cacheContent: value.cacheContent || value.localContent || "",
    cacheAvailable: Boolean(status?.cacheAvailable || value.cacheContent || value.localContent),
    lastUpdated: value.lastUpdated || "",
    lastError: value.lastError || "",
    templates: Array.isArray(value.templates) ? value.templates : [],
  });
}

async function loadPromptInjection() {
  try {
    applyPromptInjectionStatus(await getPromptInjectionSettings());
  } catch (error) {
    promptInjection.lastError = toUserError(error);
  } finally {
    promptInjectionLoaded.value = true;
  }
}

async function handleRefreshPromptInjection() {
  promptInjectionBusy.value = true;
  try {
    applyPromptInjectionStatus(await refreshPromptInjectionCatalog());
    message.success("提示词清单已更新");
  } catch (error) {
    // Keep the existing single-template refresh available when catalog access is unavailable.
    try {
      applyPromptInjectionStatus(await refreshPromptInjection());
      message.success("提示词已更新");
    } catch (fallbackError) {
      promptInjection.lastError = toUserError(fallbackError || error);
      await showActionError("拉取提示词失败", fallbackError || error);
    }
  } finally {
    promptInjectionBusy.value = false;
  }
}

async function togglePromptTemplate(template, enabled) {
  const nextTemplates = promptInjection.templates.map((item) =>
    item.name === template.name ? { ...item, enabled } : item,
  );
  promptInjection.templates = nextTemplates;
  await savePromptInjection();
}

async function handleDirectModeChange(enabled) {
  const result = await saveRoutingMode(enabled ? "upstream" : "local");
  if (!result.ok) {
    await showActionError("切换失败", result.error);
    return;
  }
  message.success(enabled ? "已切换到直连 Cursor 模式" : "已切换到本地服务模式");
}

onMounted(() => {
  void syncServiceState().catch(() => {});
  void syncHomeMetrics().catch(() => {});
  void loadPromptInjection();
});

</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto overscroll-contain p-4 pt-0 text-[#e5e5e5]">
    <HomeMetricsCard
      :metrics="appState.homeMetrics"
      :loading="appState.homeMetricsLoading"
      :error="appState.homeMetricsError"
      @refresh="handleRefreshMetrics"
    />

    <Card>
      <div class="flex flex-col gap-4">
        <div class="flex items-start justify-between gap-4">
          <div class="flex flex-col gap-1">
            <div class="text-sm" :class="appViewState.serviceStatusClass">
              {{ appViewState.serviceStatusText }}
            </div>
          </div>
          <div class="center-row gap-2">
            <Button variant="primary" :disabled="appState.serviceBusy" @click="handleToggleService">
              <span class="icon-[mdi--pause] text-[16px]" v-if="appState.serviceRunning"></span>
              <span class="icon-[mdi--play] text-[16px]" v-else></span>
              <span> {{ appViewState.serviceButtonText }}</span>
            </Button>
          </div>
        </div>

        <div v-if="appState.serviceLastError"
          class="rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
          {{ appState.serviceLastError }}
        </div>

        <Switch
          label="直连模式"
          description="开启后，Cursor将直接接通官方，请勿开启"
          enabled-text="当前为直连模式"
          disabled-text="当前为本地服务模式"
          :enabled="directModeEnabled"
          :busy="appState.configSaving"
          :disabled="appState.configSaving"
          @change="handleDirectModeChange"
        />
      </div>
    </Card>

    <Card>
      <div class="flex flex-col gap-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h2 class="text-base font-medium text-white">提示词注入</h2>
            <div class="text-xs text-[#a3a3a3]">Codex-X examples · 默认关闭，不影响现有请求</div>
          </div>
          <Switch
            compact
            label=""
            :enabled="promptInjection.enabled"
            :busy="promptInjectionBusy || !promptInjectionLoaded"
            :disabled="promptInjectionBusy || !promptInjectionLoaded"
            enabled-text="已启用"
            disabled-text="未启用"
            @change="(value) => { promptInjection.enabled = value; void savePromptInjection(); }"
          />
        </div>
        <div v-if="promptInjection.templates.length" class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <Card v-for="template in promptInjection.templates" :key="template.name">
            <div class="flex flex-col gap-3">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <div class="truncate text-sm font-medium text-white">{{ template.name }}</div>
                  <div class="mt-1 text-xs text-[#a3a3a3]">独立启用状态，关闭不会影响其他提示词</div>
                </div>
                <Switch
                  compact
                  label=""
                  :enabled="Boolean(template.enabled)"
                  :busy="promptInjectionBusy || !promptInjectionLoaded"
                  :disabled="promptInjectionBusy || !promptInjectionLoaded"
                  enabled-text="已启用"
                  disabled-text="已关闭"
                  @change="(value) => togglePromptTemplate(template, value)"
                />
              </div>
              <div class="max-h-28 overflow-y-auto whitespace-pre-wrap rounded border border-white/10 bg-black/20 p-2 text-xs text-[#a3a3a3]">
                {{ template.content || "暂无内容" }}
              </div>
              <div class="flex justify-end">
                <Button
                  variant="default"
                  :disabled="!template.content || promptInjectionBusy"
                  @click="openPromptPreview(template)"
                >
                  查看提示词
                </Button>
              </div>
            </div>
          </Card>
        </div>
        <div class="grid grid-cols-2 gap-2">
          <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
            提示词模式
            <Select
              v-model="promptInjection.mode"
              :options="promptInjectionModeOptions"
              aria-label="提示词模式"
              button-class="h-8 text-xs"
              :disabled="promptInjectionBusy"
              @change="savePromptInjection"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
            模板文件名
            <input v-model="promptInjection.selectedTemplate" class="h-8 rounded border border-white/10 bg-black/20 px-2 py-1.5 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" />
          </label>
          <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
            仓库
            <input v-model="promptInjection.repo" class="h-8 rounded border border-white/10 bg-black/20 px-2 py-1.5 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" />
          </label>
          <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
            Ref
            <input v-model="promptInjection.ref" class="h-8 rounded border border-white/10 bg-black/20 px-2 py-1.5 text-xs text-white" :disabled="promptInjectionBusy" @change="savePromptInjection" />
          </label>
        </div>
        <div class="flex items-center justify-between gap-2 text-xs text-[#a3a3a3]">
          <span>{{ promptInjection.cacheAvailable ? `本地提示词已缓存${promptInjection.lastUpdated ? ` · ${promptInjection.lastUpdated}` : ''}` : '尚未缓存提示词' }}</span>
          <div class="center-row gap-2">
            <Button
              variant="default"
              :disabled="!promptInjection.cacheAvailable || promptInjectionBusy || !promptInjectionLoaded"
              @click="openPromptPreview()"
            >
              查看提示词
            </Button>
            <Button variant="default" :disabled="promptInjectionBusy || !promptInjectionLoaded" @click="handleRefreshPromptInjection">拉取最新提示词</Button>
          </div>
        </div>
        <div v-if="promptInjection.lastError" class="rounded border border-[#4b1d1d] bg-[#2a1313] px-2 py-1 text-xs text-[#fca5a5]">
          {{ promptInjection.lastError }}
        </div>
      </div>
    </Card>

    <Card>
      <div class="flex items-start justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">软件使用中文化</h2>
          <div class="mt-1 text-xs text-[#a3a3a3]">
            开启后主要交互和 Agent 回复默认使用简体中文；代码、命令、路径、API、工具调用 JSON/schema 和错误原文保持准确。
          </div>
          <div class="mt-1 text-xs text-[#737373]">与 Codex-X 提示词注入独立，可单独开启；关闭时不改变原请求和原提示词。</div>
        </div>
        <Switch
          compact
          label=""
          :enabled="promptInjection.softwareChineseEnabled"
          :busy="promptInjectionBusy || !promptInjectionLoaded"
          :disabled="promptInjectionBusy || !promptInjectionLoaded"
          enabled-text="已启用"
          disabled-text="未启用"
          @change="(value) => { promptInjection.softwareChineseEnabled = value; void savePromptInjection(); }"
        />
      </div>
    </Card>

    <PromptPreviewModal
      :visible="Boolean(promptPreview)"
      :template-name="promptPreview?.templateName || ''"
      :repo="promptPreview?.repo || ''"
      :ref-name="promptPreview?.refName || ''"
      :content="promptPreview?.content || ''"
      @close="closePromptPreview"
    />

    <Card>
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="text-base font-medium text-white">本地配置</h2>
          <div class="text-sm text-[#a3a3a3]">打开设置目录，或单独管理模型配置</div>
        </div>
        <div class="center-row gap-2">
          <Button variant="default" @click="handleOpenConfig">设置文件夹</Button>
          <Button variant="default" @click="handleOpenRequestMetrics">请求明细</Button>
          <Button variant="primary" @click="handleOpenModelConfig">模型配置</Button>
        </div>
      </div>
    </Card>
  </div>
</template>
