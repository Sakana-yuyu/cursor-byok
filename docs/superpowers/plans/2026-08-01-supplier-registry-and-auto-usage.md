# Supplier Registry And Automatic Usage Matching Implementation Plan

> For agentic workers: REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

Goal: Register every non-Claude-Desktop brand shown in the supplier screenshots as a stable preset, fetch models through verified provider-specific catalog URLs, and automatically select only verified usage/balance query rules while preserving custom configuration.

Architecture: Keep supplier metadata in one frontend catalog consumed by the model editor, catalog fetch flow, and supplier detail UI. Pass the selected stable supplier ID and explicit catalog candidates through the Wails bridge into a backend resolver that tries ordered, protocol-aware URLs. Resolve usage by explicit request fields, persisted adapter identity, supplier ID, and host fallback; providers without a verified rule return a structured unsupported result instead of probing guessed endpoints.

Tech Stack: Vue 3, Vite, static i18n scanner, Go 1.25, Wails bridge, existing provider-balance handlers, existing model catalog parsing, cc-switch provider preset data as a reference.

## Global Constraints

- Cover every brand listed in the approved design matrix except Claude Desktop, which remains in its existing special flow.
- Never use a supplier website URL as an API URL; model catalog candidates must be API endpoints or be generated from an API base URL.
- Use supplierID as the stable persisted identity and prefer it over host matching.
- Do not send guessed balance requests for none or custom_only providers.
- Preserve existing empty/custom supplier behavior and all legacy balance fields.
- Do not add new test files; use existing tests, builds, static checks, fixtures, and browser preview verification.
- Frontend source UI literals remain compatible with the repository i18n scanner; run the frontend build/scan after UI text changes.
- Do not modify Claude Desktop-specific OAuth, role mapping, or local routing behavior.
- Do not touch unrelated existing worktree changes.

---

### Task 1: Create The Supplier Registry And Frontend Metadata Contract

Files:
- Modify: frontend/src/utils/supplierCatalog.js
- Inspect and modify as needed: frontend/src/state/appState.js, frontend/src/views/ModelEditor.vue
- Inspect and modify as needed: frontend/src/services/browserBindings.js
- Reference: .tmp-cc-switch/src/config/claudeDesktopProviderPresets.ts, .tmp-cc-switch/src/config/codingPlanProviders.ts, .tmp-cc-switch/src/config/universalProviderPresets.ts

Interfaces:
- Produce a complete SUPPLIER_TEMPLATES catalog whose entries expose id, supplierID, label, websiteURL, apiKeyURL, type, baseURL, modelCatalog, usage, models, and allowCustomURL.
- modelCatalog.status is openai_models, gemini_models, custom_url, or manual_only; it may include ordered urls and appendCandidates.
- usage.status is fixed, token_plan, newapi, general, custom_only, or none; verified entries expose a backend provider ID and source label.
- Claude Desktop is excluded from the normal model editor template list but its existing special code remains unchanged.

- [ ] Step 1: Inventory the existing catalog shape and all consumers.
  Run: rg -n "SUPPLIER_TEMPLATES|supplierCatalog|supplierID|balanceProfile|FetchModelCatalog" frontend/src internal/bridge internal/client
  Record the existing field names and preserve them while adding the new metadata fields.

- [ ] Step 2: Add one stable registry entry for every approved brand.
  Use stable lowercase IDs independent of display names, keep API base URLs separate from official websites, and derive the protocol/model strategy from verified cc-switch source or official documentation. Entries with no verified model endpoint remain selectable with manual_only; entries with no verified usage endpoint use none or custom_only.

- [ ] Step 3: Expose catalog metadata to model editor state without rewriting existing saved adapters.
  When a preset is selected, populate only missing/default fields such as protocol, base URL, supplier ID, model candidates, catalog metadata, and usage metadata. Keep an existing adapter base URL, model ID, API key, headers, and explicit balance settings intact.

- [ ] Step 4: Keep browser preview bindings deterministic.
  Return the same catalog metadata and stable IDs from browser-preview mocks so the editor and supplier pages render all presets without requiring a Wails runtime.

- [ ] Step 5: Run a static registry audit before moving on.
  Check that IDs are unique, every entry has all status fields, API URLs use http/https, website URLs are never copied into baseURL, and Claude Desktop is absent from the normal list.

### Task 2: Extend Model Catalog Requests And URL Resolution

Files:
- Modify: internal/client/model_catalog.go
- Modify: internal/bridge/proxy.go
- Modify: frontend/src/services/clientApi.js
- Modify: frontend/src/views/ModelCatalog.vue
- Modify: frontend/src/views/SupplierDetail.vue
- Inspect callers: frontend/src/views/ModelEditor.vue, frontend/src/state/appState.js

Interfaces:
- Extend ModelCatalogRequest compatibly with SupplierID, ModelCatalogURL, and a JSON-encoded ordered candidate list. Existing callers that omit the fields continue to use the old resolver.
- The backend returns success only after a candidate responds with parseable model IDs; failed candidates are summarized without credentials and are never cached as an empty catalog.

- [ ] Step 1: Preserve old bridge request decoding and add optional fields.
  Add optional request fields with zero-value behavior matching the existing request path. Update frontend serialization in one helper so all model fetch entry points send the same metadata.

- [ ] Step 2: Implement ordered, deduplicated catalog candidates.
  Use this order: explicit complete URL, preset URLs, existing /models base, OpenAI-compatible /v1/models, /vN/models, verified compatibility-path variants, and Gemini /v1beta/models. Never append paths to the official website URL.

