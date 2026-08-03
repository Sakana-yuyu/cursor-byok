package buildinfo

import "strings"

const (
	ReleaseRepo    = "Sakana-yuyu/cursor-byok"
	UpdateBaseURL  = "https://github.com/Sakana-yuyu/cursor-byok/releases/latest/download/"
	ReleasePageURL = "https://github.com/Sakana-yuyu/cursor-byok/releases"
)

// Version is injected at build time from build/config.yml.
var Version = "0.0.0"

// Commit is injected at build time; stays "unknown" when the commit cannot be resolved.
var Commit = "unknown"

func CurrentVersion() string {
	version := strings.TrimSpace(strings.TrimPrefix(Version, "v"))
	if version == "" {
		return "0.0.0"
	}
	return version
}

func CurrentCommit() string {
	commit := strings.TrimSpace(Commit)
	if commit == "" {
		return "unknown"
	}
	return commit
}

func ReleaseTag() string {
	return "v" + CurrentVersion()
}
