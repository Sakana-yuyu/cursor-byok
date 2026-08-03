import globals from "globals";
import vueParser from "vue-eslint-parser";

export default [
  {
    ignores: ["**/node_modules/**", "**/dist/**", "**/i18n/generated/**"],
  },
  {
    files: ["**/*.js", "**/*.mjs", "**/*.cjs"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.node,
        ...globals.es2021,
      },
    },
    rules: {
      "no-undef": "error",
    },
  },
  {
    files: ["**/*.vue"],
    languageOptions: {
      parser: vueParser,
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.node,
        // Vue <script setup> 编译器宏（编译期处理，非运行时全局）
        defineProps: "readonly",
        defineEmits: "readonly",
        defineModel: "readonly",
        defineExpose: "readonly",
        defineOptions: "readonly",
        withDefaults: "readonly",
        useSlots: "readonly",
        useAttrs: "readonly",
      },
    },
    rules: {
      "no-undef": "error",
    },
  },
];