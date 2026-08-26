//go:build linux

package cursor

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testCACertPEM = "-----BEGIN CERTIFICATE-----\nYWJj\n-----END CERTIFICATE-----\n"

func TestDetectLinuxCATrustSelectsDistributionPlan(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc", "pki", "ca-trust", "source", "anchors"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := DetectLinuxCATrust(root)
	if err != nil {
		t.Fatalf("DetectLinuxCATrust() error = %v", err)
	}
	wantDir := filepath.Join(root, "etc", "pki", "ca-trust", "source", "anchors")
	if plan.AnchorDir != wantDir {
		t.Fatalf("AnchorDir = %q, want %q", plan.AnchorDir, wantDir)
	}
	if !reflect.DeepEqual(plan.RefreshCommand, []string{"update-ca-trust", "extract"}) {
		t.Fatalf("RefreshCommand = %#v", plan.RefreshCommand)
	}
}

func TestLinuxCAInstalledDetectsContentAndContentChanges(t *testing.T) {
	root := t.TempDir()
	anchorDir := filepath.Join(root, "usr", "local", "share", "ca-certificates")
	if err := os.MkdirAll(anchorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(anchorDir, "cursor.crt"), []byte(testCACertPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := LinuxCATrustPlan{AnchorDir: anchorDir, RefreshCommand: []string{"update-ca-certificates"}}
	installed, err := linuxCAInstalled([]byte(testCACertPEM), plan)
	if err != nil || !installed {
		t.Fatalf("linuxCAInstalled() = %v, %v, want true", installed, err)
	}
	if err := os.WriteFile(filepath.Join(anchorDir, "cursor.crt"), []byte(strings.Replace(testCACertPEM, "YWJj", "eHl6", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err = linuxCAInstalled([]byte(testCACertPEM), plan)
	if err != nil || installed {
		t.Fatalf("linuxCAInstalled() after content change = %v, %v, want false", installed, err)
	}
}

func TestEnsureLinuxCACertInstalledReportsMissingCommand(t *testing.T) {
	root := t.TempDir()
	anchorDir := filepath.Join(root, "usr", "local", "share", "ca-certificates")
	if err := os.MkdirAll(anchorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := LinuxCATrustPlan{AnchorDir: anchorDir, RefreshCommand: []string{"missing-refresh"}}
	_, err := ensureLinuxCACertInstalled([]byte(testCACertPEM), filepath.Join(root, "ca.crt"), plan, LinuxCAOptions{
		RunCommand: func(name string, args ...string) ([]byte, error) {
			return nil, errors.New("executable file not found")
		},
		TerminalAvailable: func() bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "missing-refresh") {
		t.Fatalf("error = %v, want explicit manual sudo command", err)
	}
}

func TestEnsureLinuxCACertInstalledUsesInteractiveSudoWhenTerminalAvailable(t *testing.T) {
	root := t.TempDir()
	anchorDir := filepath.Join(root, "usr", "local", "share", "ca-certificates")
	if err := os.MkdirAll(anchorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := LinuxCATrustPlan{AnchorDir: anchorDir, RefreshCommand: []string{"update-ca-certificates"}}
	var gotName string
	var gotArgs []string
	installed, err := ensureLinuxCACertInstalled([]byte(testCACertPEM), filepath.Join(root, "ca.crt"), plan, LinuxCAOptions{
		RunCommand: func(name string, args ...string) ([]byte, error) {
			gotName = name
			gotArgs = append([]string(nil), args...)
			return nil, nil
		},
		TerminalAvailable: func() bool { return true },
	})
	if err != nil || !installed {
		t.Fatalf("ensureLinuxCACertInstalled() = %v, %v", installed, err)
	}
	if gotName != "sudo" || len(gotArgs) != 1 || gotArgs[0] != "update-ca-certificates" {
		t.Fatalf("command = %q %#v, want sudo update-ca-certificates", gotName, gotArgs)
	}
}

func TestDetectLinuxCATrustSupportsArch(t *testing.T) {
	root := t.TempDir()
	anchorDir := filepath.Join(root, "etc", "ca-certificates", "trust-source", "anchors")
	if err := os.MkdirAll(anchorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := DetectLinuxCATrust(root)
	if err != nil {
		t.Fatalf("DetectLinuxCATrust() error = %v", err)
	}
	if plan.AnchorDir != anchorDir || !reflect.DeepEqual(plan.RefreshCommand, []string{"trust", "extract-compat"}) {
		t.Fatalf("plan = %#v, want Arch trust plan", plan)
	}
}
