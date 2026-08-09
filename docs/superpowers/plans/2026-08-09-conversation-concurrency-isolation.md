# Conversation Concurrency Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serialize turns FIFO within one `conversationID` while preserving true concurrency and complete context isolation between different conversations, even when they share one provider/model/channel.

**Architecture:** Replace the subagent-only `runQueue` with an atomic conversation scheduler that owns exactly one active request per conversation plus a FIFO of pending intents. Admission happens before `streamForIntent`, so queued intents cannot open streams, persist history, or start providers; every broker terminal path releases the exact owner idempotently and asynchronously starts at most one successor. Existing per-conversation file storage, prompt replay, provider correlation, OpenAI prompt cache routing, provider retry, channel health, and shared HTTP transport remain unchanged and receive regression coverage rather than new channel-level locking.

**Tech Stack:** Go 1.24, protobuf-generated agent messages, existing forwarder actor/broker/store/projector stack, Go `testing`, `go test -race`.

## Global Constraints

- The only scheduling and context-isolation key is normalized `conversationID`; never provider, model, API key, host, project path, or normalized channel ID.
- Different conversations remain concurrent even inside one project and on the same provider/model/channel.
- One conversation has at most one nonterminal owner; later requests queue FIFO and ordinary user messages never supersede the owner.
- A queued intent cannot create an `ActiveStream`, allocate/persist a turn, write history/context/checkpoint state, or enter the provider before owner acquisition.
- Explicit cancel targets only its `requestID`: active owner cancellation follows the existing terminal flow; queued cancellation removes only that entry.
- Owner release is idempotent, verifies both `conversationID` and `requestID`, and atomically promotes at most one FIFO successor.
- Terminal persistence/checkpoint/broker work completes before owner release. If terminal persistence fails, do not silently start a successor from incomplete history.
- Preserve model-visible semantic history append-only ordering and existing prefix-cache behavior; never reorder or delete historical messages for this change.
- Preserve provider pre-output reconnect, mid-stream no-replay behavior, channel health/cooldown state, and shared HTTP/2 transport behavior.
- Preserve `ProviderRequest{RequestID, ConversationID, ModelCallID}` correlation and OpenAI `prompt_cache_key = cursor:<conversationID>`.
- A non-empty broker `requestID` binding is immutable across conversations; history append fails closed when stream, conversation, request, or turn identity conflicts.
- Do not add `maxConcurrentStreams`, a settings/config field, frontend UI, or channel semaphore.
- Do not log credentials, full provider request bodies, private prompt content, or local config secrets.
- Preserve all pre-existing dirty-worktree changes, including ComputerUse and history-active-state work; do not commit or push unless the user explicitly requests it.

## File Structure

- `internal/backend/forwarder/run_queue.go`: atomic conversation owner/FIFO scheduler and service-level promotion/finalization helpers.
- `internal/backend/forwarder/run_queue_test.go`: scheduler unit tests and focused service admission/terminal tests.
- `internal/backend/forwarder/actor.go`: run admission before stream creation and asynchronous owner-start failure recovery.
- `internal/backend/forwarder/service.go`: remove ordinary supersede, cancel queued requests, route complete/fail/cancel/shutdown terminal paths through the scheduler.
- `internal/backend/forwarder/blob_sync.go`: route checkpoint cancellation/failure terminals through the scheduler.
- `internal/backend/forwarder/compaction.go`: release conversation ownership after manual compaction terminals.
- `internal/backend/forwarder/context_projection_test.go`: integration coverage for cross-conversation concurrent provider entry and replay/history isolation.
- `internal/backend/forwarder/projector_test.go`: focused prompt/tool replay isolation if the integration fixture does not isolate projector behavior clearly enough.
- `internal/backend/forwarder/provider_cache_fingerprint_test.go`: prove complete request content **and conversation identity** separate local cache entries; add the minimal production cache-key field if the new test fails.
- `internal/backend/forwarder/broker.go` and broker tests: reject reusing one non-empty `requestID` for a different conversation.
- `internal/backend/forwarder/checkpoint_memory.go` and focused tests: reject history append when stream/conversation/request/turn identity conflicts.

