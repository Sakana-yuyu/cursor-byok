package execbridge

import (
	"encoding/base64"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestNormalizeShellProfile 验证 profile 归一化与非法取值拒绝。
func TestNormalizeShellProfile(t *testing.T) {
	valid := map[string]string{
		"":          "auto",
		"auto":      "auto",
		"POWERShell": "powershell",
		" git-bash ": "git-bash",
		"pwsh":      "pwsh",
	}
	for input, want := range valid {
		got, err := normalizeShellProfile(input)
		if err != nil {
			t.Fatalf("normalizeShellProfile(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeShellProfile(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := normalizeShellProfile("fish"); err == nil {
		t.Fatal("normalizeShellProfile(fish) = nil error, want rejection")
	}
}

// TestDecodeShellArgsProfile 验证 decodeShellArgs 接受合法 profile 并拒绝非法值。
func TestDecodeShellArgsProfile(t *testing.T) {
	args, err := decodeShellArgs([]byte(`{"command":"echo hi","profile":"pwsh"}`))
	if err != nil {
		t.Fatalf("decodeShellArgs() error = %v", err)
	}
	if args.Profile != "pwsh" {
		t.Fatalf("Profile = %q, want pwsh", args.Profile)
	}
	_, err = decodeShellArgs([]byte(`{"command":"echo hi","profile":"fish"}`))
	if err == nil {
		t.Fatal("decodeShellArgs(fish) = nil error, want rejection")
	}
}

// TestBuildExplicitShellProfileCommandRoundTrip 验证包装命令在非 Windows 上
// 用 base64 携带原始命令，且命令可无损还原。
func TestBuildExplicitShellProfileCommandRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX round-trip only meaningful on non-Windows")
	}
	command := "echo 'hello world'"
	wrapped, err := buildExplicitShellProfileCommand("git-bash", command)
	if err != nil {
		t.Fatalf("buildExplicitShellProfileCommand() error = %v", err)
	}
	// 包装命令形如 printf '%s' '<b64>' | base64 -d | '.../bash' --noprofile --norc -s
	if !strings.Contains(wrapped, "base64 -d") {
		t.Fatalf("wrapped command missing base64 decode: %s", wrapped)
	}
	start := strings.Index(wrapped, "'") + 1
	end := strings.Index(wrapped[start:], "'") + start
	payload := wrapped[start:end]
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode payload error = %v", err)
	}
	if string(raw) != command {
		t.Fatalf("payload = %q, want %q", string(raw), command)
	}
}

// TestEncodePowerShellCommandUTF16 验证 PowerShell EncodedCommand 是 UTF-16LE base64。
func TestEncodePowerShellCommandUTF16(t *testing.T) {
	encoded := encodePowerShellCommand("echo hi")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	if string(utf16.Decode(units)) != "echo hi" {
		t.Fatalf("decoded = %q, want %q", string(utf16.Decode(units)), "echo hi")
	}
}
