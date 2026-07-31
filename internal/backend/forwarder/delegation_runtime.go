package forwarder

import (
	"strings"
	"time"

	"cursor/internal/backend/delegation"
)

// DelegationTaskSnapshot is the sanitized runtime view exposed to the desktop UI.
type DelegationTaskSnapshot struct {
	ID                   string                `json:"id"`
	AggregateID          string                `json:"aggregateId"`
	Description          string                `json:"description"`
	ModelID              string                `json:"modelId"`
	ModelName            string                `json:"modelName"`
	ModelGroupID         string                `json:"modelGroupId"`
	WorkerRole           string                `json:"workerRole,omitempty"`
	ExecutionMode        string                `json:"executionMode"`
	Status               delegation.TaskStatus `json:"status"`
	SupervisionStatus    string                `json:"supervisionStatus,omitempty"`
	SupervisionPhase     string                `json:"supervisionPhase,omitempty"`
	SupervisorModelName  string                `json:"supervisorModelName,omitempty"`
	ReviewerModelName    string                `json:"reviewerModelName,omitempty"`
	SupervisionRound     int                   `json:"supervisionRound,omitempty"`
	CorrectionCount      int                   `json:"correctionCount,omitempty"`
	RetryCount           int                   `json:"retryCount,omitempty"`
	ReassignCount        int                   `json:"reassignCount,omitempty"`
	EscalateCount        int                   `json:"escalateCount,omitempty"`
	IssueCategory        string                `json:"issueCategory,omitempty"`
	LastIssueCode        string                `json:"lastIssueCode,omitempty"`
	LastProgressAtUnixMS int64                 `json:"lastProgressAtUnixMs,omitempty"`
	ProgressSummary      string                `json:"progressSummary,omitempty"`
	ToolCallCount        int                   `json:"toolCallCount"`
	Error                string                `json:"error,omitempty"`
	EventID              string                `json:"eventId"`
	Sequence             uint64                `json:"sequence"`
	EventType            string                `json:"eventType"`
	ParentRequestID      string                `json:"parentRequestId"`
	ParentExecID         string                `json:"parentExecId"`
	GroupID              string                `json:"groupId"`
	QueuedAtUnixMS       int64                 `json:"queuedAtUnixMs"`
	StartedAtUnixMS      int64                 `json:"startedAtUnixMs"`
	FinishedAtUnixMS     int64                 `json:"finishedAtUnixMs"`
	UpdatedAtUnixMS      int64                 `json:"updatedAtUnixMs"`
	DurationMS           int64                 `json:"durationMs"`
	Cancelable           bool                  `json:"cancelable"`
}

