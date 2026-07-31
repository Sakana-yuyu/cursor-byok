<script setup>
import PromptPreviewModal from "@/components/PromptPreviewModal.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import {
  getPromptInjectionSettings,
  refreshPromptInjection,
  refreshPromptInjectionCatalog,
  savePromptInjectionSettings,
} from "@/services/clientApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, reactive, ref } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const PROMPT_SAVE_KEY = "prompts.config";

const message = useMessage();

const promptPreview = ref(null);
const promptRevision = ref(0);

const prompt = reactive({
  enabled: false,
  softwareChineseEnabled: false,
  customEnabled: false,
  customContent: "",
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

const promptState = reactive({
  loaded: false,
  loading: false,
  refreshing: false,
  error: "",
  retry: null,
});

let promptSaveTail = Promise.resolve();

const promptInjectionModeOptions = [
  { value: "replace", label: "替换原提示词" },
  { value: "append", label: "追加受管区块" },
];

const templateOptions = computed(() => prompt.templates.map((template) => ({
  value: template.name,
  label: template.name,
})));

const promptSummary = computed(() => {
  const enabledItems = [];
  if (prompt.enabled) {
    enabledItems.push("Codex-X");
  }
  if (prompt.customEnabled) {
    enabledItems.push("自定义");
  }
  if (prompt.softwareChineseEnabled) {
    enabledItems.push("中文化");
  }
  return enabledItems.length ? `已启用：${enabledItems.join(" / ")}` : "未启用";
});

const cacheSummary = computed(() => {
  if (!prompt.cacheAvailable) {
    return "尚未缓存提示词";
  }
  return prompt.lastUpdated
    ? `本地提示词已缓存 · ${prompt.lastUpdated}`
    : "本地提示词已缓存";
});

function applyPromptStatus(status) {
  const value = status?.config || status || {};
  Object.assign(prompt, {
    enabled: Boolean(value.enabled),
    softwareChineseEnabled: Boolean(value.softwareChineseEnabled),
    customEnabled: Boolean(value.customEnabled),
    customContent: value.customContent || "",
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

function buildPromptConfig() {
  return {
    enabled: prompt.enabled,
    softwareChineseEnabled: prompt.softwareChineseEnabled,
    customEnabled: prompt.customEnabled,
    customContent: prompt.customContent,
    mode: prompt.mode,
    repo: prompt.repo,
    ref: prompt.ref,
    selectedTemplate: prompt.selectedTemplate,
    localContent: prompt.localContent,
    cacheContent: prompt.cacheContent,
    templates: prompt.templates.map((template) => ({
      name: template.name,
      content: template.content,
      enabled: Boolean(template.enabled),
    })),
  };
}

function markPromptChanged() {
  promptRevision.value += 1;
  promptState.error = "";
  return promptRevision.value;
}

function queuePromptTask(task) {
  const queuedTask = promptSaveTail.catch(() => {}).then(task);
  promptSaveTail = queuedTask.catch(() => {});
  return queuedTask;
}

async function persistPromptSettings() {
  return queuePromptTask(async () => {
    const revisionAtStart = promptRevision.value;
    try {
      const status = await savePromptInjectionSettings(buildPromptConfig());
      if (status?.ok === false) {
        throw new Error(status.error || "保存失败");
      }
      if (revisionAtStart === promptRevision.value) {
        applyPromptStatus(status);
        promptState.error = "";
      }
    } catch (error) {
      if (revisionAtStart === promptRevision.value) {
        promptState.error = toUserError(error);
      }
      throw error;
    }
  });
}

function schedulePromptSave() {
  props.autosave.schedule(PROMPT_SAVE_KEY, async () => {
    await persistPromptSettings();
  }, { debounceMs: 500 });
}

async function savePromptImmediately() {
  try {
    await props.autosave.run(PROMPT_SAVE_KEY, async () => {
      await persistPromptSettings();
    });
  } catch (_error) {
    // row-level state is already surfaced inline
  }
}

async function flushPromptSave() {
  try {
    await props.autosave.flush(PROMPT_SAVE_KEY);
  } catch (_error) {
    // error state is already surfaced inline
  }
}

function openPromptPreview(template = null) {
  const content = template?.content || prompt.localContent || prompt.cacheContent || "";
  if (!content.trim()) {
    return;
  }

  promptPreview.value = {
    templateName: template?.name || prompt.selectedTemplate,
    repo: prompt.repo,
    refName: prompt.ref,
    content,
  };
}

function updatePromptField(field, value) {
  prompt[field] = value;
  markPromptChanged();
  schedulePromptSave();
}

function updatePromptFieldImmediately(field, value) {
  prompt[field] = value;
  markPromptChanged();
  void savePromptImmediately();
}

function togglePromptTemplate(templateName, enabled) {
  prompt.templates = prompt.templates.map((template) => (
    template.name === templateName
      ? { ...template, enabled: Boolean(enabled) }
      : template
  ));
  markPromptChanged();
  void savePromptImmediately();
}

async function loadPromptSettings() {
  promptState.loading = true;
  promptState.error = "";
  promptState.retry = loadPromptSettings;
  try {
    applyPromptStatus(await getPromptInjectionSettings());
  } catch (error) {
    promptState.error = toUserError(error);
  } finally {
    promptState.loaded = true;
    promptState.loading = false;
  }
}

async function handleRefreshPrompt() {
  promptState.retry = handleRefreshPrompt;
  promptState.error = "";
  promptState.refreshing = true;

  try {
    await flushPromptSave();
    if (promptState.error) {
      return;
    }

    const status = await refreshPromptInjectionCatalog();
    applyPromptStatus(status);
    message.success("提示词清单已更新");
  } catch (error) {
    try {
      const fallbackStatus = await refreshPromptInjection();
      applyPromptStatus(fallbackStatus);
      message.success("提示词已更新");
    } catch (fallbackError) {
      promptState.error = toUserError(fallbackError || error);
    }
  } finally {
    promptState.refreshing = false;
  }
}

onMounted(() => {
  void loadPromptSettings();
});
</script>

<template>
  <div class="space-y-8">
    <SettingsSection
      title="提示词"
      description="管理注入模板、仓库来源、自定义内容和中文化策略。"
    >
      <div class="space-y-4">
        <div class="rounded-[8px] border border-[#343434] bg-[#252525]/40 px-4 py-3">
          <div class="flex flex-wrap items-center gap-3">
            <div class="min-w-0 flex-1">
              <div class="text-sm font-medium text-white">提示词注入</div>
              <div class="mt-1 text-xs text-[#8f8f8f]">{{ promptSummary }}</div>
            </div>
            <div class="flex items-center gap-2">
              <Button
                v-if="!prompt.templates.length && (prompt.localContent || prompt.cacheContent)"
                variant="default"
                @click="openPromptPreview()"
              >
                查看提示词
              </Button>
              <button
                type="button"
                class="flex h-9 w-9 items-center justify-center rounded-[8px] border border-[#343434] bg-[#202020] text-[#a3a3a3] transition-colors hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35 disabled:cursor-not-allowed disabled:opacity-60"
                :disabled="promptState.refreshing || !promptState.loaded"
                aria-label="刷新提示词"
                title="刷新提示词"
                @click="handleRefreshPrompt"
              >
                <span
                  class="icon-[mdi--refresh] text-[18px]"
                  :class="promptState.refreshing ? 'animate-spin' : ''"
                  aria-hidden="true"
                ></span>
              </button>
            </div>
          </div>

          <div
            v-if="promptState.error"
            class="mt-3 flex flex-wrap items-center gap-3 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-xs text-[#f2a7a7]"
          >
            <span class="min-w-0 flex-1 break-words">{{ promptState.error }}</span>
            <button
              type="button"
              class="shrink-0 text-[#10AD5D] transition-colors hover:text-[#33c476]"
              @click="promptState.retry?.()"
            >
              重试
            </button>
          </div>
        </div>

        <div v-if="!promptState.loaded || promptState.loading" class="rounded-[8px] border border-[#343434] bg-[#252525]/40 px-4 py-6 text-sm text-[#8f8f8f]">
          正在加载提示词设置...
        </div>

        <div v-else class="space-y-8">
          <SettingsSection title="基础设置">
            <SettingsRow
              label="启用注入"
              description="控制是否将当前启用的模板注入到系统提示词。"
            >
              <Switch
                compact
                label=""
                :enabled="prompt.enabled"
                aria-label="启用提示词注入"
                @change="(value) => updatePromptFieldImmediately('enabled', value)"
              />
            </SettingsRow>

            <SettingsRow label="提示词模式" description="替换原提示词，或追加到受管区块中。">
              <div class="w-[220px] max-w-full">
                <Select
                  :model-value="prompt.mode"
                  :options="promptInjectionModeOptions"
                  aria-label="提示词模式"
                  @change="(value) => updatePromptFieldImmediately('mode', value)"
                />
              </div>
            </SettingsRow>

            <SettingsRow
              label="当前模板"
              description="选择默认模板文件；模板列表为空时可手动输入文件名。"
            >
              <div class="w-full max-w-[320px]">
                <Select
                  v-if="templateOptions.length"
                  :model-value="prompt.selectedTemplate"
                  :options="templateOptions"
                  aria-label="当前模板"
                  @change="(value) => updatePromptFieldImmediately('selectedTemplate', value)"
                />
                <Input
                  v-else
                  :model-value="prompt.selectedTemplate"
                  placeholder="输入模板文件名"
                  aria-label="模板文件名"
                  @update:model-value="(value) => updatePromptField('selectedTemplate', value)"
                  @blur="flushPromptSave"
                  @keydown.enter.prevent="flushPromptSave"
                />
              </div>
            </SettingsRow>

            <SettingsRow
              label="仓库"
              description="指定提示词来源仓库，默认使用 Codex-X 示例仓库。"
            >
              <div class="w-full max-w-[360px]">
                <Input
                  :model-value="prompt.repo"
                  placeholder="owner/repo"
                  spellcheck="false"
                  aria-label="提示词仓库"
                  @update:model-value="(value) => updatePromptField('repo', value)"
                  @blur="flushPromptSave"
                  @keydown.enter.prevent="flushPromptSave"
                />
              </div>
            </SettingsRow>

            <SettingsRow label="Ref" description="指定拉取提示词时使用的分支、标签或提交。">
              <div class="w-full max-w-[240px]">
                <Input
                  :model-value="prompt.ref"
                  placeholder="main"
                  spellcheck="false"
                  aria-label="提示词 Ref"
                  @update:model-value="(value) => updatePromptField('ref', value)"
                  @blur="flushPromptSave"
                  @keydown.enter.prevent="flushPromptSave"
                />
              </div>
            </SettingsRow>

            <SettingsRow label="缓存状态" :description="cacheSummary">
              <div class="flex flex-wrap items-center gap-2">
                <Button
                  variant="default"
                  :disabled="!(prompt.localContent || prompt.cacheContent)"
                  @click="openPromptPreview()"
                >
                  查看提示词
                </Button>
                <Button
                  variant="default"
                  :disabled="promptState.refreshing"
                  @click="handleRefreshPrompt"
                >
                  {{ promptState.refreshing ? "刷新中..." : "拉取最新提示词" }}
                </Button>
              </div>
            </SettingsRow>
          </SettingsSection>

          <SettingsSection title="模板列表" description="单独控制每个模板是否参与注入，并按需预览内容。">
            <div
              v-if="prompt.templates.length"
              class="overflow-hidden rounded-[8px] border border-[#343434] bg-[#252525]/40"
            >
              <article
                v-for="template in prompt.templates"
                :key="template.name"
                class="grid gap-4 border-b border-[#343434] px-4 py-4 last:border-b-0 md:grid-cols-[minmax(0,1fr)_auto]"
              >
                <div class="min-w-0 space-y-2">
                  <div class="flex min-w-0 flex-wrap items-center gap-2">
                    <h3 class="min-w-0 text-sm font-medium text-white" :title="template.name">
                      {{ template.name }}
                    </h3>
                    <span class="rounded-full border border-[#3b3b3b] px-2 py-0.5 text-[11px] text-[#8f8f8f]">
                      {{ template.enabled ? "已启用" : "已停用" }}
                    </span>
                  </div>
                  <p class="line-clamp-3 whitespace-pre-wrap break-words text-xs leading-5 text-[#8f8f8f]">
                    {{ template.content || "暂无内容" }}
                  </p>
                </div>

                <div class="flex flex-wrap items-start justify-start gap-2 md:justify-end">
                  <Button
                    variant="default"
                    :disabled="!template.content"
                    @click="openPromptPreview(template)"
                  >
                    查看提示词
                  </Button>
                  <Switch
                    compact
                    label=""
                    :enabled="Boolean(template.enabled)"
                    :aria-label="`切换模板 ${template.name}`"
                    @change="(value) => togglePromptTemplate(template.name, value)"
                  />
                </div>
              </article>
            </div>

            <div
              v-else
              class="rounded-[8px] border border-[#343434] bg-[#252525]/40 px-4 py-6 text-sm text-[#8f8f8f]"
            >
              当前没有可用模板。你仍然可以手动输入模板文件名并刷新。
            </div>
          </SettingsSection>

          <SettingsSection title="自定义与本地化">
            <SettingsRow
              label="自定义注入"
              description="在标准模板之外附加自定义内容。"
            >
              <Switch
                compact
                label=""
                :enabled="prompt.customEnabled"
                aria-label="启用自定义注入"
                @change="(value) => updatePromptFieldImmediately('customEnabled', value)"
              />
            </SettingsRow>

            <SettingsRow
              label="软件使用中文化"
              description="默认使用中文回答，并保留代码、命令和协议字段原文。"
            >
              <Switch
                compact
                label=""
                :enabled="prompt.softwareChineseEnabled"
                aria-label="启用软件中文化"
                @change="(value) => updatePromptFieldImmediately('softwareChineseEnabled', value)"
              />
            </SettingsRow>

            <SettingsRow
              label="自定义提示词"
              description="支持多行内容；停止输入 500ms 后自动保存，失焦或回车时立即刷新保存队列。"
            >
              <div class="w-full max-w-[520px]">
                <textarea
                  :value="prompt.customContent"
                  class="min-h-[120px] w-full resize-y rounded-[6px] border border-[#3f3f3f] bg-[#232323] px-3 py-2 text-sm text-[#e5e5e5] outline-none transition-colors focus:border-[#10AD5D]"
                  placeholder="输入自定义注入内容，留空则不注入..."
                  @input="(event) => updatePromptField('customContent', event?.target?.value || '')"
                  @blur="flushPromptSave"
                  @keyup.enter="flushPromptSave"
                ></textarea>
              </div>
            </SettingsRow>
          </SettingsSection>
        </div>
      </div>
    </SettingsSection>

    <PromptPreviewModal
      :visible="Boolean(promptPreview)"
      :template-name="promptPreview?.templateName || ''"
      :repo="promptPreview?.repo || ''"
      :ref-name="promptPreview?.refName || ''"
      :content="promptPreview?.content || ''"
      @close="promptPreview = null"
    />
  </div>
</template>
