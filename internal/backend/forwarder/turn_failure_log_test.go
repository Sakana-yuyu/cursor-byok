package forwarder

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFormatTurnFailureCause(t *testing.T) {
	t.Run("nil cause formats to empty", func(t *testing.T) {
		if got := formatTurnFailureCause(nil); got != "" {
			t.Fatalf("want empty, got %q", got)
		}
	})

	t.Run("keeps wrapped chain message on a single line", func(t *testing.T) {
		wrapped := fmt.Errorf("append turn entries: %w", errors.New("decode conversation state \"c1\": unexpected EOF"))
		got := formatTurnFailureCause(wrapped)
		if !strings.Contains(got, "decode conversation state") {
			t.Fatalf("want wrapped root message preserved, got %q", got)
		}
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("want single line, got %q", got)
		}
	})

	t.Run("collapses multi-line provider output", func(t *testing.T) {
		got := formatTurnFailureCause(errors.New("provider error:\nline-two\r\nline-three"))
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("want collapsed whitespace, got %q", got)
		}
		if !strings.Contains(got, "line-two") || !strings.Contains(got, "line-three") {
			t.Fatalf("want content preserved after collapsing, got %q", got)
		}
	})

	t.Run("truncates oversized causes with marker", func(t *testing.T) {
		got := formatTurnFailureCause(errors.New(strings.Repeat("x", 2000)))
		if len(got) > 512 {
			t.Fatalf("want bounded length, got %d", len(got))
		}
		if !strings.Contains(got, "(truncated)") {
			t.Fatalf("want truncation marker, got %q", got)
		}
	})
}
