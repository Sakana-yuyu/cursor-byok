package delegation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultRepeatedActionThreshold  = 3
	DefaultRepeatedFailureThreshold = 2
	toolSignatureHashBytes          = 6
	maxChangedFilesPerIssue         = 4
	maxScopeDriftFiles              = 3
	maxPathTailSegments             = 5
)

var (
	pathTokenPattern     = regexp.MustCompile(`(?i)(?:[a-z]:[\\/][^,\s;]+|/(?:[^/\s;]+/)*[^,\s;]+|(?:\.{0,2}[\\/])?[a-z0-9_.-]+(?:[\\/][a-z0-9_. -]+)+)`)
	uuidLikePattern      = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)
	longDigitPattern     = regexp.MustCompile(`\b\d{3,}\b`)
	spaceCollapsePattern = regexp.MustCompile(`\s+`)
)

type LoopDetector struct {
	RepeatedActionThreshold  int
	RepeatedFailureThreshold int
	NoProgressWindow         time.Duration
}

type DetectCheckpointIssueInput struct {
	Contract               *SupervisionTaskContract
	Current                WorkerCheckpoint
	Previous               *WorkerCheckpoint
	ToolSignature          string
	RecentToolSignatures   []string
	ChangedFiles           []string
	ErrorText              string
	PreviousErrorText      string
	OutputSummary          string
	PreviousOutputSummary  string
	ResultMetadata         map[string]string
	PreviousResultMetadata map[string]string
	ClaimedCompletion      bool
	Now                    time.Time
}

type scopeMatcher struct {
	path string
}

func NewLoopDetector() LoopDetector {
	return LoopDetector{
		RepeatedActionThreshold:  DefaultRepeatedActionThreshold,
		RepeatedFailureThreshold: DefaultRepeatedFailureThreshold,
		NoProgressWindow:         DefaultSupervisionCheckpointInterval,
	}
}

func DetectCheckpointIssue(input DetectCheckpointIssueInput) *SupervisionIssue {
	return NewLoopDetector().DetectCheckpointIssue(input)
}

func (detector LoopDetector) DetectCheckpointIssue(input DetectCheckpointIssueInput) *SupervisionIssue {
	input = normalizeDetectCheckpointIssueInput(input)
	detector = detector.normalize(input.Contract)

	if issue := detector.detectScopeDrift(input); issue != nil {
		return issue
	}
	if issue := detector.detectRepeatedAction(input); issue != nil {
		return issue
	}
	if issue := detector.detectRepeatedFailure(input); issue != nil {
		return issue
	}
	if issue := detector.detectNoProgress(input); issue != nil {
		return issue
	}
	if issue := detector.detectMissingEvidence(input); issue != nil {
		return issue
	}
	return nil
}

func NormalizeToolSignature(toolName string, argsJSON []byte) string {
	normalizedTool := normalizeToolName(toolName)
	if normalizedTool == "" {
		return ""
	}
	shape := canonicalArgumentShape(argsJSON)
	sum := sha256.Sum256([]byte(shape))
	return normalizedTool + "#" + hex.EncodeToString(sum[:toolSignatureHashBytes])
}

func normalizeToolSignatureValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	parts := strings.SplitN(value, "#", 2)
	tool := normalizeToolName(parts[0])
	if len(parts) == 1 {
		return tool
	}
	hash := strings.TrimSpace(parts[1])
	if tool == "" || hash == "" {
		return tool
	}
	return tool + "#" + hash
}

