package debugreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequestReportSummarizesStreamingWithoutExposingPayload(t *testing.T) {
	root := t.TempDir()
	debugDir := filepath.Join(root, "conversation-1", "debug")
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		t.Fatalf("create debug directory: %v", err)
	}
	writeFixture(t, filepath.Join(debugDir, "provider.jsonl"), `
{"at":"2026-08-11T00:00:00Z","request_id":"request-1","event":"llm_request","model_call_id":"call-1","payload":{"body":{"reasoning_effort":"medium"},"request_knobs":{"runtime_thinking_effort":"max","configured_thinking_effort_maximum":"medium","effective_thinking_effort":"medium"}}}
{"at":"2026-08-11T00:00:03Z","request_id":"request-1","event":"llm_summary","model_call_id":"call-1","payload":{"input_tokens":100,"output_tokens":20,"prompt_tokens_total":100,"ttft_ms":1000,"duration_ms":3000,"finish_reason":"stop","secret":"do-not-report"}}
`)
	writeFixture(t, filepath.Join(debugDir, "runtime.jsonl"), `
{"at":"2026-08-11T00:00:01Z","request_id":"request-1","event":"text_delta_forwarded","model_call_id":"call-1","provider_pass":1,"delta_count":1,"delta_bytes":6,"delta_sha256":"5e3235a8346e5a4585f8c58562f5052b8fe26a3bb122e1e96c76784964dfc461"}
{"at":"2026-08-11T00:00:02Z","request_id":"request-1","event":"text_delta_forwarded","model_call_id":"call-1","provider_pass":1,"delta_count":2,"delta_bytes":5,"delta_sha256":"486ea46224d1bb4fb680f34f7c9ad96a8f24ec88be73ea8e5a6c65260e9cb8a7"}
{"at":"2026-08-11T00:00:03Z","request_id":"request-1","event":"provider_pass_done","model_call_id":"call-1","provider_pass":1,"thinking_delta_count":2}
`)
	writeFixture(t, filepath.Join(debugDir, "runsse.jsonl"), `
{"at":"2026-08-11T00:00:01Z","request_id":"request-1","event":"send_message","message":{"interaction_update":{"text_delta":{"text":"hello "}}}}
{"at":"2026-08-11T00:00:02Z","request_id":"request-1","event":"send_message","message":{"interaction_update":{"text_delta":{"text":"world"}}}}
`)

	report, err := LoadRequestReport(root, "conversation-1", "request-1")
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if report.ForwarderReceived.TextDeltaCount != 2 || report.RunSSE.TextDeltaCount != 2 {
		t.Fatalf("text delta counts = forwarder-received:%d runsse:%d", report.ForwarderReceived.TextDeltaCount, report.RunSSE.TextDeltaCount)
	}
	if !report.TextMatches || report.TextComparison != "match" || report.ForwarderReceived.TextSHA256 == "" {
		t.Fatalf("text hash match = %t forwarder-received=%+v runsse=%+v", report.TextMatches, report.ForwarderReceived, report.RunSSE)
	}
	if report.Effort.Runtime != "max" || report.Effort.Maximum != "medium" || report.Effort.Effective != "medium" || report.Effort.Provider != "medium" {
		t.Fatalf("unexpected effort: %+v", report.Effort)
	}
	if report.Usage.InputTokens != 100 || report.Usage.OutputTokens != 20 || report.Usage.TTFTMS != 1000 || report.Usage.DurationMS != 3000 {
		t.Fatalf("unexpected usage: %+v", report.Usage)
	}
	encoded := string(report.JSON())
	if containsSensitiveFixture(encoded) {
		t.Fatalf("report leaked fixture payload: %s", encoded)
	}
}

func TestLoadRequestReportRejectsPathTraversalAndMalformedLines(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadRequestReport(root, "..\\outside", "request-1"); err == nil {
		t.Fatal("expected invalid conversation identifier error")
	}
	debugDir := filepath.Join(root, "conversation-1", "debug")
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		t.Fatalf("create debug directory: %v", err)
	}
	writeFixture(t, filepath.Join(debugDir, "runtime.jsonl"), "not-json\n")
	if _, err := LoadRequestReport(root, "conversation-1", "request-1"); err == nil {
		t.Fatal("expected malformed JSONL error")
	}
}

func TestLoadRequestReportMarksLegacyLogsAsUnavailable(t *testing.T) {
	root := t.TempDir()
	debugDir := filepath.Join(root, "conversation-1", "debug")
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		t.Fatalf("create debug directory: %v", err)
	}
	writeFixture(t, filepath.Join(debugDir, "provider.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"llm_request\"}\n")
	writeFixture(t, filepath.Join(debugDir, "runtime.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"provider_pass_done\"}\n")
	writeFixture(t, filepath.Join(debugDir, "runsse.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"send_message\"}\n")
	report, err := LoadRequestReport(root, "conversation-1", "request-1")
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if report.TextComparison != "unavailable" || report.TextMatches {
		t.Fatalf("legacy comparison = %q matches=%t", report.TextComparison, report.TextMatches)
	}
}

func TestLoadRequestReportMarksDifferentTextAsMismatch(t *testing.T) {
	root := t.TempDir()
	debugDir := filepath.Join(root, "conversation-1", "debug")
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		t.Fatalf("create debug directory: %v", err)
	}
	writeFixture(t, filepath.Join(debugDir, "provider.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"llm_request\"}\n")
	writeFixture(t, filepath.Join(debugDir, "runtime.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"text_delta_forwarded\",\"delta_bytes\":5,\"delta_sha256\":\"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\"}\n")
	writeFixture(t, filepath.Join(debugDir, "runsse.jsonl"), "{\"request_id\":\"request-1\",\"event\":\"send_message\",\"message\":{\"interaction_update\":{\"text_delta\":{\"text\":\"world\"}}}}\n")
	report, err := LoadRequestReport(root, "conversation-1", "request-1")
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if report.TextComparison != "mismatch" || report.TextMatches {
		t.Fatalf("comparison = %q matches=%t", report.TextComparison, report.TextMatches)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func containsSensitiveFixture(value string) bool {
	return strings.Contains(value, "do-not-report") || strings.Contains(value, "hello ") || strings.Contains(value, "world")
}
