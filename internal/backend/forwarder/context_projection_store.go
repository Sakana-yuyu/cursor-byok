package forwarder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const contextProjectionFileName = "context-projection.json"

func (store *ConversationFileStore) LoadContextProjection(conversationID string) (*contextProjectionState, error) {
	if store == nil {
		return nil, fmt.Errorf("conversation file store is nil")
	}
	normalizedConversationID, err := validateConversationID(conversationID)
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(store.contextProjectionPath(normalizedConversationID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read context projection: %w", err)
	}
	var state contextProjectionState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, fmt.Errorf("decode context projection: %w", err)
	}
	return &state, nil
}

func (store *ConversationFileStore) SaveContextProjection(conversationID string, state *contextProjectionState) error {
	if store == nil {
		return fmt.Errorf("conversation file store is nil")
	}
	if state == nil {
		return fmt.Errorf("context projection is required")
	}
	normalizedConversationID, err := validateConversationID(conversationID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.ConversationID) != normalizedConversationID {
		return fmt.Errorf("context projection conversation mismatch")
	}
	if err := os.MkdirAll(store.conversationDir(normalizedConversationID), 0o755); err != nil {
		return fmt.Errorf("create conversation directory: %w", err)
	}
	release, err := acquireConversationLock(store.lockPath(normalizedConversationID))
	if err != nil {
		return err
	}
	defer release()
	return writeJSONFileAtomic(store.contextProjectionPath(normalizedConversationID), state)
}

func (store *ConversationFileStore) contextProjectionPath(conversationID string) string {
	return filepath.Join(store.conversationDir(conversationID), contextProjectionFileName)
}
