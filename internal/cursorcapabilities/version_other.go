//go:build !windows

package cursorcapabilities

import "fmt"

func cursorExecutableVersion(string) (string, error) {
	return "", fmt.Errorf("cursor metadata is only available on Windows")
}
