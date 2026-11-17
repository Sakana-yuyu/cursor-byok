//go:build windows

package cursor

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func (systemHostEnvironment) HasUser() bool { return true }
func hostUserEnvKind() uint32               { return registry.SZ }
func (systemHostEnvironment) LookupUser() (hostEnvValue, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return hostEnvValue{}, nil
	}
	if err != nil {
		return hostEnvValue{}, err
	}
	defer key.Close()
	value, kind, err := key.GetStringValue(nodeExtraCACertsEnvName)
	if errors.Is(err, registry.ErrNotExist) {
		return hostEnvValue{}, nil
	}
	if err != nil {
		return hostEnvValue{}, err
	}
	if kind != registry.SZ && kind != registry.EXPAND_SZ {
		return hostEnvValue{}, fmt.Errorf("NODE_EXTRA_CA_CERTS 注册表类型不受支持: %d", kind)
	}
	return hostEnvValue{Present: true, Value: value, Kind: kind}, nil
}
func (systemHostEnvironment) WriteUser(v hostEnvValue) (writeErr error) {
	defer func() {
		if writeErr == nil {
			notifyHostEnvironmentChanged()
		}
	}()
	if !v.Present {
		key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer key.Close()
		err = key.DeleteValue(nodeExtraCACertsEnvName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if v.Kind == registry.EXPAND_SZ {
		return key.SetExpandStringValue(nodeExtraCACertsEnvName, v.Value)
	}
	return key.SetStringValue(nodeExtraCACertsEnvName, v.Value)
}

func notifyHostEnvironmentChanged() {
	// Explorer must refresh its environment before launching a new Cursor process.
	// A hung desktop window must not block settings restoration indefinitely.
	name := windows.StringToUTF16("Environment")
	var result uintptr
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(
		0xffff, 0x001a, 0, uintptr(unsafe.Pointer(&name[0])),
		0x0002, 1000, uintptr(unsafe.Pointer(&result)),
	)
	runtime.KeepAlive(name)
}
