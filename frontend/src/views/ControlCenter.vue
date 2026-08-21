<script setup>
import AgentOpsPanel from "@/components/control-center/AgentOpsPanel.vue";
import ConfigProfilesPanel from "@/components/control-center/ConfigProfilesPanel.vue";
import CursorAccountCard from "@/components/CursorAccountCard.vue";
import RequestLabPanel from "@/components/control-center/RequestLabPanel.vue";
import RoutingPanel from "@/components/control-center/RoutingPanel.vue";
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

function domainCount(tab) {
  if (tab === "accounts") return overview.value?.accounts?.count;
  if (tab === "request-lab") return overview.value?.requestLab?.count;
  if (tab === "agents") return overview.value?.agents?.count;
  if (tab === "profiles") return overview.value?.profiles?.count;
  return 0;
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
        <span v-if="domainCount(tab.id)" class="ml-1 text-[11px] text-[#8a8a8a]">{{ domainCount(tab.id) }}</span>
      </button>
    </div>
    <div v-if="activeTab === 'accounts'" class="min-h-0 flex-1 overflow-y-auto">
      <CursorAccountCard :show-control-center-link="false" />
    </div>
    <RequestLabPanel v-else-if="activeTab === 'request-lab'" />
    <RoutingPanel v-else-if="activeTab === 'routing'" />
    <AgentOpsPanel v-else-if="activeTab === 'agents'" />
    <ConfigProfilesPanel v-else-if="activeTab === 'profiles'" />
  </div>
</template>
