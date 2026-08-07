<script setup>
import Button from "@/components/ui/Button.vue";
import SettingsSection from "@/components/settings/SettingsSection.vue";

// 子代理角色面板：仅负责列表展示与上报用户意图。
// 行数据由父组件持有（此处以响应式引用透传，行字段编辑仍即时反映），
// 新增/删除/失焦保存与配置规范化、保存后的对账全部由父组件负责。
defineProps({
  rows: {
    type: Array,
    default: () => [],
  },
  disabled: {
    type: Boolean,
    default: false,
  },
});

const emit = defineEmits(["add", "remove", "flush"]);
</script>

<template>
  <SettingsSection
    title="子代理角色"
    description="本地委派（BYOK worker）按 subagent_type 注入的角色约束：自定义片段覆盖内置角色，留空表示对该类型禁用注入；仅影响本地委派路径，Cursor 原生子代理由客户端管理。编辑失焦或增删时自动保存。"
    collapsible
    :default-expanded="false"
  >
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div class="text-sm text-[#8f8f8f]">
        内置角色：<span class="text-[#a3a3a3]">explore</span>（代码库探索）、<span class="text-[#a3a3a3]">generalPurpose</span>（通用编码）、<span class="text-[#a3a3a3]">browserUse</span>（浏览器自动化）——配置后覆盖内置。
      </div>
      <Button variant="default" :disabled="disabled" @click="emit('add')">
        新增角色
      </Button>
    </div>

    <div
      v-if="!rows.length"
      class="rounded-[8px] border border-dashed border-[#444] px-3 py-5 text-sm text-[#858585]"
    >
      尚未配置自定义子代理角色，本地委派使用内置角色。
    </div>

    <div v-else class="space-y-3">
      <div
        v-for="(row, index) in rows"
        :key="index"
        class="rounded-[8px] border border-[#343434] bg-[#232323] p-3"
      >
        <div class="flex items-center justify-between gap-3">
          <label class="w-48 shrink-0 text-xs text-[#a3a3a3]">
            子代理类型
            <input
              v-model="row.subagentType"
              class="mt-1 h-8 w-full rounded-[6px] border border-[#3f3f3f] bg-[#1e1e1e] px-2 text-xs text-white outline-none transition-colors focus:border-[#10AD5D]"
              placeholder="如 explore / generalPurpose"
              @change="emit('flush', index)"
            />
          </label>
          <Button variant="text" class="shrink-0" @click="emit('remove', index)">
            删除
          </Button>
        </div>
        <label class="mt-2 block text-xs text-[#a3a3a3]">
          角色片段（留空 = 禁用注入）
          <textarea
            v-model="row.promptFragment"
            rows="3"
            class="mt-1 w-full resize-y rounded-[6px] border border-[#3f3f3f] bg-[#1e1e1e] px-2 py-1.5 text-xs leading-5 text-white outline-none transition-colors focus:border-[#10AD5D]"
            placeholder="描述该类型子代理的工作方式与约束，将拼接到 Task prompt 之后"
            @change="emit('flush', index)"
          ></textarea>
        </label>
      </div>
    </div>
  </SettingsSection>
</template>