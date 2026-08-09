# OpenAI Responses Reasoning Summary Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent OpenAI Responses reasoning summaries from being duplicated or concatenated without separators in Cursor thinking output.

**Architecture:** Track streamed reasoning summaries by Responses `item_id` and `summary_index`. Emit only unseen text for each entry, insert a newline before a new entry, and treat the completed reasoning item's `summary[]` as a fallback snapshot rather than a second append-only payload.

**Tech Stack:** Go, OpenAI Responses SSE, existing `modeladapter.ModelEvent` stream abstraction.

## Global Constraints

- Keep the change inside the OpenAI Responses model adapter and its focused tests.
- Preserve encrypted reasoning metadata and historical reasoning replay.
- Do not modify the installed Cursor client.
- Follow test-driven development: reproduce the observed event order before changing production behavior.

---

### Task 1: Reasoning Summary Stream Tracking

**Files:**
- Create: `internal/backend/agent/model/openai_reasoning_summary_test.go`
- Modify: `internal/backend/agent/model/openai_stream_responses.go`

**Interfaces:**
- Consumes: Responses summary delta identity (`item_id`, `summary_index`) and completed `summary[]` snapshots.
- Produces: ordered, non-duplicated `ModelEventKindThinkingDelta` text.

- [x] **Step 1: Write the failing regression test**

Cover three summary delta entries followed by the completed snapshot. Assert the joined emitted text is exactly three newline-separated headings and contains each heading once.

- [x] **Step 2: Run the focused test and confirm the current implementation fails**

Run: `go test ./internal/backend/agent/model -run TestOpenAIResponsesReasoningSummary -count=1`

- [x] **Step 3: Implement the minimal tracker**

Track per-entry forwarded text, add separators at entry boundaries, and reconcile completed snapshots without replaying text already emitted from deltas.

- [x] **Step 4: Run focused and package tests**

Run: `go test ./internal/backend/agent/model -run TestOpenAIResponsesReasoningSummary -count=1`

Run: `go test ./internal/backend/agent/model -count=1`

- [x] **Step 5: Inspect the diff and verify no unrelated files changed**

Run: `git diff --check`
