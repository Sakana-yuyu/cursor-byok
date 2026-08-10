package forwarder

import (
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"cursor/internal/backend/delegation"
)

func TestIsTransientNativeSubagentFailure(t *testing.T) {
	tests := []struct {
		name      string
		error     string
		wantRetry bool
	}{
		{name: "request timeout", error: "Server Error openai responses stream error code=request_timeout: stream error: stream disconnected before completion: stream closed before response.completed", wantRetry: true},
		{name: "503 upstream connect", error: "Server Error openai adapter status=503 body=upstream connect error or disconnect/reset before headers", wantRetry: true},
		{name: "502 bad gateway", error: "openai adapter status=502 body=bad gateway", wantRetry: true},
		{name: "connection refused", error: "transport failure reason: delayed connect error: Connection refused", wantRetry: true},
		{name: "stream disconnected before completion", error: "stream closed before response.completed", wantRetry: true},
		{name: "context too large", error: "openai responses stream error code=context_length_exceeded", wantRetry: false},
		{name: "tool logic error", error: "file not found: /etc/missing", wantRetry: false},
		{name: "empty error", error: "", wantRetry: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isTransientNativeSubagentFailure(test.error); got != test.wantRetry {
				t.Fatalf("isTransientNativeSubagentFailure(%q) = %t, want %t", test.error, got, test.wantRetry)
			}
		})
	}
}

func TestMaybeAutoRetryNativeSubagentReDispatches(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation(nil)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "delegate work"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}, broker)
	stream, err := broker.OpenStream(
		"request-1",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"delegate work",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)

	oldExecID := "exec-subagent-attempt-1"
	toolCallID := "call-task-1"
	oldPending := runtimecore.PendingExec{
		MessageID:        5,
		ExecID:           oldExecID,
		ExecKind:         "subagent",
		ToolCallID:       toolCallID,
		ModelCallID:      "model-call-1",
		ArgsJSON:         []byte(`{"description":"delegate work","subagent_type":"generalPurpose"}`),
		OpenedAt:         time.Now().Add(-5 * time.Minute),
		ReasoningContent: "delegate this task",
	}
	stream.mu.Lock()
	stream.PendingExecs[oldExecID] = oldPending
	stream.mu.Unlock()
	service.nativeDelegations = map[string]*nativeDelegationRuntime{
		oldExecID: {
			ID:              oldExecID,
			ParentRequestID: stream.RequestID,
			ConversationID:  stream.ConversationID,
			ToolCallID:      toolCallID,
			Status:          delegation.TaskRunning,
		},
	}

	transientErr := "Server Error openai adapter status=503 body=upstream connect error or disconnect/reset before headers"

	if !service.maybeAutoRetryNativeSubagent(stream, oldPending, transientErr) {
		t.Fatal("maybeAutoRetryNativeSubagent() = false for transient failure, want true")
	}

	stream.mu.Lock()
	_, oldGone := stream.PendingExecs[oldExecID]
	newExecID := ""
	newKind := ""
	newToolCallID := ""
	for id, pending := range stream.PendingExecs {
		newExecID = id
		newKind = pending.ExecKind
		newToolCallID = pending.ToolCallID
	}
	stream.mu.Unlock()
	if oldGone {
		t.Fatal("old exec still in PendingExecs, want it removed")
	}
	if newExecID == "" || newExecID == oldExecID {
		t.Fatalf("new exec id = %q, want a fresh exec different from %q", newExecID, oldExecID)
	}
	if newKind != "subagent" {
		t.Fatalf("new exec kind = %q, want subagent", newKind)
	}
	if newToolCallID != toolCallID {
		t.Fatalf("new exec tool call id = %q, want original %q (same Task invocation)", newToolCallID, toolCallID)
	}

	service.delegationRuntimeMu.Lock()
	_, oldRuntimePresent := service.nativeDelegations[oldExecID]
	newRuntime := service.nativeDelegations[newExecID]
	service.delegationRuntimeMu.Unlock()
	if oldRuntimePresent {
		t.Fatal("old native runtime still registered, want removed")
	}
	if newRuntime == nil {
		t.Fatal("new native runtime not registered")
	}
	if newRuntime.Status != delegation.TaskRunning {
		t.Fatalf("new runtime status = %q, want running", newRuntime.Status)
	}
	if newRuntime.ProviderRetryCount != 1 {
		t.Fatalf("new runtime ProviderRetryCount = %d, want 1", newRuntime.ProviderRetryCount)
	}
}

