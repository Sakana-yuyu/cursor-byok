package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

type darwinCompileTask struct {
	Env  map[string]string `yaml:"env"`
	Vars map[string]string `yaml:"vars"`
	Cmds []string          `yaml:"cmds"`
}

func loadDarwinCompileTask(t *testing.T) darwinCompileTask {
	t.Helper()
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
	var compile darwinCompileTask
	node := document.Tasks["build:native"]
	if err := node.Decode(&compile); err != nil {
		t.Fatal(err)
	}
	return compile
}

func TestDarwinCompileKeepsGeneratorsOnHostArchitecture(t *testing.T) {
	compile := loadDarwinCompileTask(t)
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
				if got := render(compile.Cmds[1]); got != `lipo "bin/test-app" -verify_arch `+clangArch {
					t.Fatalf("wrong architecture verification: %s", got)
				}
			})
		}
	}
}

// TestDarwinArchitectureVerification executes the Taskfile command with Apple's
// parser: checking only a rendered string cannot validate variable-arity flags.
func TestDarwinArchitectureVerification(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires Apple's clang and lipo; executed by the native macOS CI gate")
	}
	compile := loadDarwinCompileTask(t)
	if len(compile.Cmds) != 2 {
		t.Fatal("missing architecture verification command")
	}
	parsed, err := template.New("verify").Parse(compile.Cmds[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"x86_64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			object := filepath.Join(t.TempDir(), "test object.o")
			clang := exec.Command("xcrun", "clang", "-arch", arch, "-x", "c", "-c", "-o", object, "-")
			clang.Stdin = strings.NewReader("int release_architecture_probe;\n")
			if output, err := clang.CombinedOutput(); err != nil {
				t.Fatalf("create %s Mach-O object: %v\n%s", arch, err, output)
			}
			verify := func(path, expectedArch string) ([]byte, error) {
				t.Helper()
				var command bytes.Buffer
				if err := parsed.Execute(&command, map[string]string{"OUTPUT": path, "CLANG_ARCH": expectedArch}); err != nil {
					t.Fatal(err)
				}
				return exec.Command("/bin/sh", "-c", command.String()).CombinedOutput()
			}
			if output, err := verify(object, arch); err != nil {
				t.Fatalf("verification rejected matching architecture: %v\n%s", err, output)
			}
			wrongArch := "arm64"
			if arch == wrongArch {
				wrongArch = "x86_64"
			}
			if output, err := verify(object, wrongArch); err == nil {
				t.Fatalf("verification accepted wrong architecture %s: %s", wrongArch, output)
			}
			if _, err := verify(object+".missing", arch); err == nil {
				t.Fatal("verification accepted a missing artifact")
			}
		})
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
