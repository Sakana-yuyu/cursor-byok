package upstream

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"cursor/gen/agentv1"
	legacyruntime "cursor/internal/runtime"

	"google.golang.org/protobuf/proto"
)

func TestBuildCLIModelDetailsPreservesChannelCredentials(t *testing.T) {
	adapters := []legacyruntime.ModelAdapterConfig{
		{ID: " channel-a ", ModelID: "model-a", APIKey: "provider-secret-a", BaseURL: "https://provider-a.example/v1"},
		{ID: "channel-b", ModelID: "model-a"},
		{ID: "", ModelID: "model-c"},
	}

	got := buildCLIModelDetails(adapters)
	want := []map[string]any{
		{"modelId": "channel-a", "displayModelId": "channel-a", "apiKeyCredentials": map[string]any{"apiKey": "provider-secret-a", "baseUrl": "https://provider-a.example/v1"}},
		{"modelId": "channel-b", "displayModelId": "channel-b", "apiKeyCredentials": map[string]any{"apiKey": "", "baseUrl": ""}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("build CLI model details: got %v, want %v", got, want)
	}
}

func TestEncodeCLIModelsUsesAgentModelDetailsWireFormat(t *testing.T) {
	payload := map[string]any{"models": buildCLIModelDetails([]legacyruntime.ModelAdapterConfig{{ID: "channel-a", APIKey: "provider-secret", BaseURL: "https://provider.example/v1"}})}
	encoded, err := encodeMockProto("aiserver.v1.GetUsableModelsResponse", payload)
	if err != nil {
		t.Fatalf("encode CLI models: %v", err)
	}

	response := &agentv1.GetUsableModelsResponse{}
	if err := proto.Unmarshal(encoded, response); err != nil {
		t.Fatalf("decode CLI models with agent proto: %v", err)
	}
	if len(response.Models) != 1 {
		t.Fatalf("decoded model count: got %d, want 1", len(response.Models))
	}
	model := response.Models[0]
	if model.GetModelId() != "channel-a" || model.GetDisplayModelId() != "channel-a" {
		t.Fatalf("decoded channel IDs: model=%q display=%q", model.GetModelId(), model.GetDisplayModelId())
	}
	if credentials := model.GetApiKeyCredentials(); credentials == nil || credentials.GetApiKey() != "provider-secret" || credentials.GetBaseUrl() != "https://provider.example/v1" {
		t.Fatalf("decoded relay credentials: %#v", credentials)
	}
}

func TestBuildBootstrapStatsigConfigJSONDisablesAlwaysLocalDecompositionGate(t *testing.T) {
	payload, err := buildBootstrapStatsigConfigJSON(12345, "test-auth-id")
	if err != nil {
		t.Fatalf("build bootstrap statsig config: %v", err)
	}

	var decoded statsigBootstrapTemplate
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode bootstrap statsig config: %v", err)
	}

	gate, ok := decoded.FeatureGates[bootstrapStatsigDecomposeAlwaysLocalExtHostGate]
	if !ok {
		t.Fatalf("missing feature gate %q", bootstrapStatsigDecomposeAlwaysLocalExtHostGate)
	}
	if value, _ := gate["value"].(bool); value {
		t.Fatalf("expected %q to be disabled", bootstrapStatsigDecomposeAlwaysLocalExtHostGate)
	}
	if ruleID, _ := gate["rule_id"].(string); ruleID != "local_disabled" {
		t.Fatalf("unexpected rule_id: %q", ruleID)
	}
}

type fakeSystemSettingService struct {
	adapters []legacyruntime.ModelAdapterConfig
}

func (f *fakeSystemSettingService) ResolveModelAdapters(context.Context) ([]legacyruntime.ModelAdapterConfig, error) {
	return f.adapters, nil
}

func newRequestContextWithAdapters(adapters []legacyruntime.ModelAdapterConfig) *RequestContext {
	return &RequestContext{
		Deps: &Dependencies{
			SystemSettingService: &fakeSystemSettingService{adapters: adapters},
		},
	}
}

func TestEncodeDefaultModelForCliUsesAgentModelDetailsWireFormat(t *testing.T) {
	reqCtx := newRequestContextWithAdapters([]legacyruntime.ModelAdapterConfig{{ID: "channel-a", APIKey: "provider-secret", BaseURL: "https://provider.example/v1"}})
	payload, err := buildDefaultModelForCliPayload(reqCtx)
	if err != nil {
		t.Fatalf("build default model for cli: %v", err)
	}
	encoded, err := encodeMockProto("aiserver.v1.GetDefaultModelForCliResponse", payload)
	if err != nil {
		t.Fatalf("encode default model for cli: %v", err)
	}

	response := &agentv1.GetDefaultModelForCliResponse{}
	if err := proto.Unmarshal(encoded, response); err != nil {
		t.Fatalf("decode default model for cli with agent proto: %v", err)
	}
	model := response.GetModel()
	if model == nil {
		t.Fatal("decoded default model is nil")
	}
	if model.GetModelId() != "channel-a" || model.GetDisplayModelId() != "channel-a" {
		t.Fatalf("decoded channel IDs: model=%q display=%q", model.GetModelId(), model.GetDisplayModelId())
	}
	if credentials := model.GetApiKeyCredentials(); credentials == nil || credentials.GetApiKey() != "provider-secret" || credentials.GetBaseUrl() != "https://provider.example/v1" {
		t.Fatalf("decoded relay credentials: %#v", credentials)
	}
}

func TestEncodeDefaultModelForCliEmptyAdaptersYieldsEmptyModel(t *testing.T) {
	payload, err := buildDefaultModelForCliPayload(newRequestContextWithAdapters(nil))
	if err != nil {
		t.Fatalf("build default model for cli: %v", err)
	}
	encoded, err := encodeMockProto("aiserver.v1.GetDefaultModelForCliResponse", payload)
	if err != nil {
		t.Fatalf("encode empty default model for cli: %v", err)
	}

	response := &agentv1.GetDefaultModelForCliResponse{}
	if err := proto.Unmarshal(encoded, response); err != nil {
		t.Fatalf("decode empty default model for cli: %v", err)
	}
	if response.GetModel() != nil && response.GetModel().GetModelId() != "" {
		t.Fatalf("expected empty model, got %#v", response.GetModel())
	}
}

func TestEncodeEmptyMockPayloads(t *testing.T) {
	cases := []struct {
		typeName string
		payload  map[string]any
	}{
		{"aiserver.v1.TrackEventsResponse", map[string]any{}},
		{"aiserver.v1.GetTeamAdminSettingsResponse", map[string]any{}},
		{"aiserver.v1.GetTeamReposResponse", map[string]any{}},
		{"aiserver.v1.GetGlobalCommandsResponse", map[string]any{}},
		{"aiserver.v1.GetCliDownloadUrlResponse", map[string]any{}},
		{"aiserver.v1.SubmitLogsResponse", map[string]any{"success": true}},
		{"aiserver.v1.ListMarketplacesResponse", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			// 空 payload 必须能走通 protojson 解码 + proto 编码，
			// 这是 EmptyMockBuilder/SubmitLogsMockBuilder 在真实路由上的唯一通路。
			if _, err := encodeMockProto(tc.typeName, tc.payload); err != nil {
				t.Fatalf("encode %s with payload %v: %v", tc.typeName, tc.payload, err)
			}
		})
	}
}
