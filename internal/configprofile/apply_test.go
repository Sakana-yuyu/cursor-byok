package configprofile

import (
	"errors"
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func applyTestConfig() serverconfig.Config {
	cfg := serverconfig.DefaultConfig()
	cfg.ModelAdapters = []serverconfig.ModelAdapterConfig{{
		ID:                   "adapter-1",
		DisplayName:          "Test",
		Type:                 "openai",
		BaseURL:              "https://example.invalid/v1",
		APIKey:               "sk-secret",
		ModelID:              "gpt-test",
		ReasoningEffort:      "medium",
		OpenAIEndpoint:       "/v1/responses",
		ContextWindowTokens:  272_000,
		MaxCompletionTokens:  65_536,
		OpenAIServiceTier:    "priority",
		FastMode:             true,
		ProtocolMode:         "auto",
		SupplierID:           "openai",
		AnthropicMaxTokens:   1_000,
		ThinkingBudgetTokens: 2_000,
	}}
	return cfg
}

func TestApplyRoundTripsAdapterFieldsAndKeepsCredentials(t *testing.T) {
	store := New(t.TempDir())
	base := applyTestConfig()
	summary, err := store.SaveCurrent("demo", "desc", []string{"models"}, base)
	if err != nil {
		t.Fatal(err)
	}

	current := applyTestConfig()
	current.ModelAdapters[0].DisplayName = "改名后的模型"
	current.ModelAdapters[0].ProtocolMode = "fixed"
	current.ModelAdapters[0].ContextWindowTokens = 8_000
	current.ModelAdapters[0].MaxCompletionTokens = 4_096
	current.ModelAdapters[0].FastMode = false
	current.ModelAdapters[0].OpenAIServiceTier = ""
	current.ModelAdapters[0].APIKey = "sk-rotated"

	prep, err := store.PrepareApply(summary.ID, current)
	if err != nil {
		t.Fatal(err)
	}
	var persisted serverconfig.Config
	result, err := store.ExecuteApply(prep.ConfirmationToken, current, func(next serverconfig.Config) (serverconfig.Config, error) {
		persisted = next
		return next, nil
	})
	if err != nil {
		t.Fatalf("ExecuteApply() error = %v", err)
	}
	if result.State != "succeeded" {
		t.Fatalf("ExecuteApply() state = %q, want succeeded", result.State)
	}

	gotAdapter := persisted.ModelAdapters[0]
	wantAdapter := base.ModelAdapters[0]
	if gotAdapter.DisplayName != wantAdapter.DisplayName || gotAdapter.ProtocolMode != wantAdapter.ProtocolMode {
		t.Errorf("displayName/protocolMode = %q/%q, want %q/%q", gotAdapter.DisplayName, gotAdapter.ProtocolMode, wantAdapter.DisplayName, wantAdapter.ProtocolMode)
	}
	if gotAdapter.ContextWindowTokens != wantAdapter.ContextWindowTokens || gotAdapter.MaxCompletionTokens != wantAdapter.MaxCompletionTokens {
		t.Errorf("context/max tokens = %d/%d, want %d/%d", gotAdapter.ContextWindowTokens, gotAdapter.MaxCompletionTokens, wantAdapter.ContextWindowTokens, wantAdapter.MaxCompletionTokens)
	}
	if gotAdapter.FastMode != wantAdapter.FastMode || gotAdapter.OpenAIServiceTier != wantAdapter.OpenAIServiceTier {
		t.Errorf("fastMode/openAIServiceTier = %t/%q, want %t/%q", gotAdapter.FastMode, gotAdapter.OpenAIServiceTier, wantAdapter.FastMode, wantAdapter.OpenAIServiceTier)
	}
	if gotAdapter.APIKey != "sk-rotated" {
		t.Errorf("APIKey = %q, want current credential sk-rotated preserved", gotAdapter.APIKey)
	}
}

func TestPreviewReportsFieldLevelChangesOnly(t *testing.T) {
	store := New(t.TempDir())
	base := applyTestConfig()
	summary, err := store.SaveCurrent("demo", "desc", []string{"models", "routing"}, base)
	if err != nil {
		t.Fatal(err)
	}

	current := applyTestConfig()
	current.ModelAdapters[0].DisplayName = "改名后的模型"
	current.ModelAdapters[0].MaxCompletionTokens = 4_096
	preview, err := store.Preview(summary.ID, current)
	if err != nil {
		t.Fatal(err)
	}

	wantPaths := map[string]bool{
		"/models/adapter-1/displayName":         false,
		"/models/adapter-1/maxCompletionTokens": false,
	}
	for _, change := range preview.Changes {
		if _, ok := wantPaths[change.Path]; ok {
			wantPaths[change.Path] = true
		} else if change.Path != "" {
			t.Errorf("unexpected change path %q", change.Path)
		}
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Errorf("missing field-level change %q in %#v", path, preview.Changes)
		}
	}
}

func TestExecuteApplyReportsTruthfulRollbackState(t *testing.T) {
	store := New(t.TempDir())
	base := applyTestConfig()
	summary, err := store.SaveCurrent("demo", "desc", []string{"models"}, base)
	if err != nil {
		t.Fatal(err)
	}
	current := applyTestConfig()
	prep, err := store.PrepareApply(summary.ID, current)
	if err != nil {
		t.Fatal(err)
	}

	result, err := store.ExecuteApply(prep.ConfirmationToken, current, func(serverconfig.Config) (serverconfig.Config, error) {
		return serverconfig.Config{}, errors.New("persist refused")
	})
	if err == nil {
		t.Fatal("ExecuteApply() error = nil, want failure")
	}
	if result.State != "rolled_back" {
		t.Fatalf("state = %q, want rolled_back", result.State)
	}
	if result.RollbackState != "failed" {
		t.Fatalf("RollbackState = %q, want failed (persist always fails)", result.RollbackState)
	}
}
