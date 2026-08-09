import assert from "node:assert/strict";
import test from "node:test";

import {
  applySkillsMCPScanSnapshot,
  buildSkillsMCPScanConfig,
} from "./skillsMcpScanConfig.js";

test("unrelated skill toggles preserve disabled source choices when saving", () => {
  const state = {
    enabled: true,
    skills: [],
    mcpServers: [],
    skillSources: {},
    mcpSources: {},
    enabledSkills: {},
    disabledMcpServers: {},
    skillSummaries: {},
    mcpSummaries: {},
  };

  applySkillsMCPScanSnapshot(state, {
    skills: [{ name: "existing-skill" }],
    mcpServers: [{ identifier: "existing:mcp" }],
    config: {
      enabled: true,
      skillSources: { workspace: false, user: true },
      mcpSources: { claude: false, cursor: true },
      enabledSkills: { "existing-skill": true },
    },
  });

  state.enabledSkills = { ...state.enabledSkills, "unrelated-skill": true };
  const saved = buildSkillsMCPScanConfig(state);

  assert.deepEqual(saved.skillSources, { workspace: false, user: true });
  assert.deepEqual(saved.mcpSources, { claude: false, cursor: true });
  assert.equal(saved.enabledSkills["unrelated-skill"], true);
});
