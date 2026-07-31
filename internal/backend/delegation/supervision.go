package delegation

import (
	"strings"
	"time"
)

type SupervisionStatus string

const (
	SupervisionStatusPlanned       SupervisionStatus = "planned"
	SupervisionStatusDispatched    SupervisionStatus = "dispatched"
	SupervisionStatusRunning       SupervisionStatus = "running"
	SupervisionStatusCheckpointing SupervisionStatus = "checkpointing"
	SupervisionStatusReviewing     SupervisionStatus = "reviewing"
	SupervisionStatusCorrecting    SupervisionStatus = "correcting"
	SupervisionStatusRetrying      SupervisionStatus = "retrying"
	SupervisionStatusReassigning   SupervisionStatus = "reassigning"
	SupervisionStatusEscalated     SupervisionStatus = "escalated"
	SupervisionStatusCompleted     SupervisionStatus = "completed"
	SupervisionStatusFailed        SupervisionStatus = "failed"
	SupervisionStatusCanceled      SupervisionStatus = "canceled"
	SupervisionStatusCircuitOpen   SupervisionStatus = "circuit_open"
)

type SupervisionDecisionKind string

const (
	SupervisionDecisionAccept      SupervisionDecisionKind = "accept"
	SupervisionDecisionContinue    SupervisionDecisionKind = "continue"
	SupervisionDecisionCorrect     SupervisionDecisionKind = "correct"
	SupervisionDecisionRetry       SupervisionDecisionKind = "retry"
	SupervisionDecisionReassign    SupervisionDecisionKind = "reassign"
	SupervisionDecisionEscalate    SupervisionDecisionKind = "escalate"
	SupervisionDecisionCircuitOpen SupervisionDecisionKind = "circuit_open"
)

type SupervisionIssueCode string

const (
	SupervisionIssueToolFailure     SupervisionIssueCode = "tool_failure"
	SupervisionIssueNoProgress      SupervisionIssueCode = "no_progress"
	SupervisionIssueRepeatedAction  SupervisionIssueCode = "repeated_action"
	SupervisionIssueScopeDrift      SupervisionIssueCode = "scope_drift"
	SupervisionIssueMissingEvidence SupervisionIssueCode = "missing_evidence"
	SupervisionIssueTimeout         SupervisionIssueCode = "timeout"
	SupervisionIssueModelFailure    SupervisionIssueCode = "model_failure"
	SupervisionIssueReviewFailure   SupervisionIssueCode = "review_failure"
	SupervisionIssueCorrectionLimit SupervisionIssueCode = "correction_limit"
	SupervisionIssueRetryLimit      SupervisionIssueCode = "retry_limit"
	SupervisionIssueRoundLimit      SupervisionIssueCode = "round_limit"
)

const (
	DefaultSupervisionCorrections        = 2
	DefaultSupervisionRetries            = 1
	DefaultSupervisionRounds             = 8
	DefaultSupervisionCheckpointInterval = 1500 * time.Millisecond
)

type SupervisionTaskContract struct {
	AggregateID        string
	TaskID             string
	Round              int
	Goal               string
	Scope              string
	Role               string
	AllowedTools       []string
	ExpectedOutput     string
	DoneCriteria       []string
	MaxSteps           int
	MaxCorrections     int
	MaxRetries         int
	MaxRounds          int
	CheckpointInterval time.Duration
	Timeout            time.Duration
	FailurePolicy      string
	WorkspaceHint      string
	SelectedSkillIDs   []string
	SelectedMCPIDs     []string
}

type WorkerCheckpoint struct {
	AggregateID          string
	TaskID               string
	Round                int
	Phase                SupervisionStatus
	Step                 int
	RecentToolNames      []string
	ChangedFileSummaries []string
	ProgressSummary      string
	Blocker              string
	EffectiveProgressAt  time.Time
	EventSequence        uint64
}

type SupervisionDecision struct {
	Kind    SupervisionDecisionKind
	Reason  string
	Summary string
	Round   int
	Step    int
	At      time.Time
}

type SupervisionCounters struct {
	Corrections int
	Retries     int
	Rounds      int
	Checkpoints int
}

type SupervisionIssue struct {
	Code          SupervisionIssueCode
	Summary       string
	ToolSignature string
	ChangedFiles  []string
	Round         int
	Step          int
	DetectedAt    time.Time
}

func supervisionStatusForTaskStatus(status TaskStatus) SupervisionStatus {
	switch status {
	case TaskQueued:
		return SupervisionStatusPlanned
	case TaskRunning:
		return SupervisionStatusRunning
	case TaskCompleted:
		return SupervisionStatusCompleted
	case TaskFailed, TaskTimedOut:
		return SupervisionStatusFailed
	case TaskCanceled:
		return SupervisionStatusCanceled
	default:
		return ""
	}
}

