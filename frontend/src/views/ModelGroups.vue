<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import { useRouter } from "vue-router";
import { appState, openModelEditorWindow, reloadUserConfig } from "@/state/appState";
import { providerLabel } from "@/utils/providerMeta";
import {
  SUPPLIER_GROUP_MODE_NAME,
  groupModelAdaptersAsSuppliers,
  loadSupplierGroupMode,
} from "@/utils/supplierGrouping";
import { computed, onMounted } from "vue";

const router = useRouter();

// 与模型配置页一致的分组偏好（名称 / 连接）
const groups = computed(() => {
  const mode = loadSupplierGroupMode();
  return groupModelAdaptersAsSuppliers(appState.modelAdapters, mode).map((supplier) => {
    let host = supplier.baseURL || "未设置 URL";
    try { host = new URL(host).host || host; } catch { host = String(host).replace(/^https?:\/\//, ""); }
    if (mode === SUPPLIER_GROUP_MODE_NAME) {
      const hosts = [
        ...new Set(
          (supplier.models || []).map((a) => {
            try { return new URL(String(a.baseURL || "").trim()).host; } catch { return String(a.baseURL || "").trim(); }
          }).filter(Boolean),
        ),
      ];
      host = hosts.length <= 1 ? (hosts[0] || host) : `${hosts.length} 个连接`;
    }
    return {
      key: supplier.key,
      name: mode === SUPPLIER_GROUP_MODE_NAME ? (supplier.groupName || "默认分组") : (supplier.groupName || host),
      host,
      adapters: (supplier.models || []).map((adapter) => ({
        adapter,
        index: appState.modelAdapters.indexOf(adapter),
      })),
    };
  });
});

function types(group) {
  return [...new Set(group.adapters.map(({ adapter }) => providerLabel(adapter.type)))].join(" / ");
}

async function openGroup(group) {
  await router.push({ path: "/model-config", query: { group: group.key } });
}

async function edit(index) {
  await openModelEditorWindow(index, appState.modelAdapters[index]);
}

onMounted(() => { void reloadUserConfig({ modelAdaptersOnly: true }); });
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-4 overflow-hidden p-4 pt-0 text-[#e5e5e5]">
    <div class="flex shrink-0 items-center justify-between gap-4">
      <div>
        <h1 class="text-lg font-semibold text-white">模型分组</h1>
        <p class="text-sm text-[#8f8f8f]">按用户命名或服务 URL 汇总现有模型配置，不改变原有渠道。</p>
      </div>
      <div class="center-row gap-2">
        <Button variant="default" @click="router.push('/model-config')">模型配置</Button>
        <Button variant="primary" @click="router.push('/')">返回首页</Button>
      </div>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto pr-1">
      <div v-if="groups.length === 0" class="flex min-h-[220px] items-center justify-center rounded-[8px] border border-dashed border-[#3a3a3a] bg-[#232323] text-sm text-[#a3a3a3]">暂无模型配置</div>
      <div v-else class="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(280px,1fr))]">
        <Card v-for="group in groups" :key="group.key" class="cursor-pointer" @click="openGroup(group)">
          <div class="flex flex-col gap-3">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0"><h2 class="truncate text-base font-medium text-white">{{ group.name }}</h2><p class="truncate text-xs text-[#777]">{{ group.host }}</p></div>
              <span class="shrink-0 rounded-[6px] border border-[#343434] px-2 py-1 text-xs text-[#a3a3a3]">{{ group.adapters.length }} 个模型</span>
            </div>
            <div class="grid grid-cols-2 gap-2 text-xs text-[#a3a3a3]"><div class="rounded-[6px] bg-[#232323] p-2"><div class="text-[#666]">渠道</div><div class="mt-1 text-[#d4d4d4]">{{ types(group) }}</div></div><div class="rounded-[6px] bg-[#232323] p-2"><div class="text-[#666]">轮询</div><div class="mt-1 text-[#d4d4d4]">{{ group.adapters.length > 1 ? '多渠道可轮询' : '单渠道' }}</div></div></div>
            <div class="flex flex-col gap-1 border-t border-[#343434] pt-2">
              <div v-for="item in group.adapters" :key="item.adapter.id || item.index" class="center-row justify-between gap-2 text-sm"><span class="min-w-0 truncate text-[#d4d4d4]">{{ item.adapter.displayName || item.adapter.modelID }}</span><Button variant="text" @click.stop="edit(item.index)">编辑</Button></div>
            </div>
          </div>
        </Card>
      </div>
    </div>
  </div>
</template>