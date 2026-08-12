package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testExe = "/usr/local/bin/axiom"

func TestRunInstallDryRunWritesNothing(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	var stdout bytes.Buffer
	if err := runInstall(initOptions{dryRun: true}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	settings := filepath.Join(project, ".claude", "settings.local.json")
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", settings)
	}

	out := stdout.String()
	if !strings.Contains(out, settings) {
		t.Errorf("output does not name the target file:\n%s", out)
	}
	for _, want := range []string{"SessionStart", "PostToolUse", "PostToolUseFailure", "SessionEnd"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output is missing %s:\n%s", want, out)
		}
	}
}

// The default target is settings.local.json, not the committable settings.json.
func TestRunInstallDefaultsToProjectLocalSettings(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	var stdout bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(project, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("expected settings.local.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("init wrote the committable settings.json")
	}
	if !strings.Contains(stdout.String(), ".gitignore") {
		t.Errorf("output does not warn about git tracking:\n%s", stdout.String())
	}
}

// Claude Code loads settings at session start, so hooks installed during a
// session do nothing until the next one. A user who is not told that profiles an
// empty log and concludes Axiom does not work.
func TestRunInstallSaysToStartANewSession(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "start a new session") {
		t.Errorf("output does not say to start a new Claude Code session:\n%s", out)
	}
}

// Claude Code reads .claude/settings.local.json at the repository root, so
// installing into the current directory would write a file it never reads.
func TestRunInstallTargetsTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	sub := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}
	t.Chdir(sub)

	var stdout bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("expected settings at the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".claude")); !os.IsNotExist(err) {
		t.Error("init wrote into the subdirectory, where Claude Code would not read it")
	}
}

// The home directory being a repository is one of Claude Code's own exceptions
// to root resolution, alongside directories outside any repository.
func TestProjectRootKeepsTheStartingDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.Mkdir(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	sub := filepath.Join(home, "scratch")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("create subdirectory: %v", err)
	}

	if got := projectRoot(sub); got != sub {
		t.Errorf("projectRoot = %s, want the starting directory %s", got, sub)
	}
}

func TestRunInstallGlobalUsesClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Chdir(t.TempDir())

	var stdout bytes.Buffer
	if err := runInstall(initOptions{global: true}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	if _, ok := doc["hooks"]; !ok {
		t.Fatalf("hooks were not installed:\n%s", data)
	}
	if strings.Contains(stdout.String(), ".gitignore") {
		t.Error("a global install should not warn about git tracking")
	}
}

func TestRunInstallIsIdempotent(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	var first bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &first); err != nil {
		t.Fatalf("first install: %v", err)
	}
	settings := filepath.Join(project, ".claude", "settings.local.json")
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var second bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &second); err != nil {
		t.Fatalf("second install: %v", err)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	if string(before) != string(after) {
		t.Fatalf("second install rewrote the file\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(second.String(), "already installed") {
		t.Errorf("second install did not report a no-op:\n%s", second.String())
	}
}

func TestRunInstallPreservesExistingHooks(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	claudeDir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("create .claude: %v", err)
	}
	settings := filepath.Join(claudeDir, "settings.local.json")
	seeded := `{"permissions":{"allow":["Bash(git *)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/local/bin/guard.sh"}]}]}}`
	if err := os.WriteFile(settings, []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	var stdout bytes.Buffer
	if err := runInstall(initOptions{}, testExe, &stdout); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	for _, want := range []string{"guard.sh", "Bash(git *)", "PreToolUse"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("existing configuration lost %q:\n%s", want, data)
		}
	}
}

func TestRunInstallReportsConflictWithoutWriting(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)

	claudeDir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("create .claude: %v", err)
	}
	settings := filepath.Join(claudeDir, "settings.local.json")
	seeded := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/somewhere/else/axiom","args":["hook","claude"]}]}]}}`
	if err := os.WriteFile(settings, []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	var stdout bytes.Buffer
	err := runInstall(initOptions{}, testExe, &stdout)
	if err == nil {
		t.Fatal("runInstall returned nil, want a conflict error")
	}
	if !strings.Contains(err.Error(), "/somewhere/else/axiom") {
		t.Errorf("error does not name the conflicting binary: %v", err)
	}

	after, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatalf("read settings: %v", readErr)
	}
	if string(after) != seeded {
		t.Fatalf("settings were modified despite the conflict:\n%s", after)
	}
}

func TestParseInitFlags(t *testing.T) {
	t.Parallel()

	ok, err := parseInitFlags([]string{"--global", "--dry-run"})
	if err != nil {
		t.Fatalf("parseInitFlags: %v", err)
	}
	if !ok.global || !ok.dryRun {
		t.Fatalf("options = %+v, want both set", ok)
	}

	for _, args := range [][]string{{"--nope"}, {"extra"}} {
		if _, err := parseInitFlags(args); err == nil || !IsUsage(err) {
			t.Errorf("parseInitFlags(%v) error = %v, want a usage error", args, err)
		}
	}
}

// The test binary itself lives under the temp directory, so this exercises the
// guard that stops a hook from being installed for an executable that
// disappears. The message has to serve both people who hit it: someone running
// an unpacked release archive, and a contributor using `go run`.
func TestAxiomPathRejectsTemporaryBuilds(t *testing.T) {
	t.Parallel()

	_, err := axiomPath()
	if err == nil {
		t.Skip("test binary is not running from a temporary directory")
	}
	for _, want := range []string{"temporary directory", "somewhere permanent", "make build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}
