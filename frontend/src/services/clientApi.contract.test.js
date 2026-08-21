import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const sourceURL = new URL("./clientApi.js", import.meta.url);
const browserBindingsURL = new URL("./browserBindings.js", import.meta.url);
const skillsSettingsURL = new URL("../components/settings/categories/SkillsMcpSettings.vue", import.meta.url);
const skillsConfigURL = new URL("../utils/skillsMcpScanConfig.js", import.meta.url);

test("long-running model operations use explicit timeout budgets", async () => {
  const source = await readFile(sourceURL, "utf8");

  assert.match(source, /const MODEL_TEST_TIMEOUT_MS = 60_000;/);
  assert.match(source, /const AUTO_MATCH_TIMEOUT_MS = 120_000;/);
  assert.match(
    source,
    /withApiLogging\("TestModelAdapter", adapter, .*?\{ timeoutMs: MODEL_TEST_TIMEOUT_MS \}\);/s,
  );
  assert.match(
    source,
    /withApiLogging\("AutoMatchContextWindows", \[force\], .*?\{ timeoutMs: AUTO_MATCH_TIMEOUT_MS \}\);/s,
  );
});

test("provider diagnostics binding is registered through desktop and browser preview", async () => {
  const [source, bindingsSource] = await Promise.all([
    readFile(sourceURL, "utf8"),
    readFile(browserBindingsURL, "utf8"),
  ]);

  assert.match(source, /GetProviderDiagnostics/);
  assert.match(source, /export function getProviderDiagnostics\(\)/);
  assert.match(source, /desktopOrMock\(\(\) => GetProviderDiagnostics\(\)/);
  assert.match(bindingsSource, /export const GetProviderDiagnostics =/);
  assert.match(bindingsSource, /recordPreviewCall\("GetProviderDiagnostics"\)/);
});

test("skills scan settings use an explicit enablement whitelist", async () => {
  const [bindingsSource, settingsSource, configSource] = await Promise.all([
    readFile(browserBindingsURL, "utf8"),
    readFile(skillsSettingsURL, "utf8"),
    readFile(skillsConfigURL, "utf8"),
  ]);

  assert.match(bindingsSource, /enabledSkills:\s*\{\}/);
  assert.doesNotMatch(bindingsSource, /disabledSkills:\s*\{\s*"superpowers-test-driven-development"/);
  assert.match(settingsSource, /enabledSkills:\s*\{\}/);
  assert.match(configSource, /config\?\.enabledSkills|config\.enabledSkills/);
  assert.match(settingsSource, /state\.enabledSkills\[normalizeConfigKey\(name\)\]/);
  assert.match(
    settingsSource,
    /技能默认关闭。启用后，技能只会进入 BYOK 候选池，并不会在每次请求中全部注入；系统会根据当前任务的相关性稀疏激活并注入少量技能，以减少扫描和提示词开销。此开关只控制 BYOK 扫描；Cursor 客户端显式附带的技能仍可能生效。/,
  );
});

test("browser preview exposes Defender exclusion bindings", async () => {
  const bindingsSource = await readFile(browserBindingsURL, "utf8");

  assert.match(bindingsSource, /export const GetDefenderExclusionState =/);
  assert.match(bindingsSource, /export const OfferDefenderExclusion =/);
  assert.match(bindingsSource, /export const DismissDefenderExclusion =/);
  assert.ok(bindingsSource.includes("alreadyExcluded: true"));
  assert.ok(bindingsSource.includes("offered: true"));
});
