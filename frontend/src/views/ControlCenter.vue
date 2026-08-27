<script setup>
import AccountsPanel from "@/components/control-center/AccountsPanel.vue";
import AgentOpsPanel from "@/components/control-center/AgentOpsPanel.vue";
import ConfigProfilesPanel from "@/components/control-center/ConfigProfilesPanel.vue";
import RequestLabPanel from "@/components/control-center/RequestLabPanel.vue";
import RoutingPanel from "@/components/control-center/RoutingPanel.vue";
import { getControlCenterOverview } from "@/services/clientApi";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const TABS = [
  { id: "accounts", label: "账号", description: "多账号管理与厂商余额", icon: "icon-[mdi--account-multiple-outline]", overviewKey: "accounts" },
  { id: "request-lab", label: "请求实验室", description: "官方镜像 vs 本地结构对比", icon: "icon-[mdi--flask-outline]", overviewKey: "requestLab" },
  { id: "routing", label: "自适应路由", description: "策略、评分与故障切换", icon: "icon-[mdi--routes]", overviewKey: "routing" },
  { id: "agents", label: "Agent 运行台", description: "委派任务与运行审计", icon: "icon-[mdi--robot-outline]", overviewKey: "agents" },
  { id: "profiles", label: "配置档案", description: "无凭据配置快照", icon: "icon-[mdi--folder-cog-outline]", overviewKey: "profiles" },
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

// 概览统计：并入左栏行尾（数量 + 状态点），不再单独渲染一排统计卡。
function overviewDomain(tab) {
  return overview.value?.[tab.overviewKey] || null;
}
function overviewCount(tab) {
  if (overviewLoading.value) return "…";
  const domain = overviewDomain(tab);
  return domain?.count ?? "—";
}
function overviewState(tab) {
  return overviewDomain(tab)?.state || "";
}
function overviewDotClass(tab) {
  switch (overviewState(tab)) {
    case "ready": return "bg-[#10AD5D]";
    case "warning": return "bg-[#fcd34d]";
    case "error": return "bg-[#f87171]";
    default: return "bg-[#4b4b4b]";
  }
}
function overviewTitle(tab) {
  const state = overviewState(tab);
  const label = { ready: "就绪", empty: "暂无", warning: "需关注", error: "异常" }[state] || "";
  return label ? `${tab.description} · ${label}` : tab.description;
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
  <div class="flex h-full min-h-0 flex-col text-[#e5e5e5]">
    <!-- 顶部横排 tab：避免与全局侧边栏形成双层侧边栏；行尾并入概览统计 -->
    <nav
      class="flex shrink-0 flex-wrap items-center gap-1 border-b border-[#242424] px-4 py-2"
      role="tablist"
      aria-label="控制中心模块"
    >
      <button
        v-for="tab in TABS"
        :key="tab.id"
        type="button"
        role="tab"
        class="flex h-[30px] shrink-0 items-center gap-[8px] rounded-full px-3 text-[13px] transition-colors"
        :class="activeTab === tab.id
          ? 'bg-[var(--active-bg)] text-[var(--active-text)]'
          : 'text-[#9a9a9a] hover:bg-[var(--bg-hover)] hover:text-[#e5e5e5]'"
        :aria-selected="activeTab === tab.id ? 'true' : 'false'"
        :title="overviewTitle(tab)"
        @click="selectTab(tab.id)"
      >
        <span
          :class="tab.icon"
          class="shrink-0 text-[16px]"
          aria-hidden="true"
        />
        <span class="whitespace-nowrap leading-none">{{ tab.label }}</span>
        <span class="text-[11px] tabular-nums leading-none text-[#6f6f6f]">{{ overviewCount(tab) }}</span>
        <span
          class="h-[6px] w-[6px] shrink-0 rounded-full"
          :class="overviewDotClass(tab)"
          :title="{ ready: '就绪', empty: '暂无', warning: '需关注', error: '异常' }[overviewState(tab)] || ''"
        />
      </button>

      <button
        v-if="overviewError"
        type="button"
        class="ml-2 rounded-[6px] border border-[#5c2b2b] bg-[#2a1515] px-2 py-1 text-[11px] text-[#fca5a5] transition-colors hover:border-[#7c3b3b]"
        @click="loadOverview"
      >
        概览加载失败，点击重试
      </button>
    </nav>

    <!-- 内容区：限宽居中，避免超宽屏拉伸 -->
    <main class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto flex w-full max-w-[1400px] flex-col p-4">
        <div class="mb-3 shrink-0 border-b border-[#343434] pb-3">
          <div class="center-row gap-2">
            <span
              :class="activeMeta.icon"
              class="text-[18px] text-[#6ee7a5]"
              aria-hidden="true"
            />
            <div>
              <h2 class="text-base font-medium text-white">
                {{ activeMeta.label }}
              </h2>
              <p class="text-xs text-[#8a8a8a]">
                {{ activeMeta.description }}
              </p>
            </div>
          </div>
        </div>

        <AccountsPanel v-if="activeTab === 'accounts'" />
        <RequestLabPanel v-else-if="activeTab === 'request-lab'" />
        <RoutingPanel v-else-if="activeTab === 'routing'" />
        <AgentOpsPanel v-else-if="activeTab === 'agents'" />
        <ConfigProfilesPanel v-else-if="activeTab === 'profiles'" />
      </div>
    </main>
  </div>
</template>
