<script setup>
import AccountsPanel from "@/components/control-center/AccountsPanel.vue";
import AgentOpsPanel from "@/components/control-center/AgentOpsPanel.vue";
import ConfigProfilesPanel from "@/components/control-center/ConfigProfilesPanel.vue";
import ControlCenterOverviewBar from "@/components/control-center/ControlCenterOverviewBar.vue";
import RequestLabPanel from "@/components/control-center/RequestLabPanel.vue";
import RoutingPanel from "@/components/control-center/RoutingPanel.vue";
import Button from "@/components/ui/Button.vue";
import { getControlCenterOverview } from "@/services/clientApi";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const TABS = [
  { id: "accounts", label: "账号", description: "多账号管理与厂商余额", icon: "icon-[mdi--account-multiple-outline]" },
  { id: "request-lab", label: "请求实验室", description: "官方镜像 vs 本地结构对比", icon: "icon-[mdi--flask-outline]" },
  { id: "routing", label: "自适应路由", description: "策略、评分与故障切换", icon: "icon-[mdi--routes]" },
  { id: "agents", label: "Agent 运行台", description: "委派任务与运行审计", icon: "icon-[mdi--robot-outline]" },
  { id: "profiles", label: "配置档案", description: "无凭据配置快照", icon: "icon-[mdi--folder-cog-outline]" },
];
const TAB_IDS = TABS.map((item) => item.id);

const route = useRoute();
const router = useRouter();
const overview = ref(null);
const overviewLoading = ref(false);
const overviewError = ref("");

const activeTab = computed(() => {
  const tab = String(route.query.tab || "accounts");
  return TAB_IDS.includes(tab) ? tab : "accounts";
});

const activeMeta = computed(() => TABS.find((item) => item.id === activeTab.value) || TABS[0]);

function selectTab(tab) {
  void router.replace({ path: "/control-center", query: { tab } });
}

function handleBack() {
  void router.replace("/");
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

// 概览加载与面板渲染解耦：概览失败或挂起时各 tab 面板仍可独立加载。
let overviewSeq = 0;
async function loadOverview() {
  const seq = ++overviewSeq;
  overviewLoading.value = true;
  overviewError.value = "";
  try {
    const data = await getControlCenterOverview();
    if (seq === overviewSeq) overview.value = data;
  } catch (error) {
    if (seq !== overviewSeq) return;
    overview.value = null;
    overviewError.value = error?.message || String(error || "加载概览失败");
  } finally {
    if (seq === overviewSeq) overviewLoading.value = false;
  }
}

onMounted(() => {
  void loadOverview();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col px-4 pb-4 text-[#e5e5e5]">
    <header class="mb-4 flex shrink-0 items-start justify-between gap-4">
      <div class="center-row min-w-0 gap-2">
        <span class="icon-[mdi--view-dashboard-outline] text-[20px] text-[#6ee7a5]" aria-hidden="true" />
        <div class="min-w-0">
          <h1 class="text-lg font-semibold text-white">控制中心</h1>
          <p class="text-xs text-[#8a8a8a]">账号、路由、实验与配置的统一管理入口</p>
        </div>
      </div>
      <button
        type="button"
        aria-label="返回"
        title="返回"
        class="flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-[6px] border border-white/10 bg-black/15 px-2 text-sm font-medium text-[#9a9a9a] shadow-[0_1px_2px_rgba(0,0,0,0.2)] transition-colors hover:border-white/15 hover:bg-[#292929] hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#10AD5D]/35"
        @click="handleBack"
      >
        <span class="icon-[mdi--keyboard-return] text-[16px]" aria-hidden="true"></span>
        <span>返回</span>
      </button>
    </header>

    <div class="flex min-h-0 flex-1 flex-col gap-4 lg:flex-row">
      <nav
        class="flex shrink-0 flex-row gap-2 overflow-x-auto pb-1 lg:w-[220px] lg:flex-col lg:overflow-visible lg:pb-0"
        role="tablist"
        aria-label="控制中心模块"
      >
        <button
          v-for="tab in TABS"
          :key="tab.id"
          type="button"
          role="tab"
          class="flex min-w-[140px] flex-col rounded-[8px] border px-3 py-2.5 text-left transition-colors lg:min-w-0"
          :class="activeTab === tab.id ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] bg-[#252525] text-[#a3a3a3] hover:border-[#4a4a4a] hover:text-white'"
          :aria-selected="activeTab === tab.id ? 'true' : 'false'"
          @click="selectTab(tab.id)"
        >
          <span class="center-row gap-2 text-sm font-medium">
            <span :class="tab.icon" class="text-[16px]" aria-hidden="true" />
            {{ tab.label }}
          </span>
          <span class="mt-1 hidden text-[11px] leading-snug opacity-80 lg:block">{{ tab.description }}</span>
        </button>
      </nav>

      <main class="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[8px] border border-[#343434] bg-[#292929]/60 p-4">
        <div class="mb-4 shrink-0 border-b border-[#343434] pb-3">
          <div class="center-row gap-2">
            <span :class="activeMeta.icon" class="text-[18px] text-[#6ee7a5]" aria-hidden="true" />
            <div>
              <h2 class="text-base font-medium text-white">{{ activeMeta.label }}</h2>
              <p class="text-xs text-[#8a8a8a]">{{ activeMeta.description }}</p>
            </div>
          </div>
        </div>

        <ControlCenterOverviewBar :overview="overview" :loading="overviewLoading" :active-tab="activeTab" />
        <div
          v-if="overviewError"
          class="mb-4 flex shrink-0 items-center justify-between gap-2 rounded-[8px] border border-[#5c2b2b] bg-[#2a1515] px-3 py-2 text-xs text-[#fca5a5]"
        >
          <span>概览加载失败：{{ overviewError }}</span>
          <Button variant="text" @click="loadOverview">重试</Button>
        </div>
        <AccountsPanel v-if="activeTab === 'accounts'" />
        <RequestLabPanel v-else-if="activeTab === 'request-lab'" />
        <RoutingPanel v-else-if="activeTab === 'routing'" />
        <AgentOpsPanel v-else-if="activeTab === 'agents'" />
        <ConfigProfilesPanel v-else-if="activeTab === 'profiles'" />
      </main>
    </div>
  </div>
</template>

<style scoped>
nav::-webkit-scrollbar {
  height: 4px;
}
nav::-webkit-scrollbar-thumb {
  background: #444;
  border-radius: 999px;
}
</style>
