package requestlab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cursor/internal/controlcenter"
)

const (
	kindOfficialMirror = "official_mirror"
	kindLocalProvider  = "local_provider"
	defaultLimit       = 50
	minLimit           = 1
	maxLimit           = 200
)

type RequestSourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type RequestSourceQuery struct {
	Kind       string `json:"kind"`
	Model      string `json:"model,omitempty"`
	Status     string `json:"status,omitempty"`
	FromUnixMS int64  `json:"fromUnixMs,omitempty"`
	ToUnixMS   int64  `json:"toUnixMs,omitempty"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}

type RequestSourceSummary struct {
	Ref             RequestSourceRef `json:"ref"`
	TimestampUnixMS int64            `json:"timestampUnixMs"`
	Model           string           `json:"model,omitempty"`
	Provider        string           `json:"provider,omitempty"`
	Protocol        string           `json:"protocol,omitempty"`
	Status          string           `json:"status,omitempty"`
	ShapeAvailable  bool             `json:"shapeAvailable"`
	Truncated       bool             `json:"truncated,omitempty"`
}

type RequestSourcePage struct {
	Items      []RequestSourceSummary `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

type RequestComparisonRequest struct {
	Left  RequestSourceRef `json:"left"`
	Right RequestSourceRef `json:"right"`
}

type RequestFieldDiff struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	LeftType     string `json:"leftType,omitempty"`
	RightType    string `json:"rightType,omitempty"`
	LeftSummary  string `json:"leftSummary,omitempty"`
	RightSummary string `json:"rightSummary,omitempty"`
	Sensitive    bool   `json:"sensitive,omitempty"`
}

type RequestComparisonSection struct {
	Name  string             `json:"name"`
	Diffs []RequestFieldDiff `json:"diffs"`
}

type RequestComparison struct {
	ID           string                     `json:"id"`
	Left         RequestSourceSummary       `json:"left"`
	Right        RequestSourceSummary       `json:"right"`
	MatchLevel   string                     `json:"matchLevel"`
	MatchReasons []string                   `json:"matchReasons,omitempty"`
	Sections     []RequestComparisonSection `json:"sections"`
}

type requestShape struct {
	MessageCount        int
	Roles               string
	ToolCount           int
	ThinkingEnabled     bool
	CacheControlPresent bool
	Available           bool
}

type sourceRecord struct {
	Summary RequestSourceSummary
	Shape   requestShape
}

type Lab struct {
	historyRoot string
	exportDir   string
	mu          sync.Mutex
	comparisons map[string]RequestComparison
}

func New(historyRoot, exportDir string) *Lab {
	return &Lab{
		historyRoot: historyRoot,
		exportDir:   exportDir,
		comparisons: map[string]RequestComparison{},
	}
}

func (lab *Lab) List(query RequestSourceQuery) (RequestSourcePage, error) {
	kind := strings.TrimSpace(query.Kind)
	if kind != kindOfficialMirror && kind != kindLocalProvider {
		return RequestSourcePage{}, controlcenter.NewError("request_source_query_invalid", "kind is invalid")
	}
	limit := controlcenter.ClampLimit(query.Limit, defaultLimit, minLimit, maxLimit)
	offset, err := controlcenter.DecodeOffsetCursor(query.Cursor)
	if err != nil {
		return RequestSourcePage{}, controlcenter.NewError("request_source_query_invalid", "cursor is invalid")
	}
	records, err := lab.loadKind(kind)
	if err != nil {
		return RequestSourcePage{}, err
	}
	filtered := make([]RequestSourceSummary, 0, len(records))
	for _, record := range records {
		if query.Model != "" && !strings.EqualFold(record.Summary.Model, query.Model) {
			continue
		}
		if query.Status != "" && !strings.EqualFold(record.Summary.Status, query.Status) {
			continue
		}
		if query.FromUnixMS > 0 && record.Summary.TimestampUnixMS < query.FromUnixMS {
			continue
		}
		if query.ToUnixMS > 0 && record.Summary.TimestampUnixMS > query.ToUnixMS {
			continue
		}
		filtered = append(filtered, record.Summary)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].TimestampUnixMS == filtered[j].TimestampUnixMS {
			return filtered[i].Ref.ID < filtered[j].Ref.ID
		}
		return filtered[i].TimestampUnixMS > filtered[j].TimestampUnixMS
	})
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := RequestSourcePage{Items: filtered[offset:end]}
	if page.Items == nil {
		page.Items = []RequestSourceSummary{}
	}
	if end < len(filtered) {
		page.NextCursor = controlcenter.EncodeOffsetCursor(end)
	}
	return page, nil
}

