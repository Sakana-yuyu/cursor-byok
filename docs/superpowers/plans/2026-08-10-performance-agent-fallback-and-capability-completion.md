# Performance, Agent Fallback, And Capability Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve startup and history performance, secure workspace-discovered executable assets, harden Cursor protocol reliability, and make multiple external agent CLIs reachable through the existing supervised delegation runtime.

**Architecture:** Keep `internal/backend/delegation.Scheduler` as the single queue, concurrency, cancellation, checkpoint, supervision, and snapshot owner. Add neutral executor identity, probing, and failover around its existing executor function, then implement CLI-specific adapters behind that boundary. Performance, scanner safety, and protocol fixes remain independently testable and independently revertible.

**Tech Stack:** Go 1.25+, Vue 3, Vite 7, Wails 3 bindings, Node built-in test runner, Playwright, protobuf extraction tooling.

## Global Constraints

- Treat `D:\cursor` as read-only; extraction may copy files into ignored temporary directories only.
- Use red-green-refactor for every behavior change and run the failing test before production edits.
- Make one commit per task below after fresh verification; do not mix unrelated cleanup.
- Preserve existing local and Cursor delegation defaults when external executors are disabled.
- Do not expose a UI control before its backend path, cancellation, diagnostics, and persistence exist.
- Workspace MCP execution requires explicit fingerprinted trust; discovery remains read-only.
- External processes use direct argument arrays, explicit working directories, bounded output, redaction, timeouts, and process-tree cancellation.
- UI text changes must run the repository i18n workflow and update all registered locales.
- Prompt or history model-visible changes must preserve append-only history and prefix-cache stability.
- Claude Code and Codex CLI are installed on the target machine; Gemini and Grok-compatible executors must support precise not-installed diagnosis.
- Cursor/Code editor launch capability is distinct from verified agent execution capability.

---

## File Structure

### Performance

- Create `frontend/scripts/assert-build-output.mjs`: inspect production HTML and reject eager Markdown editor references.
- Modify `frontend/package.json`: make the production build run the artifact assertion.
- Modify `frontend/vite.config.js`: keep Markdown editor modules out of shared manual chunks.
- Modify `internal/bridge/history.go`: collect session and debug sizes in one walk.
- Modify `internal/bridge/history_test.go`: cover nested files, debug presence, and one-walk classification.
- Modify `frontend/src/composables/usePolling.js` and add `frontend/src/composables/usePolling.test.js`: completion-based polling with no overlap.
- Modify `frontend/src/views/StatsOverlay.vue` and `frontend/src/components/DelegationRuntimePanel.vue`: remove duplicate initial refreshes.
- Modify `frontend/src/components/settings/categories/HistorySettings.vue`: bounded details rendering and collapsed older groups.
- Modify `frontend/src/style/global.css`: remove the global bundled TTF dependency.

### Workspace Skills And MCP

- Modify `frontend/src/components/settings/categories/SkillsMcpSettings.vue`: consume the active workspace root and round-trip source maps.
- Create `frontend/src/composables/useWorkspaceRoot.js`: share the selected/recent MCP workspace root between settings and the runtime panel.
- Modify `internal/backend/forwarder/service.go` and `asset_enrichment.go`: retain the most recent normalized request workspace as non-model-visible runtime state.
- Modify `internal/backend/host.go`, `internal/client/service.go`, `internal/bridge/proxy.go`, `frontend/src/services/clientApi.js`, and `frontend/src/services/browserBindings.js`: expose the recent workspace root to settings without changing conversation history.
- Modify `internal/backend/forwarder/skill_multisource.go`: workspace-first roots and Gemini/Copilot workspace coverage.
- Modify `internal/backend/forwarder/mcp_scanner.go`: workspace-first discovery and Gemini/Copilot MCP coverage.
- Modify `internal/backend/forwarder/skill_multisource_test.go` and create or extend `internal/backend/forwarder/mcp_scanner_test.go`: coverage and precedence.
- Create `internal/backend/forwarder/mcp_trust.go` and `internal/backend/forwarder/mcp_trust_test.go`: normalized workspace identity and command fingerprint.
- Modify `internal/backend/server/config/types.go`, `internal/backend/server/config/manager.go`, and config tests: persist trust records without exposing secret values.
- Modify `internal/bridge/proxy.go` and bridge tests: require trust before workspace MCP connection and expose grant/revoke operations.
- Modify `frontend/src/components/settings/categories/SkillsMcpSettings.vue` and Playwright settings tests: compact trust confirmation and invalidation state.

### Cursor Protocol

- Modify `internal/backend/forwarder/broker.go`, `internal/backend/forwarder/service.go`, `internal/backend/forwarder/append_seq.go`, and focused tests: reconnect retention, retry idempotency, and late non-streaming results.
- Refactor `proto/ext_tool/extractor.go` into testable parsing/resolution units and add `proto/ext_tool/extractor_test.go` fixtures for RPC/message/enum/oneof resolution and deterministic output.
- Modify `internal/backend/forwarder/tool_catalog.go`, `internal/backend/forwarder/tool_catalog_test.go`, and create `docs/cursor-capability-map.md`: distinguish executable tools from control/shared messages and record reachability.

### External Executors

