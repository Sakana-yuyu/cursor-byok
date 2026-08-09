package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestNormalizeRealtimeRequestContextPreservesNonFileRulesOnly(t *testing.T) {
	rule := &agentv1.CursorRule{
		FullPath: "AGENTS.md",
		Content:  "Always run the focused regression test.",
	}

	got := normalizeRealtimeRequestContextForStorage(&agentv1.RequestContext{
		NonFileRules: []*agentv1.CursorRule{rule},
	})

	if got == nil {
		t.Fatal("normalizeRealtimeRequestContextForStorage() returned nil for non-file rules")
	}
	if len(got.GetNonFileRules()) != 1 {
		t.Fatalf("non-file rules = %d, want 1", len(got.GetNonFileRules()))
	}
	if got.GetNonFileRules()[0].GetContent() != rule.GetContent() {
		t.Fatalf("non-file rule content = %q, want %q", got.GetNonFileRules()[0].GetContent(), rule.GetContent())
	}
}
