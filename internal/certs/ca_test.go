package certs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewPersistentManagerGeneratesAndReusesLocalCA(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "data", "ca.crt")
	keyPath := filepath.Join(root, "data", "ca.key")

	first, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("first manager: %v", err)
	}
	firstCert := first.CACertPEM()
	if len(firstCert) == 0 {
		t.Fatal("first manager returned an empty CA certificate")
	}

	for _, path := range []string{certPath, keyPath} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("expected generated file %s: %v", path, statErr)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("generated CA material %s is too permissive: %o", path, info.Mode().Perm())
		}
	}

	second, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("second manager: %v", err)
	}
	if string(second.CACertPEM()) != string(firstCert) {
		t.Fatal("second manager did not reuse the existing CA certificate")
	}
}

// TestIncompleteCAIsRecoverable 验证 cert 残留 key 缺失时返回可修复错误，且
// RepairIncompleteCA 备份残留并重建后，应用可正常复用新 CA。
func TestIncompleteCAIsRecoverable(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "data", "ca.crt")
	keyPath := filepath.Join(root, "data", "ca.key")

	if _, err := NewPersistentManager(certPath, keyPath); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}

	_, err := NewPersistentManager(certPath, keyPath)
	if !IsIncompleteCA(err) {
		t.Fatalf("want IncompleteCAError, got %T: %v", err, err)
	}

	backup, err := RepairIncompleteCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if backup == "" {
		t.Fatal("expected a backup path after repair")
	}
	if _, statErr := os.Stat(backup); statErr != nil {
		t.Fatalf("backup file missing: %v", statErr)
	}
	repaired, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("manager after repair: %v", err)
	}
	if len(repaired.CACertPEM()) == 0 {
		t.Fatal("repaired CA certificate is empty")
	}
}

// TestRepairIncompleteCADoesNothingWhenComplete 验证材料齐全时修复不动作。
func TestRepairIncompleteCADoesNothingWhenComplete(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "data", "ca.crt")
	keyPath := filepath.Join(root, "data", "ca.key")

	if _, err := NewPersistentManager(certPath, keyPath); err != nil {
		t.Fatalf("seed manager: %v", err)
	}
	backup, err := RepairIncompleteCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if backup != "" {
		t.Fatalf("expected no backup when complete, got %q", backup)
	}
}

// TestIncompleteCAErrorMessage 验证错误文案的方向：cert 残留 → 缺失描述为私钥；
// key 残留 → 缺失描述为证书。文案反了会误导用户删错文件。
func TestIncompleteCAErrorMessage(t *testing.T) {
	root := t.TempDir()

	t.Run("cert left key missing", func(t *testing.T) {
		certPath := filepath.Join(root, "cert-left", "ca.crt")
		keyPath := filepath.Join(root, "cert-left", "ca.key")
		if _, err := NewPersistentManager(certPath, keyPath); err != nil {
			t.Fatalf("seed manager: %v", err)
		}
		if err := os.Remove(keyPath); err != nil {
			t.Fatalf("remove key: %v", err)
		}
		_, err := NewPersistentManager(certPath, keyPath)
		if !IsIncompleteCA(err) {
			t.Fatalf("want IncompleteCAError, got %T: %v", err, err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "CA 证书(cert) 存在") || !strings.Contains(msg, "CA 私钥(key) 缺失") {
			t.Fatalf("wrong direction in message: %s", msg)
		}
	})

	t.Run("key left cert missing", func(t *testing.T) {
		certPath := filepath.Join(root, "key-left", "ca.crt")
		keyPath := filepath.Join(root, "key-left", "ca.key")
		if _, err := NewPersistentManager(certPath, keyPath); err != nil {
			t.Fatalf("seed manager: %v", err)
		}
		if err := os.Remove(certPath); err != nil {
			t.Fatalf("remove cert: %v", err)
		}
		_, err := NewPersistentManager(certPath, keyPath)
		if !IsIncompleteCA(err) {
			t.Fatalf("want IncompleteCAError, got %T: %v", err, err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "CA 私钥(key) 存在") || !strings.Contains(msg, "CA 证书(cert) 缺失") {
			t.Fatalf("wrong direction in message: %s", msg)
		}
	})
}

// TestWriteGeneratedCAAtomicNoTempLeftover 验证原子写入后：两文件齐全可解析、
// 目录中不残留 .tmp 临时文件。
func TestWriteGeneratedCAAtomicNoTempLeftover(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	certPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if err := writeGeneratedCA(certPath, keyPath); err != nil {
		t.Fatalf("writeGeneratedCA: %v", err)
	}
	if _, err := NewPersistentManager(certPath, keyPath); err != nil {
		t.Fatalf("manager over generated material: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}

// TestCertKeyMatch 验证 cert/key 配对校验：同源返回 true，换 key 或坏材料返回 false。
func TestCertKeyMatch(t *testing.T) {
	certPEM, keyPEM, err := generateCAPEM()
	if err != nil {
		t.Fatalf("generate CAPEM: %v", err)
	}
	if !certKeyMatch(certPEM, keyPEM) {
		t.Fatal("same-sourced cert/key must match")
	}

	otherCertPEM, _, err := generateCAPEM()
	if err != nil {
		t.Fatalf("generate other CAPEM: %v", err)
	}
	if certKeyMatch(otherCertPEM, keyPEM) {
		t.Fatal("cert from a different key pair must not match")
	}

	if certKeyMatch([]byte("not a pem"), keyPEM) {
		t.Fatal("invalid cert PEM must not match")
	}
}

// TestNewPersistentManagerRegeneratesOnMismatch 回归 v0.0.84 reconnecting bug：
// ca.crt 被单独覆盖（与 ca.key 失配）后，NewPersistentManager 复用路径曾直接带病
// 加载，导致 MITM 签名失败（x509: provided PrivateKey doesn't match parent's
// PublicKey）；现在应备份残留并重新生成一对匹配的 CA。
func TestNewPersistentManagerRegeneratesOnMismatch(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "data", "ca.crt")
	keyPath := filepath.Join(root, "data", "ca.key")

	if _, err := NewPersistentManager(certPath, keyPath); err != nil {
		t.Fatalf("initial manager: %v", err)
	}

	// 模拟「ca.crt 被单独覆盖」：用另一对密钥的 cert 覆盖 ca.crt，保留原 ca.key。
	otherCertPEM, _, err := generateCAPEM()
	if err != nil {
		t.Fatalf("generate other CAPEM: %v", err)
	}
	if err := os.WriteFile(certPath, otherCertPEM, 0o644); err != nil {
		t.Fatalf("overwrite ca.crt: %v", err)
	}

	mgr, err := NewPersistentManager(certPath, keyPath)
	if err != nil {
		t.Fatalf("manager after mismatch: %v", err)
	}
	if len(mgr.CACertPEM()) == 0 {
		t.Fatal("regenerated manager returned empty cert")
	}
	// 新落盘的一对必须匹配，且旧被覆盖的 cert 已被备份（.corrupt-*.bak）。
	onDiskCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read regenerated ca.crt: %v", err)
	}
	onDiskKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read regenerated ca.key: %v", err)
	}
	if !certKeyMatch(onDiskCert, onDiskKey) {
		t.Fatal("regenerated ca.crt/ca.key must match")
	}
	matches, _ := filepath.Glob(filepath.Join(root, "data", "*.corrupt-*.bak"))
	if len(matches) == 0 {
		t.Fatal("expected corrupted CA material to be backed up as .corrupt-*.bak")
	}
}
