<script setup>
import SettingsPageHeader from "@/components/settings/SettingsPageHeader.vue";
import AdvancedSettings from "@/components/settings/categories/AdvancedSettings.vue";
import CursorServiceSettings from "@/components/settings/categories/CursorServiceSettings.vue";
import DelegationSettings from "@/components/settings/categories/DelegationSettings.vue";
import GeneralSettings from "@/components/settings/categories/GeneralSettings.vue";
import OverlaySettings from "@/components/settings/categories/OverlaySettings.vue";
import PromptSettings from "@/components/settings/categories/PromptSettings.vue";
import SkillsMcpSettings from "@/components/settings/categories/SkillsMcpSettings.vue";
import HistorySettings from "@/components/settings/categories/HistorySettings.vue";
import GoalSettings from "@/components/settings/categories/GoalSettings.vue";
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
const autosaveStatus = autosave.status;

function settingsCategoryFromRoute(value) {
  const candidate = Array.isArray(value) ? value[0] : value;
  return candidate ? normalizeSettingsCategory(String(candidate)) : "";
}

const initialCategory = settingsCategoryFromRoute(route.query.category);
const selectedCategory = ref(initialCategory || readStoredSettingsCategory());

watch(selectedCategory, (value) => {
  const normalized = normalizeSettingsCategory(value);
  if (normalized !== value) {
    selectedCategory.value = normalized;
    return;
  }
  writeStoredSettingsCategory(normalized);
});

watch(() => route.query.category, (value) => {
  const category = settingsCategoryFromRoute(value);
  if (category && category !== selectedCategory.value) {
    selectedCategory.value = category;
  }
});

const categoryComponents = {
  general: GeneralSettings,
  "cursor-service": CursorServiceSettings,
  overlay: OverlaySettings,
  delegation: DelegationSettings,
  goal: GoalSettings,
  "skills-mcp": SkillsMcpSettings,
  prompts: PromptSettings,
  advanced: AdvancedSettings,
  history: HistorySettings,
};

const activeCategory = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return SETTINGS_CATEGORIES.find((item) => item.id === categoryID) ?? SETTINGS_CATEGORIES[0];
});

const activeCategoryComponent = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return categoryComponents[categoryID] ?? null;
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
    <div class="mx-auto flex min-h-0 w-full max-w-[1080px] flex-1 flex-col gap-5 px-4 py-5 sm:flex-row sm:gap-8 sm:px-6">
      <SettingsSidebar
        v-model="selectedCategory"
        :categories="SETTINGS_CATEGORIES"
        class="shrink-0 sm:sticky sm:top-0 sm:self-start"
      />

      <div class="flex min-h-0 min-w-0 flex-1 flex-col">
        <div class="mx-auto flex w-full max-w-[820px] flex-col">
          <SettingsPageHeader
            :title="activeCategory.label"
            :description="activeCategory.description"
            :status="autosaveStatus"
            @back="handleBack"
          />
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain pr-1">
          <div class="mx-auto flex w-full max-w-[820px] flex-col gap-6 pb-8">
            <Transition name="settings-category" mode="out-in">
              <div :key="selectedCategory" class="settings-category-panel min-w-0">
                <component
                  :is="activeCategoryComponent"
                  v-if="activeCategoryComponent"
                  :autosave="autosave"
                />
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
