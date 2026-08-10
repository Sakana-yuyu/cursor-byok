//go:build !windows

package cursor

// WindowsDefenderExclusionState 描述本地 Windows Defender 排除项引导状态。
// 非 Windows 平台 Supported=false，前端据此跳过引导。
type WindowsDefenderExclusionState struct {
	Supported       bool   `json:"supported"`
	DefenderActive  bool   `json:"defenderActive"`
	AlreadyExcluded bool   `json:"alreadyExcluded"`
	Offered         bool   `json:"offered"`
	Path            string `json:"path"`
}

// IsWindowsDefenderActive 非 Windows 平台始终返回 false。
func IsWindowsDefenderActive() bool { return false }

// IsPathExcludedByDefender 非 Windows 平台始终返回 false。
func IsPathExcludedByDefender(_ string) bool { return false }

// AddDefenderExclusion 非 Windows 平台为 no-op。
func AddDefenderExclusion(_ string) error { return nil }

// QueryDefenderExclusionState 非 Windows 平台返回 Supported=false。
func QueryDefenderExclusionState(path string, offered bool) WindowsDefenderExclusionState {
	return WindowsDefenderExclusionState{Supported: false, Path: path, Offered: offered}
}
