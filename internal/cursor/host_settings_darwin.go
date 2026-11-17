//go:build darwin

package cursor

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

func (systemHostEnvironment) HasUser() bool { return true }
func hostUserEnvKind() uint32               { return 0 }
func (systemHostEnvironment) LookupUser() (hostEnvValue, error) {
	out, err := exec.Command("launchctl", "getenv", nodeExtraCACertsEnvName).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return hostEnvValue{}, nil
		}
		return hostEnvValue{}, fmt.Errorf("读取 launchctl NODE_EXTRA_CA_CERTS 失败: %w", err)
	}
	// Strip only the one newline emitted by launchctl; spaces/newlines in the value remain.
	out = bytes.TrimSuffix(out, []byte("\n"))
	return hostEnvValue{Present: true, Value: string(out)}, nil
}
func (systemHostEnvironment) WriteUser(v hostEnvValue) error {
	args := []string{"unsetenv", nodeExtraCACertsEnvName}
	if v.Present {
		args = []string{"setenv", nodeExtraCACertsEnvName, v.Value}
	}
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("恢复/设置 launchctl NODE_EXTRA_CA_CERTS 失败: %w: %s", err, out)
	}
	return nil
}
