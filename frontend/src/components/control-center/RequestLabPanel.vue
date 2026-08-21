<script setup>
import Button from "@/components/ui/Button.vue";
import { buildRequestComparison, exportSanitizedRequestComparison, listRequestSources } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
const official = ref([]);
const local = ref([]);
const left = ref(null);
const right = ref(null);
const comparison = ref(null);
const loading = ref(false);

async function load() {
  loading.value = true;
  try {
    const [officialPage, localPage] = await Promise.all([
      listRequestSources({ kind: "official_mirror", limit: 50 }),
      listRequestSources({ kind: "local_provider", limit: 50 }),
    ]);
    official.value = officialPage?.items || [];
    local.value = localPage?.items || [];
  } catch (error) {
    message.error(error?.message || "加载请求来源失败");
  } finally {
    loading.value = false;
  }
}

async function compare() {
  if (!left.value || !right.value) {
    message.error("请选择左右两个来源");
    return;
  }
  try {
    comparison.value = await buildRequestComparison({ left: left.value, right: right.value });
  } catch (error) {
    message.error(error?.message || "对比失败");
  }
}

async function exportReport() {
  if (!comparison.value?.id) return;
  try {
    const result = await exportSanitizedRequestComparison(comparison.value.id);
    message.success(`已导出 ${result.path}`);
  } catch (error) {
    message.error(error?.message || "导出失败");
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <div class="grid gap-3 md:grid-cols-2">
      <section class="rounded-[8px] border border-[#343434] bg-[#292929] p-3">
        <div class="mb-2 text-sm text-white">官方镜像</div>
        <button
          v-for="item in official"
          :key="item.ref.id"
          type="button"
          class="mb-1 w-full rounded-[6px] border px-2 py-1.5 text-left text-xs"
          :class="left?.id === item.ref.id ? 'border-[#2f6b49] bg-[#1f3a2c]' : 'border-[#343434]'"
          @click="left = item.ref"
        >
          {{ item.model || "未知模型" }} · {{ item.status || "未标注" }}
        </button>
        <div v-if="!official.length" class="text-xs text-[#8a8a8a]">暂无官方镜像记录。</div>
      </section>
      <section class="rounded-[8px] border border-[#343434] bg-[#292929] p-3">
        <div class="mb-2 text-sm text-white">本地 Provider</div>
        <button
          v-for="item in local"
          :key="item.ref.id"
          type="button"
          class="mb-1 w-full rounded-[6px] border px-2 py-1.5 text-left text-xs"
          :class="right?.id === item.ref.id ? 'border-[#2f6b49] bg-[#1f3a2c]' : 'border-[#343434]'"
          @click="right = item.ref"
        >
          {{ item.model || "未知模型" }} · {{ item.status || "未标注" }}
        </button>
        <div v-if="!local.length" class="text-xs text-[#8a8a8a]">暂无本地 Provider 记录。</div>
      </section>
    </div>
    <div class="flex gap-2">
      <Button variant="primary" :disabled="loading" @click="compare">对比结构</Button>
      <Button :disabled="!comparison" @click="exportReport">导出脱敏报告</Button>
    </div>
    <div v-if="comparison" class="rounded-[8px] border border-[#343434] bg-[#292929] p-3 text-xs">
      <div class="mb-2 text-sm text-white">匹配等级 {{ comparison.matchLevel }}</div>
      <div v-for="section in comparison.sections" :key="section.name" class="mb-2">
        <div class="text-[#a3a3a3]">{{ section.name }}</div>
        <div v-for="diff in section.diffs" :key="diff.path" class="pl-2 text-[#e5e5e5]">
          {{ diff.path }} · {{ diff.leftSummary || "-" }} / {{ diff.rightSummary || "-" }}
        </div>
      </div>
    </div>
  </div>
</template>
