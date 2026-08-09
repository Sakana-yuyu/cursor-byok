import { createApp } from "vue";
import ResizeObserver from "resize-observer-polyfill";
import App from "@/App.vue";
import { installI18nRuntime } from "@/i18n/runtime";
import router from "@/router";
import { bootstrapAppState } from "@/state/appState";
import { startRuntimeHealthSupervisor } from "@/services/runtimeHealth";
import { safeErrorLogAttributes } from "@/utils/errorContract";
import "@/style/global.css";
import "@/style/tailwind.css";

if (typeof window !== "undefined" && typeof window.ResizeObserver === "undefined") {
  window.ResizeObserver = ResizeObserver;
}

function updateFlexGapSupportClass() {
  if (typeof document === "undefined" || !document.body) {
    return;
  }
  const flex = document.createElement("div");
  flex.style.position = "absolute";
  flex.style.visibility = "hidden";
  flex.style.display = "flex";
  flex.style.flexDirection = "column";
  flex.style.rowGap = "1px";
  flex.appendChild(document.createElement("div"));
  flex.appendChild(document.createElement("div"));
  document.body.appendChild(flex);
  document.documentElement.classList.toggle("no-flex-gap", flex.scrollHeight !== 1);
  flex.parentNode?.removeChild(flex);
}

updateFlexGapSupportClass();

const app = createApp(App);
installI18nRuntime(app);
app.use(router);

function reportFrontendDefect(error, operation) {
  const attributes = safeErrorLogAttributes(error, { operation });
  if (attributes.disposition === "canceled") return;
  console.error("[frontend] unhandled error", attributes);
}

app.config.errorHandler = (error, _instance, info) => {
  reportFrontendDefect(error, `vue.${String(info || "render")}`);
};
if (typeof window !== "undefined") {
  window.addEventListener("unhandledrejection", (event) => {
    reportFrontendDefect(event.reason, "window.unhandledrejection");
  });
}

startRuntimeHealthSupervisor();
app.mount("#root");

bootstrapAppState().catch((error) => {
  reportFrontendDefect(error, "bootstrapAppState");
});
