<script setup>
// /supplier 深链薄壳：真正逻辑在 SupplierPanel（ModelConfig 主从布局右栏复用同一组件）。
import { computed, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import SupplierPanel from "@/components/model-config/SupplierPanel.vue";
import { supplierIdentityFromRouteQuery } from "@/utils/supplierGrouping";

const route = useRoute();
const router = useRouter();

const identity = computed(() => supplierIdentityFromRouteQuery(route.query));
const autoExpandEdit = computed(() => route.query.edit === "1");
</script>

<template>
  <div class="h-full min-h-0">
    <SupplierPanel
      :identity="identity"
      :auto-expand-edit="autoExpandEdit"
      @back="router.back()"
    />
  </div>
</template>
