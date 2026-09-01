package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
	"cursor/gen/aiserverv1/aiserverv1connect"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/logger"
	"github.com/google/uuid"
)

type usageLookupRecord struct {
	InputTokens  int64
	OutputTokens int64
	CreatedAt    time.Time
}

const (
	dashboardServiceGetTokenUsageProcedure                  = "/aiserver.v1.DashboardService/GetTokenUsage"
	dashboardServiceGetGlassEarlyPreviewEnrollmentProcedure = "/aiserver.v1.DashboardService/GetGlassEarlyPreviewEnrollment"
	nameTabInputMaxRunes                                    = 4000
	nameTabOutputMaxRunes                                   = 24
	nameTabMaxOutputTokens                                  = 96
)

func newAIHandler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Infof("newAIHandler unmapped path=%s method=%s", r.URL.Path, r.Method)
		http.NotFound(w, r)
	})
	mux.Handle(
		dashboardServiceGetTokenUsageProcedure,
		connect.NewUnaryHandler(dashboardServiceGetTokenUsageProcedure, service.GetTokenUsage),
	)
	mux.Handle(
		dashboardServiceGetGlassEarlyPreviewEnrollmentProcedure,
		connect.NewUnaryHandler(dashboardServiceGetGlassEarlyPreviewEnrollmentProcedure, service.GetGlassEarlyPreviewEnrollment),
	)
	mux.Handle(
		aiserverv1connect.AiServiceCountTokensProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceCountTokensProcedure, service.CountTokens),
	)
	mux.Handle(
		aiserverv1connect.AiServiceGetThoughtAnnotationProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceGetThoughtAnnotationProcedure, service.GetThoughtAnnotation),
	)
	mux.Handle(
		aiserverv1connect.AiServiceWriteGitCommitMessageProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceWriteGitCommitMessageProcedure, service.WriteGitCommitMessage),
	)
	mux.Handle(
		aiserverv1connect.AiServiceNameTabProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceNameTabProcedure, service.NameTab),
	)
	mux.Handle(
		aiserverv1connect.AiServiceCreateExperimentalIndexProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceCreateExperimentalIndexProcedure, service.CreateExperimentalIndex),
	)
	mux.Handle(
		aiserverv1connect.AiServiceListExperimentalIndexFilesProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceListExperimentalIndexFilesProcedure, service.ListExperimentalIndexFiles),
	)
	mux.Handle(
		aiserverv1connect.AiServiceListenExperimentalIndexProcedure,
		connect.NewServerStreamHandler(aiserverv1connect.AiServiceListenExperimentalIndexProcedure, service.ListenExperimentalIndex),
	)
	mux.Handle(
		aiserverv1connect.AiServiceRegisterFileToIndexProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceRegisterFileToIndexProcedure, service.RegisterFileToIndex),
	)
	mux.Handle(
		aiserverv1connect.AiServiceSetupIndexDependenciesProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceSetupIndexDependenciesProcedure, service.SetupIndexDependencies),
	)
	mux.Handle(
		aiserverv1connect.AiServiceComputeIndexTopoSortProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceComputeIndexTopoSortProcedure, service.ComputeIndexTopoSort),
	)
	mux.Handle(
		aiserverv1connect.AiServiceDocumentationQueryProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceDocumentationQueryProcedure, service.DocumentationQuery),
	)
	mux.Handle(
		aiserverv1connect.AiServiceAvailableDocsProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceAvailableDocsProcedure, service.AvailableDocs),
	)
	mux.Handle(
		aiserverv1connect.AiServiceKnowledgeBaseAddProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceKnowledgeBaseAddProcedure, service.KnowledgeBaseAdd),
	)
	mux.Handle(
		aiserverv1connect.AiServiceKnowledgeBaseListProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceKnowledgeBaseListProcedure, service.KnowledgeBaseList),
	)
	mux.Handle(
		aiserverv1connect.AiServiceKnowledgeBaseRemoveProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceKnowledgeBaseRemoveProcedure, service.KnowledgeBaseRemove),
	)
	mux.Handle(
		aiserverv1connect.AiServiceKnowledgeBaseUpdateProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceKnowledgeBaseUpdateProcedure, service.KnowledgeBaseUpdate),
	)
	mux.Handle(
		aiserverv1connect.AiServiceFetchRelevantKnowledgeForConversationProcedure,
		connect.NewUnaryHandler(aiserverv1connect.AiServiceFetchRelevantKnowledgeForConversationProcedure, service.FetchRelevantKnowledgeForConversation),
	)
	return mux
}

