package delegation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultExecutorProbeCacheTTL   = 30 * time.Second
	DefaultExecutorFailureCooldown = 30 * time.Second
)

type ExecutorRegistration struct {
	ID           ExecutorID
	DisplayName  string
	InstallURL   string
	Enabled      bool
	Priority     int
	Capabilities []ExecutorCapability
	Probe        func(context.Context) (ExecutorProbeResult, error)
	Execute      Executor
}

type ExecutorRegistryConfig struct {
	ProbeCacheTTL   time.Duration
	FailureCooldown time.Duration
	Now             func() time.Time
}

type ExecutorSnapshot struct {
	ID            ExecutorID
	DisplayName   string
	InstallURL    string
	Enabled       bool
	Priority      int
	Capabilities  []ExecutorCapability
	Probe         ExecutorProbeResult
	CooldownUntil time.Time
	FailureClass  ExecutorFailureClass
	FailureCode   string
}

type executorRegistryEntry struct {
	registration  ExecutorRegistration
	probe         ExecutorProbeResult
	probeErr      error
	hasProbe      bool
	probeInFlight chan struct{}
	cooldownUntil time.Time
	failureClass  ExecutorFailureClass
	failureCode   string
}

type ExecutorRegistry struct {
	mu              sync.RWMutex
	entries         map[ExecutorID]*executorRegistryEntry
	probeCacheTTL   time.Duration
	failureCooldown time.Duration
	now             func() time.Time
}

