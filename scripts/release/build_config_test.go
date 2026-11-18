package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

func TestDarwinCompileKeepsGeneratorsOnHostArchitecture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "build", "darwin", "Taskfile.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Other tasks contain structured commands; inspect only the compile task.
	var document struct {
		Tasks map[string]yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var compile struct {
		Env  map[string]string `yaml:"env"`
		Vars map[string]string `yaml:"vars"`
		Cmds []string          `yaml:"cmds"`
	}
	node := document.Tasks["build:native"]
	if err := node.Decode(&compile); err != nil {
		t.Fatal(err)
	}
	if len(compile.Env) != 0 {
		t.Fatal("target environment leaks into native generator dependencies")
	}
	if len(compile.Cmds) != 2 {
		t.Fatal("compile must be followed by an architecture check")
	}
	for _, host := range []string{"amd64", "arm64"} {
		for _, target := range []string{"amd64", "arm64", ""} {
			t.Run(host+"_to_"+target, func(t *testing.T) {
				values := map[string]string{"ARCH": target, "BUILD_FLAGS": "-tags production", "OUTPUT": "bin/test-app"}
				render := func(source string) string {
					t.Helper()
					parsed, err := template.New("task").Funcs(template.FuncMap{
						"ARCH": func() string { return host },
						"default": func(fallback, value string) string {
							if value == "" {
								return fallback
							}
							return value
						},
					}).Parse(source)
					if err != nil {
						t.Fatal(err)
					}
					var output bytes.Buffer
					if err := parsed.Execute(&output, values); err != nil {
						t.Fatal(err)
					}
					return output.String()
				}
				values["CLANG_ARCH"] = render(compile.Vars["CLANG_ARCH"])
				values["MIN_MACOS"] = render(compile.Vars["MIN_MACOS"])
				effectiveTarget := target
				if effectiveTarget == "" {
					effectiveTarget = host
				}
				clangArch, minVersion := "x86_64", "10.15"
				if effectiveTarget == "arm64" {
					clangArch, minVersion = "arm64", "11.0"
				}
				command := render(compile.Cmds[0])
				for _, expected := range []string{
					"GOOS=darwin GOARCH=" + effectiveTarget + " CGO_ENABLED=1",
					`CGO_CFLAGS="-arch ` + clangArch + " -mmacosx-version-min=" + minVersion + `"`,
					`CGO_LDFLAGS="-arch ` + clangArch + " -mmacosx-version-min=" + minVersion + `"`,
					"MACOSX_DEPLOYMENT_TARGET=" + minVersion,
					`go build -tags production -o "bin/test-app"`,
				} {
					if !strings.Contains(command, expected) {
						t.Fatalf("missing %q in compile command %q", expected, command)
					}
				}
				if got := render(compile.Cmds[1]); got != `lipo -verify_arch `+clangArch+` "bin/test-app"` {
					t.Fatalf("wrong architecture verification: %s", got)
				}
			})
		}
	}
}

func TestMacOSReleaseUsesHostProtocAndAvailableIntelRunner(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "build.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ Run string } `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	script := workflow.Jobs["resolve-matrix"].Steps[0].Run
	matched := 0
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "MATRIX='") {
			continue
		}
		var matrix struct {
			Include []struct {
				Target      string `json:"target"`
				Runner      string `json:"runner"`
				ProtocAsset string `json:"protoc_asset"`
			} `json:"include"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "MATRIX='"), "'")), &matrix); err != nil {
			t.Fatal(err)
		}
		for _, entry := range matrix.Include {
			if !strings.HasPrefix(entry.Target, "darwin-") {
				continue
			}
			matched++
			if entry.Runner != "macos-15-intel" || entry.ProtocAsset != "protoc-29.3-osx-x86_64.zip" {
				t.Fatalf("%s confuses host and target: %+v", entry.Target, entry)
			}
		}
	}
	if matched != 2 {
		t.Fatalf("expected both macOS targets, got %d", matched)
	}
}
