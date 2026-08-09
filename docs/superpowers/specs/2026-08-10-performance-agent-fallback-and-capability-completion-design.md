# Performance, Agent Fallback, And Capability Completion Design

## Status And Decision

This document records the approved recommended direction for the 2026-08-10
project-wide review. The user explicitly authorized autonomous use of the
recommended approach, atomic commits, parallel audits, and implementation
without additional approval gates.

The implementation will improve three outcomes together:

1. Reduce startup, history, polling, and large-list overhead.
2. Make Claude Code, Codex CLI, Gemini CLI, configurable Grok-compatible CLI,
   and verified Cursor agent capabilities usable through one supervised
   delegation runtime with diagnosis and failover.
3. Close security, protocol, and reachability gaps so every exposed control has
   a real runtime path and every runtime capability has an observable state.

The installed Cursor client at D:\\cursor is a read-only reference. The
project must never patch, replace, or write into that installation.

## Success Conditions

The work is complete only when all of these conditions are demonstrated:

- The production entry page no longer preloads the Markdown editor bundle, but
  opening the Skills/MCP editor still loads and operates it on demand.
- A history refresh obtains session size, debug size, and debug-file presence
  through one recursive directory walk per session.
- Polling cannot overlap the same request, large history collections do not
  eagerly expand every retained session, and the application avoids loading a
  multi-megabyte bundled font at startup.
- Workspace MCP commands cannot execute before explicit trust is established
  for that workspace and command definition.
- Workspace Skills and MCP assets are scanned with the actual workspace root,
  workspace definitions have the documented precedence, and source enablement
  choices survive a settings save.
- Every external agent backend has a typed identity, installation/authentication
  health, capability set, execution lifecycle, cancellation path, bounded logs,
  and classified failure result.
- Claude Code and Codex CLI pass real local probe and smoke-execution tests.
  Gemini and Grok-compatible backends return precise not-installed status on
  this machine while remaining executable when configured or installed.
- Cursor/Code is advertised as an agent executor only after a real agent
  protocol is detected; an editor-launch command alone is not enough.
- The main Cursor agent can select an external executor, receive its result,
  cancel it, and fail over according to policy without bypassing scheduler
  limits, checkpointing, supervision, or task snapshots.
- Cursor protocol extraction is deterministic and type-correct, checked-in
  protocol mappings are auditable, and reconnect/retry/late-result edge cases
  have regression coverage.
- The UI presents executor health and task state compactly in the existing
  delegation settings/runtime surfaces, with all new text present in every
  registered locale.
- Fresh Go tests, race-focused tests for changed concurrency paths, vet, Go
  build, frontend unit tests, i18n scan, lint, production build, targeted
  Playwright flows, and live runtime probes pass.

## Scope Decomposition

The objective spans four independently reviewable workstreams. Each workstream
produces usable software on its own and is divided into atomic commits.

### Workstream A: Performance

Remove the editor chunk from entry preloads, aggregate history metadata in one
walk, serialize polling, reduce eager history rendering, and replace the global
10.97 MB TTF dependency with the operating-system font stack unless a smaller
existing WOFF2 asset is already available.

These changes must preserve editor reachability, history totals, refresh
semantics, and visual layout. Build-output assertions and focused unit tests
will prevent regressions that source-only tests cannot see.

### Workstream B: Workspace Asset Safety And Completeness

Treat workspace-discovered MCP commands as executable code. Discovery may show
an untrusted definition, but connection and process launch require a persisted
trust decision bound to a normalized workspace identity and a fingerprint of
the command, arguments, environment-variable names, transport, and source file.
Changing the definition invalidates trust.

The scanner will receive the active workspace root instead of an empty string,
cover supported Cursor, Claude, Gemini, and Copilot workspace locations, and
apply deterministic precedence: workspace over user over bundled. Settings
saves will round-trip skillSources and mcpSources instead of silently
reconstructing defaults. Disabled sources remain disabled after restart.

### Workstream C: Cursor Protocol Reliability And Mapping

The installed extension protocol is parsed as structured protobuf syntax. RPC
request/response types, message fields, enum types, oneofs, streaming markers,
and package qualification must come from the syntax tree or descriptor model,
not cross-section regular-expression matching. Generation must be stable across
two consecutive runs and strict validation must reject enum-as-message RPCs and
invalid oneof member types.

The feature inventory will distinguish executable tools from shared/control
messages before reporting mapping gaps. A generated report will record each
Cursor tool or capability, its project handler, reachability test, and status:
mapped, intentionally unsupported with reason, or missing.

Three audited stream behaviors are fixed with tests:

- Normal RunSSE unsubscribe retains terminal replay data for the configured
  reconnect grace period instead of deleting it immediately.
- An idle retry of append_seqno=1 cannot create a new epoch and replay an
  already processed intent after a response is lost.
