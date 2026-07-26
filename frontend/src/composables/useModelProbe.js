// useModelProbe.js 管理一批 catalog 模型的可用性探测状态。
//
// results[key] = { status: 'checking' | 'ok' | 'fail', message }
// 探测通过后端轻量 ProbeModelAdapter 完成，并发上限默认 3，避免打满上游。
import { reactive, ref } from "vue";
import { probeModelAdapter } from "@/services/clientApi";
import { humanizeProviderError } from "@/utils/errorHumanizer";

export function useModelProbe() {
  const results = reactive({});
  const probing = ref(false);

  function statusOf(key) {
    return results[key]?.status || "";
  }
  function messageOf(key) {
    return results[key]?.message || "";
  }
  function reset() {
    for (const key of Object.keys(results)) {
      delete results[key];
    }
  }

  /**
   * 逐个探测 items 的可用性。
   * @param {Array<{id:string}>} items 每项必须带唯一 id（作为结果 key）。
   * @param {(item:any)=>object} buildAdapter 由 item 构造探测用的 adapter 配置。
   * @param {{concurrency?:number}} options
   */
  async function probeAll(items, buildAdapter, options = {}) {
    if (probing.value) return;
    const list = (Array.isArray(items) ? items : []).filter((item) => item && item.id);
    if (!list.length) return;

    const concurrency = Math.min(Math.max(1, options.concurrency || 3), list.length);
    probing.value = true;
    list.forEach((item) => {
      results[item.id] = { status: "checking", message: "" };
    });
    try {
      const queue = [...list];
      const workers = Array.from({ length: concurrency }, async () => {
        while (queue.length > 0) {
          const item = queue.shift();
          if (!item) break;
          try {
            const res = await probeModelAdapter(buildAdapter(item));
            const ok = Boolean(res?.ok);
            results[item.id] = {
              status: ok ? "ok" : "fail",
              message: ok ? "" : (res?.message || "模型不可用"),
            };
          } catch (error) {
            results[item.id] = { status: "fail", message: humanizeProviderError(error) };
          }
        }
      });
      await Promise.allSettled(workers);
    } finally {
      probing.value = false;
    }
  }

  return { results, probing, statusOf, messageOf, reset, probeAll };
}
