package configprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	serverconfig "cursor/internal/backend/server/config"
	"cursor/internal/controlcenter"

	"github.com/google/uuid"
)

const (
	schemaVersion = 1
	maxJSONBytes  = 1 << 20
)

var allowedDomains = map[string]struct{}{
	"models": {}, "model_groups": {}, "routing": {}, "delegation": {},
	"skills_mcp": {}, "computer_use": {}, "appearance": {},
}

type Summary struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Domains         []string `json:"domains"`
	CreatedAtUnixMS int64    `json:"createdAtUnixMs"`
	UpdatedAtUnixMS int64    `json:"updatedAtUnixMs"`
}

type SaveRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Domains     []string `json:"domains"`
}

type Change struct {
	Path       string `json:"path"`
	ChangeKind string `json:"changeKind"`
	Sensitive  bool   `json:"sensitive"`
}

type CredentialBinding struct {
	AdapterID string `json:"adapterId"`
	State     string `json:"state"`
}

type Preview struct {
	Profile  Summary             `json:"profile"`
	Changes  []Change            `json:"changes"`
	Bindings []CredentialBinding `json:"bindings"`
	CanApply bool                `json:"canApply"`
}

type ApplyPreparation struct {
	controlcenter.PreparedOperation
	Preview Preview `json:"preview"`
}

type storedProfile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Summary       Summary         `json:"summary"`
	Payload       json.RawMessage `json:"payload"`
}

type Store struct {
	root string
	mu   sync.Mutex
	ops  map[string]pendingApply
}

type pendingApply struct {
	token     string
	expiresAt time.Time
	used      bool
	profileID string
}

func New(root string) *Store {
	return &Store{root: root, ops: map[string]pendingApply{}}
}

func (store *Store) List() ([]Summary, error) {
	profiles, err := store.loadAll()
	if err != nil {
		return nil, err
	}
	items := make([]Summary, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, profile.Summary)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAtUnixMS > items[j].UpdatedAtUnixMS })
	if items == nil {
		items = []Summary{}
	}
	return items, nil
}

func (store *Store) SaveCurrent(name, description string, domains []string, cfg serverconfig.Config) (Summary, error) {
	summary, err := validateMeta(name, description, domains)
	if err != nil {
		return Summary{}, err
	}
	now := time.Now().UnixMilli()
	summary.ID = uuid.NewString()
	summary.CreatedAtUnixMS = now
	summary.UpdatedAtUnixMS = now
	payload, err := extractPayload(cfg, summary.Domains)
	if err != nil {
		return Summary{}, err
	}
	if err := store.write(storedProfile{SchemaVersion: schemaVersion, Summary: summary, Payload: payload}); err != nil {
		return Summary{}, controlcenter.WrapError("profile_save_failed", "save profile failed", err)
	}
	return summary, nil
}

func (store *Store) Delete(profileID string) (controlcenter.OperationResult, error) {
	profile, err := store.load(profileID)
	if err != nil {
		return controlcenter.OperationResult{}, err
	}
	path := store.profilePath(profile.Summary.ID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return controlcenter.OperationResult{}, err
	}
	return controlcenter.OperationResult{OperationID: profile.Summary.ID, State: "succeeded", FinishedAtUnixMS: time.Now().UnixMilli()}, nil
}

func (store *Store) Preview(profileID string, current serverconfig.Config) (Preview, error) {
	profile, err := store.load(profileID)
	if err != nil {
		return Preview{}, err
	}
	if profile.SchemaVersion != schemaVersion {
		return Preview{Profile: profile.Summary, CanApply: false}, controlcenter.NewError("profile_schema_unsupported", "profile schema is unsupported")
	}
	incoming, err := decodePayload(profile.Payload)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{
		Profile:  profile.Summary,
		Changes:  diffPayload(current, incoming, profile.Summary.Domains),
		Bindings: resolveBindings(current, incoming),
		CanApply: true,
	}
	for _, binding := range preview.Bindings {
		if binding.State != "resolved" {
			preview.CanApply = false
		}
	}
	return preview, nil
}

func (store *Store) PrepareApply(profileID string, current serverconfig.Config) (ApplyPreparation, error) {
	preview, err := store.Preview(profileID, current)
	if err != nil {
		return ApplyPreparation{}, err
	}
	if !preview.CanApply {
		return ApplyPreparation{}, controlcenter.NewError("profile_binding_missing", "credential binding is missing or ambiguous")
	}
	token := uuid.NewString()
	store.mu.Lock()
	store.ops[token] = pendingApply{token: token, expiresAt: time.Now().Add(60 * time.Second), profileID: profileID}
	store.mu.Unlock()
	return ApplyPreparation{
		PreparedOperation: controlcenter.PreparedOperation{
			OperationID:       "apply-" + profileID,
			ConfirmationToken: token,
			ExpiresAtUnixMS:   time.Now().Add(60 * time.Second).UnixMilli(),
			ImpactCodes:       []string{"config_rewrite"},
			RollbackAvailable: true,
		},
		Preview: preview,
	}, nil
}