---

### Task 1: Atomic Conversation Scheduler

**Files:**
- Replace: `internal/backend/forwarder/run_queue.go:1-131`
- Create: `internal/backend/forwarder/run_queue_test.go`

**Interfaces:**
- Consumes: `InboundIntent` from `types.go`.
- Produces:

```go
type runQueueSubmitResult string

const (
    runQueueStart     runQueueSubmitResult = "start"
    runQueueQueued    runQueueSubmitResult = "queued"
    runQueueDuplicate runQueueSubmitResult = "duplicate"
)

type queuedRunCancellation struct {
    Intent   InboundIntent
    Position int
}

func newRunQueue() *runQueue
func (q *runQueue) Submit(intent InboundIntent) (result runQueueSubmitResult, ownerRequestID string, queuePosition int)
func (q *runQueue) Finish(conversationID string, requestID string) (next InboundIntent, ok bool)
func (q *runQueue) CancelQueued(conversationID string, requestID string) (queuedRunCancellation, bool)
func (q *runQueue) IsOwner(conversationID string, requestID string) bool
func (q *runQueue) Owner(conversationID string) string
func (q *runQueue) Len(conversationID string) int
```

- `Submit` normalizes IDs, returns `start` while atomically recording an empty conversation’s owner, `duplicate` when the request is already owner or queued, and otherwise appends FIFO and returns its one-based queue position.
- `Finish` returns no successor unless both IDs match the current owner; a matching finish atomically promotes the queue head to owner and removes only that head. Repeated/stale finish therefore returns `(InboundIntent{}, false)`.
- `CancelQueued` never removes the owner; it removes exactly one matching pending request and compacts the slice.

- [ ] **Step 1: Write scheduler failing tests**

Create table-driven tests with real `runQueue` operations:

```go
func TestRunQueueSerializesOneConversationFIFO(t *testing.T) {
    queue := newRunQueue()
    first := testRunIntent("conversation-a", "request-1")
    second := testRunIntent("conversation-a", "request-2")
    third := testRunIntent("conversation-a", "request-3")

    result, _, _ := queue.Submit(first)
    if result != runQueueStart { t.Fatalf("first submit = %q", result) }
    result, owner, position := queue.Submit(second)
    if result != runQueueQueued || owner != "request-1" || position != 1 {
        t.Fatalf("second submit = %q owner=%q position=%d", result, owner, position)
    }
    result, _, position = queue.Submit(third)
    if result != runQueueQueued || position != 2 { t.Fatalf("third position = %d", position) }

    next, ok := queue.Finish("conversation-a", "request-1")
    if !ok || next.RequestID != "request-2" { t.Fatalf("first finish next = %#v ok=%t", next, ok) }
    next, ok = queue.Finish("conversation-a", "request-2")
    if !ok || next.RequestID != "request-3" { t.Fatalf("second finish next = %#v ok=%t", next, ok) }
    if next, ok = queue.Finish("conversation-a", "request-3"); ok || next.RequestID != "" {
        t.Fatalf("last finish next = %#v ok=%t", next, ok)
    }
}

func TestRunQueueAllowsDifferentConversationsToOwnConcurrently(t *testing.T) {
    queue := newRunQueue()
    for _, intent := range []InboundIntent{
        testRunIntent("conversation-a", "request-a"),
        testRunIntent("conversation-b", "request-b"),
    } {
        if result, _, _ := queue.Submit(intent); result != runQueueStart {
            t.Fatalf("submit %s = %q", intent.RequestID, result)
        }
    }
    if queue.Owner("conversation-a") != "request-a" || queue.Owner("conversation-b") != "request-b" {
        t.Fatalf("owners = %q, %q", queue.Owner("conversation-a"), queue.Owner("conversation-b"))
    }
}
```

Add separate tests named:
- `TestRunQueueConcurrentSubmitElectsOneOwner`
- `TestRunQueueDuplicateOwnerOrPendingDoesNotEnqueueTwice`
- `TestRunQueueCancelQueuedRemovesOnlyTarget`
- `TestRunQueueFinishIsIdempotentAndRejectsWrongOwner`