- Create `internal/backend/delegation/executor.go`: executor IDs, capabilities, probe states, attempts, failure classes, and registry interfaces.
- Create `internal/backend/delegation/executor_registry.go` and tests: registration, cached probes, eligibility, priority, health cooldown, and bounded selection.
- Create `internal/backend/delegation/process_runner.go` plus OS-specific process-tree files and tests: direct process execution, cancellation, output bounds, and redaction.
- Create adapter files and tests under `internal/backend/delegation/executors/`: `claude.go`, `codex.go`, `gemini.go`, `custom.go`, and `cursor.go`.
- Modify `internal/backend/delegation/scheduler.go`: snapshot executor identity and attempt history while preserving the existing `Executor func` API during migration.
- Modify `internal/backend/forwarder/delegation_runtime.go`, `delegation_local.go`, `delegation_cursor.go`, and multitask/supervision tests: route through the registry and bounded failover.
- Modify `internal/backend/server/config/delegation.go`, config manager/conversion code, and tests: persist normalized executor configuration.
- Modify `internal/bridge/proxy.go` or create a focused bridge file, `internal/client/delegation.go`, frontend bindings/mocks, and client API: probe/list/refresh operations.
- Modify `frontend/src/components/settings/categories/DelegationSettings.vue`, `frontend/src/components/DelegationRuntimePanel.vue`, i18n catalogs, unit tests, and Playwright tests: compact backend health and attempt details.

---

### Task 1: Prevent Markdown Editor Entry Preload

**Files:**
- Create: `frontend/scripts/assert-build-output.mjs`
- Modify: `frontend/package.json`
- Modify: `frontend/vite.config.js`
- Test: production output under `frontend/dist/index.html`

**Interfaces:**
- Consumes: Vite production output and the existing async import in `SkillsMcpSettings.vue`.
- Produces: `npm run build` fails when entry HTML references a JS or CSS asset containing `md-editor`, while the editor remains a dynamic chunk.

- [ ] **Step 1: Write the failing artifact assertion**

Create a Node script that reads `dist/index.html`, extracts `script[src]` and `link[href]` references, and exits non-zero when a referenced basename contains `md-editor`. Also require at least one generated JS file to contain the string `MarkdownEditorModal` or `md-editor-v3` so deleting the feature cannot satisfy the test.

- [ ] **Step 2: Run the current build and observe RED**

Run: `npm --prefix frontend run build`

Expected: Vite succeeds, then the assertion fails and prints the eager `vendor-md-editor` JS/CSS references from `index.html`.

- [ ] **Step 3: Keep editor modules outside shared manual chunks**

In `manualChunks(id)`, return `undefined` for `md-editor-v3`, `codemirror`, and `@lezer` before the generic `vendor-misc` return. Do not change the async component boundary.

- [ ] **Step 4: Verify GREEN and reachability**

Run: `npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build`

Run the existing settings browser preview and open the Markdown editor modal with Playwright; require the modal textbox/editor surface to appear and no page error.

- [ ] **Step 5: Commit**

Commit: `perf(frontend): lazy load markdown editor assets`

### Task 2: Aggregate History Metadata In One Walk

**Files:**
- Modify: `internal/bridge/history.go`
- Modify: `internal/bridge/history_test.go`

**Interfaces:**
- Consumes: a session directory containing ordinary files and an optional `debug` subtree.
- Produces: `historyDirectoryStats(root) (sessionBytes int64, debugBytes int64, hasDebug bool)`, where session bytes exclude debug bytes and the implementation invokes `filepath.WalkDir` once.

- [ ] **Step 1: Add failing table tests**

