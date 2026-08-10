import assert from "node:assert/strict";
import test from "node:test";

import { normalizeDelegationExecutorPolicy } from "./delegationExecutorConfig.js";

test("delegation normalization preserves executor policy without secret values", () => {
  const source = {
    executorFailoverLimit: 2,
    executors: [{
      id: " claude-code ",
      kind: " builtin ",
      displayName: " Claude Code ",
      enabled: true,
      priority: 1,
      executable: " claude.exe ",
      probeTimeoutSeconds: 5,
      executionTimeoutSeconds: 120,
      environmentVariables: [" ANTHROPIC_API_KEY ", "ANTHROPIC_API_KEY", "TOKEN=secret-value"],
      options: { outputFormat: " stream-json ", apiKey: "secret-value", apikey: "secret-value" },
    }],
  };

  const normalized = normalizeDelegationExecutorPolicy(source);
  source.executors[0].environmentVariables[0] = "MUTATED";
  source.executors[0].options.outputFormat = "mutated";

  assert.equal(normalized.executorFailoverLimit, 2);
  assert.deepEqual(normalized.executors, [{
    id: "claude-code",
    kind: "builtin",
    displayName: "Claude Code",
    enabled: true,
    priority: 1,
    executable: "claude.exe",
    probeTimeoutSeconds: 5,
    executionTimeoutSeconds: 120,
    environmentVariables: ["ANTHROPIC_API_KEY"],
    options: { outputFormat: "stream-json" },
  }]);
});

test("legacy delegation defaults external executors to disabled empty policy", () => {
  const normalized = normalizeDelegationExecutorPolicy({ enabled: true });
  assert.equal(normalized.executorFailoverLimit, 3);
  assert.deepEqual(normalized.executors, []);
});
