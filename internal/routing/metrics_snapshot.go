package routing

import "sync"

// MetricsSnapshot stores credential-free routing metrics keyed by channel ID.
type MetricsSnapshot struct {
	mu   sync.RWMutex
	byID map[string]CandidateInput
}

func NewMetricsSnapshot() *MetricsSnapshot {
	return &MetricsSnapshot{byID: make(map[string]CandidateInput)}
}

func (snapshot *MetricsSnapshot) Set(channelID string, candidate CandidateInput) {
	if snapshot == nil || channelID == "" {
		return
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.byID == nil {
		snapshot.byID = make(map[string]CandidateInput)
	}
	snapshot.byID[channelID] = candidate
}

func (snapshot *MetricsSnapshot) Get(channelID string) (CandidateInput, bool) {
	if snapshot == nil || channelID == "" {
		return CandidateInput{}, false
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	candidate, ok := snapshot.byID[channelID]
	return candidate, ok
}
