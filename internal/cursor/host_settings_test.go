package cursor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func isolateHostSettings(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "CURSOR_BYOK_HOME"} {
		t.Setenv(name, root)
	}
	path, err := resolveCursorSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func writeHostTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func requireHostNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func requireHostSnapshot(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if want && err != nil {
		t.Fatal(err)
	}
	if !want && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot still exists: %v", err)
	}
}
func failHostWrites(t *testing.T, match func(string) bool) {
	t.Helper()
	old := hostAtomicWrite
	hostAtomicWrite = func(path string, data []byte, mode os.FileMode) error {
		if match(path) {
			return errors.New("injected disk failure")
		}
		return old(path, data, mode)
	}
	t.Cleanup(func() { hostAtomicWrite = old })
}
func TestHostSettingsMalformedPreserved(t *testing.T) {
	for _, input := range []string{"{broken", "null", "[]", "/* unfinished", "{\"n\":}"} {
		t.Run(input, func(t *testing.T) {
			path := isolateHostSettings(t)
			data := []byte(input)
			writeHostTest(t, path, data)
			if err := ValidateUserSettings(); err == nil {
				t.Fatal("preflight accepted malformed settings")
			}
			if err := WriteUserProxySettings("http://local"); err == nil {
				t.Fatal("write accepted malformed settings")
			}
			got, err := os.ReadFile(path)
			requireHostNoError(t, err)
			if !bytes.Equal(got, data) {
				t.Fatalf("original changed: %s", got)
			}
			requireHostSnapshot(t, proxySnapshotPath(path), false)
		})
	}
}
func TestHostSettingsRestoreAndRepeatedApply(t *testing.T) {
	path := isolateHostSettings(t)
	original := map[string]any{"http.proxy": "user-proxy", "http.proxySupport": nil, "cursor.general.disableHttp2": false, "unrelated": map[string]any{"number": float64(7)}}
	requireHostNoError(t, writeHostJSON(path, original))
	requireHostNoError(t, WriteUserProxySettings("http://first"))
	requireHostNoError(t, WriteUserProxySettings("http://second"))
	s, err := loadProxySnapshot(path)
	requireHostNoError(t, err)
	if s.Keys["http.proxy"].Original != "user-proxy" {
		t.Fatal("original overwritten")
	}
	// Restoration reads the persisted journal, with no in-memory ownership state.
	requireHostNoError(t, ClearUserProxySettings())
	got, err := readHostSettings(path)
	requireHostNoError(t, err)
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("restore got %#v want %#v", got, original)
	}
	requireHostSnapshot(t, proxySnapshotPath(path), false)
	requireHostNoError(t, ClearUserProxySettings())
}
func TestHostSettingsUserEditsAndNoSnapshot(t *testing.T) {
	path := isolateHostSettings(t)
	raw := []byte("{ // preserve bytes without ownership\n\"http.proxy\":\"mine\",\n}")
	writeHostTest(t, path, raw)
	requireHostNoError(t, ClearUserProxySettings())
	got, err := os.ReadFile(path)
	requireHostNoError(t, err)
	if !bytes.Equal(raw, got) {
		t.Fatal("no snapshot changed settings")
	}
	requireHostNoError(t, WriteUserProxySettings("http://app"))
	settings, err := readHostSettings(path)
	requireHostNoError(t, err)
	settings["http.proxy"] = "user-new"
	delete(settings, "http.proxySupport")
	requireHostNoError(t, writeHostJSON(path, settings))
	requireHostNoError(t, WriteUserProxySettings("http://app-new"))
	requireHostNoError(t, ClearUserProxySettings())
	settings, err = readHostSettings(path)
	requireHostNoError(t, err)
	if settings["http.proxy"] != "user-new" {
		t.Fatal("user proxy lost")
	}
	if _, ok := settings["http.proxySupport"]; ok {
		t.Fatal("user deletion lost")
	}
}
func TestHostSettingsWriteFailuresRetry(t *testing.T) {
	for _, stage := range []string{"snapshot", "apply", "restore"} {
		t.Run(stage, func(t *testing.T) {
			path := isolateHostSettings(t)
			raw := []byte("{\"http.proxy\":\"original\"}")
			writeHostTest(t, path, raw)
			if stage == "restore" {
				requireHostNoError(t, WriteUserProxySettings("http://app"))
			}
			old := hostAtomicWrite
			failHostWrites(t, func(p string) bool {
				if stage == "snapshot" {
					return p == proxySnapshotPath(path)
				}
				return p == path
			})
			var err error
			if stage == "restore" {
				err = ClearUserProxySettings()
			} else {
				err = WriteUserProxySettings("http://app")
			}
			if err == nil {
				t.Fatal("failure not reported")
			}
			if stage != "restore" {
				got, err := os.ReadFile(path)
				requireHostNoError(t, err)
				if !bytes.Equal(got, raw) {
					t.Fatal("failed write changed original")
				}
			}
			requireHostSnapshot(t, proxySnapshotPath(path), stage != "snapshot")
			hostAtomicWrite = old
			if stage == "snapshot" {
				requireHostNoError(t, WriteUserProxySettings("http://app"))
			}
			requireHostNoError(t, ClearUserProxySettings())
			got, err := readHostSettings(path)
			requireHostNoError(t, err)
			if got["http.proxy"] != "original" {
				t.Fatal("retry did not restore")
			}
		})
	}
}
func TestHostSettingsMalformedDuringRestoreRetry(t *testing.T) {
	path := isolateHostSettings(t)
	requireHostNoError(t, WriteUserProxySettings("http://app"))
	applied, err := os.ReadFile(path)
	requireHostNoError(t, err)
	writeHostTest(t, path, []byte("null"))
	if ClearUserProxySettings() == nil {
		t.Fatal("expected parse failure")
	}
	requireHostSnapshot(t, proxySnapshotPath(path), true)
	writeHostTest(t, path, applied)
	requireHostNoError(t, ClearUserProxySettings())
}