Use a start barrier and goroutines in `TestRunQueueConcurrentSubmitElectsOneOwner`; assert exactly one `runQueueStart`, all others are queued, and no duplicate request appears.

- [ ] **Step 2: Run scheduler tests and verify RED**

Run:

```bash
go test ./internal/backend/forwarder -run '^TestRunQueue' -count=1
```

Expected: compile failure because `Submit`, `Finish`, `CancelQueued`, `Owner`, and result constants do not exist. This is the feature-missing RED, not a fixture/import failure.

- [ ] **Step 3: Implement the minimal atomic scheduler**

Use one mutex and one map of per-conversation state:

```go
type conversationRunState struct {
    ownerRequestID string
    pending        []InboundIntent
}

type runQueue struct {
    mu     sync.Mutex
    states map[string]*conversationRunState
}
```

Normalize `ConversationID` and `RequestID` on every public method. Do not inspect the broker from inside the scheduler. Clear removed intent slots before reslicing for GC. Delete a conversation state only when its final owner finishes and no pending item remains.

- [ ] **Step 4: Run scheduler tests and verify GREEN**

Run:

```bash
go test ./internal/backend/forwarder -run '^TestRunQueue' -count=1
go test -race ./internal/backend/forwarder -run '^TestRunQueue' -count=1
```

Expected: PASS, including concurrent owner election under the race detector.

---

### Task 2: Admit Runs Before Stream/History Creation

**Files:**
- Modify: `internal/backend/forwarder/actor.go:133-171`
- Modify: `internal/backend/forwarder/run_queue.go`
- Modify: `internal/backend/forwarder/service.go:726-936`
- Test: `internal/backend/forwarder/run_queue_test.go`

**Interfaces:**
- Consumes: Task 1 `runQueue.Submit`, `runQueue.Finish`, `runQueue.IsOwner`.
- Produces:

```go
func (service *Service) startOwnedRun(intent InboundIntent) error
func (service *Service) finishConversationTurn(conversationID string, requestID string)
func (service *Service) startPromotedRun(intent InboundIntent)
```

- `dispatchInboundIntent` calls scheduler admission only for `Kind == "run"`.
- `startOwnedRun` performs `streamForIntent` and posts `streamCommandRun` asynchronously; non-run intents keep existing dispatch semantics.
- `finishConversationTurn` calls `Finish`; if it promotes a successor, it launches `startPromotedRun` via the repository’s safe goroutine helper rather than blocking the terminal actor.
- `startPromotedRun` may call `startOwnedRun`; if startup fails before a broker terminal exists, log safe IDs, attempt to publish a failed terminal when a stream exists, release that promoted owner, and continue promotion so the queue cannot wedge.

- [ ] **Step 1: Write admission failing tests**

Build a minimal `Service` with `NewStreamBroker()` and `newRunQueue()`. Add:

```go
func TestDispatchInboundIntentQueuesBeforeOpeningSecondConversationStream(t *testing.T) {
    service := testConversationSchedulingService(t)
    first := testRunIntent("conversation-a", "request-1")
    second := testRunIntent("conversation-a", "request-2")

    if err := service.dispatchInboundIntent(first); err != nil { t.Fatal(err) }
    if err := service.dispatchInboundIntent(second); err != nil { t.Fatal(err) }

    if _, ok := service.broker.Get("request-1"); !ok { t.Fatal("owner stream was not opened") }
    if _, ok := service.broker.Get("request-2"); ok { t.Fatal("queued stream was opened before promotion") }
    if got := service.runQueue.Len("conversation-a"); got != 1 { t.Fatalf("queue len = %d", got) }
}
```

Use a controlled provider/service fixture or actor mailbox stub so the owner can remain active without introducing sleeps. Add:
- `TestDispatchInboundIntentDoesNotPersistQueuedRun`
- `TestDispatchInboundIntentRunsDifferentConversationsConcurrently`
- `TestDispatchInboundIntentDuplicateRequestDoesNotQueueAgain`

For the persistence test, load `ConversationFileStore` after dispatching the queued request and assert no `HistoryEntry.RequestID == "request-2"` and no request-2 context item exists.

