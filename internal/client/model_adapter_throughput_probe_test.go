//go:build benchmark

package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/modelchannel"
)

// TestIsolatedThroughputProbeDoesNotTryEndpointFallback 锁定独立测速的只读边界：
// 只测试当前配置的协议，不能为了取得成功结果改走其他端点。
func TestIsolatedThroughputProbeDoesNotTryEndpointFallback(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/v1/responses" {
			http.Error(writer, "endpoint unsupported", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"1 2 3\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	service := &ProxyService{}
	_, err := service.RunModelAdapterThroughputProbe(serverconfig.ModelAdapterConfig{
		ID:                 "probe",
		DisplayName:        "probe",
		TooltipData:        "probe",
		Type:               "openai",
		BaseURL:            server.URL,
		APIKey:             "test-key",
		ModelID:            "test-model",
		OpenAIEndpoint:     modelchannel.OpenAIEndpointResponses,
		OpenAIRequestGroup: modelchannel.OpenAIRequestGroupResponses,
	})
	if err == nil {
		t.Fatal("isolated throughput probe succeeded by using an unconfigured fallback endpoint")
	}
	if len(paths) != 1 || paths[0] != "/v1/responses" {
		t.Fatalf("unexpected request paths: %v", paths)
	}
}