- [ ] Step 3: Apply protocol-specific authentication.
  Use Authorization Bearer for OpenAI-compatible endpoints, x-api-key plus anthropic-version for Anthropic endpoints, and x-goog-api-key for Gemini. Custom headers override defaults by name.

- [ ] Step 4: Wire candidate metadata through both model catalog screens.
  The editor fetch action, model catalog bulk flow, and supplier detail fetch must all pass supplier ID and candidate URLs. Existing saved drafts without metadata must still fetch via legacy behavior.

- [ ] Step 5: Build or run focused existing backend checks.
  Run existing internal/client tests and compile bridge/backend packages. Confirm no API key appears in candidate failure messages.

### Task 3: Add Strict Supplier-Aware Automatic Usage Matching

Files:
- Modify: internal/client/provider_balance.go
- Modify: internal/client/provider_balance_named.go
- Modify: internal/client/provider_balance_coding_plan.go
- Modify: internal/client/provider_balance_configured.go
- Inspect and modify as needed: internal/backend/server/config/types.go
- Inspect bridge request/response types: internal/bridge/proxy.go

Interfaces:
- Automatic matching order is explicit request fields, exact persisted adapter identity, stable supplier ID, base URL host, then only explicitly allowed newapi, token_plan, or general profiles.
- none and custom_only produce a structured unsupported result with a stable reason/source and do not enter the generic endpoint guessing chain.
- Empty/custom legacy supplier IDs retain the current generic fallback behavior.

- [ ] Step 1: Add supplier identity and capability fields to the balance resolution context.
  Carry supplier ID, usage status/provider, base URL, and explicit/custom query settings through the existing request path without changing persisted legacy JSON names.

- [ ] Step 2: Centralize verified provider routing.
  Map verified fixed rules for DeepSeek, Moonshot, StepFun, SiliconFlow, OpenRouter, Novita, Kimi Coding, Zhipu, MiniMax, ZenMux, and Volcengine to existing named/token-plan handlers. Add only endpoints confirmed by cc-switch or official docs.

- [ ] Step 3: Enforce the strict no-guess rule for unsupported presets.
  Return the existing balance response shape with supported false or its local equivalent for none/custom_only, and ensure generic <base>/user/balance runs only for explicit custom profile or a preset marked general.

- [ ] Step 4: Preserve configured query templates and placeholder substitution.
  Keep apiKey, baseUrl, accessToken, and userId substitution, dot-path field lookup, headers, units, and old balance fields working. Mark successful configured results as configured rather than silently converting them to a fixed provider.

- [ ] Step 5: Verify cache and failure semantics.
  Ensure cache keys distinguish supplier ID, strategy, URL, field, credential/token, and header summary; do not cache unsupported or all-candidate-failed results; retain existing last-known-success behavior for transient failures.

### Task 4: Surface Capability And Usage Status In The UI

Files:
- Modify: frontend/src/views/ModelEditor.vue
- Modify: frontend/src/views/ModelConfig.vue
- Modify: frontend/src/views/SupplierDetail.vue
- Modify: frontend/src/views/ModelCatalog.vue
- Modify locale/catalog files only through the repository i18n workflow if generated output changes.

Interfaces:
- Users can see model catalog capability, usage matching source/status, and official/API-key help links.
- Unsupported usage displays the exact Chinese status 暂无自动查询; the custom query editor remains available.

- [ ] Step 1: Add compact capability labels to the editor and supplier detail views.
  Show protocol, base URL, catalog status, usage status/source, and whether the URL is preset or custom without using the website URL as a request target.

- [ ] Step 2: Keep custom supplier and custom balance query controls accessible.
  The custom template can specify model catalog URL, headers, balance URL, field path, unit, access token, and user ID. Preset selection must not hide these controls.

- [ ] Step 3: Handle loading, unsupported, error, and success states.
  Render 暂无自动查询 for a declared unsupported provider, distinguish transient query failures from unsupported capability, and keep existing bulk model selection and save behavior.

- [ ] Step 4: Run the static i18n scan/build.
  Use npm --prefix frontend run i18n:scan and npm --prefix frontend run build, then review generated locale diffs and retain only expected catalog updates.

### Task 5: Verification And Handoff

Files:
- Inspect all task files and the final diff; do not stage unrelated existing changes.

- [ ] Step 1: Run repository verification commands.
  go test ./internal/client/...
  go vet ./...
  go build ./...
  npm --prefix frontend run i18n:scan
  npm --prefix frontend run build
  git diff --check

- [ ] Step 2: Run a static supplier matrix audit.
  Parse the catalog and assert unique IDs, complete capability fields, valid API URL schemes, no website-to-API URL confusion, and all approved labels present.

- [ ] Step 3: Perform manual browser-preview verification.
  Open the model editor and supplier detail views at desktop and narrow widths; exercise preset selection, custom supplier selection, catalog loading mock, unsupported usage state, custom usage form, and bulk model selection. Confirm no console errors and no overlapping status text.

- [ ] Step 4: Review the diff and report any pre-existing failures separately.
  Use git status --short, git diff --stat, and targeted diffs to confirm only task files changed. Do not revert or stage unrelated modifications.

- [ ] Step 5: Commit only implementation files if requested.
  Before any commit, rerun the verification commands above and stage only files belonging to this feature.

