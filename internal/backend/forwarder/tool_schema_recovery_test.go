package forwarder

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
)

func TestClassifyProvider400RecoveryNamedToolRequired(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    provider400RecoveryReason
	}{
		{name: "named tool schema rejected", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "Invalid schema for tool 'Read': must be object"}, want: provider400RecoveryToolSchema},
		{name: "backtick named tool rejected", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid tool `mcp_tool_unsafe` schema"}, want: provider400RecoveryToolSchema},
		{name: "body names tool", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "bad request", Body: `{"error":{"message":"tool \"Write\" has invalid schema"}}`}, want: provider400RecoveryToolSchema},
		{name: "schema marker without named tool stays terminal", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "messages.1.tools.0.input_schema: expected object"}, want: ""},
		{name: "generic parameter marker without schema context stays terminal", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid request: missing api key"}, want: ""},
		{name: "content exists", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "content exists"}, want: provider400RecoveryContentExists},
		{name: "non 400 not recoverable", err: &modeladapter.HTTPStatusError{StatusCode: 500, Message: "Invalid schema for tool 'Read'"}, want: ""},
		{name: "nil error", err: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProvider400Recovery(tt.err); got != tt.want {
				t.Fatalf("classifyProvider400Recovery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderToolSchema400ToolNameExtraction(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantName string
		wantOK   bool
	}{
		{name: "single quoted", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "tool 'Read' has invalid schema"}, wantName: "Read", wantOK: true},
		{name: "backtick quoted", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid tool `mcp_tool_unsafe` schema"}, wantName: "mcp_tool_unsafe", wantOK: true},
		{name: "double quoted in body", err: &modeladapter.HTTPStatusError{StatusCode: 400, Body: `{"error":{"message":"tool \"Write\": bad"}}`}, wantName: "Write", wantOK: true},
		{name: "no quoted name", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "schema rejected"}, wantOK: false},
		{name: "numeric quoted name rejected", err: &modeladapter.HTTPStatusError{StatusCode: 400, Message: "tool '12345' rejected"}, wantOK: false},
		{name: "non structured error", err: context.Canceled, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, ok := providerToolSchema400ToolName(tt.err)
			if ok != tt.wantOK || name != tt.wantName {
				t.Fatalf("providerToolSchema400ToolName() = (%q, %t), want (%q, %t)", name, ok, tt.wantName, tt.wantOK)
			}
		})
	}
}

func TestFilterToolDescriptorsByNameSet(t *testing.T) {
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`),
		json.RawMessage(`{"type":"function","function":{"name":"Write","parameters":{"type":"object"}}}`),
		json.RawMessage(`{"type":"function","function":{"name":"mcp tool/unsafe","parameters":{"type":"object"}}}`),
	}
	got := filterToolDescriptorsByNameSet(tools, []string{"mcp_tool_unsafe"})
	names := toolDescriptorNames(got)
	if len(names) != 2 || names[0] != "Read" || names[1] != "Write" {
		t.Fatalf("filtered tools = %#v, want Read/Write only", names)
	}

	exact := filterToolDescriptorsByNameSet(tools, []string{"Read"})
	if gotNames := toolDescriptorNames(exact); len(gotNames) != 2 || gotNames[0] != "Write" {
		t.Fatalf("exact filter = %#v, want Write first", gotNames)
	}

	if unchanged := filterToolDescriptorsByNameSet(tools, nil); len(unchanged) != len(tools) {
		t.Fatalf("nil quarantine changed tools: %d -> %d", len(tools), len(unchanged))
	}
	if unchanged := filterToolDescriptorsByNameSet(tools, []string{"NoSuchTool"}); len(unchanged) != len(tools) {
		t.Fatalf("unmatched quarantine changed tools: %d -> %d", len(tools), len(unchanged))
	}
	// 精确命中存在时优先精确匹配，不再按归一化兜底，避免误删归一化碰撞的兄弟工具。
	sibling := filterToolDescriptorsByNameSet([]json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"mcp github","parameters":{"type":"object"}}}`),
		json.RawMessage(`{"type":"function","function":{"name":"mcp_github","parameters":{"type":"object"}}}`),
	}, []string{"mcp github"})
	if got := toolDescriptorNames(sibling); len(got) != 1 || got[0] != "mcp_github" {
		t.Fatalf("exact-match preference over-removed sibling: %#v", got)
	}
}

func TestProviderPassAdvertisedToolGate(t *testing.T) {
	if !providerPassAdvertisedTool([]string{"Read"}, "Read") {
		t.Fatal("exact advertised tool was not matched")
	}
	if !providerPassAdvertisedTool([]string{"mcp tool/unsafe"}, "mcp_tool_unsafe") {
		t.Fatal("normalized advertised tool was not matched")
	}
	if providerPassAdvertisedTool([]string{"Read"}, "Write") {
		t.Fatal("non-advertised tool matched the gate")
	}
	if providerPassAdvertisedTool(nil, "Read") {
		t.Fatal("empty advertised set matched the gate")
	}
}

