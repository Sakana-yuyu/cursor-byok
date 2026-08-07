package config

import (
	"testing"
)

// 构造一个最小的已知/未知模型 adapter，验证诊断类别的归属。
func TestDiagnoseModelAdaptersCatalogUncovered(t *testing.T) {
	adapters := []ModelAdapterConfig{
		{
			ID:          "ch-known",
			DisplayName: "已知模型",
			Type:        "openai",
			ModelID:     "claude-sonnet-4.6",
		},
		{
			ID:          "ch-unknown",
			DisplayName: "未知模型",
			Type:        "openai",
			ModelID:     "brand-new-model-xyz",
		},
		{
			ID:          "ch-unknown-other-type",
			DisplayName: "未知模型非openai",
			Type:        "anthropic",
			ModelID:     "mystery-vision-1",
		},
	}
	result := DiagnoseModelAdapters(adapters)
	if result.Total != len(adapters) {
		t.Fatalf("Total = %d, want %d", result.Total, len(adapters))
	}

	var knownIssues, unknownIssues, unknownAnthropicIssues int
	for _, issue := range result.Issues {
		switch issue.ChannelID {
		case "ch-known":
			if issue.Category == DiagnosticCategoryCatalogUncovered {
				t.Errorf("known model %q reported catalog_uncovered, want covered", issue.ModelID)
			}
			knownIssues++
		case "ch-unknown":
			if issue.Category != DiagnosticCategoryCatalogUncovered {
				t.Errorf("unknown model %q category = %q, want %q", issue.ModelID, issue.Category, DiagnosticCategoryCatalogUncovered)
			}
			unknownIssues++
		case "ch-unknown-other-type":
			if issue.Category != DiagnosticCategoryCatalogUncovered {
				t.Errorf("unknown anthropic model %q category = %q, want %q", issue.ModelID, issue.Category, DiagnosticCategoryCatalogUncovered)
			}
			unknownAnthropicIssues++
		}
	}
	if unknownIssues != 1 {
		t.Errorf("unknown model catalog_uncovered issues = %d, want 1", unknownIssues)
	}
	if unknownAnthropicIssues != 1 {
		t.Errorf("unknown anthropic model catalog_uncovered issues = %d, want 1", unknownAnthropicIssues)
	}
	// 已知模型可能仍触发 provider_mismatch（claude 配 openai），但不该有 catalog_uncovered。
	_ = knownIssues
}

func TestDiagnoseModelAdaptersProviderMismatchStillReported(t *testing.T) {
	// claude 配 openai 仍应报告 provider_mismatch，且同时不被目录覆盖误伤（claude 在目录中）。
	adapters := []ModelAdapterConfig{
		{ID: "ch", DisplayName: "claude", Type: "openai", ModelID: "claude-sonnet-4.6"},
	}
	result := DiagnoseModelAdapters(adapters)
	var mismatch, uncovered bool
	for _, issue := range result.Issues {
		if issue.Category == DiagnosticCategoryProviderMismatch {
			mismatch = true
			if issue.SuggestedValue != "anthropic" {
				t.Errorf("SuggestedValue = %q, want %q", issue.SuggestedValue, "anthropic")
			}
		}
		if issue.Category == DiagnosticCategoryCatalogUncovered {
			uncovered = true
		}
	}
	if !mismatch {
		t.Error("provider_mismatch not reported for claude-as-openai")
	}
	if uncovered {
		t.Error("claude-sonnet-4.6 reported catalog_uncovered, want covered")
	}
}

func TestDiagnoseModelAdaptersEmptyModelID(t *testing.T) {
	result := DiagnoseModelAdapters([]ModelAdapterConfig{
		{ID: "ch-empty", DisplayName: "空模型", Type: "openai", ModelID: ""},
	})
	if len(result.Issues) != 0 {
		t.Errorf("issues = %v, want none for empty modelID", result.Issues)
	}
}