package forwarder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
)

const agentContractVersion = "agent.contract.v1"

// AgentContractModel 是外部 Agent 客户端可见的模型通道摘要。
// 只允许暴露非敏感元数据，不包含 API Key、完整 URL 或账户信息。
type AgentContractModel struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Provider            string `json:"provider"`
	ModelID             string `json:"modelId"`
	ContextWindowTokens int    `json:"contextWindowTokens,omitempty"`
	ReasoningEffort     string `json:"reasoningEffort,omitempty"`
	FastMode            bool   `json:"fastMode,omitempty"`
}

type agentContractWorkspace struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RegisteredAt string `json:"registeredAt"`
	Root         string `json:"-"`
}

type agentContractStartRequest struct {
	SessionID   string `json:"sessionId,omitempty"`
	ParentRunID string `json:"parentRunId,omitempty"`
	WorkspaceID string `json:"workspaceId"`
	ModelID     string `json:"modelId"`
	Mode        string `json:"mode,omitempty"`
	Prompt      string `json:"prompt"`
}

type agentContractRun struct {
	ContractVersion string `json:"contractVersion"`
	ID              string `json:"id"`
	SessionID       string `json:"sessionId"`
	ParentRunID     string `json:"parentRunId,omitempty"`
	WorkspaceID     string `json:"workspaceId"`
	ModelID         string `json:"modelId"`
	Mode            string `json:"mode"`
	Prompt          string `json:"prompt"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	CreatedAtUnix   int64  `json:"createdAtUnixMs"`
	UpdatedAtUnix   int64  `json:"updatedAtUnixMs"`
}

type agentContractEvent struct {
	ContractVersion string `json:"contractVersion"`
	RunID           string `json:"runId"`
	SessionID       string `json:"sessionId"`
	ParentRunID     string `json:"parentRunId,omitempty"`
	Sequence        int64  `json:"sequence"`
	Kind            string `json:"kind"`
	Mode            string `json:"mode"`
	ToolName        string `json:"toolName,omitempty"`
	Text            string `json:"text,omitempty"`
	ReplaySafe      bool   `json:"replaySafe"`
	AtUnix          int64  `json:"atUnixMs"`
}

type agentContractRunRecord struct {
	run            agentContractRun
	conversationID string
	brokerCursor   int
	events         []agentContractEvent
	terminalSeen   bool
}

type agentContractRuntime struct {
	service    *Service
	listModels func(context.Context) ([]AgentContractModel, error)

	mu         sync.Mutex
	workspaces map[string]agentContractWorkspace
	sessions   map[string]string
	runs       map[string]*agentContractRunRecord
}

// NewAgentContractHandler 创建供 VS Code 等本地客户端使用的稳定 JSON facade。
// Handler 不复制 Cursor 私有协议；它只在 Runtime 内部将 Contract 请求映射到
// BidiAppend、StreamBroker 和既有 forwarder 生命周期。
func NewAgentContractHandler(service *Service, listModels func(context.Context) ([]AgentContractModel, error)) http.Handler {
	return newAgentContractHandler(&agentContractRuntime{
		service:    service,
		listModels: listModels,
		workspaces: make(map[string]agentContractWorkspace),
		sessions:   make(map[string]string),
		runs:       make(map[string]*agentContractRunRecord),
	})
}

func newAgentContractHandler(runtime *agentContractRuntime) http.Handler {
	if runtime == nil {
		runtime = &agentContractRuntime{
			workspaces: make(map[string]agentContractWorkspace),
			sessions:   make(map[string]string),
			runs:       make(map[string]*agentContractRunRecord),
		}
	}
	router := chi.NewRouter()
	router.Get("/health", runtime.health)
	router.Get("/workspaces", runtime.listWorkspaces)
	router.Post("/workspaces", runtime.registerWorkspace)
	router.Get("/models", runtime.listModelsHandler)
	router.Post("/runs", runtime.startRun)
	router.Get("/runs", runtime.listRuns)
	router.Get("/runs/{runID}", runtime.getRun)
	router.Get("/runs/{runID}/events", runtime.getEvents)
	router.Get("/runs/{runID}/replay", runtime.replayRun)
	router.Post("/runs/{runID}/cancel", runtime.cancelRun)
	return router
}

func (runtime *agentContractRuntime) health(writer http.ResponseWriter, _ *http.Request) {
	if runtime == nil || runtime.service == nil {
		writeAgentContractError(writer, http.StatusServiceUnavailable, "agent_unavailable", "Agent Runtime 未初始化")
		return
	}
	writeAgentContractJSON(writer, http.StatusOK, map[string]string{
		"contractVersion": agentContractVersion,
		"service":         "cursor-byok-runtime",
		"status":          "ok",
	})
}

func (runtime *agentContractRuntime) listWorkspaces(writer http.ResponseWriter, _ *http.Request) {
	runtime.mu.Lock()
	items := make([]agentContractWorkspace, 0, len(runtime.workspaces))
	for _, item := range runtime.workspaces {
		items = append(items, item)
	}
	runtime.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeAgentContractJSON(writer, http.StatusOK, items)
}

func (runtime *agentContractRuntime) registerWorkspace(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		RootPath string `json:"rootPath"`
	}
	if !decodeAgentContractJSON(writer, request, &payload) {
		return
	}
	root, err := normalizeAgentWorkspaceRoot(payload.RootPath)
	if err != nil {
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_workspace", err.Error())
		return
	}
	hash := sha256.Sum256([]byte(strings.ToLower(root)))
	workspaceID := "ws_" + hex.EncodeToString(hash[:8])
	item := agentContractWorkspace{
		ID:           workspaceID,
		Name:         filepath.Base(root),
		RegisteredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Root:         root,
	}
	runtime.mu.Lock()
	if previous, exists := runtime.workspaces[workspaceID]; exists {
		item.RegisteredAt = previous.RegisteredAt
	}
	runtime.workspaces[workspaceID] = item
	runtime.mu.Unlock()
	writeAgentContractJSON(writer, http.StatusOK, item)
}

func (runtime *agentContractRuntime) listModelsHandler(writer http.ResponseWriter, request *http.Request) {
	if runtime == nil || runtime.listModels == nil {
		writeAgentContractJSON(writer, http.StatusOK, []AgentContractModel{})
		return
	}
	items, err := runtime.listModels(request.Context())
	if err != nil {
		writeAgentContractError(writer, http.StatusInternalServerError, "model_list_failed", "读取模型通道失败")
		return
	}
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ModelID) == "" {
			writeAgentContractError(writer, http.StatusBadGateway, "invalid_model_summary", "Runtime 返回了无效模型通道")
			return
		}
	}
	writeAgentContractJSON(writer, http.StatusOK, items)
}

func (runtime *agentContractRuntime) startRun(writer http.ResponseWriter, request *http.Request) {
	if runtime == nil || runtime.service == nil {
		writeAgentContractError(writer, http.StatusServiceUnavailable, "agent_unavailable", "Agent Runtime 未初始化")
		return
	}
	var payload agentContractStartRequest
	if !decodeAgentContractJSON(writer, request, &payload) {
		return
	}
	payload.Mode = normalizeAgentContractMode(payload.Mode)
	payload.WorkspaceID = strings.TrimSpace(payload.WorkspaceID)
	payload.ModelID = strings.TrimSpace(payload.ModelID)
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	if payload.WorkspaceID == "" || payload.ModelID == "" || payload.Prompt == "" {
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_request", "workspaceId、modelId 和 prompt 不能为空")
		return
	}

	runtime.mu.Lock()
	workspace, workspaceOK := runtime.workspaces[payload.WorkspaceID]
	if !workspaceOK {
		runtime.mu.Unlock()
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_workspace", "工作区尚未注册")
		return
	}
	models, modelErr := runtime.listModelsLocked(request.Context())
	if modelErr != nil {
		runtime.mu.Unlock()
		writeAgentContractError(writer, http.StatusInternalServerError, "model_list_failed", "读取模型通道失败")
		return
	}
	model, modelOK := findAgentContractModel(models, payload.ModelID)
	if !modelOK {
		runtime.mu.Unlock()
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_model", "模型通道不存在")
		return
	}

	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = "session_" + uuid.NewString()
	}
	conversationID := runtime.sessions[sessionID]
	if conversationID == "" {
		conversationID = "conversation_" + uuid.NewString()
		runtime.sessions[sessionID] = conversationID
	}
	runID := "run_" + uuid.NewString()
	now := time.Now().UTC()
	record := &agentContractRunRecord{
		run: agentContractRun{
			ContractVersion: agentContractVersion,
			ID:              runID,
			SessionID:       sessionID,
			ParentRunID:     strings.TrimSpace(payload.ParentRunID),
			WorkspaceID:     workspace.ID,
			ModelID:         model.ID,
			Mode:            payload.Mode,
			Prompt:          payload.Prompt,
			Status:          "running",
			CreatedAtUnix:   now.UnixMilli(),
			UpdatedAtUnix:   now.UnixMilli(),
		},
		conversationID: conversationID,
		events: []agentContractEvent{{
			ContractVersion: agentContractVersion,
			RunID:           runID,
			SessionID:       sessionID,
			ParentRunID:     strings.TrimSpace(payload.ParentRunID),
			Sequence:        1,
			Kind:            "started",
			Mode:            payload.Mode,
			ReplaySafe:      true,
			AtUnix:          now.UnixMilli(),
		}},
	}
	runtime.runs[runID] = record
	runtime.mu.Unlock()

	message, err := buildAgentContractRunMessage(runID, sessionID, conversationID, workspace.Root, model, payload)
	if err == nil {
		payloadBytes, marshalErr := proto.Marshal(message)
		if marshalErr != nil {
			err = marshalErr
		} else {
			_, err = runtime.service.BidiAppend(request.Context(), connect.NewRequest(&aiserverv1.BidiAppendRequest{
				RequestId:   &aiserverv1.BidiRequestId{RequestId: runID},
				AppendSeqno: 1,
				DataBinary:  payloadBytes,
			}))
		}
	}
	if err != nil {
		runtime.mu.Lock()
		runtime.failRunLocked(record, "Agent Runtime 无法启动本轮运行")
		runtime.mu.Unlock()
		writeAgentContractError(writer, http.StatusBadGateway, "run_start_failed", "Agent Runtime 无法启动本轮运行")
		return
	}

	writeAgentContractJSON(writer, http.StatusCreated, record.run)
}

func (runtime *agentContractRuntime) listRuns(writer http.ResponseWriter, request *http.Request) {
	workspaceID := strings.TrimSpace(request.URL.Query().Get("workspaceId"))
	runtime.mu.Lock()
	items := make([]agentContractRun, 0, len(runtime.runs))
	for _, record := range runtime.runs {
		if workspaceID != "" && record.run.WorkspaceID != workspaceID {
			continue
		}
		items = append(items, record.run)
	}
	runtime.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAtUnix < items[j].CreatedAtUnix })
	writeAgentContractJSON(writer, http.StatusOK, items)
}

func (runtime *agentContractRuntime) getRun(writer http.ResponseWriter, request *http.Request) {
	runID := strings.TrimSpace(chi.URLParam(request, "runID"))
	runtime.mu.Lock()
	record := runtime.runs[runID]
	if record != nil {
		_ = runtime.syncRunLocked(record)
	}
	var run agentContractRun
	if record != nil {
		run = record.run
	}
	runtime.mu.Unlock()
	if record == nil {
		writeAgentContractError(writer, http.StatusNotFound, "not_found", "运行不存在")
		return
	}
	writeAgentContractJSON(writer, http.StatusOK, run)
}

func (runtime *agentContractRuntime) getEvents(writer http.ResponseWriter, request *http.Request) {
	runtime.writeEvents(writer, strings.TrimSpace(chi.URLParam(request, "runID")))
}

func (runtime *agentContractRuntime) replayRun(writer http.ResponseWriter, request *http.Request) {
	runtime.writeEvents(writer, strings.TrimSpace(chi.URLParam(request, "runID")))
}

func (runtime *agentContractRuntime) writeEvents(writer http.ResponseWriter, runID string) {
	runtime.mu.Lock()
	record := runtime.runs[runID]
	if record != nil {
		_ = runtime.syncRunLocked(record)
	}
	var events []agentContractEvent
	if record != nil {
		events = append([]agentContractEvent(nil), record.events...)
	}
	runtime.mu.Unlock()
	if record == nil {
		writeAgentContractError(writer, http.StatusNotFound, "not_found", "运行不存在")
		return
	}
	writeAgentContractJSON(writer, http.StatusOK, events)
}

func (runtime *agentContractRuntime) cancelRun(writer http.ResponseWriter, request *http.Request) {
	runID := strings.TrimSpace(chi.URLParam(request, "runID"))
	runtime.mu.Lock()
	record := runtime.runs[runID]
	runtime.mu.Unlock()
	if record == nil {
		writeAgentContractError(writer, http.StatusNotFound, "not_found", "运行不存在")
		return
	}
	if runtime.service == nil || runtime.service.broker == nil {
		writeAgentContractError(writer, http.StatusServiceUnavailable, "agent_unavailable", "Agent Runtime 流服务未初始化")
		return
	}
	if err := runtime.service.broker.Cancel(runID, "[canceled] VS Code Agent canceled the run"); err != nil && !errors.Is(err, errStreamNotActive) {
		writeAgentContractError(writer, http.StatusBadGateway, "cancel_failed", "取消 Agent 运行失败")
		return
	}
	runtime.mu.Lock()
	_ = runtime.syncRunLocked(record)
	if record.run.Status == "running" {
		runtime.failRunLocked(record, "Agent 运行已取消")
		record.run.Status = "canceled"
	}
	run := record.run
	runtime.mu.Unlock()
	writeAgentContractJSON(writer, http.StatusOK, run)
}

func (runtime *agentContractRuntime) listModelsLocked(ctx context.Context) ([]AgentContractModel, error) {
	if runtime.listModels == nil {
		return []AgentContractModel{}, nil
	}
	return runtime.listModels(ctx)
}

func (runtime *agentContractRuntime) syncRunLocked(record *agentContractRunRecord) error {
	if record == nil || runtime.service == nil || runtime.service.broker == nil {
		return nil
	}
	stream, exists := runtime.service.broker.Get(record.run.ID)
	if !exists || stream == nil {
		return nil
	}
	backlog, err := runtime.service.broker.ReadFromCursor(record.run.ID, record.brokerCursor)
	if err != nil {
		return err
	}
	for _, item := range backlog {
		record.brokerCursor++
		if event, ok := contractEventFromStream(record, item); ok {
			record.events = append(record.events, event)
		}
		if item.End && !record.terminalSeen {
			record.terminalSeen = true
			stream.mu.Lock()
			status := stream.Status
			stream.mu.Unlock()
			switch status {
			case StreamStatusCompleted:
				record.run.Status = "completed"
			case StreamStatusCanceled:
				record.run.Status = "canceled"
			case StreamStatusFailed:
				record.run.Status = "failed"
				record.run.Error = "Agent Runtime 执行失败"
			}
		}
	}
	stream.mu.Lock()
	if stream.Status == StreamStatusCompleted {
		record.run.Status = "completed"
	} else if stream.Status == StreamStatusCanceled {
		record.run.Status = "canceled"
	} else if stream.Status == StreamStatusFailed {
		record.run.Status = "failed"
		record.run.Error = "Agent Runtime 执行失败"
	}
	record.run.UpdatedAtUnix = stream.UpdatedAt.UnixMilli()
	stream.mu.Unlock()
	return nil
}

func (runtime *agentContractRuntime) failRunLocked(record *agentContractRunRecord, message string) {
	if record == nil {
		return
	}
	record.run.Status = "failed"
	record.run.Error = message
	record.run.UpdatedAtUnix = time.Now().UTC().UnixMilli()
}

func contractEventFromStream(record *agentContractRunRecord, item StreamEvent) (agentContractEvent, bool) {
	if record == nil {
		return agentContractEvent{}, false
	}
	event := agentContractEvent{
		ContractVersion: agentContractVersion,
		RunID:           record.run.ID,
		SessionID:       record.run.SessionID,
		ParentRunID:     record.run.ParentRunID,
		Mode:            record.run.Mode,
		ReplaySafe:      true,
		AtUnix:          time.Now().UTC().UnixMilli(),
	}
	if item.Message != nil {
		update := item.Message.GetInteractionUpdate()
		switch {
		case update != nil && update.GetTextDelta() != nil:
			event.Kind = "delta"
			event.Text = update.GetTextDelta().GetText()
			if event.Text == "" {
				return agentContractEvent{}, false
			}
		case update != nil && update.GetThinkingDelta() != nil:
			event.Kind = "thinking"
			event.Text = update.GetThinkingDelta().GetText()
		case update != nil && update.GetToolCallStarted() != nil:
			event.Kind = "tool"
			event.ToolName = "cursor-tool"
		case update != nil && update.GetToolCallCompleted() != nil:
			event.Kind = "tool"
			event.ToolName = "cursor-tool"
		default:
			return agentContractEvent{}, false
		}
		event.Sequence = int64(len(record.events) + 1)
		return event, true
	}
	if item.End {
		event.Kind = "finished"
		event.Sequence = int64(len(record.events) + 1)
		return event, true
	}
	return agentContractEvent{}, false
}

func buildAgentContractRunMessage(runID, sessionID, conversationID, workspaceRoot string, model AgentContractModel, payload agentContractStartRequest) (*agentv1.AgentClientMessage, error) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	mode := agentModeFromContract(payload.Mode)
	processWorkingDirectory := workspaceRoot
	conversationIDValue := conversationID
	requestContext := &agentv1.RequestContext{
		Env: &agentv1.RequestContextEnv{
			WorkspacePaths:          []string{workspaceRoot},
			ProjectFolder:           workspaceRoot,
			ProcessWorkingDirectory: &processWorkingDirectory,
			Shell:                   firstNonEmpty(os.Getenv("SHELL"), "powershell"),
			TimeZone:                time.Now().Location().String(),
		},
		EnvInfoComplete: boolPointer(true),
	}
	userMessage := &agentv1.UserMessage{
		Text:      payload.Prompt,
		MessageId: uuid.NewString(),
		Mode:      mode,
	}
	action := &agentv1.ConversationAction{
		Action: &agentv1.ConversationAction_UserMessageAction{
			UserMessageAction: &agentv1.UserMessageAction{
				UserMessage:    userMessage,
				RequestContext: requestContext,
			},
		},
	}
	return &agentv1.AgentClientMessage{
		Message: &agentv1.AgentClientMessage_RunRequest{
			RunRequest: &agentv1.AgentRunRequest{
				Action:            action,
				ConversationId:    &conversationIDValue,
				ConversationState: &agentv1.ConversationStateStructure{Mode: agentModePointer(mode)},
				ModelDetails: &agentv1.ModelDetails{
					ModelId:                     model.ModelID,
					DisplayModelId:              model.ModelID,
					DisplayName:                 model.Name,
					DisplayNameShort:            model.Name,
					SupportsAutoContext:         boolPointer(model.ContextWindowTokens > 0),
					AutoContextMaxTokens:        int32Pointer(model.ContextWindowTokens),
					ContextTokenLimit:           int32Pointer(model.ContextWindowTokens),
					ContextTokenLimitForMaxMode: int32Pointer(model.ContextWindowTokens),
				},
				RequestedModel: &agentv1.RequestedModel{ModelId: model.ID},
				RunId:          stringPointer(runID),
				AgentSessionId: stringPointer(sessionID),
			},
		},
	}, nil
}

func findAgentContractModel(items []AgentContractModel, requested string) (AgentContractModel, bool) {
	requested = strings.TrimSpace(requested)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == requested || strings.TrimSpace(item.ModelID) == requested {
			return item, true
		}
	}
	return AgentContractModel{}, false
}

func normalizeAgentContractMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "ask", "plan", "review":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "chat"
	}
}

func agentModeFromContract(mode string) agentv1.AgentMode {
	switch normalizeAgentContractMode(mode) {
	case "ask":
		return agentv1.AgentMode_AGENT_MODE_ASK
	case "plan":
		return agentv1.AgentMode_AGENT_MODE_PLAN
	default:
		return agentv1.AgentMode_AGENT_MODE_AGENT
	}
}

func normalizeAgentWorkspaceRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("工作区必须是绝对路径")
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("工作区目录不可用")
	}
	return root, nil
}

func decodeAgentContractJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_request", "JSON 请求体无效")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeAgentContractError(writer, http.StatusBadRequest, "invalid_request", "JSON 请求体不能包含多个值")
		return false
	}
	return true
}

func writeAgentContractJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAgentContractError(writer http.ResponseWriter, status int, code, message string) {
	writeAgentContractJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func boolPointer(value bool) *bool { return &value }

func int32Pointer(value int) *int32 {
	if value <= 0 || value > math.MaxInt32 {
		return nil
	}
	converted := int32(value)
	return &converted
}

func stringPointer(value string) *string { return &value }

func agentModePointer(value agentv1.AgentMode) *agentv1.AgentMode { return &value }
