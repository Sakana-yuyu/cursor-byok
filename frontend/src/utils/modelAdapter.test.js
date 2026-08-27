import assert from "node:assert/strict";
import test from "node:test";

import {
  formatCompactModelAdapterTestSummary,
  formatModelAdapterTestSummary,
  normalizeModelAdapterTestResult,
} from "./modelAdapterTestResult.js";

test("紧凑卡片摘要只展示正文输出速度与首响", () => {
  const summary = formatCompactModelAdapterTestSummary({
    status: "success",
    visibleTokensPerSecond: 116.4,
    firstResponseMS: 7_842,
  });

  assert.equal(summary, "输出 116 t/s · 首响 7.8 s");
});

test("测速摘要同时展示总生成、正文、首响应和首字口径", () => {
  const summary = formatModelAdapterTestSummary({
    status: "success",
    tokensPerSecond: 60.5,
    visibleTokensPerSecond: 22.4,
    firstResponseMS: 24_616,
    firstTextTokenMS: 27_214,
  });

  assert.equal(summary, "总生成 61 t/s | 正文 22 t/s | 首响应 24.6 s | 首字 27.2 s");
});

test("成功结果忽略旧摘要并保留新增测速字段", () => {
  const result = normalizeModelAdapterTestResult({
    status: "success",
    summaryText: "61 t/s | 首字 27.2 s",
    tokensPerSecond: 60.5,
    visibleTokensPerSecond: 22.4,
    firstResponseMS: 24_616,
    firstTextTokenMS: 27_214,
    outputTokens: 197,
    visibleOutputTokens: 70,
    reasoningTokens: 127,
    effectiveThinkingEffort: "medium",
  });

  assert.equal(result.summaryText, "总生成 61 t/s | 正文 22 t/s | 首响应 24.6 s | 首字 27.2 s");
  assert.equal(result.visibleTokensPerSecond, 22.4);
  assert.equal(result.firstResponseMS, 24_616);
  assert.equal(result.visibleOutputTokens, 70);
  assert.equal(result.reasoningTokens, 127);
  assert.equal(result.effectiveThinkingEffort, "medium");
});

test("normalizeModelAdapter：备用密钥池去空、去重并剔除主密钥", async () => {
  const { normalizeModelAdapter } = await import("./modelAdapter.js");
  const adapter = normalizeModelAdapter({
    displayName: "池测试",
    type: "openai",
    baseURL: "https://api.example.com/v1",
    apiKey: "sk-primary",
    apiKeys: ["sk-a", "", "sk-a", "sk-primary", "sk-b"],
    modelID: "m-1",
  });
  assert.deepEqual(adapter.apiKeys, ["sk-a", "sk-b"]);
});

test("normalizeModelAdapter：无密钥池时输出空数组且不生成幻影条目", async () => {
  const { normalizeModelAdapter } = await import("./modelAdapter.js");
  const adapter = normalizeModelAdapter({
    displayName: "无池",
    type: "anthropic",
    baseURL: "https://api.example.com",
    apiKey: "sk-only",
    modelID: "claude-x",
  });
  assert.deepEqual(adapter.apiKeys, []);
  const legacy = normalizeModelAdapter({
    displayName: "旧字段",
    type: "openai",
    baseURL: "https://api.example.com/v1",
    apiKey: "sk-main",
    api_keys: ["sk-main"],
    modelID: "m-2",
  });
  assert.deepEqual(legacy.apiKeys, []);
});