- [ ] **Step 2: Run admission tests and verify RED**

Run:

```bash
go test ./internal/backend/forwarder -run '^TestDispatchInboundIntent' -count=1
```

Expected: the second same-conversation stream exists or history includes request-2 because current dispatch only queues when a subagent is active.

- [ ] **Step 3: Implement scheduler admission and remove supersede**

In `dispatchInboundIntent`, before `streamForIntent`:

```go
if strings.TrimSpace(intent.Kind) == "run" {
    result, ownerRequestID, queuePosition := service.runQueue.Submit(intent)
    switch result {
    case runQueueQueued:
        logger.Infof("forwarder conversation run queued request_id=%s conversation_id=%s owner_request_id=%s queue_len=%d queue_position=%d",
            strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), ownerRequestID,
            service.runQueue.Len(intent.ConversationID), queuePosition)
        return nil
    case runQueueDuplicate:
        logger.Infof("forwarder duplicate conversation run ignored request_id=%s conversation_id=%s owner_request_id=%s",
            strings.TrimSpace(intent.RequestID), strings.TrimSpace(intent.ConversationID), ownerRequestID)
        return nil
    case runQueueStart:
        // continue into stream creation
    }
}
```

Delete the ordinary `cancelOtherConversationActors(..., "[canceled] Superseded by newer request")` block from `handleRunIntent`. Retain `shouldReuseActiveRun` for RunSSE/repeated active-request idempotency, but do not use it to merge different request IDs.

If initial stream creation or actor command posting fails after `Submit` returned `start`, call `finishConversationTurn` before returning so the owner cannot wedge. A queued intent must never reach `streamForIntent`.

- [ ] **Step 4: Run admission tests and verify GREEN**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestDispatchInboundIntent|TestRunQueue)' -count=1
```

Expected: PASS; the queued request has no stream/history entry, and different conversations each have an active stream.

---

### Task 3: Explicit Queued Cancellation and Idempotent Terminal Promotion

**Files:**
- Modify: `internal/backend/forwarder/service.go:960-1045,1105-1213,3146-3218,3392-3472`
- Modify: `internal/backend/forwarder/run_queue.go`
- Test: `internal/backend/forwarder/run_queue_test.go`

**Interfaces:**
- Consumes: `runQueue.CancelQueued`, `finishConversationTurn` from Tasks 1-2.
- Produces:

```go
func (service *Service) cancelQueuedRun(intent InboundIntent) (handled bool, err error)
```

- The queued request has no broker stream, so cancellation is scheduler-first when broker lookup misses.
- `cancelQueuedRun` uses both normalized `ConversationID` and `RequestID`. If incoming cancel lacks a conversation ID, add a scheduler lookup that scans pending IDs under the same mutex and only succeeds when exactly one queued request matches; this keeps the public cancel protocol compatible without guessing an owner.
- Queued cancellation removes only that pending intent and logs `conversation_id`, `request_id`, previous owner, queue position, and new queue length. It does not create history or cancel the active owner.
- Active complete/fail/cancel calls `finishConversationTurn` only after broker terminal publication succeeds or confirms that exact stream is already terminal.

- [ ] **Step 1: Write queued cancel and terminal failing tests**

Add:
- `TestHandleCancelIntentRemovesQueuedRunWithoutCancelingOwner`
- `TestActiveCompletionPromotesExactlyOneQueuedRun`
- `TestActiveFailurePromotesExactlyOneQueuedRun`
- `TestActiveCancellationPromotesExactlyOneQueuedRun`
- `TestRepeatedTerminalFinalizationDoesNotPromoteTwice`
- `TestPromotedRunStartupFailureContinuesFIFO`

The first test submits owner + two pending requests, cancels the first pending request, and asserts:

```go
if !service.runQueue.IsOwner("conversation-a", "request-1") { t.Fatal("owner changed") }
if service.runQueue.Len("conversation-a") != 1 { t.Fatal("wrong queue length") }
next, ok := service.runQueue.Finish("conversation-a", "request-1")
if !ok || next.RequestID != "request-3" { t.Fatalf("next = %#v ok=%t", next, ok) }
```

Terminal tests use a recording start hook/provider to count promoted starts and assert one terminal call starts only the queue head; a duplicate terminal call starts nothing else.

- [ ] **Step 2: Run cancellation/terminal tests and verify RED**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestHandleCancelIntent|TestActive|TestRepeatedTerminal|TestPromotedRun)' -count=1
```