func cloneSupervisionTaskContract(contract *SupervisionTaskContract) *SupervisionTaskContract {
	if contract == nil {
		return nil
	}
	cloned := *contract
	cloned.AllowedTools = cloneStringSlice(contract.AllowedTools)
	cloned.DoneCriteria = cloneStringSlice(contract.DoneCriteria)
	cloned.SelectedSkillIDs = cloneStringSlice(contract.SelectedSkillIDs)
	cloned.SelectedMCPIDs = cloneStringSlice(contract.SelectedMCPIDs)
	return &cloned
}

func normalizeSupervisionTaskContract(contract SupervisionTaskContract) SupervisionTaskContract {
	contract.AggregateID = strings.TrimSpace(contract.AggregateID)
	contract.TaskID = strings.TrimSpace(contract.TaskID)
	if contract.Round < 1 {
		contract.Round = 1
	}
	contract.Goal = strings.TrimSpace(contract.Goal)
	contract.Scope = strings.TrimSpace(contract.Scope)
	contract.Role = strings.TrimSpace(contract.Role)
	contract.AllowedTools = normalizeStringSlice(contract.AllowedTools)
	contract.ExpectedOutput = strings.TrimSpace(contract.ExpectedOutput)
	contract.DoneCriteria = normalizeStringSlice(contract.DoneCriteria)
	if contract.MaxSteps < 0 {
		contract.MaxSteps = 0
	}
	if contract.MaxCorrections <= 0 {
		contract.MaxCorrections = DefaultSupervisionCorrections
	}
	if contract.MaxRetries <= 0 {
		contract.MaxRetries = DefaultSupervisionRetries
	}
	if contract.MaxRounds <= 0 {
		contract.MaxRounds = DefaultSupervisionRounds
	}
	if contract.CheckpointInterval <= 0 {
		contract.CheckpointInterval = DefaultSupervisionCheckpointInterval
	}
	if contract.Timeout < 0 {
		contract.Timeout = 0
	}
	contract.FailurePolicy = strings.TrimSpace(contract.FailurePolicy)
	contract.WorkspaceHint = strings.TrimSpace(contract.WorkspaceHint)
	contract.SelectedSkillIDs = normalizeStringSlice(contract.SelectedSkillIDs)
	contract.SelectedMCPIDs = normalizeStringSlice(contract.SelectedMCPIDs)
	return contract
}

func cloneWorkerCheckpoint(checkpoint *WorkerCheckpoint) *WorkerCheckpoint {
	if checkpoint == nil {
		return nil
	}
	cloned := *checkpoint
	cloned.RecentToolNames = cloneStringSlice(checkpoint.RecentToolNames)
	cloned.ChangedFileSummaries = cloneStringSlice(checkpoint.ChangedFileSummaries)
	return &cloned
}

func normalizeWorkerCheckpoint(checkpoint WorkerCheckpoint) WorkerCheckpoint {
	checkpoint.AggregateID = strings.TrimSpace(checkpoint.AggregateID)
	checkpoint.TaskID = strings.TrimSpace(checkpoint.TaskID)
	if checkpoint.Round < 0 {
		checkpoint.Round = 0
	}
	phase := normalizeSupervisionStatus(string(checkpoint.Phase))
	if phase == "" {
		phase = SupervisionStatusCheckpointing
	}
	checkpoint.Phase = phase
	if checkpoint.Step < 0 {
		checkpoint.Step = 0
	}
	checkpoint.RecentToolNames = normalizeStringSlice(checkpoint.RecentToolNames)
	checkpoint.ChangedFileSummaries = normalizeStringSlice(checkpoint.ChangedFileSummaries)
	checkpoint.ProgressSummary = strings.TrimSpace(checkpoint.ProgressSummary)
	checkpoint.Blocker = strings.TrimSpace(checkpoint.Blocker)
	if !checkpoint.EffectiveProgressAt.IsZero() {
		checkpoint.EffectiveProgressAt = checkpoint.EffectiveProgressAt.UTC()
	}
	return checkpoint
}

func normalizeSupervisedWorkerCheckpoint(checkpoint WorkerCheckpoint, workspaceHint string) WorkerCheckpoint {
	checkpoint = normalizeWorkerCheckpoint(checkpoint)
	checkpoint.ChangedFileSummaries = normalizeChangedFileSummaries(checkpoint.ChangedFileSummaries, workspaceHint)
	checkpoint.ProgressSummary = sanitizeNarrativeText(checkpoint.ProgressSummary, workspaceHint)
	checkpoint.Blocker = sanitizeNarrativeText(checkpoint.Blocker, workspaceHint)
	return checkpoint
}

func cloneSupervisionDecision(decision SupervisionDecision) SupervisionDecision {
	return decision
}

