package upstream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"cursor/gen/aiserverv1"
	"cursor/internal/modelcontext"
	legacyruntime "cursor/internal/runtime"

	"google.golang.org/protobuf/proto"
)

const (
	availableModelsDisableUnusedHours = 2400000
	availableModelsUpgradeHours       = 2

	modelRuntimeThinkingEffortParameterID = "effort"
	modelRuntimeContextParameterID        = "context"
	modelRuntimeFastParameterID           = "fast"

	// localPathEncryptionKey — стабильный ключ шифрования путей для индексации
	// репозитория, который cursor-agent CLI запрашивает через GetServerConfig
	// (indexingConfig.default{User,Team}PathEncryptionKey). Без него CLI
	// не может инициализировать repo identity в git-воркспейсе и вызовы
	// файловых инструментов падают с "[unimplemented] HTTP 404".
	localPathEncryptionKey = "6f6e63652d6c6f63616c2d706174682d656e6372797074696f6e2d6b6579"

	localUltraMembershipType       = "ultra"
	localUltraPaymentID            = "local_ultra"
	localUltraSubscriptionStatus   = "active"
	localUltraPlanIncludedCents    = 20000
	localUltraDashboardUserID      = 1
	localUltraBillingCycleDuration = 30 * 24 * time.Hour

	bootstrapStatsigGlassModeAvailableGate           = "glass_mode_available"
	bootstrapStatsigGlassOpenAgentInWindowGate       = "glass.enable_open_agent_in_window"
	bootstrapStatsigOpenAgentsTitlebarGate           = "glass_open_agents_titlebar_button"
	bootstrapStatsigOpenAgentWindowTopGate           = "open_agent_window_top"
	bootstrapStatsigOpenAgentWindowBottomGate        = "open_agent_window_bottom_convo"
	bootstrapStatsigNALAgentRetriesGate              = "nal_agent_retries"
	bootstrapStatsigNALFreshRetryIDsGate             = "nal_fresh_retry_ids"
	bootstrapStatsigUseModelParametersGate           = "use_model_parameters"
	bootstrapStatsigUseReactModelPickerGate          = "use_react_model_picker"
	bootstrapStatsigIDECmdEnterSubmitGate            = "ide_cmd_enter_submit"
	bootstrapStatsigContextVisualizerGate            = "context_visualizer"
	bootstrapStatsigWysiwygMarkdownGate              = "wysiwyg_markdown"
	bootstrapStatsigWysiwygMarkdownDefaultGate       = "wysiwyg_markdown_default"
	bootstrapStatsigSubagentSupportInterrupt         = "subagent_support_interrupt"
	bootstrapStatsigExplicitSubagentModels           = "explicit_subagent_models"
	bootstrapStatsigMcpDirectClientToolFetch         = "mcp_direct_client_tool_fetch"
	bootstrapStatsigGlassCustomThemeSupport          = "glass_custom_theme_support"
	bootstrapStatsigGlassAutomationsUI               = "glass_automations_ui"
	bootstrapStatsigTerminalUI2                      = "terminal_ui_2"
	bootstrapStatsigDisableTerminalOutputUIStreaming = "disable_terminal_output_ui_streaming"
	bootstrapStatsigBrowserCanvas                    = "browser_canvas"
	bootstrapStatsigCanvasInlinePreview              = "canvas_inline_preview"
	bootstrapStatsigGlassDesignModeComposerPill      = "glass_design_mode_composer_pill_enabled"
	bootstrapStatsigEnableMultitaskMode              = "enable_multitask_mode"
	bootstrapStatsigDecomposeAlwaysLocalExtHostGate  = "decompose_always_local_ext_host"
	bootstrapStatsigCursorExtensionsIsolationV2Gate  = "cursor_extensions_isolation_v2"
	bootstrapStatsigCursorAgentWorkerExtension       = "enable_cursor_agent_worker_extension"
	bootstrapStatsigExperimentName                   = "free_user_model_picker"
	bootstrapStatsigVariantParam                     = "variant"
	bootstrapStatsigVariantControl                   = "control"
	bootstrapStatsigModelPickerExperimentsLayer      = "model_picker_experiments"
	bootstrapStatsigEffortFirstVariantParam          = "effort_first_variant"
	bootstrapStatsigEffortFirstCompactModelIDsParam  = "effort_first_compact_model_ids"
	bootstrapStatsigEffortFirstSubmenuExperiment     = "effort_first_submenu_2026_08"
	bootstrapStatsigExperimentEnabledParam           = "enabled"
	bootstrapStatsigVariantLockedPicker              = "locked_picker"
	bootstrapStatsigVariantGrayedModels              = "grayed_models"
	bootstrapStatsigProductTipsConfigName            = "product_tips_config"
	bootstrapStatsigIdleExtensionHostKiller          = "idle_extension_host_killer_config"
	bootstrapStatsigIdleMinutesToKill                = "idleMinutesToKillExtensionHost"
	bootstrapStatsigFreeMemoryPercentageToKill       = "freeMemoryPercentageToKillExtensionHost"
	bootstrapStatsigHTTP2PingConfig                  = "http2_ping_config"
	bootstrapStatsigHTTP1KeepaliveConfig             = "http1_keepalive_config"
	bootstrapStatsigHTTP2AgentPoolConfig             = "http2_agent_connection_pool_config"
	bootstrapStatsigCanvasPromptTextConfig           = "canvas_prompt_text_config"
	bootstrapStatsigEditorBugbotConfig               = "editor_bugbot_config"
	bootstrapStatsigExtensionMonitorControl          = "extension_monitor_control"
	bootstrapStatsigExtensionSignatureBypass         = "extension_signature_verification_bypass_list"
	bootstrapStatsigGCTraceControl                   = "gc_trace_control"
	bootstrapStatsigInlineDiffPerformance            = "inline_diff_performance_config"
	bootstrapStatsigLeakedDisposablesTracker         = "leaked_disposables_tracker"
	bootstrapStatsigMcpIPCTimeouts                   = "mcp_ipc_timeouts"
	bootstrapStatsigMcpWakeProbeConfig               = "mcp_wake_probe_config"
	bootstrapStatsigNALStallDetectorTimeout          = "nal_stall_detector_timeout_config"
	bootstrapStatsigSimulatedThinkingErrorTimeout    = "simulated_thinking_error_timeout"
	bootstrapStatsigPlaywrightLogConfigs             = "playwright_log_configs"
	bootstrapStatsigRetryInterceptorParams           = "retry_interceptor_params_config"
	bootstrapStatsigSandboxNetworkAllowlist          = "sandbox_default_network_allowlist"
	bootstrapStatsigUpdatePromptConfig               = "update_prompt_config"
	bootstrapStatsigLocalDefaultRule                 = "local_default"
)

type statsigSecondaryExposure struct {
	Gate           string `json:"gate,omitempty"`
	GateValue      string `json:"gateValue,omitempty"`
	GateValueSnake string `json:"gate_value,omitempty"`
	RuleID         string `json:"ruleID,omitempty"`
	RuleIDSnake    string `json:"rule_id,omitempty"`
}