func TestClaimToolSchema400RecoveryRequiresAdvertisedAndOnce(t *testing.T) {
	service := &Service{
		provider400RecoveryTurns: make(map[string]struct{}),
	}
	stream := &ActiveStream{
		RequestID:           "request-recovery",
		TurnSeq:             7,
		ProviderPassToolNames: []string{"Read", "mcp tool/unsafe"},
	}

	cause := &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid schema for tool 'Read'"}

	name, claimed := service.claimToolSchema400Recovery(stream, stream.RequestID, stream.TurnSeq, cause)
	if !claimed || name != "Read" {
		t.Fatalf("first claim = (%q, %t), want (Read, true)", name, claimed)
	}
	if got := snapshotProviderToolQuarantine(stream); len(got) != 1 || got[0] != "Read" {
		t.Fatalf("quarantine after claim = %#v, want [Read]", got)
	}
	if _, claimed := service.claimToolSchema400Recovery(stream, stream.RequestID, stream.TurnSeq, cause); claimed {
		t.Fatal("second claim in same turn was accepted")
	}

	notAdvertised := &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid schema for tool 'Write'"}
	if _, claimed := service.claimToolSchema400Recovery(stream, stream.RequestID, stream.TurnSeq, notAdvertised); claimed {
		t.Fatal("non-advertised named tool was accepted")
	}

	normalizedCause := &modeladapter.HTTPStatusError{StatusCode: 400, Message: "invalid schema for tool `mcp_tool_unsafe`"}
	name, claimed = service.claimToolSchema400Recovery(stream, stream.RequestID, stream.TurnSeq+1, normalizedCause)
	if !claimed || name != "mcp_tool_unsafe" {
		t.Fatalf("normalized claim = (%q, %t), want (mcp_tool_unsafe, true)", name, claimed)
	}
	if got := snapshotProviderToolQuarantine(stream); len(got) != 2 {
		t.Fatalf("quarantine after normalized claim = %#v, want two entries", got)
	}
}

type quarantineLifecycleCompiler struct{}

func (quarantineLifecycleCompiler) Compile(_ *ConversationFile, mode agentv1.AgentMode, _ string, _ string, _ string, _ bool) (CompiledConversation, error) {
	return CompiledConversation{
		Mode: mode,
		Tools: []json.RawMessage{
			json.RawMessage(`{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}`),
			json.RawMessage(`{"type":"function","function":{"name":"Write","parameters":{"type":"object"}}}`),
			json.RawMessage(`{"type":"function","function":{"name":"mcp tool/unsafe","parameters":{"type":"object"}}}`),
		},
	}, nil
}

func (quarantineLifecycleCompiler) DerivePromptContexts(_ *ConversationFile, _ agentv1.AgentMode, _ string) ([]PromptContextMessage, error) {
	return nil, nil
}

type quarantineRequestProvider struct {
	requests chan ProviderRequest
}

func (provider *quarantineRequestProvider) StartStream(_ context.Context, request ProviderRequest, _ func(modeladapter.ModelEvent) error) error {
	provider.requests <- request
	return errProviderLoopInterrupted
}

// TestDriveProviderQuarantinesToolFromProviderRequest 锁定：隔离集里的工具在
// driveProvider 构建 provider 请求时被剔除，而其余工具原样保留。
func TestDriveProviderQuarantinesToolFromProviderRequest(t *testing.T) {
	store := NewConversationFileStore(t.TempDir())
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-quarantine", "请帮我读取文件"),
	})
	persisted, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries)
	if err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	provider := &quarantineRequestProvider{requests: make(chan ProviderRequest, 1)}
	broker := NewStreamBroker()
	service := newServiceWithDependencies(store, NewHistoryProjector(), quarantineLifecycleCompiler{}, provider, broker)
	stream, err := broker.OpenStream(
		"request-quarantine",
		persisted.ConversationID,
		1,
		"model-a",
		"model-a",
		agentv1.AgentMode_AGENT_MODE_AGENT,
		"请帮我读取文件",
	)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	stream.CheckpointConversation = cloneConversationFile(persisted)
	stream.ProviderToolQuarantine = []string{"mcp_tool_unsafe"}

	if err := service.driveProvider(stream); err != nil {
		t.Fatalf("driveProvider() error = %v", err)
	}
	select {
	case request := <-provider.requests:
		names := toolDescriptorNames(request.Tools)
		if len(names) != 2 || names[0] != "Read" || names[1] != "Write" {
			t.Fatalf("provider request tools = %#v, want Read/Write only", names)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider request was not started")
	}
}
