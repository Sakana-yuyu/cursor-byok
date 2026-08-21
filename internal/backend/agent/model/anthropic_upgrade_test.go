package modeladapter

import (
	"errors"
	"testing"

	legacyruntime "cursor/internal/runtime"
)

func TestUpgradeOpenAIClaudeToAnthropicUsesBaseRootAndReasoning(t *testing.T) {
	resolved := &StreamRequest{
		Provider: "openai", ProtocolMode: "auto", ProtocolGroup: "responses", BaseURL: "https://gateway.example/v1/responses?route=claude",
		ProviderModelID: "claude-test", ReasoningEffort: "high", OpenAIEndpoint: "/v1/responses", OpenAIRequestGroup: "responses",
	}
	if !upgradeOpenAIClaudeToAnthropic(resolved) {
		t.Fatal("expected Anthropic upgrade")
	}
	if resolved.Provider != "anthropic" || resolved.ProtocolGroup != "messages" {
		t.Fatalf("upgraded provider/protocol = %q/%q", resolved.Provider, resolved.ProtocolGroup)
	}
	if resolved.BaseURL != "https://gateway.example/v1?route=claude" {
		t.Fatalf("upgraded base URL = %q", resolved.BaseURL)
	}
	if resolved.AnthropicThinkingEffort != "high" {
		t.Fatalf("upgraded thinking effort = %q", resolved.AnthropicThinkingEffort)
	}
}

func TestUpgradeOpenAIClaudeFixedModeDoesNotChangeRequest(t *testing.T) {
	resolved := &StreamRequest{Provider: "openai", ProtocolMode: "fixed", BaseURL: "https://gateway.example/v1/responses", ProviderModelID: "claude-test"}
	if upgradeOpenAIClaudeToAnthropic(resolved) || resolved.Provider != "openai" {
		t.Fatalf("fixed request was changed: %#v", resolved)
	}
}

func TestShouldFallbackToOpenAIDoesNotMaskAnthropicValidation400(t *testing.T) {
	if shouldFallbackToOpenAI(&HTTPStatusError{StatusCode: 400}) {
		t.Fatal("ordinary Anthropic 400 must not trigger protocol fallback")
	}
	for _, status := range []int{404, 405} {
		if !shouldFallbackToOpenAI(&HTTPStatusError{StatusCode: status}) {
			t.Fatalf("status %d must trigger endpoint fallback", status)
		}
	}
	if shouldFallbackToOpenAI(errors.New("not an HTTP error")) {
		t.Fatal("non-HTTP error must not trigger fallback")
	}
}

func TestDowngradeAnthropicBackToOpenAIRestoresOriginalEndpoint(t *testing.T) {
	resolved := &StreamRequest{Provider: "anthropic", BaseURL: "https://gateway.example/v1"}
	channel := &legacyruntime.ResolvedChannel{ProtocolGroup: "responses", OpenAIEndpoint: "/v1/responses", OpenAIRequestGroup: "responses"}
	if !downgradeAnthropicBackToOpenAI(resolved, channel) {
		t.Fatal("expected downgrade")
	}
	if resolved.Provider != "openai" || resolved.ProtocolGroup != "responses" || resolved.OpenAIEndpoint != "/v1/responses" {
		t.Fatalf("downgraded request = %#v", resolved)
	}
}