Expected: queued cancel returns `request is not active`, or repeated/scattered drains can promote incorrectly/miss promotion.

- [ ] **Step 3: Implement queued cancel and centralized terminal release**

At the start of `handleCancelIntent`, when `broker.Get(intent.RequestID)` misses, try `cancelQueuedRun(intent)` before returning `request is not active`.

Replace these manual drains with one exact owner release:
- successful completion after `broker.Complete`
- active cancellation after `broker.Cancel`
- `failActiveStream` after `broker.Fail`

Do not call `finishConversationTurn` before `recordTurnFinalizedSnapshot`, terminal checkpoint completion, or broker terminal status. Make owner release idempotent through `runQueue.Finish`, not through timing checks in the broker.

During `Shutdown`, prevent promotion of queued work while the service is shutting down. Add a scheduler/service close gate that clears pending queues and makes `finishConversationTurn` release without starting successors; then force-cancel active owners. Test this behavior rather than allowing shutdown cancellation to start new provider calls.

- [ ] **Step 4: Run terminal tests and verify GREEN**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestRunQueue|TestDispatchInboundIntent|TestHandleCancelIntent|TestActive|TestRepeatedTerminal|TestPromotedRun)' -count=1
```

Expected: PASS with exactly one promotion per matching owner terminal and no cross-conversation effect.

---

### Task 4: Cover Checkpoint and Manual Compaction Terminal Paths

**Files:**
- Modify: `internal/backend/forwarder/blob_sync.go:300-387`
- Modify: `internal/backend/forwarder/compaction.go:486-655`
- Modify: `internal/backend/forwarder/blob_sync_test.go`
- Test: `internal/backend/forwarder/run_queue_test.go`

**Interfaces:**
- Consumes: `finishConversationTurn(conversationID, requestID)`.
- Produces: no new exported interface; every direct `broker.Complete/Fail/Cancel` terminal also releases its exact conversation owner after safe terminal publication.

- [ ] **Step 1: Write failing terminal-path tests**

Extend checkpoint fixtures with:
- `TestCheckpointCancellationPromotesQueuedConversationRun`
- `TestCheckpointSyncFailurePromotesQueuedConversationRun`

Add focused manual compaction tests:
- `TestManualCompactionCompletionPromotesQueuedConversationRun`
- `TestManualCompactionNoopPromotesQueuedConversationRun`

Each test registers `request-1` as scheduler owner and `request-2` as pending, executes the exact production terminal helper, and asserts request-2 becomes the scheduler owner exactly once. For checkpoint sync failure, assert the broker terminal code remains `checkpoint_sync_error` before promotion.

- [ ] **Step 2: Run terminal-path tests and verify RED**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestCheckpoint.*Promotes|TestManualCompaction.*Promotes)' -count=1
```

Expected: `failTerminalCheckpointSync` and manual compaction direct completion leave request-1 as owner or never start request-2.

- [ ] **Step 3: Route all direct broker terminals through finalization**

- In `finishCanceledTurnAfterCheckpoint`, replace `drainRunQueue` with `finishConversationTurn` after `broker.Cancel` succeeds.
- In `failTerminalCheckpointSync`, call `finishConversationTurn` after `broker.Fail` succeeds.
- After both manual compaction completion sites call `broker.Complete`, set the phase if needed and call `finishConversationTurn`.
- Keep failure branches that already call `failStream` unchanged; `failActiveStream` is their single scheduler release.
- Do not release on nonterminal checkpoint publication, compaction resume, or provider reconnect paths.

