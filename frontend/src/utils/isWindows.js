import { ref } from "vue";
import { isBrowserPreview, runtimeIsWindows } from "@/services/runtimeAdapter";

export const isWindows = ref(isBrowserPreview ? false : runtimeIsWindows);