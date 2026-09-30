import test from "node:test";
import assert from "node:assert/strict";

import { toUserError, toUserErrorWithCause } from "./errorHumanizer.js";

test("toUserErrorWithCause appends sanitized technical cause", () => {
  const text = toUserErrorWithCause(new Error(
    "所有模型目录候选地址均失败：模型目录网络请求失败: dial tcp 127.0.0.1:7890: connect: connection refused",
  ));

  assert.match(text, /暂时无法连接服务，正在准备恢复/);
  assert.match(text, /dial tcp 127\.0\.0\.1:7890: connect: connection refused/);
});

test("toUserErrorWithCause keeps credentials out of the banner", () => {
  const text = toUserErrorWithCause(new Error(
    "所有模型目录候选地址均失败：模型目录网络请求失败: Authorization: Bearer sk-secret",
  ));

  assert.doesNotMatch(text, /sk-secret/);
});

test("toUserErrorWithCause does not duplicate identical text", () => {
  const text = toUserErrorWithCause(new Error("操作已取消"));

  assert.equal(text, "操作已取消");
});

test("toUserError stays generic for unclassified errors", () => {
  const text = toUserError(new Error("Authorization: Bearer sk-secret"));

  assert.equal(text, "服务发生异常，请重试或导出诊断信息");
});
