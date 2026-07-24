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
  return {
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      "@bindings": path.resolve(__dirname, "./bindings"),
      ...(isBrowserPreview
        ? {
            "@wailsio/runtime": path.resolve(__dirname, "./src/services/browserRuntimeMock.js"),
            "@bindings/cursor/internal/bridge/proxyservice.js": path.resolve(__dirname, "./src/services/browserBindings.js"),
            "@bindings/cursor/internal/bridge/adservice.js": path.resolve(__dirname, "./src/services/browserBindings.js"),
            "@bindings/cursor/internal/bridge/metricsservice.js": path.resolve(__dirname, "./src/services/browserBindings.js"),
            "@bindings/cursor/internal/bridge/windowservice.js": path.resolve(__dirname, "./src/services/browserBindings.js"),
          }
        : {}),
    },
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
