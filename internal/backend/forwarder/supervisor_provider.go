package forwarder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"cursor/gen/agentv1"
	modeladapter "cursor/internal/backend/agent/model"
	"cursor/internal/backend/delegation"
)

const (
	supervisorReviewTimeout         = 45 * time.Second
	supervisorMaxDecisionBytes      = 16 * 1024
	supervisorMaxCorrectionBytes    = 4 * 1024
	supervisorReviewMaxOutputTokens = 2048
	supervisorMaxErrorBytes         = 512
)

var errSupervisorUnavailable = errors.New("supervisor provider is unavailable")

type supervisorReviewErrorKind string

const (
	supervisorReviewErrorUnavailable supervisorReviewErrorKind = "unavailable"
	supervisorReviewErrorRejected    supervisorReviewErrorKind = "rejected"
	supervisorReviewErrorFailed      supervisorReviewErrorKind = "failed"
)

type supervisorReviewError struct {
	kind    supervisorReviewErrorKind
	message string
}

func (err *supervisorReviewError) Error() string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.message)
}

type supervisorProviderAdapter struct {
	provider  ProviderGateway
	recorder  modeladapter.LLMArtifactObserver
	sequence  atomic.Uint64
	modelName func(string) string
}

type supervisorReviewInput struct {
	AggregateID    string
	TaskID         string
	ParentExecID   string
	ProviderPass   int
	Contract       delegation.SupervisionTaskContract
	Checkpoint     delegation.WorkerCheckpoint
	Result         delegation.TaskResult
	Snapshot       delegation.TaskSnapshot
	Issue          *delegation.SupervisionIssue
	AllowedActions []delegation.SupervisionDecisionKind
	ModelID        string
	ModelName      string
	ThinkingEffort string
	MaxMode        bool
}

func newSupervisorProviderAdapter(service *Service) *supervisorProviderAdapter {
	if service == nil || service.provider == nil {
		return nil
	}
	return &supervisorProviderAdapter{
		provider: service.provider,
		recorder: service.recorder,
		modelName: func(modelID string) string {
			return strings.TrimSpace(modelID)
		},
	}
}

