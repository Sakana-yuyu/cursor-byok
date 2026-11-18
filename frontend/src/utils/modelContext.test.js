import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import catalog from "../../../internal/modelcontext/models.json" with { type: "json" };
import {
  contextWindowTokensForModel,
  isModelCovered,
  resolveModelCapabilities,
  resolveModelContextWindow,
} from "./modelContext.js";

const normalize = (id) => id.toLowerCase().replace(/^models\//, "").replace(/[ _]/g, "-");

test("前端与 Go 嵌入同一份目录，顺序与能力字段不漂移", () => {
  const goSource = readFileSync(new URL("../../../internal/modelcontext/catalog.go", import.meta.url), "utf8");
  assert.match(goSource, /\/\/go:embed models\.json/);
  for (const rule of catalog.rules) {
    // All JSON patterns must compile without JS-only translation or Go inline flags.
    assert.doesNotThrow(() => new RegExp(rule.pattern));
    assert.doesNotMatch(rule.pattern, /\(\?[=!<]|\\[1-9]|\(\?[ims-]+\)/);
    for (const id of [rule.displayName, `provider/${rule.displayName}`, `models/${rule.displayName}`]) {
      const fullID = normalize(id);
      const basename = fullID.replace(/^.*\//, "");
      const expected = catalog.rules.find((entry) =>
        (!fullID.includes("/") || entry.pattern.includes("/")) && new RegExp(entry.pattern).test(fullID))
        || catalog.rules.find((entry) => new RegExp(entry.pattern).test(basename));
      const actual = resolveModelCapabilities(id);
      assert.equal(isModelCovered(id), Boolean(expected), id);
      if (!expected) {
        assert.equal(actual, null, id);
        continue;
      }
      for (const key of ["displayName", "contextWindowTokens", "maxOutputTokens", "supportsVision", "supportsAudio", "supportsTools", "supportsThinking", "reasoningEfforts"]) {
        assert.deepEqual(actual[key], expected[key], `${id}: ${key}`);
      }
      assert.equal(actual.source, expected.source || "builtin_catalog");
    }
  }
});

test("特定型号优先于 GPT6 家族估算，且保留输出及思考能力", () => {
  for (const id of ["gpt-6.1-sol", " models/GPT_6.1_SOL ", "provider/gpt-6.1-sol-latest"]) {
    const cap = resolveModelCapabilities(id);
    assert.equal(cap.contextWindowTokens, 272000);
    assert.equal(cap.maxOutputTokens, 128000);
    assert.equal(cap.source, "builtin_catalog");
    assert.ok(cap.reasoningEfforts.includes("none"));
    assert.equal(resolveModelContextWindow(id).tokens, 272000);
  }
  assert.equal(resolveModelCapabilities("Qwen/Qwen3.8-Max-Preview").contextWindowTokens, 1000000);
  assert.equal(resolveModelCapabilities("MiniMax/M3.1-Flash").displayName, "MiniMax-M3.1-Flash");
  assert.equal(resolveModelCapabilities("gpt-6-future").source, "family_estimate");
  assert.equal(resolveModelCapabilities("unknown-model"), null);
  assert.equal(isModelCovered(""), false);
});

test("命名空间与大小写下划线不改变其他专属型号能力", () => {
  const models = [
    "qwen3.8-max-preview", "m3.1-flash", "minimax-m2.7-highspeed", "deepseek-v4.1-flash",
    "grok-4.7-fast", "cursor-grok-4.5-low", "gpt-6.1-sol", "gpt-5.6-luna",
    "claude-opus-5.5", "claude-sonnet-5.5", "gemini-3.5-flash-lite", "glm-5.3-flash", "doubao-seed-2.1-pro",
  ];
  for (const model of models) {
    const baseline = resolveModelCapabilities(model);
    assert.ok(baseline, model);
    for (const namespace of ["Qwen", "MiniMax", "DeepSeek", "Claude", "GPT-6", "GLM", "owner-a", "owner-b/subteam"]) {
      for (const id of [`${namespace}/${model}`, `models/${namespace}/${model.replaceAll("-", "_").toUpperCase()}`]) {
        assert.deepEqual(resolveModelCapabilities(id), baseline, id);
        assert.equal(resolveModelContextWindow(id).tokens, baseline.contextWindowTokens, id);
      }
    }
  }
  assert.equal(resolveModelCapabilities("Qwen/not-a-known-model"), null);
});

test("目录返回值不允许调用方修改共享思考档位", () => {
  resolveModelCapabilities("gpt-6.1-sol").reasoningEfforts.push("fake");
  assert.ok(!resolveModelCapabilities("gpt-6.1-sol").reasoningEfforts.includes("fake"));
});

test("明确的小窗口与来源未知旧值不因识别而放大", () => {
  assert.equal(contextWindowTokensForModel("gpt-6.1-sol", 32000), 32000);
  assert.equal(contextWindowTokensForModel("gpt-6.1-sol", "64000"), 64000);
  assert.equal(contextWindowTokensForModel("unknown-model", 16000), 16000);
  assert.equal(contextWindowTokensForModel("gpt-6.1-sol", 0), 272000);
  assert.equal(contextWindowTokensForModel("unknown-model", 0), 0);
});