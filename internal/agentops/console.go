package agentops

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cursor/internal/backend/delegation"
	"cursor/internal/backend/forwarder"
	"cursor/internal/controlcenter"
)

type RunSummary struct {
	RunID              string `json:"runId"`
	ParentRunID        string `json:"parentRunId,omitempty"`
	AggregateID        string `json:"aggregateId,omitempty"`
	Status             string `json:"status"`
	Phase              string `json:"phase,omitempty"`
	ModelName          string `json:"modelName,omitempty"`
	ExecutorID         string `json:"executorId,omitempty"`
	ToolCallCount      int    `json:"toolCallCount"`
	AttemptCount       int    `json:"attemptCount"`
	DurationMS         int64  `json:"durationMs,omitempty"`
	Cancelable         bool   `json:"cancelable"`
	Retryable          bool   `json:"retryable"`
	SideEffectObserved bool   `json:"sideEffectObserved"`
	ErrorCode          string `json:"errorCode,omitempty"`
	UpdatedAtUnixMS    int64  `json:"updatedAtUnixMs"`
}

type RunQuery struct {
	Status     string `json:"status,omitempty"`
	ExecutorID string `json:"executorId,omitempty"`
	FromUnixMS int64  `json:"fromUnixMs,omitempty"`
	ToUnixMS   int64  `json:"toUnixMs,omitempty"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}

type ExecutorAttempt struct {
	ExecutorID       string `json:"executorId"`
	Attempt          int    `json:"attempt"`
	Status           string `json:"status"`
	FailureClass     string `json:"failureClass,omitempty"`
	RetrySafe        bool   `json:"retrySafe"`
	DiagnosticCode   string `json:"diagnosticCode,omitempty"`
	StartedAtUnixMS  int64  `json:"startedAtUnixMs,omitempty"`
	FinishedAtUnixMS int64  `json:"finishedAtUnixMs,omitempty"`
}

type RunDetail struct {
	Summary  RunSummary        `json:"summary"`
	Attempts []ExecutorAttempt `json:"attempts,omitempty"`
	Children []RunSummary      `json:"children,omitempty"`
}

type RunPage struct {
	Items      []RunSummary `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type RetryPreparation struct {
	controlcenter.PreparedOperation
	Run                RunSummary `json:"run"`
	OriginalInputAlive bool       `json:"originalInputAlive"`
	RetrySafe          bool       `json:"retrySafe"`
}

type RetryResult struct {
	controlcenter.OperationResult
	NewRunID string `json:"newRunId,omitempty"`
}

type Console struct {
	exportDir string
	snapshots func() []forwarder.DelegationTaskSnapshot
	cancel    func(string) bool
	mu        sync.Mutex
	pending   map[string]pendingRetry
}

type pendingRetry struct {
	token     string
	expiresAt time.Time
	used      bool
	runID     string
}

func New(exportDir string, snapshots func() []forwarder.DelegationTaskSnapshot, cancel func(string) bool) *Console {
	return &Console{
		exportDir: exportDir,
		snapshots: snapshots,
		cancel:    cancel,
		pending:   map[string]pendingRetry{},
	}
}

func (console *Console) List(query RunQuery) (RunPage, error) {
	if console == nil || console.snapshots == nil {
		return RunPage{}, controlcenter.NewError("agent_runs_unavailable", "agent runs unavailable")
	}
	limit := controlcenter.ClampLimit(query.Limit, 50, 1, 200)
	offset, err := controlcenter.DecodeOffsetCursor(query.Cursor)
	if err != nil {
		return RunPage{}, controlcenter.NewError("agent_runs_unavailable", "cursor is invalid")
	}
	items := projectSummaries(console.snapshots())
	filtered := make([]RunSummary, 0, len(items))
	for _, item := range items {
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		if query.ExecutorID != "" && item.ExecutorID != query.ExecutorID {
			continue
		}
		if query.FromUnixMS > 0 && item.UpdatedAtUnixMS < query.FromUnixMS {
			continue
		}
		if query.ToUnixMS > 0 && item.UpdatedAtUnixMS > query.ToUnixMS {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].UpdatedAtUnixMS == filtered[j].UpdatedAtUnixMS {
			return filtered[i].RunID < filtered[j].RunID
		}
		return filtered[i].UpdatedAtUnixMS > filtered[j].UpdatedAtUnixMS
	})
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := RunPage{Items: filtered[offset:end]}
	if page.Items == nil {
		page.Items = []RunSummary{}
	}
	if end < len(filtered) {
		page.NextCursor = controlcenter.EncodeOffsetCursor(end)
	}
	return page, nil
}

