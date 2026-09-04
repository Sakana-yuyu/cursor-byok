<script setup>
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";
import Logo from "@/assets/logo.png";

const props = defineProps({
  collapsed: { type: Boolean, default: false },
});
const emit = defineEmits(["toggle"]);

const route = useRoute();
const router = useRouter();

const navItems = [
  { path: "/", label: "首页", icon: "icon-[mdi--home-outline]" },
  { path: "/model-config", label: "模型", icon: "icon-[mdi--layers-triple-outline]" },
  { path: "/control-center", label: "控制中心", icon: "icon-[mdi--view-dashboard-outline]" },
  { path: "/metrics-detail", label: "会话分析", icon: "icon-[mdi--chart-box-outline]" },
  { path: "/request-metrics", label: "请求日志", icon: "icon-[mdi--text-box-search-outline]" },
  { path: "/diagnostics", label: "诊断", icon: "icon-[mdi--stethoscope]" },
  { path: "/settings", label: "设置", icon: "icon-[mdi--cog-outline]" },
];

// 子路由归属到侧边栏主项，保证深层页面时高亮不丢失
const activePath = computed(() => {
  const path = route.path;
  const owned = (item) => {
    if (path === item.path) return true;
    switch (item.path) {
      case "/model-config":
        return ["/model-editor", "/model-catalog", "/model-groups", "/supplier"].includes(path);
      case "/metrics-detail":
        return false;
      default:
        return false;
    }
  };
  return navItems.find((item) => owned(item))?.path ?? "";
});

function selectItem(item) {
  if (route.path === item.path) return;
  void router.push(item.path);
}
</script>

<template>
  <aside
    class="flex h-full shrink-0 flex-col border-r border-[var(--border-1)] bg-[var(--bg-sidebar)]"
    :class="collapsed ? 'w-[48px]' : 'w-[180px]'"
    style="--wails-draggable: no-drag"
  >
    <div
      class="flex h-[40px] shrink-0 items-center border-b border-[#242424]"
      :class="collapsed ? 'justify-center' : 'gap-2 px-[14px]'"
    >
      <img :src="Logo" class="h-[16px] w-[16px] shrink-0 opacity-90" />
      <span
        v-if="!collapsed"
        class="truncate text-[12px] font-medium leading-none text-[#b0b0b0]"
        style="font-family: var(--font-num);"
      >Cursor助手</span>
    </div>

    <nav class="flex min-h-0 flex-1 flex-col gap-[2px] overflow-y-auto overflow-x-hidden px-[6px] py-[8px]">
      <button
        v-for="item in navItems"
        :key="item.path"
        type="button"
        class="flex h-[32px] shrink-0 items-center rounded-[6px] text-[13px] transition-colors duration-150"
        :class="[
          collapsed ? 'justify-center w-[36px] mx-auto' : 'gap-[10px] px-[10px]',
          activePath === item.path
            ? 'bg-[var(--active-bg)] text-[var(--active-text)]'
            : 'text-[#9a9a9a] hover:bg-[#252525] hover:text-[#e5e5e5]',
        ]"
        :title="collapsed ? item.label : undefined"
        :aria-label="item.label"
        :data-tour-nav="item.path"
        @click="selectItem(item)"
      >
        <span :class="item.icon" class="shrink-0 text-[17px]" aria-hidden="true"></span>
        <span v-if="!collapsed" class="truncate leading-none">{{ item.label }}</span>
      </button>
    </nav>

    <div class="shrink-0 border-t border-[#242424] p-[6px]">
      <button
        type="button"
        class="flex h-[30px] w-full items-center justify-center rounded-[6px] text-[#6f6f6f] transition-colors duration-150 hover:bg-[#252525] hover:text-[#e5e5e5]"
        :aria-label="collapsed ? '展开侧边栏' : '收起侧边栏'"
        :title="collapsed ? '展开侧边栏' : '收起侧边栏'"
        @click="emit('toggle')"
      >
        <span
          class="text-[17px]"
          :class="collapsed ? 'icon-[mdi--chevron-double-right]' : 'icon-[mdi--chevron-double-left]'"
          aria-hidden="true"
        ></span>
      </button>
    </div>
  </aside>
</template>
