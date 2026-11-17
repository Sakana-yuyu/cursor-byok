package cursor

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type hostEnvValue struct {
	Present bool   `json:"present"`
	Value   string `json:"value"`
	Kind    uint32 `json:"kind,omitempty"`
}
type hostEnvironment interface {
	LookupProcess() (hostEnvValue, error)
	WriteProcess(hostEnvValue) error
	HasUser() bool
	LookupUser() (hostEnvValue, error)
	WriteUser(hostEnvValue) error
}
type systemHostEnvironment struct{}

func (systemHostEnvironment) LookupProcess() (hostEnvValue, error) {
	value, present := os.LookupEnv(nodeExtraCACertsEnvName)
	return hostEnvValue{Present: present, Value: value}, nil
}
func (systemHostEnvironment) WriteProcess(v hostEnvValue) error {
	if !v.Present {
		return os.Unsetenv(nodeExtraCACertsEnvName)
	}
	return os.Setenv(nodeExtraCACertsEnvName, v.Value)
}

var hostEnv hostEnvironment = systemHostEnvironment{}

type ownedEnvironment struct {
	Original hostEnvValue   `json:"original"`
	Applied  []hostEnvValue `json:"applied"`
	Released bool           `json:"released,omitempty"`
}
type environmentSnapshot struct {
	Owner   string            `json:"owner"`
	Target  string            `json:"target"`
	Process *ownedEnvironment `json:"process"`
	User    *ownedEnvironment `json:"user,omitempty"`
}

func envSnapshotPath(path string) string { return path + ".byok-env-snapshot.json" }
func loadEnvSnapshot(path string) (*environmentSnapshot, error) {
	var s environmentSnapshot
	exists, err := readHostJSON(envSnapshotPath(path), &s)
	if err != nil || !exists {
		return nil, err
	}
	if s.Owner != hostSettingsOwner || s.Target != nodeExtraCACertsEnvName || s.Process == nil || len(s.Process.Applied) == 0 || (s.User != nil && len(s.User.Applied) == 0) {
		return nil, errors.New("宿主环境快照无效，已保留且未修改环境")
	}
	if (s.User != nil) != hostEnv.HasUser() {
		return nil, errors.New("宿主环境快照平台不匹配")
	}
	return &s, nil
}
func envOwned(current hostEnvValue, s *ownedEnvironment) bool {
	if s.Released {
		return false
	}
	for _, v := range s.Applied {
		if current == v {
			return true
		}
	}
	return false
}
func prepareEnv(current hostEnvValue, entry **ownedEnvironment, applied hostEnvValue) {
	if *entry == nil {
		*entry = &ownedEnvironment{Original: current}
	}
	s := *entry
	if len(s.Applied) > 0 && !envOwned(current, s) && current != s.Original {
		s.Released = true
	}
	if !s.Released && !envOwned(applied, s) {
		s.Applied = append(s.Applied, applied)
	}
}

// SetSystemNodeExtraCACerts journals process/user values separately before any mutation.
func SetSystemNodeExtraCACerts(caCertPath string) error {
	hostSettingsMu.Lock()
	defer hostSettingsMu.Unlock()
	caCertPath = strings.TrimSpace(caCertPath)
	if caCertPath == "" {
		return errors.New("CA 证书路径为空")
	}
	path, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	s, err := loadEnvSnapshot(path)
	if err != nil {
		return err
	}
	if s == nil {
		s = &environmentSnapshot{Owner: hostSettingsOwner, Target: nodeExtraCACertsEnvName}
	}
	process, err := hostEnv.LookupProcess()
	if err != nil {
		return err
	}
	applied := hostEnvValue{Present: true, Value: caCertPath}
	prepareEnv(process, &s.Process, applied)
	userApplied := applied
	if hostEnv.HasUser() {
		user, err := hostEnv.LookupUser()
		if err != nil {
			return err
		}
		// REG_SZ is used for newly applied Windows values. Original registry type is retained.
		userApplied.Kind = hostUserEnvKind()
		prepareEnv(user, &s.User, userApplied)
	}
	if err := writeHostJSON(envSnapshotPath(path), s); err != nil {
		return fmt.Errorf("保存环境接管快照失败: %w", err)
	}
	if !s.Process.Released {
		if err := hostEnv.WriteProcess(applied); err != nil {
			return err
		}
	}
	if s.User != nil && !s.User.Released {
		if err := hostEnv.WriteUser(userApplied); err != nil {
			return err
		}
	}
	if !s.Process.Released {
		s.Process.Applied = []hostEnvValue{applied}
	}
	if s.User != nil && !s.User.Released {
		s.User.Applied = []hostEnvValue{userApplied}
	}
	return writeHostJSON(envSnapshotPath(path), s)
}

// ClearSystemNodeExtraCACerts restores only still-owned values; no journal means no-op.
func ClearSystemNodeExtraCACerts() error {
	hostSettingsMu.Lock()
	defer hostSettingsMu.Unlock()
	path, err := resolveCursorSettingsPath()
	if err != nil {
		return err
	}
	s, err := loadEnvSnapshot(path)
	if err != nil || s == nil {
		return err
	}
	var errs []error
	process, err := hostEnv.LookupProcess()
	if err == nil && envOwned(process, s.Process) {
		err = hostEnv.WriteProcess(s.Process.Original)
	}
	if err != nil {
		errs = append(errs, err)
	}
	if s.User != nil {
		user, err := hostEnv.LookupUser()
		if err == nil && envOwned(user, s.User) {
			err = hostEnv.WriteUser(s.User.Original)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return os.Remove(envSnapshotPath(path))
}