Create nested ordinary and debug files, including an empty debug directory. Assert exact ordinary size, debug size, and `hasDebug` only when a regular debug file exists. Add a test hook or injectable walker used only by the helper and assert one invocation.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/bridge -run 'HistoryDirectoryStats|ScanHistorySession'`

Expected: failure because the aggregate helper does not exist or the walker is called more than once.

- [ ] **Step 3: Implement the aggregate helper**

During one walk, classify a regular file as debug when its relative path is `debug` or begins with `debug/`. Add its size to exactly one counter and set `hasDebug`. Replace the three calls in `scanHistorySession`; keep `dirSize` for deletion/export callers.

- [ ] **Step 4: Verify GREEN**

Run: `go test -count=1 ./internal/bridge && go test -count=1 ./... && go vet ./...`

- [ ] **Step 5: Commit**

Commit: `perf(history): scan session files once`

### Task 3: Serialize Polling And Remove Duplicate Initial Requests

**Files:**
- Modify: `frontend/src/composables/usePolling.js`
- Create: `frontend/src/composables/usePolling.test.js`
- Modify: `frontend/package.json`
- Modify: `frontend/src/views/StatsOverlay.vue`
- Modify: `frontend/src/components/DelegationRuntimePanel.vue`

**Interfaces:**
- Produces: a polling controller whose next timeout is scheduled only after the previous promise settles; `start` is idempotent and `stop` prevents rescheduling after an in-flight call finishes.

- [ ] **Step 1: Add fake-timer-independent tests**

Extract a small `createPollingController(task, schedule, cancel, intervalMs)` pure controller. Tests supply deterministic schedule/cancel functions and a deferred promise, then assert a second run is not scheduled or invoked while the first is pending and stop prevents post-settlement scheduling.

- [ ] **Step 2: Run RED**

Run: `npm --prefix frontend run test:unit`

Expected: the new composable tests fail because no controller exists and current `setInterval` overlaps.

- [ ] **Step 3: Implement completion-based scheduling**

Use recursive `setTimeout` after `Promise.resolve(task()).finally(...)`. Keep the existing `usePolling` return shape. Remove manual initial calls at consumers that already use `immediate: true`; preserve genuinely separate data loads.

- [ ] **Step 4: Verify GREEN**

Run: `npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build`

- [ ] **Step 5: Commit**

Commit: `perf(frontend): prevent overlapping polls`

### Task 4: Bound History Details Rendering

**Files:**
- Modify: `frontend/src/components/settings/categories/HistorySettings.vue`
- Modify: relevant `frontend/e2e/history*.spec.mjs` file or create `frontend/e2e/history-performance.spec.mjs`

**Interfaces:**
- Produces: recent groups expanded by default, older groups collapsed, and a fixed initial row limit with an explicit load-more command in details view.

- [ ] **Step 1: Add Playwright regression data**

Provide at least 500 preview sessions across multiple months. Assert the initial number of rendered session rows is bounded, the most recent group is expanded, older groups are collapsed, and load-more reveals the next page without losing selection.

- [ ] **Step 2: Run RED**

Run: `npm --prefix frontend run test:e2e -- history-performance.spec.mjs`

Expected: current page renders all rows and expands every group.

- [ ] **Step 3: Implement bounded rendering**

Cache status presentation per session during normalization, default only the newest group expanded, slice visible sessions by a stable page size, and use an existing text-button command for load more. Preserve icon view, filters, selection, delete, and debug actions.

- [ ] **Step 4: Verify GREEN and layout**

Run: `npm --prefix frontend run test:e2e -- history-performance.spec.mjs && npm --prefix frontend run lint && npm --prefix frontend run build`

Capture desktop and narrow screenshots and confirm no row/header overlap.

- [ ] **Step 5: Commit**

Commit: `perf(history): bound details view rendering`

### Task 5: Remove The Global Bundled TTF Cost

**Files:**
- Modify: `frontend/src/style/global.css`
- Delete after proving unused: `frontend/src/style/fonts/PingFang-Medium.ttf`
- Modify: `frontend/scripts/assert-build-output.mjs`

**Interfaces:**
- Produces: the global UI uses the native system stack and production assets contain no multi-megabyte PingFang TTF.

- [ ] **Step 1: Extend the artifact assertion**

Fail when a generated `.ttf` asset exceeds 1 MB or entry CSS references `PingFang-Medium.ttf`.

- [ ] **Step 2: Run RED**

Run: `npm --prefix frontend run build`

Expected: assertion reports the current approximately 10.97 MB font.

- [ ] **Step 3: Remove the font face and unused asset**

Use `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "Microsoft YaHei", Roboto, sans-serif`. Delete the TTF only after `rg` shows no remaining references.

- [ ] **Step 4: Verify GREEN and visual parity**

Run: `npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend run test:e2e -- settings-navigation.spec.mjs`

- [ ] **Step 5: Commit**

Commit: `perf(frontend): use native system fonts`

### Task 6: Preserve Workspace Root And Source Choices

**Files:**
- Modify: `frontend/src/components/settings/categories/SkillsMcpSettings.vue`
- Create: `frontend/src/composables/useWorkspaceRoot.js`
- Modify: `frontend/src/components/DelegationRuntimePanel.vue`
- Modify: `internal/backend/forwarder/service.go`
- Modify: `internal/backend/forwarder/asset_enrichment.go`
- Modify: `internal/backend/host.go`
- Modify: `internal/client/service.go`
- Modify: `internal/bridge/proxy.go`
- Modify: `frontend/src/services/clientApi.js`
- Modify: `frontend/src/services/browserBindings.js`
- Test: frontend service/unit tests and Skills/MCP Playwright flow

**Interfaces:**
- Produces: `Service.RecentWorkspaceRoot() string` updated by asset enrichment, host/client/bridge `GetRecentWorkspaceRoot` pass-throughs, and a shared frontend workspace-root composable. Settings calls scan/read/connect APIs with that normalized root and `buildScanConfig` round-trips `skillSources` and `mcpSources`.

- [ ] **Step 1: Add failing source-contract tests**

Assert a loaded disabled source remains false after toggling an unrelated skill and saving. Add a Go concurrency test proving asset enrichment updates the recent root without touching persisted/model-visible conversation data. Assert preview bindings receive the recent or manually selected workspace root rather than an empty string.

- [ ] **Step 2: Run RED**

Run: `npm --prefix frontend run test:unit && npm --prefix frontend run test:e2e -- skills-mcp.spec.mjs`

- [ ] **Step 3: Implement root and map round-trip**

Store both source maps in component state and include them in `buildScanConfig`. Record the normalized root when `resolveWorkspaceRootFromIntent` succeeds, expose it through the host/client/bridge chain, and initialize `useWorkspaceRoot` from that value before falling back to the existing `cursor-byok-mcp-workspace-root` localStorage key. Reuse the composable in `DelegationRuntimePanel.vue`. An absent workspace remains the empty user-only scope.

- [ ] **Step 4: Verify GREEN and i18n**

Run: `npm --prefix frontend run i18n:scan && npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build`

- [ ] **Step 5: Commit**

Commit: `fix(settings): preserve workspace asset sources`

### Task 7: Complete Scanner Coverage And Workspace Precedence

**Files:**
- Modify: `internal/backend/forwarder/skill_multisource.go`
- Modify: `internal/backend/forwarder/mcp_scanner.go`
- Modify: `internal/backend/forwarder/skill_multisource_test.go`
- Create or modify: `internal/backend/forwarder/mcp_scanner_test.go`

**Interfaces:**
- Produces: ordered roots/config files put workspace definitions before user definitions for the same source and identifier; Gemini workspace and supported Copilot workspace paths are included.

- [ ] **Step 1: Add temporary-tree precedence tests**

Set a temporary home, create the same skill and MCP identifier in user and workspace locations with distinguishable content/command, and assert workspace wins. Add fixtures for `.gemini/skills`, Gemini MCP configuration supported by the installed/reference CLI, and Copilot workspace instruction/skill or MCP locations confirmed by repository/reference evidence.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/forwarder -run 'Skill.*Workspace|MCP.*Workspace|Gemini|Copilot'`

