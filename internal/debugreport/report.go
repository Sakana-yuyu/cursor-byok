// Package debugreport 以脱敏汇总方式读取一次 Cursor 请求的 debug JSONL 证据。
package debugreport

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type RequestReport struct {
	ConversationID    string          `json:"conversationId"`
	RequestID         string          `json:"requestId"`
	ModelCallIDs      []string        `json:"modelCallIds"`
	Effort            ThinkingEffort  `json:"effort"`
	Usage             Usage           `json:"usage"`
	ForwarderReceived StreamLayer     `json:"forwarderReceived"`
	RunSSE            StreamLayer     `json:"runSSE"`
	DeliveryLatency   DeliveryLatency `json:"deliveryLatency"`
	TextComparison    string          `json:"textComparison"`
	TextMatches       bool            `json:"textMatches"`
}

// DeliveryLatency 是 forwarder 接收正文增量到 RunSSE 下发同一条增量的时间差。
// 它只描述本地转发链路，不包含 provider 生成时间和 Cursor 客户端渲染时间。
type DeliveryLatency struct {
	Available bool  `json:"available"`
	FirstMS   int64 `json:"firstMs"`
	LastMS    int64 `json:"lastMs"`
}

type ThinkingEffort struct {
	Runtime   string `json:"runtime"`
	Maximum   string `json:"maximum"`
	Effective string `json:"effective"`
	Provider  string `json:"provider"`
}

type Usage struct {
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	PromptTokensTotal int64  `json:"promptTokensTotal"`
	TTFTMS            int64  `json:"ttftMs"`
	DurationMS        int64  `json:"durationMs"`
	FinishReason      string `json:"finishReason"`
}

type StreamLayer struct {
	TextDeltaCount int    `json:"textDeltaCount"`
	TextBytes      int64  `json:"textBytes"`
	TextSHA256     string `json:"textSha256"`
}

func (report RequestReport) JSON() []byte {
	encoded, _ := json.Marshal(report)
	return encoded
}

func LoadRequestReport(historyRoot, conversationID, requestID string) (RequestReport, error) {
	conversationID = strings.TrimSpace(conversationID)
	requestID = strings.TrimSpace(requestID)
	if !safePathComponent(conversationID) || !safePathComponent(requestID) {
		return RequestReport{}, fmt.Errorf("conversation_id and request_id must be plain path components")
	}
	report := RequestReport{ConversationID: conversationID, RequestID: requestID}
	debugDir := filepath.Join(strings.TrimSpace(historyRoot), conversationID, "debug")
	forwarderReceivedText := newTextDigest()
	runSSEText := newTextDigest()
	forwarderReceivedTimes := make([]time.Time, 0)
	runSSETimes := make([]time.Time, 0)
	modelCallIDs := make(map[string]struct{})

	if err := scanJSONL(filepath.Join(debugDir, "provider.jsonl"), requestID, func(event debugEvent) error {
		if event.ModelCallID != "" {
			modelCallIDs[event.ModelCallID] = struct{}{}
		}
		switch event.Event {
		case "llm_request":
			applyRequestFields(&report, event.Payload)
		case "llm_summary":
			applySummaryFields(&report.Usage, event.Payload)
		}
		return nil
	}); err != nil {
		return RequestReport{}, err
	}
	if err := scanJSONL(filepath.Join(debugDir, "runtime.jsonl"), requestID, func(event debugEvent) error {
		if event.ModelCallID != "" {
			modelCallIDs[event.ModelCallID] = struct{}{}
		}
		if event.Event == "text_delta_forwarded" {
			forwarderReceivedText.addDigest(event.DeltaBytes, event.DeltaSHA256)
			if at, ok := event.timestamp(); ok && event.DeltaBytes > 0 && validSHA256(event.DeltaSHA256) {
				forwarderReceivedTimes = append(forwarderReceivedTimes, at)
			}
		}
		return nil
	}); err != nil {
		return RequestReport{}, err
	}
	if err := scanJSONL(filepath.Join(debugDir, "runsse.jsonl"), requestID, func(event debugEvent) error {
		if event.Event == "send_message" {
			if event.TextDeltaBytes > 0 && validSHA256(event.TextDeltaSHA256) {
				runSSEText.addDigest(event.TextDeltaBytes, event.TextDeltaSHA256)
				if at, ok := event.timestamp(); ok {
					runSSETimes = append(runSSETimes, at)
				}
			} else if text := event.runSSETextDelta(); text != "" {
				sum := sha256.Sum256([]byte(text))
				runSSEText.addDigest(int64(len([]byte(text))), hex.EncodeToString(sum[:]))
			}
		}
		return nil
	}); err != nil {
		return RequestReport{}, err
	}

	report.ModelCallIDs = sortedKeys(modelCallIDs)
	report.ForwarderReceived = forwarderReceivedText.layer()
	report.RunSSE = runSSEText.layer()
	if report.ForwarderReceived.TextDeltaCount == 0 || report.RunSSE.TextDeltaCount == 0 {
		report.TextComparison = "unavailable"
		return report, nil
	}
	report.TextMatches = report.ForwarderReceived.TextDeltaCount == report.RunSSE.TextDeltaCount &&
		report.ForwarderReceived.TextSHA256 == report.RunSSE.TextSHA256
	if report.TextMatches {
		report.TextComparison = "match"
		report.DeliveryLatency = deliveryLatency(forwarderReceivedTimes, runSSETimes)
	} else {
		report.TextComparison = "mismatch"
	}
	return report, nil
}

