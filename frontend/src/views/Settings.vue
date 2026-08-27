<script setup>
import SettingsPageHeader from "@/components/settings/SettingsPageHeader.vue";
import SettingsSidebar from "@/components/settings/SettingsSidebar.vue";
import { useSettingsAutosave } from "@/composables/useSettingsAutosave";
import {
  SETTINGS_CATEGORIES,
  normalizeSettingsCategory,
  readStoredSettingsCategory,
  resolveSettingsCategoryComponent,
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

  const routeCategory = settingsCategoryFromRoute(route.query.category);
  if (routeCategory !== normalized) {
    void router.push({
      path: "/settings",
      query: { ...route.query, category: normalized },
    });
  }
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

</script>

<template>
  <div class="flex h-full min-h-0 flex-col overflow-hidden text-[#e5e5e5]">
    <!-- 顶部横排分类 chips（无页内纵向侧栏，避免与全局侧边栏叠层） -->
    <SettingsSidebar
      v-model="selectedCategory"
      :categories="SETTINGS_CATEGORIES"
      class="shrink-0"
    />

    <main class="min-h-0 flex-1 overflow-hidden">
      <div class="mx-auto h-full w-full max-w-[1400px] px-4 py-4 sm:px-6">
        <div class="w-full min-w-0">
          <SettingsPageHeader
            :title="activeCategory.label"
            :description="activeCategory.description"
            :status="autosaveStatus"
          />
        </div>

        <div
          class="min-h-0 min-w-0 overflow-x-hidden overscroll-contain pr-1 sm:pr-2"
          :class="activeCategory.id === 'history' ? 'h-[calc(100%-4.5rem)] overflow-y-hidden' : 'h-[calc(100%-4.5rem)] overflow-y-auto'"
        >
          <div
            class="flex w-full min-w-0 flex-col gap-6 pt-5"
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
      </div>
    </main>
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
