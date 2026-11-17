//go:build !windows && !darwin

package cursor

import "errors"

func (systemHostEnvironment) HasUser() bool { return false }
func hostUserEnvKind() uint32               { return 0 }
func (systemHostEnvironment) LookupUser() (hostEnvValue, error) {
	return hostEnvValue{}, errors.New("用户环境不支持持久化")
}
func (systemHostEnvironment) WriteUser(hostEnvValue) error {
	return errors.New("用户环境不支持持久化")
}