type statsigDynamicConfigTemplate struct {
	Name                               string                     `json:"name"`
	Value                              map[string]any             `json:"value"`
	RuleID                             string                     `json:"rule_id"`
	RuleIDCamel                        string                     `json:"ruleID"`
	GroupName                          string                     `json:"group_name"`
	GroupNameCamel                     string                     `json:"groupName"`
	SecondaryExposures                 []statsigSecondaryExposure `json:"secondary_exposures"`
	SecondaryExposuresCamel            []statsigSecondaryExposure `json:"secondaryExposures"`
	UndelegatedSecondaryExposures      []statsigSecondaryExposure `json:"undelegated_secondary_exposures"`
	UndelegatedSecondaryExposuresCamel []statsigSecondaryExposure `json:"undelegatedSecondaryExposures"`
	IsDeviceBased                      bool                       `json:"is_device_based"`
	IsDeviceBasedCamel                 bool                       `json:"isDeviceBased"`
	IsExperimentActive                 bool                       `json:"is_experiment_active"`
	IsExperimentActiveCamel            bool                       `json:"isExperimentActive"`
	IsUserInExperiment                 bool                       `json:"is_user_in_experiment"`
	IsUserInExperimentCamel            bool                       `json:"isUserInExperiment"`
}

type statsigBootstrapTemplate struct {
	FeatureGates   map[string]map[string]any               `json:"feature_gates"`
	DynamicConfigs map[string]statsigDynamicConfigTemplate `json:"dynamic_configs"`
	LayerConfigs   map[string]map[string]any               `json:"layer_configs"`
	User           map[string]any                          `json:"user"`
	HasUpdates     bool                                    `json:"has_updates"`
	HashUsed       string                                  `json:"hash_used"`
	SDKParams      map[string]any                          `json:"sdkParams"`
	Time           int64                                   `json:"time"`
}