func (console *Console) Get(runID string) (RunDetail, error) {
	snapshot, ok := console.find(runID)
	if !ok {
		return RunDetail{}, controlcenter.NewError("agent_run_not_found", "agent run not found")
	}
	detail := RunDetail{Summary: projectSummary(snapshot), Attempts: projectAttempts(snapshot.Attempts)}
	for _, child := range console.snapshots() {
		if strings.TrimSpace(child.ParentExecID) == snapshot.ID || strings.TrimSpace(child.AggregateID) == snapshot.ID {
			detail.Children = append(detail.Children, projectSummary(child))
		}
	}
	return detail, nil
}

func (console *Console) Cancel(runID string) (controlcenter.OperationResult, error) {
	snapshot, ok := console.find(runID)
	if !ok {
		return controlcenter.OperationResult{}, controlcenter.NewError("agent_run_not_found", "agent run not found")
	}
	if !snapshot.Cancelable {
		return controlcenter.OperationResult{}, controlcenter.NewError("agent_run_not_cancelable", "agent run is not cancelable")
	}
	if console.cancel == nil || !console.cancel(snapshot.ID) {
		return controlcenter.OperationResult{}, controlcenter.NewError("agent_cancel_failed", "cancel failed")
	}
	return controlcenter.OperationResult{OperationID: snapshot.ID, State: "succeeded", FinishedAtUnixMS: time.Now().UnixMilli()}, nil
}

func (console *Console) PrepareRetry(runID string) (RetryPreparation, error) {
	snapshot, ok := console.find(runID)
	if !ok {
		return RetryPreparation{}, controlcenter.NewError("agent_run_not_found", "agent run not found")
	}
	summary := projectSummary(snapshot)
	if !summary.Retryable {
		return RetryPreparation{}, controlcenter.NewError("agent_run_not_retryable", "agent run is not retryable")
	}
	if summary.SideEffectObserved {
		return RetryPreparation{}, controlcenter.NewError("agent_retry_side_effect_risk", "side effect already observed")
	}
	token := newToken()
	operationID := "retry-" + snapshot.ID
	console.mu.Lock()
	console.pending[token] = pendingRetry{token: token, expiresAt: time.Now().Add(60 * time.Second), runID: snapshot.ID}
	console.mu.Unlock()
	return RetryPreparation{
		PreparedOperation: controlcenter.PreparedOperation{
			OperationID:       operationID,
			ConfirmationToken: token,
			ExpiresAtUnixMS:   time.Now().Add(60 * time.Second).UnixMilli(),
			ImpactCodes:       []string{"agent_retry"},
			RollbackAvailable: false,
		},
		Run:                summary,
		OriginalInputAlive: false,
		RetrySafe:          summary.Retryable,
	}, nil
}

func (console *Console) ExecuteRetry(confirmationToken string) (RetryResult, error) {
	console.mu.Lock()
	pending, ok := console.pending[strings.TrimSpace(confirmationToken)]
	if !ok || time.Now().After(pending.expiresAt) {
		console.mu.Unlock()
		return RetryResult{}, controlcenter.NewError("confirmation_expired", "confirmation expired")
	}
	if pending.used {
		console.mu.Unlock()
		return RetryResult{}, controlcenter.NewError("confirmation_already_used", "confirmation already used")
	}
	pending.used = true
	console.pending[pending.token] = pending
	console.mu.Unlock()
	return RetryResult{}, controlcenter.NewError("agent_retry_payload_unavailable", "original input is not in process memory")
}

