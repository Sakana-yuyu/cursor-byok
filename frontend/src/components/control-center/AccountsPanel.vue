<script setup>
import CursorAccountCard from "@/components/CursorAccountCard.vue";
import VendorBalanceSyncPanel from "@/components/control-center/VendorBalanceSyncPanel.vue";
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto pb-2">
    <div class="grid gap-3 lg:grid-cols-2">
      <div class="rounded-[8px] border border-[#343434] bg-[#252525]/50 px-4 py-3">
        <div class="center-row gap-2 text-sm font-medium text-white">
          <span class="icon-[mdi--shield-account-outline] text-[#6ee7a5]" aria-hidden="true" />
          控制面当前账号
        </div>
        <p class="mt-1.5 text-xs leading-relaxed text-[#8a8a8a]">
          决定 Skills、MCP 与插件使用的 Cursor 身份。切换后不会改动 Cursor 客户端登录态。
        </p>
      </div>
      <div class="rounded-[8px] border border-[#343434] bg-[#252525]/50 px-4 py-3">
        <div class="center-row gap-2 text-sm font-medium text-white">
          <span class="icon-[mdi--application-outline] text-[#6ee7a5]" aria-hidden="true" />
          Cursor 客户端账号
        </div>
        <p class="mt-1.5 text-xs leading-relaxed text-[#8a8a8a]">
          通过「切换到 Cursor」写入本地 state.vscdb 并重启 IDE。操作前会自动备份状态文件。
        </p>
      </div>
    </div>
    <CursorAccountCard variant="panel" :show-control-center-link="false" />
    <VendorBalanceSyncPanel />
  </div>
</template>
