package requestlab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cursor/internal/controlcenter"
)

func TestListAndCompareOmitsSecrets(t *testing.T) {
	root := t.TempDir()
	officialDir := filepath.Join(root, "_debug", "mirror")
	if err := os.MkdirAll(officialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	official := `{"ts":"2026-08-21T00:00:00.000Z","phase":"request","model":"gpt-test","method":"POST","exchangeId":"ex-1","body":{"messages":[{"role":"user"},{"role":"assistant"}],"tools":[{}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(officialDir, "official.raw.jsonl"), []byte(official), 0o600); err != nil {
		t.Fatal(err)
	}
	localDir := filepath.Join(root, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "debug")
	if err := os.MkdirAll(localDir, 0o700); err != nil {
		t.Fatal(err)
	}
	local := `{"at":"2026-08-21T00:00:01.000Z","event":"llm_request","model":"gpt-test","request_id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","payload":{"messages":[{"role":"user"}],"thinking":{"type":"enabled"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(localDir, "provider.jsonl"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}

	lab := New(root, filepath.Join(t.TempDir(), "exports"))
	officialPage, err := lab.List(RequestSourceQuery{Kind: "official_mirror", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	localPage, err := lab.List(RequestSourceQuery{Kind: "local_provider", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(officialPage.Items) != 1 || len(localPage.Items) != 1 {
		t.Fatalf("pages = %#v %#v", officialPage, localPage)
	}
	encoded, err := json.Marshal(officialPage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "request_id") || strings.Contains(string(encoded), "aaaaaaaa-bbbb") {
		t.Fatalf("list leaked request id: %s", encoded)
	}
	comparison, err := lab.Compare(RequestComparisonRequest{Left: officialPage.Items[0].Ref, Right: localPage.Items[0].Ref})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.MatchLevel != "explicit" {
		t.Fatalf("matchLevel = %q", comparison.MatchLevel)
	}
	exported, err := lab.Export(comparison.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Path == "" || strings.Contains(exported.Path, string(os.PathSeparator)) || exported.SHA256 == "" {
		t.Fatalf("export = %#v", exported)
	}
}

func TestListRejectsInvalidKind(t *testing.T) {
	lab := New(t.TempDir(), t.TempDir())
	_, err := lab.List(RequestSourceQuery{Kind: "raw"})
	if controlcenter.ErrorCode(err) != "request_source_query_invalid" {
		t.Fatalf("err = %v", err)
	}
}
