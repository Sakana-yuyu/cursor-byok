import vue from "@vitejs/plugin-vue";
import vueJsx from "@vitejs/plugin-vue-jsx";
import wails from "@wailsio/runtime/plugins/vite";
import { codeInspectorPlugin } from "code-inspector-plugin";
import path from "path";
import { defineConfig } from "vite";
import topLevelAwait from "vite-plugin-top-level-await";
import { staticI18nPlugin } from "./plugins/static-i18n-plugin.js";

const isDev = process.env.NODE_ENV === "development";

export default defineConfig(({ mode }) => {
  const isBrowserPreview = mode === "browser-preview" || process.env.VITE_BROWSER_PREVIEW === "true";
  const browserBindings = path.resolve(__dirname, "./src/services/browserBindings.js");
  // browser 预览时不要挂通用 @bindings，否则会抢在具体 mock 别名之前解析到不存在的 bindings 目录
  const alias = isBrowserPreview
    ? [
        { find: "@wailsio/runtime", replacement: path.resolve(__dirname, "./src/services/browserRuntimeMock.js") },
        { find: "@bindings/cursor/internal/bridge/proxyservice.js", replacement: browserBindings },
        { find: "@bindings/cursor/internal/bridge/adservice.js", replacement: browserBindings },
        { find: "@bindings/cursor/internal/bridge/metricsservice.js", replacement: browserBindings },
        { find: "@bindings/cursor/internal/bridge/windowservice.js", replacement: browserBindings },
        { find: "@", replacement: path.resolve(__dirname, "./src") },
      ]
    : [
        { find: "@", replacement: path.resolve(__dirname, "./src") },
        { find: "@bindings", replacement: path.resolve(__dirname, "./bindings") },
      ];
  return {
  resolve: {
    alias,
  },
  build: {
    target: ["es2019", "safari13"],
    cssTarget: "safari13",
    rollupOptions: {
      output: {
        // 按供应商分包：把 echarts / vue 等共享依赖拆出入口 chunk。
        // Markdown 编辑器必须保留在动态导入子图中，否则 Rollup 会把手工 vendor
        // chunk 预载到 index.html，抵消 Skills/MCP 页面里的懒加载边界。
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("echarts")) return "vendor-echarts";
          if (id.includes("md-editor-v3") || id.includes("codemirror") || id.includes("@lezer")) return undefined;
          if (id.includes("marked")) return "vendor-marked";
          if (id.includes("@iconify")) return "vendor-iconify";
          if (id.includes("@wailsio")) return "vendor-wails";
          if (id.includes("vue-router") || id.includes("@vue") || id.includes("vue")) return "vendor-vue";
          if (id.includes("dayjs") || id.includes("@floating-ui") || id.includes("copy-text-to-clipboard") || id.includes("resize-observer-polyfill")) return "vendor-utils";
          return "vendor-misc";
        },
      },
    },
  },
  plugins: [
    isDev &&
      codeInspectorPlugin({
        bundler: "vite",
        editor: "code",
        hotKeys: ["ctrlKey"],
      }),
    !isBrowserPreview && wails("./bindings"),
    topLevelAwait(),
    staticI18nPlugin(),
    vue(),
    vueJsx(),
  ].filter(Boolean),
  };
});
