<script setup>
import PromptPreviewModal from "@/components/PromptPreviewModal.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import Button from "@/components/ui/Button.vue";
import Input from "@/components/ui/Input.vue";
import Select from "@/components/ui/Select.vue";
import Switch from "@/components/ui/Switch.vue";
import { useMessage } from "@/composables/useMessage";
import { useLocale } from "@/i18n/runtime";
import {
  getPromptInjectionSettings,
  refreshPromptInjection,
  refreshPromptInjectionCatalog,
  savePromptInjectionSettings,
} from "@/services/clientApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";

const props = defineProps({
  autosave: {
    type: Object,
    required: true,
  },
});

const PROMPT_SAVE_KEY = "prompts.config";

const message = useMessage();
const { locale: interfaceLocale } = useLocale();

// Git 提交文本本地化语言选项；auto 表示跟随界面语言。
// 值统一使用小写（zh-cn/en-us/ja-jp/ru-ru）：后端 normalizeConfig 会把语言
// 强制小写持久化，若这里用大写（zh-CN），加载时大小写敏感匹配会失败并被重置
// 回 auto——即「选择其他语言无法生效」的根因。
const commitLanguageOptions = [
  { value: "auto", label: "跟随界面语言" },
  { value: "zh-cn", label: "简体中文" },
  { value: "en-us", label: "English" },
  { value: "ja-jp", label: "日本語" },
  { value: "ru-ru", label: "Русский" },
];

// normalizeCommitMessageLanguage 把后端返回的语言值归一化为选项中的值。
// 兼容旧配置可能残留的大写写法（zh-CN/en-US）与后端小写归一化（zh-cn/en-us）：
// 大小写不敏感匹配，匹配不到（未知语言）时回退 auto。
function normalizeCommitMessageLanguage(value) {
  const normalized = String(value || "").trim().toLowerCase();
  const option = commitLanguageOptions.find((item) => String(item.value || "").trim().toLowerCase() === normalized);
  return option ? option.value : "auto";
}

// interfaceLocaleToCode 把界面语言代码归一化为后端可用的语言代码。
function interfaceLocaleToCode(localeValue) {
  const value = String(localeValue || "").trim().toLowerCase();
  return value === "en-us" || value === "ja-jp" || value === "ru-ru" ? value : "zh-cn";
}

const promptPreview = ref(null);
const promptRevision = ref(0);

