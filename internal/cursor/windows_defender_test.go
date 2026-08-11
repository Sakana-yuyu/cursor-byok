//go:build windows

package cursor

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestBuildElevatedDefenderExclusionScriptEncodesPath(t *testing.T) {
	powerShellPath := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	exclusionPath := `C:\Users\Administrator\cursor-local-assistant-v2\O'Brien`

	script := buildElevatedDefenderExclusionScript(powerShellPath, exclusionPath)
	if strings.Contains(script, exclusionPath) {
		t.Fatal("outer script leaked the exclusion path instead of passing an encoded inner command")
	}
	if !strings.Contains(script, "-EncodedCommand") {
		t.Fatalf("script = %q, want -EncodedCommand", script)
	}

	encoded := powerShellEncodedArgument(t, script)
	if got, want := decodePowerShellCommand(t, encoded), "$ErrorActionPreference = 'Stop'; try { Add-MpPreference -ExclusionPath "+quotePowerShellLiteral(exclusionPath)+" -ErrorAction Stop; exit 0 } catch { exit 1 }"; got != want {
		t.Fatalf("decoded command = %q, want %q", got, want)
	}
}

func powerShellEncodedArgument(t *testing.T, script string) string {
	t.Helper()
	const marker = "'-EncodedCommand','"
	start := strings.Index(script, marker)
	if start < 0 {
		t.Fatalf("script does not contain encoded command argument: %q", script)
	}
	encoded := script[start+len(marker):]
	end := strings.Index(encoded, "'")
	if end < 0 {
		t.Fatalf("encoded command argument is not closed: %q", script)
	}
	return encoded[:end]
}

func decodePowerShellCommand(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("encoded command byte length = %d, want even", len(raw))
	}
	units := make([]uint16, len(raw)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(raw[index*2:])
	}
	return string(utf16.Decode(units))
}