- [ ] **Step 3: Reorder and extend discovery**

Build each source's workspace entries before its user entries. Add only documented paths and parsers; do not guess unsupported formats. Preserve stable output sorting after deduplication.

- [ ] **Step 4: Verify GREEN**

Run: `go test -count=1 ./internal/backend/forwarder && go test -count=1 ./... && go vet ./...`

- [ ] **Step 5: Commit**

Commit: `fix(scanner): prefer workspace agent assets`

### Task 8: Require Fingerprinted Workspace MCP Trust

**Files:**
- Create: `internal/backend/forwarder/mcp_trust.go`
- Create: `internal/backend/forwarder/mcp_trust_test.go`
- Modify: `internal/backend/server/config/types.go`
- Modify: `internal/backend/server/config/manager.go` and config tests
- Modify: `internal/bridge/proxy.go` and bridge tests
- Modify: `frontend/src/components/settings/categories/SkillsMcpSettings.vue`
- Modify: i18n catalogs and Skills/MCP Playwright tests

**Interfaces:**
- Produces: `MCPTrustFingerprint(config MCPServerConfig) string`; persisted grants keyed by runtime workspace scope, identifier, and fingerprint; explicit grant/revoke bridge calls; connection rejects untrusted workspace definitions with a typed user-action-required error.

- [ ] **Step 1: Add failing fingerprint and connection tests**

Prove argument, command, cwd, transport, source path, URL origin, header-name, or environment-variable-name changes invalidate trust while secret values are not serialized. Prove user-scope behavior remains unchanged and workspace stdio/http connection is blocked before a grant.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/forwarder ./internal/backend/server/config ./internal/bridge -run 'MCPTrust|ConnectMCP.*Trust'`

- [ ] **Step 3: Implement trust storage and hard gate**

Normalize workspace paths with `MCPRuntimeScope`, hash a canonical structured representation, persist only fingerprint metadata, and check trust immediately before registry connect or process/network startup. A scan or snapshot must never grant trust.

- [ ] **Step 4: Implement compact confirmation UI**

Before connecting an untrusted workspace server, show source path, redacted command/arguments or URL origin, cwd, and invalidation warning. On approval call grant then connect; cancellation performs neither operation.

- [ ] **Step 5: Verify GREEN**

Run: `go test -count=1 ./... && go vet ./... && npm --prefix frontend run i18n:scan && npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend run test:e2e -- skills-mcp.spec.mjs`

- [ ] **Step 6: Commit**

Commit: `feat(mcp): require workspace command trust`

### Task 9: Preserve Cursor Terminal Replay On Unsubscribe

**Files:**
- Modify: `internal/backend/forwarder/broker.go`
- Modify or create: broker/RunSSE focused tests under `internal/backend/forwarder`

**Interfaces:**
- Produces: zero-subscriber terminal streams retain backlog until `terminalStreamRetentionPeriod` expires; normal unsubscribe does not bypass the timer.

- [ ] **Step 1: Add failing reconnect test**

Complete a stream, subscribe and unsubscribe normally, immediately resubscribe, and assert terminal backlog replays. Advance an injectable clock/timer or use a short test retention to assert deletion after grace.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/forwarder -run 'Terminal.*Replay|Unsubscribe.*Retention'`

- [ ] **Step 3: Fix ownership of terminal cleanup**

Make terminal completion schedule the sole cleanup timer and prevent `Unsubscribe` from deleting a terminal stream that is inside its grace period. Preserve immediate cleanup rules for nonterminal abandoned placeholders where applicable.

- [ ] **Step 4: Verify GREEN and race behavior**

Run: `go test -race -count=1 ./internal/backend/forwarder -run 'Terminal.*Replay|Unsubscribe.*Retention' && go test -count=1 ./internal/backend/forwarder`

- [ ] **Step 5: Commit**

Commit: `fix(protocol): retain terminal replay after unsubscribe`

### Task 10: Make Bidi Append Retry Idempotent

**Files:**
- Modify: `internal/backend/forwarder/append_seq.go`
- Modify: `internal/backend/agent/protocol/inbound_test.go` or forwarder Bidi tests

**Interfaces:**
- Produces: an idle repeated `append_seqno=1` for the same request epoch is classified stale/duplicate, not a new epoch; a genuine new turn still has an explicit reset signal or new request identity.

- [ ] **Step 1: Add lost-response retry test**

