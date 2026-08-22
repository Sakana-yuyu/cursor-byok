<script setup>
import Button from "@/components/ui/Button.vue";
import Card from "@/components/ui/Card.vue";
import ControlCenterSection from "@/components/control-center/ControlCenterSection.vue";
import { deleteConfigProfile, exportConfigProfile, importConfigProfile, listConfigProfiles, prepareConfigProfileApply, executeConfigProfileApply, previewConfigProfile, saveCurrentConfigProfile } from "@/services/clientApi";
import { useMessage } from "@/composables/useMessage";
import { onMounted, ref } from "vue";

const message = useMessage();
const profiles = ref([]);
const loading = ref(true);
const saving = ref(false);
const name = ref("当前配置");
const domains = ref(["models", "routing"]);
const preview = ref(null);
const importInput = ref(null);

const DOMAIN_OPTIONS = [
  { id: "models", label: "模型", icon: "icon-[mdi--brain]" },
  { id: "model_groups", label: "模型组", icon: "icon-[mdi--view-grid-outline]" },
  { id: "routing", label: "路由", icon: "icon-[mdi--routes]" },
  { id: "delegation", label: "委派", icon: "icon-[mdi--account-arrow-right-outline]" },
  { id: "skills_mcp", label: "Skills/MCP", icon: "icon-[mdi--puzzle-outline]" },
  { id: "computer_use", label: "ComputerUse", icon: "icon-[mdi--monitor-eye]" },
  { id: "appearance", label: "界面", icon: "icon-[mdi--palette-outline]" },
];

async function load() {
  loading.value = true;
  try {
    profiles.value = (await listConfigProfiles()) || [];
  } catch (error) {
    message.error(error?.message || "加载档案失败");
  } finally {
    loading.value = false;
  }
}

function toggleDomain(id) {
  if (domains.value.includes(id)) {
    domains.value = domains.value.filter((item) => item !== id);
    return;
  }
  domains.value = [...domains.value, id];
}

async function save() {
  if (!name.value.trim()) {
    message.error("请输入档案名称");
    return;
  }
  saving.value = true;
  try {
    await saveCurrentConfigProfile({ name: name.value.trim(), domains: domains.value });
    message.success("已保存无凭据档案");
    await load();
  } catch (error) {
    message.error(error?.message || "保存失败");
  } finally {
    saving.value = false;
  }
}

async function showPreview(id) {
  try {
    preview.value = await previewConfigProfile(id);
  } catch (error) {
    message.error(error?.message || "预览失败");
  }
}

async function apply(id) {
  try {
    const prepared = await prepareConfigProfileApply(id);
    if (!prepared.preview?.canApply) {
      message.error("凭据绑定缺失或歧义，无法应用");
      return;
    }
    await executeConfigProfileApply(prepared.confirmationToken);
    message.success("档案已应用");
  } catch (error) {
    message.error(error?.message || "应用失败");
  }
}

async function exportProfile(id) {
  try {
    const result = await exportConfigProfile(id);
    message.success(`已导出 ${result.path}`);
  } catch (error) {
    message.error(error?.message || "导出失败");
  }
}

async function remove(id) {
  try {
    await deleteConfigProfile(id);
    if (preview.value?.profile?.id === id) preview.value = null;
    await load();
  } catch (error) {
    message.error(error?.message || "删除失败");
  }
}

function openImportPicker() {
  importInput.value?.click();
}

