// provider.go 把 forwarder 的 canonical 请求转交给现有的 provider adapter 层。
package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"cursor/internal/appdata"
	modeladapter "cursor/internal/backend/agent/model"
)

type provider400RecoveryReason string

const provider400RecoveryContentExists provider400RecoveryReason = "content_exists"

// classifyProvider400Recovery returns a non-empty reason only for a known
// relay-side transient 400. Generic 400 responses remain terminal so malformed
// requests are not retried indefinitely.
func classifyProvider400Recovery(err error) provider400RecoveryReason {
	if err == nil {
		return ""
	}
	var statusErr *modeladapter.HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != 400 {
		return ""
	}
	message := strings.ToLower(strings.TrimSpace(statusErr.Message))
	body := strings.ToLower(strings.TrimSpace(statusErr.Body))
	if exactContentExistsMessage(message) || exactContentExistsMessage(body) {
		return provider400RecoveryContentExists
	}
	return ""
}

func exactContentExistsMessage(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "content exists" || value == "content_exists" {
		return true
	}
	return strings.HasSuffix(value, "body=content exists") || strings.HasSuffix(value, "body=content_exists")
}

type DefaultProviderGateway struct {
	router modeladapter.ModelAdapterRouter
}

// NewProviderGateway 创建默认 provider 网关。
//
// 当 resolver 能提供本地响应缓存配置时，返回一个精确匹配响应缓存包装网关；
// 该缓存默认关闭，关闭时 StartStream 直接透传底层网关，行为与今日完全一致。
func NewProviderGateway(resolver modeladapter.ChannelResolver) ProviderGateway {
	base := &DefaultProviderGateway{
		router: modeladapter.NewRouter(resolver),
	}
	if settingsProvider, ok := resolver.(localResponseCacheSettingsProvider); ok {
		// 缓存条目持久化到磁盘（L2 层），跨进程/重启保留；持久化开关关闭时退回纯内存。
		persistPath := localResponseCachePersistPath()
		if _, _, _, persist := settingsProvider.LocalResponseCacheSettings(); !persist {
			persistPath = ""
		}
		return newCachingProviderGateway(base, settingsProvider.LocalResponseCacheSettings, persistPath)
	}
	return base
}

// localResponseCachePersistPath 返回本地响应缓存的磁盘持久化路径。
func localResponseCachePersistPath() string {
	return filepath.Join(appdata.DataRootPath(), "local-response-cache.json")
}

// StartStream 把 forwarder 的 provider 请求翻译成 modeladapter.StreamRequest 并发起流式调用。
func (gateway *DefaultProviderGateway) StartStream(ctx context.Context, req ProviderRequest, sink func(modeladapter.ModelEvent) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer releaseArtifactSession(req.Observer, req.RequestID, req.ModelCallID)
	requestKnobs := make(map[string]any, len(req.RequestKnobs)+2)
	for key, value := range req.RequestKnobs {
		requestKnobs[key] = value
	}
	requestKnobs["stream"] = true
	if req.MaxTokens > 0 {
		requestKnobs["max_tokens"] = req.MaxTokens
	}
	if strings.TrimSpace(req.ThinkingEffort) != "" {
		requestKnobs["runtime_thinking_effort"] = strings.TrimSpace(req.ThinkingEffort)
	}
	err := gateway.router.Stream(ctx, modeladapter.StreamRequest{
		RequestID:           req.RequestID,
		RunID:               req.RunID,
		ModelCallID:         req.ModelCallID,
		ConversationID:      req.ConversationID,
		Mode:                req.Mode,
		ModelID:             req.ModelID,
		ModelName:           req.ModelName,
		Role:                req.Role,
		ParentModel:         req.ParentModel,
		ModelGroupID:        req.ModelGroupID,
		TaskID:              req.TaskID,
		ExecutionMode:       req.ExecutionMode,
		SupervisorModel:     req.SupervisorModel,
		ReviewerModel:       req.ReviewerModel,
		ThinkingEffort:      req.ThinkingEffort,
		MaxMode:             req.MaxMode,
		Messages:            req.Messages,
		StableMessageCount:  req.StableMessageCount,
		Tools:               append([]json.RawMessage(nil), req.Tools...),
		MaxTokens:           req.MaxTokens,
		Stream:              true,
		RequestKnobs:        requestKnobs,
		CompileSummary:      req.CompileSummary,
		Observer:            req.Observer,
		ArtifactPaths:       req.ArtifactPaths,
		RequestBodyOverride: req.RequestBodyOverride,
	}, sink)
	if err != nil {
		return providerTerminalError{cause: err}
	}
	return nil
}

type artifactSessionCleaner interface {
	ClearActiveArtifacts(requestID string, modelCallID string)
}

func releaseArtifactSession(observer modeladapter.LLMArtifactObserver, requestID string, modelCallID string) {
	cleaner, ok := observer.(artifactSessionCleaner)
	if !ok {
		return
	}
	cleaner.ClearActiveArtifacts(requestID, modelCallID)
}