Process sequence 1, reach idle, resend the identical sequence-1 intent, and assert the actor/provider receives it once. Add a separate test proving the supported new-turn path remains accepted.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/agent/protocol ./internal/backend/forwarder -run 'Append.*Retry|Sequence.*Epoch'`

- [ ] **Step 3: Remove implicit idle epoch reset**

Bind append sequence state to an explicit request/turn epoch and retain the last processed identity long enough to answer retries. Do not infer a new epoch solely from idle plus sequence 1.

- [ ] **Step 4: Verify GREEN and race behavior**

Run: `go test -race -count=1 ./internal/backend/agent/protocol ./internal/backend/forwarder -run 'Append.*Retry|Sequence.*Epoch' && go test -count=1 ./...`

- [ ] **Step 5: Commit**

Commit: `fix(protocol): deduplicate idle bidi retries`

### Task 11: Preserve Late Non-Streaming Tool Results

**Files:**
- Modify: `internal/backend/forwarder/service.go`
- Modify or create: exec-control focused tests under `internal/backend/forwarder`

**Interfaces:**
- Produces: non-streaming `stream_close` does not synthesize failure while a valid terminal result can still arrive; completion is event-driven with a conservative bounded fallback.

- [ ] **Step 1: Add a delayed-result regression test**

Send stream-close, delay the terminal result beyond 1.5 seconds using an injectable grace/event channel, then deliver success and assert exactly one successful tool result. Add a no-result test proving eventual bounded failure and cancellation behavior.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/forwarder -run 'NonStreaming.*Late|StreamClose.*Terminal'`

- [ ] **Step 3: Implement event-driven waiting**

Associate pending exec completion with a signal closed by terminal result handling. On non-streaming close, wait for the signal or a configurable conservative timeout; recheck pending state under the stream lock before synthesizing failure.

- [ ] **Step 4: Verify GREEN and race behavior**

Run: `go test -race -count=1 ./internal/backend/forwarder -run 'NonStreaming.*Late|StreamClose.*Terminal' && go test -count=1 ./...`

- [ ] **Step 5: Commit**

Commit: `fix(protocol): wait for late tool terminal results`

### Task 12: Make Cursor Proto Extraction Deterministic And Type-Safe

**Files:**
- Modify: `proto/ext_tool/extractor.go`
- Create: `proto/ext_tool/extractor_test.go`
- Add: minimal JS fixtures under `proto/ext_tool/testdata`
- Modify: task or script entry used for extraction verification

**Interfaces:**
- Produces: services resolve RPC input/output to messages only, fields and oneofs resolve the correct kind in their module/package context, and identical input yields byte-identical output.

- [ ] **Step 1: Add minimal failing fixtures**

Include a message and enum sharing nearby aliases, an RPC whose wrong nearest symbol is the enum, nested oneofs with repeated short names, and cross-module aliases. Assert RPC types are messages, oneof members match expected types, and two extraction runs have equal file hashes.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./proto/ext_tool`

Expected: fixture reproduces enum-as-RPC or wrong-oneof resolution.

- [ ] **Step 3: Separate symbol kinds and validate descriptors**

Resolve service method types only from message definitions and fail rather than fall back when ambiguous. Reset all package-level extraction state per run. Sort every generated collection. Validate descriptors and required method input/output message kinds before writing accepted output.

- [ ] **Step 4: Verify against the read-only installed client**

Copy the relevant installed Cursor JS into `.analysis-tmp`, run strict extraction twice into separate ignored directories, compare hashes, parse all generated protos, and compare checked-in protocol definitions except the intentional `go_package` difference.

- [ ] **Step 5: Run repository verification**

Run: `go test -count=1 ./proto/ext_tool ./internal/backend/agent/protocol ./internal/backend/forwarder && go vet ./...`

- [ ] **Step 6: Commit**

Commit: `fix(proto): resolve cursor protocol types deterministically`

### Task 13: Produce An Auditable Cursor Capability Map

**Files:**
- Modify: `internal/backend/forwarder/tool_catalog.go`
- Modify: `internal/backend/forwarder/tool_catalog_test.go`
- Modify or create: `cmd/sync-tool-catalog` classification logic
- Create: `docs/cursor-capability-map.md`

**Interfaces:**
- Produces: inventory entries classified as executable tool, control message, shared argument, or protocol support type; only executable tools count as mapping gaps.

- [ ] **Step 1: Add classification tests**

Use representative Task/Shell/MCP executable types and diagnostic/control/shared types. Assert only executable types enter the missing-handler set and every executable mapping has a handler/test reference or an explicit unsupported reason.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/forwarder -run 'ToolCatalog|Capability'`

- [ ] **Step 3: Implement explicit classification**

Prefer service/oneof/tool-catalog reachability evidence over the `<Name>Args` suffix heuristic. Generate the Markdown table deterministically with protocol name, class, handler, reachability test, and status.

- [ ] **Step 4: Verify GREEN**

Run the catalog generator twice and compare output, then run `go test -count=1 ./internal/backend/forwarder`.

- [ ] **Step 5: Commit**

Commit: `docs(protocol): map cursor capabilities to handlers`

### Task 14: Add Neutral Executor Identity And Registry

**Files:**
- Create: `internal/backend/delegation/executor.go`
- Create: `internal/backend/delegation/executor_registry.go`
- Create: `internal/backend/delegation/executor_registry_test.go`
- Modify: `internal/backend/delegation/scheduler.go` and scheduler tests

**Interfaces:**
- Produces: stable executor IDs, capability sets, probe state, classified errors, attempt snapshots, registry registration/probe/eligible selection, and scheduler snapshot fields `ExecutorID` and `Attempts`.