func normalizeChangedFileSummaries(values []string, workspaceHint string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		candidate := sanitizeChangedFileSummary(value, workspaceHint)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (detector LoopDetector) normalize(contract *SupervisionTaskContract) LoopDetector {
	if detector.RepeatedActionThreshold <= 1 {
		detector.RepeatedActionThreshold = DefaultRepeatedActionThreshold
	}
	if detector.RepeatedFailureThreshold <= 1 {
		detector.RepeatedFailureThreshold = DefaultRepeatedFailureThreshold
	}
	if detector.NoProgressWindow <= 0 {
		detector.NoProgressWindow = DefaultSupervisionCheckpointInterval
	}
	if contract != nil && contract.CheckpointInterval > 0 {
		detector.NoProgressWindow = contract.CheckpointInterval
	}
	return detector
}

func normalizeDetectCheckpointIssueInput(input DetectCheckpointIssueInput) DetectCheckpointIssueInput {
	input.Contract = cloneSupervisionTaskContract(input.Contract)
	workspaceHint := ""
	if input.Contract != nil {
		workspaceHint = input.Contract.WorkspaceHint
		input.Current = normalizeSupervisedWorkerCheckpoint(input.Current, workspaceHint)
		if input.Previous != nil {
			previous := normalizeSupervisedWorkerCheckpoint(*input.Previous, workspaceHint)
			input.Previous = &previous
		}
	} else {
		input.Current = normalizeWorkerCheckpoint(input.Current)
		if input.Previous != nil {
			previous := normalizeWorkerCheckpoint(*input.Previous)
			input.Previous = &previous
		}
	}
	input.ToolSignature = normalizeToolSignatureValue(input.ToolSignature)
	input.RecentToolSignatures = normalizeToolSignatureSlice(input.RecentToolSignatures)
	input.ChangedFiles = normalizeChangedFileSummaries(input.ChangedFiles, workspaceHint)
	input.ErrorText = normalizeErrorFingerprint(input.ErrorText, workspaceHint)
	input.PreviousErrorText = normalizeErrorFingerprint(firstNonEmpty(input.PreviousErrorText, previousCheckpointBlocker(input.Previous)), workspaceHint)
	input.OutputSummary = sanitizeNarrativeText(input.OutputSummary, workspaceHint)
	input.PreviousOutputSummary = sanitizeNarrativeText(input.PreviousOutputSummary, workspaceHint)
	input.ResultMetadata = cloneStringMap(input.ResultMetadata)
	input.PreviousResultMetadata = cloneStringMap(input.PreviousResultMetadata)
	input.ResultMetadata = sanitizeResultMetadata(input.ResultMetadata, workspaceHint)
	input.PreviousResultMetadata = sanitizeResultMetadata(input.PreviousResultMetadata, workspaceHint)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func previousCheckpointBlocker(previous *WorkerCheckpoint) string {
	if previous == nil {
		return ""
	}
	return previous.Blocker
}

func normalizeToolSignatureSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value = normalizeToolSignatureValue(value); value != "" {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (detector LoopDetector) detectRepeatedAction(input DetectCheckpointIssueInput) *SupervisionIssue {
	currentSignature := input.ToolSignature
	if currentSignature == "" && len(input.RecentToolSignatures) > 0 {
		currentSignature = input.RecentToolSignatures[len(input.RecentToolSignatures)-1]
	}
	if currentSignature == "" {
		return nil
	}
	signatures := append([]string(nil), input.RecentToolSignatures...)
	if len(signatures) == 0 || signatures[len(signatures)-1] != currentSignature {
		signatures = append(signatures, currentSignature)
	}
	consecutive := 0
	for index := len(signatures) - 1; index >= 0; index-- {
		if signatures[index] != currentSignature {
			break
		}
		consecutive++
	}
	if consecutive < detector.RepeatedActionThreshold {
		return nil
	}
	return buildSupervisionIssue(
		SupervisionIssueRepeatedAction,
		"Repeated tool action detected across consecutive checkpoints.",
		currentSignature,
		nil,
		input,
	)
}

func (detector LoopDetector) detectRepeatedFailure(input DetectCheckpointIssueInput) *SupervisionIssue {
	if input.ErrorText == "" || input.Previous == nil || input.PreviousErrorText == "" {
		return nil
	}
	if input.ErrorText != input.PreviousErrorText {
		return nil
	}
	if repeatedFailureCount(input) < detector.RepeatedFailureThreshold {
		return nil
	}
	if hasRecoveryStrategyChange(input) {
		return nil
	}
	code := classifyFailureIssueCode(input.ErrorText)
	summary := "Repeated tool failure detected without a changed recovery strategy."
	switch code {
	case SupervisionIssueTimeout:
		summary = "Repeated timeout detected without a changed recovery strategy."
	case SupervisionIssueModelFailure:
		summary = "Repeated model failure detected without a changed recovery strategy."
	}
	return buildSupervisionIssue(code, summary, input.ToolSignature, currentChangedFiles(input), input)
}

func (detector LoopDetector) detectNoProgress(input DetectCheckpointIssueInput) *SupervisionIssue {
	if input.Previous == nil {
		return nil
	}
	if hasMeaningfulProgress(input) {
		return nil
	}
	lastProgressAt := latestEffectiveProgressAt(input.Previous.EffectiveProgressAt, input.Current.EffectiveProgressAt)
	if lastProgressAt.IsZero() || input.Now.Sub(lastProgressAt) < detector.NoProgressWindow {
		return nil
	}
	return buildSupervisionIssue(
		SupervisionIssueNoProgress,
		"No meaningful progress was observed within the checkpoint window.",
		input.ToolSignature,
		currentChangedFiles(input),
		input,
	)
}

func latestEffectiveProgressAt(previous time.Time, current time.Time) time.Time {
	if previous.IsZero() {
		return current
	}
	if current.After(previous) {
		return current
	}
	return previous
}

func (detector LoopDetector) detectScopeDrift(input DetectCheckpointIssueInput) *SupervisionIssue {
	if input.Contract == nil {
		return nil
	}
	scope := buildScopeMatchers(input.Contract.Scope, input.Contract.WorkspaceHint)
	if len(scope) == 0 {
		return nil
	}
	outOfScope := make([]string, 0, maxScopeDriftFiles)
	for _, file := range currentChangedFiles(input) {
		if file == "" || pathWithinScope(file, scope) {
			continue
		}
		outOfScope = append(outOfScope, file)
		if len(outOfScope) >= maxScopeDriftFiles {
			break
		}
	}
	if len(outOfScope) == 0 {
		return nil
	}
	return buildSupervisionIssue(
		SupervisionIssueScopeDrift,
		"Changed files fall outside the delegated contract scope.",
		input.ToolSignature,
		outOfScope,
		input,
	)
}

func (detector LoopDetector) detectMissingEvidence(input DetectCheckpointIssueInput) *SupervisionIssue {
	if !input.ClaimedCompletion {
		return nil
	}
	if hasOutputEvidence(input) || hasDoneCriteriaEvidence(input.Contract, input) {
		return nil
	}
	return buildSupervisionIssue(
		SupervisionIssueMissingEvidence,
		"Worker claimed completion without output or done-criteria evidence.",
		input.ToolSignature,
		currentChangedFiles(input),
		input,
	)
}

func buildSupervisionIssue(code SupervisionIssueCode, summary string, toolSignature string, changedFiles []string, input DetectCheckpointIssueInput) *SupervisionIssue {
	issue := normalizeSupervisionIssue(SupervisionIssue{
		Code:          code,
		Summary:       summary,
		ToolSignature: toolSignature,
		ChangedFiles:  limitStrings(changedFiles, maxChangedFilesPerIssue),
		Round:         input.Current.Round,
		Step:          input.Current.Step,
		DetectedAt:    input.Now,
	})
	return &issue
}

func normalizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return strings.Join(strings.Fields(name), "_")
}

func canonicalArgumentShape(argsJSON []byte) string {
	if len(argsJSON) == 0 {
		return "null"
	}
	var decoded any
	if err := json.Unmarshal(argsJSON, &decoded); err != nil {
		return "invalid"
	}
	return shapeString(decoded)
}

func shapeString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		if len(typed) == 0 {
			return "array[]"
		}
		parts := make([]string, 0, len(typed))
		seen := make(map[string]struct{}, len(typed))
		for _, item := range typed {
			part := shapeString(item)
			if _, exists := seen[part]; exists {
				continue
			}
			seen[part] = struct{}{}
			parts = append(parts, part)
		}
		sort.Strings(parts)
		return "array[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		if len(typed) == 0 {
			return "object{}"
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if key = strings.TrimSpace(key); key != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+":"+shapeString(typed[key]))
		}
		return "object{" + strings.Join(parts, ",") + "}"
	default:
		return "unknown"
	}
}

