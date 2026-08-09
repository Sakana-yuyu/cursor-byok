package forwarder

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"cursor/gen/agentv1"
)

type disabledAssetScanConfig struct{}

func (disabledAssetScanConfig) SkillMCPScanEnabled() bool                       { return false }
func (disabledAssetScanConfig) SkillMCPScanSkillSources() map[string]bool       { return nil }
func (disabledAssetScanConfig) SkillMCPScanMCPSources() map[string]bool         { return nil }
func (disabledAssetScanConfig) SkillMCPScanEnabledSkills() map[string]bool      { return nil }
func (disabledAssetScanConfig) SkillMCPScanDisabledMCPServers() map[string]bool { return nil }

func TestAssetEnrichmentUpdatesRecentWorkspaceRootConcurrently(t *testing.T) {
	historyRoot := t.TempDir()
	store := NewConversationFileStore(historyRoot)
	conversation := testConversation([]HistoryEntry{
		testUserMessageEntry(t, 1, "request-before-workspace-root", "unchanged history"),
	})
	conversation.ConversationID = "workspace-root-history"
	conversation.RootConversationID = conversation.ConversationID
	if _, err := store.SaveConversationWithEntries(conversation.ConversationID, conversation, conversation.Entries); err != nil {
		t.Fatalf("SaveConversationWithEntries() error = %v", err)
	}
	statePath := filepath.Join(historyRoot, conversation.ConversationID, "state.json")
	contextPath := filepath.Join(historyRoot, conversation.ConversationID, "context.json")
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile(state.json) error = %v", err)
	}
	contextBefore, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("ReadFile(context.json) error = %v", err)
	}
	replayBefore, err := NewHistoryProjector().ProjectPromptReplay(conversation)
	if err != nil {
		t.Fatalf("ProjectPromptReplay(before) error = %v", err)
	}

	service := &Service{store: store, scanConfig: disabledAssetScanConfig{}}

	const workers = 64
	var wait sync.WaitGroup
	workspaceRoots := make(map[string]struct{}, workers)
	wait.Add(workers)
	for index := 0; index < workers; index++ {
		workspaceRoot := filepath.Join(t.TempDir(), fmt.Sprintf("workspace-%d", index))
		workspaceRoots[workspaceRoot] = struct{}{}
		go func(root string) {
			defer wait.Done()
			intent := &InboundIntent{
				RequestContext: &agentv1.RequestContext{
					Env: &agentv1.RequestContextEnv{ProjectFolder: "  " + root + "  "},
				},
			}
			service.enrichRequestContextWithScannedAssets(intent)
		}(workspaceRoot)
	}
	wait.Wait()

	if got := service.RecentWorkspaceRoot(); got == "" {
		t.Fatal("RecentWorkspaceRoot() is empty after enrichment")
	} else if _, ok := workspaceRoots[got]; !ok {
		t.Fatalf("RecentWorkspaceRoot() = %q, want one normalized input root", got)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("ReadFile(state.json after enrichment) error = %v", err)
	}
	contextAfter, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("ReadFile(context.json after enrichment) error = %v", err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("recent workspace tracking modified state.json")
	}
	if !bytes.Equal(contextBefore, contextAfter) {
		t.Fatal("recent workspace tracking modified context.json")
	}
	reloaded, err := store.LoadConversation(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadConversation() error = %v", err)
	}
	replayAfter, err := NewHistoryProjector().ProjectPromptReplay(reloaded)
	if err != nil {
		t.Fatalf("ProjectPromptReplay(after) error = %v", err)
	}
	if !reflect.DeepEqual(replayBefore, replayAfter) {
		t.Fatalf("recent workspace tracking changed prompt replay: before=%#v after=%#v", replayBefore, replayAfter)
	}
}