func (lab *Lab) Compare(request RequestComparisonRequest) (RequestComparison, error) {
	left, err := lab.lookup(request.Left)
	if err != nil {
		return RequestComparison{}, err
	}
	right, err := lab.lookup(request.Right)
	if err != nil {
		return RequestComparison{}, err
	}
	if left.Summary.Truncated || right.Summary.Truncated {
		return RequestComparison{}, controlcenter.NewError("request_source_truncated", "source is truncated")
	}
	comparison := RequestComparison{
		ID:           opaqueID("cmp", left.Summary.Ref.ID+"|"+right.Summary.Ref.ID),
		Left:         left.Summary,
		Right:        right.Summary,
		MatchLevel:   "explicit",
		MatchReasons: []string{"user_selected"},
		Sections:     diffShapes(left.Shape, right.Shape),
	}
	if left.Summary.Model != "" && strings.EqualFold(left.Summary.Model, right.Summary.Model) {
		comparison.MatchReasons = append(comparison.MatchReasons, "same_model")
	}
	lab.mu.Lock()
	lab.comparisons[comparison.ID] = comparison
	lab.mu.Unlock()
	return comparison, nil
}

func (lab *Lab) Export(comparisonID string) (controlcenter.SanitizedExport, error) {
	lab.mu.Lock()
	comparison, ok := lab.comparisons[strings.TrimSpace(comparisonID)]
	lab.mu.Unlock()
	if !ok {
		return controlcenter.SanitizedExport{}, controlcenter.NewError("comparison_not_found", "comparison not found")
	}
	if err := os.MkdirAll(lab.exportDir, 0o700); err != nil {
		return controlcenter.SanitizedExport{}, controlcenter.WrapError("comparison_export_failed", "create export directory failed", err)
	}
	payload, err := json.MarshalIndent(sanitizedComparison(comparison), "", "  ")
	if err != nil {
		return controlcenter.SanitizedExport{}, controlcenter.WrapError("comparison_export_failed", "marshal comparison failed", err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	name := "comparison-" + comparison.ID + ".json"
	path := filepath.Join(lab.exportDir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return controlcenter.SanitizedExport{}, controlcenter.WrapError("comparison_export_failed", "write export failed", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return controlcenter.SanitizedExport{}, controlcenter.WrapError("comparison_export_failed", "rename export failed", err)
	}
	return controlcenter.SanitizedExport{Path: name, SHA256: digest}, nil
}

func (lab *Lab) Count() int {
	official, _ := lab.loadKind(kindOfficialMirror)
	local, _ := lab.loadKind(kindLocalProvider)
	return len(official) + len(local)
}

func (lab *Lab) lookup(ref RequestSourceRef) (sourceRecord, error) {
	kind := strings.TrimSpace(ref.Kind)
	id := strings.TrimSpace(ref.ID)
	if id == "" || (kind != kindOfficialMirror && kind != kindLocalProvider) {
		return sourceRecord{}, controlcenter.NewError("request_source_not_found", "source not found")
	}
	records, err := lab.loadKind(kind)
	if err != nil {
		return sourceRecord{}, err
	}
	for _, record := range records {
		if record.Summary.Ref.ID == id {
			return record, nil
		}
	}
	return sourceRecord{}, controlcenter.NewError("request_source_not_found", "source not found")
}

func (lab *Lab) loadKind(kind string) ([]sourceRecord, error) {
	if kind == kindOfficialMirror {
		return lab.loadOfficial()
	}
	return lab.loadLocal()
}

func (lab *Lab) loadOfficial() ([]sourceRecord, error) {
	path := filepath.Join(lab.historyRoot, "_debug", "mirror", "official.raw.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, controlcenter.WrapError("request_source_read_failed", "read official mirror failed", err)
	}
	lines := strings.Split(string(raw), "\n")
	records := make([]sourceRecord, 0)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		phase, _ := row["phase"].(string)
		if phase != "" && phase != "request" {
			continue
		}
		model, _ := row["model"].(string)
		truncated, _ := row["truncated"].(bool)
		status := statusFromRow(row)
		ts := unixMSFromRow(row)
		identity := firstNonEmpty(stringFrom(row["exchangeId"]), fmt.Sprintf("line-%d", i))
		shape := extractShape(row["body"])
		records = append(records, sourceRecord{
			Summary: RequestSourceSummary{
				Ref:             RequestSourceRef{Kind: kindOfficialMirror, ID: opaqueID("off", identity)},
				TimestampUnixMS: ts,
				Model:           model,
				Provider:        "official",
				Protocol:        firstNonEmpty(stringFrom(row["method"]), "http"),
				Status:          status,
				ShapeAvailable:  shape.Available,
				Truncated:       truncated,
			},
			Shape: shape,
		})
	}
	return records, nil
}

func (lab *Lab) loadLocal() ([]sourceRecord, error) {
	entries, err := os.ReadDir(lab.historyRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, controlcenter.WrapError("request_source_read_failed", "read history root failed", err)
	}
	records := make([]sourceRecord, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		path := filepath.Join(lab.historyRoot, entry.Name(), "debug", "provider.jsonl")
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row map[string]any
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				continue
			}
			event, _ := row["event"].(string)
			if event != "" && event != "llm_request" && event != "request" {
				continue
			}
			model := firstNonEmpty(stringFrom(row["model"]), stringFrom(row["model_id"]))
			shape := extractShape(row["payload"])
			if !shape.Available {
				shape = extractShape(row["body"])
			}
			identity := entry.Name() + ":" + strconv.Itoa(i)
			records = append(records, sourceRecord{
				Summary: RequestSourceSummary{
					Ref:             RequestSourceRef{Kind: kindLocalProvider, ID: opaqueID("loc", identity)},
					TimestampUnixMS: unixMSFromRow(row),
					Model:           model,
					Provider:        firstNonEmpty(stringFrom(row["provider"]), "local"),
					Protocol:        firstNonEmpty(stringFrom(row["protocol"]), "provider"),
					Status:          statusFromRow(row),
					ShapeAvailable:  shape.Available,
					Truncated:       boolFrom(row["truncated"]),
				},
				Shape: shape,
			})
		}
	}
	return records, nil
}

