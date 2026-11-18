package upstream

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"cursor/gen/aiserverv1"
	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"

	"google.golang.org/protobuf/proto"
)

func TestModelContextOptionsPreserveExactLimitsAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name               string
		cap, defaultTokens int
		custom, want       []int
	}{
		{"unknown", 0, 0, nil, nil},
		{"below smallest tier", 16000, 16000, nil, []int{16000}},
		{"exact 272k", 272000, 272000, nil, []int{272000, 32000, 64000, 128000, 200000, 256000}},
		{"standard cap deduplicated", 256000, 256000, nil, []int{256000, 32000, 64000, 128000, 200000}},
		{"small default retained", 1000000, 16000, []int{96000, 96000, 272123, 1000001, -1, 0}, []int{16000, 32000, 64000, 128000, 200000, 256000, 500000, 1000000, 96000, 272123}},
		{"invalid old default", 16000, 1000000, []int{32000}, []int{16000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelContextOptions(tc.cap, tc.defaultTokens, tc.custom...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("options: got %v, want %v", got, tc.want)
			}
		})
	}
}

func contextTestEnumValues(t *testing.T, entry map[string]any, id string) []string {
	t.Helper()
	definition, exists := findModelParameterDefinition(entry, id)
	if !exists {
		t.Fatalf("missing definition %q", id)
	}
	values := definition["parameterType"].(map[string]any)["enumParameter"].(map[string]any)["values"].([]map[string]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value["value"].(string))
	}
	return result
}

func TestAvailableModelsContextCartesianProduct(t *testing.T) {
	for _, exploded := range []bool{false, true} {
		for _, fast := range []bool{false, true} {
			t.Run(fmt.Sprintf("exploded=%v/fast=%v", exploded, fast), func(t *testing.T) {
				adapter := legacyruntime.ModelAdapterConfig{
					ID: "context-channel", ModelID: "gpt-5.6", Type: "openai", DisplayName: "Context Model",
					ReasoningEffort: "adaptive", CustomThinkingEfforts: []string{"adaptive"},
					ContextWindowTokens: 272000, FastMode: fast,
				}
				entry := buildAvailableModelEntriesForMode([]legacyruntime.ModelAdapterConfig{adapter}, true, exploded)[0]
				contexts := contextTestEnumValues(t, entry, "context")
				wantContexts := []string{"272000", "32000", "64000", "128000", "200000", "256000"}
				if !reflect.DeepEqual(contexts, wantContexts) {
					t.Fatalf("context options: got %v, want %v", contexts, wantContexts)
				}
				efforts := contextTestEnumValues(t, entry, "reasoning")
				fastOptions := []string{""}
				if fast {
					fastOptions = []string{"false", "true"}
				}
				variants := entry["variants"].([]map[string]any)
				if len(variants) != len(contexts)*len(efforts)*len(fastOptions) {
					t.Fatalf("incomplete Cartesian product: got %d variants", len(variants))
				}
				seen, ids := make(map[string]bool), make(map[string]bool)
				defaults := 0
				for _, variant := range variants {
					parameters := modelVariantParameters(variant)
					key := parameters["context"] + "/" + parameters["reasoning"] + "/" + parameters["fast"]
					if seen[key] {
						t.Fatalf("duplicate parameter combination: %s", key)
					}
					seen[key] = true
					id := variant["variantStringRepresentation"].(string)
					if ids[id] {
						t.Fatalf("duplicate variant ID: %s", id)
					}
					ids[id] = true
					if variant["isDefaultNonMaxConfig"] == true {
						defaults++
						if parameters["context"] != "272000" || parameters["reasoning"] != "adaptive" || parameters["fast"] == "true" {
							t.Fatalf("default changed: %v", parameters)
						}
					}
					if variant["isMaxMode"] != false {
						t.Fatalf("unexpected MaxMode: %v", variant)
					}
				}
				if defaults != 1 {
					t.Fatalf("default count: got %d, want 1", defaults)
				}
				for _, tokens := range contexts {
					for _, effort := range efforts {
						for _, speed := range fastOptions {
							// Cursor can replace only Effort or Fast and still find the same small Context.
							key := tokens + "/" + effort + "/" + speed
							if !seen[key] {
								t.Fatalf("unmatchable selection: %s", key)
							}
						}
						if !ids["context-channel:"+effort] {
							t.Fatalf("old variant ID lost for effort %s", effort)
						}
					}
				}
				if _, err := encodeMockProto("aiserver.v1.AvailableModelsResponse", map[string]any{"models": []map[string]any{entry}}); err != nil {
					t.Fatalf("wire compatibility: %v", err)
				}
			})
		}
	}
}

