package forwarder

import (
	"testing"

	"cursor/gen/agentv1"
)

func TestIsAbsoluteToolPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: false},
		{name: "spaces", path: "   ", want: false},
		{name: "relative", path: "internal/backend/forwarder/path_resolution.go", want: false},
		{name: "dot relative", path: "./path_resolution.go", want: false},
		{name: "parent relative", path: "../path_resolution.go", want: false},
		{name: "windows drive relative", path: `C:path_resolution.go`, want: false},
		{name: "windows drive only", path: `C:`, want: false},
		{name: "invalid drive prefix", path: `1:/path_resolution.go`, want: false},
		{name: "posix absolute", path: "/Users/example/path_resolution.go", want: true},
		{name: "posix absolute with spaces", path: "  /tmp/path_resolution.go  ", want: true},
		{name: "windows backslash absolute", path: `C:\Users\example\path_resolution.go`, want: true},
		{name: "windows slash absolute", path: `C:/Users/example/path_resolution.go`, want: true},
		{name: "windows unc backslash", path: `\\server\share\path_resolution.go`, want: true},
		{name: "windows unc slash", path: `//server/share/path_resolution.go`, want: true},
		{name: "windows extended drive", path: `\\?\C:\Users\example\path_resolution.go`, want: true},
		{name: "windows extended unc", path: `\\?\UNC\server\share\path_resolution.go`, want: true},
		{name: "windows device path", path: `\\.\C:\Users\example\path_resolution.go`, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isAbsoluteToolPath(test.path); got != test.want {
				t.Fatalf("isAbsoluteToolPath(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

// TestUpdateStreamRequestContextDataPreservesWorkspaceOnResume 复现真实故障:
// 首次 run 携带 workspace_paths,后续 resume run_request 不带 request_context。
// 修复前 updateStreamRequestContextData 会用空值清空 stream.WorkspacePaths,
// 导致只读 Shell 策略失去 workspace 基准,子代理反复失败。
func TestUpdateStreamRequestContextDataPreservesWorkspaceOnResume(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream(
		"request-resume",
		"conversation-resume",
		1,
		"model",
		"model",
		agentv1.AgentMode_AGENT_MODE_PLAN,
		"请检查工作区",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	// 首次 run:带 workspace_paths。
	firstContext := &agentv1.RequestContext{
		Env: &agentv1.RequestContextEnv{
			WorkspacePaths: []string{`e:\MyProject\cursor-byok`},
		},
	}
	updateStreamRequestContextData(stream, firstContext)
	if len(stream.WorkspacePaths) != 1 || stream.WorkspacePaths[0] != `e:\MyProject\cursor-byok` {
		t.Fatalf("after first run WorkspacePaths = %v, want [e:\\MyProject\\cursor-byok]", stream.WorkspacePaths)
	}

	// resume run_request:不带 request_context(nil)。
	updateStreamRequestContextData(stream, nil)
	if len(stream.WorkspacePaths) != 1 || stream.WorkspacePaths[0] != `e:\MyProject\cursor-byok` {
		t.Fatalf("after resume WorkspacePaths = %v, want preserved [e:\\MyProject\\cursor-byok]", stream.WorkspacePaths)
	}

	// resume 带空 env 也不应清空。
	updateStreamRequestContextData(stream, &agentv1.RequestContext{Env: &agentv1.RequestContextEnv{}})
	if len(stream.WorkspacePaths) != 1 {
		t.Fatalf("after empty-env resume WorkspacePaths = %v, want preserved", stream.WorkspacePaths)
	}
}

// TestUpdateStreamRequestContextDataReplacesWorkspaceOnNewContext 验证新 run
// 携带不同 workspace 时仍正常替换(不会因为保留逻辑而卡住旧路径)。
func TestUpdateStreamRequestContextDataReplacesWorkspaceOnNewContext(t *testing.T) {
	broker := NewStreamBroker()
	stream, err := broker.OpenStream(
		"request-switch",
		"conversation-switch",
		1,
		"model",
		"model",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"hello",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}

	updateStreamRequestContextData(stream, &agentv1.RequestContext{
		Env: &agentv1.RequestContextEnv{
			WorkspacePaths: []string{`c:\repo\one`},
		},
	})
	updateStreamRequestContextData(stream, &agentv1.RequestContext{
		Env: &agentv1.RequestContextEnv{
			WorkspacePaths: []string{`d:\repo\two`},
		},
	})
	if len(stream.WorkspacePaths) != 1 || stream.WorkspacePaths[0] != `d:\repo\two` {
		t.Fatalf("after new context WorkspacePaths = %v, want [d:\\repo\\two]", stream.WorkspacePaths)
	}
}
