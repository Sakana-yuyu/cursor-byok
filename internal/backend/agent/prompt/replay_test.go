package promptengine

import (
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func TestBuildUserQueryReplayMessageWrapsUserQuery(t *testing.T) {
	message, ok := BuildUserQueryReplayMessage("fix the failing test")
	if !ok {
		t.Fatal("BuildUserQueryReplayMessage() ok = false")
	}
	if message.Role != "user" {
		t.Fatalf("role = %q, want user", message.Role)
	}
	if !strings.Contains(message.Content, "<user_query>") || !strings.Contains(message.Content, "fix the failing test") {
		t.Fatalf("content = %q, want wrapped user query", message.Content)
	}
}

func TestBuildUserQueryReplayMessageEmpty(t *testing.T) {
	if _, ok := BuildUserQueryReplayMessage("   "); ok {
		t.Fatal("BuildUserQueryReplayMessage() ok = true for blank input")
	}
}

func TestEncodeDecodeReplayMessagesRoundTrip(t *testing.T) {
	original := []Message{
		{
			Role:    "user",
			Content: "<user_query>\nhello\n</user_query>",
		},
		{
			Role: "assistant",
			ToolCalls: []ToolCallDescriptor{{
				ID:   "call-1",
				Type: "function",
				Function: ToolCallFunctionShape{
					Name:      "Read",
					Arguments: `{"path":"main.go"}`,
				},
			}},
			ReasoningContent: "thinking",
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			Name:       "Read",
			Content:    "package main",
		},
	}

	encoded, err := EncodeReplayMessages(original)
	if err != nil {
		t.Fatalf("EncodeReplayMessages() error = %v", err)
	}
	decoded, err := DecodeReplayMessages(encoded)
	if err != nil {
		t.Fatalf("DecodeReplayMessages() error = %v", err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("decoded len = %d, want %d", len(decoded), len(original))
	}
	if decoded[1].ToolCalls[0].Function.Name != "Read" {
		t.Fatalf("tool call name = %q, want Read", decoded[1].ToolCalls[0].Function.Name)
	}
	if decoded[2].Content != "package main" {
		t.Fatalf("tool result content = %q", decoded[2].Content)
	}
}

func TestBuildToolCallReplayMessagesAssistantAndTool(t *testing.T) {
	toolCall := &agentv1.ToolCall{
		Tool: &agentv1.ToolCall_ReadToolCall{
			ReadToolCall: &agentv1.ReadToolCall{
				Args: &agentv1.ReadToolArgs{Path: "main.go"},
				Result: &agentv1.ReadToolResult{
					Result: &agentv1.ReadToolResult_Success{
						Success: &agentv1.ReadToolSuccess{
							Output: &agentv1.ReadToolSuccess_Content{Content: "package main"},
						},
					},
				},
			},
		},
	}
	messages, ok := BuildToolCallReplayMessages("call-shell", toolCall)
	if !ok {
		t.Fatal("BuildToolCallReplayMessages() ok = false")
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Role != "assistant" || len(messages[0].ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v", messages[0])
	}
	if messages[1].Role != "tool" || messages[1].ToolCallID != "call-shell" {
		t.Fatalf("tool message = %+v", messages[1])
	}
}

func TestClientSkillPromptUsesSharedAdditiveInstructions(t *testing.T) {
	const path = "/workspace/client-skill/SKILL.md"
	for _, tc := range []struct {
		name    string
		context *agentv1.RequestContext
	}{
		{
			name: "agent skills",
			context: &agentv1.RequestContext{
				AgentSkills: []*agentv1.AgentSkill{{FullPath: path, Description: "Client skill"}},
			},
		},
		{
			name: "skill descriptors",
			context: &agentv1.RequestContext{
				SkillOptions: &agentv1.SkillOptions{SkillDescriptors: []*agentv1.SkillDescriptor{
					{ReadmeFilePath: path, Description: "Client skill"},
				}},
			},
		},
		{
			name: "combined duplicate catalogs",
			context: &agentv1.RequestContext{
				AgentSkills: []*agentv1.AgentSkill{{FullPath: path, Description: "Client skill"}},
				SkillOptions: &agentv1.SkillOptions{SkillDescriptors: []*agentv1.SkillDescriptor{
					{ReadmeFilePath: path, Description: "Duplicate client skill"},
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := buildRequestContextAgentSkillsSection(tc.context)
			if strings.Count(prompt, SkillUsageInstructions) != 1 {
				t.Fatalf("client prompt must include shared instructions exactly once: %s", prompt)
			}
			if strings.Contains(prompt, "Only use skills listed below") {
				t.Fatal("client prompt retained the obsolete exclusive allowlist instruction")
			}
			if !strings.HasPrefix(prompt, "<agent_skills>") ||
				!strings.Contains(prompt, "<available_skills description=") ||
				!strings.HasSuffix(prompt, "</available_skills>\n\n</agent_skills>") {
				t.Fatalf("client prompt lost nested catalog structure: %s", prompt)
			}
			if strings.Count(prompt, `fullPath="`+path+`"`) != 1 {
				t.Fatalf("client skill must appear exactly once: %s", prompt)
			}
		})
	}
}

func TestSkillUsageInstructionsInvocationContract(t *testing.T) {
	for _, required := range []string{
		"All <agent_skills> and <available_skills> sections are additive catalogs",
		"not mutually exclusive allowlists",
		"/skill-name or $skill-name",
		"read its SKILL.md with the Read tool and follow its instructions",
		"Reading and following SKILL.md is how skills are invoked",
		"no separate skill tool or registration step is required",
		"If an explicitly requested skill is absent from these catalogs, locate its SKILL.md",
		"do not reject it solely because it is missing from one list",
		"Respect explicit disabled-skill settings",
	} {
		if !strings.Contains(SkillUsageInstructions, required) {
			t.Errorf("shared skill instructions missing contract %q", required)
		}
	}
}