func NewExecutorRegistry(config ExecutorRegistryConfig) *ExecutorRegistry {
	probeCacheTTL := config.ProbeCacheTTL
	if probeCacheTTL <= 0 {
		probeCacheTTL = DefaultExecutorProbeCacheTTL
	}
	failureCooldown := config.FailureCooldown
	if failureCooldown <= 0 {
		failureCooldown = DefaultExecutorFailureCooldown
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ExecutorRegistry{
		entries:         make(map[ExecutorID]*executorRegistryEntry),
		probeCacheTTL:   probeCacheTTL,
		failureCooldown: failureCooldown,
		now:             now,
	}
}

func (registry *ExecutorRegistry) Register(registration ExecutorRegistration) error {
	if registry == nil {
		return fmt.Errorf("executor registry is nil")
	}
	var err error
	registration, err = normalizeExecutorRegistration(registration)
	if err != nil {
		return err
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[registration.ID]; exists {
		return fmt.Errorf("executor %q is already registered", registration.ID)
	}
	registry.entries[registration.ID] = &executorRegistryEntry{registration: registration}
	return nil
}

func (registry *ExecutorRegistry) Replace(registration ExecutorRegistration) error {
	if registry == nil {
		return fmt.Errorf("executor registry is nil")
	}
	normalized, err := normalizeExecutorRegistration(registration)
	if err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[normalized.ID]; !exists {
		return fmt.Errorf("executor %q is not registered", normalized.ID)
	}
	registry.entries[normalized.ID] = &executorRegistryEntry{registration: normalized}
	return nil
}

func (registry *ExecutorRegistry) Unregister(id ExecutorID) bool {
	if registry == nil {
		return false
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[id]; !exists {
		return false
	}
	delete(registry.entries, id)
	return true
}

func normalizeExecutorRegistration(registration ExecutorRegistration) (ExecutorRegistration, error) {
	registration.ID = ExecutorID(strings.TrimSpace(string(registration.ID)))
	if !validExecutorID(registration.ID) {
		return ExecutorRegistration{}, fmt.Errorf("executor id %q is invalid", registration.ID)
	}
	registration.DisplayName = strings.TrimSpace(registration.DisplayName)
	if registration.DisplayName == "" {
		registration.DisplayName = string(registration.ID)
	}
	registration.InstallURL = strings.TrimSpace(registration.InstallURL)
	if registration.Probe == nil {
		return ExecutorRegistration{}, fmt.Errorf("executor %q probe is required", registration.ID)
	}
	if registration.Execute == nil {
		return ExecutorRegistration{}, fmt.Errorf("executor %q execute function is required", registration.ID)
	}
	registration.Capabilities = normalizeExecutorCapabilities(registration.Capabilities)
	return registration, nil
}

func (registry *ExecutorRegistry) Probe(ctx context.Context, id ExecutorID, force bool) (ExecutorProbeResult, error) {
	if registry == nil {
		return ExecutorProbeResult{}, fmt.Errorf("executor registry is nil")
	}
	if ctx == nil {
		return ExecutorProbeResult{}, fmt.Errorf("executor probe context is nil")
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	registry.mu.Lock()
	entry, ok := registry.entries[id]
	if !ok {
		registry.mu.Unlock()
		return ExecutorProbeResult{}, fmt.Errorf("executor %q is not registered", id)
	}
	now := registry.now().UTC()
	if !force && entry.hasProbe && now.Sub(entry.probe.ProbedAt) < registry.probeCacheTTL {
		cached := cloneExecutorProbeResult(entry.probe)
		err := entry.probeErr
		registry.mu.Unlock()
		return cached, err
	}
	if entry.probeInFlight != nil {
		done := entry.probeInFlight
		registry.mu.Unlock()
		select {
		case <-ctx.Done():
			return ExecutorProbeResult{}, ctx.Err()
		case <-done:
		}
		registry.mu.RLock()
		current, exists := registry.entries[id]
		if !exists {
			registry.mu.RUnlock()
			return ExecutorProbeResult{}, fmt.Errorf("executor %q is not registered", id)
		}
		result := cloneExecutorProbeResult(current.probe)
		err := current.probeErr
		registry.mu.RUnlock()
		return result, err
	}
	probe := entry.registration.Probe
	registrationCapabilities := cloneExecutorCapabilities(entry.registration.Capabilities)
	done := make(chan struct{})
	entry.probeInFlight = done
	registry.mu.Unlock()

	result, err := probe(ctx)
	result = normalizeExecutorProbeResult(result, registrationCapabilities, registry.now().UTC(), err)

	registry.mu.Lock()
	if current, exists := registry.entries[id]; exists && current == entry {
		current.probe = cloneExecutorProbeResult(result)
		current.probeErr = err
		current.hasProbe = true
		current.probeInFlight = nil
	} else if exists {
		result = cloneExecutorProbeResult(current.probe)
		if !current.hasProbe {
			result.State = ExecutorProbeUnknown
		}
		err = current.probeErr
	}
	close(done)
	registry.mu.Unlock()
	return cloneExecutorProbeResult(result), err
}

func (registry *ExecutorRegistry) Snapshot(id ExecutorID) (ExecutorSnapshot, bool) {
	if registry == nil {
		return ExecutorSnapshot{}, false
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	entry, ok := registry.entries[id]
	if !ok {
		return ExecutorSnapshot{}, false
	}
	return snapshotExecutorEntry(entry), true
}

func (registry *ExecutorRegistry) Snapshots() []ExecutorSnapshot {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	items := make([]ExecutorSnapshot, 0, len(registry.entries))
	for _, entry := range registry.entries {
		items = append(items, snapshotExecutorEntry(entry))
	}
	registry.mu.RUnlock()
	sortExecutorSnapshots(items)
	return items
}

func (registry *ExecutorRegistry) Eligible(required []ExecutorCapability) []ExecutorSnapshot {
	if registry == nil {
		return nil
	}
	required = normalizeExecutorCapabilities(required)
	now := registry.now().UTC()
	registry.mu.RLock()
	items := make([]ExecutorSnapshot, 0, len(registry.entries))
	for _, entry := range registry.entries {
		if entry == nil || !entry.registration.Enabled || !entry.hasProbe || entry.probe.State != ExecutorProbeReady {
			continue
		}
		if entry.cooldownUntil.After(now) {
			continue
		}
		capabilities := entry.probe.Capabilities
		if len(capabilities) == 0 {
			capabilities = entry.registration.Capabilities
		}
		if !executorCapabilitiesContain(capabilities, required) {
			continue
		}
		items = append(items, snapshotExecutorEntry(entry))
	}
	registry.mu.RUnlock()
	sortExecutorSnapshots(items)
	return items
}

// Candidates 返回可被自动故障转移考虑的执行器。除已就绪项外，也保留尚未探测、
// 但静态配置满足能力要求的项；调用方必须在实际执行前完成探测，不能把 unknown
// 当作可运行状态。这样刚保存的执行器配置无需等待设置页手工刷新才会参与首个任务。
func (registry *ExecutorRegistry) Candidates(required []ExecutorCapability) []ExecutorSnapshot {
	if registry == nil {
		return nil
	}
	required = normalizeExecutorCapabilities(required)
	now := registry.now().UTC()
	registry.mu.RLock()
	items := make([]ExecutorSnapshot, 0, len(registry.entries))
	for _, entry := range registry.entries {
		if entry == nil || !entry.registration.Enabled || entry.cooldownUntil.After(now) {
			continue
		}
		capabilities := entry.registration.Capabilities
		if entry.hasProbe {
			if entry.probe.State != ExecutorProbeReady {
				continue
			}
			if len(entry.probe.Capabilities) > 0 {
				capabilities = entry.probe.Capabilities
			}
		}
		if !executorCapabilitiesContain(capabilities, required) {
			continue
		}
		items = append(items, snapshotExecutorEntry(entry))
	}
	registry.mu.RUnlock()
	sortExecutorSnapshots(items)
	return items
}

func (registry *ExecutorRegistry) RecordFailure(id ExecutorID, err error) {
	if registry == nil || err == nil {
		return
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	class, _ := ExecutorErrorClassification(err)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[id]
	if !ok {
		return
	}
	entry.failureClass = class
	entry.failureCode = executorErrorCode(err)
	if class == ExecutorFailureSwitchable {
		entry.cooldownUntil = registry.now().UTC().Add(registry.failureCooldown)
	} else {
		entry.cooldownUntil = time.Time{}
	}
}

func (registry *ExecutorRegistry) RecordSuccess(id ExecutorID) {
	if registry == nil {
		return
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, ok := registry.entries[id]
	if !ok {
		return
	}
	entry.cooldownUntil = time.Time{}
	entry.failureClass = ""
	entry.failureCode = ""
}

func (registry *ExecutorRegistry) Executor(id ExecutorID) (Executor, error) {
	if registry == nil {
		return nil, fmt.Errorf("executor registry is nil")
	}
	id = ExecutorID(strings.TrimSpace(string(id)))
	registry.mu.RLock()
	entry, ok := registry.entries[id]
	if !ok {
		registry.mu.RUnlock()
		return nil, fmt.Errorf("executor %q is not registered", id)
	}
	execute := entry.registration.Execute
	registry.mu.RUnlock()
	return func(ctx context.Context, request TaskRequest) TaskResult {
		startedAt := registry.now().UTC()
		publishExecutorAttempt(ctx, ExecutorAttemptSnapshot{
			ExecutorID: id, Status: ExecutorAttemptRunning, StartedAt: startedAt,
		})
		result := execute(ctx, request)
		finishedAt := registry.now().UTC()
		attempt := ExecutorAttemptSnapshot{
			ExecutorID: id,
			Attempt:    len(result.Attempts) + 1,
			Status:     ExecutorAttemptCompleted,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		}
		if result.Error != nil {
			attempt.Status = executorAttemptStatusForError(ctx, result.Error)
			attempt.FailureClass, attempt.RetrySafe = ExecutorErrorClassification(result.Error)
			attempt.DiagnosticCode = executorErrorCode(result.Error)
			attempt.Error = result.Error.Error()
			registry.RecordFailure(id, result.Error)
		} else {
			registry.RecordSuccess(id)
		}
		result.ExecutorID = id
		result.Attempts = append(cloneExecutorAttempts(result.Attempts), attempt)
		publishExecutorAttempt(ctx, attempt)
		result.Metadata = cloneStringMap(result.Metadata)
		if result.Metadata == nil {
			result.Metadata = make(map[string]string, 1)
		}
		result.Metadata[ExecutorMetadataIDKey] = string(id)
		return result
	}, nil
}

func snapshotExecutorEntry(entry *executorRegistryEntry) ExecutorSnapshot {
	if entry == nil {
		return ExecutorSnapshot{}
	}
	probe := cloneExecutorProbeResult(entry.probe)
	if !entry.hasProbe {
		probe.State = ExecutorProbeUnknown
	}
	capabilities := entry.probe.Capabilities
	if len(capabilities) == 0 {
		capabilities = entry.registration.Capabilities
	}
	return ExecutorSnapshot{
		ID:            entry.registration.ID,
		DisplayName:   entry.registration.DisplayName,
		InstallURL:    entry.registration.InstallURL,
		Enabled:       entry.registration.Enabled,
		Priority:      entry.registration.Priority,
		Capabilities:  cloneExecutorCapabilities(capabilities),
		Probe:         probe,
		CooldownUntil: entry.cooldownUntil,
		FailureClass:  entry.failureClass,
		FailureCode:   entry.failureCode,
	}
}

func normalizeExecutorProbeResult(result ExecutorProbeResult, fallbackCapabilities []ExecutorCapability, probedAt time.Time, probeErr error) ExecutorProbeResult {
	if result.State == "" {
		if probeErr != nil {
			result.State = ExecutorProbeUnhealthy
		} else {
			result.State = ExecutorProbeReady
		}
	}
	if len(result.Capabilities) == 0 {
		result.Capabilities = fallbackCapabilities
	}
	result.Capabilities = normalizeExecutorCapabilities(result.Capabilities)
	result.ExecutablePath = strings.TrimSpace(result.ExecutablePath)
	result.Version = strings.TrimSpace(result.Version)
	result.DiagnosticCode = strings.TrimSpace(result.DiagnosticCode)
	result.DiagnosticText = strings.TrimSpace(result.DiagnosticText)
	result.ProbedAt = probedAt.UTC()
	if result.State == ExecutorProbeReady {
		result.Installed = true
		if result.AuthState == "" {
			result.AuthState = ExecutorAuthReady
		}
	} else if result.AuthState == "" {
		result.AuthState = ExecutorAuthUnknown
	}
	return result
}

func normalizeExecutorCapabilities(capabilities []ExecutorCapability) []ExecutorCapability {
	if len(capabilities) == 0 {
		return nil
	}
	seen := make(map[ExecutorCapability]struct{}, len(capabilities))
	result := make([]ExecutorCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = ExecutorCapability(strings.TrimSpace(string(capability)))
		if capability == "" {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func executorCapabilitiesContain(available, required []ExecutorCapability) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[ExecutorCapability]struct{}, len(available))
	for _, capability := range available {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, exists := set[capability]; !exists {
			return false
		}
	}
	return true
}

func sortExecutorSnapshots(items []ExecutorSnapshot) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}

func validExecutorID(id ExecutorID) bool {
	value := string(id)
	if value == "" {
		return false
	}
	for index, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.'
		if !valid || index == 0 && (char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func executorAttemptStatusForError(ctx context.Context, err error) ExecutorAttemptStatus {
	if ctx != nil {
		switch ctx.Err() {
		case context.Canceled:
			return ExecutorAttemptCanceled
		case context.DeadlineExceeded:
			return ExecutorAttemptTimedOut
		}
	}
	if errors.Is(err, context.Canceled) {
		return ExecutorAttemptCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ExecutorAttemptTimedOut
	}
	return ExecutorAttemptFailed
}
