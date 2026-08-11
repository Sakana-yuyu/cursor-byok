package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/client"
	"gopkg.in/yaml.v3"
)

type probeOutput struct {
	Status                  string  `json:"status"`
	TokensPerSecond         float64 `json:"tokensPerSecond"`
	VisibleTokensPerSecond  float64 `json:"visibleTokensPerSecond"`
	FirstResponseMS         int64   `json:"firstResponseMS"`
	FirstTextTokenMS        int64   `json:"firstTextTokenMS"`
	TotalDurationMS         int64   `json:"totalDurationMS"`
	OutputTokens            int64   `json:"outputTokens"`
	VisibleOutputTokens     int64   `json:"visibleOutputTokens"`
	ReasoningTokens         int64   `json:"reasoningTokens"`
	EffectiveThinkingEffort string  `json:"effectiveThinkingEffort"`
	TokensEstimated         bool    `json:"tokensEstimated"`
	Error                   string  `json:"error,omitempty"`
}

func main() {
	configPath := strings.TrimSpace(os.Getenv("THROUGHPUT_PROBE_CONFIG"))
	if configPath == "" {
		fmt.Fprintln(os.Stderr, "THROUGHPUT_PROBE_CONFIG is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read config failed")
		os.Exit(2)
	}
	var input serverconfig.Config
	if err := yaml.Unmarshal(raw, &input); err != nil {
		fmt.Fprintln(os.Stderr, "parse config failed")
		os.Exit(2)
	}
	cfg, err := serverconfig.NormalizeConfig(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "normalize config failed")
		os.Exit(2)
	}
	adapter, ok := selectAdapter(cfg)
	if !ok {
		fmt.Fprintln(os.Stderr, "no configured model adapter")
		os.Exit(2)
	}
	result, testErr := client.NewProxyService(nil, nil, nil).RunModelAdapterThroughputProbe(adapter)
	output := probeOutput{
		Status:                  result.Status,
		TokensPerSecond:         result.TokensPerSecond,
		VisibleTokensPerSecond:  result.VisibleTokensPerSecond,
		FirstResponseMS:         result.FirstResponseMS,
		FirstTextTokenMS:        result.FirstTextTokenMS,
		TotalDurationMS:         result.TotalDurationMS,
		OutputTokens:            result.OutputTokens,
		VisibleOutputTokens:     result.VisibleOutputTokens,
		ReasoningTokens:         result.ReasoningTokens,
		EffectiveThinkingEffort: result.EffectiveThinkingEffort,
		TokensEstimated:         result.TokensEstimated,
	}
	if testErr != nil {
		output.Error = "model adapter test failed"
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fmt.Fprintln(os.Stderr, "encode result failed")
		os.Exit(2)
	}
	if testErr != nil {
		os.Exit(1)
	}
}

func selectAdapter(cfg serverconfig.Config) (serverconfig.ModelAdapterConfig, bool) {
	selectedID := strings.TrimSpace(cfg.LastAgentModelHash)
	for _, adapter := range cfg.ModelAdapters {
		if strings.TrimSpace(adapter.ID) == selectedID {
			return adapter, true
		}
	}
	if len(cfg.ModelAdapters) == 0 {
		return serverconfig.ModelAdapterConfig{}, false
	}
	return cfg.ModelAdapters[0], true
}
