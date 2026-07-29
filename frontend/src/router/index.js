import { createRouter, createWebHashHistory, createWebHistory } from "vue-router";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import Home from "@/views/Home.vue";
import ModelConfig from "@/views/ModelConfig.vue";
import ModelEditor from "@/views/ModelEditor.vue";
import ModelCatalog from "@/views/ModelCatalog.vue";
import ModelGroups from "@/views/ModelGroups.vue";
import RequestMetrics from "@/views/RequestMetrics.vue";
import SupplierDetail from "@/views/SupplierDetail.vue";
import MetricsDetail from "@/views/MetricsDetail.vue";
import StatsOverlay from "@/views/StatsOverlay.vue";
import Diagnostics from "@/views/Diagnostics.vue";

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
      meta: { showIcon: false, title: "实时统计", directlyClose: true },
    },
    {
      path: "/diagnostics",
      component: Diagnostics,
      meta: { showIcon: false, title: "模型协议诊断", directlyClose: true },
    },

  ],
});

export default router;