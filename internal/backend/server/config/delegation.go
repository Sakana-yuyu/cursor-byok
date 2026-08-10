package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultDelegationMaxConcurrency                  = 4
	DelegationModeCursor                             = "cursor"
	DelegationModeLocal                              = "local"
	DelegationModeAuto                               = "auto"
	DefaultDelegationMaxCorrections                  = 2
	DefaultDelegationMaxRetries                      = 1
	DefaultDelegationMaxRounds                       = 8
	DefaultDelegationExecutorFailoverLimit           = 3
	DefaultDelegationExecutorProbeTimeoutSeconds     = 5
	DefaultDelegationExecutorExecutionTimeoutSeconds = 120
	MaximumDelegationExecutorProbeTimeoutSeconds     = 30
	MaximumDelegationExecutorExecutionTimeoutSeconds = 7200
	DelegationExecutorKindBuiltin                    = "builtin"
	DelegationExecutorKindCustom                     = "custom"
	maximumCustomExecutorOutputLimitBytes            = 4 * 1024 * 1024
	// 视觉委派识图模式。
	VisionModeAuto     = "auto"     // 描述 + OCR，按内容自适应
	VisionModeDescribe = "describe" // 仅结构化描述画面
	VisionModeOCR      = "ocr"      // 仅抄录可见文字 / 表格
)

// DelegationModelGroup 描述一组可被 Multitask 委派的已配置模型。
// ModelIDs 引用 Config.ModelAdapters 中的 ID，不重复保存连接信息或密钥。
type DelegationModelGroup struct {
	ID              string          `json:"id" yaml:"id"`
	Name            string          `json:"name" yaml:"name"`
	Enabled         bool            `json:"enabled" yaml:"enabled"`
	ModelIDs        []string        `json:"modelIDs" yaml:"modelIDs"`
	DefaultModelID  string          `json:"defaultModelID,omitempty" yaml:"defaultModelID,omitempty"`
	ExecutionMode   string          `json:"executionMode,omitempty" yaml:"executionMode,omitempty"`
	ToolPermissions map[string]bool `json:"toolPermissions,omitempty" yaml:"toolPermissions,omitempty"`
}

type DelegationSupervisionConfig struct {
	Enabled           bool   `json:"enabled" yaml:"enabled"`
	SupervisorModelID string `json:"supervisorModelID,omitempty" yaml:"supervisorModelID,omitempty"`
	ReviewerModelID   string `json:"reviewerModelID,omitempty" yaml:"reviewerModelID,omitempty"`
	WorkerGroupID     string `json:"workerGroupID,omitempty" yaml:"workerGroupID,omitempty"`
	MaxCorrections    int    `json:"maxCorrections,omitempty" yaml:"maxCorrections,omitempty"`
	MaxRetries        int    `json:"maxRetries,omitempty" yaml:"maxRetries,omitempty"`
	MaxRounds         int    `json:"maxRounds,omitempty" yaml:"maxRounds,omitempty"`
	AllowReassign     bool   `json:"allowReassign,omitempty" yaml:"allowReassign,omitempty"`
	AllowEscalate     bool   `json:"allowEscalate,omitempty" yaml:"allowEscalate,omitempty"`
	StrictUnavailable bool   `json:"strictUnavailable,omitempty" yaml:"strictUnavailable,omitempty"`
}

// VisionDelegationConfig 控制视觉委派（识图代理）。
// 当主模型不支持图片输入时，把图片转发给 VisionModelID 指定的识图模型，
// 把返回的画面描述 / OCR 文本注入回原消息，使纯文本模型也能"看图"。
type VisionDelegationConfig struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	VisionModelID string `json:"visionModelID,omitempty" yaml:"visionModelID,omitempty"`
	Mode          string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// SubagentProfileOverride 用户自定义的子代理角色片段：按 subagent_type 覆盖内置注册表
// （runtimecore 的 builtinSubagentProfiles）。PromptFragment 为空表示不注入。
type SubagentProfileOverride struct {
	SubagentType   string `json:"subagentType" yaml:"subagentType"`
	PromptFragment string `json:"promptFragment,omitempty" yaml:"promptFragment,omitempty"`
}

