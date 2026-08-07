package client

import (
	"cursor/internal/backend/forwarder"
)

// GetGoals 返回全部 goal 状态快照。
func (service *ProxyService) GetGoals() []forwarder.GoalSnapshot {
	if service == nil || service.backendHost == nil {
		return []forwarder.GoalSnapshot{}
	}
	return service.backendHost.GoalSnapshots()
}

// StartGoal 以 goal 模式启动新会话，返回 conversationID。
func (service *ProxyService) StartGoal(goalText, modelID string) (string, error) {
	if service == nil || service.backendHost == nil {
		return "", nil
	}
	return service.backendHost.StartGoal(goalText, modelID)
}

// StopGoal 停止指定会话的 goal 执行。
func (service *ProxyService) StopGoal(conversationID string) error {
	if service == nil || service.backendHost == nil {
		return nil
	}
	return service.backendHost.StopGoal(conversationID)
}