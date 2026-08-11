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
