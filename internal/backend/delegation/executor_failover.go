package delegation

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const ExecutorMetadataPreviousOutputKey = "executor_previous_output"

type FailoverExecutorConfig struct {
	Registry             *ExecutorRegistry
	MaxAttempts          func() int
	RequiredCapabilities func(TaskRequest) []ExecutorCapability
	FallbackID           ExecutorID
	Fallback             Executor
}

func NewFailoverExecutor(config FailoverExecutorConfig) Executor {
	return func(ctx context.Context, request TaskRequest) TaskResult {
		if ctx == nil {
			return TaskResult{Error: fmt.Errorf("executor failover context is nil")}
		}
		limit := 1
		if config.MaxAttempts != nil {
			configuredLimit := config.MaxAttempts()
			if configuredLimit > 0 {
				limit = configuredLimit
			}
		}
		required := []ExecutorCapability(nil)
		if config.RequiredCapabilities != nil {
			required = config.RequiredCapabilities(request)
		}
		candidates := config.Registry.Eligible(required)
		attempts := make([]ExecutorAttemptSnapshot, 0, limit)
		previousOutput := ""
		var last TaskResult

		for index, candidate := range candidates {
			if len(attempts) >= limit {
				break
			}
			if err := ctx.Err(); err != nil {
				return failoverResult(last, attempts, previousOutput, err)
			}
			executor, err := config.Registry.Executor(candidate.ID)
			if err != nil {
				return failoverResult(last, attempts, previousOutput, err)
			}
			result := executor(ctx, request)
			attempts = appendRenumberedAttempts(attempts, result.Attempts)
			if strings.TrimSpace(result.Output) != "" && result.Error != nil {
				previousOutput = appendPreviousOutput(previousOutput, result.Output)
			}
			last = result
			if result.Error == nil {
				return failoverResult(result, attempts, previousOutput, nil)
			}
			if ctx.Err() != nil {
				return failoverResult(result, attempts, previousOutput, ctx.Err())
			}
			class, retrySafe := ExecutorErrorClassification(result.Error)
			if class != ExecutorFailureSwitchable || !retrySafe {
				return failoverResult(result, attempts, previousOutput, result.Error)
			}
			if index == len(candidates)-1 && config.Fallback != nil && len(attempts) < limit {
				return executeFallback(ctx, request, config, attempts, previousOutput)
			}
		}

		if len(candidates) == 0 && config.Fallback != nil && len(attempts) < limit {
			return executeFallback(ctx, request, config, attempts, previousOutput)
		}
		if last.Error != nil {
			return failoverResult(last, attempts, previousOutput, last.Error)
		}
		return failoverResult(last, attempts, previousOutput, fmt.Errorf("no eligible delegation executor"))
	}
}

func executeFallback(ctx context.Context, request TaskRequest, config FailoverExecutorConfig, attempts []ExecutorAttemptSnapshot, previousOutput string) TaskResult {
	startedAt := time.Now().UTC()
	id := config.FallbackID
	if id == "" {
		id = ExecutorID("fallback")
	}
	publishExecutorAttempt(ctx, ExecutorAttemptSnapshot{
		ExecutorID: id, Status: ExecutorAttemptRunning, StartedAt: startedAt,
	})
	result := config.Fallback(ctx, request)
	finishedAt := time.Now().UTC()
	attempt := ExecutorAttemptSnapshot{
		ExecutorID: id, Attempt: len(attempts) + 1, Status: ExecutorAttemptCompleted,
		StartedAt: startedAt, FinishedAt: finishedAt,
	}
	if result.Error != nil {
		attempt.Status = executorAttemptStatusForError(ctx, result.Error)
		attempt.FailureClass, attempt.RetrySafe = ExecutorErrorClassification(result.Error)
		attempt.DiagnosticCode = executorErrorCode(result.Error)
		attempt.Error = result.Error.Error()
	}
	result.ExecutorID = id
	result.Attempts = append(attempts, attempt)
	publishExecutorAttempt(ctx, attempt)
	return failoverResult(result, result.Attempts, previousOutput, result.Error)
}

func appendRenumberedAttempts(target, source []ExecutorAttemptSnapshot) []ExecutorAttemptSnapshot {
	for _, attempt := range cloneExecutorAttempts(source) {
		attempt.Attempt = len(target) + 1
		target = append(target, attempt)
	}
	return target
}

func appendPreviousOutput(current, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return output
	}
	return current + "\n\n" + output
}

func failoverResult(result TaskResult, attempts []ExecutorAttemptSnapshot, previousOutput string, err error) TaskResult {
	result.Attempts = cloneExecutorAttempts(attempts)
	result.Error = err
	result.Metadata = cloneStringMap(result.Metadata)
	if strings.TrimSpace(previousOutput) != "" {
		if result.Metadata == nil {
			result.Metadata = make(map[string]string, 1)
		}
		result.Metadata[ExecutorMetadataPreviousOutputKey] = previousOutput
	}
	return result
}
