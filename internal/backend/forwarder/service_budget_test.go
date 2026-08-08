package forwarder

import (
	"strings"
	"testing"

	modeladapter "cursor/internal/backend/agent/model"
)

func TestValidateProviderRequestContextBudgetRejectsOverflowAfterOutputBudgeting(t *testing.T) {
	conversation := &ConversationFile{TokenDetailsMaxTokens: 2_000}
	compiled := CompiledConversation{
		Messages: []modeladapter.Message{{Role: "user", Content: strings.Repeat("x", 3_000)}},
	}

	err := validateProviderRequestContextBudget(conversation, compiled, 1)
	if err == nil {
		t.Fatal("validateProviderRequestContextBudget() error = nil, want context overflow")
	}
	terminal, ok := err.(compactionTerminalError)
	if !ok {
		t.Fatalf("error type = %T, want compactionTerminalError", err)
	}
	if terminal.TerminalCode() != compactionOverflowTerminalCode {
		t.Fatalf("terminal code = %q, want %q", terminal.TerminalCode(), compactionOverflowTerminalCode)
	}
}

func TestValidateProviderRequestContextBudgetAllowsExactSafetyBoundary(t *testing.T) {
	compiled := CompiledConversation{Messages: []modeladapter.Message{{Role: "user", Content: "hello"}}}
	inputTokens := estimateCompiledPromptTokens(compiled)
	conversation := &ConversationFile{TokenDetailsMaxTokens: uint32(inputTokens + 7 + providerOutputSafetyTokens)}

	if err := validateProviderRequestContextBudget(conversation, compiled, 7); err != nil {
		t.Fatalf("validateProviderRequestContextBudget() error = %v, want nil", err)
	}
}
