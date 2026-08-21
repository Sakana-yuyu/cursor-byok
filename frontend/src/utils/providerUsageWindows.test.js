import assert from "node:assert/strict";
import test from "node:test";

import {
  formatProviderUsageWindowPercent,
  formatProviderUsageWindowReset,
  normalizeProviderUsageWindows,
} from "./providerUsageWindows.js";

test("normalizes structured provider usage windows", () => {
  const [fiveHour, weekly] = normalizeProviderUsageWindows([
    {
      id: "5h",
      label: "5小时",
      unit: "%",
      used: 25,
      limit: 100,
      remaining: 75,
      usedFraction: 0.25,
      remainingFraction: 0.75,
      status: "ok",
      resetsAt: "2026-08-20T12:00:00Z",
    },
    {
      id: "7d",
      label: "周限额",
      unit: "%",
      used: 87.5,
      limit: 100,
      remaining: 12.5,
    },
  ]);

  assert.equal(fiveHour.id, "5h");
  assert.equal(fiveHour.usedPercent, 25);
  assert.equal(fiveHour.remainingPercent, 75);
  assert.equal(fiveHour.status, "ok");
  assert.equal(weekly.usedFraction, 0.875);
  assert.equal(weekly.remainingFraction, 0.125);
  assert.equal(weekly.status, "warning");
});

test("derives missing fractions without treating zero as absent", () => {
  const [window] = normalizeProviderUsageWindows([
    { label: "月限额", used: 0, limit: 100, remaining: 100 },
  ]);

  assert.equal(window.usedFraction, 0);
  assert.equal(window.remainingFraction, 1);
  assert.equal(window.usedPercent, 0);
  assert.equal(window.remainingPercent, 100);
  assert.equal(window.status, "ok");
});

test("clamps malformed fractions and preserves unknown windows", () => {
  const windows = normalizeProviderUsageWindows([
    { id: "over", label: "超限", usedFraction: 1.3 },
    { id: "unknown", label: "未知", usedFraction: "invalid", status: "unknown" },
  ]);

  assert.equal(windows[0].usedFraction, 1);
  assert.equal(windows[0].remainingFraction, 0);
  assert.equal(windows[0].status, "exhausted");
  assert.equal(windows[1].usedFraction, null);
  assert.equal(windows[1].remainingFraction, null);
  assert.equal(windows[1].status, "unknown");
});

test("formats stable reset timestamps and percentages", () => {
  const reset = formatProviderUsageWindowReset("2026-08-20T10:45:00Z", "en-CA", "UTC");
  assert.match(reset, /2026/);
  assert.match(reset, /10:45/);
  assert.equal(formatProviderUsageWindowReset("not-a-date", "en-CA", "UTC"), "");
  assert.equal(formatProviderUsageWindowPercent(12.6), "13%");
  assert.equal(formatProviderUsageWindowPercent(null), "—");
});