- [ ] **Step 1: Add registry and snapshot tests**

Test duplicate registration rejection, deterministic priority order, disabled/unhealthy/capability filtering, probe cache refresh, cooldown, safe snapshot cloning, and existing scheduler callers receiving empty/default executor fields.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation -run 'Executor|Scheduler.*Attempt'`

- [ ] **Step 3: Implement neutral types and registry**

Keep the current `Executor func(context.Context, TaskRequest) TaskResult` usable. Introduce the registry as a wrapper executor first, so local and Cursor paths migrate without a flag day. Classified errors implement `error` and expose failure class and retry-safety.

- [ ] **Step 4: Verify GREEN and races**

Run: `go test -race -count=1 ./internal/backend/delegation && go test -count=1 ./...`

- [ ] **Step 5: Commit**

Commit: `feat(delegation): add executor registry`

### Task 15: Add Secure Process Runner And CLI Probes

**Files:**
- Create: `internal/backend/delegation/process_runner.go`
- Create: `internal/backend/delegation/process_windows.go`
- Create: `internal/backend/delegation/process_unix.go`
- Create: `internal/backend/delegation/process_runner_test.go`
- Create: `internal/backend/delegation/executors/probe.go` and tests

**Interfaces:**
- Produces: direct executable lookup, bounded version/probe commands, sanitized environment, stdout/stderr byte limits, timeout, and full process-tree cancellation.

- [ ] **Step 1: Add helper-process tests**

Use the Go test binary as a helper child to emit stdout/stderr, exceed limits, sleep, spawn a descendant, and echo selected environment names. Assert no shell interpolation, bounded capture, secret redaction, timeout class, and descendant termination on Windows and Unix build targets.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation -run 'ProcessRunner|CLIProbe'`

- [ ] **Step 3: Implement runner and probe cache hooks**

Use `exec.CommandContext` with argument slices, explicit `Dir`, an allowlisted/overridden environment, limited writers, and OS-specific process group/job termination. Probes never prompt and have short timeouts.

- [ ] **Step 4: Verify GREEN cross-platform compile**

Run: `go test -race -count=1 ./internal/backend/delegation && go test -count=1 ./... && go vet ./... && go build ./...`

- [ ] **Step 5: Commit**

Commit: `feat(delegation): add secure cli process runner`

### Task 16: Persist Executor Configuration And Expose Probe API

**Files:**
- Modify: `internal/backend/server/config/delegation.go`
- Modify: config conversion/manager files and tests
- Modify: `internal/backend/delegation/config.go`
- Create or modify: bridge/client delegation API files
- Modify: `frontend/src/services/clientApi.js` and `frontend/src/services/browserBindings.js`

**Interfaces:**
- Produces: normalized enabled/priority/path/timeouts/failover/custom-adapter config and read-only list/refresh probe API with secrets omitted.

- [ ] **Step 1: Add config round-trip and API tests**

Assert invalid duplicate IDs, negative priorities/timeouts, secret-bearing environment values, and empty executable templates are normalized or rejected precisely. Assert old config loads with external executors disabled and existing delegation unchanged.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/server/config ./internal/backend/delegation ./internal/bridge ./internal/client -run 'Executor|DelegationConfig'`

- [ ] **Step 3: Implement persistence and API**

Store environment variable names, not secret values. Convert persisted config into runtime registry policy. Expose snapshots containing path, version, state, diagnostic code/text, capabilities, and last probe time.

- [ ] **Step 4: Verify GREEN**

Run: `go test -count=1 ./... && go vet ./... && go build ./... && npm --prefix frontend run test:unit && npm --prefix frontend run build`

- [ ] **Step 5: Commit**

Commit: `feat(config): persist delegation executors`

### Task 17: Add Claude Code Executor

**Files:**
- Create: `internal/backend/delegation/executors/claude.go`
- Create: `internal/backend/delegation/executors/claude_test.go`
- Modify: executor registration/runtime wiring

**Interfaces:**
- Produces: `claude-code` probe and non-interactive execution adapter with progress/final parsing, cancellation, and failure classification.

- [ ] **Step 1: Add fixture-driven command/parser tests**

Assert the exact non-interactive argument array, workspace directory, structured output parsing, normal final output, permission/login prompt classification, rate limit/crash classification, malformed stream evidence, and cancellation.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation/executors -run Claude`

- [ ] **Step 3: Implement the minimal adapter**

Use only flags confirmed by `claude --help` for installed version 2.1.226. Publish bounded user-visible progress and checkpoints, never private reasoning. Record version and command contract in metadata.

- [ ] **Step 4: Verify with a harmless real smoke run**

Probe `claude --version`, then execute a strict-timeout prompt requesting a fixed short token without file changes or tools. Record success or a precise user-action-required authentication state; do not modify Claude configuration.

- [ ] **Step 5: Run suite and commit**

Run: `go test -race -count=1 ./internal/backend/delegation/... && go test -count=1 ./...`

Commit: `feat(delegation): add claude code executor`

### Task 18: Add Codex CLI Executor

**Files:**
- Create: `internal/backend/delegation/executors/codex.go`
- Create: `internal/backend/delegation/executors/codex_test.go`
- Modify: executor registration/runtime wiring

**Interfaces:**
- Produces: `codex-cli` probe and non-interactive execution adapter with structured event parsing, approval/sandbox diagnosis, cancellation, and failure classification.