func hasRecoveryStrategyChange(input DetectCheckpointIssueInput) bool {
	if input.Previous == nil {
		return true
	}
	if !equalStringSlices(currentChangedFiles(input), input.Previous.ChangedFileSummaries) {
		return true
	}
	if currentStrategySignature(input) != previousStrategySignature(input) {
		return true
	}
	if outputEvidenceFingerprint(input.OutputSummary, input.ResultMetadata) != outputEvidenceFingerprint(input.PreviousOutputSummary, input.PreviousResultMetadata) {
		return true
	}
	if meaningfulStateAdvance(input) {
		return true
	}
	return false
}

func repeatedFailureCount(input DetectCheckpointIssueInput) int {
	currentSignature := currentStrategySignature(input)
	if currentSignature == "" {
		return 0
	}
	signatures := append([]string(nil), input.RecentToolSignatures...)
	if len(signatures) == 0 || signatures[len(signatures)-1] != currentSignature {
		signatures = append(signatures, currentSignature)
	}
	count := 0
	for index := len(signatures) - 1; index >= 0; index-- {
		if signatures[index] != currentSignature {
			break
		}
		count++
	}
	return count
}

func currentStrategySignature(input DetectCheckpointIssueInput) string {
	if input.ToolSignature != "" {
		return input.ToolSignature
	}
	if len(input.RecentToolSignatures) >= 2 {
		return input.RecentToolSignatures[len(input.RecentToolSignatures)-1]
	}
	if len(input.RecentToolSignatures) == 1 {
		return input.RecentToolSignatures[0]
	}
	return ""
}

