import { createRouter, createWebHashHistory, createWebHistory } from "vue-router";
import { isBrowserPreview } from "@/services/runtimeAdapter";

// 全部视图按路由懒加载：Settings 里的 md-editor、MetricsDetail/StatsOverlay 里的
// echarts 等大依赖只在进入对应页面时才下载解析，显著降低首屏主包体积与启动耗时。
const Home = () => import("@/views/Home.vue");
const ModelConfig = () => import("@/views/ModelConfig.vue");
const ModelEditor = () => import("@/views/ModelEditor.vue");
const ModelCatalog = () => import("@/views/ModelCatalog.vue");
const ModelGroups = () => import("@/views/ModelGroups.vue");
const RequestMetrics = () => import("@/views/RequestMetrics.vue");
const SupplierDetail = () => import("@/views/SupplierDetail.vue");
const MetricsDetail = () => import("@/views/MetricsDetail.vue");
const StatsOverlay = () => import("@/views/StatsOverlay.vue");
const Diagnostics = () => import("@/views/Diagnostics.vue");
const Settings = () => import("@/views/Settings.vue");

const router = createRouter({
  history: isBrowserPreview ? createWebHistory() : createWebHashHistory(),
  routes: [
    {
      path: "/",
      component: Home,
      meta: { showIcon: true, title: "Cursor助手｜永久免费｜自定义API", directlyClose: false },
    },
    {
      path: "/model-config",
      component: ModelConfig,
      meta: { showIcon: false, title: "模型配置", directlyClose: true },
    },
    {
      path: "/model-editor",
      component: ModelEditor,
      meta: { showIcon: false, title: "模型配置", directlyClose: true },
    },
    {
      path: "/model-catalog",
      component: ModelCatalog,
      meta: { showIcon: false, title: "拉取模型", directlyClose: true },
    },
    {
      path: "/model-groups",
      component: ModelGroups,
      meta: { showIcon: false, title: "模型分组", directlyClose: true },
    },
    {
      path: "/supplier",
      component: SupplierDetail,
      meta: { showIcon: false, title: "供应商详情", directlyClose: true },
    },
    {
      path: "/metrics-detail",
      component: MetricsDetail,
      meta: { showIcon: false, title: "会话分析", directlyClose: true },
    },
    {
      path: "/request-metrics",
      component: RequestMetrics,
      meta: { showIcon: false, title: "请求明细", directlyClose: true },
    },
    {
      // 统计浮窗：独立小窗口，App.vue 按 path 分流为纯 router-view（不带 MainLayout）。
      path: "/stats-overlay",
      component: StatsOverlay,
      meta: { showIcon: false, title: "实时统计", directlyClose: true, transparentCanvas: true },
    },
    {
      path: "/diagnostics",
      component: Diagnostics,
      meta: { showIcon: false, title: "诊断", directlyClose: true },
    },
    {
      path: "/settings",
      component: Settings,
      meta: { showIcon: false, title: "设置", directlyClose: false },
    },

  ],
});

router.afterEach((to) => {
  document.documentElement.classList.toggle("stats-overlay-page", to.meta.transparentCanvas === true);
});

export default router;
