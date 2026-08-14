import globals from "globals";
import pluginVue from "eslint-plugin-vue";
import vueParser from "vue-eslint-parser";

export default [
  {
    ignores: ["**/node_modules/**", "**/dist/**", "**/i18n/generated/**"],
  },
  ...pluginVue.configs["flat/recommended"],
  {
    rules: {
      // 存量单字 UI 组件名与 computed 副作用：降为 warn，避免 recommended 阻断 lint。
      "vue/multi-word-component-names": "warn",
      "vue/no-side-effects-in-computed-properties": "warn",
    },
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
      "no-unused-vars": ["warn", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
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
      "no-unused-vars": ["warn", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      "vue/no-unused-vars": ["warn", { ignorePattern: "^_" }],
    },
  },
];