func normalizeSupervisionDecision(decision SupervisionDecision) SupervisionDecision {
	decision.Kind = normalizeSupervisionDecisionKind(string(decision.Kind))
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.Summary = strings.TrimSpace(decision.Summary)
	if decision.Round < 0 {
		decision.Round = 0
	}
	if decision.Step < 0 {
		decision.Step = 0
	}
	if !decision.At.IsZero() {
		decision.At = decision.At.UTC()
	}
	return decision
}

func cloneSupervisionCounters(counters SupervisionCounters) SupervisionCounters {
	return counters
}

func cloneSupervisionIssue(issue *SupervisionIssue) *SupervisionIssue {
	if issue == nil {
		return nil
	}
	cloned := *issue
	cloned.ChangedFiles = cloneStringSlice(issue.ChangedFiles)
	return &cloned
}

func normalizeSupervisionCounters(counters SupervisionCounters) SupervisionCounters {
	if counters.Corrections < 0 {
		counters.Corrections = 0
	}
	if counters.Retries < 0 {
		counters.Retries = 0
	}
	if counters.Rounds < 0 {
		counters.Rounds = 0
	}
	if counters.Checkpoints < 0 {
		counters.Checkpoints = 0
	}
	return counters
}

func normalizeSupervisionIssue(issue SupervisionIssue) SupervisionIssue {
	issue.Code = normalizeSupervisionIssueCode(string(issue.Code))
	issue.Summary = strings.TrimSpace(issue.Summary)
	issue.ToolSignature = normalizeToolSignatureValue(issue.ToolSignature)
	issue.ChangedFiles = normalizeChangedFileSummaries(issue.ChangedFiles, "")
	if issue.Round < 0 {
		issue.Round = 0
	}
	if issue.Step < 0 {
		issue.Step = 0
	}
	if !issue.DetectedAt.IsZero() {
		issue.DetectedAt = issue.DetectedAt.UTC()
	}
	return issue
}

func normalizeSupervisionStatus(value string) SupervisionStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SupervisionStatusPlanned):
		return SupervisionStatusPlanned
	case string(SupervisionStatusDispatched):
		return SupervisionStatusDispatched
	case string(SupervisionStatusRunning):
		return SupervisionStatusRunning
	case string(SupervisionStatusCheckpointing):
		return SupervisionStatusCheckpointing
	case string(SupervisionStatusReviewing):
		return SupervisionStatusReviewing
	case string(SupervisionStatusCorrecting):
		return SupervisionStatusCorrecting
	case string(SupervisionStatusRetrying):
		return SupervisionStatusRetrying
	case string(SupervisionStatusReassigning):
		return SupervisionStatusReassigning
	case string(SupervisionStatusEscalated):
		return SupervisionStatusEscalated
	case string(SupervisionStatusCompleted):
		return SupervisionStatusCompleted
	case string(SupervisionStatusFailed):
		return SupervisionStatusFailed
	case string(SupervisionStatusCanceled):
		return SupervisionStatusCanceled
	case string(SupervisionStatusCircuitOpen):
		return SupervisionStatusCircuitOpen
	default:
		return ""
	}
}

func normalizeSupervisionDecisionKind(value string) SupervisionDecisionKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SupervisionDecisionAccept):
		return SupervisionDecisionAccept
	case string(SupervisionDecisionContinue):
		return SupervisionDecisionContinue
	case string(SupervisionDecisionCorrect):
		return SupervisionDecisionCorrect
	case string(SupervisionDecisionRetry):
		return SupervisionDecisionRetry
	case string(SupervisionDecisionReassign):
		return SupervisionDecisionReassign
	case string(SupervisionDecisionEscalate):
		return SupervisionDecisionEscalate
	case string(SupervisionDecisionCircuitOpen):
		return SupervisionDecisionCircuitOpen
	default:
		return ""
	}
}

func normalizeSupervisionIssueCode(value string) SupervisionIssueCode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SupervisionIssueToolFailure):
		return SupervisionIssueToolFailure
	case string(SupervisionIssueNoProgress):
		return SupervisionIssueNoProgress
	case string(SupervisionIssueRepeatedAction):
		return SupervisionIssueRepeatedAction
	case string(SupervisionIssueScopeDrift):
		return SupervisionIssueScopeDrift
	case string(SupervisionIssueMissingEvidence):
		return SupervisionIssueMissingEvidence
	case string(SupervisionIssueTimeout):
		return SupervisionIssueTimeout
	case string(SupervisionIssueModelFailure):
		return SupervisionIssueModelFailure
	case string(SupervisionIssueReviewFailure):
		return SupervisionIssueReviewFailure
	case string(SupervisionIssueCorrectionLimit):
		return SupervisionIssueCorrectionLimit
	case string(SupervisionIssueRetryLimit):
		return SupervisionIssueRetryLimit
	case string(SupervisionIssueRoundLimit):
		return SupervisionIssueRoundLimit
	default:
		return ""
	}
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cloned = append(cloned, trimmed)
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func normalizeStringSlice(values []string) []string {
	return cloneStringSlice(values)
}