func (adapter *supervisorProviderAdapter) Review(ctx context.Context, input supervisorReviewInput) (delegation.SupervisionDecision, error) {
	if adapter == nil || adapter.provider == nil {
		return delegation.SupervisionDecision{}, &supervisorReviewError{kind: supervisorReviewErrorUnavailable, message: boundedSupervisorError(errSupervisorUnavailable)}
	}
	modelID := strings.TrimSpace(input.ModelID)
	if modelID == "" {
		return delegation.SupervisionDecision{}, &supervisorReviewError{kind: supervisorReviewErrorUnavailable, message: boundedSupervisorError(fmt.Errorf("supervisor model is required"))}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reviewCtx, cancel := context.WithTimeout(ctx, supervisorReviewTimeout)
	defer cancel()

	input.Contract = delegationContractClone(input.Contract)
	input.Checkpoint = delegationCheckpointClone(input.Checkpoint)
	input = sanitizeSupervisorReviewInput(input)
	sequence := adapter.sequence.Add(1)
	requestID := fmt.Sprintf("%s-supervisor-request-%d", strings.TrimSpace(input.AggregateID), sequence)
	conversationID := fmt.Sprintf("%s-supervisor-conversation-%d", strings.TrimSpace(input.AggregateID), sequence)
	modelCallID := fmt.Sprintf("%s-supervisor-model-%d", strings.TrimSpace(input.TaskID), sequence)
	messages := []modeladapter.Message{
		{Role: "system", Content: buildSupervisorSystemPrompt(input.AllowedActions)},
		{Role: "user", Content: buildSupervisorReviewPrompt(input)},
	}
	var textBuilder strings.Builder
	err := adapter.provider.StartStream(reviewCtx, ProviderRequest{
		RequestID:          requestID,
		ConversationID:     conversationID,
		RunID:              requestID,
		ModelCallID:        modelCallID,
		ModelID:            modelID,
		Mode:               agentv1.AgentMode_AGENT_MODE_AGENT,
		ThinkingEffort:     strings.TrimSpace(input.ThinkingEffort),
		MaxMode:            input.MaxMode,
		Messages:           cloneDelegatedMessages(messages),
		StableMessageCount: len(messages),
		MaxTokens:          supervisorReviewMaxOutputTokens,
		RequestKnobs: map[string]any{
			"delegation_supervisor_review": true,
			"delegation_aggregate_id":      strings.TrimSpace(input.AggregateID),
			"delegation_task_id":           strings.TrimSpace(input.TaskID),
			"delegation_provider_pass":     input.ProviderPass,
		},
		CompileSummary: "delegation_supervisor_review",
		Observer:       adapter.recorder,
		ArtifactPaths:  &modeladapter.LLMArtifactPaths{},
	}, func(event modeladapter.ModelEvent) error {
		switch event.Kind {
		case modeladapter.ModelEventKindTextDelta:
			textBuilder.WriteString(event.Text)
		case modeladapter.ModelEventKindProviderError:
			if event.Err != nil {
				return event.Err
			}
			return fmt.Errorf("supervisor provider returned an error")
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return delegation.SupervisionDecision{}, err
		}
		return delegation.SupervisionDecision{}, &supervisorReviewError{kind: supervisorReviewErrorFailed, message: boundedSupervisorError(err)}
	}
	decision, decodeErr := decodeSupervisorDecision(textBuilder.String(), input.AllowedActions)
	if decodeErr != nil {
		return delegation.SupervisionDecision{}, &supervisorReviewError{kind: supervisorReviewErrorRejected, message: boundedSupervisorError(decodeErr)}
	}
	decision.Round = input.Contract.Round
	decision.Step = input.Checkpoint.Step
	if decision.At.IsZero() {
		decision.At = time.Now().UTC()
	}
	return decision, nil
}

func buildSupervisorSystemPrompt(allowed []delegation.SupervisionDecisionKind) string {
	parts := make([]string, 0, len(allowed))
	for _, item := range allowed {
		if value := strings.TrimSpace(string(item)); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		parts = []string{string(delegation.SupervisionDecisionAccept)}
	}
	return "You supervise delegated workers. Return exactly one JSON object with keys kind, reason, and summary. " +
		"kind must be one of: " + strings.Join(parts, ", ") + ". " +
		"reason must be concise and non-empty. summary must stay concise; when kind is correct, reassign, or escalate it should contain only the correction instructions."
}

func sanitizeSupervisorReviewInput(input supervisorReviewInput) supervisorReviewInput {
	workspaceHint := strings.TrimSpace(input.Contract.WorkspaceHint)
	sanitize := func(value string) string {
		return delegation.SanitizeSupervisorText(value, workspaceHint)
	}
	input.Contract.Goal = sanitize(input.Contract.Goal)
	input.Contract.Scope = sanitize(input.Contract.Scope)
	input.Contract.Role = sanitize(input.Contract.Role)
	input.Contract.ExpectedOutput = sanitize(input.Contract.ExpectedOutput)
	input.Contract.WorkspaceHint = sanitize(input.Contract.WorkspaceHint)
	for index, criterion := range input.Contract.DoneCriteria {
		input.Contract.DoneCriteria[index] = sanitize(criterion)
	}
	input.Checkpoint.ProgressSummary = sanitize(input.Checkpoint.ProgressSummary)
	input.Checkpoint.Blocker = sanitize(input.Checkpoint.Blocker)
	for index, file := range input.Checkpoint.ChangedFileSummaries {
		input.Checkpoint.ChangedFileSummaries[index] = sanitize(file)
	}
	input.Result = delegation.SanitizeTaskResult(input.Result, workspaceHint)
	input.Snapshot.Description = sanitize(input.Snapshot.Description)
	input.Snapshot.Output = sanitize(input.Snapshot.Output)
	input.Snapshot.Error = sanitize(input.Snapshot.Error)
	input.Snapshot.ProgressSummary = sanitize(input.Snapshot.ProgressSummary)
	if input.Snapshot.Checkpoint != nil {
		checkpoint := delegationCheckpointClone(*input.Snapshot.Checkpoint)
		checkpoint.ProgressSummary = sanitize(checkpoint.ProgressSummary)
		checkpoint.Blocker = sanitize(checkpoint.Blocker)
		for index, file := range checkpoint.ChangedFileSummaries {
			checkpoint.ChangedFileSummaries[index] = sanitize(file)
		}
		input.Snapshot.Checkpoint = &checkpoint
	}
	if input.Issue != nil {
		issue := delegationSupervisionIssueClone(input.Issue)
		issue.Summary = sanitize(issue.Summary)
		for index, file := range issue.ChangedFiles {
			issue.ChangedFiles[index] = sanitize(file)
		}
		input.Issue = issue
	}
	return input
}

func buildSupervisorReviewPrompt(input supervisorReviewInput) string {
	issueSummary := "none"
	if input.Issue != nil {
		issueSummary = strings.TrimSpace(input.Issue.Summary)
		if code := strings.TrimSpace(string(input.Issue.Code)); code != "" {
			issueSummary = code + ": " + issueSummary
		}
	}
	allowed := make([]string, 0, len(input.AllowedActions))
	for _, item := range input.AllowedActions {
		if value := strings.TrimSpace(string(item)); value != "" {
			allowed = append(allowed, value)
		}
	}
	if len(allowed) == 0 {
		allowed = append(allowed, string(delegation.SupervisionDecisionAccept))
	}
	outputSummary := truncateProjectedReplayText("Supervisor", strings.TrimSpace(input.Result.Output), 3072)
	errorSummary := truncateProjectedReplayText("Supervisor error", strings.TrimSpace(input.Snapshot.Error), 2048)
	return fmt.Sprintf(
		"Aggregate ID: %s\nTask ID: %s\nParent exec ID: %s\nProvider pass: %d\nWorker model: %s\nWorker group: %s\nWorker status: %s\nRound: %d\nGoal: %s\nScope: %s\nRole: %s\nExpected output: %s\nDone criteria: %s\nCheckpoint phase: %s\nCheckpoint progress: %s\nCheckpoint blocker: %s\nRecent tools: %s\nChanged files: %s\nDetected issue: %s\nWorker output summary:\n%s\nWorker error summary:\n%s\nAllowed decisions: %s.",
		strings.TrimSpace(input.AggregateID),
		strings.TrimSpace(input.TaskID),
		strings.TrimSpace(input.ParentExecID),
		input.ProviderPass,
		firstNonEmpty(strings.TrimSpace(input.Snapshot.ModelName), strings.TrimSpace(input.Snapshot.ModelID)),
		strings.TrimSpace(input.Snapshot.ModelGroupID),
		strings.TrimSpace(string(input.Snapshot.Status)),
		input.Contract.Round,
		strings.TrimSpace(input.Contract.Goal),
		strings.TrimSpace(input.Contract.Scope),
		strings.TrimSpace(input.Contract.Role),
		strings.TrimSpace(input.Contract.ExpectedOutput),
		strings.Join(input.Contract.DoneCriteria, "; "),
		strings.TrimSpace(string(input.Checkpoint.Phase)),
		strings.TrimSpace(input.Checkpoint.ProgressSummary),
		strings.TrimSpace(input.Checkpoint.Blocker),
		strings.Join(input.Checkpoint.RecentToolNames, ", "),
		strings.Join(input.Checkpoint.ChangedFileSummaries, ", "),
		issueSummary,
		outputSummary,
		errorSummary,
		strings.Join(allowed, ", "),
	)
}

func decodeSupervisorDecision(raw string, allowed []delegation.SupervisionDecisionKind) (delegation.SupervisionDecision, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor returned empty output")
	}
	if len(raw) > supervisorMaxDecisionBytes {
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor decision exceeded %d bytes", supervisorMaxDecisionBytes)
	}
	type payload struct {
		Kind    string `json:"kind"`
		Reason  string `json:"reason"`
		Summary string `json:"summary"`
	}
	var decoded payload
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return delegation.SupervisionDecision{}, fmt.Errorf("decode supervisor decision: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return delegation.SupervisionDecision{}, fmt.Errorf("supervisor returned trailing JSON content")
		}
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor returned trailing JSON content: %w", err)
	}
	decision := delegation.SupervisionDecision{
		Kind:    delegation.SupervisionDecisionKind(strings.TrimSpace(decoded.Kind)),
		Reason:  strings.TrimSpace(decoded.Reason),
		Summary: strings.TrimSpace(decoded.Summary),
		At:      time.Now().UTC(),
	}
	decision = normalizeDelegationDecision(decision)
	if decision.Kind == "" {
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor decision kind is invalid")
	}
	if !supervisionActionAllowed(decision.Kind, allowed) {
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor decision kind %q is not allowed", decision.Kind)
	}
	if decision.Reason == "" {
		return delegation.SupervisionDecision{}, fmt.Errorf("supervisor decision reason is required")
	}
	if decision.Summary == "" && (decision.Kind == delegation.SupervisionDecisionReassign || decision.Kind == delegation.SupervisionDecisionEscalate) {
		decision.Summary = decision.Reason
	}
	if decision.Kind == delegation.SupervisionDecisionCorrect {
		if decision.Summary == "" {
			return delegation.SupervisionDecision{}, fmt.Errorf("supervisor correction summary is required")
		}
		if len(decision.Summary) > supervisorMaxCorrectionBytes {
			return delegation.SupervisionDecision{}, fmt.Errorf("supervisor correction exceeded %d bytes", supervisorMaxCorrectionBytes)
		}
	}
	return decision, nil
}