// DelegationTaskSnapshots returns retained worker state without prompts, tool
// arguments, workspace paths, or model credentials.
func (service *Service) DelegationTaskSnapshots() []DelegationTaskSnapshot {
	if service == nil || service.multitaskDelegation == nil {
		return nil
	}
	snapshots := service.multitaskDelegation.Snapshots()
	runtimeStates := service.delegationTaskRuntimeStates(snapshots)
	items := make([]DelegationTaskSnapshot, 0, len(snapshots))
	now := time.Now().UTC()
	for _, snapshot := range snapshots {
		runtimeState := runtimeStates[strings.TrimSpace(snapshot.ID)]
		if runtimeState.hasSnapshotOverride {
			if runtimeState.status != "" {
				snapshot.Status = runtimeState.status
			}
			if runtimeState.supervisionStatus != "" {
				snapshot.SupervisionStatus = runtimeState.supervisionStatus
			}
			if runtimeState.issueCode != "" {
				snapshot.SupervisionIssue = runtimeState.issueCode
			}
			if runtimeState.error != "" {
				snapshot.Error = runtimeState.error
			}
			if !runtimeState.updatedAt.IsZero() {
				snapshot.UpdatedAt = runtimeState.updatedAt
			}
			if !runtimeState.finishedAt.IsZero() {
				snapshot.FinishedAt = runtimeState.finishedAt
			}
		}
		finishedAt := snapshot.FinishedAt
		end := finishedAt
		if end.IsZero() && !snapshot.StartedAt.IsZero() {
			end = now
		}
		duration := int64(0)
		if !snapshot.StartedAt.IsZero() && !end.IsZero() {
			duration = end.Sub(snapshot.StartedAt).Milliseconds()
			if duration < 0 {
				duration = 0
			}
		}
		supervisionStatus := strings.TrimSpace(string(snapshot.SupervisionStatus))
		supervisionPhase := resolveDelegationRuntimePhase(snapshot, runtimeState.reviewPending)
		if runtimeState.hasSnapshotOverride && runtimeState.supervisionPhase != "" {
			supervisionPhase = runtimeState.supervisionPhase
		}
		issueCode := strings.TrimSpace(string(snapshot.SupervisionIssue))
		progressSummary := normalizeDelegationRuntimeSummary(snapshot.ProgressSummary)
		lastProgressAt := time.Time{}
		if snapshot.Checkpoint != nil {
			lastProgressAt = snapshot.Checkpoint.EffectiveProgressAt
			if progressSummary == "" {
				progressSummary = normalizeDelegationRuntimeSummary(snapshot.Checkpoint.ProgressSummary)
			}
		}
		if lastProgressAt.IsZero() {
			lastProgressAt = snapshot.UpdatedAt
		}
		if lastProgressAt.IsZero() {
			lastProgressAt = snapshot.FinishedAt
		}
		items = append(items, DelegationTaskSnapshot{
			ID:                   strings.TrimSpace(snapshot.ID),
			AggregateID:          strings.TrimSpace(snapshot.ParentExecID),
			Description:          strings.TrimSpace(snapshot.Description),
			ModelID:              strings.TrimSpace(snapshot.ModelID),
			ModelName:            strings.TrimSpace(snapshot.ModelName),
			ModelGroupID:         strings.TrimSpace(snapshot.ModelGroupID),
			WorkerRole:           strings.TrimSpace(snapshot.WorkerRole),
			ExecutionMode:        strings.TrimSpace(snapshot.ExecutionMode),
			Status:               snapshot.Status,
			SupervisionStatus:    supervisionStatus,
			SupervisionPhase:     supervisionPhase,
			SupervisorModelName:  runtimeState.supervisorModelName,
			ReviewerModelName:    runtimeState.reviewerModelName,
			SupervisionRound:     snapshot.SupervisionRound,
			CorrectionCount:      snapshot.CorrectionCount,
			RetryCount:           snapshot.RetryCount,
			ReassignCount:        snapshot.ReassignCount,
			EscalateCount:        snapshot.EscalateCount,
			IssueCategory:        issueCode,
			LastIssueCode:        issueCode,
			LastProgressAtUnixMS: unixMilliseconds(lastProgressAt),
			ProgressSummary:      progressSummary,
			ToolCallCount:        snapshot.ToolCallCount,
			Error:                safeDelegationRuntimeError(snapshot.Status),
			EventID:              strings.TrimSpace(snapshot.EventID),
			Sequence:             snapshot.Sequence,
			EventType:            strings.TrimSpace(snapshot.EventType),
			ParentRequestID:      strings.TrimSpace(snapshot.ParentRequestID),
			ParentExecID:         strings.TrimSpace(snapshot.ParentExecID),
			GroupID:              strings.TrimSpace(snapshot.GroupID),
			QueuedAtUnixMS:       unixMilliseconds(snapshot.QueuedAt),
			StartedAtUnixMS:      unixMilliseconds(snapshot.StartedAt),
			FinishedAtUnixMS:     unixMilliseconds(finishedAt),
			UpdatedAtUnixMS:      unixMilliseconds(snapshot.UpdatedAt),
			DurationMS:           duration,
			Cancelable:           runtimeState.reviewPending || !delegatedStatusTerminal(snapshot.Status),
		})
	}
	return items
}

// CancelDelegationTask cancels one worker without affecting sibling workers.
func (service *Service) CancelDelegationTask(taskID string) bool {
	if service == nil || service.multitaskDelegation == nil {
		return false
	}
	return service.multitaskDelegation.CancelTask(taskID)
}

