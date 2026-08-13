package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"cursor/gen/agentv1"
	"cursor/gen/aiserverv1"
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

// Design Mode 的 composer pill 与 canvas 内联预览完全是客户端本地能力，
// 唯一的阻塞点是这两个 feature gate。bootstrap payload 里没列出的 gate 会被
// 客户端当作关闭处理，所以必须显式下发为 enabled。
func TestBuildBootstrapStatsigConfigJSONEnablesDesignModeAndCanvasPreviewGates(t *testing.T) {
	payload, err := buildBootstrapStatsigConfigJSON(12345, "test-auth-id")
	if err != nil {
		t.Fatalf("build bootstrap statsig config: %v", err)
	}

	var decoded statsigBootstrapTemplate
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode bootstrap statsig config: %v", err)
	}

	for _, name := range []string{
		bootstrapStatsigGlassDesignModeComposerPill,
		bootstrapStatsigCanvasInlinePreview,
	} {
		gate, ok := decoded.FeatureGates[name]
		if !ok {
			t.Fatalf("missing feature gate %q", name)
		}
		if value, _ := gate["value"].(bool); !value {
			t.Fatalf("expected %q to be enabled, got %#v", name, gate["value"])
		}
		if gateName, _ := gate["name"].(string); gateName != name {
			t.Fatalf("gate %q carries name %q", name, gateName)
		}
		if ruleID, _ := gate["rule_id"].(string); ruleID != "local_enabled" {
			t.Fatalf("gate %q unexpected rule_id: %q", name, ruleID)
		}
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

func float64Ptr(v float64) *float64 { return &v }

// mockCompatCase 描述 host.go 中一个 MockProtoAction 路由的（builder, proto 响应类型）映射。
// 该表与 internal/backend/host.go 的路由注册一一对应；新增 mock 路由时必须同步。
type mockCompatCase struct {
	name     string
	typeName string
	builder  func(*RequestContext) (map[string]any, error)
}

// mockCompatRoutes 汇总 host.go 中全部本地 mock proto 路由。
// 表的目的是把「builder 输出 ↔ 当前 gen/ 生成 proto」的兼容性固化为回归测试：
// Cursor 升级重新生成 proto 后，若 mock 输出的字段名/类型与新版 proto 不匹配，
// encodeMockProto（DiscardUnknown:false）会立即报错，测试失败并指明路由名。
var mockCompatRoutes = []mockCompatCase{
	{"server_time", "aiserver.v1.ServerTimeResponse", ServerTimeMockBuilder},
	{"server_config", "aiserver.v1.GetServerConfigResponse", ServerConfigMockBuilder},
	{"server_config_service_get_server_config", "aiserver.v1.GetServerConfigResponse", ServerConfigMockBuilder},
	{"available_models", "aiserver.v1.AvailableModelsResponse", AvailableModelsMockBuilder},
	{"usable_models", "aiserver.v1.GetUsableModelsResponse", UsableModelsMockBuilder},
	{"default_model_for_cli", "aiserver.v1.GetDefaultModelForCliResponse", DefaultModelForCliMockBuilder},
	{"default_model", "aiserver.v1.GetDefaultModelResponse", DefaultModelMockBuilder},
	{"default_model_nudge", "aiserver.v1.GetDefaultModelNudgeDataResponse", DefaultModelNudgeMockBuilder},
	{"bootstrap_statsig", "aiserver.v1.BootstrapStatsigResponse", BootstrapStatsigMockBuilder},
	{"first_window_statsig_decision", "aiserver.v1.GetFirstWindowStatsigDecisionResponse", FirstWindowStatsigDecisionMockBuilder},
	{"analytics_submit_logs", "aiserver.v1.SubmitLogsResponse", SubmitLogsMockBuilder},
	{"analytics_track_events", "aiserver.v1.TrackEventsResponse", EmptyMockBuilder},
	{"dashboard_current_period_usage", "aiserver.v1.GetCurrentPeriodUsageResponse", DashboardCurrentPeriodUsageMockBuilder},
	{"dashboard_get_teams", "aiserver.v1.GetTeamsResponse", DashboardTeamsMockBuilder},
	{"dashboard_get_managed_skills", "aiserver.v1.GetManagedSkillsResponse", DashboardManagedSkillsMockBuilder},
	{"dashboard_get_team_admin_settings_or_empty", "aiserver.v1.GetTeamAdminSettingsResponse", EmptyMockBuilder},
	{"dashboard_get_team_repos_or_empty", "aiserver.v1.GetTeamReposResponse", EmptyMockBuilder},
	{"dashboard_get_global_commands", "aiserver.v1.GetGlobalCommandsResponse", EmptyMockBuilder},
	{"dashboard_get_cli_download_url", "aiserver.v1.GetCliDownloadUrlResponse", EmptyMockBuilder},
	{"dashboard_get_me", "aiserver.v1.GetMeResponse", DashboardGetMeMockBuilder},
	{"dashboard_user_privacy_mode", "aiserver.v1.GetUserPrivacyModeResponse", DashboardUserPrivacyModeMockBuilder},
	{"dashboard_plan_info", "aiserver.v1.GetPlanInfoResponse", DashboardPlanInfoMockBuilder},
	{"dashboard_usage_limit_status", "aiserver.v1.GetUsageLimitStatusAndActiveGrantsResponse", DashboardUsageLimitStatusAndActiveGrantsMockBuilder},
	{"dashboard_is_on_new_pricing", "aiserver.v1.IsOnNewPricingResponse", DashboardIsOnNewPricingMockBuilder},
}

func newAuthRequestContext() *RequestContext {
	reqCtx := newRequestContextWithAdapters([]legacyruntime.ModelAdapterConfig{
		{ID: "channel-a", DisplayName: "Channel A", ModelID: "claude-sonnet-4-5", ContextWindowTokens: 200000, Pricing: &legacyruntime.ModelPricing{Input: float64Ptr(3.0)}},
	})
	// GetMe / BootstrapStatsig 等 builder 从 authorization 头解析 authID。
	// 注意：MIMEHeader.Get 会 canonicalize 键为 "Authorization"，必须用规范键构造，
	// 否则 Header.Get("authorization") 永远 miss，JWT 解析路径测不到。
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"local-test-user"}`))
	reqCtx.Headers = http.Header{"Authorization": []string{"Bearer eyJhbGciOiJub25lIn0." + payload + ".sig"}}
	return reqCtx
}

func TestDashboardGetMeResolvesAuthIDFromAuthorizationHeader(t *testing.T) {
	reqCtx := newAuthRequestContext()
	payload, err := DashboardGetMeMockBuilder(reqCtx)
	if err != nil {
		t.Fatalf("builder: %v", err)
	}
	// 必须从 Authorization 头的 JWT sub 解析出 local-test-user；
	// 若 Header 键构造错误（小写 key 被 MIMEHeader canonicalize miss），
	// 会回退到 localUltraPaymentID，此断言即失败。
	if got := payload["authId"]; got != "local-test-user" {
		t.Fatalf("authId: got %q, want local-test-user", got)
	}
}

func TestAllMockBuildersCompatibleWithCurrentProto(t *testing.T) {
	reqCtx := newAuthRequestContext()
	for _, tc := range mockCompatRoutes {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := tc.builder(reqCtx)
			if err != nil {
				t.Fatalf("builder 执行失败: %v", err)
			}
			if _, err := encodeMockProto(tc.typeName, payload); err != nil {
				t.Fatalf("builder 输出与当前 proto 不兼容（Cursor 升级后字段不匹配会在此报警）: %v", err)
			}
		})
	}
}

func TestAvailableModelsPayloadDecodesWithAdapters(t *testing.T) {
	reqCtx := newAuthRequestContext()
	payload, err := AvailableModelsMockBuilder(reqCtx)
	if err != nil {
		t.Fatalf("builder: %v", err)
	}
	encoded, err := encodeMockProto("aiserver.v1.AvailableModelsResponse", payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	response := &aiserverv1.AvailableModelsResponse{}
	if err := proto.Unmarshal(encoded, response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.GetModels()) != 1 {
		t.Fatalf("model count: got %d, want 1", len(response.GetModels()))
	}
	model := response.GetModels()[0]
	if model.GetName() != "channel-a" {
		t.Fatalf("model name: got %q, want channel-a", model.GetName())
	}
	if model.GetContextTokenLimit() != 200000 {
		t.Fatalf("contextTokenLimit: got %d, want 200000", model.GetContextTokenLimit())
	}
	if model.GetPrice() != 3.0 {
		t.Fatalf("price: got %v, want 3.0", model.GetPrice())
	}
}

func TestBuildAvailableModelEntriesEnrichesNativeMetadata(t *testing.T) {
	adapters := []legacyruntime.ModelAdapterConfig{
		{
			ID:                  "channel-a",
			DisplayName:         "Channel A",
			ModelID:             "claude-sonnet-4-5",
			TooltipData:         "我的渠道备注",
			ContextWindowTokens: 200000,
			Pricing:             &legacyruntime.ModelPricing{Input: float64Ptr(3.0), Output: float64Ptr(15.0), Currency: "USD"},
		},
		{ID: "channel-b", DisplayName: "Channel B", ModelID: "unknown-model-xyz", ContextWindowTokens: 0, Pricing: nil},
	}
	entries := buildAvailableModelEntries(adapters)
	if len(entries) != 2 {
		t.Fatalf("entry count: got %d, want 2", len(entries))
	}
	entryA := entries[0]
	if got, ok := entryA["contextTokenLimit"].(int); !ok || got != 200000 {
		t.Fatalf("contextTokenLimit: got %#v, want 200000", entryA["contextTokenLimit"])
	}
	if got, ok := entryA["contextTokenLimitForMaxMode"].(int); !ok || got != 200000 {
		t.Fatalf("contextTokenLimitForMaxMode: got %#v, want 200000", entryA["contextTokenLimitForMaxMode"])
	}
	if got, ok := entryA["autoContextMaxTokens"].(int); !ok || got != 200000 {
		t.Fatalf("autoContextMaxTokens: got %#v, want 200000", entryA["autoContextMaxTokens"])
	}
	if price, ok := entryA["price"].(float64); !ok || price != 3.0 {
		t.Fatalf("price: got %#v, want 3.0", entryA["price"])
	}
	if isUserAdded, ok := entryA["isUserAdded"].(bool); !ok || !isUserAdded {
		t.Fatalf("isUserAdded: got %#v, want true", entryA["isUserAdded"])
	}
	if supportsAutoContext, ok := entryA["supportsAutoContext"].(bool); !ok || !supportsAutoContext {
		t.Fatalf("supportsAutoContext: got %#v, want true", entryA["supportsAutoContext"])
	}
	// 未知模型（无上下文窗口、无价格）不得设置误导性字段。
	entryB := entries[1]
	for _, field := range []string{"contextTokenLimit", "contextTokenLimitForMaxMode", "autoContextMaxTokens", "price"} {
		if _, exists := entryB[field]; exists {
			t.Fatalf("entry for unknown model must not set %q, got %#v", field, entryB[field])
		}
	}
	// 增强字段必须能被当前 proto 严格解码（未知字段会直接报错），
	// 防止新增的 map 字段名与 aisserver.v1.AvailableModelsResponse 不一致。
	payload := map[string]any{"models": entries}
	encoded, err := encodeMockProto("aiserver.v1.AvailableModelsResponse", payload)
	if err != nil {
		t.Fatalf("encode available models payload: %v", err)
	}
	response := &aiserverv1.AvailableModelsResponse{}
	if err := proto.Unmarshal(encoded, response); err != nil {
		t.Fatalf("decode available models with aisserver proto: %v", err)
	}
	if len(response.GetModels()) != 2 {
		t.Fatalf("decoded model count: got %d, want 2", len(response.GetModels()))
	}
	decoded := response.GetModels()[0]
	if decoded.GetContextTokenLimit() != 200000 {
		t.Fatalf("decoded contextTokenLimit: got %d, want 200000", decoded.GetContextTokenLimit())
	}
	if decoded.GetContextTokenLimitForMaxMode() != 200000 {
		t.Fatalf("decoded contextTokenLimitForMaxMode: got %d, want 200000", decoded.GetContextTokenLimitForMaxMode())
	}
	if decoded.GetAutoContextMaxTokens() != 200000 {
		t.Fatalf("decoded autoContextMaxTokens: got %d, want 200000", decoded.GetAutoContextMaxTokens())
	}
	if decoded.GetPrice() != 3.0 {
		t.Fatalf("decoded price: got %v, want 3.0", decoded.GetPrice())
	}
	if !decoded.GetIsUserAdded() {
		t.Fatal("decoded isUserAdded: want true")
	}
	if !decoded.GetSupportsAutoContext() {
		t.Fatal("decoded supportsAutoContext: want true")
	}
	// tooltip 是模型选择器展开详情的实际渲染载体：必须包含上下文窗口与价格行，
	// 用户备注保留在前，元数据行以分隔线追加在后。
	tooltipA, ok := entryA["tooltipData"].(map[string]any)["markdownContent"].(string)
	if !ok {
		t.Fatalf("tooltipData.markdownContent: got %#v", entryA["tooltipData"])
	}
	for _, want := range []string{"200,000", "**输入价格：** $3"} {
		if !strings.Contains(tooltipA, want) {
			t.Fatalf("tooltip 缺少 %q，实际内容:\n%s", want, tooltipA)
		}
	}
	if !strings.HasPrefix(tooltipA, "我的渠道备注") {
		t.Fatalf("tooltip 应以用户备注开头，实际内容:\n%s", tooltipA)
	}
}

func TestBuildModelTooltipMarkdown(t *testing.T) {
	adapter := legacyruntime.ModelAdapterConfig{
		ModelID:             "model-x",
		ContextWindowTokens: 1000000,
		MaxCompletionTokens: 65536,
		Pricing:             &legacyruntime.ModelPricing{Input: float64Ptr(0.14), Currency: "USD"},
	}
	got := buildModelTooltipMarkdown("来自 provider", adapter, 1000000)
	for _, want := range []string{"来自 provider", "**上下文窗口：** 1,000,000 tokens", "**最大输出：** 65,536 tokens", "**输入价格：** $0.14 / 1M tokens"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tooltip 缺少 %q，实际内容:\n%s", want, got)
		}
	}
	// 行间用硬换行（行尾两空格 + \n）、末尾空行结尾：防止 Cursor 客户端
	// 在 tooltip 后追加内容（如输出价格行）时与最后一行粘连。
	for _, line := range []string{"**上下文窗口：** 1,000,000 tokens", "**最大输出：** 65,536 tokens", "**输入价格：** $0.14 / 1M tokens"} {
		if !strings.Contains(got, line+"  \n") && !strings.HasSuffix(got, line+"\n\n") {
			t.Fatalf("元数据行 %q 未以硬换行/尾空行结束，实际内容:\n%q", line, got)
		}
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("tooltip 应以空行结尾（避免追加内容粘连），实际内容:\n%q", got)
	}

	// 无备注 + 无元数据 → 空字符串
	if empty := buildModelTooltipMarkdown("", legacyruntime.ModelAdapterConfig{ModelID: "unknown"}, 0); empty != "" {
		t.Fatalf("未知模型 tooltip 应为空，got %q", empty)
	}

	// CNY 币种
	cny := legacyruntime.ModelAdapterConfig{
		ModelID: "model-cny", ContextWindowTokens: 128000,
		Pricing: &legacyruntime.ModelPricing{Input: float64Ptr(12.0), Currency: "CNY"},
	}
	gotCNY := buildModelTooltipMarkdown("", cny, 128000)
	if !strings.Contains(gotCNY, "¥12") {
		t.Fatalf("CNY 价格格式错误，实际内容:\n%s", gotCNY)
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1000000, "1,000,000"},
		{200000, "200,000"},
	}
	for _, tc := range cases {
		if got := formatTokenCount(tc.in); got != tc.want {
			t.Fatalf("formatTokenCount(%d): got %q, want %q", tc.in, got, tc.want)
		}
	}
}
