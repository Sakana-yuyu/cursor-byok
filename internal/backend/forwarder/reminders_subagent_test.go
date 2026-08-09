package forwarder

import (
	"strings"
	"testing"

	promptassets "cursor/prompt"
)

func TestChildConversationPreservesRequestedDeliverable(t *testing.T) {
	contract := promptassets.MustReadPrompt(promptassets.ModeSubagent) + "\n" +
		subagentContractText() + "\n" + currentModeContractText(0, true)

	for _, forbidden := range []string{
		"do not produce a long response",
		"Return only a concise investigation result",
		"不要写成长文",
		"请始终保持输出短",
	} {
		if strings.Contains(contract, forbidden) {
			t.Fatalf("child contract must not override a detailed parent deliverable with %q", forbidden)
		}
	}
	for _, required := range []string{
		"Follow the parent task's requested output format and level of detail",
		"Return the complete deliverable to the parent agent",
		"supersedes any earlier generic instruction to return only a short or concise result",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("child contract missing deliverable guarantee %q", required)
		}
	}
}
