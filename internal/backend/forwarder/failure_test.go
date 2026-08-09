package forwarder

import (
	"errors"
	"strings"
	"testing"

	"cursor/internal/apperror"
)

func TestNormalizeStreamFailureProducesSafeTraceableError(t *testing.T) {
	cause := errors.New("provider failed token=super-secret")
	got := normalizeStreamFailure(&ActiveStream{RequestID: "request-123"}, cause)

	if got == nil {
		t.Fatal("normalizeStreamFailure() returned nil")
	}
	if got.TraceID != "request-123" || got.Operation != "forwarder.stream" {
		t.Fatalf("normalized error = trace %q operation %q", got.TraceID, got.Operation)
	}
	if got.Code != apperror.CodeInternal || got.Disposition != apperror.DispositionFatal {
		t.Fatalf("normalized error = code %q disposition %q", got.Code, got.Disposition)
	}
	if strings.Contains(got.TechnicalMessage, "super-secret") || strings.Contains(got.UserMessage, "super-secret") {
		t.Fatalf("normalized error leaks credential: %#v", got)
	}
}

func TestFailWithDetailsPublishesStructuredTerminalMetadata(t *testing.T) {
	broker := NewStreamBroker()
	if _, err := broker.OpenStream("request-456", "conversation-456", 7, "model", "model", 1, ""); err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	failure := TerminalFailure{
		Code:              "provider_error",
		Message:           "供应商暂时不可用",
		TraceID:           "request-456",
		AppErrorCode:      "provider_unavailable",
		Disposition:       "retryable",
		RetryAttemptCount: 3,
	}
	if err := broker.FailWithDetails("request-456", failure); err != nil {
		t.Fatalf("FailWithDetails() error = %v", err)
	}

	events, err := broker.ReadFromCursor("request-456", 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("terminal events = %d, want 1", len(events))
	}
	got := events[0]
	if !got.End || got.TerminalErrorCode != failure.Code || got.TerminalErrorMessage != failure.Message {
		t.Fatalf("terminal event = %#v", got)
	}
	if got.TerminalTraceID != failure.TraceID || got.TerminalAppErrorCode != failure.AppErrorCode || got.TerminalDisposition != failure.Disposition || got.TerminalRetryAttemptCount != failure.RetryAttemptCount {
		t.Fatalf("terminal diagnostics = trace %q code %q disposition %q attempts %d", got.TerminalTraceID, got.TerminalAppErrorCode, got.TerminalDisposition, got.TerminalRetryAttemptCount)
	}
}

func TestFailWithDetailsDoesNotOverrideCompletedStream(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("request-completed", "conversation-completed", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete(stream.RequestID, "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if err := broker.FailWithDetails(stream.RequestID, TerminalFailure{Code: "late_failure"}); err != nil {
		t.Fatalf("FailWithDetails() error = %v", err)
	}

	if stream.Status != StreamStatusCompleted {
		t.Fatalf("stream status = %q, want %q", stream.Status, StreamStatusCompleted)
	}
	events, err := broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != 1 || !events[0].End || events[0].TerminalErrorCode != "" {
		t.Fatalf("terminal events = %#v, want only the completed event", events)
	}
}

func TestFailWithDetailsDoesNotOverrideCanceledStream(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("request-canceled", "conversation-canceled", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Cancel(stream.RequestID, "user canceled"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	if err := broker.FailWithDetails(stream.RequestID, TerminalFailure{Code: "late_failure"}); err != nil {
		t.Fatalf("FailWithDetails() error = %v", err)
	}

	if stream.Status != StreamStatusCanceled {
		t.Fatalf("stream status = %q, want %q", stream.Status, StreamStatusCanceled)
	}
	events, err := broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != 1 || !events[0].End || events[0].TerminalErrorCode != "canceled" {
		t.Fatalf("terminal events = %#v, want only the canceled event", events)
	}
}

func TestCancelDoesNotOverrideCompletedStream(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream("request-complete-cancel", "conversation-complete-cancel", 1, "model", "model", 1, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	if err := broker.Complete(stream.RequestID, "", "done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if err := broker.Cancel(stream.RequestID, "late cancel"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	if stream.Status != StreamStatusCompleted {
		t.Fatalf("stream status = %q, want %q", stream.Status, StreamStatusCompleted)
	}
	events, err := broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != 1 || !events[0].End || events[0].TerminalErrorCode != "" {
		t.Fatalf("terminal events = %#v, want only the completed event", events)
	}
}
