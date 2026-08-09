function cloneMap(value) {
  return { ...(value || {}) };
}

export function buildSkillsMCPScanConfig(state) {
  return {
    enabled: state?.enabled !== false,
    skillSources: cloneMap(state?.skillSources),
    mcpSources: cloneMap(state?.mcpSources),
    enabledSkills: cloneMap(state?.enabledSkills),
    disabledMcpServers: cloneMap(state?.disabledMcpServers),
    skillSummaries: cloneMap(state?.skillSummaries),
    mcpSummaries: cloneMap(state?.mcpSummaries),
  };
}

export function applySkillsMCPScanSnapshot(state, snapshot) {
  const config = snapshot?.config || {};
  state.skills = Array.isArray(snapshot?.skills) ? snapshot.skills : [];
  state.mcpServers = Array.isArray(snapshot?.mcpServers) ? snapshot.mcpServers : [];
  state.enabled = config.enabled !== false;
  state.skillSources = cloneMap(config.skillSources);
  state.mcpSources = cloneMap(config.mcpSources);
  state.enabledSkills = cloneMap(config.enabledSkills);
  state.disabledMcpServers = cloneMap(config.disabledMcpServers);
  state.skillSummaries = cloneMap(config.skillSummaries);
  state.mcpSummaries = cloneMap(config.mcpSummaries);
}