func (store *Store) ExecuteApply(confirmationToken string, current serverconfig.Config, persist func(serverconfig.Config) (serverconfig.Config, error)) (controlcenter.OperationResult, error) {
	store.mu.Lock()
	pending, ok := store.ops[strings.TrimSpace(confirmationToken)]
	if !ok || time.Now().After(pending.expiresAt) {
		store.mu.Unlock()
		return controlcenter.OperationResult{}, controlcenter.NewError("confirmation_expired", "confirmation expired")
	}
	if pending.used {
		store.mu.Unlock()
		return controlcenter.OperationResult{}, controlcenter.NewError("confirmation_already_used", "confirmation already used")
	}
	pending.used = true
	store.ops[pending.token] = pending
	store.mu.Unlock()
	profile, err := store.load(pending.profileID)
	if err != nil {
		return controlcenter.OperationResult{}, err
	}
	incoming, err := decodePayload(profile.Payload)
	if err != nil {
		return controlcenter.OperationResult{}, err
	}
	backupDir := filepath.Join(store.root, "backups", pending.token)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("profile_backup_failed", "create backup failed")
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("profile_backup_failed", "marshal backup failed")
	}
	if err := os.WriteFile(filepath.Join(backupDir, "config.snapshot"), raw, 0o600); err != nil {
		return controlcenter.OperationResult{}, controlcenter.NewError("profile_backup_failed", "write backup failed")
	}
	merged := mergeConfig(current, incoming, profile.Summary.Domains)
	normalized, err := persist(merged)
	if err != nil {
		_, _ = persist(current)
		return controlcenter.OperationResult{OperationID: pending.token, State: "rolled_back", ErrorCode: "profile_apply_failed", RollbackState: "succeeded", FinishedAtUnixMS: time.Now().UnixMilli()}, controlcenter.NewError("profile_apply_failed", "apply failed")
	}
	_ = normalized
	store.cleanupBackups()
	return controlcenter.OperationResult{OperationID: pending.token, State: "succeeded", RollbackState: "available", FinishedAtUnixMS: time.Now().UnixMilli()}, nil
}

func (store *Store) Export(profileID string) (controlcenter.SanitizedExport, error) {
	profile, err := store.load(profileID)
	if err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	payload, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	if containsSecret(payload) {
		return controlcenter.SanitizedExport{}, controlcenter.NewError("profile_export_failed", "profile contains secrets")
	}
	dir := filepath.Join(store.root, "exports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	name := "profile-" + profile.Summary.ID + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return controlcenter.SanitizedExport{}, err
	}
	sum := sha256.Sum256(payload)
	return controlcenter.SanitizedExport{Path: name, SHA256: hex.EncodeToString(sum[:])}, nil
}

func (store *Store) Import(content string) (Preview, error) {
	if len(content) > maxJSONBytes {
		return Preview{}, controlcenter.NewError("profile_import_too_large", "import json exceeds 1 mib")
	}
	var profile storedProfile
	if err := json.Unmarshal([]byte(content), &profile); err != nil {
		return Preview{}, controlcenter.NewError("profile_import_invalid_schema", "import json is invalid")
	}
	if profile.SchemaVersion != schemaVersion {
		return Preview{Profile: profile.Summary, CanApply: false}, controlcenter.NewError("profile_schema_unsupported", "profile schema is unsupported")
	}
	if containsSecret([]byte(content)) {
		return Preview{}, controlcenter.NewError("profile_import_invalid_schema", "import contains secrets")
	}
	if profile.Summary.ID == "" {
		profile.Summary.ID = uuid.NewString()
	}
	now := time.Now().UnixMilli()
	profile.Summary.CreatedAtUnixMS = now
	profile.Summary.UpdatedAtUnixMS = now
	if err := store.write(profile); err != nil {
		return Preview{}, err
	}
	return Preview{Profile: profile.Summary, CanApply: false}, nil
}

func (store *Store) Count() int {
	items, _ := store.List()
	return len(items)
}

