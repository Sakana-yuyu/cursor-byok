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
