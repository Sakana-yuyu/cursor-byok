//go:build windows

package cursorcapabilities

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	versionDLL              = windows.NewLazySystemDLL("version.dll")
	getFileVersionInfoSizeW = versionDLL.NewProc("GetFileVersionInfoSizeW")
	getFileVersionInfoW     = versionDLL.NewProc("GetFileVersionInfoW")
	verQueryValueW          = versionDLL.NewProc("VerQueryValueW")
)

type vsFixedFileInfo struct {
	Signature        uint32
	StrucVersion     uint32
	FileVersionMS    uint32
	FileVersionLS    uint32
	ProductVersionMS uint32
	ProductVersionLS uint32
	FileFlagsMask    uint32
	FileFlags        uint32
	FileOS           uint32
	FileType         uint32
	FileSubtype      uint32
	FileDateMS       uint32
	FileDateLS       uint32
}

func cursorExecutableVersion(path string) (string, error) {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	size, _, callErr := getFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(file)), 0)
	if size == 0 {
		return "", callErr
	}
	buffer := make([]byte, size)
	ok, _, callErr := getFileVersionInfoW.Call(uintptr(unsafe.Pointer(file)), 0, size, uintptr(unsafe.Pointer(&buffer[0])))
	if ok == 0 {
		return "", callErr
	}
	root, err := windows.UTF16PtrFromString("\\")
	if err != nil {
		return "", err
	}
	var value unsafe.Pointer
	var valueLen uint32
	ok, _, callErr = verQueryValueW.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&valueLen)))
	if ok == 0 || value == nil || valueLen < uint32(unsafe.Sizeof(vsFixedFileInfo{})) {
		return "", callErr
	}
	info := (*vsFixedFileInfo)(value)
	if info.Signature != 0xFEEF04BD {
		return "", fmt.Errorf("invalid version signature")
	}
	return fmt.Sprintf("%d.%d.%d.%d", info.ProductVersionMS>>16, info.ProductVersionMS&0xffff, info.ProductVersionLS>>16, info.ProductVersionLS&0xffff), nil
}
