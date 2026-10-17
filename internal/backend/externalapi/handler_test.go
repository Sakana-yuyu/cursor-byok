package externalapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
	legacyruntime "cursor/internal/runtime"
)

func newExternalTestHandler(t *testing.T, upstreamURL string, provider string, group string) http.Handler {
	t.Helper()
	config := serverconfig.DefaultConfig()
	config.ExternalAPI.Enabled = true
	config.ExternalAPI.APIKey = "local-test-key"
	config.ModelAdapters = []serverconfig.ModelAdapterConfig{{
		Source:            legacyruntime.ModelSourceThirdParty,
		CredentialScope:   legacyruntime.CredentialScopeAdapterAPIKey,
		DisplayName:       "测试模型",
		GroupName:         "test-provider",
		Type:              provider,
		ProtocolMode:      "fixed",
		ProtocolGroup:     group,
		BaseURL:           upstreamURL,
		APIKey:            "provider-secret",
		TooltipData:       "测试通道",
		ModelID:           "provider-model",
		ReasoningEffort:   "medium",
		OpenAIEndpoint:    "/v1/chat/completions",
		AnthropicAuthMode: "x_api_key",
	}}
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), t.TempDir())
	manager, err := serverconfig.NewManager(t.Context(), store)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	if _, err := manager.Save(t.Context(), config); err != nil {
		t.Fatalf("保存测试配置失败: %v", err)
	}
	return NewHandler(manager)
}

func doExternalRequest(t *testing.T, handler http.Handler, method string, path string, body string, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1:18090"+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestExternalAPIRequiresLocalKeyAndListsOnlyConfiguredModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("读取模型清单不应访问上游")
	}))
	defer upstream.Close()
	handler := newExternalTestHandler(t, upstream.URL, "openai", "chat_completions")

	if response := doExternalRequest(t, handler, http.MethodGet, "/byok/v1/models", "", "wrong"); response.Code != http.StatusUnauthorized {
		t.Fatalf("错误密钥状态码 = %d，期望 401", response.Code)
	}
	response := doExternalRequest(t, handler, http.MethodGet, "/byok/v1/models", "", "local-test-key")
	if response.Code != http.StatusOK {
		t.Fatalf("模型清单状态码 = %d，响应=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析模型清单失败: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "test-provider/provider-model" {
		t.Fatalf("模型清单 = %#v", payload.Data)
	}
}

func TestExternalAPIForwardsNativeChatBodyAndStream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("上游路径 = %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Errorf("上游认证头未按渠道注入: %q", request.Header.Get("Authorization"))
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("读取上游请求失败: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("上游请求不是 JSON: %v", err)
			return
		}
		if body["model"] != "provider-model" || body["temperature"] != float64(0.2) {
			t.Errorf("模型或扩展字段丢失: %#v", body)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("工具字段丢失: %#v", body["tools"])
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("data: {\"id\":\"upstream\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()
	handler := newExternalTestHandler(t, upstream.URL, "openai", "chat_completions")
	body := `{"model":"test-provider/provider-model","stream":true,"temperature":0.2,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`
	response := doExternalRequest(t, handler, http.MethodPost, "/byok/v1/chat/completions", body, "local-test-key")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatalf("流式响应 = %d %q", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("上游请求次数 = %d，期望 1", calls.Load())
	}
}

func TestExternalAPIRejectsProtocolMismatchBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	handler := newExternalTestHandler(t, upstream.URL, "openai", "chat_completions")
	response := doExternalRequest(t, handler, http.MethodPost, "/byok/v1/responses", `{"model":"test-provider/provider-model","input":"hello"}`, "local-test-key")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("协议错配状态码 = %d，响应=%s", response.Code, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("协议错配仍访问上游，请求次数=%d", calls.Load())
	}
}

func TestExternalAPIAnthropicMessagesUsesConfiguredAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			t.Errorf("Anthropic 上游路径 = %s", request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "provider-secret" || request.Header.Get("Authorization") != "" {
			t.Errorf("Anthropic 认证头 = x-api-key:%q authorization:%q", request.Header.Get("x-api-key"), request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"msg_1","type":"message","content":[]}`))
	}))
	defer upstream.Close()
	handler := newExternalTestHandler(t, upstream.URL, "anthropic", "messages")
	response := doExternalRequest(t, handler, http.MethodPost, "/byok/v1/messages", `{"model":"test-provider/provider-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`, "local-test-key")
	if response.Code != http.StatusOK {
		t.Fatalf("Anthropic 转发状态码 = %d，响应=%s", response.Code, response.Body.String())
	}
}

func TestExternalAPIRejectsNonLoopbackAndEmptyEnabledKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("非环回请求不应访问上游")
	}))
	defer upstream.Close()
	handler := newExternalTestHandler(t, upstream.URL, "openai", "chat_completions")
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18090/byok/v1/models", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("非环回状态码 = %d，期望 403", recorder.Code)
	}

	config := serverconfig.DefaultConfig()
	config.ExternalAPI.Enabled = true
	store := serverconfig.NewStore(filepath.Join(t.TempDir(), "config.yaml"), t.TempDir())
	if _, err := store.Save(t.Context(), config); err == nil {
		t.Fatal("启用 External API 且 Key 为空时应拒绝保存")
	}
}