// NameTab 为 Cursor 新任务生成侧边栏和标题栏使用的简短摘要。
func (service *Service) NameTab(ctx context.Context, req *connect.Request[aiserverv1.NameTabRequest]) (*connect.Response[aiserverv1.NameTabResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name tab request is required"))
	}
	source := nameTabSourceText(req.Msg.GetMessages())
	if source == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name tab message is required"))
	}

	fallback := fallbackTabName(source)
	if service == nil || service.provider == nil {
		return connect.NewResponse(&aiserverv1.NameTabResponse{Name: fallback}), nil
	}

	requestID := "name-tab-" + uuid.NewString()
	modelID, modelSource, _ := service.resolveCommitMessageModelID(ctx)
	generated := ""
	err := service.provider.StartStream(ctx, ProviderRequest{
		RequestID:      requestID,
		ConversationID: strings.TrimSpace(req.Msg.GetConversationId()),
		RunID:          requestID,
		ModelCallID:    requestID + "-model",
		ModelID:        modelID,
		Mode:           agentv1.AgentMode_AGENT_MODE_AGENT,
		ThinkingEffort: "disabled",
		Messages: []modeladapter.Message{
			{Role: "system", Content: "Generate a concise task title from the user's request. Use the same language as the request. For Chinese use 6-18 characters; for other languages use 2-6 words. Preserve important code identifiers, commands, paths, and product names. Return only the title without quotes, markdown, punctuation, or explanation."},
			{Role: "user", Content: source},
		},
		MaxTokens:      nameTabMaxOutputTokens,
		CompileSummary: "generate task title model_source=" + modelSource,
	}, func(event modeladapter.ModelEvent) error {
		if event.Kind == modeladapter.ModelEventKindTextDelta {
			generated += event.Text
		}
		return nil
	})
	name := cleanTabName(generated)
	if err != nil || name == "" {
		logger.Infof("NameTab provider fallback conversation_id=%s error=%v", strings.TrimSpace(req.Msg.GetConversationId()), err)
		name = fallback
	}
	return connect.NewResponse(&aiserverv1.NameTabResponse{Name: name}), nil
}

func nameTabSourceText(messages []*aiserverv1.ConversationMessage) string {
	for _, message := range messages {
		if message == nil || message.GetType() != aiserverv1.ConversationMessage_MESSAGE_TYPE_HUMAN {
			continue
		}
		if text := compactTabText(message.GetText(), nameTabInputMaxRunes); text != "" {
			return text
		}
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if text := compactTabText(message.GetText(), nameTabInputMaxRunes); text != "" {
			return text
		}
	}
	return ""
}

func cleanTabName(value string) string {
	name := firstCommitMessageLine(stripCommitMessageCodeFence(value))
	for _, prefix := range []string{"标题：", "标题:", "title:", "task title:"} {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			name = strings.TrimSpace(name[len(prefix):])
			break
		}
	}
	name = strings.Trim(strings.TrimSpace(name), "`'\"“”‘’。，！？!?：:")
	return compactTabText(name, nameTabOutputMaxRunes)
}

func fallbackTabName(value string) string {
	name := compactTabText(value, nameTabInputMaxRunes)
	for _, prefix := range []string{"请帮我", "帮我", "请", "麻烦", "我想", "我需要"} {
		name = strings.TrimSpace(strings.TrimPrefix(name, prefix))
	}
	name = strings.NewReplacer(
		"最新的代码", "最新代码",
		"，然后", "并",
		"然后", "并",
		"查看审查", "审查",
	).Replace(name)
	name = strings.Trim(strings.TrimSpace(name), "`'\"“”‘’。，！？!?：:")
	return compactTabText(name, nameTabOutputMaxRunes)
}

