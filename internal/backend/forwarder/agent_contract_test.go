package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cursor/gen/agentv1"
)

func TestAgentContractWorkspaceRegistrationUsesOpaqueResponse(t *testing.T) {
	root := t.TempDir()
	handler := NewAgentContractHandler(&Service{broker: NewStreamBroker()}, func(_ context.Context) ([]AgentContractModel, error) {
		return []AgentContractModel{{ID: "channel-a", Name: "模型 A", Provider: "openai", ModelID: "gpt-a"}}, nil
	})

	payload, err := json.Marshal(map[string]string{"rootPath": root})
	if err != nil {
		t.Fatalf("encode workspace request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(payload))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if strings.Contains(response.Body.String(), root) || strings.Contains(strings.ToLower(response.Body.String()), `"root"`) {
		t.Fatalf("workspace response leaked local root: %s", response.Body.String())
	}
	var workspace agentContractWorkspace
	if err := json.Unmarshal(response.Body.Bytes(), &workspace); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}
	if workspace.ID == "" || !strings.HasPrefix(workspace.ID, "ws_") {
		t.Fatalf("workspace id = %q, want opaque ws_ id", workspace.ID)
	}
}

func TestAgentContractModelsRemainNonSensitive(t *testing.T) {
	secretURL := "https://provider.example/v1"
	secretKey := "sk-test-do-not-return"
	handler := NewAgentContractHandler(&Service{broker: NewStreamBroker()}, func(_ context.Context) ([]AgentContractModel, error) {
		return []AgentContractModel{{ID: "channel-a", Name: "模型 A", Provider: "openai", ModelID: "gpt-a"}}, nil
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if strings.Contains(body, secretURL) || strings.Contains(body, secretKey) || strings.Contains(body, "apiKey") {
		t.Fatalf("model response contains sensitive fields: %s", body)
	}
}

func TestBuildAgentContractRunMessageMapsDisplayMetadataAndContext(t *testing.T) {
	message, err := buildAgentContractRunMessage("run-1", "session-1", "conversation-1", t.TempDir(), AgentContractModel{
		ID:                  "channel-a",
		Name:                "GPT-5.6 Sol",
		Provider:            "openai",
		ModelID:             "gpt-5.6",
		ContextWindowTokens: 272000,
		ReasoningEffort:     "medium",
		FastMode:            true,
	}, agentContractStartRequest{Mode: "chat", Prompt: "hello"})
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	model := message.GetRunRequest().GetModelDetails()
	if model.GetModelId() != "gpt-5.6" || model.GetDisplayModelId() != "gpt-5.6" || model.GetDisplayName() != "GPT-5.6 Sol" || model.GetDisplayNameShort() != "GPT-5.6 Sol" {
		t.Fatalf("model display metadata = (%q, %q, %q, %q)", model.GetModelId(), model.GetDisplayModelId(), model.GetDisplayName(), model.GetDisplayNameShort())
	}
	if model.GetContextTokenLimit() != 272000 || model.GetContextTokenLimitForMaxMode() != 272000 || model.GetAutoContextMaxTokens() != 272000 || !model.GetSupportsAutoContext() {
		t.Fatalf("model context metadata = limit:%d max:%d auto:%d supports:%v", model.GetContextTokenLimit(), model.GetContextTokenLimitForMaxMode(), model.GetAutoContextMaxTokens(), model.GetSupportsAutoContext())
	}
	if requested := message.GetRunRequest().GetRequestedModel(); requested == nil || requested.GetModelId() != "channel-a" {
		t.Fatalf("requested model must retain channel route id: %#v", requested)
	}
}

func TestBuildAgentContractRunMessageMapsModeAndWorkspaceContext(t *testing.T) {
	root := t.TempDir()
	message, err := buildAgentContractRunMessage("run-1", "session-1", "conversation-1", root, AgentContractModel{
		ID: "channel-a", Name: "模型 A", Provider: "openai", ModelID: "gpt-a",
	}, agentContractStartRequest{Mode: "plan", Prompt: "请先分析代码"})
	if err != nil {
		t.Fatalf("build message: %v", err)
	}
	runRequest := message.GetRunRequest()
	if runRequest == nil {
		t.Fatal("missing run request")
	}
	if runRequest.GetRunId() != "run-1" || runRequest.GetAgentSessionId() != "session-1" {
		t.Fatalf("run identity = (%q, %q)", runRequest.GetRunId(), runRequest.GetAgentSessionId())
	}
	if runRequest.GetConversationState().GetMode() != agentv1.AgentMode_AGENT_MODE_PLAN {
		t.Fatalf("conversation mode = %v", runRequest.GetConversationState().GetMode())
	}
	userAction := runRequest.GetAction().GetUserMessageAction()
	if userAction == nil || userAction.GetUserMessage().GetText() != "请先分析代码" {
		t.Fatal("user message was not mapped")
	}
	if got := userAction.GetRequestContext().GetEnv().GetProjectFolder(); got != root {
		t.Fatalf("project folder = %q, want %q", got, root)
	}
	if got := userAction.GetRequestContext().GetEnv().GetWorkspacePaths(); len(got) != 1 || got[0] != root {
		t.Fatalf("workspace paths = %#v", got)
	}
}

func TestAgentContractSyncMapsEventsOnceAndKeepsOrder(t *testing.T) {
	broker := NewStreamBroker()
	service := &Service{broker: broker}
	const runID = "run-1"
	if _, err := broker.OpenStream(runID, "conversation-1", 1, "gpt-a", "模型 A", agentv1.AgentMode_AGENT_MODE_AGENT, "测试"); err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := broker.Publish(runID, StreamEvent{Message: buildTextDeltaMessage("完成")}); err != nil {
		t.Fatalf("publish delta: %v", err)
	}
	if err := broker.Complete(runID, "", ""); err != nil {
		t.Fatalf("complete stream: %v", err)
	}

	record := &agentContractRunRecord{
		run:    agentContractRun{ID: runID, SessionID: "session-1", Mode: "chat", Status: "running"},
		events: []agentContractEvent{{Sequence: 1, Kind: "started"}},
	}
	runtime := &agentContractRuntime{service: service}
	runtime.mu.Lock()
	if err := runtime.syncRunLocked(record); err != nil {
		runtime.mu.Unlock()
		t.Fatalf("sync run: %v", err)
	}
	firstCount := len(record.events)
	firstStatus := record.run.Status
	if err := runtime.syncRunLocked(record); err != nil {
		runtime.mu.Unlock()
		t.Fatalf("second sync run: %v", err)
	}
	secondCount := len(record.events)
	runtime.mu.Unlock()

	if firstCount != 3 || secondCount != firstCount {
		t.Fatalf("event counts = (%d, %d), want (3, 3)", firstCount, secondCount)
	}
	if firstStatus != "completed" {
		t.Fatalf("status = %q, want completed", firstStatus)
	}
	for index, event := range record.events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
}

func TestAgentContractQueuedRunCancelReturnsCanceled(t *testing.T) {
	const runID = "run-queued"
	runtime := &agentContractRuntime{
		service:    &Service{broker: NewStreamBroker()},
		workspaces: make(map[string]agentContractWorkspace),
		sessions:   make(map[string]string),
		runs: map[string]*agentContractRunRecord{
			runID: {run: agentContractRun{ID: runID, Status: "running"}},
		},
	}
	response := httptest.NewRecorder()
	newAgentContractHandler(runtime).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs/"+runID+"/cancel", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var run agentContractRun
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode canceled run: %v", err)
	}
	if run.Status != "canceled" {
		t.Fatalf("status = %q, want canceled", run.Status)
	}
}

func TestAgentContractStartRejectsUnregisteredWorkspace(t *testing.T) {
	handler := NewAgentContractHandler(&Service{broker: NewStreamBroker()}, func(_ context.Context) ([]AgentContractModel, error) {
		return []AgentContractModel{{ID: "channel-a", ModelID: "gpt-a"}}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/runs", bytes.NewBufferString(`{"workspaceId":"ws-missing","modelId":"channel-a","prompt":"测试"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "invalid_workspace") {
		t.Fatalf("error response = %s", response.Body.String())
	}
}

func TestNormalizeAgentWorkspaceRootRejectsRelativePath(t *testing.T) {
	if _, err := normalizeAgentWorkspaceRoot("relative/path"); err == nil {
		t.Fatal("relative workspace path was accepted")
	}
	if _, err := normalizeAgentWorkspaceRoot(os.DevNull); err == nil {
		t.Fatal("non-directory workspace path was accepted")
	}
}
