package buildinfo

import "strings"

const (
	ReleaseRepo    = "Sakana-yuyu/cursor-byok"
	UpdateBaseURL  = "https://github.com/Sakana-yuyu/cursor-byok/releases/latest/download/"
	ReleasePageURL = "https://github.com/Sakana-yuyu/cursor-byok/releases"
)

// Version is injected at build time from build/config.yml.
var Version = "0.0.0"

func CurrentVersion() string {
	version := strings.TrimSpace(strings.TrimPrefix(Version, "v"))
	if version == "" {
		return "0.0.0"
	}
	return version
}

func ReleaseTag() string {
	return "v" + CurrentVersion()
}