func previousStrategySignature(input DetectCheckpointIssueInput) string {
	if len(input.RecentToolSignatures) >= 2 {
		return input.RecentToolSignatures[len(input.RecentToolSignatures)-2]
	}
	return currentStrategySignature(input)
}

func hasMeaningfulProgress(input DetectCheckpointIssueInput) bool {
	if input.Previous == nil {
		return false
	}
	if !equalStringSlices(currentChangedFiles(input), input.Previous.ChangedFileSummaries) {
		return true
	}
	if currentStrategySignature(input) != previousStrategySignature(input) {
		return true
	}
	if outputEvidenceFingerprint(input.OutputSummary, input.ResultMetadata) != outputEvidenceFingerprint(input.PreviousOutputSummary, input.PreviousResultMetadata) {
		return true
	}
	return meaningfulStateAdvance(input)
}

func currentChangedFiles(input DetectCheckpointIssueInput) []string {
	if len(input.ChangedFiles) > 0 {
		return append([]string(nil), input.ChangedFiles...)
	}
	return append([]string(nil), input.Current.ChangedFileSummaries...)
}

func hasOutputEvidence(input DetectCheckpointIssueInput) bool {
	return outputEvidenceFingerprint(input.OutputSummary, input.ResultMetadata) != ""
}

func hasDoneCriteriaEvidence(contract *SupervisionTaskContract, input DetectCheckpointIssueInput) bool {
	if contract == nil {
		return false
	}
	corpus := strings.ToLower(strings.Join([]string{
		input.Current.ProgressSummary,
		input.OutputSummary,
	}, "\n"))
	corpus = strings.TrimSpace(corpus)
	if corpus == "" {
		return false
	}
	for _, criterion := range contract.DoneCriteria {
		if criterion = strings.ToLower(strings.TrimSpace(criterion)); criterion != "" && strings.Contains(corpus, criterion) {
			return true
		}
	}
	expectedOutput := strings.ToLower(strings.TrimSpace(contract.ExpectedOutput))
	return expectedOutput != "" && strings.Contains(corpus, expectedOutput)
}

