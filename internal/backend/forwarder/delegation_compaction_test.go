package forwarder

import (
	"errors"
	"fmt"
	"testing"
)

func TestDelegatedContextBudgetForWindow(t *testing.T) {
	cases := []struct {
		name   string
		window int64
		want   int64
	}{
		{"zero window disables proactive compaction", 0, 0},
		{"negative window disables", -1, 0},
		{"floor protection", 10_000, delegatedCompactionBudgetFloor},
		{"normal budget", 272_000, int64(0.8*272_000) - 10_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := delegatedContextBudgetForWindow(tc.window); got != tc.want {
				t.Fatalf("delegatedContextBudgetForWindow(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestDelegatedContextOverflowError(t *testing.T) {
	overflowErrors := []error{
		errors.New("openai responses stream error code=context_too_large: Your input exceeds the context window of this model"),
		errors.New("context_length_exceeded"),
		fmt.Errorf("wrapped: %w", errors.New("input exceeds the context window")),
	}
	for _, err := range overflowErrors {
		if !delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%q) = false, want true", err)
		}
	}
	notOverflow := []error{
		errors.New("request_timeout: stream closed before response.completed"),
		errors.New("network error"),
		nil,
	}
	for _, err := range notOverflow {
		if delegatedContextOverflowError(err) {
			t.Fatalf("delegatedContextOverflowError(%v) = true, want false", err)
		}
	}
}
