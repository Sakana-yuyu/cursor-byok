# Delegated Stream Timeout Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent native subagents and locally delegated workers from being terminated by parent-oriented 30-second SSE chunk and 90-second provider-idle timeouts.

**Architecture:** Preserve existing parent-agent timeout behavior. Allow each model request to carry an explicit provider-idle timeout, derive the SSE chunk-read timeout from that request, and set a delegation-specific timeout on local worker requests and native child-agent requests. Existing delegation watchdogs remain the final no-progress limits.

**Tech Stack:** Go, `net/http`, streaming SSE adapters, Go tests.

## Global Constraints

- Parent-agent requests keep the current configured provider idle timeout and default 30-second SSE chunk timeout.
- Delegated requests use a longer timeout without changing provider-visible request content.
- Native subagent detection must rely on runtime request ownership, not model ID.
- No retries after partial provider output; existing duplicate-output protection remains unchanged.

---

### Task 1: Preserve Explicit Provider Idle Timeout

**Files:**
- Modify: `internal/backend/agent/model/router.go`
- Test: `internal/backend/agent/model/router_timeout_test.go`

**Interfaces:**
- Consumes: `StreamRequest.ProviderStreamIdleTimeout time.Duration`, `ChannelResolver.ProviderStreamIdleTimeout(context.Context) time.Duration`
- Produces: router behavior that applies the resolver timeout only when the request did not provide one.

- [ ] **Step 1: Write the failing test**

Add a test around the timeout selection behavior with literal durations: explicit `10*time.Minute` must remain `10*time.Minute`; zero must fall back to resolver `90*time.Second`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/agent/model -run TestResolveProviderStreamIdleTimeout -count=1`
Expected: FAIL because explicit request timeout is currently overwritten or the selection helper does not exist.

- [ ] **Step 3: Write minimal implementation**

Add a focused selection helper and call it from `Router.streamChannel`:

```go
func resolveProviderStreamIdleTimeout(requested, configured time.Duration) time.Duration {
    if requested > 0 {
        return requested
    }
    return configured
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/backend/agent/model -run TestResolveProviderStreamIdleTimeout -count=1`
Expected: PASS.

### Task 2: Derive SSE Chunk Timeout Per Request

**Files:**
- Modify: `internal/backend/agent/model/stream_idle.go`
- Modify: `internal/backend/agent/model/openai_stream_responses.go`
- Modify: `internal/backend/agent/model/openai_stream_chat.go`
- Modify: `internal/backend/agent/model/anthropic_stream.go`
- Modify: `internal/backend/agent/model/gemini.go`
- Test: `internal/backend/agent/model/stream_idle_test.go`

**Interfaces:**
- Consumes: request-level provider idle timeout.
- Produces: `providerStreamChunkTimeout(idleTimeout time.Duration) time.Duration` and `resetStreamReadDeadline(resp *http.Response, timeout time.Duration)`.

- [ ] **Step 1: Write the failing test**

Assert literal behavior: zero or 90-second idle timeout returns 30 seconds; a 10-minute delegated idle timeout returns 150 seconds; timeout never drops below 30 seconds.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/agent/model -run TestProviderStreamChunkTimeout -count=1`
Expected: FAIL because chunk timeout is currently a fixed constant.

- [ ] **Step 3: Write minimal implementation**

Derive chunk timeout as one quarter of normalized idle timeout, clamped to at least 30 seconds, and pass it to all four streaming scanners.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/backend/agent/model -run 'TestProviderStreamChunkTimeout|TestResolveProviderStreamIdleTimeout' -count=1`
Expected: PASS.

### Task 3: Mark Delegated Provider Requests

**Files:**
- Modify: `internal/backend/forwarder/delegation_local.go`
- Modify: `internal/backend/forwarder/service.go`
- Test: `internal/backend/forwarder/delegation_local_test.go` or a focused existing forwarder test file.

**Interfaces:**
- Consumes: `ProviderRequest.Role`, active native delegation runtime keyed by child request/conversation ownership.
- Produces: delegation-specific `ProviderStreamIdleTimeout` forwarded through `DefaultProviderGateway`.

- [ ] **Step 1: Write the failing test**

Verify local worker requests carry the delegated timeout and a request recognized as a native child-agent request receives the same timeout while ordinary parent requests remain unset.

- [ ] **Step 2: Run test to verify it fails**

Run the focused forwarder test selected during implementation.
Expected: FAIL because `ProviderRequest` currently has no explicit timeout field and parent/native requests are not distinguished.

- [ ] **Step 3: Write minimal implementation**

Add `ProviderStreamIdleTimeout` to `ProviderRequest`, forward it in `DefaultProviderGateway`, set it directly for local worker requests, and set it in the main provider path only when the active request belongs to a native subagent runtime.

- [ ] **Step 4: Run test to verify it passes**

Run the focused forwarder test and `go test ./internal/backend/forwarder -count=1`.
Expected: PASS.

### Task 4: Verify Regression Scope

**Files:**
- No new production files.

- [ ] **Step 1: Format modified Go files**

Run `gofmt` on all changed Go files.

- [ ] **Step 2: Run model and forwarder tests**

Run: `go test ./internal/backend/agent/model ./internal/backend/forwarder -count=1`
Expected: PASS.

- [ ] **Step 3: Run repository Go tests**

Run: `go test ./... -count=1`
Expected: PASS or report exact unrelated failures without claiming success.

- [ ] **Step 4: Review diff**

Confirm parent defaults remain 90 seconds/30 seconds, delegated requests are explicitly isolated, and no provider-visible messages or tool schemas changed.