- [ ] **Step 4: Run forwarder terminal suites and verify GREEN**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestCheckpoint|TestManualCompaction|TestRunQueue|TestActive)' -count=1
```

Expected: PASS; every direct terminal promotes once, while nonterminal paths do not promote.

---

### Task 5: Cross-Conversation Provider and Context Isolation Integration

**Files:**
- Modify: `internal/backend/forwarder/context_projection_test.go`
- Modify only if evidence requires: `internal/backend/forwarder/projector_test.go`
- Modify only if evidence requires: `internal/backend/forwarder/provider_cache_fingerprint_test.go`

**Interfaces:**
- Consumes: existing `newServiceWithDependencies`, `contextProjectionRequestProvider`, `ConversationFileStore`, `HistoryProjector.ProjectPromptReplay`, and `ProviderRequest` correlation fields.
- Produces: regression evidence; production projector/provider/cache code changes are forbidden unless a failing isolation assertion proves a defect.

- [ ] **Step 1: Write cross-conversation concurrency test**

Add a blocking provider with `entered chan ProviderRequest` and `release chan struct{}`. Dispatch two runs with distinct conversations but the same `ModelID`, provider config, and project/workspace path. Wait until both requests enter `StartStream` before releasing either. Assert:

```go
seen := map[string]ProviderRequest{}
for len(seen) < 2 {
    select {
    case req := <-provider.entered:
        seen[req.ConversationID] = req
    case <-time.After(5 * time.Second):
        t.Fatal("different conversations did not enter provider concurrently")
    }
}
if seen["conversation-a"].RequestID != "request-a" || seen["conversation-b"].RequestID != "request-b" {
    t.Fatalf("provider correlation mismatch: %#v", seen)
}
```

Name it `TestDifferentConversationsSharingModelEnterProviderConcurrently`.

- [ ] **Step 2: Write replay/history/tool isolation test**

Create/store two conversations with unique sentinel user text, assistant text, and tool result. Project each independently and assert A’s serialized messages contain only A sentinels and B’s contain only B sentinels. Then drive one turn per conversation through the service and assert:
- each persisted entry’s `RequestID` belongs to that conversation;
- each captured `ProviderRequest.ConversationID` matches its request;
- per-conversation `NextTurnSeq`, `NextEntrySeq`, and `ContextVersion` advance independently;
- no checkpoint projection from A contains B’s tool call/result or vice versa.

Name it `TestConversationPromptReplayAndCheckpointRemainIsolated`.

- [ ] **Step 3: Run isolation tests and establish RED/GREEN honestly**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestDifferentConversationsSharingModelEnterProviderConcurrently|TestConversationPromptReplayAndCheckpointRemainIsolated)$' -count=1
```

Expected:
- The concurrency test must fail before Tasks 1-4 if same-conversation/global scheduling is wrong; after those tasks it must pass.
- Pure projector isolation may already pass because `ProjectPromptReplay` accepts one `ConversationFile`; record it as characterization coverage rather than forcing a fake production change.

- [ ] **Step 4: Add cache and defensive identity assertions**

In `provider_cache_fingerprint_test.go`, add a regression assertion that two otherwise identical `ProviderRequest` values with different `ConversationID` values produce different `providerCacheKey` values. Also retain an assertion that requests with different provider-visible messages/tool results differ on the same model/channel. This follows the isolation requirement that the local response-cache hit key contains complete provider-visible request content **and** session-related fields; an exact prompt match across conversations must not replay another conversation’s completed stream.

Update `providerCacheKeyShape`/`providerCacheKey` only as needed to include the normalized `ConversationID` (never API keys or private raw bodies beyond the existing canonical request shape). Keep the OpenAI model test asserting different `ConversationID` values produce different `openAIPromptCacheKey` values. Do not modify shared HTTP transport or router health state.

Add broker coverage that opens `request-shared` for `conversation-a`, then attempts to open the same request ID for `conversation-b`; require an error and assert the original stream binding remains `conversation-a`. In `StreamBroker.OpenStream`, reject only the case where both existing and incoming conversation IDs are non-empty and differ, so an early RunSSE placeholder with empty conversation can still be populated normally.

Add focused checkpoint-memory coverage that calls `appendConversationEntries` with a conversation ID different from `stream.ConversationID`, or entries whose non-empty `RequestID`/positive `TurnSeq` differ from the owning stream, and require an error before store/checkpoint mutation. Permit metadata entries with an empty request ID or zero turn only where existing history semantics already use those sentinel values; do not rewrite or reorder valid history.