// DelegationExecutorConfig 保存外部 executor 的非敏感策略。凭据只按环境变量名引用。
type DelegationExecutorConfig struct {
	ID                      string            `json:"id" yaml:"id"`
	Kind                    string            `json:"kind" yaml:"kind"`
	DisplayName             string            `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	Enabled                 bool              `json:"enabled" yaml:"enabled"`
	Priority                int               `json:"priority" yaml:"priority"`
	Executable              string            `json:"executable,omitempty" yaml:"executable,omitempty"`
	ProbeTimeoutSeconds     int               `json:"probeTimeoutSeconds,omitempty" yaml:"probeTimeoutSeconds,omitempty"`
	ExecutionTimeoutSeconds int               `json:"executionTimeoutSeconds,omitempty" yaml:"executionTimeoutSeconds,omitempty"`
	EnvironmentVariables    []string          `json:"environmentVariables,omitempty" yaml:"environmentVariables,omitempty"`
	Options                 map[string]string `json:"options,omitempty" yaml:"options,omitempty"`
}

// DelegationConfig 控制 Multitask 委派总开关、并发度和模型组。
type DelegationConfig struct {
	Enabled          bool                        `json:"enabled" yaml:"enabled"`
	MaxConcurrency   int                         `json:"maxConcurrency" yaml:"maxConcurrency"`
	Groups           []DelegationModelGroup      `json:"groups,omitempty" yaml:"groups,omitempty"`
	Supervision      DelegationSupervisionConfig `json:"supervision,omitempty" yaml:"supervision,omitempty"`
	VisionDelegation VisionDelegationConfig      `json:"visionDelegation,omitempty" yaml:"visionDelegation,omitempty"`
	// SubagentProfiles 子代理角色覆盖（subagentType → 自定义角色片段），读时合并进注册表。
	SubagentProfiles      []SubagentProfileOverride  `json:"subagentProfiles,omitempty" yaml:"subagentProfiles,omitempty"`
	ExecutorFailoverLimit int                        `json:"executorFailoverLimit,omitempty" yaml:"executorFailoverLimit,omitempty"`
	Executors             []DelegationExecutorConfig `json:"executors,omitempty" yaml:"executors,omitempty"`
}

func cloneDelegationConfig(input DelegationConfig) DelegationConfig {
	output := input
	if len(input.Groups) == 0 {
		output.Groups = nil
	} else {
		output.Groups = make([]DelegationModelGroup, 0, len(input.Groups))
		for _, group := range input.Groups {
			output.Groups = append(output.Groups, cloneDelegationGroup(group))
		}
	}
	output.SubagentProfiles = normalizeSubagentProfileOverrides(input.SubagentProfiles)
	output.Executors = cloneDelegationExecutors(input.Executors)
	return output
}

func cloneDelegationExecutors(input []DelegationExecutorConfig) []DelegationExecutorConfig {
	if len(input) == 0 {
		return nil
	}
	output := make([]DelegationExecutorConfig, len(input))
	for index, executor := range input {
		output[index] = executor
		output[index].EnvironmentVariables = append([]string(nil), executor.EnvironmentVariables...)
		output[index].Options = cloneStringMap(executor.Options)
	}
	return output
}

func normalizeSubagentProfileOverrides(input []SubagentProfileOverride) []SubagentProfileOverride {
	seen := make(map[string]struct{}, len(input))
	result := make([]SubagentProfileOverride, 0, len(input))
	for _, item := range input {
		item.SubagentType = strings.TrimSpace(item.SubagentType)
		item.PromptFragment = strings.TrimSpace(item.PromptFragment)
		if item.SubagentType == "" {
			continue
		}
		if _, exists := seen[item.SubagentType]; exists {
			continue
		}
		seen[item.SubagentType] = struct{}{}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func cloneDelegationGroup(input DelegationModelGroup) DelegationModelGroup {
	output := input
	output.ModelIDs = append([]string(nil), input.ModelIDs...)
	output.ToolPermissions = cloneBoolMap(input.ToolPermissions)
	return output
}

func normalizeDelegationConfig(input DelegationConfig, adapters []ModelAdapterConfig) (DelegationConfig, error) {
	availableModels := make(map[string]struct{}, len(adapters))
	for _, adapter := range adapters {
		if adapterID := strings.TrimSpace(adapter.ID); adapterID != "" {
			availableModels[adapterID] = struct{}{}
		}
	}
	output := DelegationConfig{
		Enabled:               input.Enabled,
		MaxConcurrency:        input.MaxConcurrency,
		Groups:                make([]DelegationModelGroup, 0, len(input.Groups)),
		Supervision:           normalizeDelegationSupervision(input.Supervision, availableModels, nil),
		VisionDelegation:      normalizeDelegationVision(input.VisionDelegation, availableModels),
		SubagentProfiles:      normalizeSubagentProfileOverrides(input.SubagentProfiles),
		ExecutorFailoverLimit: input.ExecutorFailoverLimit,
	}
	if output.MaxConcurrency <= 0 {
		output.MaxConcurrency = DefaultDelegationMaxConcurrency
	}
	if output.ExecutorFailoverLimit <= 0 {
		output.ExecutorFailoverLimit = DefaultDelegationExecutorFailoverLimit
	}
	executors, err := normalizeDelegationExecutors(input.Executors)
	if err != nil {
		return DelegationConfig{}, err
	}
	output.Executors = executors
	seenGroups := make(map[string]struct{}, len(input.Groups))
	groupIDs := make(map[string]struct{}, len(input.Groups))
	for index, group := range input.Groups {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			group.ID = fmt.Sprintf("delegation-group-%d", index+1)
		}
		if _, exists := seenGroups[group.ID]; exists {
			continue
		}
		seenGroups[group.ID] = struct{}{}
		groupIDs[group.ID] = struct{}{}
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" {
			group.Name = group.ID
		}
		group.ExecutionMode = normalizeDelegationMode(group.ExecutionMode)
		group.ModelIDs = filterAvailableModelIDs(group.ModelIDs, availableModels)
		group.DefaultModelID = strings.TrimSpace(group.DefaultModelID)
		if group.DefaultModelID != "" && !containsString(group.ModelIDs, group.DefaultModelID) {
			group.DefaultModelID = ""
		}
		if group.DefaultModelID == "" && len(group.ModelIDs) > 0 {
			group.DefaultModelID = group.ModelIDs[0]
		}
		if len(group.ModelIDs) == 0 {
			group.Enabled = false
		}
		group.ToolPermissions = cloneBoolMap(group.ToolPermissions)
		output.Groups = append(output.Groups, group)
	}
	output.Supervision = normalizeDelegationSupervision(input.Supervision, availableModels, groupIDs)
	return output, nil
}

func normalizeDelegationExecutors(input []DelegationExecutorConfig) ([]DelegationExecutorConfig, error) {
	seen := make(map[string]struct{}, len(input))
	result := make([]DelegationExecutorConfig, 0, len(input))
	for _, executor := range input {
		executor.ID = strings.ToLower(strings.TrimSpace(executor.ID))
		if executor.ID == "" {
			continue
		}
		if !validDelegationExecutorID(executor.ID) {
			return nil, fmt.Errorf("executor %q id is invalid", executor.ID)
		}
		if _, exists := seen[executor.ID]; exists {
			return nil, fmt.Errorf("executor %q is duplicate", executor.ID)
		}
		seen[executor.ID] = struct{}{}
		executor.Kind = strings.ToLower(strings.TrimSpace(executor.Kind))
		if executor.Kind == "" {
			executor.Kind = DelegationExecutorKindBuiltin
		}
		if executor.Kind != DelegationExecutorKindBuiltin && executor.Kind != DelegationExecutorKindCustom {
			return nil, fmt.Errorf("executor %q kind is invalid", executor.ID)
		}
		executor.DisplayName = strings.TrimSpace(executor.DisplayName)
		executor.Executable = strings.TrimSpace(executor.Executable)
		if executor.Kind == DelegationExecutorKindCustom && executor.Executable == "" {
			return nil, fmt.Errorf("executor %q executable is required", executor.ID)
		}
		if executor.Kind == DelegationExecutorKindCustom && reservedBuiltinExecutorID(executor.ID) {
			return nil, fmt.Errorf("executor %q id is reserved for a builtin executor", executor.ID)
		}
		if executor.Priority < 0 {
			executor.Priority = 0
		}
		executor.ProbeTimeoutSeconds = normalizeDelegationExecutorTimeout(executor.ProbeTimeoutSeconds, DefaultDelegationExecutorProbeTimeoutSeconds, MaximumDelegationExecutorProbeTimeoutSeconds)
		executor.ExecutionTimeoutSeconds = normalizeDelegationExecutorTimeout(executor.ExecutionTimeoutSeconds, DefaultDelegationExecutorExecutionTimeoutSeconds, MaximumDelegationExecutorExecutionTimeoutSeconds)
		var err error
		executor.EnvironmentVariables, err = normalizeDelegationExecutorEnvironmentVariables(executor.EnvironmentVariables)
		if err != nil {
			return nil, fmt.Errorf("executor %q: %w", executor.ID, err)
		}
		executor.Options, err = normalizeDelegationExecutorOptions(executor.Options)
		if err != nil {
			return nil, fmt.Errorf("executor %q: %w", executor.ID, err)
		}
		if executor.Kind == DelegationExecutorKindCustom {
			executor.Options, err = normalizeCustomExecutorOptions(executor.Options)
			if err != nil {
				return nil, fmt.Errorf("executor %q: %w", executor.ID, err)
			}
		}
		result = append(result, executor)
	}
	return result, nil
}

func validDelegationExecutorID(value string) bool {
	for index, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.'
		if !valid || index == 0 && (char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return value != ""
}

func reservedBuiltinExecutorID(value string) bool {
	switch value {
	case "claude-code", "codex-cli", "gemini-cli", "cursor-agent":
		return true
	default:
		return false
	}
}

func normalizeDelegationExecutorTimeout(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func normalizeDelegationExecutorEnvironmentVariables(input []string) ([]string, error) {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, name := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !validDelegationExecutorEnvironmentName(name) {
			return nil, fmt.Errorf("environment variable name %q is invalid", name)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func validDelegationExecutorEnvironmentName(name string) bool {
	for index, char := range name {
		if index == 0 && !((char >= 'A' && char <= 'Z') || char == '_') {
			return false
		}
		if index > 0 && !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return name != ""
}

func normalizeDelegationExecutorOptions(input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if sensitiveDelegationExecutorOptionKey(key) {
			return nil, fmt.Errorf("sensitive option %q is not allowed; reference an environment variable name instead", key)
		}
		if key != "" {
			output[key] = value
		}
	}
	if len(output) == 0 {
		return nil, nil
	}
	return output, nil
}

var customExecutorSecretLiteralPattern = regexp.MustCompile(`(?i)(?:\b(?:sk|rk|pk|ghp|github_pat|xox[baprs]-|AIza|AKIA)[a-z0-9_-]{8,}\b|\bBearer\s+[^\s]+|(?:api[_ -]?key|access[_ -]?token|secret|password)\s*[:=]\s*[^\s,;]+)`)

func normalizeCustomExecutorOptions(input map[string]string) (map[string]string, error) {
	allowed := map[string]struct{}{
		"arguments": {}, "versionArguments": {}, "stdinMode": {}, "outputMode": {},
		"finalField": {}, "progressField": {}, "errorField": {}, "outputLimitBytes": {},
	}
	for key, value := range input {
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("custom option %q is not supported", key)
		}
		if customExecutorSecretLiteralPattern.MatchString(value) {
			return nil, fmt.Errorf("custom option %q contains a secret literal; use an environment variable name allowlist instead", key)
		}
	}

	arguments, err := normalizeCustomExecutorArguments(input["arguments"], "arguments", true)
	if err != nil {
		return nil, err
	}
	versionArguments, err := normalizeCustomExecutorArguments(input["versionArguments"], "versionArguments", false)
	if err != nil {
		return nil, err
	}
	if customExecutorArgumentsContainAnyToken(versionArguments) {
		return nil, fmt.Errorf("versionArguments cannot contain template variables")
	}
	stdinMode := firstNonEmptyConfigValue(input["stdinMode"], "none")
	if stdinMode != "none" && stdinMode != "prompt" {
		return nil, fmt.Errorf("stdinMode must be none or prompt")
	}
	outputMode := firstNonEmptyConfigValue(input["outputMode"], "text")
	if outputMode != "text" && outputMode != "jsonl" {
		return nil, fmt.Errorf("outputMode must be text or jsonl")
	}
	for _, key := range []string{"finalField", "progressField", "errorField"} {
		if value := input[key]; value != "" && !validCustomExecutorFieldPath(value) {
			return nil, fmt.Errorf("%s is invalid", key)
		}
	}
	if outputMode == "jsonl" && input["finalField"] == "" {
		return nil, fmt.Errorf("finalField is required for jsonl output")
	}
	outputLimit := 1024 * 1024
	if value := input["outputLimitBytes"]; value != "" {
		outputLimit, err = strconv.Atoi(value)
		if err != nil || outputLimit <= 0 || outputLimit > maximumCustomExecutorOutputLimitBytes {
			return nil, fmt.Errorf("outputLimitBytes must be between 1 and %d", maximumCustomExecutorOutputLimitBytes)
		}
	}
	if stdinMode != "prompt" && !customExecutorArgumentsContainToken(arguments, "{{prompt}}") {
		return nil, fmt.Errorf("arguments must contain {{prompt}} when stdinMode is none")
	}

	output := map[string]string{
		"arguments": arguments, "stdinMode": stdinMode, "outputMode": outputMode,
		"outputLimitBytes": strconv.Itoa(outputLimit),
	}
	if versionArguments != "" {
		output["versionArguments"] = versionArguments
	}
	for _, key := range []string{"finalField", "progressField", "errorField"} {
		if input[key] != "" {
			output[key] = input[key]
		}
	}
	return output, nil
}

func normalizeCustomExecutorArguments(value, option string, required bool) (string, error) {
	if value == "" {
		if required {
			return "", fmt.Errorf("%s is required and must be a JSON string array", option)
		}
		return "", nil
	}
	var arguments []string
	if err := json.Unmarshal([]byte(value), &arguments); err != nil {
		return "", fmt.Errorf("%s must be a JSON string array: %w", option, err)
	}
	for _, argument := range arguments {
		if strings.ContainsRune(argument, '\x00') {
			return "", fmt.Errorf("%s contains a NUL byte", option)
		}
		if err := validateCustomExecutorTemplateVariables(argument); err != nil {
			return "", fmt.Errorf("%s: %w", option, err)
		}
	}
	normalized, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("normalize %s: %w", option, err)
	}
	return string(normalized), nil
}

func validateCustomExecutorTemplateVariables(value string) error {
	for {
		start := strings.Index(value, "{{")
		if start < 0 {
			if strings.Contains(value, "}}") {
				return fmt.Errorf("template variable is malformed")
			}
			return nil
		}
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return fmt.Errorf("template variable is malformed")
		}
		token := value[start : start+2+end+2]
		switch token {
		case "{{prompt}}", "{{workspace}}", "{{readonly}}":
		default:
			return fmt.Errorf("unknown template variable %q", token)
		}
		value = value[start+2+end+2:]
	}
}

func customExecutorArgumentsContainToken(argumentsJSON, token string) bool {
	var arguments []string
	if json.Unmarshal([]byte(argumentsJSON), &arguments) != nil {
		return false
	}
	for _, argument := range arguments {
		if strings.Contains(argument, token) {
			return true
		}
	}
	return false
}

func customExecutorArgumentsContainAnyToken(argumentsJSON string) bool {
	for _, token := range []string{"{{prompt}}", "{{workspace}}", "{{readonly}}"} {
		if customExecutorArgumentsContainToken(argumentsJSON, token) {
			return true
		}
	}
	return false
}

func validCustomExecutorFieldPath(value string) bool {
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for _, char := range segment {
			if !delegationExecutorASCIIAlphaNumeric(char) && char != '_' && char != '-' {
				return false
			}
		}
	}
	return true
}

func sensitiveDelegationExecutorOptionKey(key string) bool {
	compact := strings.ToLower(strings.Map(func(char rune) rune {
		if delegationExecutorASCIIAlphaNumeric(char) {
			return char
		}
		return -1
	}, strings.TrimSpace(key)))
	for _, sensitive := range []string{
		"accesskey", "accesstoken", "apikey", "apitoken", "authkey", "authtoken",
		"privatekey", "refreshtoken", "secretkey", "sessionkey", "sessiontoken",
	} {
		if compact == sensitive {
			return true
		}
	}
	tokens := delegationExecutorOptionKeyTokens(strings.TrimSpace(key))
	for _, token := range []string{"AUTH", "COOKIE", "CREDENTIAL", "KEY", "PASSWORD", "PRIVATE", "SECRET", "SESSION", "TOKEN"} {
		for _, candidate := range tokens {
			if candidate == token {
				return true
			}
		}
	}
	return false
}

func delegationExecutorOptionKeyTokens(key string) []string {
	runes := []rune(key)
	tokens := make([]string, 0, 4)
	start := -1
	flush := func(end int) {
		if start >= 0 && start < end {
			tokens = append(tokens, strings.ToUpper(string(runes[start:end])))
		}
		start = -1
	}
	for index, char := range runes {
		if !delegationExecutorASCIIAlphaNumeric(char) {
			flush(index)
			continue
		}
		if start < 0 {
			start = index
			continue
		}
		previous := runes[index-1]
		nextLower := index+1 < len(runes) && delegationExecutorASCIILower(runes[index+1])
		previousLowerOrDigit := delegationExecutorASCIILower(previous) || delegationExecutorASCIIDigit(previous)
		camelBoundary := delegationExecutorASCIIUpper(char) && previousLowerOrDigit
		acronymBoundary := delegationExecutorASCIIUpper(char) && delegationExecutorASCIIUpper(previous) && nextLower
		if camelBoundary || acronymBoundary {
			flush(index)
			start = index
		}
	}
	flush(len(runes))
	return tokens
}

func delegationExecutorASCIIAlphaNumeric(char rune) bool {
	return delegationExecutorASCIILower(char) || delegationExecutorASCIIUpper(char) || delegationExecutorASCIIDigit(char)
}

func delegationExecutorASCIILower(char rune) bool {
	return char >= 'a' && char <= 'z'
}

func delegationExecutorASCIIUpper(char rune) bool {
	return char >= 'A' && char <= 'Z'
}

func delegationExecutorASCIIDigit(char rune) bool {
	return char >= '0' && char <= '9'
}

func normalizeDelegationSupervision(input DelegationSupervisionConfig, availableModels map[string]struct{}, groupIDs map[string]struct{}) DelegationSupervisionConfig {
	output := DelegationSupervisionConfig{
		Enabled:           input.Enabled,
		SupervisorModelID: strings.TrimSpace(input.SupervisorModelID),
		ReviewerModelID:   strings.TrimSpace(input.ReviewerModelID),
		WorkerGroupID:     strings.TrimSpace(input.WorkerGroupID),
		MaxCorrections:    input.MaxCorrections,
		MaxRetries:        input.MaxRetries,
		MaxRounds:         input.MaxRounds,
		AllowReassign:     input.AllowReassign,
		AllowEscalate:     input.AllowEscalate,
		StrictUnavailable: input.StrictUnavailable,
	}
	if output.MaxCorrections <= 0 {
		output.MaxCorrections = DefaultDelegationMaxCorrections
	}
	if output.MaxRetries <= 0 {
		output.MaxRetries = DefaultDelegationMaxRetries
	}
	if output.MaxRounds <= 0 {
		output.MaxRounds = DefaultDelegationMaxRounds
	}
	if len(availableModels) > 0 {
		if _, ok := availableModels[output.SupervisorModelID]; !ok {
			output.SupervisorModelID = ""
		}
		if _, ok := availableModels[output.ReviewerModelID]; !ok {
			output.ReviewerModelID = ""
		}
	}
	if output.WorkerGroupID != "" && len(groupIDs) > 0 {
		if _, ok := groupIDs[output.WorkerGroupID]; !ok {
			output.WorkerGroupID = ""
		}
	}
	return output
}

// normalizeDelegationVision 归一化视觉委派配置。VisionModelID 必须引用已配置的
// 模型适配器；引用失效时清空，此时自动触发回退为"占位文字"，see_image 工具不注册。
func normalizeDelegationVision(input VisionDelegationConfig, availableModels map[string]struct{}) VisionDelegationConfig {
	output := VisionDelegationConfig{
		Enabled:       input.Enabled,
		VisionModelID: strings.TrimSpace(input.VisionModelID),
		Mode:          normalizeVisionMode(input.Mode),
	}
	if len(availableModels) > 0 {
		if _, ok := availableModels[output.VisionModelID]; !ok {
			output.VisionModelID = ""
		}
	}
	// 未指定识图模型时强制关闭自动委派，避免空跑（see_image 工具同样不注册）。
	if output.VisionModelID == "" {
		output.Enabled = false
	}
	return output
}

func normalizeVisionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VisionModeDescribe, VisionModeOCR:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return VisionModeAuto
	}
}

func filterAvailableModelIDs(values []string, available map[string]struct{}) []string {
	normalized := normalizeStringList(values)
	result := make([]string, 0, len(normalized))
	for _, value := range normalized {
		if _, exists := available[value]; exists {
			result = append(result, value)
		}
	}
	return result
}

func normalizeDelegationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DelegationModeCursor, DelegationModeLocal:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return DelegationModeAuto
	}
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]bool, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		if key != "" {
			output[key] = value
		}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}