var bootstrapStatsigTemplate = statsigBootstrapTemplate{
	FeatureGates: map[string]map[string]any{
		bootstrapStatsigGlassModeAvailableGate:           buildEnabledStatsigGate(bootstrapStatsigGlassModeAvailableGate),
		bootstrapStatsigGlassOpenAgentInWindowGate:       buildEnabledStatsigGate(bootstrapStatsigGlassOpenAgentInWindowGate),
		bootstrapStatsigOpenAgentsTitlebarGate:           buildEnabledStatsigGate(bootstrapStatsigOpenAgentsTitlebarGate),
		bootstrapStatsigOpenAgentWindowTopGate:           buildEnabledStatsigGate(bootstrapStatsigOpenAgentWindowTopGate),
		bootstrapStatsigOpenAgentWindowBottomGate:        buildEnabledStatsigGate(bootstrapStatsigOpenAgentWindowBottomGate),
		bootstrapStatsigNALAgentRetriesGate:              buildEnabledStatsigGate(bootstrapStatsigNALAgentRetriesGate),
		bootstrapStatsigNALFreshRetryIDsGate:             buildEnabledStatsigGate(bootstrapStatsigNALFreshRetryIDsGate),
		bootstrapStatsigUseModelParametersGate:           buildEnabledStatsigGate(bootstrapStatsigUseModelParametersGate),
		bootstrapStatsigUseReactModelPickerGate:          buildEnabledStatsigGate(bootstrapStatsigUseReactModelPickerGate),
		bootstrapStatsigIDECmdEnterSubmitGate:            buildEnabledStatsigGate(bootstrapStatsigIDECmdEnterSubmitGate),
		bootstrapStatsigContextVisualizerGate:            buildEnabledStatsigGate(bootstrapStatsigContextVisualizerGate),
		bootstrapStatsigWysiwygMarkdownGate:              buildEnabledStatsigGate(bootstrapStatsigWysiwygMarkdownGate),
		bootstrapStatsigWysiwygMarkdownDefaultGate:       buildEnabledStatsigGate(bootstrapStatsigWysiwygMarkdownDefaultGate),
		bootstrapStatsigSubagentSupportInterrupt:         buildEnabledStatsigGate(bootstrapStatsigSubagentSupportInterrupt),
		bootstrapStatsigExplicitSubagentModels:           buildEnabledStatsigGate(bootstrapStatsigExplicitSubagentModels),
		bootstrapStatsigMcpDirectClientToolFetch:         buildEnabledStatsigGate(bootstrapStatsigMcpDirectClientToolFetch),
		bootstrapStatsigGlassCustomThemeSupport:          buildEnabledStatsigGate(bootstrapStatsigGlassCustomThemeSupport),
		bootstrapStatsigGlassAutomationsUI:               buildEnabledStatsigGate(bootstrapStatsigGlassAutomationsUI),
		bootstrapStatsigTerminalUI2:                      buildEnabledStatsigGate(bootstrapStatsigTerminalUI2),
		bootstrapStatsigDisableTerminalOutputUIStreaming: buildEnabledStatsigGate(bootstrapStatsigDisableTerminalOutputUIStreaming),
		bootstrapStatsigBrowserCanvas:                    buildEnabledStatsigGate(bootstrapStatsigBrowserCanvas),
		bootstrapStatsigCanvasInlinePreview:              buildEnabledStatsigGate(bootstrapStatsigCanvasInlinePreview),
		bootstrapStatsigGlassDesignModeComposerPill:      buildEnabledStatsigGate(bootstrapStatsigGlassDesignModeComposerPill),
		bootstrapStatsigEnableMultitaskMode:              buildEnabledStatsigGate(bootstrapStatsigEnableMultitaskMode),
		bootstrapStatsigDecomposeAlwaysLocalExtHostGate:  buildDisabledStatsigGate(bootstrapStatsigDecomposeAlwaysLocalExtHostGate),
		bootstrapStatsigCursorExtensionsIsolationV2Gate:  buildDisabledStatsigGate(bootstrapStatsigCursorExtensionsIsolationV2Gate),
		bootstrapStatsigCursorAgentWorkerExtension:       buildDisabledStatsigGate(bootstrapStatsigCursorAgentWorkerExtension),
	},
	DynamicConfigs: map[string]statsigDynamicConfigTemplate{
		bootstrapStatsigExperimentName: buildStatsigDynamicConfig(
			bootstrapStatsigExperimentName,
			map[string]any{bootstrapStatsigVariantParam: bootstrapStatsigVariantControl},
			bootstrapStatsigVariantControl,
		),
		bootstrapStatsigEffortFirstSubmenuExperiment: buildStatsigDynamicConfigActive(
			bootstrapStatsigEffortFirstSubmenuExperiment,
			map[string]any{bootstrapStatsigExperimentEnabledParam: true},
		),

		bootstrapStatsigProductTipsConfigName: buildStatsigDynamicConfig(
			bootstrapStatsigProductTipsConfigName,
			map[string]any{
				"tips": []map[string]any{},
				"config": map[string]any{
					"intervalMs":       8000,
					"minClientVersion": "",
				},
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigIdleExtensionHostKiller: buildStatsigDynamicConfig(
			bootstrapStatsigIdleExtensionHostKiller,
			map[string]any{
				bootstrapStatsigIdleMinutesToKill:          0,
				bootstrapStatsigFreeMemoryPercentageToKill: 0,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigCanvasPromptTextConfig: buildStatsigDynamicConfig(
			bootstrapStatsigCanvasPromptTextConfig,
			buildCanvasPromptTextConfigValue(),
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigEditorBugbotConfig: buildStatsigDynamicConfig(
			bootstrapStatsigEditorBugbotConfig,
			map[string]any{
				"model":              "claude-4-5-sonnet-20250929",
				"iterations":         0,
				"agentic_iterations": 1,
				"agentic_model":      "claude-4.5-haiku",
				"context_lines":      10,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigExtensionMonitorControl: buildStatsigDynamicConfig(
			bootstrapStatsigExtensionMonitorControl,
			map[string]any{
				"local_enabled":              false,
				"backend_reporting_enabled":  false,
				"subsample_polling_rate_sec": 0,
				"sample_polling_rate_min":    0,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigExtensionSignatureBypass: buildStatsigDynamicConfig(
			bootstrapStatsigExtensionSignatureBypass,
			map[string]any{
				"extensionIds": []string{
					"nromanov.dotrush",
					"ms-python.python",
					"typescriptteam.native-preview",
					"typespec.typespec-vscode",
					"ms-toolsai.jupyter",
					"k3ndr1ckfu.tcl-language-support-for-vscode",
					"amiq.dvt",
				},
				"remoteVerificationMinVersion": "2.25.0",
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigGCTraceControl: buildStatsigDynamicConfig(
			bootstrapStatsigGCTraceControl,
			map[string]any{
				"enabled":            false,
				"drain_interval_sec": 120,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigInlineDiffPerformance: buildStatsigDynamicConfig(
			bootstrapStatsigInlineDiffPerformance,
			map[string]any{
				"maxDecorations": 100,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigLeakedDisposablesTracker: buildStatsigDynamicConfig(
			bootstrapStatsigLeakedDisposablesTracker,
			map[string]any{
				"enabled":          false,
				"reportIntervalMs": 60000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigMcpIPCTimeouts: buildStatsigDynamicConfig(
			bootstrapStatsigMcpIPCTimeouts,
			map[string]any{
				"metadata_timeout_ms":           10000,
				"lifecycle_timeout_ms":          10000,
				"dashboard_timeout_ms":          10000,
				"recovery_per_retry_timeout_ms": 10000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigMcpWakeProbeConfig: buildStatsigDynamicConfig(
			bootstrapStatsigMcpWakeProbeConfig,
			map[string]any{
				"probeOnFocus":              true,
				"probeOnBrowserOnline":      true,
				"probeOnElapsedTimeGap":     true,
				"elapsedTimeGapThresholdMs": 300000,
				"focusProbeDebounceMs":      60000,
				"onlineProbeDebounceMs":     5000,
				"resumeProbeDebounceMs":     5000,
				"startupGraceMs":            15000,
				"minProbeIntervalMs":        30000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigNALStallDetectorTimeout: buildStatsigDynamicConfig(
			bootstrapStatsigNALStallDetectorTimeout,
			map[string]any{
				"advisoryTimeoutMs": 20000,
				"failTimeoutMs":     30000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigSimulatedThinkingErrorTimeout: buildStatsigDynamicConfig(
			bootstrapStatsigSimulatedThinkingErrorTimeout,
			map[string]any{
				"timeout_ms": 120000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigPlaywrightLogConfigs: buildStatsigDynamicConfig(
			bootstrapStatsigPlaywrightLogConfigs,
			map[string]any{
				"logSizeThreshold": 25000,
				"logPreviewLines":  25,
				"logPreviewChars":  25000,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigRetryInterceptorParams: buildStatsigDynamicConfig(
			bootstrapStatsigRetryInterceptorParams,
			map[string]any{},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigUpdatePromptConfig: buildStatsigDynamicConfig(
			bootstrapStatsigUpdatePromptConfig,
			map[string]any{
				"min_hours_between_prompts": 48,
				"max_prompts_per_version":   3,
				"max_prompts_per_day":       1,
				"snooze_duration_hours":     72,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigHTTP2PingConfig: buildStatsigDynamicConfig(
			bootstrapStatsigHTTP2PingConfig,
			map[string]any{
				"enabled":                 []string{},
				"pingIdleConnection":      nil,
				"pingIntervalMs":          nil,
				"pingTimeoutMs":           nil,
				"idleConnectionTimeoutMs": nil,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigHTTP1KeepaliveConfig: buildStatsigDynamicConfig(
			bootstrapStatsigHTTP1KeepaliveConfig,
			map[string]any{
				"keepAliveInitialDelayMs": nil,
			},
			bootstrapStatsigLocalDefaultRule,
		),
		bootstrapStatsigHTTP2AgentPoolConfig: buildStatsigDynamicConfig(
			bootstrapStatsigHTTP2AgentPoolConfig,
			map[string]any{
				"poolSize": 4,
			},
			bootstrapStatsigLocalDefaultRule,
		),
	},
	LayerConfigs: map[string]map[string]any{
		bootstrapStatsigModelPickerExperimentsLayer: buildStatsigLayerConfig(
			bootstrapStatsigModelPickerExperimentsLayer,
			map[string]any{
				bootstrapStatsigEffortFirstVariantParam:         bootstrapStatsigVariantControl,
				bootstrapStatsigEffortFirstCompactModelIDsParam: []string{},
			},
		),
	},
	User: map[string]any{
		"userID": localUltraPaymentID,
		"email":  legacyruntime.InjectAccountEmail,
		"customIDs": map[string]string{
			"localUserID": localUltraPaymentID,
		},
	},
	HasUpdates: true,
	HashUsed:   "none",
	SDKParams: map[string]any{
		"stableID":                  localUltraPaymentID,
		"disableDiagnosticsLogging": true,
	},
}

func buildStatsigLayerConfig(name string, value map[string]any) map[string]any {
	return map[string]any{
		"name":                            name,
		"value":                           value,
		"rule_id":                         bootstrapStatsigLocalDefaultRule,
		"ruleID":                          bootstrapStatsigLocalDefaultRule,
		"group_name":                      bootstrapStatsigLocalDefaultRule,
		"groupName":                       bootstrapStatsigLocalDefaultRule,
		"secondary_exposures":             []statsigSecondaryExposure{},
		"secondaryExposures":              []statsigSecondaryExposure{},
		"undelegated_secondary_exposures": []statsigSecondaryExposure{},
		"undelegatedSecondaryExposures":   []statsigSecondaryExposure{},
		"is_device_based":                 false,
		"isDeviceBased":                   false,
		"is_experiment_active":            true,
		"isExperimentActive":              true,
		"is_user_in_experiment":           true,
		"isUserInExperiment":              true,
	}
}

func buildStatsigDynamicConfigActive(name string, value map[string]any) statsigDynamicConfigTemplate {
	config := buildStatsigDynamicConfig(name, value, bootstrapStatsigLocalDefaultRule)
	config.IsExperimentActive = true
	config.IsExperimentActiveCamel = true
	config.IsUserInExperiment = true
	config.IsUserInExperimentCamel = true
	return config
}

func buildStatsigDynamicConfig(name string, value map[string]any, ruleID string) statsigDynamicConfigTemplate {
	name = strings.TrimSpace(name)
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		ruleID = bootstrapStatsigLocalDefaultRule
	}
	exposures := []statsigSecondaryExposure{}
	return statsigDynamicConfigTemplate{
		Name:                               name,
		Value:                              value,
		RuleID:                             ruleID,
		RuleIDCamel:                        ruleID,
		GroupName:                          ruleID,
		GroupNameCamel:                     ruleID,
		SecondaryExposures:                 exposures,
		SecondaryExposuresCamel:            exposures,
		UndelegatedSecondaryExposures:      exposures,
		UndelegatedSecondaryExposuresCamel: exposures,
		IsDeviceBased:                      false,
		IsDeviceBasedCamel:                 false,
		IsExperimentActive:                 false,
		IsExperimentActiveCamel:            false,
		IsUserInExperiment:                 false,
		IsUserInExperimentCamel:            false,
	}
}

func buildCanvasPromptTextConfigValue() map[string]any {
	return map[string]any{
		"skillDescription": "A Cursor Canvas is a live React app that the user can open beside the chat. You MUST use a canvas when the agent produces a standalone analytical artifact \u2014 quantitative analyses, billing investigations, security audits, architecture reviews, data-heavy content, timelines, charts, tables, interactive explorations, repeatable tools, or any response that benefits from visual layout. Especially prefer a canvas when presenting results from MCP tools (Datadog, Databricks, Linear, Sentry, Slack, etc.) where the data is the deliverable \u2014 render it in a rich canvas rather than dumping it into a markdown table or code block. If you catch yourself about to write a markdown table, stop and use a canvas instead. You MUST also read this skill whenever you create, edit, or debug any .canvas.tsx file.",
		"errorFixPromptTemplate": strings.Join([]string{
			"The canvas at `{canvasPath}` has the following error:",
			"",
			`"""`,
			"{errorMessage}",
			`"""`,
			"",
			"Check if the canvas SDK has changed since this canvas was created.",
			"Update the canvas to use the latest SDK components according to the supplied documentation in the canvas skill.",
		}, "\n"),
		"welcomePageEnabled":     true,
		"marketplaceCategoryKey": "canvas-featured",
		"marketplaceMaxCards":    4,
	}
}

func buildEnabledStatsigGate(name string) map[string]any {
	return buildStatsigGate(name, true, "local_enabled")
}

func buildDisabledStatsigGate(name string) map[string]any {
	return buildStatsigGate(name, false, "local_disabled")
}

func buildStatsigGate(name string, value bool, ruleID string) map[string]any {
	return map[string]any{
		"name":                            name,
		"value":                           value,
		"rule_id":                         ruleID,
		"ruleID":                          ruleID,
		"group_name":                      ruleID,
		"groupName":                       ruleID,
		"secondary_exposures":             []statsigSecondaryExposure{},
		"secondaryExposures":              []statsigSecondaryExposure{},
		"undelegated_secondary_exposures": []statsigSecondaryExposure{},
		"undelegatedSecondaryExposures":   []statsigSecondaryExposure{},
		"is_device_based":                 false,
		"isDeviceBased":                   false,
		"id_type":                         "userID",
		"idType":                          "userID",
	}
}

func buildServerTimePayload(*RequestContext) (map[string]any, error) {
	now := float64(time.Now().UnixMilli())
	return map[string]any{
		"receiveTimestamp":  now,
		"transmitTimestamp": now,
	}, nil
}

func buildServerConfigPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"configVersion":            "local_cli_sandbox_defaults_disabled_v2",
		"http2Config":              "HTTP2_CONFIG_FORCE_ALL_DISABLED",
		"cliSandboxDefaultEnabled": true,
		"indexingConfig": map[string]any{
			"defaultUserPathEncryptionKey": localPathEncryptionKey,
			"defaultTeamPathEncryptionKey": localPathEncryptionKey,
		},
	}, nil
}

func buildAvailableModelsPayload(reqCtx *RequestContext) (map[string]any, error) {
	request, err := decodeAvailableModelsRequest(availableModelsRequestBody(reqCtx))
	if err != nil {
		return nil, err
	}
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	useModelParameters := request.UseModelParameters == nil || request.GetUseModelParameters()
	explodedVariants := request.GetVariantsWillBeShownInExplodedList()
	modelRefs := collectModelAdapterRefs(adapters)
	defaultModel := ""
	if len(modelRefs) > 0 {
		defaultModel = modelRefs[0]
	}
	modelEntries := buildAvailableModelEntriesForMode(adapters, useModelParameters, explodedVariants)
	return map[string]any{
		"backgroundComposerModelConfig": map[string]any{
			"bestOfNDefaultModels": append([]string(nil), modelRefs...),
			"defaultModel":         defaultModel,
			"fallbackModels":       append([]string(nil), modelRefs...),
		},
		"cmdKModelConfig": map[string]any{
			"defaultModel":   defaultModel,
			"fallbackModels": append([]string(nil), modelRefs...),
		},
		"composerModelConfig": map[string]any{
			"bestOfNDefaultModels": append([]string(nil), modelRefs...),
			"defaultModel":         defaultModel,
			"fallbackModels":       append([]string(nil), modelRefs...),
		},
		"deepSearchModelConfig": map[string]any{
			"defaultModel": defaultModel,
		},
		"displayConfiguration": map[string]any{
			"namedModelsViewConfig": map[string]any{
				"namedViewToRoutedModelViewToggle": map[string]any{
					"markdown": "Auto",
				},
			},
			"routedModelViewConfig": map[string]any{
				"hideSearchBar": false,
				"routedModelViewToNamedViewToggle": map[string]any{
					"setToLastNamedModel": true,
					"subtitle":            "Balanced quality and speed, recommended for most tasks",
					"titleMarkdown":       "Auto",
				},
			},
			"hideAddModels": false,
			"hideSearchBar": false,
		},
		"disableUnusedModelsAfterNHours": availableModelsDisableUnusedHours,
		"models":                         modelEntries,
		"planExecutionModelConfig": map[string]any{
			"defaultModel":   defaultModel,
			"fallbackModels": append([]string(nil), modelRefs...),
		},
		"quickAgentModelConfig": map[string]any{
			"defaultModel": defaultModel,
		},
		"specModelConfig": map[string]any{
			"defaultModel": defaultModel,
		},
		"useModelParameters":                useModelParameters,
		"upgradeUnchangedModelsAfterNHours": availableModelsUpgradeHours,
	}, nil
}

func buildDefaultModelNudgeDataPayload(reqCtx *RequestContext) (map[string]any, error) {
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"modelsWithNoDefaultSwitch": collectModelAdapterRefs(adapters),
		"nudgeDate":                 "0",
	}, nil
}

func buildUsableModelsPayload(reqCtx *RequestContext) (map[string]any, error) {
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"models": buildCLIModelDetails(adapters)}, nil
}

func buildDefaultModelForCliPayload(reqCtx *RequestContext) (map[string]any, error) {
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	models := buildCLIModelDetails(adapters)
	if len(models) == 0 {
		return map[string]any{"model": map[string]any{}}, nil
	}
	return map[string]any{"model": models[0]}, nil
}

func buildDefaultModelPayload(reqCtx *RequestContext) (map[string]any, error) {
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	defaultModel := firstModelAdapterRef(adapters)
	return map[string]any{"model": defaultModel, "thinkingModel": defaultModel}, nil
}

func buildBootstrapStatsigPayload(reqCtx *RequestContext) (map[string]any, error) {
	adapters, err := loadConfiguredModelAdapters(reqCtx)
	if err != nil {
		return nil, err
	}
	generatedAtMs := uint64(time.Now().UnixMilli())
	authID := resolveBootstrapStatsigAuthID(reqCtx)
	configJSON, err := buildBootstrapStatsigConfigJSONForModelIDs(
		int64(generatedAtMs),
		authID,
		collectModelAdapterRefs(adapters),
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"config":        string(configJSON),
		"generatedAtMs": generatedAtMs,
	}, nil
}

func buildFirstWindowStatsigDecisionPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"variant": bootstrapStatsigVariantControl,
		"reason":  bootstrapStatsigLocalDefaultRule,
	}, nil
}

func buildDashboardCurrentPeriodUsagePayload(*RequestContext) (map[string]any, error) {
	billingCycleStart := time.Now().Add(-localUltraBillingCycleDuration).UnixMilli()
	billingCycleEnd := time.Now().Add(10 * 365 * 24 * time.Hour).UnixMilli()
	return map[string]any{
		"autoModelSelectedDisplayMessage":  "Ultra plan active",
		"billingCycleEnd":                  billingCycleEnd,
		"billingCycleStart":                billingCycleStart,
		"displayMessage":                   "Ultra plan active",
		"displayThreshold":                 99999999,
		"enabled":                          true,
		"namedModelSelectedDisplayMessage": "Ultra plan active",
		"planUsage": map[string]any{
			"apiPercentUsed":   0,
			"apiSpend":         0,
			"autoPercentUsed":  0,
			"autoSpend":        0,
			"bonusTooltip":     "Ultra local account mock is active.",
			"includedSpend":    localUltraPlanIncludedCents,
			"limit":            localUltraPlanIncludedCents,
			"remaining":        localUltraPlanIncludedCents,
			"remainingBonus":   false,
			"totalPercentUsed": 0,
			"totalSpend":       0,
		},
		"spendLimitUsage": map[string]any{
			"limitType": "user",
		},
	}, nil
}

func buildDashboardTeamsPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"teams": []map[string]any{},
	}, nil
}

func buildDashboardManagedSkillsPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"skills": []map[string]any{},
	}, nil
}

func buildDashboardGetMePayload(reqCtx *RequestContext) (map[string]any, error) {
	authID := ""
	if reqCtx != nil {
		authID = authIDFromBearer(reqCtx.Headers.Get("authorization"))
	}
	if authID == "" {
		authID = authIDFromJWT(legacyruntime.InjectAuthToken)
	}
	if authID == "" {
		authID = localUltraPaymentID
	}

	return map[string]any{
		"authId":            authID,
		"userId":            localUltraDashboardUserID,
		"email":             legacyruntime.InjectAccountEmail,
		"firstName":         "Cursor",
		"lastName":          "Local",
		"createdAt":         time.Now().UTC().Format(time.RFC3339),
		"isEnterpriseUser":  false,
		"teamName":          "",
		"emailDomainType":   "personal",
		"country":           "US",
		"profilePictureUrl": "",
	}, nil
}

func buildDashboardUserPrivacyModePayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"privacyMode":                          "PRIVACY_MODE_NO_STORAGE",
		"hoursRemainingInGracePeriod":          0,
		"isEnforcedByTeam":                     false,
		"isNotMigratedToServerSourceOfTruth":   false,
		"partnerDataShare":                     false,
		"hasAcknowledgedGracePeriodDisclaimer": true,
	}, nil
}

func buildDashboardPlanInfoPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"planInfo": map[string]any{
			"planName":            "Ultra Plan",
			"includedAmountCents": localUltraPlanIncludedCents,
			"price":               "$200/mo",
			"billingCycleEnd":     time.Now().Add(10 * 365 * 24 * time.Hour).UnixMilli(),
		},
	}, nil
}

func buildDashboardUsageLimitStatusAndActiveGrantsPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"usageLimitPolicyStatus": map[string]any{
			"isInSlowPool":           false,
			"features":               map[string]string{},
			"canConfigureSpendLimit": true,
			"hasPendingRequest":      false,
			"allowedModelIds":        []string{},
			"allowedModelTags":       []string{},
		},
		"activeGrants": []map[string]any{},
	}, nil
}

func buildDashboardIsOnNewPricingPayload(*RequestContext) (map[string]any, error) {
	return map[string]any{
		"isOnNewPricing":   true,
		"isOptedOut":       false,
		"hasAutoSpillover": true,
		"dashboardUserId":  localUltraDashboardUserID,
	}, nil
}

func loadConfiguredModelAdapters(reqCtx *RequestContext) ([]legacyruntime.ModelAdapterConfig, error) {
	if reqCtx == nil || reqCtx.Deps == nil || reqCtx.Deps.SystemSettingService == nil {
		return []legacyruntime.ModelAdapterConfig{}, nil
	}
	ctx := context.Background()
	if reqCtx.Request != nil {
		ctx = reqCtx.Request.Context()
	}
	return reqCtx.Deps.SystemSettingService.ResolveModelAdapters(ctx)
}

func availableModelsRequestBody(reqCtx *RequestContext) []byte {
	if reqCtx == nil {
		return nil
	}
	return reqCtx.RequestBody
}

func decodeAvailableModelsRequest(body []byte) (*aiserverv1.AvailableModelsRequest, error) {
	request := &aiserverv1.AvailableModelsRequest{}
	if len(body) == 0 {
		return request, nil
	}
	if err := proto.Unmarshal(body, request); err != nil {
		return nil, fmt.Errorf("decode AvailableModelsRequest: %w", err)
	}
	return request, nil
}

func buildAvailableModelEntries(adapters []legacyruntime.ModelAdapterConfig) []map[string]any {
	return buildAvailableModelEntriesForMode(adapters, true, true)
}

func buildAvailableModelEntriesForMode(adapters []legacyruntime.ModelAdapterConfig, useModelParameters bool, explodedVariants bool) []map[string]any {
	if len(adapters) == 0 {
		return []map[string]any{}
	}
	output := make([]map[string]any, 0, len(adapters))
	for _, adapter := range adapters {
		channelID := strings.TrimSpace(adapter.ID)
		displayName := strings.TrimSpace(adapter.DisplayName)
		modelID := strings.TrimSpace(adapter.ModelID)
		tooltipData := strings.TrimSpace(adapter.TooltipData)
		if channelID == "" || modelID == "" {
			continue
		}
		modelDisplayName := displayName
		if modelDisplayName == "" {
			modelDisplayName = modelID
		}
		defaultThinkingEffort := defaultThinkingEffortForAdapter(adapter)
		if !explodedVariants {
			defaultThinkingEffort = compactThinkingEffortDefault(defaultThinkingEffort)
		}
		// 上下文窗口与 tooltip markdown 需在 entry 构建前算出（tooltip 展示用）。
		contextTokens := resolveAvailableModelContextTokens(adapter)
		tooltipMarkdown := buildModelTooltipMarkdown(tooltipData, adapter, contextTokens)
		entry := map[string]any{
			"clientDisplayName":                  modelDisplayName,
			"defaultOn":                          true,
			"degradationStatus":                  "DEGRADATION_STATUS_UNSPECIFIED",
			"inputboxShortModelName":             modelDisplayName,
			"isRecommendedForBackgroundComposer": false,
			"name":                               channelID,
			"visibleInRoutedModelView":           true,

			"namedModelSectionIndex": 1,
			"serverModelName":        channelID,
			"supportsAgent":          true,
			"supportsImages":         true,
			"supportsMaxMode":        false,
			"supportsNonMaxMode":     true,
			"supportsPlanMode":       true,
			"supportsSandboxing":     true,
			"supportsThinking":       true,
			"tagline":                thinkingEffortDisplayName(defaultThinkingEffort),
			"tooltipData": map[string]any{
				"markdownContent": tooltipMarkdown,
			},
			"tooltipDataForMaxMode": map[string]any{
				"markdownContent": tooltipMarkdown,
			},
		}
		if useModelParameters {
			entry["parameterDefinitions"] = buildModelParameterDefinitions(adapter, contextTokens, explodedVariants)
			entry["variants"] = buildModelVariants(adapter, channelID, modelDisplayName, tooltipMarkdown, defaultThinkingEffort, contextTokens, explodedVariants)
		}

		// 还原原生模型选择器元数据：上下文窗口（含 max 模式）、自动上下文上限、
		// 展示价格、长上下文标记与「用户自建」标记。数据源优先 adapter 显式配置，
		// 其次内置 modelcontext 目录（models.json 规则），缺失时省略对应字段。
		applyAvailableModelAutoContextMetadata(entry, contextTokens)
		entry["isLongContextOnly"] = false
		entry["isUserAdded"] = true
		if price := resolveAvailableModelDisplayPrice(adapter); price > 0 {
			entry["price"] = price
		}
		output = append(output, entry)
	}
	return output
}

// resolveAvailableModelContextTokens 返回模型在 AvailableModels 中上报的上下文窗口
// token 数：优先 adapter 显式配置的 ContextWindowTokens，其次内置 modelcontext 目录
// （models.json 的 pattern 规则，first-match-wins）。未知模型返回 0，调用方省略字段。
func resolveAvailableModelContextTokens(adapter legacyruntime.ModelAdapterConfig) int {
	if adapter.ContextWindowTokens > 0 {
		return adapter.ContextWindowTokens
	}
	return modelcontext.WindowTokens(adapter.ModelID)
}

// resolveAvailableModelDisplayPrice 返回模型选择器中展示的价格（每百万 token，
// 供应商原始币种）。仅当价格确知时返回 > 0：优先 adapter 手动/catalog 价格，
// 其次 modelcontext 内置官方价格；未知返回 0（不设置 price 字段，避免误导）。
func resolveAvailableModelDisplayPrice(adapter legacyruntime.ModelAdapterConfig) float64 {
	if adapter.Pricing != nil && adapter.Pricing.Input != nil && *adapter.Pricing.Input > 0 {
		return *adapter.Pricing.Input
	}
	pricing := modelcontext.BuiltinPricingForAdapter(adapter.ModelID, adapter.SupplierID, adapter.Type, adapter.BaseURL)
	if pricing != nil && pricing.Input != nil && *pricing.Input > 0 {
		return *pricing.Input
	}
	return 0
}

// resolveModelMaxOutputTokens 返回模型允许的最大输出 token 数：优先 adapter
// 显式配置（MaxCompletionTokens / AnthropicMaxTokens），其次 modelcontext 目录。
// 未知返回 0（tooltip 中省略该行）。
func resolveModelMaxOutputTokens(adapter legacyruntime.ModelAdapterConfig) int {
	if adapter.MaxCompletionTokens > 0 {
		return adapter.MaxCompletionTokens
	}
	if adapter.AnthropicMaxTokens > 0 {
		return adapter.AnthropicMaxTokens
	}
	return modelcontext.MaxOutputTokens(adapter.ModelID)
}

// resolveModelDisplayCurrency 返回模型展示价格的币种代码（USD/CNY）；未知回退 USD。
func resolveModelDisplayCurrency(adapter legacyruntime.ModelAdapterConfig) string {
	if adapter.Pricing != nil && strings.TrimSpace(adapter.Pricing.Currency) != "" {
		return strings.ToUpper(strings.TrimSpace(adapter.Pricing.Currency))
	}
	if pricing := modelcontext.BuiltinPricingForAdapter(adapter.ModelID, adapter.SupplierID, adapter.Type, adapter.BaseURL); pricing != nil && strings.TrimSpace(pricing.Currency) != "" {
		return strings.ToUpper(strings.TrimSpace(pricing.Currency))
	}
	return "USD"
}

// buildModelTooltipMarkdown 在用户备注基础上追加模型元数据（上下文窗口/最大输出/
// 输入价格），使模型选择器展开详情可见这些信息。Cursor UI 渲染的是
// tooltipData.markdownContent，contextTokenLimit/price 等 proto 字段仅供内部逻辑
// 使用、不会直接展示。未知元数据对应行省略。
//
// 换行约定：行间使用 markdown 硬换行（行尾两个空格 + \n），保证渲染时每行独立；
// 末尾追加空行（\n\n），避免 Cursor 客户端在 tooltip 之后追加内容（如基于 price
// 字段渲染的输出价格行）与最后一行粘连。
func buildModelTooltipMarkdown(remark string, adapter legacyruntime.ModelAdapterConfig, contextTokens int) string {
	var lines []string
	if contextTokens > 0 {
		lines = append(lines, fmt.Sprintf("**上下文窗口：** %s tokens", formatTokenCount(contextTokens)))
	}
	if maxOutput := resolveModelMaxOutputTokens(adapter); maxOutput > 0 {
		lines = append(lines, fmt.Sprintf("**最大输出：** %s tokens", formatTokenCount(maxOutput)))
	}
	if price := resolveAvailableModelDisplayPrice(adapter); price > 0 {
		lines = append(lines, fmt.Sprintf("**输入价格：** %s / 1M tokens", formatModelPrice(price, resolveModelDisplayCurrency(adapter))))
	}
	if len(lines) == 0 {
		return strings.TrimSpace(remark)
	}
	details := strings.Join(lines, "  \n") + "\n\n"
	if markdown := strings.TrimSpace(remark); markdown != "" {
		return markdown + "\n\n---\n\n" + details
	}
	return details
}

// formatTokenCount 把 token 数格式化为千分位文本（1000000 → 1,000,000）。
func formatTokenCount(n int) string {
	digits := strconv.Itoa(n)
	if n < 0 {
		return digits
	}
	var out strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return out.String()
}

// formatModelPrice 按币种格式化每百万 token 价格（0.14 USD → $0.14）。
func formatModelPrice(price float64, currency string) string {
	text := strconv.FormatFloat(price, 'f', -1, 64)
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CNY":
		return "¥" + text
	case "JPY", "KRW":
		return currency + " " + text
	default:
		return "$" + text
	}
}

func applyAvailableModelAutoContextMetadata(entry map[string]any, contextTokens int) {
	if entry == nil {
		return
	}
	if contextTokens > 0 && contextTokens <= math.MaxInt32 {
		entry["contextTokenLimit"] = contextTokens
		entry["contextTokenLimitForMaxMode"] = contextTokens
		entry["autoContextMaxTokens"] = contextTokens
		entry["supportsAutoContext"] = true
		return
	}
	// 未知上下文时不声明自动上下文能力，避免字段自相矛盾。
	entry["supportsAutoContext"] = false
}

func buildCLIModelDetails(adapters []legacyruntime.ModelAdapterConfig) []map[string]any {
	models := make([]map[string]any, 0, len(adapters))
	for _, adapter := range adapters {
		// 已停用渠道不暴露给客户端（测试失败自动停用的模型不出现在 Cursor 模型列表）
		if adapter.Disabled {
			continue
		}
		channelID := strings.TrimSpace(adapter.ID)
		if channelID == "" {
			continue
		}
		modelID := strings.TrimSpace(adapter.ModelID)
		modelDisplayName := strings.TrimSpace(adapter.DisplayName)
		if modelDisplayName == "" {
			modelDisplayName = modelID
		}
		detail := map[string]any{
			"modelId":          channelID,
			"displayModelId":   modelID,
			"displayName":      modelDisplayName,
			"displayNameShort": modelDisplayName,
			"apiKeyCredentials": map[string]any{
				"apiKey":  strings.TrimSpace(adapter.APIKey),
				"baseUrl": strings.TrimSpace(adapter.BaseURL),
			},
		}

		applyAvailableModelAutoContextMetadata(detail, resolveAvailableModelContextTokens(adapter))
		models = append(models, detail)
	}
	return models
}

func buildModelParameterDefinitions(adapter legacyruntime.ModelAdapterConfig, contextTokens int, explodedVariants bool) []map[string]any {
	definitions := buildThinkingEffortParameterDefinitions(adapter.Type, explodedVariants)
	if contextTokens > 0 {
		definitions = append(definitions, map[string]any{
			"id":              modelRuntimeContextParameterID,
			"markdownTooltip": "Context size the model has available.",
			"name":            "Context",
			"parameterType": map[string]any{
				"enumParameter": map[string]any{
					"values": []map[string]any{{
						"value":       strconv.Itoa(contextTokens),
						"displayName": formatCompactTokenCount(contextTokens),
					}},
				},
			},
		})
	}
	if adapter.FastMode {
		definitions = append(definitions, map[string]any{
			"id":              modelRuntimeFastParameterID,
			"markdownTooltip": "Use the provider's priority service tier when available.",
			"name":            "Fast",
			"parameterType": map[string]any{
				"booleanParameter": map[string]any{
					"values": []map[string]any{
						{"value": "false", "displayName": "Off"},
						{"value": "true", "displayName": "On"},
					},
				},
			},
		})
	}
	return definitions
}

func buildThinkingEffortParameterDefinitions(adapterType string, explodedVariants bool) []map[string]any {
	values := thinkingEffortValuesForAdapter(adapterType)
	if !explodedVariants {
		values = compactThinkingEffortValues(values)
	}
	options := make([]map[string]any, 0, len(values))
	for _, value := range values {
		options = append(options, map[string]any{
			"displayName":        thinkingEffortDisplayName(value),
			"increasesModelCost": value == "xhigh" || value == "max",
			"value":              value,
		})
	}
	return []map[string]any{{
		"id":                  modelRuntimeThinkingEffortParameterID,
		"isCycleableByHotkey": true,
		"markdownTooltip":     "Controls how much reasoning effort the model uses.",
		"name":                "Effort",
		"parameterType": map[string]any{
			"enumParameter": map[string]any{
				"values": options,
			},
		},
	}}
}

func buildModelVariants(adapter legacyruntime.ModelAdapterConfig, channelID string, modelDisplayName string, tooltipData string, defaultThinkingEffort string, contextTokens int, explodedVariants bool) []map[string]any {
	values := orderThinkingEffortValues(thinkingEffortValuesForAdapter(adapter.Type), defaultThinkingEffort)
	if !explodedVariants {
		values = compactThinkingEffortValues(values)
	}
	if len(values) == 0 {
		values = []string{"disabled"}
	}
	channelID = strings.TrimSpace(channelID)
	modelDisplayName = strings.TrimSpace(modelDisplayName)
	variants := make([]map[string]any, 0, len(values))
	for _, value := range values {
		parameterValues := []map[string]any{{"id": modelRuntimeThinkingEffortParameterID, "value": value}}
		if contextTokens > 0 {
			parameterValues = append(parameterValues, map[string]any{"id": modelRuntimeContextParameterID, "value": strconv.Itoa(contextTokens)})
		}
		if adapter.FastMode {
			parameterValues = append(parameterValues, map[string]any{"id": modelRuntimeFastParameterID, "value": "false"})
		}
		variantDisplayName := modelDisplayName
		if explodedVariants {
			variantDisplayName = buildThinkingEffortVariantDisplayName(modelDisplayName, value)
		}
		variant := map[string]any{
			"displayName":              variantDisplayName,
			"displayNameOutsidePicker": variantDisplayName,
			"isDefaultNonMaxConfig":    value == defaultThinkingEffort,
			"isMaxMode":                false,
			"parameterValues":          parameterValues,
		}
		if explodedVariants && normalizeAvailableModelThinkingEffort(value, true, "") != "disabled" {
			variant["tagline"] = thinkingEffortDisplayName(value)
		}
		if channelID != "" {
			variant["variantStringRepresentation"] = channelID + ":" + value
		}
		if strings.TrimSpace(tooltipData) != "" {
			variant["tooltipData"] = map[string]any{"markdownContent": tooltipData}
		}
		variants = append(variants, variant)
	}
	return variants
}

func buildThinkingEffortVariants(adapterType string, channelID string, modelDisplayName string, tooltipData string, defaultThinkingEffort string) []map[string]any {
	return buildModelVariants(legacyruntime.ModelAdapterConfig{Type: adapterType}, channelID, modelDisplayName, tooltipData, defaultThinkingEffort, 0, true)
}

func formatCompactTokenCount(tokens int) string {
	if tokens >= 1000000 {
		return strconv.FormatFloat(float64(tokens)/1000000, 'f', 1, 64) + "M"
	}
	if tokens >= 1000 {
		return strconv.Itoa(int(math.Round(float64(tokens)/1000))) + "K"
	}
	return strconv.Itoa(tokens)
}

func buildThinkingEffortVariantDisplayName(modelDisplayName string, effortValue string) string {
	modelDisplayName = html.EscapeString(strings.TrimSpace(modelDisplayName))
	if normalizeAvailableModelThinkingEffort(effortValue, true, "") == "disabled" {
		return modelDisplayName
	}
	effortDisplayName := thinkingEffortDisplayName(effortValue)
	effortDisplayName = html.EscapeString(strings.TrimSpace(effortDisplayName))
	if modelDisplayName == "" {
		return `<span class="ui-model-picker__item-tagline" style="color: var(--cursor-text-secondary); white-space: nowrap;">:icon-brain: ` + effortDisplayName + `</span>`
	}
	return modelDisplayName + ` <span class="ui-model-picker__item-tagline" style="color: var(--cursor-text-secondary); white-space: nowrap;">:icon-brain: ` + effortDisplayName + `</span>`
}

func thinkingEffortValuesForAdapter(adapterType string) []string {
	values := []string{"disabled", "low", "medium", "high", "xhigh"}
	if adapterType := strings.ToLower(strings.TrimSpace(adapterType)); adapterType == "openai" || adapterType == "anthropic" {
		values = append(values, "max")
	}
	return values
}

func compactThinkingEffortValues(values []string) []string {
	compactValues := []string{"low", "medium", "high", "xhigh"}
	available := make(map[string]bool, len(values))
	for _, value := range values {
		available[value] = true
	}

	output := make([]string, 0, len(compactValues))
	for _, value := range compactValues {
		if available[value] {
			output = append(output, value)
		}
	}
	return output
}

func compactThinkingEffortDefault(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(value))
	case "max":
		return "xhigh"
	default:
		return "medium"
	}
}

func orderThinkingEffortValues(values []string, defaultValue string) []string {
	defaultValue = strings.ToLower(strings.TrimSpace(defaultValue))
	output := make([]string, 0, len(values))
	for _, value := range values {
		if strings.EqualFold(value, defaultValue) {
			output = append(output, value)
			break
		}
	}
	for _, value := range values {
		if !strings.EqualFold(value, defaultValue) {
			output = append(output, value)
		}
	}
	return output
}

func defaultThinkingEffortForAdapter(adapter legacyruntime.ModelAdapterConfig) string {
	if strings.EqualFold(strings.TrimSpace(adapter.Type), "anthropic") {
		return normalizeAvailableModelThinkingEffort(adapter.AnthropicThinkingEffort, true, "xhigh")
	}
	return normalizeAvailableModelThinkingEffort(adapter.ReasoningEffort, true, "medium")
}

func normalizeAvailableModelThinkingEffort(raw string, allowMax bool, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "disabled", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(raw))
	case "disable", "off", "none", "false", "no", "0":
		return "disabled"
	case "max":
		if allowMax {
			return "max"
		}
		return fallback
	default:
		return fallback
	}
}

func thinkingEffortDisplayName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "disabled":
		return "Disabled"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "Extra High"
	case "max":
		return "Max"
	default:
		return strings.TrimSpace(value)
	}
}

func collectModelAdapterRefs(adapters []legacyruntime.ModelAdapterConfig) []string {
	output := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		channelID := strings.TrimSpace(adapter.ID)
		if channelID == "" {
			continue
		}
		output = append(output, channelID)
	}
	return output
}

// firstModelAdapterRef возвращает канал первого адаптера или пустую строку,
// если ни один адаптер не сконфигурирован.
func collectNonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func cloneStatsigLayerConfigs(source map[string]map[string]any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(source))
	for name, layer := range source {
		clone := make(map[string]any, len(layer))
		for key, value := range layer {
			clone[key] = value
		}
		result[name] = clone
	}
	return result
}

func firstModelAdapterRef(adapters []legacyruntime.ModelAdapterConfig) string {
	refs := collectModelAdapterRefs(adapters)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

func resolveBootstrapStatsigAuthID(reqCtx *RequestContext) string {
	if reqCtx != nil {
		if authID := authIDFromBearer(reqCtx.Headers.Get("authorization")); authID != "" {
			return authID
		}
	}
	if authID := authIDFromJWT(legacyruntime.InjectAuthToken); authID != "" {
		return authID
	}
	return localUltraPaymentID
}

func authIDFromBearer(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	if len(authorization) >= len("Bearer ") && strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
		authorization = strings.TrimSpace(authorization[len("Bearer "):])
	}
	return authIDFromJWT(authorization)
}

func authIDFromJWT(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Sub)
}

func buildBootstrapStatsigConfigJSON(nowMs int64, authID string) ([]byte, error) {
	return buildBootstrapStatsigConfigJSONForModelIDs(nowMs, authID, []string{})
}

func buildBootstrapStatsigConfigJSONForModelIDs(nowMs int64, authID string, compactModelIDs []string) ([]byte, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		authID = localUltraPaymentID
	}
	template := bootstrapStatsigTemplate
	template.Time = nowMs
	template.User = map[string]any{
		"userID": authID,
		"email":  legacyruntime.InjectAccountEmail,
		"customIDs": map[string]string{
			"localUserID": authID,
		},
	}
	template.LayerConfigs = cloneStatsigLayerConfigs(template.LayerConfigs)
	template.LayerConfigs[bootstrapStatsigModelPickerExperimentsLayer] = buildStatsigLayerConfig(
		bootstrapStatsigModelPickerExperimentsLayer,
		map[string]any{
			bootstrapStatsigEffortFirstVariantParam:         bootstrapStatsigVariantControl,
			bootstrapStatsigEffortFirstCompactModelIDsParam: collectNonEmptyStrings(compactModelIDs),
		},
	)

	// This template mirrors the Statsig initialize/bootstrap response shape that
	// the bundled client reads for experiments. hash_used stays "none" so the
	// experiment can be looked up by its plain name without spec hashing.
	//
	// Cursor currently branches on free_user_model_picker.variant. Known values
	// are "control", "locked_picker", and "grayed_models". Keep this template
	// centralized and update it first if the bundled Statsig bootstrap shape changes.
	return json.Marshal(template)
}
