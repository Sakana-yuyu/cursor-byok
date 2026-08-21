<script setup>
import CursorAccountCard from "@/components/CursorAccountCard.vue";
import { getControlCenterOverview } from "@/services/clientApi";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const TABS = [
  { id: "accounts", label: "账号" },
  { id: "request-lab", label: "请求实验室" },
  { id: "routing", label: "自适应路由" },
  { id: "agents", label: "Agent 运行台" },
  { id: "profiles", label: "配置档案" },
];
const TAB_IDS = TABS.map((item) => item.id);

const route = useRoute();
const router = useRouter();
const overview = ref(null);

const activeTab = computed(() => {
  const tab = String(route.query.tab || "accounts");
  return TAB_IDS.includes(tab) ? tab : "accounts";
});

function selectTab(tab) {
  void router.replace({ path: "/control-center", query: { tab } });
}

watch(
  () => route.query.tab,
  (tab) => {
    const value = String(tab || "");
    if (!TAB_IDS.includes(value)) {
      void router.replace({ path: "/control-center", query: { tab: "accounts" } });
    }
  },
  { immediate: true },
);

onMounted(() => {
  void getControlCenterOverview()
    .then((value) => {
      overview.value = value;
    })
    .catch(() => {
      overview.value = null;
    });
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col px-4 pb-4 text-[#e5e5e5]">
    <div class="mb-4 flex flex-wrap gap-2">
      <button
        v-for="tab in TABS"
        :key="tab.id"
        type="button"
        role="tab"
        class="rounded-[7px] border px-3 py-1.5 text-sm transition-colors"
        :class="activeTab === tab.id ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] bg-[#252525] text-[#a3a3a3] hover:text-white'"
        :aria-selected="activeTab === tab.id ? 'true' : 'false'"
        @click="selectTab(tab.id)"
      >
        {{ tab.label }}
        <span v-if="tab.id === 'accounts' && overview?.accounts?.count" class="ml-1 text-[11px] text-[#8a8a8a]">{{ overview.accounts.count }}</span>
      </button>
    </div>
    <div v-if="activeTab === 'accounts'" class="min-h-0 flex-1 overflow-y-auto">
      <CursorAccountCard :show-control-center-link="false" />
    </div>
    <div v-else class="rounded-[8px] border border-[#343434] bg-[#292929] px-4 py-6 text-sm text-[#a3a3a3]">
      {{ TABS.find((item) => item.id === activeTab)?.label || "该功能" }}暂不可用。
    </div>
  </div>
</template>
