<script setup>
import Button from "@/components/ui/Button.vue";
import { applyDiagnosticFixes, diagnoseModelAdapters } from "@/services/clientApi";
import { onMounted, ref } from "vue";

const result = ref({ total: 0, issues: [] });
const loading = ref(false);
const fixing = ref(false);
const error = ref("");

async function scan() {
  loading.value = true;
  error.value = "";
  try { result.value = await diagnoseModelAdapters(); } catch (err) { error.value = String(err); }
  finally { loading.value = false; }
}
async function fixAll() {
  if (!result.value.issues.length) return;
  fixing.value = true;
  error.value = "";
  try { result.value = await applyDiagnosticFixes(result.value.issues.map((issue) => issue.channelId)); }
  catch (err) { error.value = String(err); }
  finally { fixing.value = false; }
}
onMounted(scan);
</script>

<template>
  <main class="mx-auto max-w-4xl px-6 py-8 text-white">
    <div class="mb-6 flex items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">模型协议诊断</h1>
        <p class="mt-1 text-sm text-zinc-400">检查已导入模型的协议配置是否与模型类型匹配。</p>
      </div>
      <div class="flex gap-2">
        <Button variant="default" :disabled="loading" @click="scan">重新扫描</Button>
        <Button variant="primary" :disabled="fixing || !result.issues.length" @click="fixAll">一键修正</Button>
      </div>
    </div>
    <p v-if="error" class="mb-4 text-sm text-red-400">{{ error }}</p>
    <div class="mb-4 text-sm text-zinc-400">已检查 {{ result.total }} 个模型，发现 {{ result.issues.length }} 个问题。</div>
    <div v-if="!result.issues.length && !loading" class="border border-emerald-900 bg-emerald-950/30 p-5 text-emerald-300">未发现协议配置问题。</div>
    <div v-else class="space-y-3">
      <article v-for="issue in result.issues" :key="issue.channelId + issue.modelID" class="border border-zinc-800 bg-zinc-900/60 p-4">
        <div class="flex items-start justify-between gap-4">
          <div><h2 class="font-medium">{{ issue.displayName || issue.modelID }}</h2><p class="mt-1 text-xs text-zinc-500">{{ issue.modelID }} · {{ issue.groupName || "未分组" }}</p></div>
          <span class="text-xs text-amber-300">{{ issue.currentValue }} → {{ issue.suggestedValue }}</span>
        </div>
        <p class="mt-3 text-sm text-zinc-300">{{ issue.message }}</p>
      </article>
    </div>
  </main>
</template>
