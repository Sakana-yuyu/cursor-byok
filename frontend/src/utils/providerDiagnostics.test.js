import assert from "node:assert/strict";
import test from "node:test";

import {
  formatProviderCooldown,
  normalizeProviderDiagnostics,
} from "./providerDiagnostics.js";

test("normalizes safe provider diagnostics without inventing live recovery", () => {
  const now = 1_800_000_000_000;
  const snapshot = normalizeProviderDiagnostics({
    generatedAtUnixMs: now - 1000,
    routerAvailable: true,
    channels: [
      {
        channelId: "ready",
        displayName: "Ready Channel",
        provider: "openai",
        endpointScheme: "https",
        endpointHost: "provider.example:8443",
        credentialConfigured: true,
        healthState: "ready",
        apiKey: "sk-ignored-secret",
        baseURL: "https://user:password@example.invalid?token=secret",
      },
      {
        channelId: "cooling",
        modelId: "model-b",
        healthState: "cooldown",
        cooldownUntilUnixMs: now + 120_000,
      },
      {
        channelId: "expired",
        healthState: "cooldown",
        cooldownUntilUnixMs: now - 1000,
      },
    ],
    modelCatalogCache: { entryCount: 2, ttlSeconds: 300 },
  }, now);

  assert.equal(snapshot.routerAvailable, true);
  assert.equal(snapshot.channels[0].endpoint, "https://provider.example:8443");
  assert.equal(snapshot.channels[0].credentialConfigured, true);
  assert.equal(snapshot.channels[0].apiKey, undefined);
  assert.equal(snapshot.channels[0].baseURL, undefined);
  assert.equal(snapshot.channels[1].healthState, "cooldown");
  assert.equal(snapshot.channels[2].healthState, "cooldown");
  assert.equal(snapshot.readyCount, 1);
  assert.equal(snapshot.cooldownCount, 2);
  assert.deepEqual(snapshot.modelCatalogCache, {
    entryCount: 2,
    ttlSeconds: 300,
    oldestStoredAtUnixMs: 0,
    nextExpiryAtUnixMs: 0,
  });
});

test("rejects unsafe endpoint projections and formats cooldowns", () => {
  const now = 1_800_000_000_000;
  const snapshot = normalizeProviderDiagnostics({
    channels: [{ endpointScheme: "file", endpointHost: "secret.example", healthState: "ready" }],
  }, now);
  assert.equal(snapshot.channels[0].endpoint, "");
  assert.equal(formatProviderCooldown(now - 1, now), "已到恢复时间，请刷新确认");
  assert.equal(formatProviderCooldown(now + 30_000, now), "不到 1 分钟后恢复");
  assert.equal(formatProviderCooldown(now + 120_000, now), "2 分钟后恢复");
  assert.equal(formatProviderCooldown(now + 3_660_000, now), "1 小时 1 分钟后恢复");
  assert.equal(formatProviderCooldown(now + 7_199_000, now), "2 小时后恢复");
  assert.equal(formatProviderCooldown(now + 7_200_000, now), "2 小时后恢复");
});
