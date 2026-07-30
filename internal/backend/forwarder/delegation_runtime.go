package forwarder

import (
	"strings"
	"time"

	"cursor/internal/backend/delegation"
)

// DelegationTaskSnapshot is the sanitized runtime view exposed to the desktop UI.
type DelegationTaskSnapshot struct {
	ID               string                `json:"id"`
	AggregateID      string                `json:"aggregateId"`
	Description      string                `json:"description"`
	ModelID          string                `json:"modelId"`
	ModelName        string                `json:"modelName"`
	ModelGroupID     string                `json:"modelGroupId"`
	ExecutionMode    string                `json:"executionMode"`
	Status           delegation.TaskStatus `json:"status"`
	ToolCallCount    int                   `json:"toolCallCount"`
	Error            string                `json:"error,omitempty"`
	EventID          string                `json:"eventId"`
	Sequence         uint64                `json:"sequence"`
	EventType        string                `json:"eventType"`
	ParentRequestID  string                `json:"parentRequestId"`
	ParentExecID     string                `json:"parentExecId"`
	GroupID          string                `json:"groupId"`
	QueuedAtUnixMS   int64                 `json:"queuedAtUnixMs"`
	StartedAtUnixMS  int64                 `json:"startedAtUnixMs"`
	FinishedAtUnixMS int64                 `json:"finishedAtUnixMs"`
	UpdatedAtUnixMS  int64                 `json:"updatedAtUnixMs"`
	DurationMS       int64                 `json:"durationMs"`
	Cancelable       bool                  `json:"cancelable"`
}

// DelegationTaskSnapshots returns retained worker state without prompts, tool
// arguments, workspace paths, or model credentials.
func (service *Service) DelegationTaskSnapshots() []DelegationTaskSnapshot {
	if service == nil || service.multitaskDelegation == nil {
		return nil
	}
	snapshots := service.multitaskDelegation.Snapshots()
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
		items = append(items, DelegationTaskSnapshot{
			ID:               strings.TrimSpace(snapshot.ID),
			AggregateID:      strings.TrimSpace(snapshot.Request.ParentExecID),
			Description:      strings.TrimSpace(snapshot.Request.Description),
			ModelID:          strings.TrimSpace(snapshot.Request.ModelID),
			ModelName:        strings.TrimSpace(snapshot.Request.ModelName),
			ModelGroupID:     strings.TrimSpace(snapshot.Request.ModelGroupID),
			ExecutionMode:    strings.TrimSpace(snapshot.Request.ExecutionMode),
			Status:           snapshot.Status,
			ToolCallCount:    snapshot.ToolCallCount,
			Error:            strings.TrimSpace(snapshot.Error),
			EventID:          strings.TrimSpace(snapshot.EventID),
			Sequence:         snapshot.Sequence,
			EventType:        strings.TrimSpace(snapshot.EventType),
			ParentRequestID:  strings.TrimSpace(snapshot.ParentRequestID),
			ParentExecID:     strings.TrimSpace(snapshot.ParentExecID),
			GroupID:          strings.TrimSpace(snapshot.GroupID),
			QueuedAtUnixMS:   unixMilliseconds(snapshot.QueuedAt),
			StartedAtUnixMS:  unixMilliseconds(snapshot.StartedAt),
			FinishedAtUnixMS: unixMilliseconds(finishedAt),
			UpdatedAtUnixMS:  unixMilliseconds(snapshot.UpdatedAt),
			DurationMS:       duration,
			Cancelable:       !delegatedStatusTerminal(snapshot.Status),
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
