package execbridge

import (
	"bytes"
	"testing"

	runtimecore "cursor/internal/backend/agent/core"
)

func TestOpenShellTypedOutputNotification(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		wantNil     bool
	}{
		{"缺省", `{"command":"echo hi"}`, true},
		{"完整配置", `{"command":"echo hi","notify_on_output":{"pattern":"done","reason":"completed","debounce_ms":1500,"notification_limit":3}}`, false},
		{"空原因", `{"command":"echo hi","notify_on_output":{"pattern":"done","reason":""}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			invocation := runtimecore.ToolInvocation{CallID: "shell-1", ToolName: "Shell", ArgsJSON: []byte(tt.input)}
			message, pending, err := NewBridge().OpenExec(OpenExecContext{}, invocation)
			if err != nil {
				t.Fatal(err)
			}
			config := message.GetExecServerMessage().GetShellStreamArgs().GetOutputNotification()
			if tt.wantNil {
				if config != nil {
					t.Fatalf("应无通知配置: %+v", config)
				}
			} else if config == nil || config.GetPattern() != "done" || config.GetReason() != "completed" || config.GetDebounce() != 5 || config.GetNotificationLimit() != 3 {
				t.Fatalf("结构化通知配置不正确: %+v", config)
			}
			if !bytes.Equal(pending.ArgsJSON, invocation.ArgsJSON) {
				t.Fatal("构造执行请求不得改写工具参数历史")
			}
		})
	}
}
