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
	runtimeStates := service.delegationTaskRuntimeStates()
	items := make([]DelegationTaskSnapshot, 0, len(snapshots))
	now := time.Now().UTC()
	for _, snapshot := range snapshots {
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
		runtimeState := runtimeStates[strings.TrimSpace(snapshot.ID)]
		supervisionStatus := strings.TrimSpace(string(snapshot.SupervisionStatus))
		supervisionPhase := resolveDelegationRuntimePhase(snapshot, runtimeState.reviewPending)
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
			Error:                strings.TrimSpace(snapshot.Error),
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
	reviewPending       bool
	supervisorModelName string
	reviewerModelName   string
}

func (service *Service) delegationTaskRuntimeStates() map[string]delegationTaskRuntimeState {
	if service == nil || service.multitaskDelegation == nil || service.multitaskDelegation.supervisor == nil {
		return nil
	}
	supervisor := service.multitaskDelegation.supervisor
	supervisor.mu.RLock()
	aggregates := make([]*supervisedAggregate, 0, len(supervisor.aggregates))
	for _, aggregate := range supervisor.aggregates {
		if aggregate != nil {
			aggregates = append(aggregates, aggregate)
		}
	}
	supervisor.mu.RUnlock()
	if len(aggregates) == 0 {
		return nil
	}
	result := make(map[string]delegationTaskRuntimeState)
	for _, aggregate := range aggregates {
		aggregate.mu.Lock()
		supervisorModelID := resolveSupervisorModelID(aggregate.config, aggregate.base)
		supervisorModelName := resolveDelegationRuntimeModelName(aggregate.config, supervisorModelID, firstNonEmpty(strings.TrimSpace(aggregate.base.ModelName), strings.TrimSpace(aggregate.base.ModelID)))
		reviewerModelID := firstNonEmpty(strings.TrimSpace(aggregate.config.ReviewerModelID), supervisorModelID, strings.TrimSpace(aggregate.base.ModelID))
		reviewerModelName := resolveDelegationRuntimeModelName(aggregate.config, reviewerModelID, supervisorModelName)
		for _, task := range aggregate.tasks {
			if task == nil {
				continue
			}
			result[strings.TrimSpace(task.currentTaskID)] = delegationTaskRuntimeState{
				reviewPending:       task.reviewPending,
				supervisorModelName: supervisorModelName,
				reviewerModelName:   reviewerModelName,
			}
		}
		aggregate.mu.Unlock()
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func resolveDelegationRuntimePhase(snapshot delegation.TaskSnapshot, reviewPending bool) string {
	if reviewPending {
		return string(delegation.SupervisionStatusReviewing)
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
