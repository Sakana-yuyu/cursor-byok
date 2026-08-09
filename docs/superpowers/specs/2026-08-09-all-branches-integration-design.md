# All Branches Integration Design

## Goal

Consolidate every local feature branch into `main` without regressing the
newer conversation-isolation implementation or losing the error-resilience
feature set. Remove obsolete local branches only after the integrated tree is
verified and every branch tip is an ancestor of the final `main`.

## Inputs

- `main` already contains the completed conversation owner scheduler and the
  current local snapshot, including ComputerUse, settings, reminders, OpenAI
  reasoning summaries, and related documentation.
- `fix/conversation-concurrency-isolation-impl` contains the detailed commit
  history that produced the scheduler now represented on `main`.
- `fix/conversation-concurrency-isolation` is an earlier test-only ancestor of
  the implementation branch.
- `feat/error-resilience` adds the unified application error contract, provider
  retry classification, runtime health recovery, diagnostics, frontend status
  UI, translations, and tests.
- `fix/subagent-black-window-flashing` is already an ancestor of `main`.

## Integration Strategy

1. Work from a clean integration branch based on the latest `main`.
2. Merge the completed conversation-isolation implementation branch with a
   merge commit. When equivalent changes conflict, keep the newer `main`
   implementation while preserving the source branch as merged history.
3. Merge `feat/error-resilience` with a merge commit. Resolve conflicts by
   composing both behaviors: the `main` scheduler remains authoritative for
   run ownership and lifecycle, while error classification, retry policy,
   tracing, health state, and diagnostics remain intact.
4. Regenerate i18n catalogs through the repository scanner rather than
   manually resolving generated reference churn.
5. Verify targeted packages first, then run the complete Go test suite,
   frontend unit tests, lint, and production build.
6. Fast-forward `main` to the verified integration branch. Remove other local
   branch worktrees only after confirming their untracked files are already
   present identically on final `main`.

## Conflict Rules

- Preserve `runQueue` owner/FIFO admission, exact owner release, queued cancel,
  shutdown gating, terminal checkpoint handling, and conversation-aware cache
  keys from current `main`.
- Apply `apperror` classification at failure boundaries without bypassing the
  scheduler's terminalization and queue-release paths.
- Keep retry decisions provider-local and avoid retrying after user-visible
  partial stream output.
- Keep model-visible history append-only and conversation-scoped. Error
  metadata may enrich diagnostics but must not reorder persisted prompt history.
- Keep Chinese source UI messages and regenerate all locale catalogs.
- Preserve all current `main` ComputerUse and settings work.

## Verification Gate

The integration is acceptable only when:

- no unmerged paths or conflict markers remain;
- scheduler, error-contract, retry, runtime-health, and safety tests pass;
- `go test ./...` passes;
- frontend JavaScript tests, lint, and production build pass;
- generated locale files have matching, non-empty keys;
- every deleted local branch tip is reachable from final `main`; and
- the original `main` worktree remains clean except for changes created by the
  final fast-forward itself.
