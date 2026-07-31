<script setup>
import SettingsPageHeader from "@/components/settings/SettingsPageHeader.vue";
import AdvancedSettings from "@/components/settings/categories/AdvancedSettings.vue";
import CursorServiceSettings from "@/components/settings/categories/CursorServiceSettings.vue";
import GeneralSettings from "@/components/settings/categories/GeneralSettings.vue";
import OverlaySettings from "@/components/settings/categories/OverlaySettings.vue";
import SettingsRow from "@/components/settings/SettingsRow.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";
import SettingsSidebar from "@/components/settings/SettingsSidebar.vue";
import { useSettingsAutosave } from "@/composables/useSettingsAutosave";
import {
  SETTINGS_CATEGORIES,
  normalizeSettingsCategory,
  readStoredSettingsCategory,
  writeStoredSettingsCategory,
} from "@/components/settings/settingsCategories";
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

const categoryComponents = {
  general: GeneralSettings,
  "cursor-service": CursorServiceSettings,
  overlay: OverlaySettings,
  advanced: AdvancedSettings,
};

const placeholderCategoryContent = {
  delegation: [
    {
      title: "迁移计划",
      rows: [
        {
          label: "任务委托",
          description: "这里将承载委托开关、运行时信息和相关说明。",
          value: "后续任务继续完善",
        },
      ],
    },
  ],
  "skills-mcp": [
    {
      title: "迁移计划",
      rows: [
        {
          label: "扫描与开关",
          description: "技能列表、MCP server 列表和重扫操作会迁入这里。",
          value: "后续任务继续完善",
        },
      ],
    },
  ],
  prompts: [
    {
      title: "迁移计划",
      rows: [
        {
          label: "提示词注入",
          description: "模板、刷新和自定义内容将在这里提供独立编辑体验。",
          value: "后续任务继续完善",
        },
      ],
    },
  ],
};

const activeCategory = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return SETTINGS_CATEGORIES.find((item) => item.id === categoryID) ?? SETTINGS_CATEGORIES[0];
});

const activeCategoryComponent = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return categoryComponents[categoryID] ?? null;
});

const activePlaceholderSections = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return placeholderCategoryContent[categoryID] ?? [];
});

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
              :title="activeCategory.label"
              :description="activeCategory.description"
              :status="autosave.status"
              @back="handleBack"
            />

            <Transition name="settings-category" mode="out-in">
              <div :key="selectedCategory" class="settings-category-panel min-w-0">
                <component
                  :is="activeCategoryComponent"
                  v-if="activeCategoryComponent"
                  :autosave="autosave"
                />

                <div v-else class="space-y-8">
                  <SettingsSection
                    v-for="section in activePlaceholderSections"
                    :key="section.title"
                    :title="section.title"
                    :description="section.description"
                  >
                    <SettingsRow
                      v-for="row in section.rows"
                      :key="row.label"
                      :label="row.label"
                      :description="row.description"
                    ><div class="text-sm text-[#8f8f8f]">
                        {{ row.value }}
                      </div></SettingsRow>
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
