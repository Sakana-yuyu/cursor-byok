package forwarder

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	runtimecore "cursor/internal/backend/agent/core"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeLegacyShellToolCallPayload(t *testing.T) {
	t.Run("已有结构化对象字节不变", func(t *testing.T) {
		legacy := []byte(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":{"pattern":"done","reason":"completed","debounce":1.5,"notification_limit":3}}}}`)
		normalized := normalizeLegacyShellToolCallPayload(legacy)
		if !bytes.Equal(normalized, legacy) {
			t.Fatalf("已有对象必须保持字节不变: %s", normalized)
		}
		toolCall := &agentv1.ToolCall{}
		if err := protojson.Unmarshal(normalized, toolCall); err != nil {
			t.Fatalf("protojson.Unmarshal(normalized) error = %v\nnormalized=%s", err, normalized)
		}
		shell := toolCall.GetShellToolCall()
		if shell == nil {
			t.Fatal("shell tool call is nil after normalize")
		}
		got := shell.GetArgs().GetOutputNotification()
		if got == nil {
			t.Fatal("outputNotification message empty after normalize")
		}
		config := got
		if config.GetPattern() != "done" || config.GetReason() != "completed" {
			t.Fatalf("config = %#v, want pattern=done reason=completed", config)
		}
		if config.GetNotificationLimit() != 3 {
			t.Fatalf("notification_limit = %d, want 3", config.GetNotificationLimit())
		}
	})

	t.Run("旧base64转换为结构化message", func(t *testing.T) {
		configBytes, err := proto.Marshal(&agentv1.ShellOutputNotificationConfig{Pattern: "done", Reason: "completed"})
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		newFormat := []byte(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":"` + base64.StdEncoding.EncodeToString(configBytes) + `"}}}`)
		got := normalizeLegacyShellToolCallPayload(newFormat)
		message := &agentv1.ToolCall{}
		if err := protojson.Unmarshal(got, message); err != nil {
			t.Fatalf("旧bytes应可按新schema回放: %v", err)
		}
		if message.GetShellToolCall().GetArgs().GetOutputNotification().GetPattern() != "done" {
			t.Fatal("通知配置未还原")
		}
		if again := normalizeLegacyShellToolCallPayload(got); !bytes.Equal(got, again) {
			t.Fatal("转换应幂等")
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

func TestNormalizeLegacyShellNotificationAliasesAndEncodings(t *testing.T) {
	config := &agentv1.ShellOutputNotificationConfig{Pattern: "?~~~???", Reason: "completed"}
	encoded, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range [][2]string{{"shellToolCall", "outputNotification"}, {"shell_tool_call", "output_notification"}, {"shellToolCall", "output_notification"}, {"shell_tool_call", "outputNotification"}} {
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			text := `{"` + keys[0] + `":{"args":{"command":"echo hi","` + keys[1] + `":"` + encoding.EncodeToString(encoded) + `"}}}`
			normalized := normalizeLegacyShellToolCallPayload([]byte(text))
			decoded := &agentv1.ToolCall{}
			if err := protojson.Unmarshal(normalized, decoded); err != nil {
				t.Fatalf("别名/编码回放失败: %v; %s", err, text)
			}
			if !proto.Equal(config, decoded.GetShellToolCall().GetArgs().GetOutputNotification()) {
				t.Fatalf("通知语义变化: %s", normalized)
			}
		}
	}
}

func TestNormalizeLegacyShellOnlyReplacesTargetValue(t *testing.T) {
	encoded, err := proto.Marshal(&agentv1.ShellOutputNotificationConfig{Pattern: "done"})
	if err != nil {
		t.Fatal(err)
	}
	encodedText := `"` + base64.StdEncoding.EncodeToString(encoded) + `"`
	input := []byte(` { "unrelated" : 9007199254740993, "shellToolCall" : { "args" : {"timeout":9223372036854775807, "outputNotification" : ` + encodedText + `, "raw" : {"big":18446744073709551615}}, "meta" : [1, 2] } } `)
	normalized := normalizeLegacyShellToolCallPayload(input)
	var configJSON json.RawMessage
	var root map[string]json.RawMessage
	_ = json.Unmarshal(normalized, &root)
	var shell map[string]json.RawMessage
	_ = json.Unmarshal(root["shellToolCall"], &shell)
	var args map[string]json.RawMessage
	_ = json.Unmarshal(shell["args"], &args)
	configJSON = args["outputNotification"]
	if len(configJSON) == 0 || configJSON[0] != '{' {
		t.Fatalf("目标未转换: %s", normalized)
	}
	want := strings.Replace(string(input), encodedText, string(configJSON), 1)
	if string(normalized) != want {
		t.Fatalf("只能替换目标字段的原始值:\nwant=%s\ngot=%s", want, normalized)
	}
}

func TestNormalizeLegacyShellInvalidInputStaysUnchanged(t *testing.T) {
	for _, input := range []string{`{bad`, `null`, `[]`, `{"shellToolCall":null}`, `{"shellToolCall":{"args":[]}}`, `{"shellToolCall":{"args":{"outputNotification":"%%%"}}}`, `{"shellToolCall":{"args":{"outputNotification":"/w=="}}}`, `{"shellToolCall":{"args":{"outputNotification":42}}}`, `{"shellToolCall":{"args":{"outputNotification":{"unknown":"field"}}}}`, `{"shellToolCall":{"args":{"outputNotification":null}}}`, `{"shellToolCall":{"args":{"outputNotification":"","output_notification":""}}}`} {
		if got := normalizeLegacyShellToolCallPayload([]byte(input)); string(got) != input {
			t.Errorf("非法或不适用输入应保持原样: %s => %s", input, got)
		}
	}
}

func TestBuildStartedShellToolCallTypedOutputNotification(t *testing.T) {
	call := buildStartedToolCall(runtimecore.ToolInvocation{CallID: "shell-1", ToolName: "Shell", ArgsJSON: []byte(`{"command":"echo hi","notify_on_output":{"pattern":"done","reason":"completed","debounce_ms":1500,"notification_limit":3}}`)})
	config := call.GetShellToolCall().GetArgs().GetOutputNotification()
	if config == nil || config.GetPattern() != "done" || config.GetReason() != "completed" || config.GetDebounce() != 5 || config.GetNotificationLimit() != 3 {
		t.Fatalf("新schema通知配置不正确: %+v", config)
	}
}

func TestProjectPromptReplayToleratesLegacyShellOutputNotification(t *testing.T) {
	for _, legacyBytes := range []bool{false, true} {
		t.Run(map[bool]string{false: "结构化对象", true: "旧bytes"}[legacyBytes], func(t *testing.T) {
			conversation := testConversation(nil)
			legacyToolCall := json.RawMessage(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":{"pattern":"done","reason":"completed"}}}}`)
			if legacyBytes {
				encoded, err := proto.Marshal(&agentv1.ShellOutputNotificationConfig{Pattern: "done", Reason: "completed"})
				if err != nil {
					t.Fatal(err)
				}
				legacyToolCall = json.RawMessage(`{"shellToolCall":{"args":{"command":"echo hi","outputNotification":"` + base64.StdEncoding.EncodeToString(encoded) + `"}}}`)
			}
			appendEntriesInPlace(conversation, []HistoryEntry{
				testUserMessageEntry(t, 1, "request-1", "run it"),
				newToolCallEntry(1, "request-1", "call-1", "Shell", "", "", legacyToolCall),
				newToolResultEntry(1, "request-1", "call-1", "Shell", `{"command":"echo hi"}`, "hi\n", "", nil),
			})
			before, err := json.Marshal(conversation.Entries)
			if err != nil {
				t.Fatal(err)
			}

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
			after, err := json.Marshal(conversation.Entries)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("回放不得写回历史")
			}
		})
	}
}
