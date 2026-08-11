package terminalenv

import (
	"encoding/json"
	"testing"
)

func TestStatusJSONUsesFrontendFieldNames(t *testing.T) {
	encoded, err := json.Marshal(Status{
		ShellName:  "PowerShell 7",
		ShellPath:  `C:\\Program Files\\PowerShell\\7\\pwsh.exe`,
		PythonPath: `C:\\Python313\\python.exe`,
	})
	if err != nil {
		t.Fatalf("Marshal(Status) error = %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("Unmarshal(Status JSON) error = %v", err)
	}
	for _, field := range []string{"shellName", "shellPath", "pythonPath"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("Status JSON missing frontend field %q: %s", field, encoded)
		}
	}
	for _, field := range []string{"ShellName", "ShellPath", "PythonPath"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("Status JSON exposes Go field %q: %s", field, encoded)
		}
	}
}