- [ ] **Step 1: Add fixture-driven tests**

Assert exact installed-version-supported command arguments, JSON-lines final extraction, usage when available, approval/login/rate-limit/crash/parse failure classes, bounded logs, and cancellation.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation/executors -run Codex`

- [ ] **Step 3: Implement from installed help contract**

Use only flags confirmed by `codex --help` and relevant subcommand help for version 0.147.0. Do not write global configuration or broaden sandbox/approval policy silently.

- [ ] **Step 4: Verify with a harmless real smoke run**

Probe `codex --version`, then execute a strict-timeout read-only prompt returning a fixed short token. Capture precise authentication or policy state if execution cannot proceed.

- [ ] **Step 5: Run suite and commit**

Run: `go test -race -count=1 ./internal/backend/delegation/... && go test -count=1 ./...`

Commit: `feat(delegation): add codex cli executor`

### Task 19: Add Gemini CLI Executor

**Files:**
- Create: `internal/backend/delegation/executors/gemini.go`
- Create: `internal/backend/delegation/executors/gemini_test.go`
- Modify: executor registration/runtime wiring

**Interfaces:**
- Produces: `gemini-cli` adapter for the official @google/gemini-cli contract and a stable not-installed probe state.

- [ ] **Step 1: Add absent and fixture tests**

Assert PATH absence returns installed=false without an application error. Fixture tests cover official version/help parsing, non-interactive command construction, final output, auth/action-required, rate limit, malformed output, and cancellation.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation/executors -run Gemini`

- [ ] **Step 3: Implement the adapter**

Base flags and formats on the checked reference repository/current official help. Gate eligibility by confirmed non-interactive capability and compatible version.

- [ ] **Step 4: Verify target-machine diagnosis**

Run the probe with the real PATH and assert the UI/API snapshot says not installed with an installation hint, while application startup and other executors remain healthy.

- [ ] **Step 5: Run suite and commit**

Run: `go test -count=1 ./internal/backend/delegation/... ./...`

Commit: `feat(delegation): add gemini cli executor`

### Task 20: Add Grok-Compatible And Custom CLI Executor

**Files:**
- Create: `internal/backend/delegation/executors/custom.go`
- Create: `internal/backend/delegation/executors/custom_test.go`
- Modify: executor config validation and registration

**Interfaces:**
- Produces: validated custom executor definitions with executable, argument template tokens, stdin mode, optional JSON-lines field mappings, environment-name allowlist, timeouts, and Grok-compatible display identity.

- [ ] **Step 1: Add validation and execution tests**

Reject shell metacharacter strings that require interpolation, unknown template variables, secret literal persistence, duplicate IDs, unbounded output, and missing final mappings. Prove direct argument substitution, stdin delivery, plain-text and JSON-lines results, cancellation, and not-installed diagnosis.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation/executors ./internal/backend/server/config -run 'Custom|Grok'`

- [ ] **Step 3: Implement configuration-driven adapter**

Support a fixed set of tokens such as task prompt and workspace path without invoking a shell. Register `grok-cli` only when a validated definition exists; otherwise show unconfigured/not-installed diagnosis.

- [ ] **Step 4: Verify GREEN**

Run: `go test -race -count=1 ./internal/backend/delegation/... ./internal/backend/server/config && go test -count=1 ./...`

- [ ] **Step 5: Commit**

Commit: `feat(delegation): add configurable cli executor`

### Task 21: Verify Cursor Agent Capability Separately From Editor Launch

**Files:**
- Create: `internal/backend/delegation/executors/cursor.go`
- Create: `internal/backend/delegation/executors/cursor_test.go`
- Modify: existing Cursor adapter/runtime registration
- Modify: Cursor detection snapshot/API fields

**Interfaces:**
- Produces: separate `EditorAvailable` and `AgentExecutionAvailable` states; registry eligibility requires a verified agent handshake/capability.

- [ ] **Step 1: Add detection tests**

Provide fixtures for editor-only `cursor/code` launch commands, a valid existing Cursor task bridge, and an incompatible/missing agent protocol. Assert only the verified bridge becomes eligible.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation ./internal/client -run 'Cursor.*Capability|EditorAvailable'`

- [ ] **Step 3: Implement capability adapter**

Wrap the existing `CursorAdapter.Execute` path with executor identity and probe state. Do not invent a CLI protocol. Use the running service/bridge evidence already required for Cursor-native delegation.

- [ ] **Step 4: Verify against installed Cursor read-only**

Confirm installed version/path and protocol match without writing to it, then exercise a harmless delegated task through the project runtime when a Cursor session is available.

- [ ] **Step 5: Run suite and commit**

Run: `go test -race -count=1 ./internal/backend/delegation ./internal/backend/forwarder ./internal/client && go test -count=1 ./...`

Commit: `feat(delegation): verify cursor executor capability`

### Task 22: Add Bounded Selection, Failover, And Supervision Integration

**Files:**
- Modify: `internal/backend/delegation/executor_registry.go`
- Modify: `internal/backend/delegation/scheduler.go`
- Modify: `internal/backend/forwarder/delegation_runtime.go`
- Modify: `internal/backend/forwarder/delegation_local.go`
- Modify: `internal/backend/forwarder/delegation_cursor.go`
- Modify: multitask, timeout, cancellation, and supervision tests

**Interfaces:**
- Produces: auto policy attempts each eligible executor at most once up to configured maximum; switchable failures advance, user-action-required and terminal failures stop, cancellation aborts the active attempt, and final result enters existing supervision.

