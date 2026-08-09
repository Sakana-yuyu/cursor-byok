# Skills Opt-In Default Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make scanned skills opt-in by default and explain in the Skills settings page that BYOK switches do not block skills explicitly supplied by Cursor.

**Architecture:** Add an `enabledSkills` whitelist to persisted scan configuration and carry it through the existing config provider into `SkillStore`. Filter scanned candidates before BM25 activation, while leaving request-context skills and all MCP behavior unchanged. Update the Vue settings state and browser preview binding to edit the whitelist, then add localized explanatory copy.

**Tech Stack:** Go, Vue 3 Composition API, Wails bindings, Node test runner, Playwright, static i18n scanner.

## Global Constraints

- Missing or empty `enabledSkills` means every scanned skill is disabled.
- Existing `disabledSkills` remains readable but no longer controls runtime skill activation.
- Cursor-provided `SkillOptions`, `AgentSkills`, and skill rules remain outside this whitelist.
- MCP defaults and enablement behavior must not change.
- Existing unrelated worktree changes must be preserved.

---

### Task 1: Backend Skill Whitelist

**Files:**
- Modify: `internal/backend/server/config/types.go`
- Modify: `internal/backend/server/config/manager.go`
- Modify: `internal/backend/forwarder/asset_enrichment.go`
- Modify: `internal/backend/forwarder/skill_store.go`
- Modify: `internal/bridge/proxy.go`
- Test: `internal/backend/forwarder/skill_goal_activation_test.go`
- Test: `internal/backend/server/config/types_test.go`

**Interfaces:**
- Produces: `SkillMCPScanConfig.EnabledSkills map[string]bool`.
- Produces: `SkillMCPScanEnabledSkills() map[string]bool` on the scan config provider.
- Changes: `SkillStore.SetScanSettings(enabled bool, sources map[string]bool, enabledSkills map[string]bool)`.

- [x] **Step 1: Write failing tests for opt-in activation**

Add tests that create valid skills and assert an empty whitelist produces no prompt, an explicit lowercase whitelist entry enables one skill, a newly added unlisted skill stays disabled, and Goal mode cannot inject `goal-loop` until whitelisted. Add a config test asserting defaults contain no enabled skills and legacy `disabledSkills` does not populate the whitelist.

- [x] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/backend/forwarder ./internal/backend/server/config`
Expected: FAIL because `EnabledSkills` and whitelist filtering do not exist.

- [x] **Step 3: Implement the whitelist data flow**

Add `EnabledSkills`, expose it through the config manager/provider, copy it into `SkillMCPScanSettings`, synchronize it to `SkillStore`, and replace disabled-map filtering with `enabledSkills[normalizedName] == true`. Preserve `DisabledSkills` only as a compatibility field. Update bridge conversions to pass the whitelist.

- [x] **Step 4: Run focused tests and verify pass**

Run: `go test ./internal/backend/forwarder ./internal/backend/server/config ./internal/bridge`
Expected: PASS.

### Task 2: Skills Settings Opt-In UI

**Files:**
- Modify: `frontend/src/components/settings/categories/SkillsMcpSettings.vue`
- Modify: `frontend/src/services/browserBindings.js`
- Test: `frontend/src/services/clientApi.contract.test.js` or a focused service test matching existing patterns
- Generated: `frontend/src/i18n/generated/catalog.json`
- Generated: `frontend/src/i18n/locales/zh-CN.json`
- Generated: `frontend/src/i18n/locales/en-US.json`
- Generated: `frontend/src/i18n/locales/ja-JP.json`
- Generated: `frontend/src/i18n/locales/ru-RU.json`

**Interfaces:**
- Consumes: `config.enabledSkills` from the Skills/MCP snapshot.
- Produces: saves `enabledSkills` with normalized skill names mapped to `true`.

- [x] **Step 1: Add a failing frontend contract assertion**

Assert the browser preview scan config starts with an empty `enabledSkills` map and that saving a whitelist round-trips it without converting it into `disabledSkills`.

- [x] **Step 2: Run the unit test and verify failure**

Run: `npm --prefix frontend run test:unit`
Expected: FAIL because preview state and settings still use blacklist semantics.

- [x] **Step 3: Implement UI whitelist state and explanatory notice**

Replace `disabledSkills` UI state with `enabledSkills`. Treat absent keys as disabled; add a key on enable and delete it on disable. Add a Skills-only notice using the approved Chinese source text, keeping MCP controls unchanged. Update browser preview defaults to an empty whitelist.

- [x] **Step 4: Build and translate generated catalog entries**

Run: `npm --prefix frontend run build` to scan source strings. Fill the new message in `en-US`, `ja-JP`, and `ru-RU` with non-empty translations while preserving placeholders, then rerun the build until stable.

- [x] **Step 5: Run frontend verification**

Run: `npm --prefix frontend run test:unit`
Run: `npm --prefix frontend run lint`
Run: `npm --prefix frontend run build`
Expected: all PASS.

### Task 3: Full Verification

**Files:**
- Verify all files changed in Tasks 1-2.

**Interfaces:**
- Consumes: backend whitelist semantics and frontend whitelist persistence.
- Produces: verified release-ready behavior without modifying MCP semantics.

- [x] **Step 1: Run all Go verification**

Run: `go test ./...`
Run: `go vet ./...`
Run: `go build ./...`
Expected: all PASS.

- [x] **Step 2: Verify i18n consistency and diff hygiene**

Run a script comparing locale keys with `catalog.json`, checking non-source values are non-empty and placeholders match.
Run: `git diff --check`
Expected: no mismatch and no whitespace errors.

- [x] **Step 3: Inspect the final diff**

Confirm only skill whitelist, UI notice, tests, plan, and generated i18n changes belong to this feature; preserve all pre-existing unrelated modifications.