func unixMilliseconds(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

type delegationTaskRuntimeState struct {
	hasSnapshotOverride bool
	reviewPending       bool
	supervisorModelName string
	reviewerModelName   string
	status              delegation.TaskStatus
	supervisionStatus   delegation.SupervisionStatus
	supervisionPhase    string
	issueCode           delegation.SupervisionIssueCode
	error               string
	updatedAt           time.Time
	finishedAt          time.Time
}

func (service *Service) delegationTaskRuntimeStates(snapshots []delegation.TaskSnapshot) map[string]delegationTaskRuntimeState {
	if service == nil || service.multitaskDelegation == nil || service.multitaskDelegation.supervisor == nil {
		return make(map[string]delegationTaskRuntimeState)
	}
	supervisor := service.multitaskDelegation.supervisor
	taskIDs := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		if taskID := strings.TrimSpace(snapshot.ID); taskID != "" {
			taskIDs[taskID] = struct{}{}
		}
	}
	supervisor.mu.RLock()
	aggregates := make([]*supervisedAggregate, 0, len(supervisor.aggregates))
	for _, aggregate := range supervisor.aggregates {
		if aggregate != nil {
			aggregates = append(aggregates, aggregate)
		}
	}
	supervisor.mu.RUnlock()
	result := supervisor.runtimeTaskStates(taskIDs)
	if result == nil {
		result = make(map[string]delegationTaskRuntimeState, len(taskIDs))
	}
	for _, aggregate := range aggregates {
		aggregate.mu.Lock()
		for _, task := range aggregate.tasks {
			if task == nil {
				continue
			}
			taskID := strings.TrimSpace(task.currentTaskID)
			if _, ok := taskIDs[taskID]; !ok {
				continue
			}
			if existing, ok := result[taskID]; ok && existing.hasSnapshotOverride {
				continue
			}
			result[taskID] = delegationTaskRuntimeStateForTask(aggregate, task, false)
		}
		aggregate.mu.Unlock()
	}
	return result
}

func delegationTaskRuntimeStateForTask(aggregate *supervisedAggregate, task *supervisedTaskState, snapshotOverride bool) delegationTaskRuntimeState {
	if aggregate == nil || task == nil {
		return delegationTaskRuntimeState{}
	}
	supervisorModelID := resolveSupervisorModelID(aggregate.config, aggregate.base)
	supervisorModelName := resolveDelegationRuntimeModelName(aggregate.config, supervisorModelID, firstNonEmpty(strings.TrimSpace(aggregate.base.ModelName), strings.TrimSpace(aggregate.base.ModelID)))
	reviewerModelID := firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), supervisorModelID, strings.TrimSpace(aggregate.base.ModelID))
	state := delegationTaskRuntimeState{
		hasSnapshotOverride: snapshotOverride,
		reviewPending:       task.reviewPending,
		supervisorModelName: supervisorModelName,
		reviewerModelName:   resolveDelegationRuntimeModelName(aggregate.config, reviewerModelID, supervisorModelName),
	}
	if snapshotOverride {
		state.status = task.lastSnapshot.Status
		state.supervisionStatus = task.lastSnapshot.SupervisionStatus
		state.supervisionPhase = resolveDelegationRuntimePhase(task.lastSnapshot, task.reviewPending)
		state.issueCode = task.lastSnapshot.SupervisionIssue
		state.error = safeDelegationRuntimeError(task.lastSnapshot.Status)
		state.updatedAt = task.lastSnapshot.UpdatedAt
		state.finishedAt = task.lastSnapshot.FinishedAt
	}
	return state
}

func safeDelegationRuntimeError(status delegation.TaskStatus) string {
	switch status {
	case delegation.TaskCanceled:
		return "delegation task canceled"
	case delegation.TaskTimedOut:
		return "delegation task timed out"
	case delegation.TaskFailed:
		return "delegation task failed"
	default:
		return ""
	}
}

func resolveDelegationRuntimePhase(snapshot delegation.TaskSnapshot, reviewPending bool) string {
	if reviewPending {
		return string(delegation.SupervisionStatusReviewing)
	}
	if delegatedStatusTerminal(snapshot.Status) && snapshot.SupervisionStatus != "" {
		return strings.TrimSpace(string(snapshot.SupervisionStatus))
	}
	if snapshot.Checkpoint != nil && snapshot.Checkpoint.Phase != "" {
		return strings.TrimSpace(string(snapshot.Checkpoint.Phase))
	}
	return strings.TrimSpace(string(snapshot.SupervisionStatus))
}

func resolveDelegationRuntimeModelName(config delegation.RuntimeConfig, modelID string, fallback string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		if name := strings.TrimSpace(config.ModelNames[modelID]); name != "" {
			return name
		}
		return modelID
	}
	return strings.TrimSpace(fallback)
}

func normalizeDelegationRuntimeSummary(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return truncateProjectedReplayText("Supervisor progress", value, 512)
}
