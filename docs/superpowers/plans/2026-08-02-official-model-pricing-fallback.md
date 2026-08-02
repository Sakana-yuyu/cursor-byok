# Official Model Pricing And Average Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the built-in pricing catalog to all provider families already adapted by the application, including z.ai/GLM, MiniMax, ByteDance/Volcengine, and use an explicit average estimate when no official price matches.

**Architecture:** Keep provider/model pricing in `internal/modelcontext`, expose a stable `BuiltinPricingFor` lookup, and add a separately marked average fallback in the bridge price-rate snapshot. Preserve the existing precedence: manual/catalog channel pricing, then official built-in pricing, then average estimate. Frontend displays the computed amount without overriding configured channel prices.

**Tech Stack:** Go pricing catalog, historymetrics price lookup, Vue/Vite frontend, official provider pricing references.

## Global Constraints

- Prices are USD per 1,000,000 tokens unless a provider's official page explicitly documents another unit and the conversion is encoded.
- User-configured and catalog-probed prices always override built-in prices.
- Average fallback is an estimate and must be distinguishable from an official price in backend metadata and UI copy where price source is shown.
- Do not add repository tests because the repository's local test-requirements skill forbids writing tests; use existing tests, Go checks, and production builds.
- Do not replace arbitrary third-party custom model prices when no official model identity can be established.

---

### Task 1: Map adapted provider families and official pricing sources

**Files:**
- Modify: `internal/modelcontext/catalog.go`
- Modify: `internal/app/runner.go`
- Reference: official provider pricing pages recorded in the implementation notes

- [ ] Enumerate the provider/model families already represented by `capabilityRules` and adapter types: OpenAI, Anthropic, Gemini, xAI, Qwen/Alibaba, DeepSeek, Kimi/Moonshot, z.ai/GLM, MiMo, MiniMax, StepFun, and Volcengine/ByteDance.
- [ ] Record each official source URL and the input/output/cache unit used for every family.
- [ ] Mark unsupported or unpublished models as average-only rather than assigning an invented official price.

### Task 2: Complete built-in official pricing rules

**Files:**
- Modify: `internal/modelcontext/catalog.go`

- [ ] Add specific rules before generic family rules for z.ai/GLM model IDs, MiniMax model IDs, ByteDance/Volcengine model IDs, and any uncovered existing provider families.
- [ ] Keep regex matching normalized and ordered from specific model to family fallback.
- [ ] Populate input/output/cache-read/cache-write values only where supported by the official source; leave unsupported cache prices nil.
- [ ] Keep `BuiltinPricingFor(modelID)` pure and deterministic.

### Task 3: Add explicit average-price fallback metadata

**Files:**
- Modify: `internal/modelcontext/catalog.go`
- Modify: `internal/app/runner.go`
- Modify: `internal/historymetrics/pricing.go`
- Modify: `internal/historymetrics/report.go`

- [ ] Compute per-field arithmetic means from the built-in official catalog, ignoring nil fields and excluding average-derived entries.
- [ ] Add a source marker such as `official` or `average` to `historymetrics.PriceRate` and request/provider spend response metadata.
- [ ] Use average fallback only after manual/catalog and official built-in lookup fail.
- [ ] Preserve current cost formula: `(tokens * price-per-million) / 1_000_000`.

### Task 4: Surface estimate status without changing configured prices

**Files:**
- Modify: `frontend/src/views/RequestMetrics.vue`
- Modify: `frontend/src/views/MetricsDetail.vue`
- Modify: `frontend/src/components/StationSpendCard.vue`
- Modify: `frontend/src/components/HomeMetricsCard.vue` only if source status is shown there
- Generated: `frontend/src/i18n/generated/catalog.json` and locale JSON files when source text changes

- [ ] Keep configured/manual and official amounts visually unchanged.
- [ ] Add a concise “均价估算” indicator only for average-derived amounts.
- [ ] Keep unknown/unpriced behavior for rows that cannot be safely associated with an adapted provider or model family.
- [ ] Run the i18n scan build after any source text changes.

### Task 5: Verify pricing and release readiness

**Files:**
- No additional source files.

- [ ] Run `go test ./...` with the repository Go 1.25 toolchain.
- [ ] Run `go vet ./...`.
- [ ] Run `go build ./...`.
- [ ] Run `npm run build` from `frontend`.
- [ ] Run `git diff --check` and confirm generated locale keys remain consistent.
- [ ] Report official-source coverage, average fallback behavior, and any provider whose official pricing is unavailable.
