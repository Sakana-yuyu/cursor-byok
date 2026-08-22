package cursor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func writeDiskKVFixture(t *testing.T, rows map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE cursorDiskKV (key TEXT, value BLOB)"); err != nil {
		t.Fatalf("create cursorDiskKV: %v", err)
	}
	for key, value := range rows {
		if _, err := db.Exec("INSERT INTO cursorDiskKV(key, value) VALUES(?, ?)", key, value); err != nil {
			t.Fatalf("insert fixture row %s: %v", key, err)
		}
	}
	return path
}

func diskKVHexID(t *testing.T, value []byte) string {
	t.Helper()
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func TestReadDiskKVBlobsFromPath(t *testing.T) {
	first := []byte(`{"role":"user","content":"hello"}`)
	second := []byte(`{"role":"assistant","content":[{"type":"text","text":"hi"}]}`)
	firstID := diskKVHexID(t, first)
	secondID := diskKVHexID(t, second)
	path := writeDiskKVFixture(t, map[string][]byte{
		"agentKv:blob:" + firstID:  first,
		"agentKv:blob:" + secondID: second,
		"agentKv:checkpoint:other": []byte("not a blob"),
	})

	values, err := ReadDiskKVBlobsFromPath(path, []string{
		firstID,
		secondID,
		strings.Repeat("0", 64),
		"",
		firstID,
	})
	if err != nil {
		t.Fatalf("ReadDiskKVBlobsFromPath() error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("values = %d entries, want 2: %#v", len(values), values)
	}
	if string(values[firstID]) != string(first) {
		t.Fatalf("first blob = %q", values[firstID])
	}
	if string(values[secondID]) != string(second) {
		t.Fatalf("second blob = %q", values[secondID])
	}
}

func TestReadDiskKVBlobsFromPathMissingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.vscdb")
	if _, err := ReadDiskKVBlobsFromPath(path, []string{strings.Repeat("a", 64)}); err == nil {
		t.Fatal("ReadDiskKVBlobsFromPath() accepted missing db file")
	}
}

func TestReadDiskKVBlobsFromPathNoIDs(t *testing.T) {
	values, err := ReadDiskKVBlobsFromPath("unused", nil)
	if err != nil {
		t.Fatalf("ReadDiskKVBlobsFromPath() error = %v", err)
	}
	if values != nil {
		t.Fatalf("values = %#v, want nil", values)
	}
}