- Non-streaming stream_close waits on an event or a conservative bounded
  late-result window instead of fabricating failure after 1.5 seconds.

### Workstream D: Unified External Agent Runtime

External CLIs integrate as executor adapters under the existing
internal/backend/delegation scheduler. They do not introduce a second task
server, queue, concurrency controller, or checkpoint store.

The design separates two concepts that are currently conflated:

- Execution mode: auto, cursor, or local, describing how a task is delegated at
  the product level.
- Executor ID: local-provider, cursor-agent, claude-code, codex-cli, gemini-cli,
  grok-cli, or a stable custom adapter ID, identifying the concrete runtime.

Task snapshots, metrics, supervision evidence, and UI labels carry executor ID
without changing existing callers that omit it.

## Unified Executor Contract

The delegation package owns a narrow executor interface and neutral types. The
exact names may follow local conventions, but the boundary provides identity,
probe, start, wait, and cancel operations. Probe results include executable
path, resolved version, installation state, authentication state when detectable
without changing user state, supported capabilities, diagnostic code, and last
probe time.

Adapters translate a neutral task request into their CLI contract and translate
stdout, stderr, or structured events into progress, tool-like evidence, final
output, usage when available, and classified errors. They use argument arrays,
an explicit working directory, a sanitized environment, bounded output capture,
process-tree cancellation on Windows, and no shell interpolation.

## Built-In Adapters

### Claude Code

Detect the resolved claude executable and version, use its documented
non-interactive print or stream format, keep authentication diagnosis separate
from installation, and map permission or login prompts to user-action-required.

### Codex CLI

Detect the resolved codex executable and version, use a documented
non-interactive execution mode with structured output when supported, and map
sandbox, approval, authentication, and rate-limit outcomes explicitly.

### Gemini CLI

Detect the official @google/gemini-cli executable. Absence is a healthy
diagnostic state, not a startup error. The adapter becomes eligible only after
version and non-interactive capability checks succeed.

### Grok-Compatible And Custom CLI

No undocumented binary name or output protocol is treated as official Grok.
The built-in Grok-compatible slot is configuration-driven: executable,
argument template, input transport, optional JSON-lines mappings, environment
allowlist, and cancellation behavior. The same implementation supports custom
CLI adapters with validation and secret redaction.

### Cursor And Code

Cursor/Code detection records editor launch capability separately. It becomes
an executor only when a supported agent protocol, command, or RPC handshake is
proved. Otherwise the UI states that the editor is available but agent execution
is unavailable, and automatic routing never selects it.

## Selection, Health, And Failover

The scheduler receives an ordered executor policy. Auto selects only eligible
executors whose required capabilities match the task. Selection uses configured
priority, recent health, active load, and a cooldown after transient failure.
Existing local and Cursor delegation remain valid candidates.

Failures belong to exactly one class:

1. switchable: executable disappeared, compatible-version check failed,
   transient network or rate-limit failure, process crash, structured-output
   parse failure, or health cooldown. Auto may try the next eligible executor.
2. user_action_required: login, interactive permission, missing credential,
   workspace trust, or configuration correction. The task stops with a precise
   action; silently switching would hide a security or account decision.
3. terminal: cancellation, invalid task, policy rejection, or a result proving
   that another executor could duplicate an unsafe side effect.

Failover is bounded by a maximum attempt count and one attempt per executor.
Every attempt is visible in the task timeline. Partial output is retained as
evidence but is not presented as a successful final result. Supervision reviews
the selected final result through the existing coordinator.

## Data Flow

1. The main agent or user submits a normal delegation task with an optional
   executor policy and workspace hint.
2. The scheduler admits the task under existing queue and concurrency rules.
3. The executor registry resolves eligible adapters from cached probe state and
   task capabilities.
4. The selected adapter starts a child process or verified Cursor run and emits
   neutral events into the existing checkpoint and event pipeline.
5. Cancellation propagates from UI, parent request, timeout, or supervision to
   the run handle and its process tree.
6. A classified failure either advances to the next candidate or ends with a
   diagnostic result. Success becomes the scheduler result and flows back to
   the Cursor main agent exactly like an existing delegated result.
7. Runtime snapshots expose the attempt list, executor, phase, health, duration,
   truncated logs, and action-required state to the compact UI.

## Configuration And Persistence

Configuration extends the existing delegation config and normalization flow.
It stores enabled executors, priority, executable overrides, probe timeout,
execution timeout, failover limit, per-adapter non-secret options, and custom
adapter definitions. Secrets remain referenced by environment-variable name or
the project's existing secret mechanism; API keys and raw sensitive environment
values are never returned to the frontend or persisted in task snapshots.

Defaults preserve current behavior. Existing users who never enable an external
executor continue to use the same local or Cursor delegation paths. Invalid or
missing external configuration produces diagnostics without preventing startup.