type fakeHostEnvironment struct {
	process, user         hostEnvValue
	failProcess, failUser bool
	writes                int
	userEnabled           bool
}

func (f *fakeHostEnvironment) HasUser() bool                        { return f.userEnabled }
func (f *fakeHostEnvironment) LookupProcess() (hostEnvValue, error) { return f.process, nil }
func (f *fakeHostEnvironment) LookupUser() (hostEnvValue, error)    { return f.user, nil }
func (f *fakeHostEnvironment) WriteProcess(v hostEnvValue) error {
	if f.failProcess {
		return errors.New("process failed")
	}
	f.process = v
	f.writes++
	return nil
}
func (f *fakeHostEnvironment) WriteUser(v hostEnvValue) error {
	if f.failUser {
		return errors.New("user failed")
	}
	f.user = v
	f.writes++
	return nil
}
func injectHostEnvironment(t *testing.T, f *fakeHostEnvironment) {
	t.Helper()
	old := hostEnv
	hostEnv = f
	t.Cleanup(func() { hostEnv = old })
}
func TestHostEnvironmentTwoLayersRestore(t *testing.T) {
	for _, original := range []hostEnvValue{{}, {Present: true, Value: ""}, {Present: true, Value: "process-original"}} {
		t.Run(original.Value+string(rune(48+boolInt(original.Present))), func(t *testing.T) {
			path := isolateHostSettings(t)
			user := hostEnvValue{Present: true, Value: "%USERPROFILE%\\original.pem", Kind: 2}
			f := &fakeHostEnvironment{process: original, user: user, userEnabled: true}
			injectHostEnvironment(t, f)
			requireHostNoError(t, SetSystemNodeExtraCACerts("app-ca"))
			requireHostNoError(t, SetSystemNodeExtraCACerts("new-app-ca"))
			// A newly started process inherits the applied user value, but must not rebaseline it.
			restarted := &fakeHostEnvironment{process: hostEnvValue{Present: true, Value: "new-app-ca"}, user: f.user, userEnabled: true}
			hostEnv = restarted
			requireHostNoError(t, SetSystemNodeExtraCACerts("new-app-ca"))
			requireHostNoError(t, ClearSystemNodeExtraCACerts())
			if restarted.process != original || restarted.user != user {
				t.Fatalf("wrong originals: process=%+v user=%+v", restarted.process, restarted.user)
			}
			requireHostSnapshot(t, envSnapshotPath(path), false)
		})
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func TestHostEnvironmentNoSnapshotAndUserChanges(t *testing.T) {
	isolateHostSettings(t)
	f := &fakeHostEnvironment{process: hostEnvValue{Present: true, Value: "mine"}, user: hostEnvValue{Present: true, Value: "user-mine", Kind: hostUserEnvKind()}, userEnabled: true}
	injectHostEnvironment(t, f)
	requireHostNoError(t, ClearSystemNodeExtraCACerts())
	if f.writes != 0 {
		t.Fatal("no snapshot modified env")
	}
	requireHostNoError(t, SetSystemNodeExtraCACerts("app"))
	f.process = hostEnvValue{Present: true, Value: "edited"}
	f.user = hostEnvValue{}
	requireHostNoError(t, SetSystemNodeExtraCACerts("app2"))
	requireHostNoError(t, ClearSystemNodeExtraCACerts())
	if f.process.Value != "edited" || f.user.Present {
		t.Fatal("user edits overwritten")
	}
}
func TestHostEnvironmentFailureAndRetry(t *testing.T) {
	path := isolateHostSettings(t)
	process := hostEnvValue{Present: true, Value: "process"}
	user := hostEnvValue{Present: true, Value: "user", Kind: hostUserEnvKind()}
	f := &fakeHostEnvironment{process: process, user: user, userEnabled: true}
	injectHostEnvironment(t, f)
	old := hostAtomicWrite
	failHostWrites(t, func(p string) bool { return p == envSnapshotPath(path) })
	if SetSystemNodeExtraCACerts("app") == nil || f.writes != 0 {
		t.Fatal("snapshot failure mutated environment")
	}
	hostAtomicWrite = old
	f.failUser = true
	if SetSystemNodeExtraCACerts("app") == nil {
		t.Fatal("expected partial apply failure")
	}
	requireHostSnapshot(t, envSnapshotPath(path), true)
	// Even when user restoration fails, process restoration is attempted independently.
	f.failUser = false
	requireHostNoError(t, SetSystemNodeExtraCACerts("app"))
	f.failUser = true
	if ClearSystemNodeExtraCACerts() == nil {
		t.Fatal("expected restore failure")
	}
	if f.process != process {
		t.Fatal("process restoration not attempted")
	}
	requireHostSnapshot(t, envSnapshotPath(path), true)
	f.failUser = false
	requireHostNoError(t, ClearSystemNodeExtraCACerts())
	if f.process != process || f.user != user {
		t.Fatal("retry restoration failed")
	}
}
func TestHostSettingsLatestValueOwnership(t *testing.T) {
	path := isolateHostSettings(t)
	requireHostNoError(t, WriteUserProxySettings("http://first"))
	requireHostNoError(t, WriteUserProxySettings("http://second"))
	settings, err := readHostSettings(path)
	requireHostNoError(t, err)
	settings["http.proxy"] = "http://first"
	requireHostNoError(t, writeHostJSON(path, settings))
	requireHostNoError(t, ClearUserProxySettings())
	settings, err = readHostSettings(path)
	requireHostNoError(t, err)
	if settings["http.proxy"] != "http://first" {
		t.Fatal("user-selected old value was incorrectly owned")
	}
}
func TestHostEnvironmentLatestValueOwnership(t *testing.T) {
	isolateHostSettings(t)
	f := &fakeHostEnvironment{userEnabled: true}
	injectHostEnvironment(t, f)
	requireHostNoError(t, SetSystemNodeExtraCACerts("first"))
	requireHostNoError(t, SetSystemNodeExtraCACerts("second"))
	f.process = hostEnvValue{Present: true, Value: "first"}
	f.user = hostEnvValue{Present: true, Value: "first", Kind: hostUserEnvKind()}
	requireHostNoError(t, ClearSystemNodeExtraCACerts())
	if f.process.Value != "first" || f.user.Value != "first" {
		t.Fatal("user-selected old environment value was incorrectly owned")
	}
}
func TestHostSettingsCommitFailureRollback(t *testing.T) {
	for _, env := range []bool{false, true} {
		t.Run(map[bool]string{false: "proxy", true: "env"}[env], func(t *testing.T) {
			path := isolateHostSettings(t)
			f := &fakeHostEnvironment{userEnabled: true}
			injectHostEnvironment(t, f)
			snapshot := proxySnapshotPath(path)
			if env {
				snapshot = envSnapshotPath(path)
			}
			old := hostAtomicWrite
			writes := 0
			failHostWrites(t, func(p string) bool {
				if p == snapshot {
					writes++
				}
				return p == snapshot && writes == 2
			})
			var err error
			if env {
				err = SetSystemNodeExtraCACerts("app")
			} else {
				err = WriteUserProxySettings("http://app")
			}
			if err == nil {
				t.Fatal("commit failure was not returned")
			}
			requireHostSnapshot(t, snapshot, true)
			hostAtomicWrite = old
			requireHostNoError(t, RestoreHostSettings())
			if env {
				if f.process.Present || f.user.Present {
					t.Fatal("env rollback failed")
				}
			} else {
				settings, err := readHostSettings(path)
				requireHostNoError(t, err)
				if len(settings) != 0 {
					t.Fatal("proxy rollback failed")
				}
			}
		})
	}
}
func TestHostSettingsInvalidSnapshotFailClosed(t *testing.T) {
	path := isolateHostSettings(t)
	raw := []byte("{\"http.proxy\":\"mine\"}")
	writeHostTest(t, path, raw)
	writeHostTest(t, proxySnapshotPath(path), []byte("null"))
	if WriteUserProxySettings("http://app") == nil || ClearUserProxySettings() == nil {
		t.Fatal("invalid snapshot accepted")
	}
	got, err := os.ReadFile(path)
	requireHostNoError(t, err)
	if !bytes.Equal(got, raw) {
		t.Fatal("invalid snapshot changed settings")
	}
}

func TestHostEnvironmentUserEmptyAndAbsent(t *testing.T) {
	for _, user := range []hostEnvValue{{}, {Present: true, Value: "", Kind: hostUserEnvKind()}} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[user.Present], func(t *testing.T) {
			isolateHostSettings(t)
			f := &fakeHostEnvironment{user: user, userEnabled: true}
			injectHostEnvironment(t, f)
			requireHostNoError(t, SetSystemNodeExtraCACerts("app"))
			requireHostNoError(t, ClearSystemNodeExtraCACerts())
			if f.user != user {
				t.Fatalf("user environment lost presence/value: %+v", f.user)
			}
		})
	}
}

func TestHostEnvironmentProcessOnly(t *testing.T) {
	isolateHostSettings(t)
	f := &fakeHostEnvironment{}
	injectHostEnvironment(t, f)
	requireHostNoError(t, SetSystemNodeExtraCACerts("app"))
	requireHostNoError(t, ClearSystemNodeExtraCACerts())
	if f.process.Present || f.writes != 2 {
		t.Fatal("process-only restore failed")
	}
}
