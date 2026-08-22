<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import { buildRequestComparison, exportSanitizedRequestComparison, listRequestSources } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { computed, onMounted, ref } from "vue";

const message = useMessage();
const official = ref([]);
const local = ref([]);
const left = ref(null);
const right = ref(null);
const comparison = ref(null);
const loading = ref(false);
const comparing = ref(false);

const step = computed(() => {
  if (comparison.value) return 3;
  if (left.value && right.value) return 2;
  if (left.value || right.value) return 1;
  return 0;
});

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
    message.error("请先在左右两侧各选择一个来源");
    return;
  }
  comparing.value = true;
  try {
    comparison.value = await buildRequestComparison({ left: left.value, right: right.value });
  } catch (error) {
    message.error(error?.message || "对比失败");
  } finally {
    comparing.value = false;
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

function sourceLabel(item) {
  return `${item.model || "未知模型"} · ${item.status || "未标注"}`;
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <div class="grid grid-cols-3 gap-2 text-center text-[11px]">
      <div class="rounded-[6px] border px-2 py-1.5" :class="step >= 1 ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] text-[#737373]'">1. 选官方镜像</div>
      <div class="rounded-[6px] border px-2 py-1.5" :class="step >= 2 ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] text-[#737373]'">2. 选本地 Provider</div>
      <div class="rounded-[6px] border px-2 py-1.5" :class="step >= 3 ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] text-[#737373]'">3. 查看结构差异</div>
    </div>

    <div v-if="loading" class="py-10 text-center text-sm text-[#8a8a8a]">
      <span class="icon-[mdi--loading] animate-spin text-[20px]" /> 加载请求来源…
    </div>

    <template v-else>
      <div class="grid min-h-0 gap-3 lg:grid-cols-2">
        <Card>
          <ControlCenterSection title="官方镜像" description="来自 Cursor 官方链路的脱敏请求快照。" icon="icon-[mdi--cloud-outline]">
            <div class="max-h-[240px] overflow-y-auto pr-1">
              <button
                v-for="item in official"
                :key="item.ref.id"
                type="button"
                class="mb-1.5 w-full rounded-[6px] border px-2.5 py-2 text-left text-xs transition-colors"
                :class="left?.id === item.ref.id ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] bg-[#252525]/70 text-[#d4d4d4] hover:border-[#4a4a4a]'"
                @click="left = item.ref; comparison = null"
              >
                {{ sourceLabel(item) }}
              </button>
              <div v-if="!official.length" class="rounded-[8px] border border-dashed border-[#3f3f3f] px-3 py-6 text-center text-xs text-[#8a8a8a]">
                暂无官方镜像记录。开启镜像捕获并产生请求后会出现。
              </div>
            </div>
          </ControlCenterSection>
        </Card>

        <Card>
          <ControlCenterSection title="本地 Provider" description="经过 BYOK 代理转发的脱敏请求快照。" icon="icon-[mdi--server-network]">
            <div class="max-h-[240px] overflow-y-auto pr-1">
              <button
                v-for="item in local"
                :key="item.ref.id"
                type="button"
                class="mb-1.5 w-full rounded-[6px] border px-2.5 py-2 text-left text-xs transition-colors"
                :class="right?.id === item.ref.id ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] bg-[#252525]/70 text-[#d4d4d4] hover:border-[#4a4a4a]'"
                @click="right = item.ref; comparison = null"
              >
                {{ sourceLabel(item) }}
              </button>
              <div v-if="!local.length" class="rounded-[8px] border border-dashed border-[#3f3f3f] px-3 py-6 text-center text-xs text-[#8a8a8a]">
                暂无本地 Provider 记录。
              </div>
            </div>
          </ControlCenterSection>
        </Card>
      </div>

      <div class="center-row flex-wrap gap-2">
        <Button variant="primary" :disabled="comparing || !left || !right" @click="compare">
          {{ comparing ? "对比中…" : "对比结构" }}
        </Button>
        <Button :disabled="!comparison" @click="exportReport">导出脱敏报告</Button>
      </div>

      <Card v-if="comparison">
        <ControlCenterSection title="对比结果" :description="`匹配等级：${comparison.matchLevel || '—'}`" icon="icon-[mdi--compare-horizontal]">
          <div v-if="!comparison.sections?.length" class="text-sm text-[#8a8a8a]">未发现可展示的结构差异。</div>
          <div v-for="section in comparison.sections" :key="section.name" class="mb-3 last:mb-0">
            <div class="mb-1 text-xs font-medium uppercase tracking-wide text-[#737373]">{{ section.name }}</div>
            <div
              v-for="diff in section.diffs"
              :key="diff.path"
              class="mb-1 rounded-[6px] border border-[#343434] bg-[#252525]/70 px-2.5 py-2 text-xs"
            >
              <div class="font-mono text-[#e5e5e5]">{{ diff.path }}</div>
              <div class="mt-1 grid gap-1 sm:grid-cols-2">
                <div class="text-[#fca5a5]">官方：{{ diff.leftSummary || "—" }}</div>
                <div class="text-[#6ee7a5]">本地：{{ diff.rightSummary || "—" }}</div>
              </div>
            </div>
          </div>
        </ControlCenterSection>
      </Card>
    </template>
  </div>
</template>
