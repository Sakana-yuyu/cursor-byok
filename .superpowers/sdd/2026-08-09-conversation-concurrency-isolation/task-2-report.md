# Task 2 Report: Admit Runs Before Stream/History Creation

## Status

Implemented conversation-owner admission for run intents.

## Changes

- `dispatchInboundIntent` now calls `runQueue.Submit` before opening a run stream.
- Same-conversation successors queue without opening a stream or mutating persisted history.
- Duplicate owner or pending request IDs are ignored without adding queue entries.
- Different conversations can own and start runs concurrently.
- Initial stream/open/post failures release ownership through `finishConversationTurn`.
- Promoted startup failures fail an opened non-terminal stream when possible, then release ownership only if the failed request still owns the conversation, avoiding a double `Finish` if terminal handling already released it.
- Removed the ordinary same-conversation supersede cancellation from `handleRunIntent`.
- Removed unused legacy `Enqueue`, `Dequeue`, and subagent-only admission APIs while retaining `drainRunQueue` for current terminal call-site build compatibility.

## TDD Evidence

- Prior agent recorded the first RED: `TestDispatchInboundIntentQueuesBeforeOpeningSecondConversationStream` failed with `queued stream was opened before promotion`.
- Resumed focused first test: PASS.
- Added the remaining tests one at a time. Each passed on its first run because the partial admission implementation already supplied the behavior, so no false RED was manufactured:
  - `TestDispatchInboundIntentDoesNotPersistQueuedRun`
  - `TestDispatchInboundIntentRunsDifferentConversationsConcurrently`
  - `TestDispatchInboundIntentDuplicateRequestDoesNotQueueAgain`

## Verification

```text
go test ./internal/backend/forwarder -run '^(TestDispatchInboundIntent|TestRunQueue)' -count=1
ok  cursor/internal/backend/forwarder  3.376s
```

`gofmt` was run on `actor.go`, `run_queue.go`, `run_queue_test.go`, and `service.go`. `git diff --check` reported no whitespace errors.

## Notes

The focused suite initially exposed a nil provider panic from the minimal test fixture after asynchronous run handling reached provider startup. The fixture now marks scheduling test intents as prewarm runs, which keeps the owner active and exercises admission deterministically without starting a provider.

The pre-existing unrelated `service.go` ComputerUse and active-conversation changes remain outside the Task 2 commit.
