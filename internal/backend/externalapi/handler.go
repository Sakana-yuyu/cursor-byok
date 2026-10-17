package externalapi

// handler.go 实现上游更新中提供的本机 External API：它只做协议同形的原生转发，
// 不把请求投影成内部 Agent 事件，因而不会丢失工具、图片、推理或供应商扩展字段。

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	modeladapter "cursor/internal/backend/agent/model"
	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/logger"
	"cursor/internal/modelchannel"
	"cursor/internal/netproxy"
	legacyruntime "cursor/internal/runtime"
)

const (
	requestBodyLimit  int64 = 8 << 20
	responseBodyLimit int64 = 64 << 20
	publicRoutePrefix       = "/byok/v1"
)

type protocol string

const (
	protocolChat      protocol = "chat"
	protocolResponses protocol = "responses"
	protocolMessages  protocol = "messages"
)

// NewHandler 创建挂载在 /byok/v1 下的本机 External API。
func NewHandler(manager *serverconfig.Manager) http.Handler {
	return &handler{
		manager: manager,
		client: &http.Client{
			Transport: netproxy.NewTransport(nil),
			Timeout:   30 * time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type handler struct {
	manager *serverconfig.Manager
	client  *http.Client
}

type publicModel struct {
	ID          string
	AdapterID   string
	DisplayName string
	Provider    string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "external API 仅允许本机访问")
		return
	}
	if !h.authorize(w, r) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, publicRoutePrefix)
	if path == "" {
		path = "/"
	}
	switch {
	case r.Method == http.MethodGet && path == "/models":
		h.listModels(w, r)
	case r.Method == http.MethodPost && path == "/chat/completions":
		h.forward(w, r, protocolChat)
	case r.Method == http.MethodPost && path == "/responses":
		h.forward(w, r, protocolResponses)
	case r.Method == http.MethodPost && path == "/messages":
		h.forward(w, r, protocolMessages)
	default:
		writeError(w, http.StatusNotFound, "external API 路径不存在")
	}
}

