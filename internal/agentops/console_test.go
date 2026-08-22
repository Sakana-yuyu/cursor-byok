package agentops

import (
	"encoding/json"
	"strings"
	"testing"

	"cursor/internal/backend/forwarder"
	"cursor/internal/controlcenter"
)

func TestProjectOmitsPromptAndMarksSideEffects(t *testing.T) {
	console := New(t.TempDir(), func() []forwarder.DelegationTaskSnapshot {
		return []forwarder.DelegationTaskSnapshot{{
			ID:              "run-1",
			Status:          "failed",
			ModelName:       "gpt-test",
			ToolCallCount:   2,
			Cancelable:      false,
			UpdatedAtUnixMS: 1,
			Attempts: []forwarder.DelegationExecutorAttemptSnapshot{{
				ExecutorID: "native",
				Attempt:    1,
				Status:     "failed",
				RetrySafe:  true,
			}},
		}}
	}, nil)
	page, err := console.List(RunQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "prompt") {
		t.Fatalf("leaked prompt: %s", raw)
	}
	if !page.Items[0].SideEffectObserved || page.Items[0].Retryable {
		t.Fatalf("summary = %#v", page.Items[0])
	}
}

func TestRetryWithoutPayloadFailsClosed(t *testing.T) {
	console := New(t.TempDir(), func() []forwarder.DelegationTaskSnapshot {
		return []forwarder.DelegationTaskSnapshot{{
			ID:              "run-2",
			Status:          "failed",
			ToolCallCount:   0,
			UpdatedAtUnixMS: 1,
			Attempts:        []forwarder.DelegationExecutorAttemptSnapshot{{RetrySafe: true, Status: "failed"}},
		}}
	}, nil)
	prepared, err := console.PrepareRetry("run-2")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.OriginalInputAlive {
		t.Fatal("payload should not be alive")
	}
	_, err = console.ExecuteRetry(prepared.ConfirmationToken)
	if controlcenter.ErrorCode(err) != "agent_retry_payload_unavailable" {
		t.Fatalf("err = %v", err)
	}
}