## UI Design

The existing Delegation settings category gains one compact execution-backends
section. It uses a table or list, status icon, backend name, version, short
health text, enable toggle, priority control, refresh icon, and overflow menu
for detailed configuration. It does not create a separate dashboard or a card
per CLI.

The runtime panel adds an executor label and expands an existing task row to
show attempts, bounded logs, cancellation, and required action. Installed but
unavailable, not installed, unauthenticated, unhealthy, and ready have distinct
text and icons, not color-only meaning.

Workspace MCP trust uses the existing confirmation or modal pattern and shows
the source file, executable, arguments with secrets redacted, working directory,
and the fact that a changed definition requires approval again.

Every new source message follows the repository i18n workflow: source catalog,
generated catalogs, all registered translations, scanner validation, and
browser assertions for the Chinese and English primary paths.

## Performance Budgets And Observability

- index.html must not module-preload the Markdown editor chunk or its CSS.
- Executor probes are bounded and concurrent but use a small fixed limit; they
  never block the main UI startup path.
- Polling uses completion-based scheduling or an in-flight guard. Slow calls do
  not create accumulating concurrent requests.
- Logs are line and byte bounded in memory and snapshots; full debug output
  follows the existing debug-log storage policy and secret redaction.
- History metadata collection exposes walk count and duration in tests or a
  test hook, ensuring later changes do not restore repeated traversal.
- Task metrics record executor ID, attempt count, failure class, queue time,
  execution time, cancellation, and failover outcome.

## Testing Strategy

Every behavior change follows red-green-refactor and receives an atomic commit.
Tests are placed at the lowest useful boundary:

- Build artifact tests inspect production index.html and chunk references.
- Go unit tests cover one-pass history aggregation, probe parsing, registry
  eligibility, failure classification, failover limits, environment redaction,
  command construction, cancellation, and configuration normalization.
- Protocol tests reproduce terminal replay, append retry idempotency, late
  non-streaming tool results, deterministic extraction, and mapping inventory.
- Scanner and config tests use temporary user and workspace trees to prove
  coverage, precedence, trust invalidation, and source-switch round trips.
- Frontend unit tests cover normalization and state rendering; Playwright covers
  settings reachability, probe refresh, disabled and unavailable states, custom
  CLI validation, task cancellation, and workspace trust.
- Runtime smoke tests execute harmless prompts through installed Claude Code and
  Codex CLI with strict timeouts. Missing Gemini and Grok paths assert precise
  diagnostics.
- Final verification includes full repository tests and builds plus an installed
  Cursor session using this project, while keeping D:\\cursor read-only.

## Atomic Delivery Order

1. Design and implementation plan documentation.
2. Markdown editor preload removal.
3. One-pass history metadata collection.
4. Non-overlapping polling and duplicate initial refresh removal.
5. History rendering and font startup reductions.
6. Workspace root, source round-trip, scanner coverage, and precedence fixes.
7. Workspace MCP trust gate.
8. Cursor stream reliability fixes.
9. Deterministic protocol extraction and capability mapping report.
10. Executor identity and neutral registry types.
11. Probe runtime and compact settings UI.
12. Claude Code adapter.
13. Codex CLI adapter.
14. Gemini CLI adapter.
15. Grok-compatible and custom adapter.
16. Verified Cursor capability adapter and editor-only diagnosis.
17. Scheduler selection, classified failover, cancellation, and supervision.
18. Runtime panel, logs, metrics, and end-to-end reachability.
19. Full verification and final audit documentation.

Each item is independently tested, reviewed, and committed. No commit may expose
a selectable backend or visible control before its runtime path and diagnostic
behavior are implemented.

## Non-Goals

- Replacing the existing delegation scheduler with T3 Code or another server.
- Installing missing CLIs, logging into user accounts, or modifying third-party
  CLI configuration without an explicit user action.
- Treating editor launch commands as autonomous agent protocols.
- Copying reference-project code without license review and adaptation to this
  repository's interfaces.
- Writing into the installed Cursor application.
- Expanding the UI into independent pages for every executor.

## Risks And Controls

- External CLI output formats may change. Version-gated parsers, raw bounded
  evidence, and precise parse-failure classification keep failures diagnosable.
- Automatic failover may duplicate side effects. Only pre-execution or explicitly
  retry-safe failures are switchable; adapters mark side-effect uncertainty.
- Workspace MCP definitions execute arbitrary code. Fingerprinted trust is a
  hard gate and cannot be bypassed by auto-connect.
- Protocol extraction can silently produce plausible invalid types. Descriptor
  validation and two-run determinism checks are required before mappings update.
- Broad changes can become unreviewable. The atomic delivery order and fresh
  verification before every commit provide rollback points and bounded diffs.