func (store *Store) write(profile storedProfile) error {
	if err := os.MkdirAll(filepath.Join(store.root, "profiles"), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	path := store.profilePath(profile.Summary.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (store *Store) load(id string) (storedProfile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return storedProfile{}, controlcenter.NewError("profile_not_found", "profile not found")
	}
	raw, err := os.ReadFile(store.profilePath(id))
	if err != nil {
		return storedProfile{}, controlcenter.NewError("profile_not_found", "profile not found")
	}
	var profile storedProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return storedProfile{}, controlcenter.NewError("profile_store_unreadable", "profile is unreadable")
	}
	return profile, nil
}

func (store *Store) loadAll() ([]storedProfile, error) {
	dir := filepath.Join(store.root, "profiles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, controlcenter.NewError("profile_store_unreadable", "profile store is unreadable")
	}
	items := make([]storedProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var profile storedProfile
		if json.Unmarshal(raw, &profile) == nil {
			items = append(items, profile)
		}
	}
	return items, nil
}

func (store *Store) profilePath(id string) string {
	return filepath.Join(store.root, "profiles", id+".json")
}

func (store *Store) cleanupBackups() {
	dir := filepath.Join(store.root, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= 10 {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, entry := range entries[10:] {
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
}

func validateMeta(name, description string, domains []string) (Summary, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return Summary{}, controlcenter.NewError("profile_name_invalid", "profile name is invalid")
	}
	if utf8.RuneCountInString(description) > 500 {
		return Summary{}, controlcenter.NewError("profile_name_invalid", "profile description is too long")
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if _, ok := allowedDomains[domain]; !ok {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		clean = append(clean, domain)
	}
	if len(clean) == 0 {
		return Summary{}, controlcenter.NewError("profile_name_invalid", "domains are invalid")
	}
	sort.Strings(clean)
	return Summary{Name: name, Description: description, Domains: clean}, nil
}

type profilePayload struct {
	Adapters    []map[string]any                 `json:"adapters,omitempty"`
	Routing     *serverconfig.RoutingConfig      `json:"routing,omitempty"`
	Delegation  *serverconfig.DelegationConfig   `json:"delegation,omitempty"`
	SkillMCP    *serverconfig.SkillMCPScanConfig `json:"skillMcpScan,omitempty"`
	ComputerUse *serverconfig.ComputerUseConfig  `json:"computerUse,omitempty"`
	HomeMetrics *serverconfig.HomeMetricsConfig  `json:"homeMetrics,omitempty"`
}

func extractPayload(cfg serverconfig.Config, domains []string) (json.RawMessage, error) {
	payload := profilePayload{}
	wanted := map[string]struct{}{}
	for _, domain := range domains {
		wanted[domain] = struct{}{}
	}
	if _, ok := wanted["models"]; ok {
		payload.Adapters = sanitizeAdapters(cfg.ModelAdapters)
	}
	if _, ok := wanted["model_groups"]; ok && payload.Adapters == nil {
		payload.Adapters = sanitizeAdapters(cfg.ModelAdapters)
	}
	if _, ok := wanted["routing"]; ok {
		routing := cfg.Routing
		payload.Routing = &routing
	}
	if _, ok := wanted["delegation"]; ok {
		delegation := cfg.Delegation
		payload.Delegation = &delegation
	}
	if _, ok := wanted["skills_mcp"]; ok {
		scan := cfg.SkillMCPScan
		payload.SkillMCP = &scan
	}
	if _, ok := wanted["computer_use"]; ok {
		use := cfg.ComputerUse
		payload.ComputerUse = &use
	}
	if _, ok := wanted["appearance"]; ok {
		metrics := cfg.HomeMetrics
		payload.HomeMetrics = &metrics
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if containsSecret(raw) {
		return nil, controlcenter.NewError("profile_save_failed", "extracted profile contains secrets")
	}
	return raw, nil
}

func sanitizeAdapters(adapters []serverconfig.ModelAdapterConfig) []map[string]any {
	items := make([]map[string]any, 0, len(adapters))
	for _, adapter := range adapters {
		items = append(items, map[string]any{
			"id":                      adapter.ID,
			"displayName":             adapter.DisplayName,
			"groupName":               adapter.GroupName,
			"type":                    adapter.Type,
			"supplierID":              adapter.SupplierID,
			"protocolMode":            adapter.ProtocolMode,
			"baseURL":                 adapter.BaseURL,
			"modelID":                 adapter.ModelID,
			"reasoningEffort":         adapter.ReasoningEffort,
			"openAIEndpoint":          adapter.OpenAIEndpoint,
			"contextWindowTokens":     adapter.ContextWindowTokens,
			"maxCompletionTokens":     adapter.MaxCompletionTokens,
			"anthropicMaxTokens":      adapter.AnthropicMaxTokens,
			"anthropicThinkingEffort": adapter.AnthropicThinkingEffort,
			"thinkingBudgetTokens":    adapter.ThinkingBudgetTokens,
			"fastMode":                adapter.FastMode,
			"openAIServiceTier":       adapter.OpenAIServiceTier,
			"customHeadersEnabled":    adapter.CustomHeadersEnabled,
			"pricing":                 adapter.Pricing,
		})
	}
	return items
}

func decodePayload(raw json.RawMessage) (profilePayload, error) {
	var payload profilePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return profilePayload{}, err
	}
	return payload, nil
}

func resolveBindings(current serverconfig.Config, incoming profilePayload) []CredentialBinding {
	if len(incoming.Adapters) == 0 {
		return nil
	}
	counts := map[string]int{}
	keys := map[string]bool{}
	for _, adapter := range current.ModelAdapters {
		id := strings.TrimSpace(adapter.ID)
		if id == "" {
			continue
		}
		counts[id]++
		keys[id] = strings.TrimSpace(adapter.APIKey) != ""
	}
	bindings := make([]CredentialBinding, 0, len(incoming.Adapters))
	for _, adapter := range incoming.Adapters {
		id, _ := adapter["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			bindings = append(bindings, CredentialBinding{AdapterID: "unknown", State: "missing"})
			continue
		}
		state := "resolved"
		if counts[id] == 0 || !keys[id] {
			state = "missing"
		} else if counts[id] > 1 {
			state = "ambiguous"
		}
		bindings = append(bindings, CredentialBinding{AdapterID: id, State: state})
	}
	return bindings
}

func diffPayload(current serverconfig.Config, incoming profilePayload, domains []string) []Change {
	changes := make([]Change, 0)
	for _, domain := range domains {
		changes = append(changes, Change{Path: "/" + domain, ChangeKind: "update", Sensitive: false})
	}
	if incoming.Routing != nil && incoming.Routing.Mode != current.Routing.Mode {
		changes = append(changes, Change{Path: "/routing/mode", ChangeKind: "update", Sensitive: false})
	}
	return changes
}

func mergeConfig(current serverconfig.Config, incoming profilePayload, domains []string) serverconfig.Config {
	wanted := map[string]struct{}{}
	for _, domain := range domains {
		wanted[domain] = struct{}{}
	}
	if incoming.Routing != nil {
		if _, ok := wanted["routing"]; ok {
			current.Routing.Mode = incoming.Routing.Mode
			current.Routing.Policy = incoming.Routing.Policy
		}
	}
	if incoming.Delegation != nil {
		if _, ok := wanted["delegation"]; ok {
			current.Delegation = *incoming.Delegation
		}
	}
	if incoming.SkillMCP != nil {
		if _, ok := wanted["skills_mcp"]; ok {
			current.SkillMCPScan = *incoming.SkillMCP
		}
	}
	if incoming.ComputerUse != nil {
		if _, ok := wanted["computer_use"]; ok {
			current.ComputerUse = *incoming.ComputerUse
		}
	}
	if incoming.HomeMetrics != nil {
		if _, ok := wanted["appearance"]; ok {
			current.HomeMetrics = *incoming.HomeMetrics
		}
	}
	if incoming.Adapters != nil {
		if _, ok := wanted["models"]; ok {
			byID := map[string]map[string]any{}
			for _, adapter := range incoming.Adapters {
				id, _ := adapter["id"].(string)
				byID[strings.TrimSpace(id)] = adapter
			}
			for i := range current.ModelAdapters {
				id := strings.TrimSpace(current.ModelAdapters[i].ID)
				patch, ok := byID[id]
				if !ok {
					continue
				}
				if value, ok := patch["displayName"].(string); ok {
					current.ModelAdapters[i].DisplayName = value
				}
				if value, ok := patch["groupName"].(string); ok {
					current.ModelAdapters[i].GroupName = value
				}
				if value, ok := patch["modelID"].(string); ok {
					current.ModelAdapters[i].ModelID = value
				}
			}
		}
	}
	return current
}

func containsSecret(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"apikey", "access_token", "refresh_token", "cookie", "authorization", "balanceaccesstoken"} {
		if strings.Contains(strings.ReplaceAll(lower, "\"", ""), banned) && strings.Contains(lower, `"`+banned) {
			return true
		}
	}
	return strings.Contains(lower, `"apiKey"`) || strings.Contains(lower, `"api_key"`) || strings.Contains(lower, `"balanceAccessToken"`)
}
