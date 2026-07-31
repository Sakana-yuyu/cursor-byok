<script setup>
import LocaleSelect from "@/components/LocaleSelect.vue";
import SettingsPageHeader from "@/components/settings/SettingsPageHeader.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import SettingsSidebar from "@/components/settings/SettingsSidebar.vue";
import Button from "@/components/ui/Button.vue";
import { useSettingsAutosave } from "@/composables/useSettingsAutosave";
import {
  SETTINGS_CATEGORIES,
  normalizeSettingsCategory,
  readStoredSettingsCategory,
  writeStoredSettingsCategory,
} from "@/components/settings/settingsCategories";
import { openConfigWindow } from "@/state/appState";
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const router = useRouter();
const route = useRoute();
const autosave = useSettingsAutosave();
const selectedCategory = ref(readStoredSettingsCategory());

watch(selectedCategory, (value) => {
  const normalized = normalizeSettingsCategory(value);
  if (normalized !== value) {
    selectedCategory.value = normalized;
    return;
  }
  writeStoredSettingsCategory(normalized);
});

const categoryContent = {
  general: {
    title: "通用",
    description: "工作区基础与常用偏好已经迁入独立页面。",
    sections: [
      {
        title: "工作区",
        description: "这一页先完成路由、导航、状态栏和稳定布局基础。",
        rows: [
          {
            label: "界面语言",
            description: "使用现有语言选择器切换界面语言。",
            kind: "locale",
          },
          {
            label: "设置目录",
            description: "打开本地配置文件所在目录。",
            kind: "button",
            buttonText: "打开",
          },
        ],
      },
    ],
  },
  "cursor-service": {
    title: "Cursor 与服务",
    description: "服务路由与启动相关设置会在后续任务中迁移到这里。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "本地服务与启动",
            description: "这里将承载服务状态、启动路径和主窗口关闭行为。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
  overlay: {
    title: "浮窗",
    description: "浮窗偏好将在新工作区中按分类整理。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "桌面浮窗",
            description: "样式、置顶、贴边和显隐等控制将在这里集中展示。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
  delegation: {
    title: "模型与委派",
    description: "委托设置和运行时面板将拆成更清晰的工作区内容。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "任务委托",
            description: "这里将承载委托开关、运行时信息和相关说明。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
  "skills-mcp": {
    title: "Skills 与 MCP",
    description: "跨工具扫描和开关管理将在这里收拢。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "扫描与开关",
            description: "技能列表、MCP server 列表和重扫操作会迁入这里。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
  prompts: {
    title: "提示词",
    description: "提示词与本地化配置将在新布局里拆成更稳定的编辑区。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "提示词注入",
            description: "模板、刷新和自定义内容将在这里提供独立编辑体验。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
  advanced: {
    title: "高级",
    description: "风险更高或更低频的系统配置会保留在独立分类里。",
    sections: [
      {
        title: "迁移计划",
        rows: [
          {
            label: "高级连接",
            description: "直连模式和其他高级系统设置将在这里集中展示。",
            kind: "placeholder",
            value: "后续任务继续完善",
          },
        ],
      },
    ],
  },
};

const activeCategory = computed(
  () => categoryContent[normalizeSettingsCategory(selectedCategory.value)] ?? categoryContent.general,
);

async function handleOpenConfigWindow() {
  await openConfigWindow();
}

function handleBack() {
  const previousPath = String(window.history.state?.back || "").trim();
  if (
    previousPath
    && previousPath !== route.fullPath
    && previousPath !== "/settings"
    && previousPath !== "/stats-overlay"
  ) {
    router.back();
    return;
  }

  void router.replace("/");
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden bg-[#202020] text-[#e5e5e5]">
    <div class="flex-1 min-h-0 overflow-y-auto overflow-x-hidden">
      <div class="mx-auto flex min-h-full w-full max-w-[1080px] flex-col gap-5 px-4 pb-8 pt-5 sm:flex-row sm:gap-8 sm:px-6">
        <SettingsSidebar
          v-model="selectedCategory"
          :categories="SETTINGS_CATEGORIES"
          class="shrink-0"
        />

        <div class="min-w-0 flex-1">
          <div class="mx-auto flex min-h-full w-full max-w-[820px] flex-col gap-6">
            <SettingsPageHeader
              :title="activeCategory.title"
              :description="activeCategory.description"
              :status="autosave.status"
              @back="handleBack"
            />

            <Transition name="settings-category" mode="out-in">
              <div :key="selectedCategory" class="settings-category-panel min-w-0">
                <div class="space-y-8">
                  <SettingsSection
                    v-for="section in activeCategory.sections"
                    :key="section.title"
                    :title="section.title"
                    :description="section.description"
                  >
                    <SettingsRow
                      v-for="row in section.rows"
                      :key="row.label"
                      :label="row.label"
                      :description="row.description"
                    >
                      <LocaleSelect
                        v-if="row.kind === 'locale'"
                        wrapper-class="w-[220px] max-w-full"
                        aria-label="界面语言"
                      />

                      <Button
                        v-else-if="row.kind === 'button'"
                        variant="default"
                        @click="handleOpenConfigWindow"
                      >
                        {{ row.buttonText }}
                      </Button>

                      <div v-else class="text-sm text-[#8f8f8f]">
                        {{ row.value }}
                      </div>
                    </SettingsRow>
                  </SettingsSection>
                </div>
              </div>
            </Transition>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-category-enter-active,
.settings-category-leave-active {
  transition: opacity 150ms ease, transform 150ms ease;
}

.settings-category-enter-from,
.settings-category-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

@media (prefers-reduced-motion: reduce) {
  .settings-category-enter-active,
  .settings-category-leave-active {
    transition: none;
  }

  .settings-category-enter-from,
  .settings-category-leave-to {
    opacity: 1;
    transform: none;
  }
}
</style>
