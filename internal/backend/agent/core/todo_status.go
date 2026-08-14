package runtimecore

import (
	"fmt"
	"strconv"
	"strings"

	"cursor/gen/agentv1"
)

// TodoStatusFromValue parses todo status from JSON-decoded dynamic values.
func TodoStatusFromValue(value any) (agentv1.TodoStatus, error) {
	switch item := value.(type) {
	case nil:
		return agentv1.TodoStatus_TODO_STATUS_UNSPECIFIED, nil
	case float64:
		return agentv1.TodoStatus(int32(item)), nil
	case float32:
		return agentv1.TodoStatus(int32(item)), nil
	case int:
		return agentv1.TodoStatus(item), nil
	case int32:
		return agentv1.TodoStatus(item), nil
	case int64:
		return agentv1.TodoStatus(item), nil
	case string:
		return TodoStatusFromString(item)
	default:
		return agentv1.TodoStatus_TODO_STATUS_UNSPECIFIED, fmt.Errorf("unsupported todo status type %T", value)
	}
}

// TodoStatusFromString parses todo status from string tokens (including numeric strings).
func TodoStatusFromString(raw string) (agentv1.TodoStatus, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" || normalized == "unspecified" || normalized == "todo_status_unspecified" {
		return agentv1.TodoStatus_TODO_STATUS_UNSPECIFIED, nil
	}
	if numeric, err := strconv.ParseInt(normalized, 10, 32); err == nil {
		return agentv1.TodoStatus(numeric), nil
	}
	switch normalized {
	case "pending", "todo_status_pending":
		return agentv1.TodoStatus_TODO_STATUS_PENDING, nil
	case "in_progress", "in-progress", "inprogress", "todo_status_in_progress":
		return agentv1.TodoStatus_TODO_STATUS_IN_PROGRESS, nil
	case "completed", "complete", "todo_status_completed":
		return agentv1.TodoStatus_TODO_STATUS_COMPLETED, nil
	case "cancelled", "canceled", "todo_status_cancelled":
		return agentv1.TodoStatus_TODO_STATUS_CANCELLED, nil
	default:
		return agentv1.TodoStatus_TODO_STATUS_UNSPECIFIED, fmt.Errorf("unsupported todo status %q", raw)
	}
}