func TestAvailableModelsContextBudgetCapsAndLegacyMetadata(t *testing.T) {
	for _, tc := range []struct {
		name         string
		adapter      legacyruntime.ModelAdapterConfig
		wantCap      int
		wantMetadata bool
	}{
		{"tiny channel", legacyruntime.ModelAdapterConfig{ModelID: "gpt-5.6", ContextWindowTokens: 8000}, 8000, true},
		{"small custom channel", legacyruntime.ModelAdapterConfig{ModelID: "gpt-5.6", ContextWindowTokens: 96001}, 96001, true},
		{"known capacity clamps config", legacyruntime.ModelAdapterConfig{ModelID: "gpt-4o", ContextWindowTokens: 1000000}, 128000, true},
		{"unknown conservative", legacyruntime.ModelAdapterConfig{ModelID: "unknown-context-model"}, 200000, false},
		{"unknown uses channel limit", legacyruntime.ModelAdapterConfig{ModelID: "unknown-context-model", ContextWindowTokens: 64000}, 64000, true},
		{"unknown explicit large channel", legacyruntime.ModelAdapterConfig{ModelID: "unknown-context-model", ContextWindowTokens: 1000000}, 1000000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.adapter.ID = "budget-channel"
			if got := modelcontext.ResolveContextBudget(modelcontext.ContextBudgetInput{ModelID: tc.adapter.ModelID, ConfiguredTokens: tc.adapter.ContextWindowTokens}).EffectiveTokens; got != tc.wantCap {
				t.Fatalf("shared API fixture cap: got %d, want %d", got, tc.wantCap)
			}
			for _, parameters := range []bool{false, true} {
				entry := buildAvailableModelEntriesForMode([]legacyruntime.ModelAdapterConfig{tc.adapter}, parameters, true)[0]
				for _, field := range []string{"contextTokenLimit", "contextTokenLimitForMaxMode", "autoContextMaxTokens"} {
					value, exists := entry[field]
					if exists != tc.wantMetadata || (exists && value != tc.wantCap) {
						t.Fatalf("metadata %s: got %v, exists=%v, want cap=%d exists=%v", field, value, exists, tc.wantCap, tc.wantMetadata)
					}
				}
				if !parameters {
					if _, exists := entry["parameterDefinitions"]; exists {
						t.Fatal("legacy definitions unexpectedly enabled")
					}
					if _, exists := entry["variants"]; exists {
						t.Fatal("legacy variants unexpectedly enabled")
					}
					continue
				}
				contexts := contextTestEnumValues(t, entry, "context")
				if contexts[0] != strconv.Itoa(tc.wantCap) {
					t.Fatalf("old context default changed: %v", contexts)
				}
				for _, value := range contexts {
					tokens, err := strconv.Atoi(value)
					if err != nil || tokens <= 0 || tokens > tc.wantCap {
						t.Fatalf("context exceeds cap: %q", value)
					}
				}
				if tc.wantCap < 32000 && len(contexts) != 1 {
					t.Fatalf("tiny model has oversized tiers: %v", contexts)
				}
				defaults := 0
				for _, variant := range entry["variants"].([]map[string]any) {
					if variant["isDefaultNonMaxConfig"] == true {
						defaults++
					}
				}
				if defaults != 1 {
					t.Fatalf("default count: %d", defaults)
				}
				if !tc.wantMetadata {
					tooltip := entry["tooltipData"].(map[string]any)["markdownContent"].(string)
					if strings.Contains(tooltip, "上下文窗口") {
						t.Fatalf("unknown capacity advertised as recognized: %s", tooltip)
					}
				}
			}
			cli := buildCLIModelDetails([]legacyruntime.ModelAdapterConfig{tc.adapter})[0]
			if value, exists := cli["contextTokenLimit"]; exists != tc.wantMetadata || (exists && value != tc.wantCap) {
				t.Fatalf("CLI context metadata: %v", cli)
			}
		})
	}
}

func TestAvailableModelsRetainsLegacyVariantContract(t *testing.T) {
	variants := buildThinkingEffortVariants("openai", "legacy-channel", "Legacy Model", "tooltip", "high")
	defaults := 0
	for _, variant := range variants {
		parameters := modelVariantParameters(variant)
		if len(parameters) != 1 || variant["variantStringRepresentation"] != "legacy-channel:"+parameters["reasoning"] {
			t.Fatalf("effort-only variant contract changed: %v", variant)
		}
		if variant["isDefaultNonMaxConfig"] == true {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("effort-only defaults: %d", defaults)
	}

	// Older config can carry max/custom default values without a populated custom list.
	for _, effort := range []string{"max", "adaptive"} {
		entry := buildAvailableModelEntries([]legacyruntime.ModelAdapterConfig{{ID: "legacy-channel", ModelID: "unknown", ReasoningEffort: effort, ContextWindowTokens: 16000}})[0]
		defaults := 0
		for _, variant := range entry["variants"].([]map[string]any) {
			if variant["isDefaultNonMaxConfig"] == true {
				defaults++
			}
		}
		if defaults != 1 {
			t.Fatalf("legacy %s default count: %d", effort, defaults)
		}
	}

	request, err := proto.Marshal(&aiserverv1.AvailableModelsRequest{UseModelParameters: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	reqCtx := newRequestContextWithAdapters([]legacyruntime.ModelAdapterConfig{{ID: "legacy-channel", ModelID: "unknown", ContextWindowTokens: 16000}})
	reqCtx.RequestBody = request
	payload, err := AvailableModelsMockBuilder(reqCtx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encodeMockProto("aiserver.v1.AvailableModelsResponse", payload); err != nil {
		t.Fatalf("legacy wire format: %v", err)
	}
}
