<script setup>
import SettingsPageHeader from "@/components/settings/SettingsPageHeader.vue";
import SettingsSidebar from "@/components/settings/SettingsSidebar.vue";
import { useSettingsAutosave } from "@/composables/useSettingsAutosave";
import {
  SETTINGS_CATEGORIES,
  normalizeSettingsCategory,
  readStoredSettingsCategory,
  readStoredSidebarCollapsed,
  resolveSettingsCategoryComponent,
  writeStoredSettingsCategory,
  writeStoredSidebarCollapsed,
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
const moreExpanded = ref(true);
const sidebarCollapsed = ref(readStoredSidebarCollapsed());

function syncMoreExpanded(categoryID) {
  const category = SETTINGS_CATEGORIES.find((item) => item.id === normalizeSettingsCategory(categoryID));
  if (category?.nav === "more") {
    moreExpanded.value = true;
  }
}

syncMoreExpanded(selectedCategory.value);

watch(selectedCategory, (value) => {
  const normalized = normalizeSettingsCategory(value);
  if (normalized !== value) {
    selectedCategory.value = normalized;
    return;
  }
  writeStoredSettingsCategory(normalized);
  syncMoreExpanded(normalized);

  const routeCategory = settingsCategoryFromRoute(route.query.category);
  if (routeCategory !== normalized) {
    void router.push({
      path: "/settings",
      query: { ...route.query, category: normalized },
    });
  }
});

watch(sidebarCollapsed, (value) => {
  writeStoredSidebarCollapsed(value);
});

watch(() => route.query.category, (value) => {
  const category = settingsCategoryFromRoute(value);
  if (category && category !== selectedCategory.value) {
    selectedCategory.value = category;
  }
});

const activeCategory = computed(() => {
  const categoryID = normalizeSettingsCategory(selectedCategory.value);
  return SETTINGS_CATEGORIES.find((item) => item.id === categoryID) ?? SETTINGS_CATEGORIES[0];
});

const activeCategoryComponent = computed(() =>
  resolveSettingsCategoryComponent(activeCategory.value.id),
);

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
    <div class="grid min-h-0 min-w-0 flex-1 grid-cols-[minmax(0,1fr)] gap-4 px-3 py-4 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-6 sm:px-6 sm:py-5 lg:gap-8 lg:px-8">
      <SettingsSidebar
        v-model="selectedCategory"
        v-model:more-expanded="moreExpanded"
        v-model:collapsed="sidebarCollapsed"
        :categories="SETTINGS_CATEGORIES"
        class="min-w-0 self-start sm:sticky sm:top-0"
      />

      <main class="flex min-h-0 min-w-0 flex-1 flex-col">
        <div class="w-full min-w-0">
          <SettingsPageHeader
            :title="activeCategory.label"
            :description="activeCategory.description"
            :status="autosaveStatus"
            @back="handleBack"
          />
        </div>

        <div
          class="min-h-0 min-w-0 flex-1 overflow-x-hidden overscroll-contain pr-1 sm:pr-2"
          :class="activeCategory.id === 'history' ? 'overflow-y-hidden' : 'overflow-y-auto'"
        >
          <div
            class="flex w-full min-w-0 flex-col gap-6 pt-6 2xl:max-w-[1280px]"
            :class="activeCategory.id === 'history' ? 'h-full min-h-0 pb-0' : 'pb-8'"
          >
            <Transition name="settings-category" mode="out-in">
              <div
                :key="selectedCategory"
                class="settings-category-panel min-w-0"
                :class="activeCategory.id === 'history' ? 'h-full min-h-0' : ''"
              >
                <component
                  :is="activeCategoryComponent"
                  v-if="activeCategoryComponent"
                  :autosave="autosave"
                />
              </div>
            </Transition>
          </div>
        </div>
      </main>
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