- [ ] **Step 1: Add end-to-end registry executor tests**

Use fake adapters to cover priority, capability filtering, transient failover success, user-action stop, unsafe-side-effect terminal stop, max attempts, partial-output evidence, parent timeout, manual cancellation, snapshot attempts, and supervisor review of only the selected final result.

- [ ] **Step 2: Run RED**

Run: `go test -count=1 ./internal/backend/delegation ./internal/backend/forwarder -run 'ExecutorFailover|Delegation.*Executor|Supervision.*Executor'`

- [ ] **Step 3: Implement wrapper executor and attempt events**

Resolve candidates at task start, append an immutable attempt snapshot before each start, update health after completion, and return a normal `TaskResult` to the scheduler. Reuse existing checkpoint, visible-update, result, and supervisor paths.

- [ ] **Step 4: Verify GREEN and races**

Run: `go test -race -count=1 ./internal/backend/delegation ./internal/backend/forwarder && go test -count=1 ./... && go vet ./...`

- [ ] **Step 5: Commit**

Commit: `feat(delegation): add executor failover`

### Task 23: Add Compact Executor Settings And Runtime UI

**Files:**
- Modify: `frontend/src/components/settings/categories/DelegationSettings.vue`
- Modify: `frontend/src/components/DelegationRuntimePanel.vue`
- Modify: frontend state/services/bindings and unit tests
- Modify: i18n source/generated catalogs and translations
- Modify or create: delegation executor Playwright specs

**Interfaces:**
- Produces: compact backend list with status, version, toggle, priority, refresh, custom configuration, and runtime attempt details/cancel action.

- [ ] **Step 1: Add browser-preview scenarios**

Mock ready Claude/Codex, absent Gemini, unconfigured Grok, editor-only Cursor, and a task that fails over once. Assert every row and action is reachable, unavailable states have text/icon meaning, refresh updates state, invalid custom config is blocked, attempt timeline is visible, and cancel invokes the runtime API.

- [ ] **Step 2: Run RED**

Run: `npm --prefix frontend run test:e2e -- delegation-executors.spec.mjs`

- [ ] **Step 3: Implement the compact UI**

Use the existing delegation section and runtime table. Use switches for enablement, numeric priority input/stepper, refresh/settings/cancel icons with tooltips, and one modal for custom details. Avoid nested cards and per-executor dashboards.

- [ ] **Step 4: Complete i18n and responsive verification**

Run: `npm --prefix frontend run i18n:scan && npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend run test:e2e -- delegation-executors.spec.mjs`

Capture desktop and narrow screenshots and confirm no overlap or clipped labels.

- [ ] **Step 5: Commit**

Commit: `feat(ui): manage delegation executors`

### Task 24: Verify Real Reachability And Publish Final Audit

**Files:**
- Create: `docs/tasks/2026-08-10-performance-agent-fallback-verification.md`
- Modify only if evidence exposes defects: focused implementation/test files in separate fix commits before this final documentation commit

**Interfaces:**
- Produces: reproducible evidence matrix for performance budgets, scanners/trust, Cursor mappings, every executor, automatic fallback, UI reachability, cancellation, and all repository gates.

- [ ] **Step 1: Run full backend verification**

Run: `go test -race -count=1 ./internal/backend/delegation ./internal/backend/forwarder ./internal/backend/agent/protocol`

Run: `go test -count=1 ./... && go vet ./... && go build ./...`

- [ ] **Step 2: Run full frontend verification**

Run: `npm --prefix frontend run i18n:scan && npm --prefix frontend run test:unit && npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend run test:e2e`

- [ ] **Step 3: Run artifact and performance checks**

Record entry JS/CSS preload references and sizes, absence of editor/font preload, history one-walk test, bounded rendered rows, and non-overlapping polling evidence.

- [ ] **Step 4: Run real executor probes and smoke tasks**

Record Claude and Codex versions/probes and harmless smoke results. Record Gemini/Grok-compatible not-installed/unconfigured diagnostics. Exercise cancellation and one controlled fake or real transient failover through the same scheduler path.

- [ ] **Step 5: Run Cursor compatibility audit**

Strictly extract the installed client protocol twice from a copied ignored input, compare hashes and checked-in definitions, run the capability map, and exercise the relevant project against installed Cursor without modifying `D:\cursor`.

- [ ] **Step 6: Audit reachability and dead code**

For every new backend API, find its frontend or main-agent caller; for every new visible control, identify its backend handler and test. Run `rg`-based inventory and remove unreachable additions in separate focused commits before proceeding.

- [ ] **Step 7: Write evidence document and commit**

Record commands, exit status, key counts, known external-account constraints, commit sequence, and rollback points.

Commit: `docs: verify performance and agent fallback completion`

---

## Plan Self-Review

- Every design success condition maps to at least one task and fresh verification command.
- External executor UI appears only after registry, runner, config, adapters, and failover runtime exist.
- Missing Gemini/Grok installations are testable diagnostic outcomes, not skipped requirements.
- Cursor editor launch and agent execution remain distinct through Tasks 21 and 23.
- Workspace MCP trust precedes all expansion of external execution surface.
- Each task has a red test, minimal production step, green verification, and one commit.
- The plan contains no placeholder implementation steps; exact discovered files may be narrowed during a task, but interface and behavior names are fixed here.
