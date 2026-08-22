package forwarder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

func TestShouldLogThinkingDeltaSamplesFirstAndInterval(t *testing.T) {
	tests := []struct {
		name       string
		deltaCount int
		want       bool
	}{
		{name: "zero", deltaCount: 0, want: false},
		{name: "first", deltaCount: 1, want: true},
		{name: "before interval", deltaCount: thinkingDeltaLogInterval - 1, want: false},
		{name: "interval", deltaCount: thinkingDeltaLogInterval, want: true},
		{name: "after interval", deltaCount: thinkingDeltaLogInterval + 1, want: false},
		{name: "second interval", deltaCount: thinkingDeltaLogInterval * 2, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldLogThinkingDelta(test.deltaCount); got != test.want {
				t.Fatalf("shouldLogThinkingDelta(%d) = %t, want %t", test.deltaCount, got, test.want)
			}
		})
	}
}

func TestThinkingDeltaDebugFieldsSampleFirstAndInterval(t *testing.T) {
	if fields := thinkingDeltaDebugFields("call-1", 2, 2, 128); fields != nil {
		t.Fatalf("non-sampled fields = %#v, want nil", fields)
	}

	fields := thinkingDeltaDebugFields("call-1", 2, thinkingDeltaLogInterval, 128)
	if fields["model_call_id"] != "call-1" || fields["provider_pass"] != 2 || fields["delta_count"] != thinkingDeltaLogInterval || fields["accumulated_bytes"] != 128 {
		t.Fatalf("sampled fields = %#v", fields)
	}
}

func TestThinkingDeltaSamplingKeepsEveryBrokerEvent(t *testing.T) {
	service := NewService(t.TempDir(), nilResolver{})
	stream, err := service.broker.OpenStream("request-thinking", "conversation-thinking", 1, "model", "model", agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	const deltaCount = thinkingDeltaLogInterval * 2
	for index := 0; index < deltaCount; index++ {
		event := modeladapter.ModelEvent{
			Kind:          modeladapter.ModelEventKindThinkingDelta,
			Text:          "reasoning",
			ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
		}
		if err := service.applyProviderModelEvent(stream, event); err != nil {
			t.Fatalf("applyProviderModelEvent(%d) error = %v", index, err)
		}
	}

	events, err := service.broker.ReadFromCursor(stream.RequestID, 0)
	if err != nil {
		t.Fatalf("ReadFromCursor() error = %v", err)
	}
	if len(events) != deltaCount {
		t.Fatalf("thinking events = %d, want %d", len(events), deltaCount)
	}
	for index, event := range events {
		if got := event.Message.GetInteractionUpdate().GetThinkingDelta().GetText(); got != "reasoning" {
			t.Fatalf("thinking event %d text = %q, want reasoning", index, got)
		}
	}
}

func TestDeltaRuntimeLogsUseProviderPassCapturedWithStreamState(t *testing.T) {
	root := t.TempDir()
	broker := NewStreamBroker()
	service := &Service{
		broker: broker,
		store:  NewConversationFileStore(root),
	}
	// 勿用 NewService：会启动 history maintenance，与 t.TempDir 清理竞态（CI 偶发 directory not empty）。
	service.debug = newDebugRecorder(root, broker, stubDebugLogConfig{enabled: true, maxBytes: -1})
	stream, err := service.broker.OpenStream("request-provider-pass", "conversation-provider-pass", 1, "model", "model", agentv1.AgentMode_AGENT_MODE_AGENT, "")
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.ProviderPassCount = 7

	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{Kind: modeladapter.ModelEventKindTextDelta, Text: "text"}); err != nil {
		t.Fatalf("text delta error = %v", err)
	}
	if err := service.applyProviderModelEvent(stream, modeladapter.ModelEvent{
		Kind:          modeladapter.ModelEventKindThinkingDelta,
		Text:          "thinking",
		ThinkingStyle: agentv1.ThinkingStyle_THINKING_STYLE_DEFAULT,
	}); err != nil {
		t.Fatalf("thinking delta error = %v", err)
	}

	path := filepath.Join(root, stream.ConversationID, "debug", "runtime.jsonl")
	// 落盘走异步 worker 队列：两条事件可能分批写入。轮询到文件非空就立即解析
	// 会在慢 runner（CI -race）上读到只有第一条的快照，必须等两条事件都齐。
	seen := map[string]int{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(path)
		if readErr == nil && len(data) > 0 {
			seen = map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var entry struct {
					Event        string `json:"event"`
					ProviderPass int    `json:"provider_pass"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					// 半行写入视作尚未就绪，继续轮询；超时后由下方断言报错。
					seen = nil
					break
				}
				if entry.Event == "text_delta_forwarded" || entry.Event == "thinking_delta_forwarded" {
					seen[entry.Event] = entry.ProviderPass
				}
			}
			if seen != nil && seen["text_delta_forwarded"] == 7 && seen["thinking_delta_forwarded"] == 7 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seen == nil {
		t.Fatalf("runtime log was not written: %s", path)
	}
	for _, eventName := range []string{"text_delta_forwarded", "thinking_delta_forwarded"} {
		if got := seen[eventName]; got != 7 {
			t.Fatalf("%s provider_pass = %d, want 7", eventName, got)
		}
	}
}