const prompt = reactive({
  enabled: false,
  softwareChineseEnabled: false,
  commitMessageEnabled: false,
  commitMessageLanguage: "auto",
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
  errorSource: "",
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

const promptControlsDisabled = computed(() => (
  !promptState.loaded || promptState.loading || promptState.refreshing
));

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
  if (prompt.commitMessageEnabled) {
    enabledItems.push("提交本地化");
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
    commitMessageEnabled: Boolean(value.commitMessageEnabled),
    commitMessageLanguage: normalizeCommitMessageLanguage(value.commitMessageLanguage),
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
  // auto 模式把当前界面语言作为已解析值一并持久化，保证后端注入具体语言。
  const resolvedLanguage = prompt.commitMessageLanguage === "auto"
    ? interfaceLocaleToCode(interfaceLocale.value)
    : prompt.commitMessageLanguage;
  return {
    enabled: prompt.enabled,
    softwareChineseEnabled: prompt.softwareChineseEnabled,
    commitMessageEnabled: prompt.commitMessageEnabled,
    commitMessageLanguage: prompt.commitMessageLanguage,
    commitMessageLanguageResolved: resolvedLanguage,
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
  promptState.errorSource = "";
  promptState.retry = null;
  return promptRevision.value;
}

function queuePromptTask(task) {
  const queuedTask = promptSaveTail.catch(() => {}).then(task);
  promptSaveTail = queuedTask.catch(() => {});
  return queuedTask;
}

function clearPromptError() {
  promptState.error = "";
  promptState.errorSource = "";
  promptState.retry = null;
}

async function retryPromptSave(payload, revision) {
  if (revision !== promptRevision.value) {
    return;
  }

  clearPromptError();
  try {
    await props.autosave.run(PROMPT_SAVE_KEY, async () => {
      await persistPromptSettings({ payload, revision });
    });
  } catch (_error) {
    // persistPromptSettings restores the scoped retry and inline error
  }
}

async function persistPromptSettings({ payload = null, revision = null, onFailure = null } = {}) {
  return queuePromptTask(async () => {
    const revisionAtStart = revision ?? promptRevision.value;
    const savePayload = payload || buildPromptConfig();
    try {
      const status = await savePromptInjectionSettings(savePayload);
      if (status?.ok === false) {
        throw new Error(status.error || "保存失败");
      }
      if (revisionAtStart === promptRevision.value) {
        applyPromptStatus(status);
        clearPromptError();
      }
    } catch (error) {
      if (revisionAtStart === promptRevision.value) {
        if (typeof onFailure === "function") {
          onFailure();
        }
        promptState.error = toUserError(error);
        promptState.errorSource = "save";
        promptState.retry = () => retryPromptSave(savePayload, revisionAtStart);
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

async function savePromptImmediately({ payload = null, revision = null, onFailure = null } = {}) {
  try {
    await props.autosave.run(PROMPT_SAVE_KEY, async () => {
      await persistPromptSettings({ payload, revision, onFailure });
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

async function drainPromptSaveQueue() {
  await flushPromptSave();
  await promptSaveTail;
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
  const previousValue = prompt[field];
  prompt[field] = value;
  const revision = markPromptChanged();
  void savePromptImmediately({
    revision,
    onFailure: () => {
      prompt[field] = previousValue;
    },
  });
}

// updateCommitMessageLanguage 更新提交文本语言：
// 「跟随界面语言」时 commitMessageLanguage 保持 auto，已解析值由 buildPromptConfig 写入。
function updateCommitMessageLanguage(value) {
  const selected = String(value || "auto");
  const previousValue = prompt.commitMessageLanguage;
  prompt.commitMessageLanguage = selected;
  const revision = markPromptChanged();
  void savePromptImmediately({
    revision,
    onFailure: () => {
      prompt.commitMessageLanguage = previousValue;
    },
  });
}

// 界面语言切换时，若用户选择「跟随界面语言」，自动把新语言写入提交本地化配置。
watch(interfaceLocale, () => {
  if (prompt.commitMessageLanguage !== "auto" || !prompt.commitMessageEnabled) {
    return;
  }
  const revision = markPromptChanged();
  void savePromptImmediately({ revision });
});

function togglePromptTemplate(templateName, enabled) {
  const previousTemplates = prompt.templates;
  prompt.templates = prompt.templates.map((template) => (
    template.name === templateName
      ? { ...template, enabled: Boolean(enabled) }
      : template
  ));
  const revision = markPromptChanged();
  void savePromptImmediately({
    revision,
    onFailure: () => {
      prompt.templates = previousTemplates;
    },
  });
}

async function loadPromptSettings() {
  promptState.loading = true;
  clearPromptError();
  promptState.retry = loadPromptSettings;
  try {
    applyPromptStatus(await getPromptInjectionSettings());
    clearPromptError();
  } catch (error) {
    promptState.error = toUserError(error);
    promptState.errorSource = "load";
    promptState.retry = loadPromptSettings;
  } finally {
    promptState.loaded = true;
    promptState.loading = false;
  }
}

async function handleRefreshPrompt() {
  if (promptState.errorSource === "save") {
    return;
  }

  clearPromptError();
  promptState.refreshing = true;

  try {
    await drainPromptSaveQueue();
    if (promptState.errorSource === "save") {
      return;
    }

    promptRevision.value += 1;
    const refreshRevision = promptRevision.value;
    try {
      const status = await refreshPromptInjectionCatalog();
      if (status?.ok === false) {
        throw new Error(status.error || "拉取提示词失败");
      }
      if (refreshRevision === promptRevision.value) {
        applyPromptStatus(status);
        clearPromptError();
      }
      message.success("提示词清单已更新");
    } catch (error) {
      const fallbackStatus = await refreshPromptInjection();
      if (fallbackStatus?.ok === false) {
        throw new Error(fallbackStatus.error || toUserError(error));
      }
      if (refreshRevision === promptRevision.value) {
        applyPromptStatus(fallbackStatus);
        clearPromptError();
      }
      message.success("提示词已更新");
    }
  } catch (error) {
    promptState.error = toUserError(error);
    promptState.errorSource = "refresh";
    promptState.retry = handleRefreshPrompt;
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
                :disabled="promptControlsDisabled"
                aria-label="启用提示词注入"
                @change="(value) => updatePromptFieldImmediately('enabled', value)"
              />
            </SettingsRow>

            <SettingsRow label="提示词模式" description="替换原提示词，或追加到受管区块中。">
              <div class="w-[220px] max-w-full">
                <Select
                  :model-value="prompt.mode"
                  :options="promptInjectionModeOptions"
                  :disabled="promptControlsDisabled"
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
                  :disabled="promptControlsDisabled"
                  aria-label="当前模板"
                  @change="(value) => updatePromptFieldImmediately('selectedTemplate', value)"
                />
                <Input
                  v-else
                  :model-value="prompt.selectedTemplate"
                  :disabled="promptControlsDisabled"
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
                  :disabled="promptControlsDisabled"
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
                  :disabled="promptControlsDisabled"
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

          <SettingsSection
            title="模板列表"
            description="单独控制每个模板是否参与注入，并按需预览内容。"
            collapsible
            :default-expanded="false"
          >
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
                    :disabled="promptControlsDisabled"
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

          <SettingsSection
            title="自定义与本地化"
            collapsible
            :default-expanded="false"
          >
            <SettingsRow
              label="自定义注入"
              description="在标准模板之外附加自定义内容。"
            >
              <Switch
                compact
                label=""
                :enabled="prompt.customEnabled"
                :disabled="promptControlsDisabled"
                aria-label="启用自定义注入"
                @change="(value) => updatePromptFieldImmediately('customEnabled', value)"
              />
            </SettingsRow>

            <SettingsRow
              label="Git 提交文本本地化"
              description="按所选语言生成提交信息；选择跟随界面语言时，提交信息语言会随界面语言切换自动同步。"
            >
              <div class="flex items-center gap-3">
                <Switch
                  compact
                  label=""
                  :enabled="prompt.commitMessageEnabled"
                  :disabled="promptControlsDisabled"
                  aria-label="启用 Git 提交文本本地化"
                  @change="(value) => updatePromptFieldImmediately('commitMessageEnabled', value)"
                />
                <div class="w-[170px] max-w-full">
                  <Select
                    :model-value="prompt.commitMessageLanguage"
                    :options="commitLanguageOptions"
                    aria-label="提交信息语言"
                    :disabled="promptControlsDisabled || !prompt.commitMessageEnabled"
                    @change="updateCommitMessageLanguage"
                  />
                </div>
              </div>
            </SettingsRow>

            <SettingsRow
              label="自定义提示词"
              description="支持多行内容；停止输入 500ms 后自动保存，失焦或回车时立即刷新保存队列。"
            >
              <div class="w-full max-w-[520px]">
                <textarea
                  :value="prompt.customContent"
                  :disabled="promptControlsDisabled"
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
