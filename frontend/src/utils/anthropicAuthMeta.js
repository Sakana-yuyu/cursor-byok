export const anthropicAuthModeOptions = [
  { label: "自动（官方仅 X-Api-Key，兼容网关双头）", value: "auto" },
  { label: "兼容双头（旧配置默认）", value: "legacy_dual" },
  { label: "仅 X-Api-Key", value: "x_api_key" },
  { label: "仅 Bearer", value: "bearer" },
];