async function onImportSelected(event) {
  const file = event.target.files?.[0];
  event.target.value = "";
  if (!file) return;
  if (file.size > 1024 * 1024) {
    message.error("JSON 文件不能超过 1 MiB");
    return;
  }
  try {
    const content = await file.text();
    preview.value = await importConfigProfile(content);
    message.success(`已导入档案 ${preview.value?.profile?.name || ""}`.trim());
    await load();
  } catch (error) {
    message.error(error?.message || "导入失败");
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
    <Card>
      <ControlCenterSection title="保存当前配置" description="导出不含 API Key 的配置快照，便于在多环境间迁移。" icon="icon-[mdi--content-save-outline]">
        <label class="flex flex-col gap-1 text-xs text-[#a3a3a3]">
          档案名称
          <input v-model="name" class="rounded-[6px] border border-[#343434] bg-[#1f1f1f] px-2 py-2 text-sm text-white outline-none focus:border-[#10AD5D]" />
        </label>
        <div class="mt-3 flex flex-wrap gap-2">
          <button
            v-for="item in DOMAIN_OPTIONS"
            :key="item.id"
            type="button"
            class="center-row gap-1 rounded-[6px] border px-2.5 py-1.5 text-xs transition-colors"
            :class="domains.includes(item.id) ? 'border-[#2f6b49] bg-[#1f3a2c] text-white' : 'border-[#343434] text-[#a3a3a3] hover:border-[#4a4a4a]'"
            @click="toggleDomain(item.id)"
          >
            <span :class="item.icon" class="text-[13px]" aria-hidden="true" />
            {{ item.label }}
          </button>
        </div>
        <div class="mt-4 center-row flex-wrap gap-2">
          <Button variant="primary" :disabled="saving" @click="save">{{ saving ? "保存中…" : "保存为档案" }}</Button>
          <input ref="importInput" type="file" accept="application/json,.json" class="hidden" @change="onImportSelected" />
          <Button @click="openImportPicker">导入 JSON 档案</Button>
        </div>
      </ControlCenterSection>
    </Card>

    <ControlCenterSection title="已保存档案" description="应用前会预览变更；凭据需在本机重新绑定。" icon="icon-[mdi--folder-multiple-outline]">
      <div v-if="loading" class="py-8 text-center text-sm text-[#8a8a8a]">
        <span class="icon-[mdi--loading] animate-spin text-[20px]" /> 加载档案…
      </div>
      <div v-else-if="!profiles.length" class="rounded-[8px] border border-dashed border-[#3f3f3f] px-3 py-8 text-center text-sm text-[#8a8a8a]">
        还没有配置档案。保存当前配置或导入 JSON 后会显示在这里。
      </div>
      <div v-else class="grid gap-3">
        <Card v-for="profile in profiles" :key="profile.id">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div class="text-sm font-medium text-white">{{ profile.name }}</div>
              <div class="mt-1 text-xs text-[#737373]">
                {{ profile.domains?.length ? profile.domains.join(" · ") : "未标注域" }}
              </div>
            </div>
            <div class="center-row flex-wrap gap-2">
              <Button @click="showPreview(profile.id)">预览</Button>
              <Button variant="primary" @click="apply(profile.id)">应用</Button>
              <Button @click="exportProfile(profile.id)">导出</Button>
              <Button variant="danger" @click="remove(profile.id)">删除</Button>
            </div>
          </div>
        </Card>
      </div>
    </ControlCenterSection>

    <Card v-if="preview">
      <ControlCenterSection title="档案预览" icon="icon-[mdi--file-eye-outline]">
        <div class="grid gap-2 text-sm">
          <div class="center-row justify-between gap-2">
            <span class="text-[#a3a3a3]">可应用</span>
            <span :class="preview.canApply ? 'text-[#6ee7a5]' : 'text-[#fca5a5]'">{{ preview.canApply ? "是" : "否" }}</span>
          </div>
          <div class="center-row justify-between gap-2">
            <span class="text-[#a3a3a3]">变更项</span>
            <span class="text-white">{{ preview.changes?.length || 0 }}</span>
          </div>
        </div>
        <div v-if="preview.changes?.length" class="mt-3 max-h-[200px] overflow-y-auto rounded-[8px] border border-[#343434]">
          <div
            v-for="change in preview.changes"
            :key="`${change.domain}-${change.path}`"
            class="border-b border-[#343434] px-3 py-2 text-xs last:border-b-0"
          >
            <span class="text-[#737373]">{{ change.domain }}</span>
            <span class="mx-1 text-[#525252]">·</span>
            <span class="font-mono text-[#e5e5e5]">{{ change.path }}</span>
          </div>
        </div>
      </ControlCenterSection>
    </Card>
  </div>
</template>
