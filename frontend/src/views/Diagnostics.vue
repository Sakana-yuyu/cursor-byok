<script setup>
import Button from "@/components/ui/Button.vue";
import { applyDiagnosticFixes, diagnoseModelAdapters } from "@/services/clientApi";
import { exportSessionDebugBundle, listSessionDebugFiles, readSessionDebugTail } from "@/services/runtimeControlApi";
import { toUserError } from "@/state/appState";
import { computed, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

const result = ref({ total: 0, issues: [] });
const loading = ref(false);
const fixing = ref(false);
const error = ref("");
// 是否存在可自动修正（provider_mismatch）的问题：catalog_uncovered 只能提示不能修正
const hasFixableIssues = computed(() =>
  result.value.issues.some((issue) => issue.category === "provider_mismatch"),
);

async function scan() {
  loading.value = true;
  error.value = "";
  try { result.value = await diagnoseModelAdapters(); } catch (err) { error.value = String(err); }
  finally { loading.value = false; }
}
async function fixAll() {
  if (!result.value.issues.length) return;
  // 只修正可自动修正的类别（provider_mismatch）；catalog_uncovered 无法自动修正。
  const fixable = result.value.issues.filter((issue) => issue.category === "provider_mismatch");
  if (!fixable.length) return;
  fixing.value = true;
  error.value = "";
  try { result.value = await applyDiagnosticFixes(fixable.map((issue) => issue.channelId)); }
  catch (err) { error.value = String(err); }
  finally { fixing.value = false; }
}

// --- 会话证据 ---
const route = useRoute();
const sessionID = ref("");
const requestFilter = ref("");
const files = ref([]);
const filesLoading = ref(false);
const filesError = ref("");
const selectedFile = ref("");
const tail = ref("");
const tailLoading = ref(false);
const tailError = ref("");
const exporting = ref(false);
const exportResult = ref("");
const exportError = ref("");

function syncSessionFromRoute() {
  const query = String(route.query.session || "").trim();
  if (query) sessionID.value = query;
}

function resetEvidenceState() {
  files.value = [];
  selectedFile.value = "";
  tail.value = "";
  tailError.value = "";
  exportResult.value = "";
  exportError.value = "";
}

async function loadFiles() {
  if (!sessionID.value.trim()) return;
  filesLoading.value = true;
  filesError.value = "";
  files.value = [];
  selectedFile.value = "";
  tail.value = "";
  tailError.value = "";
  try {
    files.value = await listSessionDebugFiles(sessionID.value.trim());
  } catch (err) {
    filesError.value = toUserError(err);
  } finally {
    filesLoading.value = false;
  }
}

async function loadTail(name) {
  if (!name) return;
  selectedFile.value = name;
  tailLoading.value = true;
  tailError.value = "";
  tail.value = "";
  try {
    tail.value = await readSessionDebugTail(sessionID.value.trim(), name, 0);
  } catch (err) {
    tailError.value = toUserError(err);
  } finally {
    tailLoading.value = false;
  }
}

async function exportBundle() {
  if (!sessionID.value.trim()) return;
  exporting.value = true;
  exportError.value = "";
  exportResult.value = "";
  try {
    const path = await exportSessionDebugBundle(sessionID.value.trim());
    exportResult.value = path;
  } catch (err) {
    exportError.value = toUserError(err);
  } finally {
    exporting.value = false;
  }
}

// 文件是否命中 requestID 过滤：检查尾部内容里是否包含过滤值。
// 后端只暴露尾部内容，这里复用已加载的 tail 做粗筛；未加载尾部时默认展示。
function fileMatchesRequestFilter(name) {
  const filter = requestFilter.value.trim();
  if (!filter) return true;
  if (selectedFile.value === name && tail.value) {
    return tail.value.includes(filter);
  }
  return true;
}

function formatBytes(bytes) {
  const value = Number(bytes || 0);
  if (value <= 0) return "0 B";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`;
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function formatModTime(unixMs) {
  const value = Number(unixMs || 0);
  if (value <= 0) return "";
  const date = new Date(value);
  const pad = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

watch(sessionID, () => {
  resetEvidenceState();
});

onMounted(() => {
  syncSessionFromRoute();
  void scan();
  if (sessionID.value.trim()) void loadFiles();
});
</script>

<template>
  <main class="mx-auto max-w-4xl px-6 py-8 text-white">
    <div class="mb-6">
      <h1 class="text-xl font-semibold">诊断</h1>
      <p class="mt-1 text-sm text-zinc-400">模型协议诊断与会话排查证据。</p>
    </div>

    <!-- 会话证据 -->
    <section class="mb-8 rounded-[10px] border border-white/10 bg-black/20 p-5">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="text-sm font-medium">会话证据</h2>
          <p class="mt-1 text-xs text-[#858585]">按会话定位调试日志，查看尾部内容并导出证据包。</p>
        </div>
        <div class="flex items-center gap-2">
          <Button variant="default" :disabled="filesLoading" @click="loadFiles">
            {{ filesLoading ? "加载中..." : "列出调试日志" }}
          </Button>
          <Button variant="primary" :disabled="exporting || !sessionID.trim()" @click="exportBundle">
            {{ exporting ? "导出中..." : "导出证据包" }}
          </Button>
        </div>
      </div>

      <div class="mb-3 flex flex-wrap items-center gap-2">
        <input
          v-model="sessionID"
          type="text"
          placeholder="会话 ID（UUID）"
          class="min-w-0 flex-1 rounded-[6px] border border-white/10 bg-black/30 px-3 py-1.5 text-sm text-white outline-none focus:border-[#10AD5D]/60"
          @keydown.enter="loadFiles"
        />
        <input
          v-model="requestFilter"
          type="text"
          placeholder="按 requestID 过滤"
          class="min-w-0 w-40 rounded-[6px] border border-white/10 bg-black/30 px-3 py-1.5 text-sm text-white outline-none focus:border-[#10AD5D]/60"
        />
      </div>

      <div v-if="filesError" class="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
        <span class="min-w-0">{{ filesError }}</span>
        <Button variant="default" :disabled="filesLoading" @click="loadFiles">
          {{ filesLoading ? "重试中..." : "重试" }}
        </Button>
      </div>

      <div v-if="exportError" class="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
        <span class="min-w-0">{{ exportError }}</span>
        <Button variant="default" :disabled="exporting" @click="exportBundle">
          {{ exporting ? "重试中..." : "重试" }}
        </Button>
      </div>

      <div v-if="exportResult" class="mb-3 rounded-[8px] border border-emerald-900 bg-emerald-950/30 px-3 py-2 text-sm text-emerald-300">
        证据包已导出：{{ exportResult }}
      </div>

      <div v-if="filesLoading" class="py-4 text-center text-sm text-[#858585]">加载调试日志...</div>
      <div v-else-if="!filesError && files.length === 0 && sessionID.trim()" class="rounded-[8px] border border-white/10 bg-black/15 px-4 py-6 text-center text-sm text-[#858585]">
        该会话没有调试日志
      </div>
      <div v-else-if="files.length > 0" class="space-y-2">
        <div
          v-for="file in files.filter((f) => fileMatchesRequestFilter(f.name))"
          :key="file.name"
          class="rounded-[6px] border bg-black/15 px-3 py-2"
          :class="selectedFile === file.name ? 'border-[#10AD5D]/50' : 'border-white/10'"
        >
          <div class="flex items-center justify-between gap-3">
            <button type="button" class="min-w-0 flex-1 text-left text-sm text-white hover:underline" @click="loadTail(file.name)">
              {{ file.name }}
            </button>
            <span class="shrink-0 text-[11px] tabular-nums text-[#858585]">{{ formatBytes(file.sizeBytes) }} · {{ formatModTime(file.modTimeUnixMs) }}</span>
          </div>
          <div v-if="selectedFile === file.name" class="mt-2">
            <div v-if="tailLoading" class="text-xs text-[#858585]">读取尾部...</div>
            <div v-else-if="tailError" class="flex flex-wrap items-center justify-between gap-2 rounded-[6px] border border-[#4b1d1d] bg-[#2a1313] px-2 py-1.5 text-xs text-[#fca5a5]">
              <span class="min-w-0">{{ tailError }}</span>
              <Button variant="default" :disabled="tailLoading" @click="loadTail(file.name)">
                {{ tailLoading ? "重试中..." : "重试" }}
              </Button>
            </div>
            <pre v-else class="max-h-72 overflow-auto rounded-[6px] bg-black/40 p-2 text-[11px] leading-relaxed text-zinc-300">{{ tail || "(空)" }}</pre>
          </div>
        </div>
      </div>
    </section>

    <!-- 模型协议诊断 -->
    <section>
      <div class="mb-4 flex items-center justify-between gap-4">
        <div>
          <h2 class="text-sm font-medium">模型协议诊断</h2>
          <p class="mt-1 text-xs text-zinc-400">检查已导入模型的协议配置与能力目录覆盖情况。</p>
        </div>
        <div class="flex gap-2">
          <Button variant="default" :disabled="loading" @click="scan">重新扫描</Button>
          <Button variant="primary" :disabled="fixing || !hasFixableIssues" @click="fixAll">一键修正协议</Button>
        </div>
      </div>
      <div v-if="error" class="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-[8px] border border-[#4b1d1d] bg-[#2a1313] px-3 py-2 text-sm text-[#fca5a5]">
        <span class="min-w-0">{{ error }}</span>
        <Button variant="default" :disabled="loading" @click="scan">
          {{ loading ? "重试中..." : "重试" }}
        </Button>
      </div>
      <div class="mb-4 text-sm text-zinc-400">已检查 {{ result.total }} 个模型，发现 {{ result.issues.length }} 个问题。</div>
      <div v-if="!result.issues.length && !loading" class="border border-emerald-900 bg-emerald-950/30 p-5 text-emerald-300">未发现问题。</div>
      <div v-else class="space-y-3">
        <article v-for="issue in result.issues" :key="issue.channelId + issue.modelID + issue.category" class="border p-4" :class="issue.category === 'catalog_uncovered' ? 'border-amber-900/50 bg-amber-950/20' : 'border-zinc-800 bg-zinc-900/60'">
          <div class="flex items-start justify-between gap-4">
            <div><h3 class="font-medium">{{ issue.displayName || issue.modelID }}</h3><p class="mt-1 text-xs text-zinc-500">{{ issue.modelID }} · {{ issue.groupName || "未分组" }}</p></div>
            <span v-if="issue.category === 'catalog_uncovered'" class="shrink-0 text-xs text-amber-300">目录未覆盖</span>
            <span v-else class="shrink-0 text-xs text-amber-300">{{ issue.currentValue }} -> {{ issue.suggestedValue }}</span>
          </div>
          <p class="mt-3 text-sm text-zinc-300">{{ issue.message }}</p>
        </article>
      </div>
    </section>
  </main>
</template>
