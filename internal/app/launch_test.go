package app

import (
	"testing"

	"cursor/internal/autostart"
)

func TestHasLaunchFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"no args", nil, false},
		{"empty args", []string{}, false},
		{"unrelated flag", []string{"--verbose", "--port=18080"}, false},
		{"silent flag", []string{autostart.SilentLaunchFlag}, true},
		{"silent flag among others", []string{"--foo", autostart.SilentLaunchFlag, "--bar"}, true},
		{"similar flag no match", []string{"--silent=1", "--silently"}, false},
	}
	for _, tc := range cases {
		if got := hasLaunchFlag(tc.args); got != tc.want {
			t.Fatalf("%s: hasLaunchFlag(%v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}

func TestStartHidden(t *testing.T) {
	cases := []struct {
		name        string
		silentStart bool
		args        []string
		want        bool
	}{
		// 手动启动（无标记）：无论是否开启静默启动都必须显示窗口。
		{"manual launch with silentStart on", true, nil, false},
		{"manual launch with silentStart off", false, nil, false},
		{"manual launch other flags", true, []string{"--verbose"}, false},
		// 自启动（带 --silent）：静默启动设置才生效。
		{"autostart with silentStart on", true, []string{autostart.SilentLaunchFlag}, true},
		{"autostart with silentStart off", false, []string{autostart.SilentLaunchFlag}, false},
	}
	for _, tc := range cases {
		if got := startHidden(tc.silentStart, tc.args); got != tc.want {
			t.Fatalf("%s: startHidden(%v, %v) = %v, want %v", tc.name, tc.silentStart, tc.args, got, tc.want)
		}
	}
}
