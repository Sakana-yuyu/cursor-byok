import { ref } from "vue";
import { runtimeIsMacOS } from "@/services/runtimeAdapter";

export const isMacOS = ref(runtimeIsMacOS);