func compactTabText(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func (service *Service) GetThoughtAnnotation(_ context.Context, req *connect.Request[aiserverv1.GetThoughtAnnotationRequest]) (*connect.Response[aiserverv1.GetThoughtAnnotationResponse], error) {
	requestID := strings.TrimSpace(req.Msg.GetRequestId())
	thought, ok, err := service.lookupThoughtAnnotation(requestID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !ok {
		return connect.NewResponse(&aiserverv1.GetThoughtAnnotationResponse{}), nil
	}
	return connect.NewResponse(&aiserverv1.GetThoughtAnnotationResponse{
		ThoughtAnnotation: &aiserverv1.AiThoughtAnnotation{
			RequestId: requestID,
			Thought:   thought,
		},
	}), nil
}

func (service *Service) GetTokenUsage(_ context.Context, req *connect.Request[aiserverv1.GetTokenUsageRequest]) (*connect.Response[aiserverv1.GetTokenUsageResponse], error) {
	record, ok, err := service.lookupUsageRecord(strings.TrimSpace(req.Msg.GetUsageUuid()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !ok {
		return connect.NewResponse(&aiserverv1.GetTokenUsageResponse{}), nil
	}
	return connect.NewResponse(&aiserverv1.GetTokenUsageResponse{
		InputTokens:  clampInt64ToInt32(record.InputTokens),
		OutputTokens: clampInt64ToInt32(record.OutputTokens),
	}), nil
}

func (service *Service) GetGlassEarlyPreviewEnrollment(context.Context, *connect.Request[aiserverv1.GetGlassEarlyPreviewEnrollmentRequest]) (*connect.Response[aiserverv1.GetGlassEarlyPreviewEnrollmentResponse], error) {
	granted := true
	return connect.NewResponse(&aiserverv1.GetGlassEarlyPreviewEnrollmentResponse{
		Enabled:                           true,
		EnterpriseGlassSelfEnrollEligible: &granted,
		GlassAccessGranted:                &granted,
	}), nil
}

func (service *Service) CountTokens(_ context.Context, req *connect.Request[aiserverv1.CountTokensRequest]) (*connect.Response[aiserverv1.CountTokensResponse], error) {
	total := int64(0)
	details := make([]*aiserverv1.ContextItemTokenDetail, 0, len(req.Msg.GetContextItems()))
	for _, item := range req.Msg.GetContextItems() {
		count := estimateContextItemTokens(item)
		total += count
		details = append(details, &aiserverv1.ContextItemTokenDetail{
			RelativeWorkspacePath: contextItemRelativeWorkspacePath(item),
			Count:                 clampInt64ToInt32(count),
			LineCount:             lineCountForContextItem(item),
		})
	}
	return connect.NewResponse(&aiserverv1.CountTokensResponse{
		Count:        clampInt64ToInt32(total),
		TokenDetails: details,
	}), nil
}

func (service *Service) lookupUsageRecord(usageUUID string) (usageLookupRecord, bool, error) {
	if service == nil {
		return usageLookupRecord{}, false, nil
	}
	if service.usageStore == nil {
		return usageLookupRecord{}, false, nil
	}
	item, ok, err := service.usageStore.LookupEvent(strings.TrimSpace(usageUUID))
	if err != nil || !ok {
		return usageLookupRecord{}, ok, err
	}
	return usageLookupRecord{
		InputTokens:  item.InputTokens + item.CacheReadTokens + item.CacheWriteTokens,
		OutputTokens: item.OutputTokens,
		CreatedAt:    item.At,
	}, true, nil
}

func (service *Service) lookupThoughtAnnotation(requestID string) (string, bool, error) {
	if service == nil || service.store == nil {
		return "", false, nil
	}
	needle := strings.TrimSpace(requestID)
	if needle == "" {
		return "", false, nil
	}
	foundRequest := false
	foundThought := false
	latestThought := ""
	latestThoughtAt := time.Time{}
	conversationIDs, err := service.store.ListConversationIDs()
	if err != nil {
		return "", false, err
	}
	for _, conversationID := range conversationIDs {
		conversation, err := service.store.LoadConversation(conversationID)
		if err != nil {
			return "", false, err
		}
		if conversation == nil {
			continue
		}
		for _, entry := range conversation.Entries {
			if strings.TrimSpace(entry.RequestID) != needle {
				continue
			}
			foundRequest = true
			if strings.TrimSpace(entry.Kind) != "metadata" {
				continue
			}
			var payload metadataPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Type) != "thought_annotation" {
				continue
			}
			if strings.TrimSpace(readStringValue(payload.Value["kind"])) != "summary_completed" {
				continue
			}
			thought := strings.TrimSpace(readStringValue(payload.Value["thought"]))
			if thought == "" {
				continue
			}
			if !foundThought || entry.CreatedAt.After(latestThoughtAt) {
				foundThought = true
				latestThought = thought
				latestThoughtAt = entry.CreatedAt
			}
		}
	}
	if foundThought {
		return latestThought, true, nil
	}
	if foundRequest {
		return defaultSummaryCompletedThought, true, nil
	}
	return "", false, nil
}

func lineCountForContextItem(item *aiserverv1.ContextItem) int32 {
	if item == nil {
		return 0
	}
	if chunk := item.GetFileChunk(); chunk != nil {
		return countTextLines(chunk.GetChunkContents())
	}
	if outline := item.GetOutlineChunk(); outline != nil {
		return lineCountForRange(outline.GetFullRange())
	}
	if selection := item.GetCmdKSelection(); selection != nil {
		return int32(len(selection.GetLines()))
	}
	if sparse := item.GetSparseFileChunk(); sparse != nil {
		return int32(len(sparse.GetLines()))
	}
	return 0
}

func contextItemRelativeWorkspacePath(item *aiserverv1.ContextItem) string {
	if item == nil {
		return ""
	}
	if chunk := item.GetFileChunk(); chunk != nil {
		return strings.TrimSpace(chunk.GetRelativeWorkspacePath())
	}
	if outline := item.GetOutlineChunk(); outline != nil {
		return strings.TrimSpace(outline.GetRelativeWorkspacePath())
	}
	if sparse := item.GetSparseFileChunk(); sparse != nil {
		return strings.TrimSpace(sparse.GetRelativeWorkspacePath())
	}
	return ""
}

func lineCountForRange(lineRange *aiserverv1.LineRange) int32 {
	if lineRange == nil {
		return 0
	}
	start := lineRange.GetStartLineNumber()
	end := lineRange.GetEndLineNumberInclusive()
	if start <= 0 || end < start {
		return 0
	}
	return end - start + 1
}

func countTextLines(text string) int32 {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return int32(strings.Count(text, "\n") + 1)
}

func readInt64Value(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint32:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint32(0)>>1) {
			return int64(^uint32(0) >> 1)
		}
		return int64(typed)
	case json.Number:
		value, err := typed.Int64()
		if err == nil {
			return value
		}
	}
	return 0
}

func readStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func readBoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func readTimeValue(value any) time.Time {
	return parseRFC3339Time(readStringValue(value))
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}
