package runtimecore

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestTodoStatusFromString(t *testing.T) {
	tests := []struct {
		raw    string
		want   agentv1.TodoStatus
		wantOK bool
	}{
		{raw: "pending", want: agentv1.TodoStatus_TODO_STATUS_PENDING, wantOK: true},
		{raw: "in_progress", want: agentv1.TodoStatus_TODO_STATUS_IN_PROGRESS, wantOK: true},
		{raw: "2", want: agentv1.TodoStatus(2), wantOK: true},
		{raw: "unknown", wantOK: false},
	}
	for _, tt := range tests {
		got, err := TodoStatusFromString(tt.raw)
		if tt.wantOK {
			if err != nil {
				t.Fatalf("TodoStatusFromString(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("TodoStatusFromString(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("TodoStatusFromString(%q) expected error", tt.raw)
		}
	}
}

func TestTodoStatusFromValueNumericString(t *testing.T) {
	got, err := TodoStatusFromValue("3")
	if err != nil {
		t.Fatalf("TodoStatusFromValue: %v", err)
	}
	if got != agentv1.TodoStatus(3) {
		t.Fatalf("got %v want 3", got)
	}
}
