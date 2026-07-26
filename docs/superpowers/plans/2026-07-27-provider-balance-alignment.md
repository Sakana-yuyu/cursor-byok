# Provider Balance Alignment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align cursor-byok provider balance querying with cc-switch named-provider behavior, preserve the new configurable fallback safely, and complete a focused code review.

**Architecture:** Named providers remain hardcoded Go strategies for deterministic support. Configured balance queries remain adapter-level fallback configuration. UI only edits configuration and displays balance; backend owns provider semantics, transient classification, and cache identity.

**Tech Stack:** Go backend, Vue 3 frontend, Wails bindings, existing metadata cache.

## Global Constraints

- Do not add tests; this repository has a no-new-tests convention.
- Do not change unrelated stream timing or netproxy code.
- Keep `balanceQueryHeadersJSON` frontend-only; persist `balanceQueryHeaders` as a map.
- Preserve existing user config where possible; duplicate merges must not overwrite non-empty existing balance config.

---

## Source Baseline

cc-switch reference:

- `E:/Project/cc-switch/src-tauri/src/services/balance.rs`

cursor-byok implementation files:

- `internal/client/provider_balance.go`
- `internal/client/provider_balance_named.go`
- `internal/client/provider_balance_configured.go`
- `internal/backend/server/config/types.go`
- `frontend/src/state/appState.js`
- `frontend/src/views/ModelEditor.vue`
- `frontend/src/views/ModelConfig.vue`
- `frontend/src/views/SupplierDetail.vue`

## cc-switch Named Provider Matrix

| Provider | Detect | Endpoint | Fields | Unit | Error Semantics |
|---|---|---|---|---|---|
| DeepSeek | `api.deepseek.com` | `/user/balance` | `balance_infos[].total_balance`, `currency`, `is_available` | response currency, default CNY | network/read = transient; auth/non-2xx/json = deterministic |
| StepFun | `api.stepfun.ai|com` | `https://api.stepfun.com/v1/accounts` | `balance` | CNY | same |
| SiliconFlow CN | `api.siliconflow.cn` | `/v1/user/info` | `data.totalBalance` | CNY | same |
| SiliconFlow EN | `api.siliconflow.com` | `/v1/user/info` | `data.totalBalance` | USD | same |
| OpenRouter | `openrouter.ai` | `/api/v1/credits` | `data.total_credits`, `data.total_usage` or root fallback | USD | remaining <= 0 marked no credits |
| Novita | `api.novita.ai` | `/v3/user/balance` | `availableBalance / 10000` | USD | remaining <= 0 marked no balance |

## Findings To Fix

- [x] Invalid `balanceQueryHeadersJSON` must not be silently dropped before final persistence validation.
- [x] Configured balance lookup should not match only `type + baseURL` when API keys differ.
- [x] Configured balance cache identity must account for config changes.
- [x] DeepSeek should handle multiple `balance_infos` and `is_available` more faithfully.
- [x] OpenRouter should accept root-level credit fields like cc-switch.
- [x] OpenRouter and Novita should surface exhausted balance messages.
- [x] Named/configured request timeout should be aligned or intentionally documented.
- [x] Duplicate adapter merge should preserve balance query fields.

## Tasks

### Task 1: Frontend Persistence Safety

- [x] Validate `balanceQueryHeadersJSON` before stripping it from payload.
- [x] Keep payload map-only (`balanceQueryHeaders`) for backend config.
- [x] Preserve empty JSON as disabled/no extra headers.

### Task 2: Named Provider Alignment

- [x] DeepSeek: aggregate multiple `balance_infos` into scalar output without losing availability semantics.
- [x] DeepSeek: if `is_available=false`, return successful query with clear message.
- [x] OpenRouter: support both `data` wrapper and root-level fields.
- [x] OpenRouter/Novita: keep `Supported:true` for zero balances but set clear exhausted messages.

### Task 3: Configured Lookup And Cache

- [x] Match configured adapter by `type + baseURL + apiKey` when request API key is present.
- [x] Include balance query config hash in configured-cache identity or otherwise avoid stale configured results.
- [x] Keep named/provider fallback cache behavior unchanged.

### Task 4: Duplicate Merge Preservation

- [x] Frontend duplicate merge preserves incoming balance config only when existing config is empty.
- [x] Backend duplicate merge does the same during config normalization.

### Task 5: Validation And Review

- [x] Run targeted Go tests/checks that are available.
- [x] Run frontend static validation if package scripts support it.
- [x] Produce final review: fixed issues, retained differences, residual risk.

## Acceptance Checklist

- [x] cc-switch named provider endpoints/fields/units are matched or documented.
- [x] Invalid balance header JSON cannot be saved through any normal config-save path.
- [x] Configured balance queries do not use the first same-baseURL adapter with a different key.
- [x] Editing configured balance fields cannot be hidden by stale configured-cache results.
- [x] Duplicate imports preserve balance query config.
- [ ] Final answer includes code-review findings and verification output.