func extractShape(value any) requestShape {
	object, ok := asObject(value)
	if !ok {
		return requestShape{}
	}
	shape := requestShape{Available: true}
	if messages, ok := object["messages"].([]any); ok {
		shape.MessageCount = len(messages)
		roles := make([]string, 0, len(messages))
		for _, item := range messages {
			msg, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			if role != "" {
				roles = append(roles, role)
			}
		}
		shape.Roles = strings.Join(roles, ",")
	}
	switch tools := object["tools"].(type) {
	case []any:
		shape.ToolCount = len(tools)
	case map[string]any:
		shape.ToolCount = len(tools)
	}
	if thinking, ok := object["thinking"].(map[string]any); ok {
		shape.ThinkingEnabled = boolFrom(thinking["type"]) || boolFrom(thinking["enabled"]) || stringFrom(thinking["type"]) != ""
	}
	if _, ok := object["cache_control"]; ok {
		shape.CacheControlPresent = true
	}
	return shape
}

func diffShapes(left, right requestShape) []RequestComparisonSection {
	return []RequestComparisonSection{
		{
			Name: "messages",
			Diffs: []RequestFieldDiff{
				countDiff("/messages/count", "count", left.MessageCount, right.MessageCount, left.Available, right.Available),
				stringDiff("/messages/roles", "enum", left.Roles, right.Roles),
			},
		},
		{
			Name: "tools",
			Diffs: []RequestFieldDiff{
				countDiff("/tools/count", "count", left.ToolCount, right.ToolCount, left.Available, right.Available),
			},
		},
		{
			Name: "thinking",
			Diffs: []RequestFieldDiff{
				boolDiff("/thinking/enabled", left.ThinkingEnabled, right.ThinkingEnabled),
			},
		},
		{
			Name: "cache_control",
			Diffs: []RequestFieldDiff{
				boolDiff("/cache_control/present", left.CacheControlPresent, right.CacheControlPresent),
			},
		},
	}
}

func countDiff(path, kind string, left, right int, leftOK, rightOK bool) RequestFieldDiff {
	diff := RequestFieldDiff{Path: path, Kind: kind, LeftType: "number", RightType: "number"}
	if leftOK {
		diff.LeftSummary = "count=" + strconv.Itoa(left)
	}
	if rightOK {
		diff.RightSummary = "count=" + strconv.Itoa(right)
	}
	return diff
}

func stringDiff(path, kind, left, right string) RequestFieldDiff {
	return RequestFieldDiff{
		Path:         path,
		Kind:         kind,
		LeftType:     "string",
		RightType:    "string",
		LeftSummary:  presenceSummary(left),
		RightSummary: presenceSummary(right),
		Sensitive:    false,
	}
}

func boolDiff(path string, left, right bool) RequestFieldDiff {
	return RequestFieldDiff{
		Path:         path,
		Kind:         "flag",
		LeftType:     "boolean",
		RightType:    "boolean",
		LeftSummary:  strconv.FormatBool(left),
		RightSummary: strconv.FormatBool(right),
	}
}

func presenceSummary(value string) string {
	if value == "" {
		return "absent"
	}
	return "present:" + strconv.Itoa(len(strings.Split(value, ",")))
}

func sanitizedComparison(comparison RequestComparison) RequestComparison {
	return comparison
}

func opaqueID(prefix, identity string) string {
	sum := sha256.Sum256([]byte(prefix + "|" + identity))
	return prefix + "-" + hex.EncodeToString(sum[:8])
}

func unixMSFromRow(row map[string]any) int64 {
	if value, ok := row["timestampUnixMs"].(float64); ok {
		return int64(value)
	}
	if raw, ok := row["ts"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed.UnixMilli()
		}
	}
	if raw, ok := row["at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

func statusFromRow(row map[string]any) string {
	if value, ok := row["status"].(string); ok && value != "" {
		return value
	}
	if value, ok := row["status"].(float64); ok {
		return strconv.Itoa(int(value))
	}
	if ok, exists := row["ok"].(bool); exists {
		if ok {
			return "ok"
		}
		return "error"
	}
	return ""
}

func asObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case string:
		var object map[string]any
		if json.Unmarshal([]byte(typed), &object) == nil {
			return object, true
		}
	}
	return nil, false
}

func stringFrom(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func boolFrom(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed != "" && typed != "false" && typed != "0"
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
