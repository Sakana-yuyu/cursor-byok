import test from "node:test";
import assert from "node:assert/strict";

import {
  normalizeClientError,
  safeErrorLogAttributes,
  summarizePayload,
} from "./errorContract.js";

test("classifies timeout as retryable", () => {
  const error = normalizeClientError(new Error("request timeout"), { operation: "LoadState" });

  assert.equal(error.code, "timeout");
  assert.equal(error.kind, "timeout");
  assert.equal(error.disposition, "retryable");
  assert.equal(error.operation, "LoadState");
});

test("classifies authentication failure as blocked", () => {
  const error = normalizeClientError({ statusCode: 401, message: "invalid api key" });

  assert.equal(error.code, "authentication_required");
  assert.equal(error.disposition, "blocked");
  assert.equal(error.statusCode, 401);
});

test("recognizes workspace MCP trust as a stable user action requirement", () => {
  const error = normalizeClientError(new Error(
    "mcp_workspace_trust_required: workspace MCP server requires explicit trust",
  ));

  assert.equal(error.code, "mcp_workspace_trust_required");
  assert.equal(error.kind, "authorization");
  assert.equal(error.disposition, "user_action_required");
});

test("preserves structured backend fields", () => {
  const error = normalizeClientError({
    code: "provider_rate_limited",
    kind: "rate_limit",
    disposition: "retryable",
    userMessage: "稍后重试",
    technicalMessage: "status=429",
    traceId: "req-123",
    retryAfterMs: 1200,
  });

  assert.equal(error.traceId, "req-123");
  assert.equal(error.retryAfterMs, 1200);
  assert.equal(error.technicalMessage, "status=429");
});

test("summarizes payload without leaking secrets or full prompts", () => {
  const summary = summarizePayload({
    apiKey: "sk-secret",
    nested: { authorization: "Bearer secret", model: "gpt-test" },
    prompt: "private prompt",
    items: [1, 2, 3, 4, 5, 6],
  });

  assert.equal(summary.apiKey, "[redacted]");
  assert.equal(summary.nested.authorization, "[redacted]");
  assert.equal(summary.nested.model, "gpt-test");
  assert.equal(summary.prompt, "[redacted]");
  assert.deepEqual(summary.items, { type: "array", length: 6 });
});

test("does not expose sensitive technical message to the user", () => {
  const error = normalizeClientError(new Error("Authorization: Bearer sk-secret"));

  assert.equal(error.userMessage, "服务发生异常，请重试或导出诊断信息");
  assert.doesNotMatch(error.technicalMessage, /sk-secret/);
});

test("redacts secret values from URL query parameters", () => {
  const error = normalizeClientError(new Error("request failed https://api.example.test/v1?token=secret-token&model=test"));

  assert.doesNotMatch(error.technicalMessage, /secret-token/);
  assert.match(error.technicalMessage, /token=\[redacted\]/);
  assert.match(error.technicalMessage, /model=test/);
});

test("safe log attributes exclude raw messages and causes", () => {
  const attributes = safeErrorLogAttributes(new Error("prompt=private apiKey=sk-secret"), {
    operation: "ui.render",
    traceId: "trace-safe",
  });

  assert.deepEqual(attributes, {
    operation: "ui.render",
    code: "internal_error",
    kind: "internal",
    disposition: "fatal",
    statusCode: 0,
    traceId: "trace-safe",
  });
  assert.doesNotMatch(JSON.stringify(attributes), /private|sk-secret/);
});
