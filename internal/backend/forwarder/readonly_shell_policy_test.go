package forwarder

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodePolicyArgs(t *testing.T, argsJSON []byte) map[string]any {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		t.Fatalf("decode rewritten args: %v", err)
	}
	return args
}

func TestApplyReadonlyShellPolicyStripsAdvertisedOptionalArgs(t *testing.T) {
	workspace := []string{`E:\workspace\repo`}
	tests := []struct {
		name        string
		argsJSON    string
		wantCommand string
	}{
		{
			name:        "notify_on_output stripped snake case",
			argsJSON:    `{"command":"git status --short","notify_on_output":{"pattern":"$^","debounce_ms":5000},"block_until_ms":30000}`,
			wantCommand: "git --no-pager --no-optional-locks status --short",
		},
		{
			name:        "notify_on_output stripped camel case",
			argsJSON:    `{"command":"tasklist","notifyOnOutput":{"pattern":"NEVER_MATCH"}}`,
			wantCommand: "tasklist",
		},
		{
			name:        "profile coerced to auto",
			argsJSON:    `{"command":"netstat","profile":"powershell"}`,
			wantCommand: "netstat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, err := applyReadonlyShellPolicy([]byte(tt.argsJSON), workspace)
			if err != nil {
				t.Fatalf("applyReadonlyShellPolicy() error = %v", err)
			}
			args := decodePolicyArgs(t, rewritten)
			if _, found := args["notify_on_output"]; found {
				t.Fatalf("notify_on_output survived the strip: %s", rewritten)
			}
			if _, found := args["notifyOnOutput"]; found {
				t.Fatalf("notifyOnOutput survived the strip: %s", rewritten)
			}
			if profile, found := args["profile"]; found && profile != "auto" {
				t.Fatalf("profile = %v, want auto", profile)
			}
			if got, _ := args["command"].(string); got != tt.wantCommand {
				t.Fatalf("command = %q, want %q", got, tt.wantCommand)
			}
		})
	}
}

func TestApplyReadonlyShellPolicyStripsRequiredPermissionsBeforeWhitelist(t *testing.T) {
	argsJSON := `{"command":"git status","required_permissions":["all"],"notify_on_output":{"pattern":"never"},"profile":"powershell"}`
	rewritten, err := applyReadonlyShellPolicy([]byte(argsJSON), []string{`E:\\workspace\\repo`})
	if err != nil {
		t.Fatalf("applyReadonlyShellPolicy() error = %v", err)
	}
	args := decodePolicyArgs(t, rewritten)
	if _, found := args["required_permissions"]; found {
		t.Fatalf("required_permissions survived the strip: %s", rewritten)
	}
	if _, found := args["notify_on_output"]; found {
		t.Fatalf("notify_on_output survived the strip: %s", rewritten)
	}
	if profile, found := args["profile"]; found && profile != "auto" {
		t.Fatalf("profile = %v, want auto", profile)
	}
	if got := args["command"]; got != "git --no-pager --no-optional-locks status" {
		t.Fatalf("command = %v, want readonly rewrite", got)
	}
}

func TestApplyReadonlyShellPolicyRequiredPermissionsDoesNotBypassWhitelist(t *testing.T) {
	argsJSON := `{"command":"curl https://example.com","required_permissions":["all"]}`
	_, err := applyReadonlyShellPolicy([]byte(argsJSON), nil)
	if err == nil || !strings.Contains(err.Error(), "not in the read-only whitelist") {
		t.Fatalf("applyReadonlyShellPolicy() error = %v, want whitelist rejection", err)
	}
}

func TestApplyReadonlyShellPolicyRemarshalsWhenOnlyArgsMutated(t *testing.T) {
	// 回归：曾经只有 git 词元被改写时才 re-marshal，非 git 命令上的剥离会被静默丢弃。
	argsJSON := `{"command":"tasklist","notify_on_output":{"pattern":"$^"}}`
	rewritten, err := applyReadonlyShellPolicy([]byte(argsJSON), nil)
	if err != nil {
		t.Fatalf("applyReadonlyShellPolicy() error = %v", err)
	}
	if strings.Contains(string(rewritten), "notify_on_output") {
		t.Fatalf("rewritten args still contain notify_on_output: %s", rewritten)
	}
}

func TestParseReadonlyShellCommandQuoting(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantTokens []string
		wantSimple bool
	}{
		{name: "plain", command: "git status --short", wantTokens: []string{"git", "status", "--short"}, wantSimple: true},
		{name: "double quoted path", command: `git diff "path with spaces/file.go"`, wantTokens: []string{"git", "diff", "path with spaces/file.go"}, wantSimple: true},
		{name: "single quoted", command: `git log --format='%h %s'`, wantTokens: []string{"git", "log", "--format=%h %s"}, wantSimple: true},
		{name: "escaped quote inside double quotes", command: `git grep "say \"hi\""`, wantTokens: []string{"git", "grep", `say "hi"`}, wantSimple: true},
		{name: "pipe rejected", command: "git log | head", wantSimple: false},
		{name: "semicolon rejected", command: "git status; git log", wantSimple: false},
		{name: "substitution rejected", command: "git show $(git rev-parse HEAD)", wantSimple: false},
		{name: "redirection rejected", command: "git log > out.txt", wantSimple: false},
		{name: "unterminated quote rejected", command: `git diff "broken`, wantSimple: false},
		{name: "env-assignment prefix rejected", command: "FOO=bar git status", wantSimple: false},
		{name: "empty", command: "  ", wantSimple: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, simple := parseReadonlyShellCommand(tt.command)
			if simple != tt.wantSimple {
				t.Fatalf("parseReadonlyShellCommand(%q) simple = %t, want %t", tt.command, simple, tt.wantSimple)
			}
			if !tt.wantSimple {
				return
			}
			if len(tokens) != len(tt.wantTokens) {
				t.Fatalf("tokens = %#v, want %#v", tokens, tt.wantTokens)
			}
			for index := range tokens {
				if tokens[index] != tt.wantTokens[index] {
					t.Fatalf("tokens[%d] = %q, want %q", index, tokens[index], tt.wantTokens[index])
				}
			}
		})
	}
}

