package forwarder

import (
	"strings"
	"time"
)

const checkpointPersistDebounce = 250 * time.Millisecond

func maxEntrySeq(entries []HistoryEntry) int64 {
	var maxSeq int64
	for _, entry := range entries {
		if entry.Seq > maxSeq {
			maxSeq = entry.Seq
		}
	}
	return maxSeq
}

func syncCheckpointPersistBaseline(stream *ActiveStream, conversation *ConversationFile) {
	if stream == nil {
		return
	}
	stream.CheckpointLastPersistedEntrySeq = maxEntrySeq(conversationEntries(conversation))
}

func conversationEntries(conversation *ConversationFile) []HistoryEntry {
	if conversation == nil {
		return nil
	}
	return conversation.Entries
}

func (service *Service) scheduleCheckpointPersist(stream *ActiveStream, conversationID string) {
	if service == nil || service.store == nil || stream == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	stream.mu.Lock()
	if stream.CheckpointPersistTimer != nil {
		stream.CheckpointPersistTimer.Stop()
	}
	conversationIDCopy := conversationID
	stream.CheckpointPersistTimer = time.AfterFunc(checkpointPersistDebounce, func() {
		_ = service.persistCheckpointDelta(stream, conversationIDCopy)
	})
	stream.mu.Unlock()
}

func (service *Service) flushCheckpointPersistSync(stream *ActiveStream, conversationID string) error {
	if service == nil || service.store == nil || stream == nil {
		return nil
	}
	stream.mu.Lock()
	if stream.CheckpointPersistTimer != nil {
		stream.CheckpointPersistTimer.Stop()
		stream.CheckpointPersistTimer = nil
	}
	stream.mu.Unlock()
	return service.persistCheckpointDelta(stream, conversationID)
}

func (service *Service) persistCheckpointDelta(stream *ActiveStream, conversationID string) error {
	if service == nil || service.store == nil || stream == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}

	stream.mu.Lock()
	if stream.CheckpointConversation == nil {
		stream.mu.Unlock()
		return nil
	}
	conversation := cloneConversationFile(stream.CheckpointConversation)
	lastPersisted := stream.CheckpointLastPersistedEntrySeq
	var pending []HistoryEntry
	for _, entry := range conversation.Entries {
		if entry.Seq > lastPersisted {
			pending = append(pending, entry)
		}
	}
	stream.mu.Unlock()

	if len(pending) > 0 {
		persisted, assigned, err := service.store.AppendEntries(conversationID, resetEntrySequences(pending))
		if err != nil {
			return err
		}
		stream.mu.Lock()
		if stream.CheckpointConversation != nil && persisted != nil {
			if len(stream.CheckpointConversation.Entries) >= len(persisted.Entries) {
				mergeConversationMetadata(stream.CheckpointConversation, persisted)
			} else {
				stream.CheckpointConversation = cloneConversationFile(persisted)
			}
		}
		stream.CheckpointLastPersistedEntrySeq = maxEntrySeq(assigned)
		if stream.CheckpointPersistTimer != nil {
			stream.CheckpointPersistTimer.Stop()
			stream.CheckpointPersistTimer = nil
		}
		stream.mu.Unlock()
		conversation = cloneConversationFile(stream.CheckpointConversation)
	}

	if conversation == nil {
		return nil
	}
	if err := service.syncConversationRecord(conversationID, conversation); err != nil {
		return err
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.CheckpointPersistTimer != nil {
		stream.CheckpointPersistTimer.Stop()
		stream.CheckpointPersistTimer = nil
	}
	if len(pending) == 0 {
		stream.CheckpointLastPersistedEntrySeq = maxEntrySeq(conversationEntries(stream.CheckpointConversation))
	}
	return nil
}
