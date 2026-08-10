package forwarder

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"cursor/gen/agentv1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeLegacyShellToolCallPayload(t *testing.T) {
	t.Run("legacy message object upgrades to bytes", func(t *testing.T) {
		legacy := []byte(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":{"pattern":"done","reason":"completed","debounce":1.5,"notification_limit":3}}}}`)
		normalized := normalizeLegacyShellToolCallPayload(legacy)
		toolCall := &agentv1.ToolCall{}
		if err := protojson.Unmarshal(normalized, toolCall); err != nil {
			t.Fatalf("protojson.Unmarshal(normalized) error = %v\nnormalized=%s", err, normalized)
		}
		shell := toolCall.GetShellToolCall()
		if shell == nil {
			t.Fatal("shell tool call is nil after normalize")
		}
		got := shell.GetArgs().GetOutputNotification()
		if len(got) == 0 {
			t.Fatal("outputNotification bytes empty after normalize")
		}
		config := &agentv1.ShellOutputNotificationConfig{}
		if err := proto.Unmarshal(got, config); err != nil {
			t.Fatalf("unmarshal normalized bytes into config: %v", err)
		}
		if config.GetPattern() != "done" || config.GetReason() != "completed" {
			t.Fatalf("config = %#v, want pattern=done reason=completed", config)
		}
		if config.GetNotificationLimit() != 3 {
			t.Fatalf("notification_limit = %d, want 3", config.GetNotificationLimit())
		}
	})

	t.Run("new base64 format stays unchanged", func(t *testing.T) {
		configBytes, err := proto.Marshal(&agentv1.ShellOutputNotificationConfig{Pattern: "done", Reason: "completed"})
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		newFormat := []byte(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":"` + base64.StdEncoding.EncodeToString(configBytes) + `"}}}`)
		if got := string(normalizeLegacyShellToolCallPayload(newFormat)); got != string(newFormat) {
			t.Fatalf("new format payload was modified:\nbefore=%s\nafter=%s", newFormat, got)
		}
	})

	t.Run("non-shell tool call stays unchanged", func(t *testing.T) {
		payload := []byte(`{"read":{"path":"/tmp/x"}}`)
		if got := string(normalizeLegacyShellToolCallPayload(payload)); got != string(payload) {
			t.Fatalf("non-shell payload was modified: %s", got)
		}
	})

	t.Run("malformed payload stays unchanged", func(t *testing.T) {
		payload := []byte(`{not json`)
		if got := string(normalizeLegacyShellToolCallPayload(payload)); got != string(payload) {
			t.Fatalf("malformed payload was modified: %s", got)
		}
	})
}

func TestProjectPromptReplayToleratesLegacyShellOutputNotification(t *testing.T) {
	conversation := testConversation(nil)
	legacyToolCall := json.RawMessage(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":{"pattern":"done","reason":"completed"}}}}`)
	appendEntriesInPlace(conversation, []HistoryEntry{
		testUserMessageEntry(t, 1, "request-1", "run it"),
		newToolCallEntry(1, "request-1", "call-1", "Shell", "", "", legacyToolCall),
		newToolResultEntry(1, "request-1", "call-1", "Shell", `{"command":"echo hi"}`, "hi\n", "", nil),
	})

	// ProjectPromptReplay 是 run 启动（handleRunIntent:publishCheckpointForce）与
	// 终态 checkpoint 的公共解析路径：legacy outputNotification 对象不得让它报错。
	projector := NewHistoryProjector()
	messages, err := projector.ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay(legacy shell tool call) error = %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("ProjectPromptReplay returned no messages")
	}
}
