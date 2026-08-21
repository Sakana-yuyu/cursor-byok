function finiteNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function safeText(value) {
  return String(value || "").trim();
}

function safeEndpoint(scheme, host) {
  const normalizedScheme = safeText(scheme).toLowerCase();
  const normalizedHost = safeText(host);
  if (!normalizedHost || !["http", "https"].includes(normalizedScheme)) return "";
  return `${normalizedScheme}://${normalizedHost}`;
}

export function normalizeProviderDiagnostics(snapshot, now = Date.now()) {
  const source = snapshot && typeof snapshot === "object" ? snapshot : {};
  const generatedAtUnixMs = finiteNumber(source.generatedAtUnixMs, finiteNumber(now, Date.now()));
  const channels = Array.isArray(source.channels)
    ? source.channels.filter((channel) => channel && typeof channel === "object").map((channel, index) => {
      const cooldownUntilUnixMs = finiteNumber(channel.cooldownUntilUnixMs);
      const cooling = safeText(channel.healthState) === "cooldown";
      return {
        channelId: safeText(channel.channelId) || `channel-${index + 1}`,
        displayName: safeText(channel.displayName) || safeText(channel.modelId) || `渠道 ${index + 1}`,
        groupName: safeText(channel.groupName),
        provider: safeText(channel.provider),
        protocolMode: safeText(channel.protocolMode),
        protocolGroup: safeText(channel.protocolGroup),
        modelId: safeText(channel.modelId),
        endpoint: safeEndpoint(channel.endpointScheme, channel.endpointHost),
        contextWindowTokens: Math.max(0, finiteNumber(channel.contextWindowTokens)),
        maxCompletionTokens: Math.max(0, finiteNumber(channel.maxCompletionTokens)),
        credentialConfigured: Boolean(channel.credentialConfigured),
        customHeadersConfigured: Boolean(channel.customHeadersConfigured),
        healthState: cooling ? "cooldown" : "ready",
        cooldownUntilUnixMs: cooling ? cooldownUntilUnixMs : 0,
      };
    })
    : [];
  const cache = source.modelCatalogCache && typeof source.modelCatalogCache === "object"
    ? source.modelCatalogCache
    : {};
  const routerAvailable = Boolean(source.routerAvailable);
  const state = ["ready", "unavailable", "error"].includes(safeText(source.state))
    ? safeText(source.state)
    : (routerAvailable ? "ready" : "unavailable");
  return {
    generatedAtUnixMs,
    state,
    errorCode: safeText(source.errorCode),
    routerAvailable,
    channels,
    readyCount: channels.filter((channel) => channel.healthState === "ready").length,
    cooldownCount: channels.filter((channel) => channel.healthState === "cooldown").length,
    modelCatalogCache: {
      entryCount: Math.max(0, finiteNumber(cache.entryCount)),
      ttlSeconds: Math.max(0, finiteNumber(cache.ttlSeconds)),
      oldestStoredAtUnixMs: Math.max(0, finiteNumber(cache.oldestStoredAtUnixMs)),
      nextExpiryAtUnixMs: Math.max(0, finiteNumber(cache.nextExpiryAtUnixMs)),
    },
  };
}

export function formatProviderCooldown(untilUnixMs, now = Date.now()) {
  const until = finiteNumber(untilUnixMs);
  const remaining = until - finiteNumber(now, Date.now());
  if (remaining <= 0) return "已到恢复时间，请刷新确认";
  if (remaining < 60_000) return "不到 1 分钟后恢复";
  const totalMinutes = Math.ceil(remaining / 60_000);
  if (totalMinutes < 60) return `${totalMinutes} 分钟后恢复`;
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (minutes > 0) return `${hours} 小时 ${minutes} 分钟后恢复`;
  return `${hours} 小时后恢复`;
}

export function formatDiagnosticTimestamp(unixMs, locale) {
  const value = finiteNumber(unixMs);
  if (value <= 0) return "—";
  return new Intl.DateTimeFormat(locale || undefined, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(value));
}
