package client

import "cursor/internal/backend/forwarder"

// GetDelegationTaskSnapshots returns retained Multitask worker state.
func (s *ProxyService) GetDelegationTaskSnapshots() []forwarder.DelegationTaskSnapshot {
	if s == nil || s.backendHost == nil {
		return nil
	}
	return s.backendHost.DelegationTaskSnapshots()
}

// CancelDelegationTask cancels one Multitask worker without stopping siblings.
func (s *ProxyService) CancelDelegationTask(taskID string) bool {
	if s == nil || s.backendHost == nil {
		return false
	}
	return s.backendHost.CancelDelegationTask(taskID)
}