type debugEvent struct {
	At              string         `json:"at"`
	RequestID       string         `json:"request_id"`
	Event           string         `json:"event"`
	ModelCallID     string         `json:"model_call_id"`
	Payload         map[string]any `json:"payload"`
	Text            string         `json:"text"`
	DeltaBytes      int64          `json:"delta_bytes"`
	DeltaSHA256     string         `json:"delta_sha256"`
	TextDeltaBytes  int64          `json:"text_delta_bytes"`
	TextDeltaSHA256 string         `json:"text_delta_sha256"`
	Message         map[string]any `json:"message"`
}

func (event debugEvent) timestamp() (time.Time, bool) {
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.At))
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

func deliveryLatency(forwarderTimes, runSSETimes []time.Time) DeliveryLatency {
	if len(forwarderTimes) == 0 || len(forwarderTimes) != len(runSSETimes) {
		return DeliveryLatency{}
	}
	first := runSSETimes[0].Sub(forwarderTimes[0]).Milliseconds()
	last := runSSETimes[len(runSSETimes)-1].Sub(forwarderTimes[len(forwarderTimes)-1]).Milliseconds()
	if first < 0 || last < 0 {
		return DeliveryLatency{}
	}
	return DeliveryLatency{Available: true, FirstMS: first, LastMS: last}
}

func (event debugEvent) runSSETextDelta() string {
	interaction := mapValue(event.Message, "interaction_update")
	textDelta := mapValue(interaction, "text_delta")
	return rawStringValue(textDelta, "text")
}

func scanJSONL(path, requestID string, visit func(debugEvent) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open debug file %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		var event debugEvent
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return fmt.Errorf("decode %s line %d: %w", filepath.Base(path), line, err)
		}
		if event.RequestID == requestID {
			if err := visit(event); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return nil
}

func applyRequestFields(report *RequestReport, payload map[string]any) {
	if report == nil {
		return
	}
	knobs := mapValue(payload, "request_knobs")
	report.Effort.Runtime = stringValue(knobs, "runtime_thinking_effort")
	report.Effort.Maximum = stringValue(knobs, "configured_thinking_effort_maximum")
	report.Effort.Effective = stringValue(knobs, "effective_thinking_effort")
	report.Effort.Provider = stringValue(mapValue(payload, "body"), "reasoning_effort")
}

func applySummaryFields(usage *Usage, payload map[string]any) {
	if usage == nil {
		return
	}
	usage.InputTokens = int64Value(payload, "input_tokens")
	usage.OutputTokens = int64Value(payload, "output_tokens")
	usage.PromptTokensTotal = int64Value(payload, "prompt_tokens_total")
	usage.TTFTMS = int64Value(payload, "ttft_ms")
	usage.DurationMS = int64Value(payload, "duration_ms")
	usage.FinishReason = stringValue(payload, "finish_reason")
}

type textDigest struct {
	hash  hashWriter
	count int
	bytes int64
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newTextDigest() *textDigest { return &textDigest{hash: sha256.New()} }

func (digest *textDigest) addDigest(bytes int64, hash string) {
	if digest == nil || bytes <= 0 || !validSHA256(hash) {
		return
	}
	_, _ = digest.hash.Write([]byte(strings.ToLower(strings.TrimSpace(hash))))
	digest.count++
	digest.bytes += bytes
}

func (digest *textDigest) layer() StreamLayer {
	if digest == nil || digest.count == 0 {
		return StreamLayer{}
	}
	return StreamLayer{
		TextDeltaCount: digest.count,
		TextBytes:      digest.bytes,
		TextSHA256:     hex.EncodeToString(digest.hash.Sum(nil)),
	}
}

func mapValue(values map[string]any, key string) map[string]any {
	value, _ := values[key].(map[string]any)
	return value
}

func stringValue(values map[string]any, key string) string {
	return strings.TrimSpace(rawStringValue(values, key))
}

func rawStringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func int64Value(values map[string]any, key string) int64 {
	value, _ := values[key].(float64)
	return int64(value)
}

func validSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `\/`)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	slices.Sort(keys)
	return keys
}
