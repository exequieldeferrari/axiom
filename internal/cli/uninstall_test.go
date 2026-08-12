package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// install puts Axiom into the settings the given options select, so an
// uninstall test starts from exactly what init produces.
func install(t *testing.T, opts initOptions) {
	t.Helper()

	if err := runInstall(opts, testExe, io.Discard); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
}

func settingsDoc(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings are not valid JSON: %v\n%s", err, data)
	}
	return doc
}

func TestRunUninstallRemovesWhatInitWrote(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	data := t.TempDir()
	t.Setenv("AXIOM_DATA_DIR", data)

	install(t, initOptions{telemetry: true, addr: DefaultAddr})

	var stdout bytes.Buffer
	if err := runRemove(uninstallOptions{}, &stdout); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	settings := filepath.Join(project, ".claude", "settings.local.json")
	if doc := settingsDoc(t, settings); len(doc) != 0 {
		t.Errorf("settings still hold %d keys, want none: %#v", len(doc), doc)
	}

	out := stdout.String()
	for _, want := range []string{
		settings,
		"4 hooks",
		"SessionStart, PostToolUse, PostToolUseFailure, SessionEnd",
		"telemetry configuration",
		"can be deleted",
		// Removing the integration is not a request to throw away the
		// recording, so the user is told where it is instead.
		data,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestRunUninstallDryRunWritesNothing(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())

	install(t, initOptions{})
	settings := filepath.Join(project, ".claude", "settings.local.json")
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var stdout bytes.Buffer
	if err := runRemove(uninstallOptions{dryRun: true}, &stdout); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("the dry run modified %s:\n%s", settings, after)
	}
	out := stdout.String()
	if !strings.Contains(out, "Would remove") {
		t.Errorf("output does not say the removal is hypothetical:\n%s", out)
	}
	if !strings.Contains(out, "{}") {
		t.Errorf("output does not show the resulting document:\n%s", out)
	}
}

// Running uninstall when Axiom is not there is a success: the requested state
// already holds.
func TestRunUninstallWithoutAnInstallation(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())

	var stdout bytes.Buffer
	if err := runRemove(uninstallOptions{}, &stdout); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	if !strings.Contains(stdout.String(), "not installed") {
		t.Errorf("output does not say Axiom is absent:\n%s", stdout.String())
	}

	settings := filepath.Join(project, ".claude", "settings.local.json")
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Errorf("uninstall created %s", settings)
	}
}

func TestRunUninstallIsIdempotent(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())

	install(t, initOptions{})
	if err := runRemove(uninstallOptions{}, io.Discard); err != nil {
		t.Fatalf("first uninstall: %v", err)
	}

	var stdout bytes.Buffer
	if err := runRemove(uninstallOptions{}, &stdout); err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if !strings.Contains(stdout.String(), "not installed") {
		t.Errorf("a second uninstall did not report Axiom as absent:\n%s", stdout.String())
	}
}

// The user's own hooks and settings are the reason this command exists rather
// than instructions to edit JSON by hand.
func TestRunUninstallKeepsUnrelatedConfiguration(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())

	settings := filepath.Join(project, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	seeded := []byte(`{
  "permissions": {"allow": ["Bash(git *)"]},
  "env": {"EDITOR": "vim"},
  "hooks": {
    "PostToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "/usr/local/bin/fmt.sh"}]}
    ]
  }
}`)
	if err := os.WriteFile(settings, seeded, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	install(t, initOptions{telemetry: true, addr: DefaultAddr})
	if err := runRemove(uninstallOptions{}, io.Discard); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	doc := settingsDoc(t, settings)
	if _, ok := doc["permissions"]; !ok {
		t.Error("the user's permissions were removed")
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || env["EDITOR"] != "vim" {
		t.Errorf("env = %#v, want the user's EDITOR kept", doc["env"])
	}
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %#v, want the user's hook kept", doc["hooks"])
	}
	groups, ok := hooks["PostToolUse"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("PostToolUse = %#v, want the user's single group", hooks["PostToolUse"])
	}
	group := groups[0].(map[string]any)
	if group["matcher"] != "Edit" {
		t.Errorf("matcher = %v, want Edit", group["matcher"])
	}
	handlers := group["hooks"].([]any)
	if len(handlers) != 1 || handlers[0].(map[string]any)["command"] != "/usr/local/bin/fmt.sh" {
		t.Errorf("handlers = %#v, want only the user's own", handlers)
	}
}

func TestRunUninstallGlobalUsesClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())
	t.Chdir(t.TempDir())

	install(t, initOptions{global: true})
	if err := runRemove(uninstallOptions{global: true}, io.Discard); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	settings := filepath.Join(home, "settings.json")
	if doc := settingsDoc(t, settings); len(doc) != 0 {
		t.Errorf("settings still hold %d keys, want none: %#v", len(doc), doc)
	}
}

// A project-scoped uninstall must not reach into the user's global settings,
// and neither scope should touch the other's file.
func TestRunUninstallStaysInScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())
	project := t.TempDir()
	t.Chdir(project)

	install(t, initOptions{global: true})
	install(t, initOptions{})

	if err := runRemove(uninstallOptions{}, io.Discard); err != nil {
		t.Fatalf("runRemove: %v", err)
	}

	global := settingsDoc(t, filepath.Join(home, "settings.json"))
	if _, ok := global["hooks"]; !ok {
		t.Errorf("the global installation was removed by a project uninstall: %#v", global)
	}
	local := settingsDoc(t, filepath.Join(project, ".claude", "settings.local.json"))
	if len(local) != 0 {
		t.Errorf("the project installation survived: %#v", local)
	}
}

func TestRunUninstallParsesItsFlags(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	t.Setenv("AXIOM_DATA_DIR", t.TempDir())

	install(t, initOptions{})

	var stdout bytes.Buffer
	if err := runUninstall([]string{"--dry-run"}, &stdout); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}
	if !strings.Contains(stdout.String(), "Would remove") {
		t.Errorf("output does not reflect --dry-run:\n%s", stdout.String())
	}
	if doc := settingsDoc(t, filepath.Join(project, ".claude", "settings.local.json")); doc["hooks"] == nil {
		t.Error("the dry run removed the installation")
	}
}

func TestUninstallFlagErrors(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"unknown flag":        {"--nope"},
		"unexpected argument": {"global"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := runUninstall(args, io.Discard)
			if !IsUsage(err) {
				t.Fatalf("runUninstall(%v) error = %v, want a usage error", args, err)
			}
		})
	}
}
