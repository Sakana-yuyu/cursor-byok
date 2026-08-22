<script setup>
const props = defineProps({
  overview: { type: Object, default: null },
  activeTab: { type: String, default: "accounts" },
  loading: { type: Boolean, default: false },
});

const ITEMS = [
  { id: "accounts", label: "账号", key: "accounts", icon: "icon-[mdi--account-multiple-outline]" },
  { id: "request-lab", label: "请求实验室", key: "requestLab", icon: "icon-[mdi--flask-outline]" },
  { id: "routing", label: "自适应路由", key: "routing", icon: "icon-[mdi--routes]" },
  { id: "agents", label: "Agent", key: "agents", icon: "icon-[mdi--robot-outline]" },
  { id: "profiles", label: "配置档案", key: "profiles", icon: "icon-[mdi--folder-cog-outline]" },
];

function domain(item) {
  return props.overview?.[item.key] || null;
}

function statusLabel(state) {
  if (props.loading) return "加载中";
  if (state === "ready") return "就绪";
  if (state === "empty") return "暂无";
  if (state === "warning") return "需关注";
  if (state === "error") return "异常";
  return "—";
}

function statusClass(state, active) {
  if (active) return "border-[#2f6b49] bg-[#1f3a2c] text-white";
  if (state === "error") return "border-[#5c2b2b] bg-[#2a1515] text-[#fca5a5]";
  if (state === "warning") return "border-[#5c4a1f] bg-[#2a2415] text-[#fcd34d]";
  return "border-[#343434] bg-[#252525] text-[#a3a3a3]";
}
</script>

<template>
  <div class="mb-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
    <div
      v-for="item in ITEMS"
      :key="item.id"
      class="rounded-[8px] border px-3 py-2 transition-colors"
      :class="statusClass(domain(item)?.state, activeTab === item.id)"
    >
      <div class="center-row gap-1.5 text-[11px]">
        <span :class="item.icon" class="text-[14px]" aria-hidden="true" />
        <span>{{ item.label }}</span>
      </div>
      <div class="mt-1 flex items-baseline justify-between gap-2">
        <span class="text-lg font-semibold tabular-nums text-white">{{ props.loading ? "…" : (domain(item)?.count ?? "—") }}</span>
        <span class="text-[10px] uppercase tracking-wide opacity-80">{{ statusLabel(domain(item)?.state) }}</span>
      </div>
    </div>
  </div>
</template>
