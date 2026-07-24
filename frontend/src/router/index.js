import { createRouter, createWebHashHistory, createWebHistory } from "vue-router";
import { isBrowserPreview } from "@/services/runtimeAdapter";
import Home from "@/views/Home.vue";
import ModelConfig from "@/views/ModelConfig.vue";
import ModelEditor from "@/views/ModelEditor.vue";
import ModelGroups from "@/views/ModelGroups.vue";
import RequestMetrics from "@/views/RequestMetrics.vue";
import SupplierDetail from "@/views/SupplierDetail.vue";

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
      path: "/request-metrics",
      component: RequestMetrics,
      meta: { showIcon: false, title: "请求明细", directlyClose: true },
    },

  ],
});

export default router;