func (h *handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if h == nil || h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "外部 API 配置未初始化")
		return false
	}
	config := h.manager.Current().ExternalAPI
	if !config.Enabled {
		writeError(w, http.StatusForbidden, "external API is disabled")
		return false
	}
	key := strings.TrimSpace(config.APIKey)
	if key == "" {
		writeError(w, http.StatusServiceUnavailable, "外部 API 已开启但尚未设置 API Key")
		return false
	}
	supplied := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if supplied == "" {
		supplied = bearerToken(r.Header.Get("Authorization"))
	}
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(key)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return false
	}
	return true
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil || strings.TrimSpace(r.RemoteAddr) == "" {
		return true // httptest and in-process callers do not set RemoteAddr.
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *handler) listModels(w http.ResponseWriter, _ *http.Request) {
	models, err := h.models()
	if err != nil {
		writeError(w, http.StatusConflict, "读取模型列表失败")
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, item := range models {
		data = append(data, map[string]any{
			"id": item.ID, "object": "model", "created": 0,
			"owned_by": "cursor-byok", "name": item.DisplayName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (h *handler) models() ([]publicModel, error) {
	if h == nil || h.manager == nil {
		return nil, errors.New("config manager is nil")
	}
	adapters, err := serverconfig.NormalizeModelAdapterConfigs(h.manager.Current().ModelAdapters)
	if err != nil {
		return nil, err
	}
	result := make([]publicModel, 0, len(adapters))
	seen := make(map[string]struct{}, len(adapters))
	for _, adapter := range adapters {
		if adapter.Disabled || adapter.Source != legacyruntime.ModelSourceThirdParty ||
			(adapter.Type != "openai" && adapter.Type != "anthropic") {
			continue
		}
		id := publicModelID(adapter)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, publicModel{ID: id, AdapterID: adapter.ID, DisplayName: adapter.DisplayName, Provider: adapter.Type})
	}
	return result, nil
}

func publicModelID(adapter serverconfig.ModelAdapterConfig) string {
	group := strings.TrimSpace(adapter.GroupName)
	if group == "" {
		if parsed, err := url.Parse(strings.TrimSpace(adapter.BaseURL)); err == nil {
			group = strings.TrimSpace(parsed.Host)
		}
	}
	if group == "" {
		group = strings.TrimSpace(adapter.BaseURL)
	}
	modelID := strings.TrimSpace(adapter.ModelID)
	if group == "" || modelID == "" {
		return ""
	}
	return group + "/" + modelID
}

func (h *handler) forward(w http.ResponseWriter, r *http.Request, proto protocol) {
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	publicID, stream, err := requestMeta(proto, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	models, err := h.models()
	if err != nil {
		writeError(w, http.StatusConflict, "读取模型列表失败")
		return
	}
	var selected *publicModel
	for index := range models {
		if models[index].ID == publicID {
			selected = &models[index]
			break
		}
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "model not found")
		return
	}
	channel, err := h.manager.SelectChannelForModel(r.Context(), selected.AdapterID)
	if err != nil || channel == nil {
		writeError(w, http.StatusConflict, "模型通道不可用")
		return
	}
	if err := validateProtocol(proto, channel); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := modelchannel.ResolveTransportPlan(modelchannel.TransportPlanInput{
		Provider: channel.Provider, BaseURL: channel.BaseURL, ModelID: channel.Model,
		ProtocolMode: modelchannel.ProtocolModeFixed, ProtocolGroup: expectedGroup(proto),
		OpenAIEndpoint: channel.OpenAIEndpoint, Stream: stream,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型通道地址解析失败")
		return
	}
	bodyBytes, err := rewriteBody(body, channel.Model, channel)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型通道参数合并失败")
		return
	}
	upstream, err := h.newRequest(r, plan.RequestURL, bodyBytes, channel, proto)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模型通道请求构造失败")
		return
	}
	response, err := h.client.Do(upstream)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeError(w, http.StatusBadGateway, "上游模型请求失败")
		return
	}
	defer response.Body.Close()
	if stream && response.StatusCode >= 200 && response.StatusCode < 300 {
		h.copyStream(w, response, h.manager.Current().ProviderStreamIdleTimeout)
		return
	}
	h.copyBody(w, response)
}

func requestMeta(proto protocol, body map[string]json.RawMessage) (string, bool, error) {
	var modelID string
	if raw, ok := body["model"]; ok && json.Unmarshal(raw, &modelID) == nil {
		modelID = strings.TrimSpace(modelID)
	}
	if modelID == "" {
		return "", false, errors.New("model is required")
	}
	stream := false
	if raw, ok := body["stream"]; ok {
		if err := json.Unmarshal(raw, &stream); err != nil {
			return "", false, errors.New("stream must be a boolean")
		}
	}
	switch proto {
	case protocolChat:
		if !jsonArray(body["messages"]) {
			return "", false, errors.New("messages must be an array")
		}
	case protocolResponses:
		if raw, ok := body["input"]; !ok || (len(bytes.TrimSpace(raw)) == 0 || (bytes.TrimSpace(raw)[0] != '"' && bytes.TrimSpace(raw)[0] != '[')) {
			return "", false, errors.New("input must be a string or an array")
		}
	case protocolMessages:
		if !jsonArray(body["messages"]) {
			return "", false, errors.New("messages must be an array")
		}
	default:
		return "", false, errors.New("unsupported protocol")
	}
	return modelID, stream, nil
}

func jsonArray(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '['
}

func expectedGroup(proto protocol) string {
	switch proto {
	case protocolChat:
		return modelchannel.ProtocolGroupChatCompletions
	case protocolResponses:
		return modelchannel.ProtocolGroupResponses
	case protocolMessages:
		return modelchannel.ProtocolGroupAnthropicMessages
	default:
		return ""
	}
}

func validateProtocol(proto protocol, channel *legacyruntime.ResolvedChannel) error {
	if channel == nil {
		return errors.New("模型通道不可用")
	}
	provider := strings.ToLower(strings.TrimSpace(channel.Provider))
	group := strings.TrimSpace(channel.ProtocolGroup)
	switch proto {
	case protocolMessages:
		if provider != "anthropic" {
			return errors.New("该模型通道不是 Anthropic Messages 协议")
		}
	case protocolChat:
		if provider != "openai" || (group != modelchannel.ProtocolGroupChatCompletions && group != modelchannel.ProtocolGroupChatCompletionsCompat) {
			return errors.New("该模型通道不是 OpenAI Chat Completions 协议")
		}
	case protocolResponses:
		if provider != "openai" || group != modelchannel.ProtocolGroupResponses {
			return errors.New("该模型通道不是 OpenAI Responses 协议")
		}
	default:
		return errors.New("unsupported protocol")
	}
	return nil
}

func rewriteBody(body map[string]json.RawMessage, modelID string, channel *legacyruntime.ResolvedChannel) ([]byte, error) {
	body["model"], _ = json.Marshal(strings.TrimSpace(modelID))
	if channel != nil {
		var extras string
		if channel.Provider == "openai" && channel.OpenAIExtraParamsEnabled {
			extras = channel.OpenAIExtraParamsJSON
		} else if channel.Provider == "anthropic" && channel.AnthropicExtraParamsEnabled {
			extras = channel.AnthropicExtraParamsJSON
		}
		if strings.TrimSpace(extras) != "" {
			var values map[string]json.RawMessage
			if err := json.Unmarshal([]byte(extras), &values); err != nil {
				return nil, err
			}
			for key, value := range values {
				if !protectedBodyKey(key) {
					body[key] = value
				}
			}
		}
	}
	return json.Marshal(body)
}

func protectedBodyKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "model", "stream", "messages", "input", "instructions", "tools", "max_tokens", "max_completion_tokens", "max_output_tokens", "reasoning_effort", "reasoning", "thinking", "output_config":
		return true
	default:
		return false
	}
}