func buildScopeMatchers(scope string, workspaceHint string) []scopeMatcher {
	matches := pathTokenPattern.FindAllString(scope, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	items := make([]scopeMatcher, 0, len(matches))
	for _, match := range matches {
		normalized := normalizePathForComparison(match, workspaceHint)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		items = append(items, scopeMatcher{path: normalized})
	}
	return items
}

func pathWithinScope(path string, scope []scopeMatcher) bool {
	normalizedPath := normalizePathForComparison(path, "")
	if normalizedPath == "" {
		return true
	}
	for _, item := range scope {
		if normalizedPath == item.path || strings.HasPrefix(normalizedPath, item.path+"/") {
			return true
		}
	}
	return false
}

func meaningfulStateAdvance(input DetectCheckpointIssueInput) bool {
	if input.Previous == nil {
		return false
	}
	return progressEvidenceFingerprint(input.Current) != progressEvidenceFingerprint(*input.Previous)
}

func sanitizeChangedFileSummary(summary string, workspaceHint string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	if match := pathTokenPattern.FindString(summary); match != "" {
		return normalizePathForComparison(match, workspaceHint)
	}
	return sanitizeNarrativeText(summary, workspaceHint)
}

func normalizePathForComparison(raw string, workspaceHint string) string {
	raw = strings.TrimSpace(strings.Trim(raw, `"'[]()<>`))
	raw = strings.TrimRight(raw, ".,:;")
	if raw == "" {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(raw))
	workspaceRoot := filepath.Clean(strings.TrimSpace(workspaceHint))
	if workspaceRoot != "." && workspaceRoot != "" && filepath.IsAbs(cleaned) {
		if rel, err := filepath.Rel(workspaceRoot, cleaned); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			cleaned = rel
		}
	}
	if filepath.IsAbs(cleaned) {
		cleaned = summarizeAbsolutePath(cleaned)
	}
	cleaned = filepath.ToSlash(cleaned)
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	return strings.TrimSpace(cleaned)
}

func summarizeAbsolutePath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" {
		return ""
	}
	volume := filepath.VolumeName(cleaned)
	if volume != "" {
		cleaned = strings.TrimPrefix(cleaned, volume)
	}
	cleaned = strings.TrimPrefix(cleaned, string(filepath.Separator))
	if cleaned == "" {
		return ""
	}
	parts := strings.Split(cleaned, string(filepath.Separator))
	if len(parts) > maxPathTailSegments {
		parts = parts[len(parts)-maxPathTailSegments:]
	}
	return filepath.ToSlash(filepath.Join(parts...))
}

func sanitizeNarrativeText(value string, workspaceHint string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	workspaceHint = strings.TrimSpace(workspaceHint)
	for _, match := range pathTokenPattern.FindAllString(value, -1) {
		safe := normalizePathForComparison(match, workspaceHint)
		if safe == "" {
			safe = "<path>"
		}
		value = strings.ReplaceAll(value, match, safe)
	}
	value = uuidLikePattern.ReplaceAllString(value, "<id>")
	value = longDigitPattern.ReplaceAllString(value, "<n>")
	value = spaceCollapsePattern.ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func sanitizeResultMetadata(metadata map[string]string, workspaceHint string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	sanitized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if safe := sanitizeNarrativeText(value, workspaceHint); safe != "" {
			sanitized[key] = safe
		}
	}
	if len(sanitized) == 0 {
		return nil
	}
	return sanitized
}

func outputEvidenceFingerprint(summary string, metadata map[string]string) string {
	parts := make([]string, 0, 5)
	if summary = strings.TrimSpace(summary); summary != "" {
		parts = append(parts, summary)
	}
	for _, key := range []string{"output", "final_message", "final_output", "result"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

func progressEvidenceFingerprint(checkpoint WorkerCheckpoint) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(checkpoint.ProgressSummary); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(checkpoint.Blocker); value != "" {
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

func normalizeErrorFingerprint(value string, workspaceHint string) string {
	value = strings.ToLower(sanitizeNarrativeText(value, workspaceHint))
	return strings.TrimSpace(value)
}

func classifyFailureIssueCode(value string) SupervisionIssueCode {
	switch {
	case strings.Contains(value, "deadline exceeded"),
		strings.Contains(value, "timed out"),
		strings.Contains(value, "timeout"):
		return SupervisionIssueTimeout
	case strings.Contains(value, "provider error"),
		strings.Contains(value, "model"),
		strings.Contains(value, "rate limit"),
		strings.Contains(value, "context length"),
		strings.Contains(value, "overloaded"),
		strings.Contains(value, "quota"):
		return SupervisionIssueModelFailure
	default:
		return SupervisionIssueToolFailure
	}
}

func limitStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit <= 0 || len(values) <= limit {
		return cloneStringSlice(values)
	}
	return cloneStringSlice(values[:limit])
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}
