package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempAVExclusionState 把 avExclusionStatePath 临时指向 tempdir 内的文件，
// 测试结束后恢复原值。避免写入真实用户 home 目录。
func withTempAVExclusionState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := avExclusionStatePath
	avExclusionStatePath = func() string { return filepath.Join(dir, "av-exclusion-state.json") }
	t.Cleanup(func() { avExclusionStatePath = orig })
}

func TestReadAVExclusionStateMissingFileReturnsZero(t *testing.T) {
	withTempAVExclusionState(t)
	state := readAVExclusionState()
	if state.Offered || state.Done || state.Dismissed || state.Path != "" {
		t.Fatalf("missing file should return zero state, got %+v", state)
	}
}

func TestWriteThenReadAVExclusionStateRoundTrip(t *testing.T) {
	withTempAVExclusionState(t)
	want := AVExclusionState{Offered: true, Done: true, Path: "C:\\app"}
	if err := writeAVExclusionState(want); err != nil {
		t.Fatalf("writeAVExclusionState: %v", err)
	}
	got := readAVExclusionState()
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestReadAVExclusionStateCorruptJSONReturnsZero(t *testing.T) {
	withTempAVExclusionState(t)
	if err := os.WriteFile(avExclusionStatePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	state := readAVExclusionState()
	if state.Offered || state.Done || state.Dismissed || state.Path != "" {
		t.Fatalf("corrupt file should return zero state, got %+v", state)
	}
}

func TestWriteAVExclusionStateCreatesFile(t *testing.T) {
	withTempAVExclusionState(t)
	state := AVExclusionState{Offered: true, Dismissed: true}
	if err := writeAVExclusionState(state); err != nil {
		t.Fatalf("writeAVExclusionState: %v", err)
	}
	if _, err := os.Stat(avExclusionStatePath()); err != nil {
		t.Fatalf("state file should exist after write: %v", err)
	}
}