func normalizeDelegationDecision(decision delegation.SupervisionDecision) delegation.SupervisionDecision {
	decision.Kind = delegation.SupervisionDecisionKind(strings.ToLower(strings.TrimSpace(string(decision.Kind))))
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.Summary = strings.TrimSpace(decision.Summary)
	if !decision.At.IsZero() {
		decision.At = decision.At.UTC()
	}
	return decision
}

func delegationContractClone(contract delegation.SupervisionTaskContract) delegation.SupervisionTaskContract {
	cloned := contract
	cloned.AllowedTools = append([]string(nil), contract.AllowedTools...)
	cloned.DoneCriteria = append([]string(nil), contract.DoneCriteria...)
	cloned.SelectedSkillIDs = append([]string(nil), contract.SelectedSkillIDs...)
	cloned.SelectedMCPIDs = append([]string(nil), contract.SelectedMCPIDs...)
	return cloned
}

func delegationCheckpointClone(checkpoint delegation.WorkerCheckpoint) delegation.WorkerCheckpoint {
	cloned := checkpoint
	cloned.RecentToolNames = append([]string(nil), checkpoint.RecentToolNames...)
	cloned.ChangedFileSummaries = append([]string(nil), checkpoint.ChangedFileSummaries...)
	return cloned
}

func delegationSupervisionIssueClone(issue *delegation.SupervisionIssue) *delegation.SupervisionIssue {
	if issue == nil {
		return nil
	}
	cloned := *issue
	cloned.ChangedFiles = append([]string(nil), issue.ChangedFiles...)
	return &cloned
}

func supervisionActionAllowed(kind delegation.SupervisionDecisionKind, allowed []delegation.SupervisionDecisionKind) bool {
	if kind == "" {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(string(candidate)) == strings.TrimSpace(string(kind)) {
			return true
		}
	}
	return false
}

func boundedSupervisorError(err error) string {
	if err == nil {
		return ""
	}
	sanitized := delegation.SanitizeSupervisorText(err.Error(), "")
	return truncateProjectedReplayText("Supervisor review error", strings.TrimSpace(sanitized), supervisorMaxErrorBytes)
}