func (console *Console) Export(runID string) (controlcenter.SanitizedExport, error) {
	detail, err := console.Get(runID)
	if err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	if err := os.MkdirAll(console.exportDir, 0o700); err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	payload, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	sum := sha256.Sum256(payload)
	name := "agent-run-" + detail.Summary.RunID + ".json"
	path := filepath.Join(console.exportDir, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	return controlcenter.SanitizedExport{Path: name, SHA256: hex.EncodeToString(sum[:])}, nil
}

func (console *Console) Count() int {
	if console == nil || console.snapshots == nil {
		return 0
	}
	return len(console.snapshots())
}

func (console *Console) find(runID string) (forwarder.DelegationTaskSnapshot, bool) {
	runID = strings.TrimSpace(runID)
	if console == nil || console.snapshots == nil || runID == "" {
		return forwarder.DelegationTaskSnapshot{}, false
	}
	for _, snapshot := range console.snapshots() {
		if snapshot.ID == runID {
			return snapshot, true
		}
	}
	return forwarder.DelegationTaskSnapshot{}, false
}

func projectSummaries(snapshots []forwarder.DelegationTaskSnapshot) []RunSummary {
	items := make([]RunSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, projectSummary(snapshot))
	}
	return items
}

func projectSummary(snapshot forwarder.DelegationTaskSnapshot) RunSummary {
	retrySafe := false
	for _, attempt := range snapshot.Attempts {
		if attempt.RetrySafe {
			retrySafe = true
		}
	}
	status := mapStatus(string(snapshot.Status))
	sideEffect := snapshot.ToolCallCount > 0
	terminalFailed := status == "failed" || status == "timed_out"
	return RunSummary{
		RunID:              snapshot.ID,
		ParentRunID:        snapshot.ParentExecID,
		AggregateID:        snapshot.AggregateID,
		Status:             status,
		Phase:              snapshot.SupervisionPhase,
		ModelName:          snapshot.ModelName,
		ExecutorID:         snapshot.ExecutorID,
		ToolCallCount:      snapshot.ToolCallCount,
		AttemptCount:       len(snapshot.Attempts),
		DurationMS:         snapshot.DurationMS,
		Cancelable:         snapshot.Cancelable,
		Retryable:          terminalFailed && retrySafe && !sideEffect,
		SideEffectObserved: sideEffect,
		ErrorCode:          snapshot.LastIssueCode,
		UpdatedAtUnixMS:    snapshot.UpdatedAtUnixMS,
	}
}

func projectAttempts(source []forwarder.DelegationExecutorAttemptSnapshot) []ExecutorAttempt {
	items := make([]ExecutorAttempt, 0, len(source))
	for _, attempt := range source {
		items = append(items, ExecutorAttempt{
			ExecutorID:       attempt.ExecutorID,
			Attempt:          attempt.Attempt,
			Status:           attempt.Status,
			FailureClass:     attempt.FailureClass,
			RetrySafe:        attempt.RetrySafe,
			DiagnosticCode:   attempt.DiagnosticCode,
			StartedAtUnixMS:  attempt.StartedAtUnixMS,
			FinishedAtUnixMS: attempt.FinishedAtUnixMS,
		})
	}
	return items
}

func mapStatus(status string) string {
	switch delegation.TaskStatus(status) {
	case delegation.TaskCompleted:
		return "succeeded"
	case delegation.TaskQueued:
		return "queued"
	case delegation.TaskRunning:
		return "running"
	case delegation.TaskFailed:
		return "failed"
	case delegation.TaskCanceled:
		return "canceled"
	case delegation.TaskTimedOut:
		return "timed_out"
	default:
		if status == "backgrounded" || status == "waiting" {
			return status
		}
		if status == "" {
			return "queued"
		}
		return status
	}
}

func newToken() string {
	sum := sha256.Sum256([]byte(time.Now().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:16])
}