func TestApplyReadonlyShellPolicyQuotedPathSurvivesGitRewrite(t *testing.T) {
	argsJSON := `{"command":"git diff \"path with spaces/file.go\""}`
	rewritten, err := applyReadonlyShellPolicy([]byte(argsJSON), nil)
	if err != nil {
		t.Fatalf("applyReadonlyShellPolicy() error = %v", err)
	}
	args := decodePolicyArgs(t, rewritten)
	want := `git --no-pager --no-optional-locks diff "path with spaces/file.go"`
	if got, _ := args["command"].(string); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestValidateReadonlyGitCommandWhitelist(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantErr string
	}{
		{name: "stash list allowed", command: "git stash list"},
		{name: "stash pop rejected", command: "git stash pop", wantErr: "git stash only allows"},
		{name: "bare stash rejected", command: "git stash", wantErr: "git stash only allows"},
		{name: "grep allowed", command: "git grep -n pattern"},
		{name: "grep pager escape rejected", command: "git grep -O pattern", wantErr: "git grep flag"},
		{name: "grep open-files-in-pager rejected", command: "git grep --open-files-in-pager=vim pattern", wantErr: "git grep flag"},
		{name: "describe allowed", command: "git describe --tags"},
		{name: "shortlog allowed", command: "git shortlog -sn"},
		{name: "cherry allowed", command: "git cherry -v"},
		{name: "count-objects allowed", command: "git count-objects -v"},
		{name: "bare reflog allowed", command: "git reflog"},
		{name: "reflog show allowed", command: "git reflog show"},
		{name: "reflog expire rejected", command: "git reflog expire", wantErr: "git reflog only allows"},
		{name: "config rejected", command: "git config --get user.name", wantErr: "not read-only"},
		{name: "worktree rejected", command: "git worktree list", wantErr: "not read-only"},
		{name: "ls-remote rejected", command: "git ls-remote origin", wantErr: "not read-only"},
		{name: "push rejected", command: "git push origin main", wantErr: "not read-only"},
		{name: "diff output flag rejected", command: "git diff --output=x.patch", wantErr: "flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, simple := parseReadonlyShellCommand(tt.command)
			if !simple {
				t.Fatalf("parseReadonlyShellCommand(%q) unexpectedly not simple", tt.command)
			}
			_, err := validateReadonlyShellCommandTokens(tokens)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateReadonlyShellCommandTokens(%q) error = %v, want nil", tt.command, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateReadonlyShellCommandTokens(%q) error = %v, want containing %q", tt.command, err, tt.wantErr)
			}
		})
	}
}

func TestApplyReadonlyShellPolicyBoundariesUnchanged(t *testing.T) {
	workspace := []string{`E:\workspace\repo`}
	tests := []struct {
		name     string
		argsJSON string
		wantErr  string
	}{
		{name: "workdir escape rejected", argsJSON: `{"command":"git status","working_directory":"C:\\Windows"}`, wantErr: "must stay inside the workspace"},
		{name: "background block_until_ms rejected", argsJSON: `{"command":"git status","block_until_ms":0}`, wantErr: "short foreground window"},
		{name: "non-whitelisted executable rejected", argsJSON: `{"command":"curl https://example.com"}`, wantErr: "not in the read-only whitelist"},
		{name: "pipe rejected", argsJSON: `{"command":"git log | head"}`, wantErr: "single simple command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := applyReadonlyShellPolicy([]byte(tt.argsJSON), workspace)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("applyReadonlyShellPolicy() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRewriteReadonlyShellToolRemovesUnsupportedSchemaFields(t *testing.T) {
	item := json.RawMessage(`{"function":{"name":"Shell","description":"orig","parameters":{"type":"object","properties":{"command":{"type":"string"},"notify_on_output":{"type":"object"},"profile":{"type":"string"},"required_permissions":{"type":"array"}},"required":["command","notify_on_output","required_permissions"]}}}`)
	rewritten, err := rewriteReadonlyShellTool(item)
	if err != nil {
		t.Fatalf("rewriteReadonlyShellTool() error = %v", err)
	}
	var tool map[string]any
	if err := json.Unmarshal(rewritten, &tool); err != nil {
		t.Fatalf("decode rewritten tool: %v", err)
	}
	function := tool["function"].(map[string]any)
	parameters := function["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	if _, found := properties["notify_on_output"]; found {
		t.Fatalf("notify_on_output still present in schema properties")
	}
	if _, found := properties["profile"]; found {
		t.Fatalf("profile still present in schema properties")
	}
	if _, found := properties["required_permissions"]; found {
		t.Fatalf("required_permissions still present in schema properties")
	}
	if _, found := properties["command"]; !found {
		t.Fatalf("command property must survive the rewrite")
	}
	required, _ := parameters["required"].([]any)
	for _, field := range required {
		if field == "notify_on_output" || field == "profile" {
			t.Fatalf("required still lists removed field %v", field)
		}
	}
}