- [ ] **Step 5: Run isolation and cache suites**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestDifferentConversations|TestConversationPrompt|TestProviderCache|TestResponseCache)' -count=1
go test ./internal/backend/agent/model -run 'PromptCacheKey' -count=1
```

Expected: PASS; same model/channel does not imply shared prompt/history state, while conversation-scoped provider cache routing stays distinct.

---

### Task 6: Race, Regression, and Documentation Verification

**Files:**
- Modify: `docs/superpowers/specs/2026-08-09-conversation-concurrency-isolation-design.md:4`
- Review only: all files changed in Tasks 1-5.

**Interfaces:**
- Consumes: completed implementation and tests.
- Produces: verified implementation with the design status updated from implementation-pending to implemented only after all checks pass.

- [ ] **Step 1: Format and inspect the exact diff**

Run:

```bash
gofmt -w internal/backend/forwarder/run_queue.go \
  internal/backend/forwarder/run_queue_test.go \
  internal/backend/forwarder/actor.go \
  internal/backend/forwarder/service.go \
  internal/backend/forwarder/blob_sync.go \
  internal/backend/forwarder/compaction.go \
  internal/backend/forwarder/blob_sync_test.go \
  internal/backend/forwarder/context_projection_test.go \
  internal/backend/forwarder/projector_test.go \
  internal/backend/forwarder/provider_cache_fingerprint_test.go
git diff --check
git diff -- internal/backend/forwarder docs/superpowers/specs/2026-08-09-conversation-concurrency-isolation-design.md
```

Expected: no whitespace errors; no frontend/config/channel-concurrency changes; no API keys or full provider bodies.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/backend/forwarder -run '^(TestRunQueue|TestDispatchInboundIntent|TestHandleCancelIntent|TestActive|TestRepeatedTerminal|TestPromotedRun|TestCheckpoint|TestManualCompaction|TestDifferentConversations|TestConversationPrompt|TestProviderCache|TestResponseCache)' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full forwarder and model tests**

Run:

```bash
go test ./internal/backend/forwarder ./internal/backend/agent/model -count=1
```

Expected: PASS.

- [ ] **Step 4: Run race verification**

Run:

```bash
go test -race ./internal/backend/forwarder -run '^(TestRunQueue|TestDispatchInboundIntent|TestHandleCancelIntent|TestActive|TestRepeatedTerminal|TestPromotedRun|TestCheckpoint.*Promotes|TestManualCompaction.*Promotes|TestDifferentConversations)' -count=1
```

Expected: PASS with no race reports.

- [ ] **Step 5: Run broader Go regression**

Run:

```bash
go test ./internal/backend/... ./internal/bridge/... ./internal/client/... -count=1
```

Expected: PASS. If an unrelated pre-existing failure remains, capture its exact package/test/output and verify that focused changed-package tests still pass; do not claim a clean full regression.

- [ ] **Step 6: Self-review every requirement**

Verify from code and tests:
- different conversations concurrently enter provider on the same model/channel;
- same conversation has one atomic owner and FIFO pending turns;
- ordinary supersede string/path is gone from run handling;
- queued cancel affects only its request;
- complete/fail/cancel/checkpoint/manual-compaction paths release once;
- queued requests cannot write history before promotion;
- projector/tool/checkpoint/provider/cache correlation is conversation-safe; local response-cache keys include conversation identity;
- one request ID cannot be rebound across conversations, and mismatched history append is rejected before mutation;
- shutdown does not start queued work;
- no `maxConcurrentStreams` or frontend/config addition exists.

- [ ] **Step 7: Update design status only after verification**

Change the design header to:

```markdown
**状态：** 已实施并通过并发、隔离与竞态测试
```

Do not commit or push. Report changed files, exact verification commands/results, and any remaining upstream correlated-disconnect limitation: conversation scheduling preserves concurrency and prevents local context races, but it does not serialize independent conversations to hide supplier/HTTP2 failures.
