package forwarder

import (
	"strings"
	"testing"
)

func TestHistoricalLsReplayUsesCompactResultText(t *testing.T) {
	repeatedTreeMarker := "large-directory-entry-"
	content := `{"lsToolCall":{"args":{"path":"E:\\MyProject\\cursor-byok"},"result":{"success":{"directoryTreeRoot":{"absPath":"E:\\MyProject\\cursor-byok","numFiles":768,"childrenFiles":[` +
		strings.Repeat(`{"name":"large-directory-entry-file.txt"},`, 300) + `]}}}}}`
	resultText := `ls success path=E:\MyProject\cursor-byok files=768`

	got := limitProjectedToolResultReplay("Ls", content, resultText, true, true)
	if !strings.Contains(got, `path=E:\MyProject\cursor-byok`) || !strings.Contains(got, "files=768") {
		t.Fatalf("summary metadata missing: %s", got)
	}
	if strings.Contains(got, repeatedTreeMarker) {
		t.Fatalf("full directory tree leaked into historical replay")
	}
	if !strings.Contains(got, "历史 Ls 目录树已从 provider 回放中省略") {
		t.Fatalf("omission notice missing: %s", got)
	}
}

func TestCurrentLsReplayKeepsCurrentResult(t *testing.T) {
	content := `{"lsToolCall":{"args":{"path":"E:\\MyProject"},"result":{"success":{"directoryTreeRoot":{"absPath":"E:\\MyProject","numFiles":1,"childrenFiles":[{"name":"keep-me.txt"}]}}}}}`

	got := limitProjectedToolResultReplay("Ls", content, "ls success path=E:\\MyProject files=1", true, false)
	if !strings.Contains(got, "keep-me.txt") {
		t.Fatalf("current Ls result was compacted: %s", got)
	}
}

func TestHistoricalLsReplayExtractsStructuredSummaryWithoutResultText(t *testing.T) {
	content := `{"lsToolCall":{"args":{"path":"E:\\MyProject"},"result":{"success":{"directoryTreeRoot":{"absPath":"E:\\MyProject","numFiles":2,"childrenFiles":[{"name":"a.txt"},{"name":"b.txt"}]}}}}}`
	content += strings.Repeat(" ", staleToolResultAggressiveThreshold)

	got := limitProjectedToolResultReplay("Ls", content, "", true, true)
	if !strings.Contains(got, `path=E:\MyProject`) || !strings.Contains(got, "files=2") {
		t.Fatalf("structured summary missing: %s", got)
	}
}