func (h *handler) newRequest(incoming *http.Request, target string, body []byte, channel *legacyruntime.ResolvedChannel, proto protocol) (*http.Request, error) {
	request, err := http.NewRequestWithContext(incoming.Context(), http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	copySafeHeaders(request.Header, incoming.Header)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Del("Content-Length")
	if proto == protocolMessages {
		for key, value := range modelchannel.AnthropicGeneratedAuthHeaders(target, channel.AnthropicAuthMode, channel.APIKey) {
			request.Header.Del(key)
			for _, item := range value {
				request.Header.Add(key, item)
			}
		}
		if request.Header.Get("anthropic-version") == "" {
			request.Header.Set("anthropic-version", "2023-06-01")
		}
	} else {
		request.Header.Set("Authorization", "Bearer "+modelchannel.AnthropicAuthToken(channel.APIKey))
	}
	if channel.CustomHeadersEnabled {
		if err := modeladapter.ApplyCustomHeaders(request, true, channel.CustomHeadersJSON); err != nil {
			return nil, err
		}
	}
	// 不允许渠道自定义头重新把本机 API Key 或目标主机暴露给其他地址。
	request.Header.Del("Host")
	request.Header.Del("Content-Length")
	return request, nil
}

func copySafeHeaders(dst, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "authorization", "x-api-key", "host", "content-length", "content-type", "connection", "transfer-encoding", "accept-encoding", "cookie", "proxy-authorization", "proxy-authenticate", "te", "trailer", "upgrade":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func (h *handler) copyStream(w http.ResponseWriter, response *http.Response, idleSeconds int) {
	copyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	idle := time.Duration(idleSeconds) * time.Second
	if idle <= 0 {
		idle = 90 * time.Second
	}
	for {
		chunk, err := readWithIdle(response.Body, 32*1024, idle)
		if len(chunk) > 0 {
			if _, writeErr := w.Write(chunk); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Warn("external API 上游流读取失败", "error", err.Error())
			}
			return
		}
	}
}

func (h *handler) copyBody(w http.ResponseWriter, response *http.Response) {
	copyResponseHeaders(w.Header(), response.Header)
	limited := io.LimitReader(response.Body, responseBodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取上游响应失败")
		return
	}
	if int64(len(body)) > responseBodyLimit {
		writeError(w, http.StatusBadGateway, "上游响应超过大小限制")
		return
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func readWithIdle(body io.ReadCloser, size int, idle time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		buffer := make([]byte, size)
		n, err := body.Read(buffer)
		resultCh <- result{data: buffer[:n], err: err}
	}()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	select {
	case value := <-resultCh:
		return value.data, value.err
	case <-timer.C:
		_ = body.Close()
		return nil, fmt.Errorf("上游流空闲超过 %s", idle)
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "content-length", "content-encoding", "transfer-encoding", "connection", "set-cookie":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func readJSONBody(r *http.Request) (map[string]json.RawMessage, error) {
	if r.Body == nil {
		return nil, errors.New("请求体不能为空")
	}
	limited := io.LimitReader(r.Body, requestBodyLimit+1)
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		return nil, errors.New("请求体必须是合法 JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("请求体只能包含一个 JSON 对象")
		}
		return nil, errors.New("请求体超过大小限制或包含多余数据")
	}
	if body == nil {
		return nil, errors.New("请求体必须是 JSON 对象")
	}
	return body, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"message": message,
		"type":    map[bool]string{true: "invalid_request_error", false: "server_error"}[status >= 400 && status < 500],
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