func TestMaybeAutoRetryNativeSubagentGuards(t *testing.T) {
	newServiceAndStream := func(t *testing.T) (*Service, *ActiveStream) {
		t.Helper()
		store := NewConversationFileStore(t.TempDir())
		conversation := testConversation(nil)
		appendEntriesInPlace(conversation, []HistoryEntry{
			testUserMessageEntry(t, 1, "request-1", "delegate work"),
		})
		persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
		if err != nil {
			t.Fatalf("SaveConversationWithEntries() error = %v", err)
		}
		broker := NewStreamBroker()
		service := newServiceWithDependencies(store, NewHistoryProjector(), contextProjectionLifecycleCompiler{}, &contextProjectionRequestProvider{requests: make(chan ProviderRequest, 1)}, broker)
		stream, err := broker.OpenStream(
			"request-1",
			persisted.ConversationID,
			1,
			"model-a",
			"model-a",
			agentv1.AgentMode_AGENT_MODE_AGENT,
			"delegate work",
		)
		if err != nil {
			t.Fatalf("OpenStream() error = %v", err)
		}
		stream.CheckpointConversation = cloneConversationFile(persisted)
		return service, stream
	}
	transientErr := "Server Error openai adapter status=503 body=upstream connect error"
	pending := runtimecore.PendingExec{ExecID: "exec-guard", ExecKind: "subagent", ToolCallID: "call-guard", ArgsJSON: []byte(`{"description":"x","subagent_type":"generalPurpose"}`)}

	t.Run("non-transient error does not retry", func(t *testing.T) {
		service, stream := newServiceAndStream(t)
		service.nativeDelegations = map[string]*nativeDelegationRuntime{
			"exec-guard": {ID: "exec-guard", Status: delegation.TaskRunning},
		}
		if service.maybeAutoRetryNativeSubagent(stream, pending, "file not found") {
			t.Fatal("maybeAutoRetryNativeSubagent() = true for non-transient error, want false")
		}
		stream.mu.Lock()
		_, still := stream.PendingExecs["exec-guard"]
		stream.mu.Unlock()
		if still {
			t.Fatal("non-transient failure removed pending exec, want left intact")
		}
	})

	t.Run("retry count at max does not retry", func(t *testing.T) {
		service, stream := newServiceAndStream(t)
		service.nativeDelegations = map[string]*nativeDelegationRuntime{
			"exec-guard": {ID: "exec-guard", Status: delegation.TaskRunning, ProviderRetryCount: nativeSubagentTransientRetryMax},
		}
		if service.maybeAutoRetryNativeSubagent(stream, pending, transientErr) {
			t.Fatal("maybeAutoRetryNativeSubagent() = true at retry cap, want false")
		}
		stream.mu.Lock()
		_, still := stream.PendingExecs["exec-guard"]
		stream.mu.Unlock()
		if still {
			t.Fatal("capped retry removed pending exec, want left intact")
		}
	})

	t.Run("terminal stream does not retry", func(t *testing.T) {
		service, stream := newServiceAndStream(t)
		service.nativeDelegations = map[string]*nativeDelegationRuntime{
			"exec-guard": {ID: "exec-guard", Status: delegation.TaskRunning},
		}
		stream.mu.Lock()
		stream.Status = StreamStatusFailed
		stream.mu.Unlock()
		if service.maybeAutoRetryNativeSubagent(stream, pending, transientErr) {
			t.Fatal("maybeAutoRetryNativeSubagent() = true on terminal stream, want false")
		}
	})

	t.Run("missing runtime does not retry", func(t *testing.T) {
		service, stream := newServiceAndStream(t)
		service.nativeDelegations = map[string]*nativeDelegationRuntime{}
		if service.maybeAutoRetryNativeSubagent(stream, pending, transientErr) {
			t.Fatal("maybeAutoRetryNativeSubagent() = true with no runtime, want false")
		}
		if !strings.Contains(stream.ConversationID, "conversation") {
			t.Fatalf("unexpected conversation id %q", stream.ConversationID)
		}
	})
}
